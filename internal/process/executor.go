package process

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
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
	hc         *discover.HardcoverClient

	Unfound               []string
	Skipped               []string
	Phase3SearchPhase     []Phase3SearchEntry
	BookPhase3SearchPhase []*BookSearchResult
	phase3Movies          []Phase3Movie
	phase3Seasons         []struct {
		SeriesID     int
		SeasonNumber int
		SeriesTitle  string
		MediaType    model.MediaType
		TvdbID       int
	}
	skipRejectedTvdbIDs         map[int]struct{}
	phase3BatchProcessedMovies  map[int]bool         // TMDB IDs of movies processed via BatchPicker
	phase3BatchProcessedSeasons map[int]map[int]bool // tvdbID -> set of season numbers processed via BatchPicker
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
	var hc *discover.HardcoverClient
	if cfg.Hardcover.APIKey != "" {
		hc = discover.NewHardcoverClient(cfg.Hardcover.APIKey)
	}
	catMap := map[string]int{
		"videos":     cfg.Prowlarr.IndexerIDs.Videos,
		"music":      cfg.Prowlarr.IndexerIDs.Music,
		"anime":      cfg.Prowlarr.IndexerIDs.Anime,
		"ebooks":     cfg.Prowlarr.IndexerIDs.Ebooks,
		"audiobooks": cfg.Prowlarr.IndexerIDs.Audiobooks,
	}
	return &Executor{
		log:                         logger,
		cfg:                         cfg,
		db:                          database,
		prowl:                       search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey, cfg.Prowlarr.Timeout, catMap),
		dl:                          dl,
		radarr:                      radarr,
		sonarr:                      sonarr,
		lidarr:                      lidarr,
		bookClient:                  bookClient,
		hc:                          hc,
		skipRejectedTvdbIDs:         make(map[int]struct{}),
		phase3BatchProcessedMovies:  make(map[int]bool),
		phase3BatchProcessedSeasons: make(map[int]map[int]bool),
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
