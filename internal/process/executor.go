package process

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/download"
	"github.com/pdfrg/wmd/internal/library"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/quality"
	"github.com/pdfrg/wmd/internal/search"
)

type ProcessResult int

const (
	ResultProcessed ProcessResult = iota
	ResultSkipped
	ResultNotFound
)

type Executor struct {
	cfg    *config.Config
	db     *db.DB
	prowl  *search.ProwlarrClient
	dl     download.Client
	radarr *library.RadarrClient
	sonarr *library.SonarrClient

	Unfound []string
}

func NewExecutor(cfg *config.Config, database *db.DB) *Executor {
	dl := createDownloadClient(cfg)
	radarr := library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey)
	sonarr := library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey)
	return &Executor{
		cfg:    cfg,
		db:     database,
		prowl:  search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey, cfg.Prowlarr.Timeout),
		dl:     dl,
		radarr: radarr,
		sonarr: sonarr,
	}
}

func createDownloadClient(cfg *config.Config) download.Client {
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
		log.Printf("Warning: unknown downloader type %q, downloads disabled", cfg.Downloader.Type)
		return nil
	}
}

func (e *Executor) ProcessApproved(ctx context.Context, evt db.EventWithTitle) error {
	title := evt.Title
	log.Printf("Processing: %s (%d)", title.Title, title.Year)

	season := quality.ParseSeasonNumber(title.Title)
	stripped := quality.StripSeason(title.Title)

	releases, err := e.searchRelease(ctx, title, stripped, season)
	if err != nil {
		return fmt.Errorf("searching releases: %w", err)
	}
	if len(releases) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s (%d)", title.Title, title.Year))
		return nil
	}

	prefs := buildQualityPrefs(e.cfg, title.MediaType)
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

	sel := NewSelector(title.Title, top)
	chosen, err := sel.Run()
	if err != nil {
		return err
	}
	if chosen == nil {
		log.Printf("  Skipped %q", title.Title)
		return nil
	}

	log.Printf("  Selected: %s (score %d)", chosen.RawTitle, chosen.Score)

	if e.dl != nil {
		category := e.cfg.Downloader.Categories.Movies
		if title.MediaType == model.MediaTypeTV {
			category = e.cfg.Downloader.Categories.TV
		}

		uri := chosen.DownloadURL
		if uri == "" {
			uri = chosen.MagnetURL
		}
		var torrentID string
		if uri != "" {
			torrentID, err = e.dl.AddTorrent(uri, download.WithCategory(category))
			if err != nil {
				log.Printf("  Warning: direct add failed: %v", err)
			} else {
				log.Printf("  Added to %s (%s)", e.cfg.Downloader.Type, category)
			}
		}

		dl := &model.Download{
			TitleID:         title.ID,
			ReleaseEventID:  evt.Event.ID,
			Quality:         fmt.Sprintf("%dp", chosen.Resolution),
			SourceType:      chosen.Source,
			Codec:           chosen.Codec,
			InfoHash:        chosen.InfoHash,
			Category:        category,
			Status:          model.DownloadAdded,
			ClientTorrentID: torrentID,
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			log.Printf("  Warning: creating download record: %v", err)
		}
	}

	if evt.Event.Notes != "" && strings.Contains(evt.Event.Notes, "upgrade:") {
		oldDL, err := e.db.GetDownloadByTitleID(ctx, title.ID)
		if err == nil && oldDL != nil {
			if err := e.db.UpdateDownloadStatus(ctx, oldDL.ID, model.DownloadUpgraded); err != nil {
				log.Printf("  Warning: marking old download as upgraded: %v", err)
			} else {
				log.Printf("  Marked previous download as upgraded")
			}
		}
	}

	if err := e.addToLibrary(ctx, evt, season); err != nil {
		log.Printf("  Warning: library add failed: %v", err)
	}

	if err := e.db.UpdateReleaseEventStatus(ctx, evt.Event.ID, model.StatusDownloaded); err != nil {
		log.Printf("  Warning: updating event status: %v", err)
	}

	return nil
}

