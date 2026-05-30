package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

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

type PickedItem struct {
	Event  db.EventWithTitle
	Season int
	Chosen []quality.ParsedRelease
}

type Executor struct {
	log    zerolog.Logger
	cfg    *config.Config
	db     *db.DB
	prowl  *search.ProwlarrClient
	dl     download.Client
	radarr *library.RadarrClient
	sonarr *library.SonarrClient
	lidarr *library.LidarrClient

	Unfound       []string
	phase3Movies  []Phase3Movie
	phase3Seasons []struct {
		SeriesID     int
		SeasonNumber int
		SeriesTitle  string
	}
}

func NewExecutor(logger zerolog.Logger, cfg *config.Config, database *db.DB) *Executor {
	dl := createDownloadClient(logger, cfg)
	radarr := library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey, cfg.Library.Radarr.Timeout)
	sonarr := library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey, cfg.Library.Sonarr.Timeout)
	var lidarr *library.LidarrClient
	if cfg.Library.Lidarr.URL != "" {
		lidarr = library.NewLidarrClient(cfg.Library.Lidarr.URL, cfg.Library.Lidarr.APIKey, cfg.Library.Lidarr.Timeout)
	}
	return &Executor{
		log:    logger,
		cfg:    cfg,
		db:     database,
		prowl:  search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey, cfg.Prowlarr.Timeout),
		dl:     dl,
		radarr: radarr,
		sonarr: sonarr,
		lidarr: lidarr,
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
	if _, err := e.radarr.GetAllMovies(ctx); err != nil {
		e.log.Warn().Err(err).Msg("background pre-warm Radarr")
	}
}

func (e *Executor) PreWarmSonarr(ctx context.Context) {
	if _, err := e.sonarr.GetAllSeries(ctx); err != nil {
		e.log.Warn().Err(err).Msg("background pre-warm Sonarr")
	}
}

func (e *Executor) HealthCheck(ctx context.Context) HealthCheckResult {
	var result HealthCheckResult

	if err := e.prowl.Ping(ctx); err != nil {
		result.Critical = append(result.Critical, fmt.Sprintf("Prowlarr: %v", err))
	}
	if e.dl != nil {
		if err := e.dl.Ping(ctx); err != nil {
			result.Critical = append(result.Critical, fmt.Sprintf("Downloader (%s): %v", e.cfg.Downloader.Type, err))
		}
	}
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
	if title.MediaType == model.MediaTypeTV {
		category = e.cfg.Downloader.Categories.TV
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
// processing.
func (e *Executor) SearchAndPickAll(ctx context.Context, events []db.EventWithTitle) []PickedItem {
	results := e.SearchAll(ctx, events)
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
			continue
		}
		e.addToClient(ctx, sr.Event, chosen)
		e.handleUpgrade(ctx, sr.Event)
		e.markDownloaded(ctx, sr.Event)
		picked = append(picked, PickedItem{Event: sr.Event, Season: sr.Season, Chosen: chosen})
	}
	return picked
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
		return nil
	}
	e.addToClient(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)
	e.markDownloaded(ctx, evt)
	return &PickedItem{Event: evt, Season: sr.Season, Chosen: chosen}
}

