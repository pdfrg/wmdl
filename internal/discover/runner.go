package discover

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/notifier"
)

type Runner struct {
	cfg    *config.Config
	db     *db.DB
	tmdb   *TMDBClient
	rt     *RTFinder
	notify *notifier.Gotify
}

func NewRunner(cfg *config.Config, database *db.DB) *Runner {
	return &Runner{
		cfg:    cfg,
		db:     database,
		tmdb:   NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken),
		rt:     NewRTFinder(),
		notify: notifier.NewGotify(cfg.Notifier.GotifyURL, cfg.Notifier.GotifyToken),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.TMDB.APIKey == "" && r.cfg.TMDB.AccessToken == "" {
		return fmt.Errorf("TMDB not configured: set tmdb.api_key or tmdb.access_token in config\n  Get a free API key at https://www.themoviedb.org/settings/api")
	}

	providers := []ReleaseProvider{
		NewDVDReleaseDates(),
		NewTMDBDiscoverProvider(r.tmdb),
	}

	// FlixPatrol via chromedp (best-effort, requires Brave running on debug port)
	debugURL := fmt.Sprintf("http://127.0.0.1:%d", r.cfg.Browser.DebugPort)
	fp := NewFlixPatrolProvider(debugURL)
	providers = append(providers, fp)

	var allItems []ScrapedItem

	for _, p := range providers {
		log.Printf("Scraping %s...", p.Name())
		items, err := p.Scrape()
		if err != nil {
			log.Printf("Warning: %s failed: %v", p.Name(), err)
			continue
		}
		log.Printf("  Found %d items from %s", len(items), p.Name())
		allItems = append(allItems, items...)
	}

	if len(allItems) == 0 {
		log.Println("No new releases found.")
		return nil
	}

	// Deduplicate by title+year
	seen := make(map[string]bool)
	var unique []ScrapedItem
	for _, item := range allItems {
		key := fmt.Sprintf("%s|%d|%s", item.Title, item.Year, item.ReleaseType)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}

	var processed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)

	for _, item := range unique {
		wg.Add(1)
		go func(item ScrapedItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := r.processItem(ctx, item); err != nil {
				log.Printf("  Error processing %q: %v", item.Title, err)
				return
			}
			mu.Lock()
			processed++
			mu.Unlock()
		}(item)
	}

	wg.Wait()
	log.Printf("Processed %d/%d items", processed, len(unique))

	// Track week state
	if ws := weekStateFromItems(unique); ws != nil {
		ws.Discovered = true
		if err := r.db.UpsertWeekState(ctx, ws); err != nil {
			log.Printf("Warning: tracking week state: %v", err)
		}
	}

	// Send notification
	if r.notify != nil && processed > 0 {
		msg := fmt.Sprintf("**%d new release%s** ready for review:\n", processed, map[bool]string{true: "s", false: ""}[processed != 1])
		count := 0
		for _, item := range unique {
			if count >= 5 {
				msg += fmt.Sprintf("\n+ %d more", processed-count)
				break
			}
			msg += fmt.Sprintf("\n- %s (%d)", item.Title, item.Year)
			count++
		}
		if err := r.notify.Send("wmd: New Releases", msg, 5); err != nil {
			log.Printf("Warning: notification failed: %v", err)
		}
	}

	return nil
}