func (e *Executor) searchRelease(ctx context.Context, title *model.Title, stripped string, season int) ([]quality.ParsedRelease, error) {
	resCfg := e.cfg.Quality.Movies
	searchType := "movie"
	if title.MediaType == model.MediaTypeTV {
		resCfg = e.cfg.Quality.TV
		searchType = "tvsearch"
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
	var pool []quality.ParsedRelease

	// Phase 1: all tiers on preferred indexer only
	if indexerID > 0 {
		name := e.prowl.GetIndexerName(ctx, indexerID)
		log.Printf("  preferred: %s (%d)", name, indexerID)

		for i, q := range queries {
			log.Printf("  [%d/%d] Searching: %q", i+1, numTiers, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:     q,
				Type:      searchType,
				IndexerID: indexerID,
				Limit:     50,
			})
			if err != nil {
				return nil, err
			}
			filtered := quality.FilterReleases(results, stripped, title.Year, season, string(title.MediaType))
			before := len(pool)
			pool = mergeReleases(pool, filtered)
			log.Printf("    → %d filtered (%d total)", len(pool)-before, len(pool))
			if len(pool) >= 10 {
				return pool, nil
			}
		}

		log.Printf("  → %d total from preferred, searching all indexers", len(pool))
	}

	// Phase 2: all tiers on all indexers
	for i, q := range queries {
		log.Printf("  [%d/%d] Searching all: %q", i+1, numTiers, q)
		results, err := e.prowl.Search(ctx, search.SearchParams{
			Query: q,
			Type:  searchType,
			Limit: 50,
		})
		if err != nil {
			return nil, err
		}
		filtered := quality.FilterReleases(results, stripped, title.Year, season, string(title.MediaType))
		before := len(pool)
		pool = mergeReleases(pool, filtered)
		log.Printf("    → %d filtered (%d total)", len(pool)-before, len(pool))
		if len(pool) >= 10 {
			return pool, nil
		}
	}

	if len(pool) > 0 {
		return pool, nil
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
		return e.addToRadarr(ctx, evt)
	}
	return e.addToSonarr(ctx, evt, season)
}

func (e *Executor) addToRadarr(ctx context.Context, evt db.EventWithTitle) error {
	tmdbID := evt.Title.TmdbID
	if tmdbID == 0 {
		return nil
	}

	existing, err := e.radarr.Exists(ctx, tmdbID)
	if err != nil {
		return err
	}
	if existing != nil {
		log.Printf("  Already in Radarr: %s", evt.Title.Title)
		return nil
	}

	lookup, err := e.radarr.Lookup(ctx, tmdbID)
	if err != nil {
		return err
	}
	if lookup == nil {
		return fmt.Errorf("movie not found on TMDB (tmdb_id=%d)", tmdbID)
	}

	log.Printf("  Add to Radarr?")
	log.Printf("    %s (%d)", lookup.Title, lookup.Year)
	if lookup.Overview != "" {
		for _, line := range formatOverview(lookup.Overview, 72) {
			log.Printf("    %s", line)
		}
	}
	log.Printf("    Profile: %s", e.cfg.Library.Radarr.QualityProfile)
	log.Printf("    Root:    %s", e.cfg.Library.Radarr.RootFolder)
	if !promptYesNo("  Add to Radarr?") {
		return nil
	}

	profiles, err := e.radarr.GetQualityProfiles(ctx)
	if err != nil {
		return err
	}
	profileID := 1
	for _, p := range profiles {
		if p.Name == e.cfg.Library.Radarr.QualityProfile {
			profileID = p.ID
			break
		}
	}

	added, err := e.radarr.Add(ctx, tmdbID, lookup.Title, lookup.Year, library.AddMovieOptions{
		Monitored:           e.cfg.Library.Radarr.Monitor,
		MinimumAvailability: "released",
		QualityProfileID:    profileID,
		RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
		SearchNow:           false,
	})
	if err != nil {
		return err
	}
	log.Printf("  Added to Radarr: %s (ID %d)", added.Title, added.ID)

	if added.Collection != nil && added.Collection.TMDBID > 0 {
		e.checkCollectionGaps(ctx, tmdbID, profileID)
	}

	return nil
}

