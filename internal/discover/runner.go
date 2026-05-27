package discover

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"

	"github.com/pdfrg/wmdl/internal/browser"
	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/notifier"
)

type Runner struct {
	log           zerolog.Logger
	cfg           *config.Config
	db            *db.DB
	tmdb          *TMDBClient
	rt            *RTFinder
	imdb          *IMDbAPIClient
	notify        notifier.Notifier
	debugURL      string
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	targetYear    int
	targetWeek    int
	hasTargetWeek bool
	headless      bool
	killBrave     func() error
}

func (r *Runner) SetTargetWeek(year, week int) {
	r.targetYear = year
	r.targetWeek = week
	r.hasTargetWeek = true
}

func NewRunner(logger zerolog.Logger, cfg *config.Config, database *db.DB, headless bool) *Runner {
	notify, err := notifier.New(cfg.Notifier)
	if err != nil {
		logger.Warn().Err(err).Msg("notifier unavailable")
		notify = nil
	}

	return &Runner{
		log:      logger,
		cfg:      cfg,
		db:       database,
		tmdb:     NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken),
		rt:       NewRTFinder(),
		imdb:     NewIMDbAPIClient(),
		notify:   notify,
		debugURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.Browser.DebugPort),
		headless: headless,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.TMDB.APIKey == "" && r.cfg.TMDB.AccessToken == "" {
		return fmt.Errorf("TMDB not configured: set tmdb.api_key or tmdb.access_token in config\n  Get a free API key at https://www.themoviedb.org/settings/api")
	}

	// Auto-launch Brave if not already running on the debug port
	killBrave, err := browser.EnsureRunning(r.cfg.Browser.Binary, r.cfg.Browser.DebugPort, r.cfg.Browser.Profile, r.headless)
	if err != nil {
		r.log.Warn().Err(err).Msg("browser unavailable, some features disabled")
	} else if killBrave != nil {
		r.killBrave = killBrave
		r.log.Info().Int("port", r.cfg.Browser.DebugPort).Str("profile", r.cfg.Browser.Profile).Msg("launched Brave")
	} else {
		r.log.Info().Int("port", r.cfg.Browser.DebugPort).Msg("connected to Brave")
	}

	// If we auto-launched in headless mode, kill the browser when done.
	// Defer this BEFORE allocCtx setup so allocCancel runs first (LIFO).
	if r.killBrave != nil && r.headless {
		defer func() { _ = r.killBrave() }()
	}

	// Shared chromedp allocator for all RT page scraping (single WebSocket connection)
	r.allocCtx, r.allocCancel = chromedp.NewRemoteAllocator(ctx, r.debugURL)
	if r.allocCancel != nil {
		defer r.allocCancel()
	}

	providers := []ReleaseProvider{
		NewDVDReleaseDates(),
		NewTMDBDiscoverProvider(r.tmdb),
	}

	// FlixPatrol via chromedp (best-effort, requires Brave running on debug port)
	fp := NewFlixPatrolProvider(r.debugURL)
	providers = append(providers, fp)

	// Set target week on providers that support it
	if r.hasTargetWeek {
		for _, p := range providers {
			if ws, ok := p.(WeekSettable); ok {
				ws.SetWeekRange(r.targetYear, r.targetWeek)
			}
		}
	}

	var allItems []ScrapedItem

	for _, p := range providers {
		r.log.Info().Str("provider", p.Name()).Msg("scraping")
		items, err := p.Scrape()
		if err != nil {
			r.log.Warn().Err(err).Str("provider", p.Name()).Msg("scrape failed")
			continue
		}
		r.log.Info().Int("count", len(items)).Str("provider", p.Name()).Msg("found items")
		allItems = append(allItems, items...)
	}

	if len(allItems) == 0 {
		r.log.Info().Msg("no new releases found")
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

	progYear, progWeek := r.targetYear, r.targetWeek
	if !r.hasTargetWeek {
		progYear, progWeek = programWeekFromItems(unique)
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

			if err := r.processItem(ctx, item, progYear, progWeek); err != nil {
				r.log.Warn().Err(err).Str("title", item.Title).Msg("error processing item")
				return
			}
			mu.Lock()
			processed++
			mu.Unlock()
		}(item)
	}

	wg.Wait()
	r.log.Info().Msgf("Processed %d/%d items", processed, len(unique))

	// Track week state — use target week when set, never derive from
	// streaming items (which can be 2 months in the past).
	if r.hasTargetWeek {
		ws := &model.WeekState{
			Year:       r.targetYear,
			Week:       r.targetWeek,
			WeekDate:   tuesdayOfISOWeek(r.targetYear, r.targetWeek).Format("2006-01-02"),
			Discovered: true,
		}
		if err := r.db.UpsertWeekState(ctx, ws); err != nil {
			r.log.Warn().Err(err).Msg("tracking week state")
		}
	} else if ws := weekStateFromItems(unique); ws != nil {
		ws.Discovered = true
		if err := r.db.UpsertWeekState(ctx, ws); err != nil {
			r.log.Warn().Err(err).Msg("tracking week state")
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
			if item.Year > 0 {
				msg += fmt.Sprintf("\n- %s (%d)", item.Title, item.Year)
			} else {
				msg += fmt.Sprintf("\n- %s", item.Title)
			}
			count++
		}
		if err := r.notify.Send("wmdl: New Releases", msg, 5); err != nil {
			r.log.Warn().Err(err).Msg("notification failed")
		}
	}

	return nil
}