func (r *Runner) processItem(ctx context.Context, item ScrapedItem) error {
	// Clean title: remove parenthetical suffixes like "(season 6)", "(weekly, ...)"
	searchTitle := cleanTitleForSearch(item.Title)

	// API calls get their own timeout
	apiCtx, apiCancel := context.WithTimeout(ctx, 20*time.Second)

	mediaType := item.MediaType
	var tmdbID int
	var rating float64
	var enrich *TMDBEnrichment

	enrich, err := r.tmdb.Enrich(apiCtx, searchTitle, item.Year)
	if err == nil {
		mediaType = model.MediaType(enrich.MediaType)
		tmdbID = enrich.TMDBID
		rating = enrich.Rating
	} else {
		log.Printf("  TMDB lookup failed for %q: %v", item.Title, err)
	}

	rtURL := r.rt.FindURL(searchTitle, item.Year, string(mediaType))
	apiCancel()

	// DB operations use parent context — no aggressive timeout

	// Best-effort RT rating scrape
	rtCritics, rtAudience := 0.0, 0.0
	if rtURL != "" {
		if ratings, err := ScrapeRTRatings(ctx, rtURL); err == nil {
			rtCritics = ratings.CriticsScore
			rtAudience = ratings.AudienceScore
		}
	}

	tvdbID := 0
	if enrich != nil {
		tvdbID = enrich.TVDBID
	}

	overview := ""
	genres := ""
	runtime := 0
	imdbID := ""
	if enrich != nil {
		overview = enrich.Overview
		genres = enrich.Genres
		runtime = enrich.Runtime
		imdbID = enrich.IMDbID
	}

	title := &model.Title{
		TmdbID:          tmdbID,
		TvdbID:          tvdbID,
		Title:           item.Title,
		Year:            item.Year,
		MediaType:       mediaType,
		ImdbID:          imdbID,
		RTURL:           rtURL,
		RTCriticsScore:  rtCritics,
		RTAudienceScore: rtAudience,
		TmdbRating:      rating,
		Overview:        overview,
		Genres:          genres,
		Runtime:         runtime,
	}

	titleID, err := r.db.UpsertTitle(ctx, title)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	existing, err := r.db.GetLatestReleaseEvent(ctx, titleID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}

	var prevStatus model.ReleaseStatus
	var notes string

	if existing != nil {
		prevStatus = existing.Status

		// Check for upgrade: was it previously downloaded with a lower source type?
		if existing.Status == model.StatusDownloaded {
			dl, err := r.db.GetDownloadByTitleID(ctx, titleID)
			if err == nil && dl != nil && dl.SourceType != "" {
				if isUpgrade(dl.SourceType, item.ReleaseType) {
					notes = fmt.Sprintf("upgrade: %s → %s", dl.SourceType, item.ReleaseType)
				}
			}
		}
	}

	status := model.StatusPending
	if existing != nil && notes == "" {
		switch existing.Status {
		case model.StatusRejected:
			status = model.StatusPending
		case model.StatusDownloaded:
			status = model.StatusPending
		}
	}
	// If upgrade, always show as pending
	if notes != "" {
		status = model.StatusPending
	}

	event := &model.ReleaseEvent{
		TitleID:        titleID,
		Source:         "scraper",
		ReleaseType:    item.ReleaseType,
		ReleaseDate:    item.ReleaseDate,
		Status:         status,
		PreviousStatus: prevStatus,
		Notes:          notes,
	}

	// If previous event was downloaded and this is new, mark the old as "upgraded"
	if existing != nil && existing.Status == model.StatusDownloaded && notes != "" {
		_ = r.db.UpdateReleaseEventStatus(ctx, existing.ID, model.StatusDownloaded)
	}

	if _, err := r.db.CreateReleaseEvent(ctx, event); err != nil {
		return fmt.Errorf("saving release event: %w", err)
	}

	return nil
}

// isUpgrade checks if a new release type is an upgrade over a previous download's source type.
// Physical (BluRay) is an upgrade over streaming (Web-DL/WebRip).
// A higher-quality source within the same category is also an upgrade.
func isUpgrade(prevSource string, newReleaseType model.ReleaseType) bool {
	if newReleaseType != model.ReleasePhysical {
		return false
	}
	prev := string(prevSource)
	prev = strings.ToLower(prev)
	switch prev {
	case "webrip", "web-dl", "hdtv":
		return true
	case "bluray", "remux":
		return false
	}
	// Streaming to physical is always an upgrade
	return newReleaseType == model.ReleasePhysical
}

var parenSuffix = regexp.MustCompile(`\s*\([^)]*\)`)

func cleanTitleForSearch(title string) string {
	cleaned := parenSuffix.ReplaceAllString(title, "")
	return strings.TrimSpace(cleaned)
}

func weekStateFromItems(items []ScrapedItem) *model.WeekState {
	// Prefer physical items for week state, then fall back to streaming
	for _, item := range items {
		if item.ReleaseType != model.ReleasePhysical || item.ReleaseDate == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", item.ReleaseDate)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		return &model.WeekState{
			Year:     y,
			Week:     w,
			WeekDate: item.ReleaseDate,
		}
	}
	// Fall back to streaming date (not ideal but better than nothing)
	for _, item := range items {
		if item.ReleaseDate == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", item.ReleaseDate)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		return &model.WeekState{
			Year:     y,
			Week:     w,
			WeekDate: item.ReleaseDate,
		}
	}
	return nil
}
