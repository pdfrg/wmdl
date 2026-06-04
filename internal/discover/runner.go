package discover

import (
	"context"
	"database/sql"
	"encoding/json"
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

type Runner struct {
	log             zerolog.Logger
	cfg             *config.Config
	db              *db.DB
	tmdb            *TMDBClient
	rt              *RTFinder
	imdb            *IMDbAPIClient
	mb              *MBClient
	hc              *HardcoverClient
	ol              *OLClient
	radarr          *library.RadarrClient
	sonarr          *library.SonarrClient
	lidarr          *library.LidarrClient
	abs             *library.AudiobookshelfClient
	notify          notifier.Notifier
	debugURL        string
	allocCtx        context.Context
	allocCancel     context.CancelFunc
	targetYear      int
	targetWeek      int
	hasTargetWeek   bool
	headless        bool
	killBrave       func() error
	mediaTypeFilter model.MediaType // "" = all types

	radarrRes *asyncResult[[]library.RadarrMovie]
	sonarrRes *asyncResult[[]library.SonarrSeries]
	lidarrRes *asyncResult[struct {
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
		imdb:     NewIMDbAPIClient(),
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

	if cfg.Library.Audiobookshelf.URL != "" && cfg.Library.Audiobookshelf.APIKey != "" {
		r.abs = library.NewAudiobookshelfClient(
			cfg.Library.Audiobookshelf.URL,
			cfg.Library.Audiobookshelf.APIKey,
			cfg.Library.Audiobookshelf.LibraryID,
			cfg.Library.Audiobookshelf.Timeout,
		)
	}

	return r
}

func (r *Runner) cacheLibraryData(ctx context.Context) error {
	if r.radarrRes == nil && r.sonarrRes == nil && r.lidarrRes == nil && r.abs == nil {
		return nil
	}
	r.log.Info().Msg("persisting library cache from background fetches")

	var sonarrSeries []library.SonarrSeries

	if r.sonarrRes != nil {
		r.log.Info().Msg("waiting for Sonarr library...")
		series, err := r.sonarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to fetch Sonarr library")
		} else {
			sonarrSeries = series
			var entries []db.LibraryCache
			for _, s := range series {
				details, _ := json.Marshal(s)
				entries = append(entries, db.LibraryCache{
					Source: "sonarr", ExtID: strconv.Itoa(s.TVDBID),
					ArrID: int64(s.ID), ArrTitle: s.Title, Details: string(details),
				})
			}
			if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
				r.log.Warn().Err(err).Msg("failed to save Sonarr library cache")
			} else {
				r.log.Info().Int("count", len(series)).Msg("cached Sonarr library")
			}
		}
	}

	if r.radarrRes != nil {
		r.log.Info().Msg("waiting for Radarr library...")
		movies, err := r.radarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to fetch Radarr library")
		} else {
			var entries []db.LibraryCache
			for _, m := range movies {
				details, _ := json.Marshal(m)
				entries = append(entries, db.LibraryCache{
					Source: "radarr", ExtID: strconv.Itoa(m.TMDBID),
					ArrID: int64(m.ID), ArrTitle: m.Title, Details: string(details),
				})
			}
			if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
				r.log.Warn().Err(err).Msg("failed to save Radarr library cache")
			} else {
				r.log.Info().Int("count", len(movies)).Msg("cached Radarr library")
			}
		}
	}

	if r.lidarrRes != nil {
		r.log.Info().Msg("waiting for Lidarr library...")
		result, err := r.lidarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to fetch Lidarr library")
		} else {
			{
				var entries []db.LibraryCache
				for _, a := range result.artists {
					details, _ := json.Marshal(a)
					entries = append(entries, db.LibraryCache{
						Source: "lidarr", ExtID: a.MBID,
						ArrID: int64(a.ID), ArrTitle: a.ArtistName, Details: string(details),
					})
				}
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save Lidarr artist cache")
				} else {
					r.log.Info().Int("count", len(result.artists)).Msg("cached Lidarr artists")
				}
			}
			{
				var entries []db.LibraryCache
				for _, a := range result.albums {
					details, _ := json.Marshal(a)
					entries = append(entries, db.LibraryCache{
						Source: "lidarr-album", ExtID: a.ForeignAlbumID,
						ArrID: int64(a.ID), ArrTitle: a.Title, Details: string(details),
					})
				}
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save Lidarr album cache")
				} else {
					r.log.Info().Int("count", len(result.albums)).Msg("cached Lidarr albums")
				}
			}
		}
	}

	// Cache Audiobookshelf library items by ISBN/ASIN for library status display
	if r.abs != nil {
		r.log.Info().Msg("fetching Audiobookshelf library...")
		items, err := r.abs.GetLibraryItems(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to fetch Audiobookshelf library")
		} else {
			var entries []db.LibraryCache
			for _, item := range items {
				meta := item.Media.Metadata
				details, _ := json.Marshal(item)
				if meta.ISBN != "" {
					entries = append(entries, db.LibraryCache{
						Source: "abs", ExtID: meta.ISBN,
						ArrID: 0, ArrTitle: meta.Title, Details: string(details),
					})
				}
				if meta.ASIN != "" && meta.ASIN != meta.ISBN {
					entries = append(entries, db.LibraryCache{
						Source: "abs", ExtID: meta.ASIN,
						ArrID: 0, ArrTitle: meta.Title, Details: string(details),
					})
				}
			}
			if len(entries) > 0 {
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save Audiobookshelf library cache")
				} else {
					r.log.Info().Int("count", len(items)).Msg("cached Audiobookshelf library")
				}
			}
		}
	}

	// Anime title matching: for items with mal_id but no tvdb_id, try to match
	// against cached Sonarr series by title.
	if len(sonarrSeries) > 0 {
		titles, err := r.db.ListTitles(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to list titles for anime matching")
			return nil
		}
		for _, t := range titles {
			if t.MediaType != model.MediaTypeAnime || t.TvdbID != 0 || t.MalID == 0 {
				continue
			}
			matchTitle := strings.ToLower(strings.TrimSpace(t.Title))
			if idx := strings.Index(matchTitle, "(season"); idx > 0 {
				matchTitle = strings.TrimSpace(matchTitle[:idx])
			}
			for _, s := range sonarrSeries {
				seriesTitle := strings.ToLower(strings.TrimSpace(s.Title))
				if matchTitle == seriesTitle || strings.HasPrefix(seriesTitle, matchTitle) || strings.HasPrefix(matchTitle, seriesTitle) {
					r.log.Info().Str("anime", t.Title).Int("tvdb_id", s.TVDBID).Str("sonarr_title", s.Title).Msg("matched anime to Sonarr series")
					if err := r.db.UpdateTitleTvdbID(ctx, t.ID, s.TVDBID); err != nil {
						r.log.Warn().Err(err).Msg("failed to update anime tvdb_id")
						continue
					}
					t.TvdbID = s.TVDBID
					details, _ := json.Marshal(s)
					if err := r.db.UpsertLibraryCache(ctx, "sonarr", strconv.Itoa(s.TVDBID), int64(s.ID), s.Title, string(details)); err != nil {
						r.log.Warn().Err(err).Msg("failed to upsert sonarr cache for matched anime")
					}
					break
				}
			}
		}
	}

	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.TMDB.APIKey == "" && r.cfg.TMDB.AccessToken == "" {
		return fmt.Errorf("TMDB not configured: set tmdb.api_key or tmdb.access_token in config\n  Get a free API key at https://www.themoviedb.org/settings/api")
	}

	wantVideo := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeMovie || r.mediaTypeFilter == model.MediaTypeTV
	wantAnime := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeAnime
	wantMusic := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeMusic
	wantBooks := r.mediaTypeFilter == "" || r.mediaTypeFilter == model.MediaTypeBook

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
	if wantVideo || wantMusic || wantBooks {
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
	}

	providers := []ReleaseProvider{}

	// Video/movie/TV providers (DVD release dates, TMDB discover, FlixPatrol via chromedp)
	if wantVideo {
		providers = append(providers, NewDVDReleaseDates(), NewTMDBDiscoverProvider(r.tmdb))

		fp := NewFlixPatrolProvider(r.debugURL)
		providers = append(providers, fp)

		if r.hasTargetWeek {
			for _, p := range providers {
				if ws, ok := p.(WeekSettable); ok {
					ws.SetWeekRange(r.targetYear, r.targetWeek)
				}
			}
		}
	}

	// Anime provider (gated on config and type filter)
	if wantAnime && r.cfg.MediaTypes.Anime.Enabled {
		jikan := NewJikanAnimeProvider(r.cfg.MediaTypes.Anime)
		if r.hasTargetWeek {
			animeYear, animeWeek := r.animeTargetWeek()
			jikan.SetWeekRange(animeYear, animeWeek)
		}
		providers = append(providers, jikan)
	}

	// Book providers (gated on config and type filter)
	if wantBooks && r.cfg.MediaTypes.Books.Enabled {
		// Goodreads requires chromedp/Brave
		if r.allocCtx != nil {
			gr := NewGoodreadsProvider(r.debugURL, r.allocCtx)
			if r.hasTargetWeek {
				bookYear, bookWeek := r.bookTargetWeek()
				gr.SetWeekRange(bookYear, bookWeek)
			}
			providers = append(providers, gr)
		}
	}

	// Music providers (gated on config and type filter)
	if wantMusic && r.cfg.MediaTypes.Music.Enabled {
		aoty := NewAOTYProvider(r.cfg.MediaTypes.Music.Filter)
		if r.hasTargetWeek {
			musicYear, musicWeek := r.musicTargetWeek()
			aoty.SetWeekRange(musicYear, musicWeek)
		}
		providers = append(providers, aoty)

		// AllMusic Editor's Choice (requires chromedp/Brave)
		if r.allocCtx != nil {
			allmusic := NewAllMusicProvider(r.debugURL, r.allocCtx)
			if r.hasTargetWeek {
				allmusic.SetWeekRange(r.targetYear, r.targetWeek)
			}
			providers = append(providers, allmusic)
		}
	}

	if len(providers) == 0 {
		return fmt.Errorf("no providers enabled for type filter %q", r.mediaTypeFilter)
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

	// Deduplicate book items by author+title
	seenBooks := make(map[string]bool)
	var uniqueBooks []ScrapedItem
	for _, item := range bookItems {
		key := fmt.Sprintf("%s|%s", item.ArtistName, item.Title)
		if seenBooks[key] {
			continue
		}
		seenBooks[key] = true
		uniqueBooks = append(uniqueBooks, item)
	}
	bookItems = uniqueBooks

	progYear, progWeek := r.targetYear, r.targetWeek
	if !r.hasTargetWeek {
		progYear, progWeek = programWeekFromItems(uniqueVideos)
	}

	var processed int
	totalItems := len(uniqueVideos) + len(animeItems) + len(uniqueMusic) + len(uniqueBooks)

	// Process video items (only when type filter matches)
	if wantVideo && len(uniqueVideos) > 0 {
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

	eventCount, _ := r.db.CountReleaseEventsByWeek(ctx, progYear, progWeek)
	albumCount, _ := r.db.CountAlbumReleaseEventsByWeek(ctx, progYear, progWeek)
	bookEventCount, _ := r.db.CountBookReleaseEventsByWeek(ctx, progYear, progWeek)
	totalEvents := eventCount + albumCount + bookEventCount
	skipped := processed - totalEvents
	if skipped > 0 {
		r.log.Info().Msgf("Processed %d/%d items (%d events created, %d duplicate%s skipped)",
			processed, totalItems, totalEvents, skipped, map[bool]string{true: "s", false: ""}[skipped != 1])
	} else {
		r.log.Info().Msgf("Processed %d/%d items (%d events created)",
			processed, totalItems, totalEvents)
	}

	// Track week state — use target week when set, never derive from
	// streaming items (which can be 2 months in the past).
	// When a type filter is active, only track if we're running for the
	// target week's primary type (skip week tracking for filtered dev runs).
	if r.mediaTypeFilter == "" && r.hasTargetWeek {
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

	// Send notification (up to 5 items, from all media types)
	if r.notify != nil && processed > 0 {
		msg := fmt.Sprintf("**%d new release%s** ready for review:\n", processed, map[bool]string{true: "s", false: ""}[processed != 1])
		type namedItem struct {
			title string
			year  int
		}
		var notifyItems []namedItem
		for _, item := range uniqueVideos {
			notifyItems = append(notifyItems, namedItem{item.Title, item.Year})
		}
		for _, item := range animeItems {
			notifyItems = append(notifyItems, namedItem{item.Title, item.Year})
		}
		for _, item := range uniqueMusic {
			notifyItems = append(notifyItems, namedItem{item.ArtistName + " — " + item.Title, item.Year})
		}
		for _, item := range bookItems {
			notifyItems = append(notifyItems, namedItem{item.ArtistName + " — " + item.Title, item.Year})
		}
		count := 0
		for _, item := range notifyItems {
			if count >= 5 {
				msg += fmt.Sprintf("\n+ %d more", processed-count)
				break
			}
			if item.year > 0 {
				msg += fmt.Sprintf("\n- %s (%d)", item.title, item.year)
			} else {
				msg += fmt.Sprintf("\n- %s", item.title)
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

	err = r.db.Transaction(ctx, func(tx *sql.Tx) error {
		id, err := r.db.UpsertTitleTx(ctx, tx, title)
		if err != nil {
			return fmt.Errorf("saving title: %w", err)
		}
		titleID = id

		existing, err := r.db.GetLatestReleaseEventTx(ctx, tx, titleID)
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
				dl, err := r.db.GetDownloadByTitleIDTx(ctx, tx, titleID)
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
			if err := r.db.UpdateReleaseEventStatusTx(ctx, tx, existing.ID, model.StatusDownloaded); err != nil {
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

		if _, err := r.db.CreateReleaseEventTx(ctx, tx, evt); err != nil {
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

// animeTargetWeek returns the week to scrape for anime.
// Uses the runner's target week directly (no timeshift), consistent
// with video/movie/TV physical media discovery.
func (r *Runner) animeTargetWeek() (int, int) {
	return r.targetYear, r.targetWeek
}

// addISOWeekOffset adds an offset (positive = past) to an ISO week/year pair,
// properly wrapping across year boundaries.
func addISOWeekOffset(year, week, offset int) (int, int) {
	t := tuesdayOfISOWeek(year, week)
	t = t.AddDate(0, 0, -7*offset)
	return t.ISOWeek()
}

// musicTargetWeek returns the release week to scrape for music.
// Applies the configured InitialTimeshiftWeeks offset from the runner's
// target week. Consistent with video/movie/TV physical media discovery.
func (r *Runner) musicTargetWeek() (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Music.InitialTimeshiftWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}

	return addISOWeekOffset(r.targetYear, r.targetWeek, timeshiftWeeks)
}

// bookTargetWeek returns the release week to scrape for books.
// Applies the configured InitialTimeshiftWeeks offset (configurable, like music).
func (r *Runner) bookTargetWeek() (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Books.InitialTimeshiftWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}
	return addISOWeekOffset(r.targetYear, r.targetWeek, timeshiftWeeks)
}

// processMusicItem stores a scraped music item and enriches with MusicBrainz
// data when available. MB lookup is best-effort — items are always stored.
func (r *Runner) processAnimeItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	r.log.Info().Str("title", item.Title).Int("year", item.Year).Msg("processing anime item")

	title := &model.Title{
		MalID:         item.MalID,
		Title:         item.Title,
		Year:          item.Year,
		MediaType:     model.MediaTypeAnime,
		USRating:      item.USRating,
		Overview:      item.Overview,
		Genres:        item.Genres,
		PosterPath:    item.ImageURL,
		TmdbRating:    item.ImdbRating, // stores MAL score for display
		AnimeType:     item.AnimeType,
		AnimeEpisodes: item.AnimeEpisodes,
		AnimeStatus:   item.AnimeStatus,
		AnimeMembers:  item.AnimeMembers,
		AnimeRank:     item.AnimeRank,
		AnimeSource:   item.AnimeSource,
		AnimeStudio:   item.AnimeStudio,
		Themes:        item.Themes,
		Demographics:  item.Demographics,
		Streaming:     item.Streaming,
	}

	var titleID int64
	var existingEvent *model.ReleaseEvent

	err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
		id, err := r.db.UpsertTitleTx(ctx, tx, title)
		if err != nil {
			return fmt.Errorf("saving anime title: %w", err)
		}
		titleID = id

		existing, err := r.db.GetLatestReleaseEventTx(ctx, tx, titleID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		if existing != nil {
			if existing.Status == model.StatusPending {
				existingEvent = existing
				return nil
			}
			if existing.Source == item.Source &&
				(existing.Status == model.StatusDownloaded || existing.Status == model.StatusRejected) {
				r.log.Info().Str("title", item.Title).Msg("anime already handled, skipping")
				return nil
			}
		}

		evt := &model.ReleaseEvent{
			TitleID:     titleID,
			Source:      item.Source,
			ReleaseType: model.ReleaseStreaming,
			Status:      model.StatusPending,
			Notes:       item.Notes,
			ISOYear:     progYear,
			ISOWeek:     progWeek,
		}

		if _, err := r.db.CreateReleaseEventTx(ctx, tx, evt); err != nil {
			return fmt.Errorf("saving anime release event: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	if existingEvent != nil && existingEvent.Status == model.StatusPending {
		return nil
	}

	return nil
}

func (r *Runner) processMusicItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	apiCtx, apiCancel := context.WithTimeout(ctx, 30*time.Second)
	defer apiCancel()

	// Filter: skip low-quality albums immediately (AOTY only — AllMusic is curator-filtered)
	if item.Source != "allmusic" {
		if !passesMusicFilter(r.cfg.MediaTypes.Music.Filter, item) {
			r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("does not pass filter, skipping")
			return nil
		}
	}
	r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("musicbrainz: searching release group")

	// Step 1: MusicBrainz enrichment (best-effort)
	mbAlbumID := ""
	mbArtistID := ""
	mbArtistName := item.ArtistName
	var artistDetail *MBArtistDetail
	var mbGenres []string
	mbRating := 0.0

	rgResult, err := r.mb.SearchReleaseGroup(apiCtx, item.Title, item.ArtistName)
	if err != nil {
		r.log.Warn().Err(err).Str("album", item.Title).Msg("MusicBrainz search failed, storing without MB data")
	} else if rgResult == nil {
		r.log.Info().Str("album", item.Title).Msg("not found in MusicBrainz, storing without MB data")
	} else {
		mbAlbumID = rgResult.MBID
		mbArtistID = rgResult.ArtistMBID
		if rgResult.ArtistName != "" {
			mbArtistName = rgResult.ArtistName
		}

		// Fetch artist detail by MBID for enrichment
		if mbArtistID != "" {
			r.log.Info().Str("mbid", mbArtistID).Msg("musicbrainz: fetching artist detail")
			ad, err := r.mb.GetArtist(apiCtx, mbArtistID)
			if err != nil {
				r.log.Warn().Err(err).Str("mbid", mbArtistID).Msg("musicbrainz: artist detail fetch failed")
			} else {
				artistDetail = ad
			}
		}

		// Fetch release group detail for rating and genres
		if mbAlbumID != "" {
			r.log.Info().Str("mbid", mbAlbumID).Msg("musicbrainz: fetching release group detail")
			detail, err := r.mb.GetReleaseGroupDetail(apiCtx, mbAlbumID)
			if err == nil && detail != nil {
				mbRating = detail.Rating
				mbGenres = detail.Genres
			}
		}
	}

	// Step 2: Upsert artist (always — with or without MB data)
	artist := &model.Artist{
		MBID: mbArtistID,
		Name: mbArtistName,
	}
	if artistDetail != nil {
		artist.Country = artistDetail.Country
		artist.ArtistType = artistDetail.Type
		artist.BeginDate = artistDetail.BeginDate
		artist.EndDate = artistDetail.EndDate
		artist.BeginArea = artistDetail.BeginArea
		artist.Area = artistDetail.Area
		artist.Disambiguation = artistDetail.Disambiguation
		artist.Tags = strings.Join(artistDetail.Tags, ", ")
		artist.Genres = strings.Join(artistDetail.Genres, ", ")
		artist.MBRating = artistDetail.Rating
	}
	artistID, err := r.db.UpsertArtist(ctx, artist)
	if err != nil {
		return fmt.Errorf("saving artist: %w", err)
	}

	// Step 3: Upsert album (always)
	genresStr := strings.Join(mbGenres, ", ")

	album := &model.Album{
		ArtistID:        artistID,
		Title:           item.Title,
		Year:            item.Year,
		MBID:            mbAlbumID,
		AlbumType:       item.AlbumType,
		ReleaseDate:     item.ReleaseDate,
		Genres:          genresStr,
		PosterPath:      item.ImageURL,
		AOTYURL:         item.AOTYURL,
		AOTYCriticScore: item.AOTYCriticScore,
		AOTYCriticCount: item.AOTYCriticCount,
		AOTYUserScore:   item.AOTYUserScore,
		AOTYUserCount:   item.AOTYUserCount,
		AOTYMustHear:    item.AOTYMustHear,
		AllMusicRating:  item.AllMusicRating,
		AllMusicURL:     item.AllMusicURL,
		MBRating:        mbRating,
	}
	albumID, err := r.db.UpsertAlbum(ctx, album)
	if err != nil {
		return fmt.Errorf("saving album: %w", err)
	}

	// Step 4: Create/update release event
	existing, err := r.db.GetLatestAlbumReleaseEvent(ctx, albumID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}

	if existing != nil {
		switch existing.Status {
		case model.StatusPending:
			return nil // already in review queue
		case model.StatusApproved, model.StatusDownloaded:
			return nil // already processed, don't re-queue
		case model.StatusRejected:
			// AllMusic gives rejected items a second chance
			if item.Source == "allmusic" {
				notes := existing.Notes
				if notes != "" {
					notes += "; "
				}
				notes += "AllMusic Editor's Choice"
				if err := r.db.RequeueAlbumReleaseEvent(ctx, existing.ID, "allmusic", notes); err != nil {
					return fmt.Errorf("re-queuing album release event: %w", err)
				}
				r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("re-queued from rejected by AllMusic Editor's Choice")
				return nil
			}
			return nil // AOTY item rejected, don't re-queue
		}
	}

	evt := &model.AlbumReleaseEvent{
		AlbumID:     albumID,
		Source:      item.Source,
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

func (r *Runner) processBookItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	apiCtx, apiCancel := context.WithTimeout(ctx, 30*time.Second)
	defer apiCancel()

	r.log.Info().Str("title", item.Title).Str("author", item.ArtistName).Msg("processing book item")

	// Step 1: Hardcover enrichment (best-effort, only if API key configured)
	var hcResult *HCBookResult
	if r.hc != nil {
		var hcErr error
		hcResult, hcErr = r.hc.SearchBook(apiCtx, item.Title, item.ArtistName)
		if hcErr != nil {
			r.log.Warn().Err(hcErr).Str("book", item.Title).Msg("hardcover search failed, falling back to Open Library")
		}
	}

	// Step 2: Open Library fallback
	var olResult *OLBookResult
	if hcResult == nil || hcResult.ISBN13 == "" {
		var olErr error
		olResult, olErr = r.ol.SearchBook(apiCtx, item.Title, item.ArtistName)
		if olErr != nil {
			r.log.Warn().Err(olErr).Str("book", item.Title).Msg("openlibrary search failed, storing without enrichment")
		}
	}

	// Step 3: Filter by score/rating
	minRating := r.cfg.MediaTypes.Books.Filter.MinRating
	minRatings := r.cfg.MediaTypes.Books.Filter.MinRatings

	rating := item.ImdbRating // from Goodreads
	if hcResult != nil && hcResult.Rating > 0 {
		rating = hcResult.Rating
	}
	ratingsCount := item.RatingsCount
	if hcResult != nil && hcResult.RatingsCount > 0 {
		ratingsCount = hcResult.RatingsCount
	}

	// Warn when filters are configured but enrichment data is unavailable
	enrichmentFailed := hcResult == nil && r.hc != nil
	if enrichmentFailed && (minRating > 0 || minRatings > 0) {
		r.log.Warn().Str("book", item.Title).Msg("enrichment unavailable, filter thresholds may not be applied")
	}

	if minRatings > 0 && ratingsCount > 0 && ratingsCount < minRatings {
		r.log.Info().Str("book", item.Title).Int("ratings", ratingsCount).Int("min", minRatings).Msg("below min_ratings filter, skipping")
		return nil
	}
	if minRating > 0 && rating > 0 && rating < minRating {
		r.log.Info().Str("book", item.Title).Float64("rating", rating).Float64("min", minRating).Msg("below min_rating filter, skipping")
		return nil
	}

	// Step 4: Build author from enrichment data
	authorName := item.ArtistName
	authorOLID := ""
	authorBio := ""
	authorBorn := ""
	authorDied := ""
	authorImage := ""
	var hcAuthorID int

	if hcResult != nil && hcResult.Author != nil {
		authorName = hcResult.Author.Name
		authorOLID = hcResult.Author.OLID
		authorBio = hcResult.Author.Bio
		authorBorn = hcResult.Author.BornDate
		authorDied = hcResult.Author.DeathDate
		authorImage = hcResult.Author.ImageURL
		hcAuthorID = hcResult.Author.ID
	} else if olResult != nil && olResult.Author != nil {
		authorName = olResult.Author.Name
		authorOLID = strings.TrimPrefix(olResult.Author.OLID, "/authors/")

		// Fetch author detail for bio/image
		if olResult.Author.OLID != "" {
			olAuthor, olErr := r.ol.GetAuthor(apiCtx, olResult.Author.OLID)
			if olErr == nil && olAuthor != nil {
				authorBio = olAuthor.Bio
				authorBorn = olAuthor.BornDate
				authorDied = olAuthor.DeathDate
				authorImage = olAuthor.ImageURL
			}
		}
	}

	author := &model.Author{
		HardcoverID: hcAuthorID,
		OLID:        authorOLID,
		Name:        authorName,
		Bio:         authorBio,
		BornDate:    authorBorn,
		DeathDate:   authorDied,
		ImageURL:    authorImage,
	}

	isbn10, isbn13, asin := "", "", ""
	pages := 0
	audioSeconds := 0
	publisher := ""
	language := ""
	tags := ""
	literaryType := ""
	hcBookID := 0
	olWorkID := ""
	description := item.Overview
	imageURL := item.ImageURL
	releaseDate := item.ReleaseDate
	releaseYear := item.Year
	hcRating := 0.0
	hcRatingsCount := 0
	title := item.Title
	subtitle := ""

	if hcResult != nil {
		isbn10 = hcResult.ISBN10
		isbn13 = hcResult.ISBN13
		asin = hcResult.ASIN
		pages = hcResult.Pages
		audioSeconds = hcResult.AudioSeconds
		publisher = hcResult.Publisher
		language = hcResult.Language
		tags = strings.Join(hcResult.Tags, ", ")
		literaryType = hcResult.LiteraryType
		hcBookID = hcResult.ID
		olWorkID = hcResult.OLID
		if hcResult.Title != "" {
			title = hcResult.Title
		}
		subtitle = hcResult.Subtitle
		if hcResult.Description != "" {
			description = hcResult.Description
		}
		if hcResult.ImageURL != "" {
			imageURL = hcResult.ImageURL
		}
		if hcResult.ReleaseDate != "" {
			releaseDate = hcResult.ReleaseDate
		}
		if hcResult.ReleaseYear > 0 {
			releaseYear = hcResult.ReleaseYear
		}
		hcRating = hcResult.Rating
		hcRatingsCount = hcResult.RatingsCount
	} else if olResult != nil {
		olWorkID = olResult.OLID
		isbn10 = olResult.ISBN10
		isbn13 = olResult.ISBN13
		if olResult.Title != "" {
			title = olResult.Title
		}
		subtitle = olResult.Subtitle
		if olResult.Description != "" {
			description = olResult.Description
		}
		if olResult.ImageURL != "" {
			imageURL = olResult.ImageURL
		}
		if olResult.ReleaseYear > 0 {
			releaseYear = olResult.ReleaseYear
		}
		tags = strings.Join(olResult.Subjects, ", ")
	}

	err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
		authorID, err := r.db.UpsertAuthorTx(ctx, tx, author)
		if err != nil {
			return fmt.Errorf("saving author: %w", err)
		}

		book := &model.Book{
			AuthorID:     authorID,
			Title:        title,
			Subtitle:     subtitle,
			HardcoverID:  hcBookID,
			OLID:         olWorkID,
			ISBN10:       isbn10,
			ISBN13:       isbn13,
			ASIN:         asin,
			Pages:        pages,
			AudioSeconds: audioSeconds,
			Description:  description,
			ReleaseDate:  releaseDate,
			ReleaseYear:  releaseYear,
			Rating:       hcRating,
			RatingsCount: hcRatingsCount,
			ImageURL:     imageURL,
			Language:     language,
			Publisher:    publisher,
			Tags:         tags,
			LiteraryType: literaryType,
		}
		bookID, err := r.db.UpsertBookTx(ctx, tx, book)
		if err != nil {
			return fmt.Errorf("saving book: %w", err)
		}

		existing, err := r.db.GetLatestBookReleaseEvent(ctx, bookID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		if existing != nil {
			switch existing.Status {
			case model.StatusPending:
				return nil // already in review queue
			case model.StatusApproved, model.StatusDownloaded:
				return nil // already processed
			case model.StatusRejected:
				return nil // don't re-queue
			}
		}

		formatPref := model.BookFormat(r.cfg.MediaTypes.Books.DefaultFormat)

		evt := &model.BookReleaseEvent{
			BookID:      bookID,
			Source:      item.Source,
			ReleaseDate: releaseDate,
			FormatPref:  formatPref,
			Status:      model.StatusPending,
			ISOYear:     progYear,
			ISOWeek:     progWeek,
		}
		if _, err := r.db.CreateBookReleaseEvent(ctx, evt); err != nil {
			return fmt.Errorf("saving book release event: %w", err)
		}

		return nil
	})

	return err
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

// waitDone returns a channel that closes when the WaitGroup counter reaches zero.
func waitDone(ctx context.Context, wg *sync.WaitGroup) chan struct{} {
	done := make(chan struct{})
	go func() {
		wg.Wait()
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