func programWeekFromItems(items []ScrapedItem) (int, int) {
	for _, item := range items {
		if item.ReleaseType != model.ReleasePhysical || item.ReleaseDate == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", item.ReleaseDate)
		if err != nil {
			continue
		}
		return t.ISOWeek()
	}
	for _, item := range items {
		if item.ReleaseDate == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", item.ReleaseDate)
		if err != nil {
			continue
		}
		return t.ISOWeek()
	}
	return time.Now().ISOWeek()
}

func (r *Runner) processItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	searchTitle := cleanTitleForSearch(item.Title)
	r.log.Info().Str("title", item.Title).Int("year", item.Year).Msg("processing item")

	// Phase 1: TMDB enrichment (gets us tmdb_id, imdb_id, rating, metadata)
	apiCtx, apiCancel := context.WithTimeout(ctx, 20*time.Second)

	mediaType := item.MediaType
	var tmdbID int
	var rating float64
	var enrich *TMDBEnrichment
	var imdbID string

	var preferType string
	if item.Source != "dvdsreleasedates" {
		preferType = string(item.MediaType)
	}

	enrich, err := r.tmdb.enrichWithPrefs(apiCtx, searchTitle, item.Year, preferType)
	if err == nil {
		tmdbID = enrich.TMDBID
		rating = enrich.Rating
		imdbID = enrich.IMDbID
		r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched")
	} else {
		r.log.Warn().Err(err).Msg("TMDB lookup failed")
		imdbID = item.ImdbID
	}

	// Use TMDB year when scraper couldn't determine it
	if enrich != nil && enrich.Year > 0 {
		item.Year = enrich.Year
	}

	// Phase 2: IMDbAPI ratings (IMDb score + Metacritic) using imdb_id
	var imdbRating float64
	var metacriticScore float64
	if imdbID != "" {
		ratings, err := r.imdb.FetchRatings(apiCtx, imdbID)
		if err == nil && ratings != nil {
			imdbRating = ratings.ImdbRating
			metacriticScore = ratings.MetacriticScore
			r.log.Info().Float64("imdb_rating", imdbRating).Float64("metacritic", metacriticScore).Msg("IMDbAPI ratings")
		} else if err != nil {
			r.log.Warn().Err(err).Msg("IMDbAPI failed")
		}
	}
	// Fall back to scraped IMDb rating if API returned nothing
	if imdbRating == 0 && item.ImdbRating > 0 {
		imdbRating = item.ImdbRating
		r.log.Info().Float64("imdb_rating", imdbRating).Msg("using scraped IMDb rating")
	}

	// Phase 3: US content rating from TMDB
	var usRating string
	if tmdbID > 0 {
		if mediaType == model.MediaTypeTV {
			usRating = r.tmdb.GetTVRating(apiCtx, tmdbID)
		} else {
			usRating = r.tmdb.GetUSCertification(apiCtx, tmdbID, string(mediaType))
		}
		if usRating != "" {
			r.log.Info().Str("rating", usRating).Msg("US content rating")
		}
	}

	// Use TMDB title for RT URL slug when available (cleaner than scraped title)
	rtTitle := searchTitle
	if enrich != nil && enrich.Title != "" {
		rtTitle = enrich.Title
	}
	rtURL := r.rt.FindURL(rtTitle, item.Year, string(mediaType))
	apiCancel()

	// Phase 4: Best-effort RT rating scrape via chromedp
	rtCritics, rtAudience := 0.0, 0.0
	if rtURL != "" {
		r.log.Info().Str("url", rtURL).Msg("RT URL found")
		ratings := ScrapeRTRatings(ctx, r.allocCtx, rtURL)
		rtCritics = ratings.CriticsScore
		rtAudience = ratings.AudienceScore
		if rtCritics > 0 || rtAudience > 0 {
			r.log.Info().Float64("critics", rtCritics).Float64("audience", rtAudience).Msg("RT scores")
		} else {
			r.log.Warn().Msg("RT scrape: no scores found")
		}
	} else {
		r.log.Info().Msg("RT URL not found")
	}
	// Fall back to FlixPatrol RT scores if chromedp returned nothing
	if rtCritics == 0 && item.RTCriticsScore > 0 {
		rtCritics = item.RTCriticsScore
		r.log.Info().Float64("critics_score", rtCritics).Msg("using FlixPatrol RT critics score")
	}

	overview := ""
	genres := ""
	runtime := 0
	tvdbID := 0
	posterPath := ""
	originalLanguage := ""
	originCountry := ""
	if enrich != nil {
		tvdbID = enrich.TVDBID
		overview = enrich.Overview
		genres = enrich.Genres
		runtime = enrich.Runtime
		posterPath = enrich.PosterPath
		originalLanguage = enrich.OriginalLanguage
		originCountry = enrich.OriginCountry
	}

	title := &model.Title{
		TmdbID:           tmdbID,
		TvdbID:           tvdbID,
		Title:            item.Title,
		Year:             item.Year,
		MediaType:        mediaType,
		ImdbID:           imdbID,
		ImdbRating:       imdbRating,
		MetacriticScore:  metacriticScore,
		RTURL:            rtURL,
		RTCriticsScore:   rtCritics,
		RTAudienceScore:  rtAudience,
		TmdbRating:       rating,
		USRating:         usRating,
		YoutubeViews:     item.YoutubeViews,
		Overview:         overview,
		Genres:           genres,
		Runtime:          runtime,
		PosterPath:       posterPath,
		OriginalLanguage: originalLanguage,
		OriginCountry:    originCountry,
	}

	titleID, err := r.db.UpsertTitle(ctx, title)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	existing, err := r.db.GetLatestReleaseEvent(ctx, titleID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}

	// Skip if already pending — don't stack duplicate events
	if existing != nil && existing.Status == model.StatusPending {
		return nil
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
		ISOYear:        progYear,
		ISOWeek:        progWeek,
	}

	// If previous event was downloaded and this is new, mark the old as "upgraded"
	if existing != nil && existing.Status == model.StatusDownloaded && notes != "" {
		if err := r.db.UpdateReleaseEventStatus(ctx, existing.ID, model.StatusDownloaded); err != nil {
			r.log.Warn().Err(err).Msg("failed to mark previous download as upgraded")
		}
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

var (
	parenSuffix = regexp.MustCompile(`\s*\([^)]*\)`)
	trailingFmt = regexp.MustCompile(`(?i)\s+(season\s+\d+|dvd|blu-ray|4k)\s*$`)
)

func cleanTitleForSearch(title string) string {
	cleaned := html.UnescapeString(title)
	cleaned = parenSuffix.ReplaceAllString(cleaned, "")
	// Strip colon-suffixes that look like season descriptors
	if parts := strings.SplitN(cleaned, ":", 2); len(parts) == 2 {
		suffix := strings.ToLower(strings.TrimSpace(parts[1]))
		if strings.Contains(suffix, "season") || strings.Contains(suffix, "complete") {
			cleaned = parts[0]
		}
	}
	cleaned = trailingFmt.ReplaceAllString(cleaned, "")
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
