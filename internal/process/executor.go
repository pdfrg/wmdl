package process

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

type ProcessResult int

const (
	ResultProcessed ProcessResult = iota
	ResultSkipped
	ResultNotFound
)

var ErrAbort = errors.New("pipeline aborted by user")

type Phase3Movie struct {
	Title     string
	Year      int
	TMDBID    int
	ProfileID int
	RootPath  string
}

type Phase3Candidate struct {
	Title     string
	Year      int
	MediaType model.MediaType
	TmdbID    int
	TvdbID    int
	Season    int // 0 for movies
}

type Phase3SearchEntry struct {
	Candidate Phase3Candidate
	Top       []quality.ParsedRelease
}

type PickedItem struct {
	Event  db.EventWithTitle
	Season int
	Chosen []quality.ParsedRelease
}

type Executor struct {
	log        zerolog.Logger
	cfg        *config.Config
	db         *db.DB
	prowl      *search.ProwlarrClient
	dl         download.Client
	radarr     *library.RadarrClient
	sonarr     *library.SonarrClient
	lidarr     *library.LidarrClient
	bookClient library.BookClient

	Unfound           []string
	Skipped           []string
	phase3SearchPhase []Phase3SearchEntry
	phase3Movies      []Phase3Movie
	phase3Seasons     []struct {
		SeriesID     int
		SeasonNumber int
		SeriesTitle  string
		MediaType    model.MediaType
	}
	skipRejectedTvdbIDs map[int]struct{}
}

func NewExecutor(logger zerolog.Logger, cfg *config.Config, database *db.DB) *Executor {
	dl := createDownloadClient(logger, cfg)
	radarr := library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey, cfg.Library.Radarr.Timeout)
	sonarr := library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey, cfg.Library.Sonarr.Timeout)
	var lidarr *library.LidarrClient
	if cfg.Library.Lidarr.URL != "" {
		lidarr = library.NewLidarrClient(cfg.Library.Lidarr.URL, cfg.Library.Lidarr.APIKey, cfg.Library.Lidarr.Timeout)
	}
	var bookClient library.BookClient
	if cfg.Library.LazyLibrarian.URL != "" {
		bc := library.NewBookClient(
			cfg.Library.BookBackend,
			cfg.Library.LazyLibrarian.URL,
			cfg.Library.LazyLibrarian.APIKey,
			cfg.Library.LazyLibrarian.Timeout,
		)
		if bc == nil {
			logger.Warn().Str("backend", cfg.Library.BookBackend).Msg("book client: unknown backend, disabling")
		}
		bookClient = bc
	}
	catMap := map[string]int{
		"videos":     cfg.Prowlarr.IndexerIDs.Videos,
		"music":      cfg.Prowlarr.IndexerIDs.Music,
		"anime":      cfg.Prowlarr.IndexerIDs.Anime,
		"ebooks":     cfg.Prowlarr.IndexerIDs.Ebooks,
		"audiobooks": cfg.Prowlarr.IndexerIDs.Audiobooks,
	}
	return &Executor{
		log:                 logger,
		cfg:                 cfg,
		db:                  database,
		prowl:               search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey, cfg.Prowlarr.Timeout, catMap),
		dl:                  dl,
		radarr:              radarr,
		sonarr:              sonarr,
		lidarr:              lidarr,
		bookClient:          bookClient,
		skipRejectedTvdbIDs: make(map[int]struct{}),
	}
}

func createDownloadClient(logger zerolog.Logger, cfg *config.Config) download.Client {
	switch cfg.Downloader.Type {
	case "qbittorrent":
		return download.NewQbittorrentClient(
			cfg.Downloader.Qbittorrent.URL,
			cfg.Downloader.Qbittorrent.Username,
			cfg.Downloader.Qbittorrent.Password,
		)
	case "transmission":
		return download.NewTransmissionClient(
			cfg.Downloader.Transmission.URL,
			cfg.Downloader.Transmission.Username,
			cfg.Downloader.Transmission.Password,
		)
	case "deluge":
		return download.NewDelugeClient(
			cfg.Downloader.Deluge.URL,
			cfg.Downloader.Deluge.Password,
		)
	default:
		logger.Warn().Str("type", cfg.Downloader.Type).Msg("unknown downloader type, downloads disabled")
		return nil
	}
}

type HealthCheckResult struct {
	Critical []string
	Warnings []string
}

func (e *Executor) PreWarmRadarr(ctx context.Context) {
	// Try loading from library_cache first (if fresh enough)
	if e.tryLoadRadarrCache(ctx) {
		return
	}
	movies, err := e.radarr.GetAllMovies(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Radarr")
		return
	}
	e.updateRadarrCache(ctx, movies)
}

func (e *Executor) tryLoadRadarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "radarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var movies []library.RadarrMovie
	for _, c := range caches {
		if c.Source != "radarr" {
			continue
		}
		var m library.RadarrMovie
		if err := json.Unmarshal([]byte(c.Details), &m); err != nil {
			continue
		}
		movies = append(movies, m)
	}
	if len(movies) == 0 {
		return false
	}
	// Set the in-memory cache on the radarr client
	e.radarr.SetAllMovies(movies)
	return true
}

func (e *Executor) updateRadarrCache(ctx context.Context, movies []library.RadarrMovie) {
	var entries []db.LibraryCache
	for _, m := range movies {
		details, _ := json.Marshal(m)
		entries = append(entries, db.LibraryCache{
			Source: "radarr", ExtID: strconv.Itoa(m.TMDBID),
			ArrID: int64(m.ID), ArrTitle: m.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Radarr cache")
	}
}

func (e *Executor) PreWarmSonarr(ctx context.Context) {
	// Try loading from library_cache first (if fresh enough)
	if e.tryLoadSonarrCache(ctx) {
		return
	}
	series, err := e.sonarr.GetAllSeries(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Sonarr")
		return
	}
	e.updateSonarrCache(ctx, series)
}

func (e *Executor) tryLoadSonarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "sonarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var series []library.SonarrSeries
	for _, c := range caches {
		if c.Source != "sonarr" {
			continue
		}
		var s library.SonarrSeries
		if err := json.Unmarshal([]byte(c.Details), &s); err != nil {
			continue
		}
		series = append(series, s)
	}
	if len(series) == 0 {
		return false
	}
	e.sonarr.SetAllSeries(series)
	return true
}

func (e *Executor) updateSonarrCache(ctx context.Context, series []library.SonarrSeries) {
	var entries []db.LibraryCache
	for _, s := range series {
		details, _ := json.Marshal(s)
		entries = append(entries, db.LibraryCache{
			Source: "sonarr", ExtID: strconv.Itoa(s.TVDBID),
			ArrID: int64(s.ID), ArrTitle: s.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Sonarr cache")
	}
}

func (e *Executor) PreWarmLidarr(ctx context.Context) {
	if e.lidarr == nil {
		return
	}
	if e.tryLoadLidarrCache(ctx) {
		return
	}
	artists, err := e.lidarr.GetAllArtists(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Lidarr artists")
		return
	}
	albums, err := e.lidarr.GetAllAlbums(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Lidarr albums")
		return
	}
	e.updateLidarrCache(ctx, artists, albums)
}

func (e *Executor) tryLoadLidarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "lidarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var artists []library.LidarrArtist
	var albums []library.LidarrAlbum
	for _, c := range caches {
		switch c.Source {
		case "lidarr":
			var a library.LidarrArtist
			if err := json.Unmarshal([]byte(c.Details), &a); err != nil {
				continue
			}
			artists = append(artists, a)
		case "lidarr-album":
			var a library.LidarrAlbum
			if err := json.Unmarshal([]byte(c.Details), &a); err != nil {
				continue
			}
			albums = append(albums, a)
		}
	}
	if len(artists) == 0 {
		return false
	}
	e.lidarr.SetAllArtists(artists)
	e.lidarr.SetAllAlbums(albums)
	return true
}

func (e *Executor) updateLidarrCache(ctx context.Context, artists []library.LidarrArtist, albums []library.LidarrAlbum) {
	var entries []db.LibraryCache
	for _, a := range artists {
		details, _ := json.Marshal(a)
		entries = append(entries, db.LibraryCache{
			Source: "lidarr", ExtID: a.MBID,
			ArrID: int64(a.ID), ArrTitle: a.ArtistName, Details: string(details),
		})
	}
	for _, a := range albums {
		details, _ := json.Marshal(a)
		entries = append(entries, db.LibraryCache{
			Source: "lidarr-album", ExtID: a.ForeignAlbumID,
			ArrID: int64(a.ID), ArrTitle: a.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Lidarr cache")
	}
}

func (e *Executor) PreWarmBookClient(ctx context.Context) {
	if e.bookClient == nil {
		return
	}
	if e.tryLoadBookClientCache(ctx) {
		return
	}
	books, err := e.bookClient.GetAllBooks(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm LazyLibrarian")
		return
	}
	e.updateBookClientCache(ctx, books)
}

func (e *Executor) tryLoadBookClientCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "book-client")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var books []model.BookStatus
	for _, c := range caches {
		if c.Source != "book-client" {
			continue
		}
		var b model.BookStatus
		if err := json.Unmarshal([]byte(c.Details), &b); err != nil {
			continue
		}
		// Deduplicate by BookID since the same book may have ISBN13 and ASIN entries
		found := false
		for _, existing := range books {
			if existing.BookID == b.BookID {
				found = true
				break
			}
		}
		if !found {
			books = append(books, b)
		}
	}
	if len(books) == 0 {
		return false
	}
	e.bookClient.SetAllBooks(books)
	return true
}

func (e *Executor) updateBookClientCache(ctx context.Context, books []model.BookStatus) {
	var entries []db.LibraryCache
	for _, b := range books {
		bookID, _ := strconv.Atoi(b.BookID)
		details, _ := json.Marshal(b)
		// Index by ISBN13
		if b.Isbn != "" {
			entries = append(entries, db.LibraryCache{
				Source: "book-client", ExtID: b.Isbn,
				ArrID: int64(bookID), ArrTitle: b.Title, Details: string(details),
			})
		}
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving LazyLibrarian cache")
	}
	// Set in-memory cache on the client
	e.bookClient.SetAllBooks(books)
}

func (e *Executor) HealthCheck(ctx context.Context, checkProwlarr, checkDownloader, checkLibrary bool) HealthCheckResult {
	var result HealthCheckResult

	if checkProwlarr {
		if err := e.prowl.Ping(ctx); err != nil {
			result.Critical = append(result.Critical, fmt.Sprintf("Prowlarr: %v", err))
		}
	}
	if checkDownloader && e.dl != nil {
		if err := e.dl.Ping(ctx); err != nil {
			result.Critical = append(result.Critical, fmt.Sprintf("Downloader (%s): %v", e.cfg.Downloader.Type, err))
		}
	}
	if checkLibrary {
		if e.radarr != nil {
			if err := e.radarr.Ping(ctx); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Radarr: %v", err))
			}
		}
		if e.sonarr != nil {
			if err := e.sonarr.Ping(ctx); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Sonarr: %v", err))
			}
		}
		if e.lidarr != nil {
			if err := e.lidarr.Ping(ctx); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Lidarr: %v", err))
			}
		}
		if e.bookClient != nil {
			if err := e.bookClient.Ping(ctx); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("LazyLibrarian: %v", err))
			}
		}
	}

	return result
}

type SearchResult struct {
	Event    db.EventWithTitle
	Season   int
	Stripped string
	Top      []quality.ParsedRelease
	Error    error
}

func (e *Executor) ProcessApproved(ctx context.Context, evt db.EventWithTitle) error {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		return sr.Error
	}
	return e.PresentResult(ctx, sr)
}

func (e *Executor) SearchEvent(ctx context.Context, evt db.EventWithTitle) *SearchResult {
	title := evt.Title

	season := quality.ParseSeasonNumber(title.Title)
	stripped := quality.StripSeason(title.Title)

	e.log.Info().Str("title", title.Title).Int("year", title.Year).Int("season", season).Str("stripped", stripped).Msg("processing title")

	releases, err := e.searchRelease(ctx, title, stripped, season)
	if err != nil {
		return &SearchResult{Event: evt, Error: fmt.Errorf("searching releases: %w", err)}
	}
	if len(releases) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s (%d)", title.Title, title.Year))
		return &SearchResult{Event: evt, Season: season, Stripped: stripped}
	}

	prefs := buildQualityPrefs(e.cfg, title.MediaType)
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

	return &SearchResult{
		Event:    evt,
		Season:   season,
		Stripped: stripped,
		Top:      top,
	}
}

func (e *Executor) SearchAll(ctx context.Context, events []db.EventWithTitle) []*SearchResult {
	e.log.Info().Msgf("Searching %d items...", len(events))
	results := make([]*SearchResult, 0, len(events))
	for i, ev := range events {
		e.log.Info().Str("title", ev.Title.Title).Msgf("[%d/%d] searching", i+1, len(events))
		sr := e.SearchEvent(ctx, ev)
		if sr.Error != nil {
			e.log.Warn().Err(sr.Error).Str("title", ev.Title.Title).Msg("error searching")
			sr.Error = nil // don't fail the whole batch for one error
		} else if len(sr.Top) == 0 {
			e.log.Info().Str("title", ev.Title.Title).Msg("no results")
		} else {
			e.log.Info().Int("count", len(sr.Top)).Str("title", ev.Title.Title).Msg("results found")
		}
		results = append(results, sr)
	}
	return results
}

func (e *Executor) presentPicker(ctx context.Context, sr *SearchResult) ([]quality.ParsedRelease, error) {
	if len(sr.Top) == 0 {
		return nil, nil
	}
	sel := NewSelector(sr.Event.Title.Title, sr.Top)
	chosen, err := sel.Run()
	if err != nil {
		return nil, err
	}
	return chosen, nil
}

