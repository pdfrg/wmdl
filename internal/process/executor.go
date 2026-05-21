package process

import (
	"context"
	"fmt"
	"log"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/download"
	"github.com/pdfrg/wmd/internal/library"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/quality"
	"github.com/pdfrg/wmd/internal/search"
)

type Executor struct {
	cfg    *config.Config
	db     *db.DB
	prowl  *search.ProwlarrClient
	dl     download.Client
	radarr *library.RadarrClient
	sonarr *library.SonarrClient
}

func NewExecutor(cfg *config.Config, database *db.DB) *Executor {
	dl := createDownloadClient(cfg)
	radarr := library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey)
	sonarr := library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey)
	return &Executor{
		cfg:    cfg,
		db:     database,
		prowl:  search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey),
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

	// Build search query
	query := fmt.Sprintf("%s %d", title.Title, title.Year)
	if title.MediaType == model.MediaTypeTV {
		query = title.Title
	}

	// Search Prowlarr
	var releases []quality.ParsedRelease
	var err error
	if title.MediaType == model.MediaTypeMovie {
		releases, err = e.prowl.SearchMovies(ctx, query)
	} else {
		releases, err = e.prowl.SearchTV(ctx, query)
	}
	if err != nil {
		return fmt.Errorf("prowlarr search: %w", err)
	}

	if len(releases) == 0 {
		log.Printf("  No results for %q", query)
		return nil
	}

	// Build quality preferences
	prefs := buildQualityPrefs(e.cfg, title.MediaType)

	// Score and sort
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

	// Show picker
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

	// Grab via Prowlarr (sends to Prowlarr's configured download client)
	if err := e.prowl.Grab(ctx, chosen.IndexerID, chosen.Guid); err != nil {
		return fmt.Errorf("prowlarr grab: %w", err)
	}

	// Also add directly to our download client for proper category tagging
	if e.dl != nil {
		category := e.cfg.Downloader.Categories.Movies
		if title.MediaType == model.MediaTypeTV {
			category = e.cfg.Downloader.Categories.TV
		}

		uri := chosen.DownloadURL
		if uri == "" {
			uri = chosen.MagnetURL
		}
		if uri != "" {
			if _, err := e.dl.AddTorrent(uri, download.WithCategory(category)); err != nil {
				log.Printf("  Warning: direct add failed (Prowlarr grab may have succeeded): %v", err)
			}
		}
	}

	// Add to library
	if err := e.addToLibrary(ctx, evt); err != nil {
		log.Printf("  Warning: library add failed: %v", err)
	}

	return nil
}

func (e *Executor) addToLibrary(ctx context.Context, evt db.EventWithTitle) error {
	if evt.Title.MediaType == model.MediaTypeMovie {
		return e.addToRadarr(ctx, evt)
	}
	return e.addToSonarr(ctx, evt)
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

	// Look up quality profile ID by name
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

	added, err := e.radarr.Add(ctx, tmdbID, evt.Title.Title, evt.Title.Year, library.AddMovieOptions{
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
	return nil
}

func (e *Executor) addToSonarr(ctx context.Context, evt db.EventWithTitle) error {
	tvdbID := evt.Title.TvdbID
	if tvdbID == 0 {
		return nil
	}

	existing, err := e.sonarr.Exists(ctx, tvdbID)
	if err != nil {
		return err
	}
	if existing != nil {
		log.Printf("  Already in Sonarr: %s", evt.Title.Title)
		return nil
	}

	// Look up quality profile ID
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

	// Look up language profile ID
	langProfiles, err := e.sonarr.GetLanguageProfiles(ctx)
	if err != nil {
		return err
	}
	langProfileID := 1
	if len(langProfiles) > 0 {
		langProfileID = langProfiles[0].ID
	}

	// Build seasons — monitored=false by default, monitor only the seasons we're adding
	seasons := []library.SonarrSeason{
		{SeasonNumber: 1, Monitored: e.cfg.Library.Sonarr.MonitorNewEpisodes},
	}

	added, err := e.sonarr.Add(ctx, tvdbID, evt.Title.Title, evt.Title.Year, library.AddSeriesOptions{
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
	return nil
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