// ProcessLibraryDecisions implements Phase 2 + Phase 3, shared by both
// batch and interactive modes. For each picked item it asks library questions
// (add to Radarr/Sonarr, collection gaps, earlier seasons), executes adds,
// then searches Prowlarr for approved collection movies and earlier seasons.
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
		evt    db.EventWithTitle
		season int
		isTV   bool
		tmdbID int
		tvdbID int
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
		if item.Event.Title.MediaType == model.MediaTypeMovie {
			tmdbID := item.Event.Title.TmdbID
			if tmdbID == 0 {
				continue
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
						phase3FromCollection := e.checkCollectionGaps(ctx, existing.TMDBID, existing.Collection.TMDBID, profileID)
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
			if promptYesNo(ctx, "  Add to Radarr?") {
				addActions = append(addActions, addAction{
					evt:    item.Event,
					isTV:   false,
					tmdbID: tmdbID,
				})
			}
		} else {
			tvdbID := item.Event.Title.TvdbID
			if tvdbID == 0 {
				continue
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
							if promptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", existing.Title, missingS.SeasonNumber)) {
								e.phase3Seasons = append(e.phase3Seasons, struct {
									SeriesID     int
									SeasonNumber int
									SeriesTitle  string
								}{
									SeriesID:     existing.ID,
									SeasonNumber: missingS.SeasonNumber,
									SeriesTitle:  existing.Title,
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
			if promptYesNo(ctx, "  Add to Sonarr?") {
				addActions = append(addActions, addAction{
					evt:    item.Event,
					season: item.Season,
					isTV:   true,
					tvdbID: tvdbID,
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
				addErr = e.addToSonarr(ctx, a.evt, a.season, true)
			} else {
				addErr = e.addToRadarr(ctx, a.evt, true)
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

	// Phase 3: Search Prowlarr for approved collection movies and earlier seasons
	if len(e.phase3Movies) > 0 {
		fmt.Fprintln(os.Stderr, "\n── Searching for collection movies ──")
		for _, p3m := range e.phase3Movies {
			e.searchPhase3Movie(ctx, p3m)
		}
	}
	if len(e.phase3Seasons) > 0 {
		fmt.Fprintln(os.Stderr, "\n── Searching for earlier seasons ──")
		for _, p3s := range e.phase3Seasons {
			e.searchPhase3Season(ctx, p3s)
		}
	}
}

func (e *Executor) searchPhase3Movie(ctx context.Context, m Phase3Movie) {
	title := m.Title
	stripped := quality.StripSeason(title)
	season := quality.ParseSeasonNumber(title)

	releases, err := e.searchRelease(ctx, &model.Title{
		Title:     title,
		Year:      m.Year,
		MediaType: model.MediaTypeMovie,
	}, stripped, season)
	if err != nil {
		e.log.Warn().Err(err).Str("title", title).Msg("error searching collection movie")
		return
	}

	prefs := buildQualityPrefs(e.cfg, model.MediaTypeMovie)
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
	if len(top) == 0 {
		e.log.Info().Str("title", title).Msg("no results for collection movie")
		return
	}

	sr := &SearchResult{
		Event:  db.EventWithTitle{Title: &model.Title{Title: title, Year: m.Year, MediaType: model.MediaTypeMovie, TmdbID: m.TMDBID}},
		Season: season,
		Top:    top,
	}

	chosen, err := e.presentPicker(ctx, sr)
	if err != nil || len(chosen) == 0 {
		return
	}

	// Create a synthetic event for tracking
	synthEvent := db.EventWithTitle{
		Title: &model.Title{
			Title:     title,
			Year:      m.Year,
			MediaType: model.MediaTypeMovie,
			TmdbID:    m.TMDBID,
		},
	}
	e.addToClient(ctx, synthEvent, chosen)
}

func (e *Executor) searchPhase3Season(ctx context.Context, s struct {
	SeriesID     int
	SeasonNumber int
	SeriesTitle  string
}) {
	stripped := quality.StripSeason(s.SeriesTitle)

	releases, err := e.searchRelease(ctx, &model.Title{
		Title:     s.SeriesTitle,
		Year:      0,
		MediaType: model.MediaTypeTV,
	}, stripped, s.SeasonNumber)
	if err != nil {
		e.log.Warn().Err(err).Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("error searching earlier season")
		return
	}

	prefs := buildQualityPrefs(e.cfg, model.MediaTypeTV)
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
	if len(top) == 0 {
		e.log.Info().Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("no results for earlier season")
		return
	}

	sr := &SearchResult{
		Event:  db.EventWithTitle{Title: &model.Title{Title: s.SeriesTitle, MediaType: model.MediaTypeTV}},
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
			MediaType: model.MediaTypeTV,
		},
	}
	e.addToClient(ctx, synthEvent, chosen)
}

func (e *Executor) searchRelease(ctx context.Context, title *model.Title, stripped string, season int) ([]quality.ParsedRelease, error) {
	resCfg := e.cfg.Quality.Movies
	searchType := "movie"
	searchCats := []int{search.CatMovie}
	if title.MediaType == model.MediaTypeTV {
		resCfg = e.cfg.Quality.TV
		searchType = "tvsearch"
		searchCats = []int{search.CatTV}
	}

	resKeyword := resolutionSearchKeyword(resCfg.Resolution)
	fallbackRes := fallbackResolution(resKeyword)

	var queries []string
	if title.MediaType == model.MediaTypeTV {
		queries = tvSearchQueries(stripped, season, resKeyword, fallbackRes)
	} else {
		queries = movieSearchQueries(stripped, title.Year, resKeyword)
	}

	indexerID := e.cfg.Prowlarr.IndexerID
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	// Phase 1: all tiers on preferred indexer only
	if indexerID > 0 {
		name := e.prowl.GetIndexerName(ctx, indexerID)
		e.log.Info().Str("name", name).Int("id", indexerID).Msg("preferred indexer")

		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] preferred: %s", i+1, numTiers, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       searchType,
				IndexerID:  indexerID,
				Limit:      50,
				Categories: searchCats,
			})
			if err != nil {
				return nil, err
			}
			exact, fuzzy := quality.PartitionReleases(results, stripped, title.Year, season, string(title.MediaType))
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= 10 {
				return exactPool, nil
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	// Phase 2: all tiers on all indexers
	for i, q := range queries {
		e.log.Info().Msgf("[%d/%d] searching all: %s", i+1, numTiers, q)
		results, err := e.prowl.Search(ctx, search.SearchParams{
			Query:      q,
			Type:       searchType,
			Limit:      50,
			Categories: searchCats,
		})
		if err != nil {
			return nil, err
		}
		exact, fuzzy := quality.PartitionReleases(results, stripped, title.Year, season, string(title.MediaType))
		exactPool = mergeReleases(exactPool, exact)
		fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
		e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
		if len(exactPool) >= 10 {
			return exactPool, nil
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
	return []string{
		fmt.Sprintf("%s %d %s", title, year, res),
		fmt.Sprintf("%s %d 4k", title, year),
		fmt.Sprintf("%s %d 1080p", title, year),
		fmt.Sprintf("%s %d", title, year),
	}
}

func tvSearchQueries(stripped string, season int, res, fallback string) []string {
	seasonStr := fmt.Sprintf("S%02d", season)
	seasonWord := fmt.Sprintf("season %d", season)

	tiers := []string{
		fmt.Sprintf("%s %s complete %s", stripped, seasonStr, res),
		fmt.Sprintf("%s %s complete %s", stripped, seasonWord, res),
		fmt.Sprintf("%s %s %s", stripped, seasonStr, res),
		fmt.Sprintf("%s %s %s", stripped, seasonWord, res),
		fmt.Sprintf("%s %s", stripped, res),
	}
	if fallback != "" {
		tiers = append(tiers, fmt.Sprintf("%s %s", stripped, fallback))
	}
	return tiers
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
		return e.addToRadarr(ctx, evt, false)
	}
	return e.addToSonarr(ctx, evt, season, false)
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

func (e *Executor) addToRadarr(ctx context.Context, evt db.EventWithTitle, confirmed bool) error {
	tmdbID := evt.Title.TmdbID
	if tmdbID == 0 {
		return nil
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

		e.log.Info().Msgf("Add to Radarr? %s (%d)", lookup.Title, lookup.Year)
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
		SearchNow:           false,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Radarr")

	if e.cfg.CheckCollections {
		if added.Collection != nil && added.Collection.TMDBID > 0 {
			e.log.Info().Str("title", title).Str("collection", added.Collection.Name).Int("tmdb", added.Collection.TMDBID).Msg("checking collection gaps")
			phase3FromCollection := e.checkCollectionGaps(ctx, tmdbID, added.Collection.TMDBID, profileID)
			e.phase3Movies = append(e.phase3Movies, phase3FromCollection...)
		} else {
			e.log.Info().Str("title", title).Msg("movie not part of a TMDB collection, skipping")
		}
	}

	return nil
}

func (e *Executor) addToSonarr(ctx context.Context, evt db.EventWithTitle, season int, confirmed bool) error {
	tvdbID := evt.Title.TvdbID
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

		e.log.Info().Msgf("Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
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
			seasons = append(seasons, library.SonarrSeason{
				SeasonNumber: s.SeasonNumber,
				Monitored:    false,
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
		SearchForMissing:  false,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Sonarr")

	if season > 1 && added.ID > 0 {
		for s := 1; s < season; s++ {
			if promptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", title, s)) {
				e.phase3Seasons = append(e.phase3Seasons, struct {
					SeriesID     int
					SeasonNumber int
					SeriesTitle  string
				}{
					SeriesID:     added.ID,
					SeasonNumber: s,
					SeriesTitle:  title,
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

func (e *Executor) checkCollectionGaps(ctx context.Context, tmdbID int, colTMDBID int, profileID int) []Phase3Movie {
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
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID)
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
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID)
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
			if promptYesNo(ctx, fmt.Sprintf("    Collection %q: Add %s?", col.Name, m.Title)) {
				if _, err := e.radarr.Add(ctx, m.TMDBID, m.Title, m.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           false,
				}); err != nil {
					e.log.Warn().Err(err).Str("title", m.Title).Msg("error adding movie to collection")
				} else {
					e.log.Info().Str("title", m.Title).Msg("added movie to collection (will search in Phase 3)")
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
	indexerID := e.cfg.Prowlarr.IndexerID
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	if indexerID > 0 {
		name := e.prowl.GetIndexerName(ctx, indexerID)
		e.log.Info().Str("name", name).Int("id", indexerID).Msg("preferred indexer (music)")

		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] preferred: %s", i+1, numTiers, q)
			results, err := e.prowl.SearchMusicWithIndexer(ctx, q, indexerID)
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

func (e *Executor) ProcessMusicAlbum(ctx context.Context, ae db.EventWithAlbum) (*MusicAlbumResult, error) {
	sr, err := e.SearchMusicRelease(ctx, ae)
	if err != nil {
		return nil, err
	}
	if len(sr.Top) == 0 {
		e.log.Info().Str("artist", ae.Artist.Name).Str("album", ae.Album.Title).Msg("no music results found")
		return &MusicAlbumResult{Event: ae}, nil
	}

	// Show picker
	selLabel := ae.Artist.Name + " - " + ae.Album.Title
	sel := NewSelector(selLabel, sr.Top)
	chosen, err := sel.Run()
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return &MusicAlbumResult{Event: ae}, nil
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

	return &MusicAlbumResult{Event: ae, Downloaded: true}, nil
}

func (e *Executor) ProcessMusicAlbumDecisions(ctx context.Context, results []MusicAlbumResult) {
	if len(results) == 0 || e.lidarr == nil {
		return
	}

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
		if promptYesNo(ctx, "  Add to Lidarr?") {
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