func (e *Executor) addToClient(ctx context.Context, evt db.EventWithTitle, chosen []quality.ParsedRelease) {
	if e.dl == nil {
		return
	}
	title := evt.Title
	if title == nil {
		e.log.Warn().Msg("addToClient called with nil title")
		return
	}
	category := e.cfg.Downloader.Categories.Movies
	switch title.MediaType {
	case model.MediaTypeTV:
		category = e.cfg.Downloader.Categories.TV
	case model.MediaTypeAnime:
		category = e.cfg.Downloader.Categories.Anime
	}

	var releaseEventID int64
	if evt.Event != nil {
		releaseEventID = evt.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Int("score", release.Score).Msg("selected")
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		var torrentID string
		if uri != "" {
			tid, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category))
			if err != nil {
				e.log.Warn().Err(err).Msg("direct add failed")
			} else {
				torrentID = tid
				e.log.Info().Str("client", e.cfg.Downloader.Type).Str("category", category).Msg("added to download client")
			}
		}

		dl := &model.Download{
			TitleID:         title.ID,
			ReleaseEventID:  releaseEventID,
			Quality:         fmt.Sprintf("%dp", release.Resolution),
			SourceType:      release.Source,
			Codec:           release.Codec,
			InfoHash:        release.InfoHash,
			Category:        category,
			Status:          model.DownloadAdded,
			ClientTorrentID: torrentID,
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("creating download record")
		}
	}
}

func (e *Executor) handleUpgrade(ctx context.Context, evt db.EventWithTitle) {
	title := evt.Title
	if evt.Event.Notes != "" && strings.Contains(evt.Event.Notes, "upgrade:") {
		oldDL, err := e.db.GetDownloadByTitleID(ctx, title.ID)
		if err == nil && oldDL != nil {
			if err := e.db.UpdateDownloadStatus(ctx, oldDL.ID, model.DownloadUpgraded); err != nil {
				e.log.Warn().Err(err).Msg("marking old download as upgraded")
			} else {
				e.log.Info().Msg("marked previous download as upgraded")
			}
		}
	}
}

func (e *Executor) markDownloaded(ctx context.Context, evt db.EventWithTitle) {
	if err := e.db.UpdateReleaseEventStatus(ctx, evt.Event.ID, model.StatusDownloaded); err != nil {
		e.log.Warn().Err(err).Msg("updating event status")
	}
}

// grabViaProwlarr sends chosen releases to Prowlarr's grab API instead of
// adding them to a locally-configured download client. Prowlarr handles
// routing to its own configured download client.
func (e *Executor) grabViaProwlarr(ctx context.Context, evt db.EventWithTitle, chosen []quality.ParsedRelease) {
	title := evt.Title
	if title == nil {
		e.log.Warn().Msg("grabViaProwlarr called with nil title")
		return
	}

	var releaseEventID int64
	if evt.Event != nil {
		releaseEventID = evt.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Int("score", release.Score).Int("indexer", release.IndexerID).Msg("grabbing via Prowlarr")

		if err := e.prowl.Grab(ctx, release.IndexerID, release.Guid); err != nil {
			e.log.Warn().Err(err).Str("release", release.RawTitle).Msg("prowlarr grab failed")
			continue
		}

		dl := &model.Download{
			TitleID:        title.ID,
			ReleaseEventID: releaseEventID,
			Quality:        fmt.Sprintf("%dp", release.Resolution),
			SourceType:     release.Source,
			Codec:          release.Codec,
			InfoHash:       release.InfoHash,
			Status:         model.DownloadAdded,
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("creating download record")
		}
	}
}

// ProcessGrabResults shows pickers for pre-searched results and grabs
// via Prowlarr's API. Intended for prowlarr-grab mode (no download client,
// no library management).
func (e *Executor) ProcessGrabResults(ctx context.Context, results []*SearchResult) {
	for _, sr := range results {
		if len(sr.Top) == 0 {
			continue
		}
		chosen, err := e.presentPicker(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("title", sr.Event.Title.Title).Msg("picker error")
			continue
		}
		if len(chosen) == 0 {
			e.log.Info().Str("title", sr.Event.Title.Title).Msg("skipped")
			label := sr.Event.Title.Title
			if sr.Event.Title.Year > 0 {
				label = fmt.Sprintf("%s (%d)", label, sr.Event.Title.Year)
			}
			e.Skipped = append(e.Skipped, label)
			continue
		}
		e.grabViaProwlarr(ctx, sr.Event, chosen)
		e.handleUpgrade(ctx, sr.Event)
		e.markDownloaded(ctx, sr.Event)
	}
}

// SearchAndGrabOne is the interactive-mode equivalent of SearchAndPickOne
// for prowlarr-grab mode: search, show picker, grab via Prowlarr, mark.
func (e *Executor) SearchAndGrabOne(ctx context.Context, evt db.EventWithTitle) *PickedItem {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		e.log.Warn().Err(sr.Error).Str("title", evt.Title.Title).Msg("error searching")
		return nil
	}
	if len(sr.Top) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("no search results found, skipping")
		return nil
	}
	chosen, err := e.presentPicker(ctx, sr)
	if errors.Is(err, ErrAbort) {
		e.log.Info().Msg("pipeline aborted by user")
		return nil
	}
	if err != nil {
		e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("picker error")
		return nil
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		return nil
	}
	e.grabViaProwlarr(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)
	e.markDownloaded(ctx, evt)
	// No PickedItem returned — prowlarr-grab mode has no library step
	return nil
}

func (e *Executor) PresentResult(ctx context.Context, sr *SearchResult) error {
	if sr.Error != nil {
		return sr.Error
	}
	evt := sr.Event

	chosen, err := e.presentPicker(ctx, sr)
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		return nil
	}

	e.addToClient(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)

	if err := e.addToLibrary(ctx, evt, sr.Season); err != nil {
		e.log.Warn().Err(err).Msg("library add failed")
	}

	e.markDownloaded(ctx, evt)
	return nil
}

// SearchAndPickAll is Phase 1 for batch mode: search all items, present picker
// for each, download chosen releases. Returns list of picked items for library
// processing. Deprecated — use SearchAll + PickResults for unified batch flow.
func (e *Executor) SearchAndPickAll(ctx context.Context, events []db.EventWithTitle) []PickedItem {
	return e.PickResults(ctx, e.SearchAll(ctx, events))
}

// SearchAndPickOne is Phase 1 for interactive mode: search one item, present
// picker, download. Returns a PickedItem for library processing.
func (e *Executor) SearchAndPickOne(ctx context.Context, evt db.EventWithTitle) *PickedItem {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		e.log.Warn().Err(sr.Error).Str("title", evt.Title.Title).Msg("error searching")
		return nil
	}
	if len(sr.Top) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("no search results found, skipping")
		return nil
	}
	chosen, err := e.presentPicker(ctx, sr)
	if errors.Is(err, ErrAbort) {
		e.log.Info().Msg("pipeline aborted by user")
		return nil
	}
	if err != nil {
		e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("picker error")
		return nil
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		e.handleSkipLibrary(ctx, evt, sr.Season)
		return nil
	}
	e.addToClient(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)
	e.markDownloaded(ctx, evt)
	return &PickedItem{Event: evt, Season: sr.Season, Chosen: chosen}
}

// PickResults takes pre-searched results and presents pickers, downloads,
// and marks as downloaded. Returns list of picked items for library processing.
func (e *Executor) PickResults(ctx context.Context, results []*SearchResult) []PickedItem {
	var picked []PickedItem
	for _, sr := range results {
		if len(sr.Top) == 0 {
			continue
		}
		chosen, err := e.presentPicker(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("title", sr.Event.Title.Title).Msg("picker error")
			continue
		}
		if len(chosen) == 0 {
			e.log.Info().Str("title", sr.Event.Title.Title).Msg("skipped")
			label := sr.Event.Title.Title
			if sr.Event.Title.Year > 0 {
				label = fmt.Sprintf("%s (%d)", label, sr.Event.Title.Year)
			}
			e.Skipped = append(e.Skipped, label)
			e.handleSkipLibrary(ctx, sr.Event, sr.Season)
			continue
		}
		e.addToClient(ctx, sr.Event, chosen)
		e.handleUpgrade(ctx, sr.Event)
		e.markDownloaded(ctx, sr.Event)
		picked = append(picked, PickedItem{Event: sr.Event, Season: sr.Season, Chosen: chosen})
	}
	return picked
}

func (e *Executor) handleSkipLibrary(ctx context.Context, evt db.EventWithTitle, season int) {
	mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
	if mode != "full" {
		return
	}
	if evt.Title.MediaType == model.MediaTypeTV || evt.Title.MediaType == model.MediaTypeAnime {
		if tvdbID := evt.Title.TvdbID; tvdbID > 0 {
			existing, _ := e.sonarr.Exists(ctx, tvdbID)
			if existing == nil {
				if lookup, err := e.sonarr.Lookup(ctx, tvdbID); err == nil && lookup != nil {
					e.log.Info().Msgf("  Sonarr: %s (%d)", lookup.Title, lookup.Year)
					if lookup.Overview != "" {
						for _, line := range formatOverview(lookup.Overview, 72) {
							e.log.Info().Msg(line)
						}
					}
				}
				if promptYesNo(ctx, fmt.Sprintf("  Add %s to Sonarr anyway?", evt.Title.Title)) {
					searchNow := false
					monitorTarget := false
					if promptYesNo(ctx, "    Monitor item? (Sonarr will search and manage downloads)") {
						searchNow = true
						monitorTarget = true
					}
					beforeSeasons := len(e.phase3Seasons)
					if err := e.addToSonarr(ctx, evt, tvdbID, season, true, searchNow, monitorTarget); err != nil {
						e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("adding to Sonarr after skip")
					} else {
						e.phase3Seasons = e.phase3Seasons[:beforeSeasons]
					}
				} else {
					e.skipRejectedTvdbIDs[tvdbID] = struct{}{}
				}
			}
		}
	} else if evt.Title.MediaType == model.MediaTypeMovie {
		if tmdbID := evt.Title.TmdbID; tmdbID > 0 {
			existing, _ := e.radarr.Exists(ctx, tmdbID)
			if existing == nil {
				if lookup, err := e.radarr.Lookup(ctx, tmdbID); err == nil && lookup != nil {
					e.log.Info().Msgf("  Radarr: %s (%d)", lookup.Title, lookup.Year)
					if lookup.Overview != "" {
						for _, line := range formatOverview(lookup.Overview, 72) {
							e.log.Info().Msg(line)
						}
					}
				}
				if promptYesNo(ctx, fmt.Sprintf("  Add %s to Radarr anyway?", evt.Title.Title)) {
					searchNow := false
					if promptYesNo(ctx, "    Monitor item? (Radarr will search and manage downloads)") {
						searchNow = true
					}
					if err := e.addToRadarr(ctx, evt, true, searchNow); err != nil {
						e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("adding to Radarr after skip")
					}
				}
			}
		}
	}
}

func (e *Executor) handleSkipLibraryMusic(ctx context.Context, ae db.EventWithAlbum) {
	if e.lidarr == nil {
		return
	}
	mode := e.cfg.MediaTypeMode(model.MediaTypeMusic)
	if mode != "full" {
		return
	}
	artistMbid := ae.Artist.MBID
	if artistMbid == "" {
		return
	}
	existing, err := e.lidarr.GetArtist(ctx, artistMbid)
	if err != nil {
		e.log.Warn().Err(err).Str("artist", ae.Artist.Name).Msg("lidarr check error during skip")
		return
	}
	if existing != nil {
		e.log.Info().Str("artist", ae.Artist.Name).Int("lidarr_id", existing.ID).Msg("artist already in Lidarr")
		return
	}
	if !promptYesNo(ctx, fmt.Sprintf("  Add %s to Lidarr anyway?", ae.Artist.Name)) {
		return
	}
	qualProfileID, err := e.lidarr.ResolveQualityProfileID(ctx, e.cfg.Library.Lidarr.QualityProfile)
	if err != nil {
		e.log.Warn().Err(err).Msg("resolving quality profile for Lidarr")
		return
	}
	metaProfileID, err := e.lidarr.ResolveMetadataProfileID(ctx, e.cfg.Library.Lidarr.MetadataProfile)
	if err != nil {
		e.log.Warn().Err(err).Msg("resolving metadata profile for Lidarr")
		return
	}
	rootFolder := e.cfg.Library.Lidarr.RootFolder
	if rootFolder == "" {
		e.log.Warn().Msg("lidarr root_folder not configured, skipping")
		return
	}
	monitor := e.cfg.Library.Lidarr.Monitor
	if monitor == "" {
		monitor = "all"
	}
	added, err := e.lidarr.AddArtist(ctx, artistMbid, ae.Artist.Name, library.AddArtistOptions{
		Monitored:         true,
		MonitorNewAlbums:  e.cfg.Library.Lidarr.MonitorNewAlbums,
		QualityProfileID:  qualProfileID,
		MetadataProfileID: metaProfileID,
		RootFolderPath:    rootFolder,
		Monitor:           monitor,
		SearchNow:         true,
	})
	if err != nil {
		e.log.Warn().Err(err).Str("artist", ae.Artist.Name).Msg("failed adding to Lidarr after skip")
		return
	}
	_ = e.db.SetSetting(ctx, fmt.Sprintf("lidarr_artist_%s", artistMbid), fmt.Sprintf("%d", added.ID))
	e.log.Info().Int("lidarr_id", added.ID).Str("artist", ae.Artist.Name).Msg("added artist to Lidarr after skip")
}

