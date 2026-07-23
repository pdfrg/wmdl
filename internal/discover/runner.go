package discover

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"

	"github.com/pdfrg/wmdl/internal/browser"
	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/notifier"
)

type asyncResult[T any] struct {
	ready chan struct{}
	val   T
	err   error
}

func newAsyncResult[T any]() *asyncResult[T] {
	return &asyncResult[T]{ready: make(chan struct{})}
}

func (a *asyncResult[T]) done(val T, err error) {
	a.val = val
	a.err = err
	close(a.ready)
}

func (a *asyncResult[T]) wait(ctx context.Context) (T, error) {
	select {
	case <-a.ready:
		return a.val, a.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

type LookbackRange struct {
	Min, Max int
}

type LookbackOverrides map[string]LookbackRange

type Runner struct {
	log             zerolog.Logger
	cfg             *config.Config
	db              *db.DB
	tmdb            *TMDBClient
	rt              *RTFinder
	omdb            *OMDBClient
	mb              *MBClient
	hc              *HardcoverClient
	ol              *OLClient
	radarr          *library.RadarrClient
	sonarr          *library.SonarrClient
	lidarr          *library.LidarrClient
	notify          notifier.Notifier
	debugURL        string
	allocCtx        context.Context
	allocCancel     context.CancelFunc
	browserCtx      context.Context // single browser instance shared by all chromedp providers
	targetYear      int
	targetWeek      int
	hasTargetWeek   bool
	headless        bool
	killBrave       func() error
	mediaTypeFilter model.MediaType // "" = all types

	lookbackOverrides LookbackOverrides

	bookClient library.BookClient
	radarrRes  *asyncResult[[]library.RadarrMovie]
	sonarrRes  *asyncResult[[]library.SonarrSeries]
	lidarrRes  *asyncResult[struct {
		artists []library.LidarrArtist
		albums  []library.LidarrAlbum
	}]
}

func (r *Runner) SetMediaTypeFilter(mt model.MediaType) {
	r.mediaTypeFilter = mt
}

func (r *Runner) SetTargetWeek(year, week int) {
	r.targetYear = year
	r.targetWeek = week
	r.hasTargetWeek = true
}

func (r *Runner) SetLookbackOverrides(ov LookbackOverrides) {
	r.lookbackOverrides = ov
}

func (r *Runner) hasLookbackOverride(key string) bool {
	if r.lookbackOverrides == nil {
		return false
	}
	_, ok := r.lookbackOverrides[key]
	return ok
}

// lookbackRange returns the (min, max) lookback range for a given key.
// If an override is present, it takes precedence. Otherwise returns (val, val).
func (r *Runner) lookbackRange(key string, cfgVal int) (int, int) {
	if ov, ok := r.lookbackOverrides[key]; ok {
		return ov.Min, ov.Max
	}
	return cfgVal, cfgVal
}

func NewRunner(logger zerolog.Logger, cfg *config.Config, database *db.DB, headless bool) *Runner {
	notify, err := notifier.New(cfg.Notifier)
	if err != nil {
		logger.Warn().Err(err).Msg("notifier unavailable")
		notify = nil
	}

	var hcClient *HardcoverClient
	if cfg.Hardcover.APIKey != "" {
		hcClient = NewHardcoverClient(cfg.Hardcover.APIKey)
	}

	r := &Runner{
		log:      logger,
		cfg:      cfg,
		db:       database,
		tmdb:     NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken),
		rt:       NewRTFinder(),
		omdb:     NewOMDBClient(cfg.OMDB.APIKey),
		mb:       NewMBClient(),
		hc:       hcClient,
		ol:       NewOLClient(),
		notify:   notify,
		debugURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.Browser.DebugPort),
		headless: headless,
	}

	if cfg.Library.Radarr.URL != "" && cfg.Library.Radarr.APIKey != "" {
		r.radarr = library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey, cfg.Library.Radarr.Timeout)
	}
	if cfg.Library.Sonarr.URL != "" && cfg.Library.Sonarr.APIKey != "" {
		r.sonarr = library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey, cfg.Library.Sonarr.Timeout)
	}
	if cfg.Library.Lidarr.URL != "" && cfg.Library.Lidarr.APIKey != "" {
		r.lidarr = library.NewLidarrClient(cfg.Library.Lidarr.URL, cfg.Library.Lidarr.APIKey, cfg.Library.Lidarr.Timeout)
	}
	if bc := library.NewBookClient(cfg.Library.BookBackend, cfg.Library.LazyLibrarian.URL, cfg.Library.LazyLibrarian.APIKey, cfg.Library.LazyLibrarian.Timeout); bc != nil {
		r.bookClient = bc
	}

	return r
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.TMDB.APIKey == "" && r.cfg.TMDB.AccessToken == "" {
		return fmt.Errorf("TMDB not configured: set tmdb.api_key or tmdb.access_token in config\n  Get a free API key at https://www.themoviedb.org/settings/api")
	}

	wantMovie := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeMovie
	wantTV := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeTV
	wantAnime := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeAnime
	wantMusic := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeMusic
	wantBooks := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeBook

	// Pre-flight healthchecks for enrichment services.
	// Network errors are warnings (may be transient), auth failures surface in the error text.
	if wantMovie || wantTV {
		if err := r.tmdb.Ping(ctx); err != nil {
			r.log.Warn().Err(err).Msg("tmdb healthcheck failed — enrichment will be degraded")
		}
	}
	if wantBooks && r.hc != nil {
		if err := r.hc.Ping(ctx); err != nil {
			r.log.Warn().Err(err).Msg("hardcover healthcheck failed — will fall back to OpenLibrary")
		}
	}

	// Kick off *arr library fetches in background so they run during scraping.
	if r.sonarr != nil {
		r.sonarrRes = newAsyncResult[[]library.SonarrSeries]()
		go func() {
			series, err := r.sonarr.GetAllSeries(ctx)
			r.sonarrRes.done(series, err)
		}()
	}
	if r.radarr != nil {
		r.radarrRes = newAsyncResult[[]library.RadarrMovie]()
		go func() {
			movies, err := r.radarr.GetAllMovies(ctx)
			r.radarrRes.done(movies, err)
		}()
	}
	if r.lidarr != nil {
		r.lidarrRes = newAsyncResult[struct {
			artists []library.LidarrArtist
			albums  []library.LidarrAlbum
		}]()
		go func() {
			artists, aErr := r.lidarr.GetAllArtists(ctx)
			albums, alErr := r.lidarr.GetAllAlbums(ctx)
			if aErr != nil {
				r.lidarrRes.done(struct {
					artists []library.LidarrArtist
					albums  []library.LidarrAlbum
				}{}, aErr)
				return
			}
			r.lidarrRes.done(struct {
				artists []library.LidarrArtist
				albums  []library.LidarrAlbum
			}{artists, albums}, alErr)
		}()
	}

	// Auto-launch Brave if not already running on the debug port
	// Needed for video/TV/movie processing, AllMusic, and Goodreads scraping.
	if (wantMovie || wantTV) || wantMusic || wantBooks {
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

		// Pre-allocate the browser so all providers share one WebSocket connection.
		// Each provider creates its own tab via NewContext(browserCtx) without
		// opening a new CDP WebSocket — Chrome only supports one at a time.
		r.browserCtx, _ = chromedp.NewContext(r.allocCtx,
			chromedp.WithLogf(func(s string, v ...any) {
				r.log.Debug().Str("source", "chromedp").Msgf(s, v...)
			}),
			chromedp.WithErrorf(func(s string, v ...any) {
				r.log.Debug().Str("source", "chromedp").Msgf(s, v...)
			}),
		)

		// Force-allocate the browser with the options above so all derived
		// provider contexts inherit an existing browser and won't call
		// Allocate again (preventing duplicate WebSocket connections).
		if err := chromedp.Run(r.browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return nil
		})); err != nil {
			r.log.Warn().Err(err).Msg("failed to pre-allocate chromedp browser, disabling browser providers")
			r.browserCtx = nil
		}
	}

	providers, err := r.buildProviders(wantMovie, wantTV, wantAnime, wantMusic, wantBooks)
	if err != nil {
		return err
	}

	var allItems []ScrapedItem

	for _, p := range providers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.log.Info().Str("provider", p.Name()).Msg("scraping")
		items, err := p.Scrape()
		if err != nil {
			r.log.Warn().Err(err).Str("provider", p.Name()).Msg("scrape failed")
			continue
		}
		r.log.Info().Int("count", len(items)).Str("provider", p.Name()).Msg("found items")
		allItems = append(allItems, items...)
	}

	// Log bookshop pre-population detail (items stored under a different week)
	if r.cfg.MediaTypes.Books.Enabled {
		var bsCount int
		var bsReleaseDate string
		for _, item := range allItems {
			if item.Source == "bookshop" || strings.Contains(item.Source, "bookshop") {
				if bsCount == 0 {
					bsReleaseDate = item.ReleaseDate
				}
				bsCount++
			}
		}
		if bsCount > 0 && bsReleaseDate != "" {
			if d, err := time.Parse("January 2, 2006", bsReleaseDate); err == nil {
				storeYear, storeWeek := d.ISOWeek()
				reviewStart := tuesdayOfISOWeek(storeYear, storeWeek).AddDate(0, 0, 1)
				ts := r.cfg.MediaTypes.Books.LookbackWeeks
				r.log.Info().
					Int("count", bsCount).
					Str("release_date", d.Format("2006-01-02")).
					Str("stored_under", fmt.Sprintf("%d-W%02d", storeYear, storeWeek)).
					Int("timeshift_weeks", ts).
					Str("review_from", reviewStart.Format("2006-01-02")).
					Msg("bookshop: future week pre-population")
			} else if d, err := time.Parse("Jan 2, 2006", bsReleaseDate); err == nil {
				storeYear, storeWeek := d.ISOWeek()
				reviewStart := tuesdayOfISOWeek(storeYear, storeWeek).AddDate(0, 0, 1)
				ts := r.cfg.MediaTypes.Books.LookbackWeeks
				r.log.Info().
					Int("count", bsCount).
					Str("release_date", d.Format("2006-01-02")).
					Str("stored_under", fmt.Sprintf("%d-W%02d", storeYear, storeWeek)).
					Int("timeshift_weeks", ts).
					Str("review_from", reviewStart.Format("2006-01-02")).
					Msg("bookshop: future week pre-population")
			}
		}
	}

	if len(allItems) == 0 {
		r.log.Info().Msg("no new releases found")
		return nil
	}

	// Split video, anime, music, and book items
	var videoItems, animeItems, musicItems, bookItems []ScrapedItem
	for _, item := range allItems {
		switch item.MediaType {
		case model.MediaTypeMusic:
			musicItems = append(musicItems, item)
		case model.MediaTypeAnime:
			animeItems = append(animeItems, item)
		case model.MediaTypeBook:
			bookItems = append(bookItems, item)
		default:
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

	// Deduplicate music items by artist+album (normalize titles: strip bracketed suffixes)
	seenMusic := make(map[string]bool)
	var uniqueMusic []ScrapedItem
	for _, item := range musicItems {
		title := strings.TrimSpace(allMusicBracketRe.ReplaceAllString(item.Title, ""))
		key := fmt.Sprintf("%s|%s", item.ArtistName, title)
		if seenMusic[key] {
			continue
		}
		seenMusic[key] = true
		uniqueMusic = append(uniqueMusic, item)
	}

	// Deduplicate anime items by title+year
	seenAnime := make(map[string]bool)
	var uniqueAnime []ScrapedItem
	for _, item := range animeItems {
		key := fmt.Sprintf("%s|%d", item.Title, item.Year)
		if seenAnime[key] {
			continue
		}
		seenAnime[key] = true
		uniqueAnime = append(uniqueAnime, item)
	}

	// Deduplicate book items by normalized author+title — merge sources instead of discarding
	bookMap := make(map[string]*ScrapedItem)
	for i := range bookItems {
		key := fmt.Sprintf("%s|%s", normalizeBookKey(bookItems[i].ArtistName), normalizeBookKey(bookItems[i].Title))
		if existing, ok := bookMap[key]; ok {
			mergeBookItems(existing, &bookItems[i])
		} else {
			bookMap[key] = &bookItems[i]
		}
	}
	bookItems = make([]ScrapedItem, 0, len(bookMap))
	for _, item := range bookMap {
		bookItems = append(bookItems, *item)
	}

	// Filter out FlixPatrol items tagged as "Anime" genre when the MAL API
	// already tracks them. Items unknown stay in the pipeline as a
	// second-chance fallback (e.g. shows below member thresholds).
	if r.cfg.MediaTypes.Anime.FilterFlixPatrolAnime {
		var filtered []ScrapedItem
		for _, item := range uniqueVideos {
			if item.Source == "flixpatrol" && item.Genres == "Anime" {
				found, err := malAnimeExists(ctx, item.Title)
				if err != nil {
					r.log.Warn().Err(err).Str("title", item.Title).
						Msg("mal search failed, keeping flixpatrol item")
				} else if found {
					r.log.Debug().Str("title", item.Title).
						Msg("flixpatrol: skipping anime-genre item already tracked")
					continue
				}
			}
			filtered = append(filtered, item)
		}
		uniqueVideos = filtered
	}

	progYear, progWeek := r.targetYear, r.targetWeek
	if !r.hasTargetWeek {
		progYear, progWeek = programWeekFromItems(uniqueVideos)
	}

	var processed int

	// Process video items (only when type filter matches)
	if (wantMovie || wantTV) && len(uniqueVideos) > 0 {
		// When filtering to a specific video subtype (movie or tv),
		// only process items matching that type.
		toProcess := uniqueVideos
		if r.mediaTypeFilter == model.MediaTypeMovie || r.mediaTypeFilter == model.MediaTypeTV {
			var filtered []ScrapedItem
			for _, item := range uniqueVideos {
				if item.MediaType == r.mediaTypeFilter {
					filtered = append(filtered, item)
				}
			}
			toProcess = filtered
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3)
		wg.Add(len(toProcess))
		for _, item := range toProcess {
			go func(item ScrapedItem) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()

				itemCtx, itemCancel := context.WithTimeout(ctx, 2*time.Minute)
				defer itemCancel()

				if err := r.processItem(itemCtx, item, progYear, progWeek); err != nil {
					r.log.Warn().Err(err).Str("title", item.Title).Msg("error processing item")
				}
				mu.Lock()
				processed++
				mu.Unlock()
			}(item)
		}
		select {
		case <-waitDone(ctx, &wg):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Process anime items (with Jikan data, no TMDB/IMDb/RT enrichment)
	if wantAnime && len(animeItems) > 0 {
		var muAnime sync.Mutex
		var wgAnime sync.WaitGroup
		semAnime := make(chan struct{}, 3)
		wgAnime.Add(len(animeItems))
		for _, item := range animeItems {
			go func(item ScrapedItem) {
				defer wgAnime.Done()
				select {
				case semAnime <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semAnime }()
				itemCtx, itemCancel := context.WithTimeout(ctx, 30*time.Second)
				defer itemCancel()
				if err := r.processAnimeItem(itemCtx, item, progYear, progWeek); err != nil {
					r.log.Warn().Err(err).Str("title", item.Title).Msg("error processing anime item")
				}
				muAnime.Lock()
				processed++
				muAnime.Unlock()
			}(item)
		}
		select {
		case <-waitDone(ctx, &wgAnime):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Process music items (with MusicBrainz enrichment)
	if wantMusic && len(uniqueMusic) > 0 {
		var muMusic sync.Mutex
		var wgMusic sync.WaitGroup
		var musicProcessed int
		semMusic := make(chan struct{}, 2)
		wgMusic.Add(len(uniqueMusic))
		for _, item := range uniqueMusic {
			go func(item ScrapedItem) {
				defer wgMusic.Done()
				select {
				case semMusic <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semMusic }()
				itemCtx, itemCancel := context.WithTimeout(ctx, 2*time.Minute)
				defer itemCancel()
				// Store under the atomic wmdl week so review finds them
				if err := r.processMusicItem(itemCtx, item, progYear, progWeek); err != nil {
					r.log.Warn().Err(err).Str("album", item.Title).Str("artist", item.ArtistName).Msg("error processing music item")
				}
				muMusic.Lock()
				musicProcessed++
				count := musicProcessed
				muMusic.Unlock()
				r.log.Info().Int("processed", count).Int("total", len(uniqueMusic)).Msg("music processing progress")
			}(item)
		}
		select {
		case <-waitDone(ctx, &wgMusic):
		case <-ctx.Done():
			return ctx.Err()
		}
		processed += musicProcessed
	}

	// Process book items (with Hardcover + Open Library enrichment)
	if wantBooks && len(bookItems) > 0 {
		var muBooks sync.Mutex
		var wgBooks sync.WaitGroup
		var bookProcessed int
		semBooks := make(chan struct{}, 2)
		wgBooks.Add(len(bookItems))
		for _, item := range bookItems {
			go func(item ScrapedItem) {
				defer wgBooks.Done()
				select {
				case semBooks <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semBooks }()
				itemCtx, itemCancel := context.WithTimeout(ctx, 2*time.Minute)
				defer itemCancel()
				if err := r.processBookItem(itemCtx, item, progYear, progWeek); err != nil {
					r.log.Warn().Err(err).Str("book", item.Title).Msg("error processing book item")
				}
				muBooks.Lock()
				bookProcessed++
				count := bookProcessed
				muBooks.Unlock()
				r.log.Info().Int("processed", count).Int("total", len(bookItems)).Msg("book processing progress")
			}(item)
		}
		select {
		case <-waitDone(ctx, &wgBooks):
		case <-ctx.Done():
			return ctx.Err()
		}
		processed += bookProcessed
	}

	// Post-processing: merge duplicate book records that share an ISBN13.
	// This catches race-condition duplicates from parallel goroutines and
	// cross-session duplicates where enrichment produced different titles.
	if len(bookItems) > 0 {
		if err := r.deduplicateBookEvents(ctx); err != nil {
			r.log.Warn().Err(err).Msg("book deduplication failed")
		}
	}

	eventCount, _ := r.db.CountReleaseEventsByWeek(ctx, progYear, progWeek)
	albumCount, _ := r.db.CountAlbumReleaseEventsByWeek(ctx, progYear, progWeek)
	bookEventCount, _ := r.db.CountBookReleaseEventsByWeek(ctx, progYear, progWeek)
	totalEvents := eventCount + albumCount + bookEventCount
	r.log.Info().Msgf("Week %d-W%02d: %d events in DB, %d items processed",
		progYear, progWeek, totalEvents, processed)

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

	// Cache library data from Radarr/Sonarr/Lidarr for review and process use.
	// Best-effort: failures only log a warning; the week is already discovered.
	if err := r.cacheLibraryData(ctx); err != nil {
		r.log.Warn().Err(err).Msg("library cache failed (will be fetched during process)")
	}

	r.sendNotification(ctx, wantMovie, wantTV, wantAnime, wantMusic, wantBooks, uniqueVideos, uniqueAnime, uniqueMusic, bookItems, progYear, progWeek, totalEvents)

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

func addISOWeekOffset(year, week, offset int) (int, int) {
	t := tuesdayOfISOWeek(year, week)
	t = t.AddDate(0, 0, -7*offset)
	return t.ISOWeek()
}

// waitDone returns a channel that closes when the WaitGroup counter reaches zero.
// If the context is cancelled first, the channel closes without waiting for the WaitGroup.
func waitDone(ctx context.Context, wg *sync.WaitGroup) chan struct{} {
	done := make(chan struct{})
	go func() {
		doneCh := make(chan struct{})
		go func() {
			wg.Wait()
			close(doneCh)
		}()
		select {
		case <-doneCh:
		case <-ctx.Done():
		}
		close(done)
	}()
	return done
}

var (
	// Only strip metadata parentheticals like "(season 3)", "(complete series)", etc.
	// Keep parentheticals that look like alternate titles e.g. "(good boy)".
	metaParen   = regexp.MustCompile(`(?i)\s*\((season\s+\d+|complete\s+.*|series\s+\d+|vol\..*)\)`)
	trailingFmt = regexp.MustCompile(`(?i)\s+(season\s+\d+|dvd|blu-ray|4k)\s*$`)
	// Strip AllMusic formatting suffixes like [2 CD], [Deluxe Edition], [Super Deluxe]
	allMusicBracketRe = regexp.MustCompile(`\s*\[[^\]]*\]`)
	goodreadsSizeRe   = regexp.MustCompile(`\._S[XY]\d+_\.`)
	bookmarksWpSizeRe = regexp.MustCompile(`(-\d+x\d+)(\.[a-zA-Z]+)$`)
	bookYearParenRe   = regexp.MustCompile(`\s*\(\d{4}\)`)
	bookSubtitleRe    = regexp.MustCompile(`\s*[;:].*`)
)

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