func (e *Executor) addToSonarr(ctx context.Context, evt db.EventWithTitle, season int) error {
	tvdbID := evt.Title.TvdbID
	if tvdbID == 0 {
		return nil
	}

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
			log.Printf("  Season %d already complete in Sonarr", season)
			return nil
		}
		log.Printf("  Already in Sonarr, season %d incomplete — proceeding", season)
		return nil
	}

	lookup, err := e.sonarr.Lookup(ctx, tvdbID)
	if err != nil {
		return err
	}
	if lookup == nil {
		return fmt.Errorf("series not found on TVDB (tvdb_id=%d)", tvdbID)
	}

	log.Printf("  Add to Sonarr?")
	log.Printf("    %s (%d)", lookup.Title, lookup.Year)
	if lookup.Overview != "" {
		for _, line := range formatOverview(lookup.Overview, 72) {
			log.Printf("    %s", line)
		}
	}
	log.Printf("    Profile: %s", e.cfg.Library.Sonarr.QualityProfile)
	log.Printf("    Root:    %s", e.cfg.Library.Sonarr.RootFolder)
	if !promptYesNo("  Add to Sonarr?") {
		return nil
	}

	profiles, err := e.sonarr.GetQualityProfiles(ctx)
	if err != nil {
		return err
	}
	profileID := 1
	for _, p := range profiles {
		if p.Name == e.cfg.Library.Sonarr.QualityProfile {
			profileID = p.ID
			break
		}
	}

	langProfiles, err := e.sonarr.GetLanguageProfiles(ctx)
	if err != nil {
		return err
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
				Monitored:    false,
			})
		}
	}

	added, err := e.sonarr.Add(ctx, tvdbID, lookup.Title, lookup.Year, library.AddSeriesOptions{
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
	log.Printf("  Added to Sonarr: %s (ID %d)", added.Title, added.ID)

	if season > 1 && added.ID > 0 {
		e.checkEarlierSeasons(ctx, added.ID)
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

func (e *Executor) checkEarlierSeasons(ctx context.Context, seriesID int) {
	series, err := e.sonarr.GetSeries(ctx, seriesID)
	if err != nil {
		log.Printf("  Warning: cannot fetch series details: %v", err)
		return
	}

	var missingSeasons []library.SonarrSeason
	for _, s := range series.Seasons {
		if s.SeasonNumber == 0 {
			continue
		}
		if s.Statistics != nil && s.Statistics.EpisodeFileCount == 0 && s.Statistics.EpisodeCount > 0 {
			missingSeasons = append(missingSeasons, s)
		}
	}

	if len(missingSeasons) == 0 {
		return
	}

	log.Printf("  Series has %d season(s) with no files", len(missingSeasons))
	for _, s := range missingSeasons {
		if promptYesNo(fmt.Sprintf("    Search for Season %d?", s.SeasonNumber)) {
			if err := e.sonarr.TriggerSeasonSearch(ctx, seriesID, s.SeasonNumber); err != nil {
				log.Printf("    Error searching Season %d: %v", s.SeasonNumber, err)
			} else {
				log.Printf("    Searching Season %d", s.SeasonNumber)
			}
		}
	}
}

func (e *Executor) checkCollectionGaps(ctx context.Context, tmdbID int, profileID int) {
	collections, err := e.radarr.GetCollections(ctx)
	if err != nil {
		log.Printf("  Warning: cannot check collections: %v", err)
		return
	}

	for _, col := range collections {
		var missing []library.RadarrMovie
		for _, m := range col.Movies {
			if !m.HasFile && m.TMDBID != tmdbID {
				missing = append(missing, m)
			}
		}
		if len(missing) == 0 {
			continue
		}
		log.Printf("  Collection %q has %d missing movies", col.Name, len(missing))
		for _, m := range missing {
			if promptYesNo(fmt.Sprintf("    Add %s (%d)?", m.Title, m.Year)) {
				if _, err := e.radarr.Add(ctx, m.TMDBID, m.Title, m.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           true,
				}); err != nil {
					log.Printf("    Error adding %s: %v", m.Title, err)
				} else {
					log.Printf("    Added and searching: %s", m.Title)
				}
			}
		}
	}
}

func promptYesNo(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
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