// ComputePhase3Candidates pre-computes Phase 3 search candidates for approved
// events by checking library caches for collection gaps (movies) and missing
// earlier seasons (TV/anime). Uses pre-warmed Radarr + Sonarr caches.
// In Stage 1, collection gaps are only found for movies already in Radarr.
// Stage 2 (discover enrichment of collection_id) will enable all movies.
func (e *Executor) ComputePhase3Candidates(ctx context.Context, events []db.EventWithTitle) []Phase3Candidate {
	var candidates []Phase3Candidate

	// Pre-fetch Radarr collections once for all movie collection gap checks
	var radarrCollections []library.RadarrCollection
	if e.cfg.CheckCollections && e.radarr != nil {
		var err error
		radarrCollections, err = e.radarr.GetCollections(ctx)
		if err != nil {
			e.log.Warn().Err(err).Msg("failed to fetch Radarr collections, skipping Phase 3 movies")
		}
	}

	for _, ev := range events {
		if ev.Title.MediaType == model.MediaTypeMovie {
			if !e.cfg.CheckCollections || e.radarr == nil {
				continue
			}
			// Check if movie is in Radarr with collection info from cache
			tmdbID := ev.Title.TmdbID
			if tmdbID == 0 {
				e.log.Warn().Str("title", ev.Title.Title).Msg("no TMDB ID, skipping collection gap check")
				continue
			}
			existing, err := e.radarr.Exists(ctx, tmdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("radarr check for phase 3")
				continue
			}
			if existing == nil || existing.Collection == nil || existing.Collection.TMDBID == 0 {
				continue
			}

			// Fetch all Radarr movies for "not in library" check
			allMovies, err := e.radarr.GetAllMovies(ctx)
			if err != nil {
				e.log.Warn().Err(err).Msg("failed to fetch Radarr movies for Phase 3")
				continue
			}
			movieByTMDB := make(map[int]library.RadarrMovie, len(allMovies))
			for _, m := range allMovies {
				movieByTMDB[m.TMDBID] = m
			}

			for _, col := range radarrCollections {
				if col.TMDBID != existing.Collection.TMDBID {
					continue
				}
				for _, m := range col.Movies {
					if m.TMDBID == tmdbID {
						continue
					}
					if m.Status != "" && m.Status != "released" {
						continue
					}
					if existing, ok := movieByTMDB[m.TMDBID]; ok && existing.HasFile {
						continue
					}
					candidates = append(candidates, Phase3Candidate{
						Title:     m.Title,
						Year:      m.Year,
						MediaType: model.MediaTypeMovie,
						TmdbID:    m.TMDBID,
					})
				}
			}
		}

		if ev.Title.MediaType == model.MediaTypeTV || ev.Title.MediaType == model.MediaTypeAnime {
			season := quality.ParseSeasonNumber(ev.Title.Title)
			if season <= 1 {
				continue
			}

			tvdbID := ev.Title.TvdbID
			if tvdbID == 0 && ev.Title.MediaType == model.MediaTypeAnime {
				lookup, err := e.sonarr.LookupByTitle(ctx, ev.Title.Title)
				if err != nil || lookup == nil {
					e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("anime title lookup failed, skipping Phase 3 season check")
					continue
				}
				tvdbID = lookup.TVDBID
			}
			if tvdbID == 0 {
				e.log.Warn().Str("title", ev.Title.Title).Msg("no TVDB ID, skipping Phase 3 season check")
				continue
			}

			existing, err := e.sonarr.Exists(ctx, tvdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("sonarr check for phase 3")
				continue
			}

			var missingSeasons []int
			if existing != nil {
				// Series is in Sonarr — check for missing seasons
				series, err := e.sonarr.GetSeries(ctx, existing.ID)
				if err != nil {
					e.log.Warn().Err(err).Msg("fetching sonarr series for phase 3")
					continue
				}
				for _, s := range series.Seasons {
					if s.SeasonNumber == 0 || s.SeasonNumber >= season {
						continue
					}
					if s.Statistics != nil && s.Statistics.EpisodeFileCount > 0 {
						continue
					}
					if s.Statistics != nil && s.Statistics.TotalEpisodeCount == 0 {
						continue
					}
					missingSeasons = append(missingSeasons, s.SeasonNumber)
				}
			} else {
				// Series not in Sonarr — assume all earlier seasons needed
				for s := 1; s < season; s++ {
					missingSeasons = append(missingSeasons, s)
				}
			}

			for _, ms := range missingSeasons {
				candidates = append(candidates, Phase3Candidate{
					Title:     ev.Title.Title,
					Year:      ev.Title.Year,
					MediaType: ev.Title.MediaType,
					TvdbID:    tvdbID,
					Season:    ms,
				})
			}
		}
	}

	return candidates
}

// SearchAllCandidates searches all Phase 3 candidates and stores results
// in the executor's phase3SearchPhase slice.
func (e *Executor) SearchAllCandidates(ctx context.Context, candidates []Phase3Candidate) {
	e.log.Info().Msgf("Searching %d Phase 3 candidates...", len(candidates))
	for i, c := range candidates {
		e.log.Info().Str("title", c.Title).Msgf("[%d/%d] searching phase 3", i+1, len(candidates))

		title := &model.Title{
			Title:     c.Title,
			Year:      c.Year,
			MediaType: c.MediaType,
			TmdbID:    c.TmdbID,
			TvdbID:    c.TvdbID,
		}
		stripped := quality.StripSeason(c.Title)
		season := c.Season
		if season == 0 {
			season = quality.ParseSeasonNumber(c.Title)
		}

		releases, err := e.searchRelease(ctx, title, stripped, season)
		if err != nil {
			e.log.Warn().Err(err).Str("title", c.Title).Msg("error searching phase 3 candidate")
			entry := Phase3SearchEntry{Candidate: c}
			e.phase3SearchPhase = append(e.phase3SearchPhase, entry)
			continue
		}

		prefs := buildQualityPrefs(e.cfg, c.MediaType)
		top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

		entry := Phase3SearchEntry{Candidate: c, Top: top}
		e.phase3SearchPhase = append(e.phase3SearchPhase, entry)

		if len(top) > 0 {
			e.log.Info().Int("count", len(top)).Str("title", c.Title).Msg("phase 3 results found")
		} else {
			e.log.Info().Str("title", c.Title).Msg("no phase 3 results")
		}
	}
}

