package discover

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
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
	mb            *MBClient
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
		mb:       NewMBClient(),
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

	// Music provider
	aoty := NewAOTYProvider()
	if r.hasTargetWeek {
		// Music uses timeshifted week: video target - music_timeshift_weeks
		musicYear, musicWeek := r.musicTargetWeek(ctx)
		aoty.SetWeekRange(musicYear, musicWeek)
	}
	providers = append(providers, aoty)

	// Set target week on video providers
	if r.hasTargetWeek {
		for _, p := range []ReleaseProvider{NewDVDReleaseDates(), NewTMDBDiscoverProvider(r.tmdb)} {
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

	// Split music and video items
	var videoItems, musicItems []ScrapedItem
	for _, item := range allItems {
		if item.MediaType == model.MediaTypeMusic {
			musicItems = append(musicItems, item)
		} else {
			videoItems = append(videoItems, item)
		}
	}

	// Deduplicate video items by title+year
	seen := make(map[string]bool)
	var uniqueVideos []ScrapedItem
	for _, item := range videoItems {
		key := fmt.Sprintf("%s|%d|%s", item.Title, item.Year, item.ReleaseType)
		if seen[key] {
			continue
		}
		seen[key] = true
		uniqueVideos = append(uniqueVideos, item)
	}

	// Deduplicate music items by artist+album
	seenMusic := make(map[string]bool)
	var uniqueMusic []ScrapedItem
	for _, item := range musicItems {
		key := fmt.Sprintf("%s|%s", item.ArtistName, item.Title)
		if seenMusic[key] {
			continue
		}
		seenMusic[key] = true
		uniqueMusic = append(uniqueMusic, item)
	}

	progYear, progWeek := r.targetYear, r.targetWeek
	if !r.hasTargetWeek {
		progYear, progWeek = programWeekFromItems(uniqueVideos)
	}

	var processed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)

	// Process video items
	wg.Add(len(uniqueVideos))
	for _, item := range uniqueVideos {
		go func(item ScrapedItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := r.processItem(ctx, item, progYear, progWeek); err != nil {
				r.log.Warn().Err(err).Str("title", item.Title).Msg("error processing item")
			}
			mu.Lock()
			processed++
			mu.Unlock()
		}(item)
	}
	wg.Wait()

	// Process music items (with MusicBrainz enrichment)
	if len(uniqueMusic) > 0 {
		musicYear, musicWeek := r.targetYear, r.targetWeek
		if r.hasTargetWeek {
			musicYear, musicWeek = r.musicTargetWeek(ctx)
		}

		var musicProcessed int
		for _, item := range uniqueMusic {
			if err := r.processMusicItem(ctx, item, musicYear, musicWeek); err != nil {
				r.log.Warn().Err(err).Str("album", item.Title).Str("artist", item.ArtistName).Msg("error processing music item")
				continue
			}
			musicProcessed++
		}
		processed += musicProcessed

		// Track music discovery week in settings
		_ = r.db.SetSetting(ctx, "music_last_iso_year", fmt.Sprintf("%d", musicYear))
		_ = r.db.SetSetting(ctx, "music_last_iso_week", fmt.Sprintf("%d", musicWeek))
	}

	r.log.Info().Msgf("Processed %d/%d items", processed, len(uniqueVideos)+len(uniqueMusic))

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
	} else if ws := weekStateFromItems(uniqueVideos); ws != nil {
		ws.Discovered = true
		if err := r.db.UpsertWeekState(ctx, ws); err != nil {
			r.log.Warn().Err(err).Msg("tracking week state")
		}
	}

	// Send notification
	if r.notify != nil && processed > 0 {
		msg := fmt.Sprintf("**%d new release%s** ready for review:\n", processed, map[bool]string{true: "s", false: ""}[processed != 1])
		count := 0
		for _, item := range uniqueVideos {
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

	preferType := string(item.MediaType)

	enrich, err := r.tmdb.enrichWithPrefs(apiCtx, searchTitle, item.Year, preferType)
	if err == nil {
		tmdbID = enrich.TMDBID
		rating = enrich.Rating
		imdbID = enrich.IMDbID
		r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched")
	}

	// Retry with uncleaned original title if cleaned search gave a weak match
	// (non-exact) or failed entirely.
	if err != nil || (enrich != nil && !strings.EqualFold(enrich.Title, searchTitle)) {
		origTitle := html.UnescapeString(item.Title)
		if origTitle != searchTitle {
			r.log.Info().Str("original", origTitle).Msg("retrying TMDB with original title")
			enrich2, err2 := r.tmdb.enrichWithPrefs(apiCtx, origTitle, item.Year, preferType)
			if err2 == nil {
				enrich = enrich2
				tmdbID = enrich.TMDBID
				rating = enrich.Rating
				imdbID = enrich.IMDbID
				r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (retry)")
			}
		}

		// If original title retry also didn't yield an exact match, try
		// parenthetical content as a last resort.
		if enrich == nil || !strings.EqualFold(enrich.Title, searchTitle) {
			if paren := extractParenthetical(item.Title); paren != "" && !strings.EqualFold(paren, searchTitle) && !strings.EqualFold(paren, origTitle) {
				r.log.Info().Str("parenthetical", paren).Msg("retrying TMDB with parenthetical content")
				enrich3, err3 := r.tmdb.enrichWithPrefs(apiCtx, paren, item.Year, preferType)
				if err3 == nil {
					enrich = enrich3
					tmdbID = enrich.TMDBID
					rating = enrich.Rating
					imdbID = enrich.IMDbID
					r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (parenthetical)")
				}
			}
		}
	}

	if enrich == nil {
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
	rtURL := r.rt.FindURL(apiCtx, rtTitle, item.Year, string(mediaType))

	// If URL guessing returned a search page (fallback) and we have a browser,
	// try scraping the search results directly.
	if strings.Contains(rtURL, "search?search=") {
		if r.allocCtx != nil {
			r.log.Info().Msg("trying RT search via chromedp")
			if searchURL := SearchRTSite(ctx, r.allocCtx, rtTitle, item.Year, string(mediaType)); searchURL != "" {
				rtURL = searchURL
				r.log.Info().Str("url", rtURL).Msg("RT URL found via search")
			}
		}
	}
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
	tmdbTitle := ""
	if enrich != nil {
		tvdbID = enrich.TVDBID
		tmdbTitle = enrich.Title
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
		TmdbTitle:        tmdbTitle,
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

	var titleID int64
	var existingEvent *model.ReleaseEvent

	err = r.db.Transaction(ctx, func(ctx context.Context) error {
		id, err := r.db.UpsertTitle(ctx, title)
		if err != nil {
			return fmt.Errorf("saving title: %w", err)
		}
		titleID = id

		existing, err := r.db.GetLatestReleaseEvent(ctx, titleID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		// Skip if already pending — don't stack duplicate events
		if existing != nil && existing.Status == model.StatusPending {
			existingEvent = existing
			return nil
		}

		var evtNotes string
		var evtPrev model.ReleaseStatus

		if existing != nil {
			evtPrev = existing.Status

			// Check for upgrade: was it previously downloaded with a lower source type?
			if existing.Status == model.StatusDownloaded {
				dl, err := r.db.GetDownloadByTitleID(ctx, titleID)
				if err == nil && dl != nil && dl.SourceType != "" {
					if isUpgrade(dl.SourceType, item.ReleaseType) {
						evtNotes = fmt.Sprintf("upgrade: %s → %s", dl.SourceType, item.ReleaseType)
					}
				}
			}
		}

		st := model.StatusPending
		if existing != nil && evtNotes == "" {
			switch existing.Status {
			case model.StatusRejected:
				st = model.StatusPending
			case model.StatusDownloaded:
				st = model.StatusPending
			}
		}
		if evtNotes != "" {
			st = model.StatusPending
		}

		// If previous event was downloaded and this is new, mark the old as "upgraded"
		if existing != nil && existing.Status == model.StatusDownloaded && evtNotes != "" {
			if err := r.db.UpdateReleaseEventStatus(ctx, existing.ID, model.StatusDownloaded); err != nil {
				r.log.Warn().Err(err).Msg("failed to mark previous download as upgraded")
			}
		}

		evt := &model.ReleaseEvent{
			TitleID:        titleID,
			Source:         "scraper",
			ReleaseType:    item.ReleaseType,
			ReleaseDate:    item.ReleaseDate,
			Status:         st,
			PreviousStatus: evtPrev,
			Notes:          evtNotes,
			ISOYear:        progYear,
			ISOWeek:        progWeek,
		}

		if _, err := r.db.CreateReleaseEvent(ctx, evt); err != nil {
			return fmt.Errorf("saving release event: %w", err)
		}

		existingEvent = existing
		return nil
	})

	if err != nil {
		return err
	}

	// Skip if already pending — don't stack duplicate events
	if existingEvent != nil && existingEvent.Status == model.StatusPending {
		return nil
	}

	return nil
}

// musicTargetWeek determines which release week to scrape for music.
// First run: current video week - timeshift offset.
// Subsequent runs: last discovered music week + 1.
func (r *Runner) musicTargetWeek(ctx context.Context) (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Music.InitialTimeshiftWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}

	lastYearStr, _ := r.db.GetSetting(ctx, "music_last_iso_year")
	lastWeekStr, _ := r.db.GetSetting(ctx, "music_last_iso_week")

	if lastYearStr == "" || lastWeekStr == "" {
		// First run: derive from video target week
		return r.targetYear, r.targetWeek - timeshiftWeeks
	}

	lastYear, _ := strconv.Atoi(lastYearStr)
	lastWeek, _ := strconv.Atoi(lastWeekStr)

	// Advance by 1 wmdl week
	nextWeek := lastWeek + 1
	nextYear := lastYear
	if nextWeek > 52 {
		nextWeek = 1
		nextYear++
	}

	return nextYear, nextWeek
}

// processMusicItem enriches a scraped music item with MusicBrainz data and
// stores it in the artists + albums + album_release_events tables.
func (r *Runner) processMusicItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	apiCtx, apiCancel := context.WithTimeout(ctx, 20*time.Second)
	defer apiCancel()

	// Filter: check if this album passes quality thresholds
	// Standard types (LP, EP, soundtrack): require score + review count
	// Special types (live, remix, box set): score only, no review count
	ft := r.cfg.MediaTypes.Music.Filter
	isStandard := item.AlbumType == model.AlbumTypeLP ||
		item.AlbumType == model.AlbumTypeEP ||
		item.AlbumType == model.AlbumTypeSoundtrack

	var passesFilter bool
	if isStandard {
		passesFilter = (item.AOTYCriticScore >= float64(ft.MinCriticScore) && item.AOTYCriticCount >= ft.MinCriticReviews) ||
			(item.AOTYUserScore >= float64(ft.MinUserScore) && item.AOTYUserCount >= ft.MinUserRatings) ||
			(ft.IncludeMustHear && item.AOTYMustHear)
	} else {
		passesFilter = (ft.MinCriticScore > 0 && item.AOTYCriticScore >= float64(ft.MinCriticScore)) ||
			(ft.MinUserScore > 0 && item.AOTYUserScore >= float64(ft.MinUserScore)) ||
			(ft.IncludeMustHear && item.AOTYMustHear)
	}

	r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).
		Bool("passes", passesFilter).Msg("music item filter check")

	// Step 1: MusicBrainz enrichment
	rgResult, err := r.mb.SearchReleaseGroup(apiCtx, item.Title, item.ArtistName)
	if err != nil {
		r.log.Warn().Err(err).Msg("MusicBrainz search failed, continuing without MB data")
	} else if rgResult == nil {
		r.log.Info().Str("album", item.Title).Msg("not found in MusicBrainz, skipping")
		return nil // If not in MB, can't integrate with Lidarr
	}

	// If MB returned a result, search for artist
	mbArtistID := rgResult.ArtistMBID
	mbAlbumID := rgResult.MBID
	mbArtistName := rgResult.ArtistName
	if mbArtistName == "" {
		mbArtistName = item.ArtistName
	}

	if mbArtistID == "" {
		artResult, err := r.mb.SearchArtist(apiCtx, item.ArtistName)
		if err == nil && artResult != nil {
			mbArtistID = artResult.MBID
		}
	}

	if mbArtistID == "" {
		r.log.Info().Str("artist", item.ArtistName).Msg("artist not found in MusicBrainz, skipping")
		return nil
	}

	// Step 2: Fetch detail for MB rating
	if mbAlbumID != "" {
		detail, err := r.mb.GetReleaseGroupDetail(apiCtx, mbAlbumID)
		if err == nil && detail != nil && detail.Rating > 0 {
			_ = detail // rating available in detail.Rating
		}
	}

	// Step 3: Upsert artist
	artist := &model.Artist{
		MBID: mbArtistID,
		Name: mbArtistName,
	}
	artistID, err := r.db.UpsertArtist(ctx, artist)
	if err != nil {
		return fmt.Errorf("saving artist: %w", err)
	}

	// Step 4: Upsert album
	mbRating := 0.0
	if rgResult != nil {
		mbRating = rgResult.Rating
	}

	album := &model.Album{
		ArtistID:       artistID,
		Title:          item.Title,
		Year:           item.Year,
		MBID:           mbAlbumID,
		AlbumType:      item.AlbumType,
		ReleaseDate:    item.ReleaseDate,
		AOTYCriticScore: item.AOTYCriticScore,
		AOTYCriticCount: item.AOTYCriticCount,
		AOTYUserScore:   item.AOTYUserScore,
		AOTYUserCount:   item.AOTYUserCount,
		AOTYMustHear:    item.AOTYMustHear,
		MBRating:        mbRating,
	}
	albumID, err := r.db.UpsertAlbum(ctx, album)
	if err != nil {
		return fmt.Errorf("saving album: %w", err)
	}

	// Step 5: Create release event (only if passes filter)
	existing, err := r.db.GetLatestAlbumReleaseEvent(ctx, albumID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}
	if existing != nil && existing.Status == model.StatusPending {
		return nil // already pending
	}

	if !passesFilter {
		r.log.Info().Str("album", item.Title).Msg("does not pass filter, skipping review event")
		return nil
	}

	evt := &model.AlbumReleaseEvent{
		AlbumID:     albumID,
		Source:      "albumoftheyear",
		ReleaseDate: item.ReleaseDate,
		Status:      model.StatusPending,
		ISOYear:     progYear,
		ISOWeek:     progWeek,
	}
	if _, err := r.db.CreateAlbumReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("saving album release event: %w", err)
	}

	return nil
}

// isUpgrade checks if a new release type is an upgrade over a previous download's source type.
// Physical (BluRay) is an upgrade over streaming (Web-DL/WebRip).
// A higher-quality source within the same category is also an upgrade.
func isUpgrade(prevSource string, newReleaseType model.ReleaseType) bool {
	if prevSource == "" {
		return false
	}
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
	// Only strip metadata parentheticals like "(season 3)", "(complete series)", etc.
	// Keep parentheticals that look like alternate titles e.g. "(good boy)".
	metaParen   = regexp.MustCompile(`(?i)\s*\((season\s+\d+|complete\s+.*|series\s+\d+|vol\..*)\)`)
	trailingFmt = regexp.MustCompile(`(?i)\s+(season\s+\d+|dvd|blu-ray|4k)\s*$`)
)

func cleanTitleForSearch(title string) string {
	cleaned := html.UnescapeString(title)
	cleaned = metaParen.ReplaceAllString(cleaned, "")
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

// extractParenthetical extracts the first parenthetical group from a title.
// Used as a fallback search term when the primary search yields a non-exact match.
func extractParenthetical(title string) string {
	re := regexp.MustCompile(`\(([^)]+)\)`)
	m := re.FindStringSubmatch(title)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
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
