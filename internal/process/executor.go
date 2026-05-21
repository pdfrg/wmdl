package process

import (
	"context"
	"fmt"
	"log"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/quality"
	"github.com/pdfrg/wmd/internal/search"
)

type Executor struct {
	cfg    *config.Config
	db     *db.DB
	prowl  *search.ProwlarrClient
	dl     downloadClient
	radarr libraryClient
	sonarr libraryClient
}

type downloadClient interface {
	AddTorrent(url string, opts ...interface{}) (string, error)
	AddMagnet(uri string, opts ...interface{}) (string, error)
}

type libraryClient interface{}

func NewExecutor(cfg *config.Config, database *db.DB) *Executor {
	return &Executor{
		cfg:   cfg,
		db:    database,
		prowl: search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey),
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

	// Grab via Prowlarr
	if err := e.prowl.Grab(ctx, chosen.IndexerID, chosen.Guid); err != nil {
		return fmt.Errorf("prowlarr grab: %w", err)
	}

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