// ProcessPhase3Pickers presents pickers for pre-searched Phase 3 items,
// adds collection movies to Radarr, and downloads torrents.
func (e *Executor) ProcessPhase3Pickers(ctx context.Context) {
	if len(e.phase3SearchPhase) == 0 && len(e.phase3Movies) == 0 && len(e.phase3Seasons) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Phase 3: Collection movies & earlier seasons ──")

	profileID := 1
	if e.radarr != nil {
		profileID = e.resolveProfileID(ctx, e.cfg.Library.Radarr.QualityProfile)
	}

	// Pre-computed Phase 3 candidates
	for _, entry := range e.phase3SearchPhase {
		c := entry.Candidate
		mode := e.cfg.MediaTypeMode(c.MediaType)

		// Skip TV/anime candidates for series the user rejected adding to library
		if c.TvdbID > 0 {
			if _, rejected := e.skipRejectedTvdbIDs[c.TvdbID]; rejected {
				existing, _ := e.sonarr.Exists(ctx, c.TvdbID)
				if existing == nil {
					e.log.Info().Str("title", c.Title).Int("tvdb", c.TvdbID).Msg("skipping phase 3: user rejected library add")
					continue
				}
			}
		}

		if mode == "arr" || mode == "auto" || mode == "yolo" {
			// Arr mode: add to *arr with SearchNow, skip picker & download
			if c.MediaType == model.MediaTypeMovie && c.TmdbID > 0 {
				existing, err := e.radarr.Exists(ctx, c.TmdbID)
				if err == nil && existing == nil {
					if _, addErr := e.radarr.Add(ctx, c.TmdbID, c.Title, c.Year, library.AddMovieOptions{
						Monitored:           e.cfg.Library.Radarr.Monitor,
						MinimumAvailability: "released",
						QualityProfileID:    profileID,
						RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
						SearchNow:           true,
					}); addErr != nil {
						e.log.Warn().Err(addErr).Str("title", c.Title).Msg("adding collection movie to Radarr")
					} else {
						e.log.Info().Str("title", c.Title).Msg("added collection movie to Radarr [will trigger search]")
					}
				}
			} else if c.Season > 0 && c.TvdbID > 0 {
				// Earlier season — trigger Sonarr search
				existing, err := e.sonarr.Exists(ctx, c.TvdbID)
				if err == nil && existing != nil {
					if err := e.sonarr.TriggerSeasonSearch(ctx, existing.ID, c.Season); err != nil {
						e.log.Warn().Err(err).Str("title", c.Title).Int("season", c.Season).Msg("triggering season search")
					} else {
						e.log.Info().Str("title", c.Title).Int("season", c.Season).Msg("triggered Sonarr season search")
					}
				}
			}
			continue
		}

		// Full mode: current behavior (picker + download)
		if len(entry.Top) == 0 {
			continue
		}

		sr := &SearchResult{
			Event: db.EventWithTitle{
				Title: &model.Title{
					Title:     c.Title,
					Year:      c.Year,
					MediaType: c.MediaType,
					TmdbID:    c.TmdbID,
					TvdbID:    c.TvdbID,
				},
			},
			Season: c.Season,
			Top:    entry.Top,
		}
		if c.Season > 0 {
			sr.Season = c.Season
		}

		chosen, err := e.presentPicker(ctx, sr)
		if err != nil || len(chosen) == 0 {
			continue
		}

		// For movies, add to Radarr first if not already present
		if c.MediaType == model.MediaTypeMovie && c.TmdbID > 0 {
			existing, err := e.radarr.Exists(ctx, c.TmdbID)
			if err == nil && existing == nil {
				if _, addErr := e.radarr.Add(ctx, c.TmdbID, c.Title, c.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           false,
				}); addErr != nil {
					e.log.Warn().Err(addErr).Str("title", c.Title).Msg("adding collection movie to Radarr")
				}
			}
		}

		category := e.cfg.Downloader.Categories.Movies
		if c.MediaType == model.MediaTypeTV || c.MediaType == model.MediaTypeAnime {
			category = e.cfg.Downloader.Categories.TV
		}

		for _, release := range chosen {
			uri := release.DownloadURL
			if uri == "" {
				uri = release.MagnetURL
			}
			if uri != "" {
				if _, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category)); err != nil {
					e.log.Warn().Err(err).Msg("phase 3 add failed")
				}
			}
		}
		e.log.Info().Str("title", c.Title).Msg("phase 3 item downloaded")
	}

	// Handle any Phase 3 items accumulated during ProcessLibraryDecisions
	// (collection gaps from movies newly added to Radarr in Phase 2b).
	if len(e.phase3Movies) > 0 {
		mode := e.cfg.MediaTypeMode(model.MediaTypeMovie)

		if mode == "arr" || mode == "auto" || mode == "yolo" {
			fmt.Fprintln(os.Stderr, "\n── Adding collection movies to Radarr ──")
			for _, p3m := range e.phase3Movies {
				existing, err := e.radarr.Exists(ctx, p3m.TMDBID)
				if err == nil && existing == nil {
					if _, addErr := e.radarr.Add(ctx, p3m.TMDBID, p3m.Title, p3m.Year, library.AddMovieOptions{
						Monitored:           e.cfg.Library.Radarr.Monitor,
						MinimumAvailability: "released",
						QualityProfileID:    p3m.ProfileID,
						RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
						SearchNow:           true,
					}); addErr != nil {
						e.log.Warn().Err(addErr).Str("title", p3m.Title).Msg("adding collection movie to Radarr")
					} else {
						e.log.Info().Str("title", p3m.Title).Msg("added collection movie to Radarr [will trigger search]")
					}
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "\n── Additional collection movies (from library adds) ──")
			for _, p3m := range e.phase3Movies {
				// Skip if already handled via pre-searched phase 3
				if e.hasPhase3Movie(p3m.TMDBID) {
					continue
				}

				title := p3m.Title
				stripped := quality.StripSeason(title)
				season := quality.ParseSeasonNumber(title)

				releases, err := e.searchRelease(ctx, &model.Title{
					Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie,
				}, stripped, season)
				if err != nil {
					e.log.Warn().Err(err).Str("title", title).Msg("error searching collection movie")
					continue
				}

				prefs := buildQualityPrefs(e.cfg, model.MediaTypeMovie)
				top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
				if len(top) == 0 {
					continue
				}

				sr := &SearchResult{
					Event: db.EventWithTitle{Title: &model.Title{
						Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie, TmdbID: p3m.TMDBID,
					}},
					Season: season, Top: top,
				}

				chosen, err := e.presentPicker(ctx, sr)
				if err != nil || len(chosen) == 0 {
					continue
				}
				synthEvent := db.EventWithTitle{Title: &model.Title{
					Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie, TmdbID: p3m.TMDBID,
				}}
				e.addToClient(ctx, synthEvent, chosen)
			}
		}
		e.phase3Movies = nil
	}

	if len(e.phase3Seasons) > 0 {
		// Check mode from the first season entry (all should be same media type)
		seasonMode := e.cfg.MediaTypeMode(e.phase3Seasons[0].MediaType)

		if seasonMode == "arr" || seasonMode == "auto" || seasonMode == "yolo" {
			fmt.Fprintln(os.Stderr, "\n── Triggering Sonarr season searches ──")
			for _, p3s := range e.phase3Seasons {
				existing, err := e.sonarr.Exists(ctx, p3s.SeriesID)
				if err == nil && existing != nil {
					if err := e.sonarr.TriggerSeasonSearch(ctx, existing.ID, p3s.SeasonNumber); err != nil {
						e.log.Warn().Err(err).Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("triggering season search")
					} else {
						e.log.Info().Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("triggered Sonarr season search")
					}
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "\n── Additional earlier seasons (from library adds) ──")
			for _, p3s := range e.phase3Seasons {
				// Skip if already handled via pre-searched phase 3
				if e.hasPhase3Season(p3s.SeriesTitle, p3s.SeasonNumber) {
					continue
				}

				stripped := quality.StripSeason(p3s.SeriesTitle)
				releases, err := e.searchRelease(ctx, &model.Title{
					Title: p3s.SeriesTitle, MediaType: p3s.MediaType,
				}, stripped, p3s.SeasonNumber)
				if err != nil {
					e.log.Warn().Err(err).Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("error searching season")
					continue
				}

				prefs := buildQualityPrefs(e.cfg, p3s.MediaType)
				top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
				if len(top) == 0 {
					continue
				}

				sr := &SearchResult{
					Event:  db.EventWithTitle{Title: &model.Title{Title: p3s.SeriesTitle, MediaType: p3s.MediaType}},
					Season: p3s.SeasonNumber, Top: top,
				}

				chosen, err := e.presentPicker(ctx, sr)
				if err != nil || len(chosen) == 0 {
					continue
				}
				synthEvent := db.EventWithTitle{Title: &model.Title{
					Title: p3s.SeriesTitle, MediaType: p3s.MediaType,
				}}
				e.addToClient(ctx, synthEvent, chosen)
			}
		}
		e.phase3Seasons = nil
	}

	// Reset for next run
	e.phase3SearchPhase = nil
}

func (e *Executor) hasPhase3Movie(tmdbID int) bool {
	for i := range e.phase3SearchPhase {
		if e.phase3SearchPhase[i].Candidate.TmdbID == tmdbID {
			return true
		}
	}
	return false
}

func (e *Executor) hasPhase3Season(title string, season int) bool {
	for i := range e.phase3SearchPhase {
		c := &e.phase3SearchPhase[i].Candidate
		if c.Title == title && c.Season == season {
			return true
		}
	}
	return false
}

// ProcessLibraryDecisions implements Phase 2 + Phase 3, shared by both
// batch and interactive modes. For each picked item it asks library questions
// (add to Radarr/Sonarr, collection gaps, earlier seasons), executes adds.
func (e *Executor) ProcessLibraryDecisions(ctx context.Context, picked []PickedItem) {
	if len(picked) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Library decisions ──")

	fmt.Fprintf(os.Stderr, "  Loading Radarr library...")
	if movies, err := e.radarr.GetAllMovies(ctx); err != nil {
		fmt.Fprintf(os.Stderr, " error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, " %d movies loaded\n", len(movies))
	}

	fmt.Fprintf(os.Stderr, "  Loading Sonarr library...")
	if series, err := e.sonarr.GetAllSeries(ctx); err != nil {
		fmt.Fprintf(os.Stderr, " error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, " %d series loaded\n", len(series))
	}

	// Phase 2a: Collect and prompt for library adds
	type addAction struct {
		evt       db.EventWithTitle
		season    int
		isTV      bool
		tmdbID    int
		tvdbID    int
		searchNow bool
	}
	var addActions []addAction

	type retryAction int
	const (
		retryActionSkip retryAction = iota
		retryActionRetry
		retryActionQuit
	)

	promptRetry := func(label string, err error) retryAction {
		fmt.Fprintf(os.Stderr, "  %s error: %v\n", label, err)
		for {
			fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip this item  [q] quit pipeline\n")
			fmt.Fprintf(os.Stderr, "  Choose: ")
			ch := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				ch <- scanner.Text()
			}()
			select {
			case ans := <-ch:
				switch strings.ToLower(strings.TrimSpace(ans)) {
				case "r", "retry":
					return retryActionRetry
				case "s", "skip":
					return retryActionSkip
				case "q", "quit":
					return retryActionQuit
				}
			case <-ctx.Done():
				return retryActionQuit
			}
		}
	}

processPicked:
	for _, item := range picked {
		mode := e.cfg.MediaTypeMode(item.Event.Title.MediaType)
		searchNow := mode == "arr" || mode == "auto" || mode == "yolo"
		autoConfirm := mode == "auto" || mode == "yolo"
		if item.Event.Title.MediaType == model.MediaTypeMovie {
			tmdbID := item.Event.Title.TmdbID
			if tmdbID == 0 {
				e.log.Warn().Str("title", item.Event.Title.Title).Msg("no TMDB ID, trying title lookup in Radarr")
				titleLookup, lookupErr := e.radarr.LookupByTitle(ctx, item.Event.Title.Title)
				if lookupErr != nil || titleLookup == nil {
					e.log.Warn().Err(lookupErr).Str("title", item.Event.Title.Title).Msg("Radarr title lookup failed, skipping library decisions")
					goto nextPicked
				}
				tmdbID = titleLookup.TMDBID
				e.log.Info().Str("title", item.Event.Title.Title).Int("tmdb_id", tmdbID).Msg("found TMDB ID via Radarr title lookup")
			}
			for {
				existing, err := e.radarr.Exists(ctx, tmdbID)
				if err != nil {
					switch promptRetry("Radarr", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if existing != nil {
					e.log.Info().Str("title", item.Event.Title.Title).Msg("already in Radarr")
					if e.cfg.CheckCollections && existing.Collection != nil && existing.Collection.TMDBID > 0 {
						profileID := e.resolveProfileID(ctx, e.cfg.Library.Radarr.QualityProfile)
						phase3FromCollection := e.checkCollectionGaps(ctx, existing.TMDBID, existing.Collection.TMDBID, profileID, searchNow)
						e.phase3Movies = append(e.phase3Movies, phase3FromCollection...)
					}
					goto nextPicked
				}
				break
			}
			var lookup *library.RadarrMovie
			for {
				var err error
				lookup, err = e.radarr.Lookup(ctx, tmdbID)
				if err != nil {
					switch promptRetry("Radarr lookup", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if lookup == nil {
					e.log.Warn().Str("title", item.Event.Title.Title).Int("tmdb", tmdbID).Msg("movie not found on TMDB")
					goto nextPicked
				}
				break
			}
			e.log.Info().Msgf("Add to Radarr? %s (%d)", lookup.Title, lookup.Year)
			if lookup.Overview != "" {
				for _, line := range formatOverview(lookup.Overview, 72) {
					e.log.Info().Msg(line)
				}
			}
			e.log.Info().Str("profile", e.cfg.Library.Radarr.QualityProfile).Str("root", e.cfg.Library.Radarr.RootFolder).Msg("Radarr config")
			if autoConfirm || promptYesNo(ctx, "  Add to Radarr?") {
				addActions = append(addActions, addAction{
					evt:       item.Event,
					isTV:      false,
					tmdbID:    tmdbID,
					searchNow: searchNow,
				})
			}
		} else {
			tvdbID := item.Event.Title.TvdbID
			if tvdbID == 0 {
				if item.Event.Title.MediaType == model.MediaTypeAnime {
					searchTitle := sanitizeSearchQuery(quality.StripSeason(item.Event.Title.Title))
					titleLookup, err := e.sonarr.LookupByTitle(ctx, searchTitle)
					if err != nil {
						e.log.Warn().Err(err).Str("title", item.Event.Title.Title).Msg("anime title lookup in Sonarr failed")
						goto nextPicked
					}
					if titleLookup == nil {
						e.log.Warn().Str("title", item.Event.Title.Title).Msg("anime not found in Sonarr by title lookup")
						goto nextPicked
					}
					tvdbID = titleLookup.TVDBID
				} else {
					e.log.Warn().Str("title", item.Event.Title.Title).Msg("no TVDB ID, skipping Sonarr library decisions")
					goto nextPicked
				}
			}
			for {
				existing, err := e.sonarr.Exists(ctx, tvdbID)
				if err != nil {
					switch promptRetry("Sonarr", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if existing != nil {
					e.log.Info().Str("title", existing.Title).Int("season", item.Season).Msg("already in Sonarr")
					if item.Season > 1 {
						missing := e.checkExistingSonarrSeasons(ctx, existing, item.Season)
						for _, missingS := range missing {
							enqueue := false
							if searchNow { // auto/yolo modes
								enqueue = true
								e.log.Info().Str("series", existing.Title).Int("season", missingS.SeasonNumber).Msg("auto-queueing earlier season search")
							} else {
								enqueue = promptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", existing.Title, missingS.SeasonNumber))
							}
							if enqueue {
								e.phase3Seasons = append(e.phase3Seasons, struct {
									SeriesID     int
									SeasonNumber int
									SeriesTitle  string
									MediaType    model.MediaType
								}{
									SeriesID:     existing.ID,
									SeasonNumber: missingS.SeasonNumber,
									SeriesTitle:  existing.Title,
									MediaType:    item.Event.Title.MediaType,
								})
							}
						}
					}
					goto nextPicked
				}
				break
			}
			var lookup *library.SonarrSeries
			for {
				var err error
				lookup, err = e.sonarr.Lookup(ctx, tvdbID)
				if err != nil {
					switch promptRetry("Sonarr lookup", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if lookup == nil {
					e.log.Warn().Str("title", item.Event.Title.Title).Int("tvdb", tvdbID).Msg("series not found on TVDB")
					goto nextPicked
				}
				break
			}
			e.log.Info().Msgf("Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
			if lookup.Overview != "" {
				for _, line := range formatOverview(lookup.Overview, 72) {
					e.log.Info().Msg(line)
				}
			}
			e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
			if autoConfirm || promptYesNo(ctx, "  Add to Sonarr?") {
				addActions = append(addActions, addAction{
					evt:       item.Event,
					season:    item.Season,
					isTV:      true,
					tvdbID:    tvdbID,
					searchNow: searchNow,
				})
			}
		}
	nextPicked:
	}

	// Phase 2b: Execute adds + check collections/earlier seasons
	if len(addActions) > 0 {
		fmt.Fprintln(os.Stderr, "\n── Executing library adds ──")

		for _, a := range addActions {
			var title string
			if a.isTV {
				title = a.evt.Title.Title
			} else {
				title = a.evt.Title.Title
			}

			var addErr error
		actionRetry:
			if a.isTV {
				addErr = e.addToSonarr(ctx, a.evt, a.tvdbID, a.season, true, a.searchNow, false)
			} else {
				addErr = e.addToRadarr(ctx, a.evt, true, a.searchNow)
			}
			if addErr != nil {
				fmt.Fprintf(os.Stderr, "  Error adding %q to library: %v\n", title, addErr)
				fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip  [q] quit pipeline\n")
				fmt.Fprintf(os.Stderr, "  Choose: ")
				ch := make(chan string, 1)
				go func() {
					scanner := bufio.NewScanner(os.Stdin)
					scanner.Scan()
					ch <- scanner.Text()
				}()
				select {
				case ans := <-ch:
					switch strings.ToLower(strings.TrimSpace(ans)) {
					case "r", "retry":
						goto actionRetry
					case "s", "skip":
						e.log.Warn().Msgf("skipped adding %q to library", title)
					case "q", "quit":
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}
	}

}

func (e *Executor) searchPhase3Season(ctx context.Context, s struct {
	SeriesID     int
	SeasonNumber int
	SeriesTitle  string
	MediaType    model.MediaType
}) {
	stripped := quality.StripSeason(s.SeriesTitle)

	releases, err := e.searchRelease(ctx, &model.Title{
		Title:     s.SeriesTitle,
		Year:      0,
		MediaType: s.MediaType,
	}, stripped, s.SeasonNumber)
	if err != nil {
		e.log.Warn().Err(err).Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("error searching earlier season")
		return
	}

	prefs := buildQualityPrefs(e.cfg, s.MediaType)
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
	if len(top) == 0 {
		e.log.Info().Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("no results for earlier season")
		return
	}

	sr := &SearchResult{
		Event:  db.EventWithTitle{Title: &model.Title{Title: s.SeriesTitle, MediaType: s.MediaType}},
		Season: s.SeasonNumber,
		Top:    top,
	}

	chosen, err := e.presentPicker(ctx, sr)
	if err != nil || len(chosen) == 0 {
		return
	}

	synthEvent := db.EventWithTitle{
		Title: &model.Title{
			Title:     s.SeriesTitle,
			MediaType: s.MediaType,
		},
	}
	e.addToClient(ctx, synthEvent, chosen)
}

func (e *Executor) searchRelease(ctx context.Context, title *model.Title, stripped string, season int) ([]quality.ParsedRelease, error) {
	resCfg := e.cfg.Quality.Movies
	searchType := "movie"
	searchCats := []int{search.CatMovie}

	switch title.MediaType {
	case model.MediaTypeTV:
		resCfg = e.cfg.Quality.TV
		searchType = "tvsearch"
		searchCats = []int{search.CatTV}
	case model.MediaTypeAnime:
		resCfg = e.cfg.Quality.Anime
		searchType = "tvsearch"
		searchCats = []int{search.CatAnime, search.CatTV}
	}

	resKeyword := resolutionSearchKeyword(resCfg.Resolution)
	fallbackRes := fallbackResolution(resKeyword)

	var queries []string
	if title.MediaType == model.MediaTypeTV || title.MediaType == model.MediaTypeAnime {
		// When season defaults to 1 and the title contains an ambiguous
		// descriptor like "Final Season" or "Last Season", skip the
		// S01/season-1 query tiers to avoid polluting results.
		lower := strings.ToLower(title.Title)
		skipSeason := season == 1 && (strings.Contains(lower, "final season") || strings.Contains(lower, "last season"))
		queries = tvSearchQueries(stripped, season, resKeyword, fallbackRes, skipSeason)
	} else {
		queries = movieSearchQueries(stripped, title.Year, resKeyword)
	}

	preferredID := e.prowl.PreferredIndexerID(searchCats[0])
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	// Sanitize the search title so FilterRelease words match what
	// was actually sent to Prowlarr (e.g. "Hell's" → "Hells",
	// "Fate/strange" → "Fate strange").
	sanitized := sanitizeSearchQuery(stripped)

	// For anime: define category sets for tiered search
	animeCatSets := [][]int{searchCats}
	allCatSets := [][]int{searchCats}
	if title.MediaType == model.MediaTypeAnime {
		animeCatSets = [][]int{{search.CatAnime}}
		allCatSets = [][]int{{search.CatTV}}
	}

	// Phase 1: all tiers on preferred indexer only
	if preferredID > 0 {
		name := e.prowl.GetIndexerName(ctx, preferredID)
		e.log.Info().Str("name", name).Int("id", preferredID).Msg("preferred indexer")

		for _, cats := range animeCatSets {
			for i, q := range queries {
				e.log.Info().Msgf("[%d/%d] preferred (cats=%v): %s", i+1, numTiers, cats, q)
				results, err := e.prowl.Search(ctx, search.SearchParams{
					Query:      q,
					Type:       searchType,
					IndexerID:  preferredID,
					Limit:      50,
					Categories: cats,
				})
				if err != nil {
					return nil, err
				}
				exact, fuzzy := quality.PartitionReleases(results, sanitized, title.Year, season, string(title.MediaType))
				exactPool = mergeReleases(exactPool, exact)
				fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
				e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
				if len(exactPool) >= 10 {
					return exactPool, nil
				}
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	// Phase 2: all tiers on all indexers
	for _, cats := range allCatSets {
		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] searching all (cats=%v): %s", i+1, numTiers, cats, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       searchType,
				Limit:      50,
				Categories: cats,
			})
			if err != nil {
				return nil, err
			}
			exact, fuzzy := quality.PartitionReleases(results, sanitized, title.Year, season, string(title.MediaType))
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= 10 {
				return exactPool, nil
			}
		}
	}

	if len(exactPool) > 0 || len(fuzzyPool) > 0 {
		result := exactPool
		if n := 10 - len(exactPool); n > 0 && len(fuzzyPool) > 0 {
			if n > len(fuzzyPool) {
				n = len(fuzzyPool)
			}
			result = append(result, fuzzyPool[:n]...)
		}
		return result, nil
	}
	return nil, nil
}

func mergeReleases(a, b []quality.ParsedRelease) []quality.ParsedRelease {
	seen := make(map[string]bool, len(a))
	result := make([]quality.ParsedRelease, 0, len(a)+len(b))
	for _, r := range a {
		key := r.InfoHash
		if key == "" {
			key = r.Guid
		}
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, r)
		}
	}
	for _, r := range b {
		key := r.InfoHash
		if key == "" {
			key = r.Guid
		}
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, r)
		}
	}
	return result
}

func resolutionSearchKeyword(res string) string {
	switch res {
	case "2160p", "4k", "uhd":
		return "2160p"
	case "1080p":
		return "1080p"
	case "720p":
		return "720p"
	}
	return "1080p"
}

func movieSearchQueries(title string, year int, res string) []string {
	queries := []string{
		fmt.Sprintf("%s %d %s", title, year, res),
		fmt.Sprintf("%s %d 4k", title, year),
		fmt.Sprintf("%s %d 1080p", title, year),
		fmt.Sprintf("%s %d", title, year),
	}
	for i := range queries {
		queries[i] = sanitizeSearchQuery(queries[i])
	}
	return queries
}

func tvSearchQueries(stripped string, season int, res, fallback string, skipSeason bool) []string {
	seasonStr := fmt.Sprintf("S%02d", season)
	seasonWord := fmt.Sprintf("season %d", season)

	var tiers []string
	if !skipSeason {
		tiers = []string{
			sanitizeSearchQuery(fmt.Sprintf("%s %s complete %s", stripped, seasonStr, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s complete %s", stripped, seasonWord, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s %s", stripped, seasonStr, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s %s", stripped, seasonWord, res)),
		}
	}
	tiers = append(tiers, sanitizeSearchQuery(fmt.Sprintf("%s %s", stripped, res)))
	if fallback != "" {
		tiers = append(tiers, sanitizeSearchQuery(fmt.Sprintf("%s %s", stripped, fallback)))
	}
	return tiers
}

// sanitizeSearchQuery removes characters that can interfere with
// Newznab/Prowlarr search, and normalises punctuation that differs
// between release names and metadata titles.
func sanitizeSearchQuery(q string) string {
	q = strings.ReplaceAll(q, "/", " ")
	q = strings.ReplaceAll(q, "'", "")
	q = strings.ReplaceAll(q, "\"", "")
	q = strings.ReplaceAll(q, "?", "")
	q = strings.ReplaceAll(q, ":", " ")
	q = strings.ReplaceAll(q, "½", "")
	q = strings.ReplaceAll(q, "¼", "")
	q = strings.ReplaceAll(q, "¾", "")
	q = strings.ReplaceAll(q, "⁄", "")
	return strings.TrimSpace(q)
}

func fallbackResolution(res string) string {
	switch res {
	case "2160p":
		return "1080p"
	case "1080p":
		return "720p"
	case "720p":
		return "480p"
	}
	return ""
}

func (e *Executor) addToLibrary(ctx context.Context, evt db.EventWithTitle, season int) error {
	if evt.Title.MediaType == model.MediaTypeMovie {
		return e.addToRadarr(ctx, evt, false, false)
	}
	tvdbID := evt.Title.TvdbID
	if tvdbID == 0 && evt.Title.MediaType == model.MediaTypeAnime {
		lookup, err := e.sonarr.LookupByTitle(ctx, evt.Title.Title)
		if err != nil {
			return fmt.Errorf("anime title lookup in Sonarr: %w", err)
		}
		if lookup == nil {
			e.log.Warn().Str("title", evt.Title.Title).Msg("anime not found in Sonarr by title lookup")
			return nil
		}
		tvdbID = lookup.TVDBID
	}
	return e.addToSonarr(ctx, evt, tvdbID, season, false, false, false)
}

func (e *Executor) resolveProfileID(ctx context.Context, profileName string) int {
	profiles, err := e.radarr.GetQualityProfiles(ctx)
	if err != nil {
		return 1
	}
	for _, p := range profiles {
		if p.Name == profileName {
			return p.ID
		}
	}
	return 1
}

func (e *Executor) resolveSonarrProfileID(ctx context.Context, profileName string) int {
	profiles, err := e.sonarr.GetQualityProfiles(ctx)
	if err != nil {
		return 1
	}
	for _, p := range profiles {
		if p.Name == profileName {
			return p.ID
		}
	}
	return 1
}

func (e *Executor) addToRadarr(ctx context.Context, evt db.EventWithTitle, confirmed bool, searchNow bool) error {
	tmdbID := evt.Title.TmdbID
	if tmdbID == 0 {
		return fmt.Errorf("cannot add to Radarr: movie %q has no TMDB ID", evt.Title.Title)
	}

	if !confirmed {
		existing, err := e.radarr.Exists(ctx, tmdbID)
		if err != nil {
			return err
		}
		if existing != nil {
			e.log.Info().Str("title", evt.Title.Title).Msg("already in Radarr")
			return nil
		}

		lookup, err := e.radarr.Lookup(ctx, tmdbID)
		if err != nil {
			return err
		}
		if lookup == nil {
			return fmt.Errorf("movie not found on TMDB (tmdb_id=%d)", tmdbID)
		}

		prompt := fmt.Sprintf("  Add to Radarr? %s (%d)", lookup.Title, lookup.Year)
		if searchNow {
			prompt += " [will trigger search]"
		}
		e.log.Info().Msg(prompt)
		if lookup.Overview != "" {
			for _, line := range formatOverview(lookup.Overview, 72) {
				e.log.Info().Msg(line)
			}
		}
		e.log.Info().Str("profile", e.cfg.Library.Radarr.QualityProfile).Str("root", e.cfg.Library.Radarr.RootFolder).Msg("Radarr config")
		if !promptYesNo(ctx, "  Add to Radarr?") {
			return nil
		}
	}

	title := evt.Title.Title
	year := evt.Title.Year
	profileID := e.resolveProfileID(ctx, e.cfg.Library.Radarr.QualityProfile)

	added, err := e.radarr.Add(ctx, tmdbID, title, year, library.AddMovieOptions{
		Monitored:           e.cfg.Library.Radarr.Monitor,
		MinimumAvailability: "released",
		QualityProfileID:    profileID,
		RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
		SearchNow:           searchNow,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Radarr")

	if e.cfg.CheckCollections {
		if added.Collection != nil && added.Collection.TMDBID > 0 {
			e.log.Info().Str("title", title).Str("collection", added.Collection.Name).Int("tmdb", added.Collection.TMDBID).Msg("checking collection gaps")
			phase3FromCollection := e.checkCollectionGaps(ctx, tmdbID, added.Collection.TMDBID, profileID, searchNow)
			e.phase3Movies = append(e.phase3Movies, phase3FromCollection...)
		} else {
			e.log.Info().Str("title", title).Msg("movie not part of a TMDB collection, skipping")
		}
	}

	return nil
}

func (e *Executor) AddAiringAnimeToSonarr(ctx context.Context, evt db.EventWithTitle, searchNow bool) (*library.SonarrSeries, error) {
	if e.sonarr == nil {
		e.log.Warn().Msg("Sonarr not configured, skipping anime addition")
		return nil, nil
	}

	season := quality.ParseSeasonNumber(evt.Title.Title)
	stripped := quality.StripSeason(evt.Title.Title)
	searchTitle := sanitizeSearchQuery(stripped)

	lookup, err := e.sonarr.LookupByTitle(ctx, searchTitle)
	if err != nil {
		return nil, fmt.Errorf("looking up anime in Sonarr: %w", err)
	}
	if lookup == nil {
		e.log.Warn().Str("title", searchTitle).Msg("anime not found on Sonarr via title lookup")
		return nil, nil
	}

	existing, err := e.sonarr.Exists(ctx, lookup.TVDBID)
	if err == nil && existing != nil {
		e.log.Info().Str("title", searchTitle).Int("id", existing.ID).Msg("already in Sonarr")
		return existing, nil
	}

	prompt := fmt.Sprintf("  Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
	if searchNow {
		prompt += " [will trigger search]"
	}
	e.log.Info().Msg(prompt)
	if lookup.Overview != "" {
		for _, line := range formatOverview(lookup.Overview, 72) {
			e.log.Info().Msg(line)
		}
	}
	e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
	if searchNow {
		e.log.Info().Str("title", searchTitle).Msg("auto-adding airing anime to Sonarr")
	} else if !promptYesNo(ctx, "  Add to Sonarr?") {
		e.log.Info().Str("title", searchTitle).Msg("skipped adding airing anime to Sonarr")
		return nil, nil
	}

	profileID := e.resolveSonarrProfileID(ctx, e.cfg.Library.Sonarr.QualityProfile)
	langProfiles, err := e.sonarr.GetLanguageProfiles(ctx)
	if err != nil {
		return nil, err
	}
	langProfileID := 1
	if len(langProfiles) > 0 {
		langProfileID = langProfiles[0].ID
	}

	var seasons []library.SonarrSeason
	for _, s := range lookup.Seasons {
		if s.SeasonNumber > 0 {
			seasons = append(seasons, library.SonarrSeason{
				SeasonNumber: s.SeasonNumber,
				Monitored:    s.SeasonNumber == season,
			})
		}
	}

	opts := library.AddSeriesOptions{
		Monitored:         true,
		SeasonFolder:      e.cfg.Library.Sonarr.SeasonFolders,
		QualityProfileID:  profileID,
		LanguageProfileID: langProfileID,
		RootFolderPath:    e.cfg.Library.Sonarr.RootFolder,
		Seasons:           seasons,
		SearchForMissing:  searchNow,
	}

	series, err := e.sonarr.Add(ctx, lookup.TVDBID, lookup.Title, lookup.Year, opts)
	if err != nil {
		return nil, fmt.Errorf("adding anime to Sonarr: %w", err)
	}

	e.log.Info().Str("title", series.Title).Int("id", series.ID).Msg("added airing anime to Sonarr")
	return series, nil
}

// SearchAiringAnimeEarlierSeasons checks for missing earlier seasons of a
// currently-airing anime that was added to Sonarr. For each missing season
// the user approves, it looks up pre-searched results (from the batch search
// phase) and presents the torrent picker.
func (e *Executor) SearchAiringAnimeEarlierSeasons(ctx context.Context, evt db.EventWithTitle, series *library.SonarrSeries) {
	season := quality.ParseSeasonNumber(evt.Title.Title)
	if season <= 1 {
		return
	}

	displayTitle := series.Title
	if displayTitle == "" {
		displayTitle = evt.Title.Title
	}

	missing := e.checkExistingSonarrSeasons(ctx, series, season)
	mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
	for _, ms := range missing {
		searchSeason := false
		if mode == "auto" || mode == "yolo" {
			searchSeason = true
			e.log.Info().Str("title", displayTitle).Int("season", ms.SeasonNumber).Msg("auto-searching earlier season")
		} else {
			searchSeason = promptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", displayTitle, ms.SeasonNumber))
		}
		if !searchSeason {
			continue
		}

		// Find pre-searched results from batch phase
		var entry *Phase3SearchEntry
		for i := range e.phase3SearchPhase {
			c := &e.phase3SearchPhase[i].Candidate
			if c.Title == evt.Title.Title && c.Season == ms.SeasonNumber {
				entry = &e.phase3SearchPhase[i]
				break
			}
		}
		if entry == nil || len(entry.Top) == 0 {
			e.log.Info().Str("title", displayTitle).Int("season", ms.SeasonNumber).Msg("no pre-searched results for earlier season, falling back")
			e.searchPhase3Season(ctx, struct {
				SeriesID     int
				SeasonNumber int
				SeriesTitle  string
				MediaType    model.MediaType
			}{
				SeriesID:     series.ID,
				SeasonNumber: ms.SeasonNumber,
				SeriesTitle:  displayTitle,
				MediaType:    evt.Title.MediaType,
			})
			continue
		}

		sr := &SearchResult{
			Event:  db.EventWithTitle{Title: &model.Title{Title: displayTitle, MediaType: evt.Title.MediaType}},
			Season: ms.SeasonNumber,
			Top:    entry.Top,
		}
		chosen, err := e.presentPicker(ctx, sr)
		if err != nil || len(chosen) == 0 {
			continue
		}
		synthEvent := db.EventWithTitle{Title: &model.Title{Title: displayTitle, MediaType: evt.Title.MediaType}}
		e.addToClient(ctx, synthEvent, chosen)
	}
}

func (e *Executor) addToSonarr(ctx context.Context, evt db.EventWithTitle, tvdbID, season int, confirmed bool, searchNow bool, monitorTarget bool) error {
	if tvdbID == 0 {
		return nil
	}

	if !confirmed {
		existing, err := e.sonarr.Exists(ctx, tvdbID)
		if err != nil {
			return err
		}
		if existing != nil {
			series, err := e.sonarr.GetSeries(ctx, existing.ID)
			if err != nil {
				return err
			}
			targetSeason := findSeasonByNumber(series.Seasons, season)
			if targetSeason != nil && targetSeason.Statistics != nil &&
				targetSeason.Statistics.EpisodeFileCount >= targetSeason.Statistics.EpisodeCount &&
				targetSeason.Statistics.EpisodeCount > 0 {
				e.log.Info().Str("title", evt.Title.Title).Int("season", season).Msg("season already complete in Sonarr")
				return nil
			}
			e.log.Info().Str("title", evt.Title.Title).Int("season", season).Msg("already in Sonarr, season incomplete")
			return nil
		}

		lookup, err := e.sonarr.Lookup(ctx, tvdbID)
		if err != nil {
			return err
		}
		if lookup == nil {
			return fmt.Errorf("series not found on TVDB (tvdb_id=%d)", tvdbID)
		}

		prompt := fmt.Sprintf("  Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
		if searchNow {
			prompt += " [will trigger search]"
		}
		e.log.Info().Msg(prompt)
		if lookup.Overview != "" {
			for _, line := range formatOverview(lookup.Overview, 72) {
				e.log.Info().Msg(line)
			}
		}
		e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
		if !promptYesNo(ctx, "  Add to Sonarr?") {
			return nil
		}
	}

	title := evt.Title.Title
	year := evt.Title.Year
	profileID := e.resolveSonarrProfileID(ctx, e.cfg.Library.Sonarr.QualityProfile)

	langProfiles, err := e.sonarr.GetLanguageProfiles(ctx)
	if err != nil {
		return err
	}
	langProfileID := 1
	if len(langProfiles) > 0 {
		langProfileID = langProfiles[0].ID
	}

	lookup, err := e.sonarr.Lookup(ctx, tvdbID)
	if err != nil {
		return err
	}
	if lookup == nil {
		return fmt.Errorf("series not found on TVDB (tvdb_id=%d)", tvdbID)
	}

	var seasons []library.SonarrSeason
	for _, s := range lookup.Seasons {
		if s.SeasonNumber > 0 {
			monitored := false
			if monitorTarget && s.SeasonNumber == season {
				monitored = true
			}
			seasons = append(seasons, library.SonarrSeason{
				SeasonNumber: s.SeasonNumber,
				Monitored:    monitored,
			})
		}
	}

	added, err := e.sonarr.Add(ctx, tvdbID, title, year, library.AddSeriesOptions{
		Monitored:         true,
		SeasonFolder:      e.cfg.Library.Sonarr.SeasonFolders,
		QualityProfileID:  profileID,
		LanguageProfileID: langProfileID,
		RootFolderPath:    e.cfg.Library.Sonarr.RootFolder,
		Seasons:           seasons,
		SearchForMissing:  searchNow,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Sonarr")

	if season > 1 && added.ID > 0 {
		mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
		for s := 1; s < season; s++ {
			enqueue := false
			if mode == "auto" || mode == "yolo" {
				enqueue = true
				e.log.Info().Str("title", title).Int("season", s).Msg("auto-queueing earlier season search")
			} else {
				enqueue = promptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", title, s))
			}
			if enqueue {
				e.phase3Seasons = append(e.phase3Seasons, struct {
					SeriesID     int
					SeasonNumber int
					SeriesTitle  string
					MediaType    model.MediaType
				}{
					SeriesID:     added.ID,
					SeasonNumber: s,
					SeriesTitle:  title,
					MediaType:    evt.Title.MediaType,
				})
			}
		}
	}

	return nil
}

func findSeasonByNumber(seasons []library.SonarrSeason, num int) *library.SonarrSeason {
	for i := range seasons {
		if seasons[i].SeasonNumber == num {
			return &seasons[i]
		}
	}
	return nil
}

func (e *Executor) checkExistingSonarrSeasons(ctx context.Context, series *library.SonarrSeries, currentSeason int) []library.SonarrSeason {
	var missing []library.SonarrSeason
	for _, s := range series.Seasons {
		if s.SeasonNumber == 0 || s.SeasonNumber >= currentSeason {
			continue
		}
		if s.Statistics != nil && s.Statistics.EpisodeFileCount > 0 {
			continue
		}
		if s.Statistics != nil && s.Statistics.TotalEpisodeCount == 0 {
			continue
		}
		missing = append(missing, s)
	}
	if len(missing) > 0 {
		e.log.Info().Str("series", series.Title).Msgf("Found %d earlier season(s) missing in Sonarr", len(missing))
	} else {
		e.log.Info().Str("series", series.Title).Msg("all earlier seasons already have files in Sonarr")
	}
	return missing
}

func (e *Executor) checkCollectionGaps(ctx context.Context, tmdbID int, colTMDBID int, profileID int, searchNow bool) []Phase3Movie {
	allMovies, err := e.radarr.GetAllMovies(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Radarr error fetching movie list: %v\n", err)
		fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip collections  [q] quit pipeline\n")
		fmt.Fprintf(os.Stderr, "  Choose: ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "r", "retry":
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID, searchNow)
			case "q", "quit":
				return nil
			}
		case <-ctx.Done():
			return nil
		}
		return nil
	}

	movieByTMDB := make(map[int]library.RadarrMovie, len(allMovies))
	for _, m := range allMovies {
		movieByTMDB[m.TMDBID] = m
	}

	collections, err := e.radarr.GetCollections(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Radarr collections error: %v\n", err)
		fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip collections  [q] quit pipeline\n")
		fmt.Fprintf(os.Stderr, "  Choose: ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "r", "retry":
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID, searchNow)
			case "q", "quit":
				return nil
			}
		case <-ctx.Done():
			return nil
		}
		return nil
	}

	var phase3 []Phase3Movie

	for _, col := range collections {
		if col.TMDBID != colTMDBID {
			continue
		}
		var missing []library.RadarrMovie
		for _, m := range col.Movies {
			if m.TMDBID == tmdbID {
				continue
			}
			if m.Status != "" && m.Status != "released" {
				e.log.Debug().Str("title", m.Title).Str("status", m.Status).Int("tmdb", m.TMDBID).Msg("collection movie not released, skipping")
				continue
			}
			if existing, ok := movieByTMDB[m.TMDBID]; ok && existing.HasFile {
				continue
			}
			missing = append(missing, m)
		}
		if len(missing) == 0 {
			e.log.Info().Str("collection", col.Name).Msg("no missing movies in collection")
			continue
		}
		e.log.Info().Str("collection", col.Name).Msgf("collection has %d missing movie(s)", len(missing))
		for _, m := range missing {
			addIt := false
			mode := e.cfg.MediaTypeMode(model.MediaTypeMovie)
			if mode == "auto" || mode == "yolo" {
				addIt = true
				e.log.Info().Str("title", m.Title).Str("collection", col.Name).Msg("auto-adding collection movie")
			} else {
				addIt = promptYesNo(ctx, fmt.Sprintf("    Collection %q: Add %s?", col.Name, m.Title))
			}
			if addIt {
				if _, err := e.radarr.Add(ctx, m.TMDBID, m.Title, m.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           searchNow,
				}); err != nil {
					e.log.Warn().Err(err).Str("title", m.Title).Msg("error adding movie to collection")
				} else {
					e.log.Info().Str("title", m.Title).Msg("added movie to collection")
					phase3 = append(phase3, Phase3Movie{
						Title:     m.Title,
						Year:      m.Year,
						TMDBID:    m.TMDBID,
						ProfileID: profileID,
						RootPath:  e.cfg.Library.Radarr.RootFolder,
					})
				}
			}
		}
	}
	return phase3
}

func promptYesNo(ctx context.Context, prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		ch <- scanner.Text()
	}()
	select {
	case ans := <-ch:
		return strings.ToLower(strings.TrimSpace(ans)) == "y" || strings.ToLower(strings.TrimSpace(ans)) == "yes"
	case <-ctx.Done():
		return false
	}
}

// ─── Music album processing ─────────────────────────────────────────────

type MusicSearchResult struct {
	Event db.EventWithAlbum
	Top   []quality.ParsedRelease
	Error error
}

type MusicAlbumResult struct {
	Event      db.EventWithAlbum
	Downloaded bool
}

func musicSearchQueries(artist, album string, year int) []string {
	artist = quality.StripAccents(artist)
	album = quality.StripAccents(album)
	cleanArtist := quality.CleanArtist(artist)
	cleanAlbum := quality.CleanAlbum(album)

	raw := []string{
		fmt.Sprintf("%s %s %d", artist, album, year),
		fmt.Sprintf("%s %s", artist, album),
		fmt.Sprintf("%s %d", artist, year),
		fmt.Sprintf("%s %s %d", cleanArtist, cleanAlbum, year),
		fmt.Sprintf("%s %s", cleanArtist, cleanAlbum),
		fmt.Sprintf("%s %d", cleanAlbum, year),
		cleanAlbum,
	}

	seen := make(map[string]bool)
	var queries []string
	for _, q := range raw {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		queries = append(queries, q)
	}
	return queries
}

func (e *Executor) searchMusicRelease(ctx context.Context, artist, album string, year int) ([]quality.ParsedRelease, error) {
	queries := musicSearchQueries(artist, album, year)
	preferredID := e.prowl.PreferredIndexerID(search.CatMusic)
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	if preferredID > 0 {
		name := e.prowl.GetIndexerName(ctx, preferredID)
		e.log.Info().Str("name", name).Int("id", preferredID).Msg("preferred indexer (music)")

		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] preferred: %s", i+1, numTiers, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       "music",
				IndexerID:  preferredID,
				Limit:      50,
				Categories: []int{search.CatMusic},
			})
			if err != nil {
				return nil, err
			}
			exact, fuzzy := quality.PartitionMusicReleases(results, artist, album)
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= e.cfg.ShowTopN {
				return exactPool, nil
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	for i, q := range queries {
		e.log.Info().Msgf("[%d/%d] searching all: %s", i+1, numTiers, q)
		results, err := e.prowl.SearchMusic(ctx, q)
		if err != nil {
			return nil, err
		}
		exact, fuzzy := quality.PartitionMusicReleases(results, artist, album)
		exactPool = mergeReleases(exactPool, exact)
		fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
		e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
		if len(exactPool) >= e.cfg.ShowTopN {
			return exactPool, nil
		}
	}

	if len(exactPool) > 0 || len(fuzzyPool) > 0 {
		result := exactPool
		if n := e.cfg.ShowTopN - len(exactPool); n > 0 && len(fuzzyPool) > 0 {
			if n > len(fuzzyPool) {
				n = len(fuzzyPool)
			}
			result = append(result, fuzzyPool[:n]...)
		}
		return result, nil
	}
	return nil, nil
}

func (e *Executor) SearchMusicRelease(ctx context.Context, ae db.EventWithAlbum) (*MusicSearchResult, error) {
	e.log.Info().Str("artist", ae.Artist.Name).Str("album", ae.Album.Title).Int("year", ae.Album.Year).Msg("searching music")

	releases, err := e.searchMusicRelease(ctx, ae.Artist.Name, ae.Album.Title, ae.Album.Year)
	if err != nil {
		return nil, fmt.Errorf("searching music: %w", err)
	}

	if len(releases) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s - %s", ae.Artist.Name, ae.Album.Title))
		return &MusicSearchResult{Event: ae}, nil
	}

	// Parse with music quality
	for i := range releases {
		musicParsed := quality.ParseMusicRelease(releases[i].RawTitle)
		releases[i].Source = musicParsed.Source
		releases[i].Codec = musicParsed.Codec
	}

	prefs := quality.MusicQualityPrefs{
		FormatPriority:  e.cfg.Quality.Music.FormatPriority,
		BitratePriority: e.cfg.Quality.Music.BitratePriority,
		MinSeeders:      e.cfg.MinSeeders,
		PreferredGroups: e.cfg.PreferredGroups,
	}
	top := quality.SortMusicTop(releases, prefs, e.cfg.ShowTopN)

	return &MusicSearchResult{Event: ae, Top: top}, nil
}

// SearchMusicAll searches all music events and returns results.
func (e *Executor) SearchMusicAll(ctx context.Context, events []db.EventWithAlbum) []*MusicSearchResult {
	e.log.Info().Msgf("Searching %d music album(s)...", len(events))
	results := make([]*MusicSearchResult, 0, len(events))
	for i, ae := range events {
		e.log.Info().Str("album", ae.Album.Title).Str("artist", ae.Artist.Name).Msgf("[%d/%d] searching", i+1, len(events))
		sr, err := e.SearchMusicRelease(ctx, ae)
		if err != nil {
			e.log.Warn().Err(err).Str("album", ae.Album.Title).Str("artist", ae.Artist.Name).Msg("error searching music")
			continue
		}
		results = append(results, sr)
	}
	return results
}

// PickMusicResults presents pickers for pre-searched music results and downloads.
func (e *Executor) PickMusicResults(ctx context.Context, results []*MusicSearchResult) []MusicAlbumResult {
	var albumResults []MusicAlbumResult
	for _, sr := range results {
		result := e.PickMusicAlbum(ctx, sr)
		if result != nil {
			albumResults = append(albumResults, *result)
		}
	}
	return albumResults
}

// ProcessMusicAlbumsInteractive processes music albums one at a time in
// interactive mode: for each event, searches Prowlarr, shows picker, downloads,
// and returns the results for library processing.
func (e *Executor) ProcessMusicAlbumsInteractive(ctx context.Context, events []db.EventWithAlbum) []MusicAlbumResult {
	e.log.Info().Msgf("Processing %d music album(s) interactively...", len(events))
	var albumResults []MusicAlbumResult
	for _, ae := range events {
		select {
		case <-ctx.Done():
			return albumResults
		default:
		}
		e.log.Info().Str("artist", ae.Artist.Name).Str("album", ae.Album.Title).Msgf("processing album")
		sr, err := e.SearchMusicRelease(ctx, ae)
		if err != nil {
			e.log.Warn().Err(err).Str("album", ae.Album.Title).Str("artist", ae.Artist.Name).Msg("error searching music")
			continue
		}
		result := e.PickMusicAlbum(ctx, sr)
		if result != nil {
			albumResults = append(albumResults, *result)
		}
	}
	return albumResults
}

func (e *Executor) ProcessMusicAlbum(ctx context.Context, ae db.EventWithAlbum) (*MusicAlbumResult, error) {
	sr, err := e.SearchMusicRelease(ctx, ae)
	if err != nil {
		return nil, err
	}
	return e.PickMusicAlbum(ctx, sr), nil
}

func (e *Executor) PickMusicAlbum(ctx context.Context, sr *MusicSearchResult) *MusicAlbumResult {
	ae := sr.Event
	if len(sr.Top) == 0 {
		e.log.Info().Str("artist", ae.Artist.Name).Str("album", ae.Album.Title).Msg("no music results found")
		return &MusicAlbumResult{Event: ae}
	}

	// Show picker
	selLabel := ae.Artist.Name + " - " + ae.Album.Title
	sel := NewSelector(selLabel, sr.Top)
	chosen, err := sel.Run()
	if err != nil {
		return &MusicAlbumResult{Event: ae}
	}
	if len(chosen) == 0 {
		label := ae.Artist.Name + " - " + ae.Album.Title
		e.Skipped = append(e.Skipped, label)
		e.handleSkipLibraryMusic(ctx, ae)
		return &MusicAlbumResult{Event: ae}
	}

	// Download
	category := e.cfg.Downloader.Categories.Music
	if category == "" {
		category = "Music"
	}
	var releaseEventID int64
	if ae.Event != nil {
		releaseEventID = ae.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Str("artist", ae.Artist.Name).Str("album", ae.Album.Title).Int("score", release.Score).Msg("selected music release")
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		if uri != "" {
			tid, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category))
			if err != nil {
				e.log.Warn().Err(err).Msg("music add failed")
			} else {
				e.log.Info().Str("client", e.cfg.Downloader.Type).Str("category", category).Str("torrent_id", tid).Msg("added music to download client")
				_ = tid
			}
		}

		// Create download record in DB
		dl := &model.Download{
			TitleID:         0,
			ReleaseEventID:  releaseEventID,
			Quality:         release.Source,
			SourceType:      release.Source,
			Codec:           release.Codec,
			InfoHash:        release.InfoHash,
			Category:        category,
			Status:          model.DownloadAdded,
			ClientTorrentID: "",
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("saving music download record")
		}

		_ = e.db.UpdateAlbumReleaseEventStatus(ctx, releaseEventID, model.StatusDownloaded)
	}

	return &MusicAlbumResult{Event: ae, Downloaded: true}
}

func (e *Executor) ProcessMusicAlbumDecisions(ctx context.Context, results []MusicAlbumResult) {
	if len(results) == 0 || e.lidarr == nil {
		return
	}

	mode := e.cfg.MediaTypeMode(model.MediaTypeMusic)
	autoConfirm := mode == "auto" || mode == "yolo"

	fmt.Fprintln(os.Stderr, "\n── Lidarr decisions ──")

	type retryAction int
	const (
		retryActionSkip retryAction = iota
		retryActionRetry
		retryActionQuit
	)

	promptRetry := func(label string, err error) retryAction {
		fmt.Fprintf(os.Stderr, "  %s error: %v\n", label, err)
		for {
			fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip this item  [q] quit pipeline\n")
			fmt.Fprintf(os.Stderr, "  Choose: ")
			ch := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				ch <- scanner.Text()
			}()
			select {
			case ans := <-ch:
				switch strings.ToLower(strings.TrimSpace(ans)) {
				case "r", "retry":
					return retryActionRetry
				case "s", "skip":
					return retryActionSkip
				case "q", "quit":
					return retryActionQuit
				}
			case <-ctx.Done():
				return retryActionQuit
			}
		}
	}

	type albumDecision struct {
		evt        db.EventWithAlbum
		artistMbid string
	}

	var decisions []albumDecision

	for _, r := range results {
		if !r.Downloaded {
			continue
		}
		ae := r.Event
		artistMbid := ae.Artist.MBID
		if artistMbid == "" {
			continue
		}

		existing, err := e.lidarr.GetArtist(ctx, artistMbid)
		if err != nil {
			e.log.Warn().Err(err).Str("artist", ae.Artist.Name).Msg("lidarr check error")
			continue
		}
		if existing != nil {
			e.log.Info().Str("artist", ae.Artist.Name).Int("lidarr_id", existing.ID).Msg("artist already in Lidarr")
			continue
		}

		e.log.Info().Msgf("Add to Lidarr? %s — %s (%d)", ae.Artist.Name, ae.Album.Title, ae.Album.Year)
		if autoConfirm || promptYesNo(ctx, "  Add artist to Lidarr?") {
			decisions = append(decisions, albumDecision{
				evt:        ae,
				artistMbid: artistMbid,
			})
		}
	}

	if len(decisions) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Executing Lidarr adds ──")

decisionsLoop:
	for _, d := range decisions {
		ae := d.evt
	addRetry:
		e.log.Info().Str("artist", ae.Artist.Name).Msg("adding artist to Lidarr")

		qualProfileID, err := e.lidarr.ResolveQualityProfileID(ctx, e.cfg.Library.Lidarr.QualityProfile)
		if err != nil {
			e.log.Warn().Err(err).Msg("resolving quality profile")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		metaProfileID, err := e.lidarr.ResolveMetadataProfileID(ctx, e.cfg.Library.Lidarr.MetadataProfile)
		if err != nil {
			e.log.Warn().Err(err).Msg("resolving metadata profile")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		rootFolder := e.cfg.Library.Lidarr.RootFolder
		if rootFolder == "" {
			e.log.Warn().Msg("lidarr root_folder not configured, skipping")
			continue
		}

		monitor := e.cfg.Library.Lidarr.Monitor
		if monitor == "" {
			monitor = "all"
		}

		added, err := e.lidarr.AddArtist(ctx, d.artistMbid, ae.Artist.Name, library.AddArtistOptions{
			Monitored:         true,
			MonitorNewAlbums:  e.cfg.Library.Lidarr.MonitorNewAlbums,
			QualityProfileID:  qualProfileID,
			MetadataProfileID: metaProfileID,
			RootFolderPath:    rootFolder,
			Monitor:           monitor,
			SearchNow:         false,
		})
		if err != nil {
			e.log.Warn().Err(err).Str("artist", ae.Artist.Name).Msg("failed adding to Lidarr")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		_ = e.db.SetSetting(ctx, fmt.Sprintf("lidarr_artist_%s", d.artistMbid), fmt.Sprintf("%d", added.ID))
		e.log.Info().Int("lidarr_id", added.ID).Str("artist", ae.Artist.Name).Msg("added artist to Lidarr")
	}
}

func formatOverview(text string, maxWidth int) []string {
	var lines []string
	for _, w := range strings.Fields(text) {
		if len(lines) == 0 || len(lines[len(lines)-1])+len(w)+1 > maxWidth {
			lines = append(lines, w)
		} else {
			lines[len(lines)-1] += " " + w
		}
	}
	if len(lines) > 5 {
		lines = lines[:5]
		lines[4] += " ..."
	}
	return lines
}

// ─── Book Processing ──────────────────────────────────────────────────────

type BookSearchResult struct {
	Event  db.EventWithBook
	Format model.BookFormat
	Top    []quality.ParsedBookRelease
	Error  error
}

type BookDownloadInfo struct {
	EbookDownloaded     bool
	AudiobookDownloaded bool
}

func bookSearchQueries(evt db.EventWithBook) []string {
	var queries []string
	seen := make(map[string]bool)

	add := func(q string) {
		q = strings.TrimSpace(q)
		if q != "" && !seen[q] {
			seen[q] = true
			queries = append(queries, q)
		}
	}

	// Prefer exact IDs: these match structured indexers with ISBN/ASIN support
	add(evt.Book.ISBN13)
	add(evt.Book.ASIN)
	add(evt.Book.ISBN10)

	// Title + author tiers (for trackers without ID-based search)
	if evt.Author.Name != "" {
		if evt.Book.ReleaseYear > 0 {
			add(fmt.Sprintf("%s %s %d", evt.Author.Name, evt.Book.Title, evt.Book.ReleaseYear))
		}
		add(fmt.Sprintf("%s %s", evt.Author.Name, evt.Book.Title))
	}
	if evt.Book.ReleaseYear > 0 {
		add(fmt.Sprintf("%s %d", evt.Book.Title, evt.Book.ReleaseYear))
	}
	add(evt.Book.Title)

	// Fallback: strip subtitle (after ": ") — many torrents omit the subtitle
	// so the extra words can prevent finding results.
	mainTitle := evt.Book.Title
	if idx := strings.Index(mainTitle, ": "); idx > 0 {
		mainTitle = strings.TrimSpace(mainTitle[:idx])
	}
	if mainTitle != evt.Book.Title {
		if evt.Author.Name != "" {
			if evt.Book.ReleaseYear > 0 {
				add(fmt.Sprintf("%s %s %d", evt.Author.Name, mainTitle, evt.Book.ReleaseYear))
			}
			add(fmt.Sprintf("%s %s", evt.Author.Name, mainTitle))
		}
		if evt.Book.ReleaseYear > 0 {
			add(fmt.Sprintf("%s %d", mainTitle, evt.Book.ReleaseYear))
		}
		add(mainTitle)
	}

	return queries
}

func (e *Executor) SearchBook(ctx context.Context, evt db.EventWithBook, format model.BookFormat) *BookSearchResult {
	label := string(format)
	e.log.Info().Str("book", evt.Book.Title).Str("author", evt.Author.Name).Str("format", label).Msg("searching book")

	queries := bookSearchQueries(evt)
	if len(queries) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	cat := search.CatBookEbook
	if format == model.BookFormatAudiobook {
		cat = search.CatAudioAudiobook
	}
	bookCatSets := [][]int{{cat}, {search.CatBook}}

	preferredID := e.prowl.PreferredIndexerID(cat)
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	// Phase 1: preferred indexer
	if preferredID > 0 {
		name := e.prowl.GetIndexerName(ctx, preferredID)
		e.log.Info().Str("name", name).Int("id", preferredID).Msg("preferred indexer (books)")

		for _, cats := range bookCatSets {
			for i, q := range queries {
				e.log.Info().Msgf("[%d/%d] preferred (cats=%v): %s", i+1, numTiers, cats, q)
				results, err := e.prowl.Search(ctx, search.SearchParams{
					Query:      q,
					Type:       "search",
					IndexerID:  preferredID,
					Limit:      50,
					Categories: cats,
				})
				if err != nil {
					e.log.Warn().Err(err).Str("book", evt.Book.Title).Msg("preferred indexer search failed")
					continue
				}
				exact, fuzzy := quality.PartitionBookReleasesRaw(results, evt.Author.Name, evt.Book.Title)
				exactPool = mergeReleases(exactPool, exact)
				fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
				e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
				if len(exactPool) >= e.cfg.ShowTopN {
					return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format)
				}
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	// Phase 2: all indexers
	for _, cats := range bookCatSets {
		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] searching all (cats=%v): %s", i+1, numTiers, cats, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       "search",
				Limit:      50,
				Categories: cats,
			})
			if err != nil {
				e.log.Warn().Err(err).Str("book", evt.Book.Title).Msg("prowlarr book search failed")
				continue
			}
			exact, fuzzy := quality.PartitionBookReleasesRaw(results, evt.Author.Name, evt.Book.Title)
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= e.cfg.ShowTopN {
				return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format)
			}
		}
	}

	return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format)
}

func (e *Executor) buildBookSearchResult(evt db.EventWithBook, exactPool, fuzzyPool []quality.ParsedRelease, format model.BookFormat) *BookSearchResult {
	label := string(format)
	if len(exactPool) == 0 && len(fuzzyPool) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	// Convert to ParsedBookRelease and parse format info
	parseBookResults := func(releases []quality.ParsedRelease) []quality.ParsedBookRelease {
		var result []quality.ParsedBookRelease
		for _, pr := range releases {
			br := quality.ParseBookRelease(pr.RawTitle)
			br.ParsedRelease = pr
			result = append(result, br)
		}
		return result
	}

	exactBooks := parseBookResults(exactPool)
	fuzzyBooks := parseBookResults(fuzzyPool)

	// Filter by the requested format — ensure results match what we're searching for
	switch format {
	case model.BookFormatEbook:
		exactBooks = filterEbookResults(exactBooks)
		fuzzyBooks = filterEbookResults(fuzzyBooks)
	case model.BookFormatAudiobook:
		exactBooks = filterAudiobookResults(exactBooks)
		fuzzyBooks = filterAudiobookResults(fuzzyBooks)
	}

	if len(exactBooks) == 0 && len(fuzzyBooks) == 0 {
		e.log.Info().Str("book", evt.Book.Title).Str("format", label).Msg("no results match format filter")
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	prefs := buildBookQualityPrefs(e.cfg)
	showTopN := e.cfg.ShowTopN

	var top []quality.ParsedBookRelease
	if len(exactBooks) > 0 {
		top = quality.SortBookTop(exactBooks, prefs, showTopN)
		if n := showTopN - len(top); n > 0 && len(fuzzyBooks) > 0 {
			fuzzyTop := quality.SortBookTop(fuzzyBooks, prefs, n)
			top = append(top, fuzzyTop...)
		}
	} else if len(fuzzyBooks) > 0 {
		top = quality.SortBookTop(fuzzyBooks, prefs, showTopN)
	}

	return &BookSearchResult{Event: evt, Format: format, Top: top}
}

func filterEbookResults(books []quality.ParsedBookRelease) []quality.ParsedBookRelease {
	var out []quality.ParsedBookRelease
	for _, b := range books {
		if b.IsEbook && b.EbookFormat != "" {
			out = append(out, b)
		} else if !b.IsAudiobook {
			b.IsEbook = true
			b.EbookFormat = "unknown"
			out = append(out, b)
		}
	}
	return out
}

func filterAudiobookResults(books []quality.ParsedBookRelease) []quality.ParsedBookRelease {
	var out []quality.ParsedBookRelease
	for _, b := range books {
		if b.IsAudiobook && b.AudiobookFormat != "" {
			out = append(out, b)
		} else if !b.IsEbook {
			b.IsAudiobook = true
			b.AudiobookFormat = "unknown"
			out = append(out, b)
		}
	}
	return out
}

func (e *Executor) presentBookPicker(ctx context.Context, sr *BookSearchResult) ([]quality.ParsedBookRelease, error) {
	if len(sr.Top) == 0 {
		return nil, nil
	}

	// Convert book releases to ParsedRelease for the selector
	parsed := make([]quality.ParsedRelease, len(sr.Top))
	for i, br := range sr.Top {
		parsed[i] = br.ParsedRelease
	}

	pickerTitle := sr.Event.Book.Title
	if sr.Format != "" {
		pickerTitle = fmt.Sprintf("%s [%s]", sr.Event.Book.Title, sr.Format)
	}
	sel := NewSelector(pickerTitle, parsed)
	chosen, err := sel.Run()
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, nil
	}

	// Map back to book releases
	var result []quality.ParsedBookRelease
	for _, c := range chosen {
		for _, br := range sr.Top {
			if br.Guid == c.Guid {
				result = append(result, br)
				break
			}
		}
	}
	return result, nil
}

func (e *Executor) addBookToClient(ctx context.Context, evt db.EventWithBook, chosen []quality.ParsedBookRelease, format model.BookFormat) int {
	cat := e.cfg.Downloader.Categories.Ebooks
	if format == model.BookFormatAudiobook {
		cat = e.cfg.Downloader.Categories.Audiobooks
	}

	var added int
	for _, r := range chosen {
		url := r.DownloadURL
		if url == "" {
			url = r.MagnetURL
		}
		if url == "" {
			e.log.Warn().Str("title", r.RawTitle).Msg("book release has no download URL or magnet")
			continue
		}

		var tid string
		var dlErr error
		if strings.HasPrefix(url, "magnet:") {
			tid, dlErr = e.dl.AddMagnet(ctx, url, download.WithCategory(cat))
			if dlErr != nil {
				e.log.Warn().Err(dlErr).Str("title", r.RawTitle).Msg("adding book magnet")
				continue
			}
			e.log.Info().Str("title", r.RawTitle).Str("tid", tid).Str("category", cat).Msg("book magnet added")
		} else {
			tid, dlErr = e.dl.AddTorrent(ctx, url, download.WithCategory(cat))
			if dlErr != nil {
				e.log.Warn().Err(dlErr).Str("title", r.RawTitle).Msg("adding book torrent")
				continue
			}
			e.log.Info().Str("title", r.RawTitle).Str("tid", tid).Str("category", cat).Msg("book torrent added")
		}

		// Determine quality from the parsed book format
		quality := r.EbookFormat
		if r.IsAudiobook && r.AudiobookFormat != "" {
			quality = r.AudiobookFormat
		}
		if quality == "" {
			quality = r.Codec
		}

		// Record the download
		dl := &model.BookDownload{
			BookID:           evt.Book.ID,
			BookReleaseEvent: evt.Event.ID,
			Format:           format,
			Quality:          quality,
			SourceType:       r.Source,
			Codec:            r.Codec,
			InfoHash:         r.InfoHash,
			Category:         cat,
			ClientTorrentID:  tid,
			Status:           model.DownloadAdded,
		}
		if _, err := e.db.CreateBookDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("saving book download record")
		}
		added++
	}
	return added
}

func (e *Executor) markBookDownloaded(ctx context.Context, evt db.EventWithBook) {
	if err := e.db.UpdateBookReleaseEventStatus(ctx, evt.Event.ID, model.StatusDownloaded); err != nil {
		e.log.Warn().Err(err).Msg("marking book as downloaded")
	}
}

func (e *Executor) PickBook(ctx context.Context, sr *BookSearchResult) (int, error) {
	if len(sr.Top) == 0 {
		return 0, nil
	}
	chosen, err := e.presentBookPicker(ctx, sr)
	if err != nil {
		return 0, err
	}
	if len(chosen) == 0 {
		label := fmt.Sprintf("%s by %s [%s]", sr.Event.Book.Title, sr.Event.Author.Name, sr.Format)
		e.Skipped = append(e.Skipped, label)
		return 0, nil
	}
	return e.addBookToClient(ctx, sr.Event, chosen, sr.Format), nil
}

// SearchBooksAll searches all book events (ebook + audiobook formats) and returns results.
func (e *Executor) SearchBooksAll(ctx context.Context, events []db.EventWithBook) []*BookSearchResult {
	e.log.Info().Msgf("Searching %d book(s)...", len(events))
	var results []*BookSearchResult
	for i, evt := range events {
		e.log.Info().Str("book", evt.Book.Title).Str("author", evt.Author.Name).Msgf("[%d/%d] searching", i+1, len(events))
		pref := evt.Event.FormatPref
		ebookDone := evt.Event.EbookProcessed
		audiobookDone := evt.Event.AudiobookProcessed
		needsEbook := (pref == model.BookFormatEbook || pref == model.BookFormatBoth) && !ebookDone
		needsAudiobook := (pref == model.BookFormatAudiobook || pref == model.BookFormatBoth) && !audiobookDone
		if needsEbook {
			results = append(results, e.SearchBook(ctx, evt, model.BookFormatEbook))
		}
		if needsAudiobook {
			results = append(results, e.SearchBook(ctx, evt, model.BookFormatAudiobook))
		}
	}
	return results
}

// PickBookResults presents pickers for pre-searched book results and downloads.
func (e *Executor) PickBookResults(ctx context.Context, results []*BookSearchResult) map[int64]*BookDownloadInfo {
	done := make(map[int64]*BookDownloadInfo)
	events := make(map[int64]db.EventWithBook)

	for _, sr := range results {
		n, err := e.PickBook(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("book", sr.Event.Book.Title).Msg("book picker error")
			continue
		}
		if n > 0 {
			if err := e.db.MarkBookFormatProcessed(ctx, sr.Event.Event.ID, sr.Format); err != nil {
				e.log.Warn().Err(err).Msg("marking book format processed")
			}
			id := sr.Event.Event.ID
			if _, ok := done[id]; !ok {
				done[id] = &BookDownloadInfo{
					EbookDownloaded:     sr.Event.Event.EbookProcessed,
					AudiobookDownloaded: sr.Event.Event.AudiobookProcessed,
				}
				events[id] = sr.Event
			}
			switch sr.Format {
			case model.BookFormatEbook:
				done[id].EbookDownloaded = true
			case model.BookFormatAudiobook:
				done[id].AudiobookDownloaded = true
			}
		}
	}

	for id, info := range done {
		pref := events[id].Event.FormatPref
		allDone := false
		switch pref {
		case model.BookFormatEbook:
			allDone = info.EbookDownloaded
		case model.BookFormatAudiobook:
			allDone = info.AudiobookDownloaded
		case model.BookFormatBoth:
			allDone = info.EbookDownloaded && info.AudiobookDownloaded
		}
		if allDone {
			e.markBookDownloaded(ctx, events[id])
		}
	}
	return done
}

func (e *Executor) ProcessBooks(ctx context.Context, events []db.EventWithBook) map[int64]*BookDownloadInfo {
	if len(events) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "\n── Book Processing ──")

	downloaded := make(map[int64]*BookDownloadInfo)

	for _, evt := range events {
		title := evt.Book.Title
		author := evt.Author.Name
		pref := evt.Event.FormatPref
		ebookDone := evt.Event.EbookProcessed
		audiobookDone := evt.Event.AudiobookProcessed

		needsEbook := (pref == model.BookFormatEbook || pref == model.BookFormatBoth) && !ebookDone
		needsAudiobook := (pref == model.BookFormatAudiobook || pref == model.BookFormatBoth) && !audiobookDone

		if !needsEbook && !needsAudiobook {
			continue
		}

		info := &BookDownloadInfo{
			EbookDownloaded:     ebookDone,
			AudiobookDownloaded: audiobookDone,
		}

		// Process ebook format
		if needsEbook {
			fmt.Fprintf(os.Stderr, "\n  Searching %s by %s [ebook]\n", title, author)
			sr := e.SearchBook(ctx, evt, model.BookFormatEbook)
			if len(sr.Top) > 0 {
				n, err := e.PickBook(ctx, sr)
				if errors.Is(err, ErrAbort) {
					e.log.Info().Msg("pipeline aborted by user")
					return nil
				}
				if err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("ebook picker error")
				} else if n > 0 {
					if err := e.db.MarkBookFormatProcessed(ctx, evt.Event.ID, model.BookFormatEbook); err != nil {
						e.log.Warn().Err(err).Msg("marking ebook processed")
					}
					info.EbookDownloaded = true
					fmt.Fprintf(os.Stderr, "  ✓ %s by %s [ebook] (%d added)\n", title, author, n)
				} else {
					fmt.Fprintf(os.Stderr, "  %s by %s [ebook]: skipped\n", title, author)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  %s by %s [ebook]: no results\n", title, author)
			}
		}

		// Process audiobook format
		if needsAudiobook {
			fmt.Fprintf(os.Stderr, "\n  Searching %s by %s [audiobook]\n", title, author)
			sr := e.SearchBook(ctx, evt, model.BookFormatAudiobook)
			if len(sr.Top) > 0 {
				n, err := e.PickBook(ctx, sr)
				if errors.Is(err, ErrAbort) {
					e.log.Info().Msg("pipeline aborted by user")
					return nil
				}
				if err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("audiobook picker error")
				} else if n > 0 {
					if err := e.db.MarkBookFormatProcessed(ctx, evt.Event.ID, model.BookFormatAudiobook); err != nil {
						e.log.Warn().Err(err).Msg("marking audiobook processed")
					}
					info.AudiobookDownloaded = true
					fmt.Fprintf(os.Stderr, "  ✓ %s by %s [audiobook] (%d added)\n", title, author, n)
				} else {
					fmt.Fprintf(os.Stderr, "  %s by %s [audiobook]: skipped\n", title, author)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  %s by %s [audiobook]: no results\n", title, author)
			}
		}

		// Check if all required formats are done
		allDone := false
		switch pref {
		case model.BookFormatEbook:
			allDone = info.EbookDownloaded
		case model.BookFormatAudiobook:
			allDone = info.AudiobookDownloaded
		case model.BookFormatBoth:
			allDone = info.EbookDownloaded && info.AudiobookDownloaded
		}
		if allDone {
			e.markBookDownloaded(ctx, evt)
		}
		downloaded[evt.Event.ID] = info
	}
	return downloaded
}

func (e *Executor) BookClientAvailable() bool {
	return e.bookClient != nil
}

func (e *Executor) ProcessBookLibraryDecisions(ctx context.Context, events []db.EventWithBook, downloaded map[int64]*BookDownloadInfo) {
	mode := e.cfg.MediaTypeMode(model.MediaTypeBook)
	autoConfirm := mode == "auto" || mode == "yolo"

	if e.bookClient == nil {
		return
	}

	for _, evt := range events {
		if evt.Book.HardcoverID == 0 {
			e.log.Warn().Str("book", evt.Book.Title).Msg("no HardcoverID, skipping LazyLibrarian")
			continue
		}

		title := evt.Book.Title
		author := evt.Author.Name
		bookID := strconv.Itoa(int(evt.Book.HardcoverID))

		if !autoConfirm {
			label := fmt.Sprintf("  Add \"%s\" by %s to LazyLibrarian?", title, author)
			if !promptYesNo(ctx, label) {
				e.log.Info().Str("book", title).Msg("skipped LazyLibrarian (user declined)")
				continue
			}
		}

		if _, err := e.bookClient.AddBook(ctx, bookID); err != nil {
			e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian addBook")
			continue
		}

		info := downloaded[evt.Event.ID]
		pref := evt.Event.FormatPref

		// Per-format decisions: if format was downloaded, set Skipped (qbit script handles import).
		// If format was not downloaded (skipped or no results), set Wanted so LL searches.
		ebookWanted := pref == model.BookFormatEbook || pref == model.BookFormatBoth
		audiobookWanted := pref == model.BookFormatAudiobook || pref == model.BookFormatBoth

		if ebookWanted {
			if info != nil && info.EbookDownloaded {
				if err := e.bookClient.UnqueueBook(ctx, bookID, model.BookFormatEbook); err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian unqueueBook ebook")
				}
			} else {
				if err := e.bookClient.QueueBook(ctx, bookID, model.BookFormatEbook); err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian queueBook ebook")
				}
			}
		}

		if audiobookWanted {
			if info != nil && info.AudiobookDownloaded {
				if err := e.bookClient.UnqueueBook(ctx, bookID, model.BookFormatAudiobook); err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian unqueueBook audiobook")
				}
			} else {
				if err := e.bookClient.QueueBook(ctx, bookID, model.BookFormatAudiobook); err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian queueBook audiobook")
				}
			}
		}

		e.log.Info().Str("book", title).Str("ll_id", bookID).Msg("LazyLibrarian: added")

		// Phase 3: series gap detection
		if evt.Book.SeriesID != "" {
			searchNow := mode == "arr" || mode == "auto" || mode == "yolo"
			e.checkBookSeriesGaps(ctx, evt, searchNow)
		}
	}
}

func (e *Executor) checkBookSeriesGaps(ctx context.Context, evt db.EventWithBook, searchNow bool) {
	members, err := e.bookClient.GetSeriesMembers(ctx, evt.Book.SeriesID)
	if err != nil {
		e.log.Warn().Err(err).Str("book", evt.Book.Title).Str("series", evt.Book.SeriesName).Msg("series gap: GetSeriesMembers failed")
		return
	}

	var added int
	for _, m := range members {
		if m.BookID == "" {
			continue
		}

		// Skip future releases
		if m.PubDate != "" {
			t, parseErr := time.Parse("2006-01-02", m.PubDate)
			if parseErr != nil {
				if len(m.PubDate) >= 4 {
					t, parseErr = time.Parse("2006", m.PubDate[:4])
				}
			}
			if parseErr == nil && t.After(time.Now()) {
				e.log.Debug().Str("book", m.Title).Str("pubdate", m.PubDate).Msg("series gap: future release, skipping")
				continue
			}
		}

		// AddBook is idempotent — safe for books already in LL
		if _, err := e.bookClient.AddBook(ctx, m.BookID); err != nil {
			e.log.Warn().Err(err).Str("book", m.Title).Msg("series gap: addBook failed")
			continue
		}

		if !searchNow {
			if err := e.bookClient.UnqueueBook(ctx, m.BookID, model.BookFormatEbook); err != nil {
				e.log.Warn().Err(err).Str("book", m.Title).Msg("series gap: unqueueBook ebook")
			}
			if err := e.bookClient.UnqueueBook(ctx, m.BookID, model.BookFormatAudiobook); err != nil {
				e.log.Warn().Err(err).Str("book", m.Title).Msg("series gap: unqueueBook audiobook")
			}
		}

		added++
		e.log.Info().Str("book", m.Title).Str("author", m.AuthorName).Int("position", m.Position).Msg("series gap: added to LazyLibrarian")
	}

	if added > 0 {
		e.log.Info().Str("series", evt.Book.SeriesName).Int("added", added).Msg("series gap check complete")
	}
}

func buildBookQualityPrefs(cfg *config.Config) quality.BookQualityPrefs {
	return quality.BookQualityPrefs{
		EbookFormatPriority:     cfg.Quality.Books.Ebooks.FormatPriority,
		AudiobookFormatPriority: cfg.Quality.Books.Audiobooks.FormatPriority,
		MinSeeders:              cfg.MinSeeders,
		PreferredGroups:         cfg.PreferredGroups,
	}
}

func buildQualityPrefs(cfg *config.Config, mediaType model.MediaType) quality.QualityPrefs {
	var qc config.MediaQualityConfig
	if mediaType == model.MediaTypeTV {
		qc = cfg.Quality.TV
	} else {
		qc = cfg.Quality.Movies
	}

	res := 1080
	switch qc.Resolution {
	case "2160p", "4k", "uhd":
		res = 2160
	case "1080p":
		res = 1080
	case "720p":
		res = 720
	}

	return quality.QualityPrefs{
		TargetResolution: res,
		PreferHDR:        qc.PreferHDR,
		SourcePriority:   qc.SourcePriority,
		CodecPriority:    qc.CodecPriority,
		PreferredGroups:  cfg.PreferredGroups,
		MinSeeders:       cfg.MinSeeders,
	}
}
