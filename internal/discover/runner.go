package discover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog"
	"golang.org/x/text/unicode/norm"

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
	imdb            *IMDbAPIClient
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

	return r
}

func (r *Runner) cacheLibraryData(ctx context.Context) error {
	if r.radarrRes == nil && r.sonarrRes == nil && r.lidarrRes == nil {
		return nil
	}
	r.log.Info().Msg("persisting library cache from background fetches")

	var sonarrSeries []library.SonarrSeries

	if r.sonarrRes != nil {
		r.log.Info().Msg("waiting for Sonarr library...")
		series, err := r.sonarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("background Sonarr fetch failed, retrying synchronously...")
			series, err = r.sonarr.GetAllSeries(ctx)
			if err != nil {
				r.log.Warn().Err(err).Msg("failed to fetch Sonarr library")
			}
		}
		if err == nil {
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
			r.log.Warn().Err(err).Msg("background Radarr fetch failed, retrying synchronously...")
			movies, err = r.radarr.GetAllMovies(ctx)
			if err != nil {
				r.log.Warn().Err(err).Msg("failed to fetch Radarr library")
			}
		}
		if err == nil {
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
			r.log.Warn().Err(err).Msg("background Lidarr fetch failed, retrying synchronously...")
			artists, aErr := r.lidarr.GetAllArtists(ctx)
			albums, alErr := r.lidarr.GetAllAlbums(ctx)
			if aErr != nil {
				r.log.Warn().Err(aErr).Msg("failed to fetch Lidarr artists")
			} else if alErr != nil {
				r.log.Warn().Err(alErr).Msg("failed to fetch Lidarr albums")
			} else {
				result = struct {
					artists []library.LidarrArtist
					albums  []library.LidarrAlbum
				}{artists, albums}
				err = nil
			}
		}
		if err == nil {
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
			matchTitle := normalizeAnimeTitle(t.Title)
			for _, s := range sonarrSeries {
				seriesTitle := normalizeAnimeTitle(s.Title)
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

	providers := []ReleaseProvider{}

	// Video/movie/TV providers (DVD release dates, TMDB discover, FlixPatrol)
	if (wantMovie && r.cfg.MediaTypes.Movies.Enabled) ||
		(wantTV && r.cfg.MediaTypes.TV.Enabled) {

		// Collect which scraper names are needed and what media types requested them
		typeReq := make(map[string]map[model.MediaType]bool)

		if wantMovie && r.cfg.MediaTypes.Movies.Enabled {
			for _, s := range r.cfg.MediaTypes.Movies.Scrapers {
				if typeReq[s] == nil {
					typeReq[s] = make(map[model.MediaType]bool)
				}
				typeReq[s][model.MediaTypeMovie] = true
			}
		}
		if wantTV && r.cfg.MediaTypes.TV.Enabled {
			for _, s := range r.cfg.MediaTypes.TV.Scrapers {
				if typeReq[s] == nil {
					typeReq[s] = make(map[model.MediaType]bool)
				}
				typeReq[s][model.MediaTypeTV] = true
			}
		}

		for scraperName, types := range typeReq {
			switch scraperName {
			case "dvdsreleasedates":
				lo, hi := r.lookbackRange("physical", r.cfg.MediaTypes.PhysicalLookbackWeeks)
				for wk := lo; wk <= hi; wk++ {
					dvd := NewDVDReleaseDates()
					if len(types) == 1 {
						for mt := range types {
							dvd.SetMediaTypeFilter(mt)
						}
					}
					if r.hasTargetWeek {
						py, pw := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
						dvd.SetWeekRange(py, pw)
					}
					providers = append(providers, dvd)
				}

			case "tmdb-discover", "flixpatrol":
				for mt := range types {
					var cfgVal int
					var key string
					switch mt {
					case model.MediaTypeMovie:
						cfgVal = r.cfg.MediaTypes.Movies.StreamingLookbackWeeks
						key = "movie"
					case model.MediaTypeTV:
						cfgVal = r.cfg.MediaTypes.TV.StreamingLookbackWeeks
						key = "tv"
					}
					if cfgVal <= 0 {
						cfgVal = 8
					}
					lo, hi := r.lookbackRange(key, cfgVal)
					for wk := lo; wk <= hi; wk++ {
						sy, sw := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
						switch scraperName {
						case "tmdb-discover":
							tmdb := NewTMDBDiscoverProvider(r.tmdb)
							tmdb.SetMediaTypeFilter(mt)
							if r.hasTargetWeek {
								tmdb.SetWeekRange(sy, sw)
							}
							providers = append(providers, tmdb)
						case "flixpatrol":
							fp := NewFlixPatrolProvider(r.debugURL)
							fp.SetMediaTypeFilter(mt)
							if r.hasTargetWeek {
								fp.SetWeekRange(sy, sw)
							}
							providers = append(providers, fp)
						}
					}
				}

			default:
				r.log.Warn().Str("scraper", scraperName).Msg("unknown video scraper configured")
			}
		}
	}

	// Anime provider (gated on config and type filter)
	if wantAnime && r.cfg.MediaTypes.Anime.Enabled {
		lo, hi := r.lookbackRange("anime", r.cfg.MediaTypes.Anime.LookbackWeeks)
		for wk := lo; wk <= hi; wk++ {
			jikan := NewJikanAnimeProvider(r.cfg.MediaTypes.Anime)
			if r.hasTargetWeek {
				animeYear, animeWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
				jikan.SetWeekRange(animeYear, animeWeek)
			}
			providers = append(providers, jikan)
		}
	}

	// Book providers (gated on config and type filter)
	if wantBooks && r.cfg.MediaTypes.Books.Enabled {
		bookYear, bookWeek := r.targetYear, r.targetWeek
		if !r.hasTargetWeek {
			bookYear, bookWeek = time.Now().ISOWeek()
		}

		hasBookshop := false
		for _, n := range r.cfg.MediaTypes.Books.Scrapers {
			if n == "bookshop" {
				hasBookshop = true
				break
			}
		}

		bookLo, bookHi := r.lookbackRange("book", r.cfg.MediaTypes.Books.LookbackWeeks)
		for wk := bookLo; wk <= bookHi; wk++ {
			scrapeYear, scrapeWeek := addISOWeekOffset(bookYear, bookWeek, wk)
			for _, name := range r.cfg.MediaTypes.Books.Scrapers {
				switch name {
				case "bookshop":
					continue // handled separately below
				case "goodreads":
					if r.browserCtx == nil {
						r.log.Warn().Msg("goodreads: browser unavailable, skipping")
						continue
					}
					gr := NewGoodreadsProvider(r.debugURL, r.browserCtx)
					gr.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, gr)
				case "goodreads_blog":
					grb := NewGoodreadsBlogProvider()
					grb.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, grb)
				case "bookmarks":
					bm := NewBookMarksProvider()
					bm.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, bm)
				default:
					r.log.Warn().Str("scraper", name).Msg("unknown book scraper configured")
				}
			}
		}

		if hasBookshop {
			if r.hasLookbackOverride("book") {
				r.log.Info().Msg("bookshop: does not support lookback, scraping current real week as usual")
			}
			if r.browserCtx == nil {
				r.log.Warn().Msg("bookshop: browser unavailable, skipping")
			} else {
				bs := NewBookshopProvider(r.debugURL, r.browserCtx)
				realYear, realWeek := time.Now().ISOWeek()
				bs.SetWeekRange(realYear, realWeek)
				providers = append(providers, bs)
			}
		}
	}

	// Music providers (gated on config and type filter)
	if wantMusic && r.cfg.MediaTypes.Music.Enabled {
		cfgVal := r.cfg.MediaTypes.Music.LookbackWeeks
		if cfgVal <= 0 {
			cfgVal = 1
		}
		lo, hi := r.lookbackRange("music", cfgVal)
		for wk := lo; wk <= hi; wk++ {
			musicYear, musicWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
			for _, name := range r.cfg.MediaTypes.Music.Scrapers {
				switch name {
				case "albumoftheyear":
					aoty := NewAOTYProvider(r.cfg.MediaTypes.Music.Filter)
					if r.hasTargetWeek {
						aoty.SetWeekRange(musicYear, musicWeek)
					}
					providers = append(providers, aoty)

				case "allmusic":
					if r.browserCtx == nil {
						r.log.Warn().Msg("allmusic: browser unavailable, skipping")
						continue
					}
					allmusic := NewAllMusicProvider(r.debugURL, r.browserCtx)
					if r.hasTargetWeek {
						allmusic.SetWeekRange(musicYear, musicWeek)
					}
					providers = append(providers, allmusic)

				default:
					r.log.Warn().Str("scraper", name).Msg("unknown music scraper configured")
				}
			}
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

	// Filter out FlixPatrol items tagged as "Anime" genre when Jikan already
	// tracks them. Items unknown to Jikan stay in the video pipeline as a
	// second-chance fallback (e.g. shows below Jikan member thresholds).
	if r.cfg.MediaTypes.Anime.FilterFlixPatrolAnime {
		var filtered []ScrapedItem
		for _, item := range uniqueVideos {
			if item.Source == "flixpatrol" && item.Genres == "Anime" {
				found, err := jikanAnimeExists(ctx, item.Title)
				if err != nil {
					r.log.Warn().Err(err).Str("title", item.Title).
						Msg("jikan search failed, keeping flixpatrol item")
				} else if found {
					r.log.Debug().Str("title", item.Title).
						Msg("flixpatrol: skipping anime-genre item already tracked by jikan")
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
	skipped := processed - totalEvents
	r.log.Info().Msgf("Created %d events from %d items (%d filtered/skipped)",
		totalEvents, processed, skipped)

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
	if r.notify != nil && totalEvents > 0 {
		msg := fmt.Sprintf("**%d new release%s** ready for review:\n", totalEvents, map[bool]string{true: "s", false: ""}[totalEvents != 1])
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
				msg += fmt.Sprintf("\n+ %d more", totalEvents-count)
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
	var err error

	preferType := string(item.MediaType)

	if item.TmdbID > 0 {
		// TMDB ID already known (e.g. from tmdb-discover scraper).
		// Fetch details directly, skipping the search/scoring phase.
		enrich, err = r.tmdb.enrichByID(apiCtx, item.TmdbID, preferType, 0, item.Title, item.Year)
		if err == nil {
			tmdbID = enrich.TMDBID
			rating = enrich.Rating
			imdbID = enrich.IMDbID
			r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (by ID)")
		}
	}

	if tmdbID == 0 {
		enrich, err = r.tmdb.enrichWithPrefs(apiCtx, searchTitle, item.Year, preferType)
		if err == nil {
			tmdbID = enrich.TMDBID
			rating = enrich.Rating
			imdbID = enrich.IMDbID
			r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched")
		}
	}

	// Retry with uncleaned original title if cleaned search gave a weak match
	// (non-exact) or failed entirely.
	if tmdbID == 0 && (err != nil || (enrich != nil && !strings.EqualFold(enrich.Title, searchTitle))) {
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
		if tmdbID == 0 && (enrich == nil || !strings.EqualFold(enrich.Title, searchTitle)) {
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
	collectionID := 0
	collectionName := ""
	if enrich != nil {
		tvdbID = enrich.TVDBID
		tmdbTitle = enrich.Title
		overview = enrich.Overview
		genres = enrich.Genres
		runtime = enrich.Runtime
		posterPath = enrich.PosterPath
		originalLanguage = enrich.OriginalLanguage
		originCountry = enrich.OriginCountry
		collectionID = enrich.CollectionID
		collectionName = enrich.CollectionName
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
		CollectionID:     collectionID,
		CollectionName:   collectionName,
	}

	// Content filter
	cf := &r.cfg.MediaTypes.Movies.Filter
	if mediaType == model.MediaTypeTV {
		cf = &r.cfg.MediaTypes.TV.Filter
	}
	if result := FilterTitle(cf, title); !result.Passed {
		r.log.Info().Str("title", item.Title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
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
			Source:         item.Source,
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

	// Store collection membership in library_cache if movie belongs to a collection
	if enrich != nil && enrich.CollectionID > 0 && enrich.MediaType == "movie" {
		collIDStr := strconv.Itoa(enrich.CollectionID)
		existingCache, _ := r.db.GetLibraryCache(ctx, "tmdb-collection", collIDStr)
		if existingCache == nil {
			coll, err := r.tmdb.GetCollection(ctx, enrich.CollectionID)
			if err == nil && coll != nil && len(coll.Parts) > 0 {
				type collPart struct {
					TmdbID int    `json:"tmdb_id"`
					Title  string `json:"title"`
					Year   int    `json:"year"`
				}
				parts := make([]collPart, 0, len(coll.Parts))
				for _, p := range coll.Parts {
					year := 0
					if len(p.ReleaseDate) >= 4 {
						if n, _ := fmt.Sscanf(p.ReleaseDate[:4], "%d", &year); n != 1 {
							year = 0
						}
					}
					parts = append(parts, collPart{TmdbID: p.ID, Title: p.Title, Year: year})
				}
				collJSON, _ := json.Marshal(map[string]any{
					"name":   coll.Name,
					"movies": parts,
				})
				_ = r.db.UpsertLibraryCache(ctx, "tmdb-collection", collIDStr, int64(coll.ID), coll.Name, string(collJSON))
			}
		}
	}

	return nil
}

// addISOWeekOffset adds an offset (positive = past) to an ISO week/year pair,
// properly wrapping across year boundaries.
func addISOWeekOffset(year, week, offset int) (int, int) {
	t := tuesdayOfISOWeek(year, week)
	t = t.AddDate(0, 0, -7*offset)
	return t.ISOWeek()
}

// bookTargetWeekFrom applies the book timeshift to an arbitrary year/week.
func (r *Runner) bookTargetWeekFrom(year, week int) (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Books.LookbackWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}
	return addISOWeekOffset(year, week, timeshiftWeeks)
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

	// Content filter
	if result := FilterTitle(&r.cfg.MediaTypes.Anime.Filter, title); !result.Passed {
		r.log.Info().Str("title", item.Title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
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
		if HasOverrideGenre(&r.cfg.MediaTypes.Music.Filter.ContentFilter, item.Genres) {
			r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).
				Msg("override genre matched, skipping score filter")
		} else if !passesMusicFilter(r.cfg.MediaTypes.Music.Filter, item) {
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

	// Split on " / " (AllMusic multi-artist format) and try each artist
	artists := strings.Split(item.ArtistName, " / ")
	for i, a := range artists {
		artists[i] = strings.TrimSpace(a)
	}

	var rgResult *MBReleaseGroupResult
	var err error
	for _, a := range artists {
		rgResult, err = r.mb.SearchReleaseGroup(apiCtx, item.Title, a)
		if err == nil && rgResult != nil {
			break
		}
	}

	if err != nil {
		r.log.Warn().Err(err).Str("album", item.Title).Msg("MusicBrainz search failed, storing without MB data")
	} else if rgResult == nil {
		blindResult, blindErr := r.mb.SearchReleaseGroupByAlbum(apiCtx, item.Title)
		if blindErr == nil && blindResult != nil {
			match := false
			for _, a := range artists {
				if artistNamesMatch(a, blindResult.ArtistName) {
					match = true
					break
				}
			}
			if !match {
				r.log.Warn().Str("scraped_artist", item.ArtistName).
					Str("mb_artist", blindResult.ArtistName).
					Str("album", item.Title).
					Msg("artist mismatch with blind MB search, skipping unreliable pair")
				return nil
			}
		}
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
	genresStr := item.Genres
	if genresStr == "" {
		genresStr = strings.Join(mbGenres, ", ")
	}

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
		Overview:        item.Overview,
	}

	// Content filter
	if result := FilterMusic(&r.cfg.MediaTypes.Music.Filter.ContentFilter, artist, album); !result.Passed {
		r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Str("reason", result.Reason).
			Msg("content filter: skipping")
		return nil
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
	r.log.Info().Str("title", item.Title).Str("author", item.ArtistName).Msg("processing book item")

	// Extract EAN/ISBN from Notes (from bookshop/bookmarks scrapers)
	var notesISBN string
	if item.Notes != "" {
		for _, part := range strings.Split(item.Notes, "|") {
			if strings.HasPrefix(part, "isbn=") {
				notesISBN = strings.TrimPrefix(part, "isbn=")
			} else if strings.HasPrefix(part, "ean=") && notesISBN == "" {
				notesISBN = strings.TrimPrefix(part, "ean=")
			}
		}
	}

	// Step 1: Hardcover enrichment (best-effort, only if API key configured)
	var hcResult *HCBookResult
	if r.hc != nil {
		var hcErr error
		hcResult, hcErr = r.hc.SearchBook(ctx, item.Title, item.ArtistName)
		if hcErr != nil {
			// If title+author search failed, try with ISBN in query
			if notesISBN != "" {
				hcResult, hcErr = r.hc.SearchBook(ctx, item.Title+" "+notesISBN, item.ArtistName)
			}
		}
		if hcErr != nil {
			r.log.Warn().Err(hcErr).Str("book", item.Title).Msg("hardcover search failed, falling back to Open Library")
		}
	}

	// Step 2: Open Library supplement (when HC is missing or lacks release date)
	var olResult *OLBookResult
	if hcResult == nil || hcResult.ISBN13 == "" || (hcResult.ReleaseDate == "" && hcResult.ReleaseYear == 0) {
		var olErr error
		olResult, olErr = r.ol.SearchBook(ctx, item.Title, item.ArtistName)
		if olErr != nil {
			// If title+author search failed, try with ISBN in query
			if notesISBN != "" {
				olResult, olErr = r.ol.SearchBook(ctx, item.Title, notesISBN)
			}
		}
		if olErr != nil {
			r.log.Warn().Err(olErr).Str("book", item.Title).Msg("openlibrary search failed, storing without enrichment")
		}
	}

	// Step 3: Build author from enrichment data
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
		authorOLID = olResult.Author.OLID

		// Fetch author detail for bio/image (its own timeout so the OL search doesn't eat the budget)
		if olResult.Author.OLID != "" {
			authorCtx, authorCancel := context.WithTimeout(ctx, 15*time.Second)
			defer authorCancel()
			olAuthor, olErr := r.ol.GetAuthor(authorCtx, olResult.Author.OLID)
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
	seriesID, seriesName := "", ""
	pages := 0
	audioSeconds := 0
	publisher := ""
	language := ""
	tags := ""
	literaryType := ""
	hcBookID := 0
	hcSlug := ""
	olWorkID := ""
	description := item.Overview
	imageURL := item.ImageURL
	releaseDate := item.ReleaseDate
	releaseYear := item.Year
	shelvingsCount := item.ShelvingsCount
	title := item.Title
	subtitle := ""

	if hcResult != nil {
		isbn10 = hcResult.ISBN10
		isbn13 = hcResult.ISBN13
		asin = hcResult.ASIN
		pages = hcResult.Pages
		seriesID = hcResult.SeriesID
		seriesName = hcResult.SeriesName
		audioSeconds = hcResult.AudioSeconds
		publisher = hcResult.Publisher
		language = hcResult.Language
		tags = strings.Join(hcResult.Tags, ", ")
		literaryType = hcResult.LiteraryType
		hcBookID = hcResult.ID
		hcSlug = hcResult.Slug
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
		} else if olResult != nil && olResult.ReleaseDate != "" {
			releaseDate = olResult.ReleaseDate
		}
		if hcResult.ReleaseYear > 0 {
			releaseYear = hcResult.ReleaseYear
		} else if olResult != nil && olResult.ReleaseYear > 0 {
			releaseYear = olResult.ReleaseYear
		}
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
		if imageURL == "" && olResult.ImageURL != "" {
			imageURL = olResult.ImageURL
		}
		if olResult.ReleaseDate != "" {
			releaseDate = olResult.ReleaseDate
		}
		if olResult.ReleaseYear > 0 {
			releaseYear = olResult.ReleaseYear
		}
		tags = strings.Join(olResult.Subjects, ", ")
	}

	// Fallback: use ISBN from Notes if enrichment didn't provide one
	if isbn13 == "" && notesISBN != "" {
		if len(notesISBN) == 13 {
			isbn13 = notesISBN
		} else if len(notesISBN) == 10 {
			isbn10 = notesISBN
		}
	}

	// Bookshop: fall back to page header date if enrichment didn't provide one
	if releaseDate == "" && (item.Source == "bookshop" || strings.Contains(item.Source, "bookshop")) {
		if item.ReleaseDate != "" {
			for _, f := range []string{"January 2, 2006", "Jan 2, 2006"} {
				if t, err := time.Parse(f, item.ReleaseDate); err == nil {
					releaseDate = t.Format("2006-01-02")
					break
				}
			}
		}
	}

	// Step 4: Filter by score/rating (with override for matching genres)
	rating := item.ImdbRating         // from Goodreads
	ratingsCount := item.RatingsCount // from Goodreads

	if HasOverrideGenre(&r.cfg.MediaTypes.Books.Filter.ContentFilter, tags) {
		r.log.Info().Str("book", item.Title).Msg("override genre matched, skipping score filter")
	} else {
		minRating := r.cfg.MediaTypes.Books.Filter.MinRating
		minRatings := r.cfg.MediaTypes.Books.Filter.MinRatings

		// Rating/ratings filters only apply when the source actually provides this data.
		// Sources like bookshop.org and bookmarks.reviews don't supply scores.
		if ratingsCount > 0 {
			if minRatings > 0 && ratingsCount < minRatings {
				r.log.Info().Str("book", item.Title).Int("ratings", ratingsCount).Int("min", minRatings).Msg("below min_ratings filter, skipping")
				return nil
			}
		}
		if rating > 0 {
			if minRating > 0 && rating < minRating {
				r.log.Info().Str("book", item.Title).Float64("rating", rating).Float64("min", minRating).Msg("below min_rating filter, skipping")
				return nil
			}
		}
	}

	// Upgrade Goodreads thumbnail to 500px for faster loading and consistent quality
	imageURL = upgradeGoodreadsImage(imageURL)

	// Strip WordPress thumbnail size suffixes from bookmarks images
	imageURL = upgradeBookmarksImage(imageURL)

	// Skip books without a verified release date — we can't confirm they
	// belong in the target week, and they'd appear in review with missing data.
	if releaseDate == "" {
		r.log.Info().Str("book", item.Title).Str("author", item.ArtistName).
			Msg("no release date available from enrichment, skipping")
		return nil
	}

	// Parse release date for week assignment and filtering
	var t time.Time
	var parseErr error
	for _, f := range []string{"2006-01-02", "January 2, 2006", "Jan 2, 2006"} {
		t, parseErr = time.Parse(f, releaseDate)
		if parseErr == nil {
			break
		}
	}

	// Storage week: bookshop items go to their actual release week;
	// non-bookshop items use progYear/progWeek set below.
	var storeYear, storeWeek int

	isBookshop := item.Source == "bookshop" || strings.Contains(item.Source, "bookshop")

	if isBookshop && parseErr == nil {
		// Bookshop items: store under the book's actual release week
		// (ignore timeshift — bookshop always returns current-week data)
		storeYear, storeWeek = t.ISOWeek()
	} else if !isBookshop && parseErr == nil {
		// Non-bookshop items: apply the timeshift window filter
		scrapeYear, scrapeWeek := r.bookTargetWeekFrom(progYear, progWeek)
		scrapeEnd := tuesdayOfISOWeek(scrapeYear, scrapeWeek)
		scrapeStart := scrapeEnd.AddDate(0, 0, -6)
		if t.Before(scrapeStart) || t.After(scrapeEnd) {
			r.log.Info().Str("book", item.Title).Str("date", releaseDate).
				Str("window", fmt.Sprintf("%s – %s", scrapeStart.Format("Jan 2"), scrapeEnd.Format("Jan 2"))).
				Msg("not in target week, skipping")
			return nil
		}
	}

	// Content filter (before transaction to avoid orphaned authors)
	filterBook := &model.Book{
		Language: language,
		Tags:     tags,
	}
	if result := FilterBook(&r.cfg.MediaTypes.Books.Filter.ContentFilter, filterBook); !result.Passed {
		r.log.Info().Str("book", title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
	}

	err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
		authorID, err := r.db.UpsertAuthorTx(ctx, tx, author)
		if err != nil {
			return fmt.Errorf("saving author: %w", err)
		}

		book := &model.Book{
			AuthorID:       authorID,
			Title:          title,
			Subtitle:       subtitle,
			HardcoverID:    hcBookID,
			HardcoverSlug:  hcSlug,
			OLID:           olWorkID,
			ISBN10:         isbn10,
			ISBN13:         isbn13,
			ASIN:           asin,
			Pages:          pages,
			AudioSeconds:   audioSeconds,
			Description:    description,
			ReleaseDate:    releaseDate,
			ReleaseYear:    releaseYear,
			Rating:         rating,
			RatingsCount:   ratingsCount,
			ShelvingsCount: shelvingsCount,
			ImageURL:       imageURL,
			Language:       language,
			Publisher:      publisher,
			Tags:           tags,
			LiteraryType:   literaryType,
			SeriesID:       seriesID,
			SeriesName:     seriesName,
		}
		bookID, err := r.db.UpsertBookTx(ctx, tx, book)
		if err != nil {
			return fmt.Errorf("saving book: %w", err)
		}

		existing, err := r.db.GetLatestBookReleaseEventTx(ctx, tx, bookID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		if existing != nil {
			switch existing.Status {
			case model.StatusPending:
				// Merge source and notes if a different source found this book
				if existing.Source != item.Source {
					existingSrcSet := make(map[string]bool)
					for _, s := range strings.Split(existing.Source, ",") {
						existingSrcSet[strings.TrimSpace(s)] = true
					}
					needsMerge := false
					for _, s := range strings.Split(item.Source, ",") {
						if !existingSrcSet[strings.TrimSpace(s)] {
							needsMerge = true
							break
						}
					}
					if needsMerge {
						a := &ScrapedItem{Source: existing.Source, Notes: existing.Notes}
						b := &ScrapedItem{Source: item.Source, Notes: item.Notes}
						mergeBookItems(a, b)
						if err := r.db.UpdateBookReleaseEventSourceAndNotesTx(ctx, tx, existing.ID, a.Source, a.Notes); err != nil {
							return fmt.Errorf("updating event source: %w", err)
						}
					}
				}
				return nil
			case model.StatusApproved, model.StatusDownloaded:
				return nil // already processed
			case model.StatusRejected:
				return nil // don't re-queue
			}
		}

		formatPref := model.BookFormat(r.cfg.MediaTypes.Books.DefaultFormat)

		// Non-bookshop items: store under the current target week.
		// Bookshop items: apply the negative timeshift so they're stored under
		// the review week (not the raw release week), matching other scrapers.
		evtYear, evtWeek := progYear, progWeek
		if isBookshop && storeYear > 0 {
			ts := r.cfg.MediaTypes.Books.LookbackWeeks
			if ts <= 0 {
				ts = 1
			}
			evtYear, evtWeek = addISOWeekOffset(storeYear, storeWeek, -ts)
		}
		evt := &model.BookReleaseEvent{
			BookID:      bookID,
			Source:      item.Source,
			ReleaseDate: releaseDate,
			FormatPref:  formatPref,
			Notes:       item.Notes,
			Status:      model.StatusPending,
			ISOYear:     evtYear,
			ISOWeek:     evtWeek,
		}
		if _, err := r.db.CreateBookReleaseEventTx(ctx, tx, evt); err != nil {
			return fmt.Errorf("saving book release event: %w", err)
		}

		return nil
	})

	return err
}

// mergeBookItems merges a duplicate book ScrapedItem (from a different source) into
// the primary item. Sources are combined, notes are namespaced, and the best data
// from each source is preserved.
func mergeBookItems(a, b *ScrapedItem) {
	// Capture original sources before merging
	aSrc := a.Source
	bSrc := b.Source

	// Merge sources (deduped, comma-separated)
	seen := map[string]bool{}
	for _, s := range strings.Split(aSrc, ",") {
		seen[strings.TrimSpace(s)] = true
	}
	for _, s := range strings.Split(bSrc, ",") {
		s = strings.TrimSpace(s)
		if !seen[s] {
			if a.Source != "" {
				a.Source += ","
			}
			a.Source += s
			seen[s] = true
		}
	}

	// Merge notes with source prefix namespacing
	var notesParts []string
	if a.Notes != "" {
		notesParts = append(notesParts, aSrc+":"+a.Notes)
	}
	if b.Notes != "" {
		notesParts = append(notesParts, bSrc+":"+b.Notes)
	}
	if len(notesParts) > 0 {
		a.Notes = strings.Join(notesParts, "||")
	}

	// Prefer higher rating data
	if b.ImdbRating > a.ImdbRating {
		a.ImdbRating = b.ImdbRating
	}
	if b.RatingsCount > a.RatingsCount {
		a.RatingsCount = b.RatingsCount
	}
	if b.ShelvingsCount > a.ShelvingsCount {
		a.ShelvingsCount = b.ShelvingsCount
	}

	// Prefer longer description
	if len(b.Overview) > len(a.Overview) {
		a.Overview = b.Overview
	}

	// Prefer non-empty release date
	if a.ReleaseDate == "" && b.ReleaseDate != "" {
		a.ReleaseDate = b.ReleaseDate
	}

	// Prefer non-empty image URL
	if a.ImageURL == "" && b.ImageURL != "" {
		a.ImageURL = b.ImageURL
	}
}

// deduplicateBookEvents finds book records with matching ISBN13 that each have
// separate pending release events (created by parallel scrapers or across runs)
// and merges them into a single book+event.
func (r *Runner) deduplicateBookEvents(ctx context.Context) error {
	groups, err := r.db.FindPendingBookEventsByISBN(ctx)
	if err != nil {
		return fmt.Errorf("finding duplicate book events: %w", err)
	}

	merged := 0
	for isbn, entries := range groups {
		// Group entries by book_id to get per-book event lists
		bookEvents := make(map[int64][]int64) // book_id -> event_ids
		bookIDs := make([]int64, 0, len(entries))
		for _, e := range entries {
			if _, ok := bookEvents[e.BookID]; !ok {
				bookIDs = append(bookIDs, e.BookID)
			}
			bookEvents[e.BookID] = append(bookEvents[e.BookID], e.EventID)
		}
		sort.Slice(bookIDs, func(i, j int) bool { return bookIDs[i] < bookIDs[j] })
		survivorBookID := bookIDs[0]

		for _, dupBookID := range bookIDs[1:] {
			dupEventIDs := bookEvents[dupBookID]
			// For each event in the duplicate book, merge source/notes
			// into the survivor book's first event.
			survivorEventIDs := bookEvents[survivorBookID]
			if len(survivorEventIDs) == 0 {
				continue
			}
			survivorEventID := survivorEventIDs[0]

			// Fetch the existing survivor source/notes to merge in
			survivorEvent, err := r.db.GetLatestBookReleaseEvent(ctx, survivorBookID)
			if err != nil {
				r.log.Warn().Err(err).Int64("survivor_event", survivorEventID).Msg("dedup: fetch survivor event failed")
				continue
			}
			if survivorEvent == nil {
				continue
			}

			for _, dupEventID := range dupEventIDs {
				dupEvent, err := r.db.GetLatestBookReleaseEvent(ctx, dupBookID)
				if err != nil || dupEvent == nil {
					continue
				}

				// Merge the duplicate event's source/notes into the survivor
				a := &ScrapedItem{Source: survivorEvent.Source, Notes: survivorEvent.Notes}
				b := &ScrapedItem{Source: dupEvent.Source, Notes: dupEvent.Notes}
				mergeBookItems(a, b)
				survivorEvent.Source = a.Source
				survivorEvent.Notes = a.Notes

				// Delete the duplicate event
				if err := r.db.DeleteBookReleaseEvent(ctx, dupEventID); err != nil {
					r.log.Warn().Err(err).Int64("event", dupEventID).Msg("dedup: delete duplicate event failed")
				}
				merged++
			}

			// Update survivor event with merged source/notes
			if err := r.db.UpdateBookReleaseEventSourceAndNotes(ctx, survivorEventID, survivorEvent.Source, survivorEvent.Notes); err != nil {
				r.log.Warn().Err(err).Int64("event", survivorEventID).Msg("dedup: update survivor event failed")
			}

			// Delete the duplicate book (no more events pointing to it)
			if err := r.db.DeleteBook(ctx, dupBookID); err != nil {
				r.log.Warn().Err(err).Int64("book", dupBookID).Msg("dedup: delete duplicate book failed")
			}
		}

		if len(bookIDs) > 1 {
			r.log.Info().Str("isbn", isbn).Int("merged", len(bookIDs)-1).Msg("dedup: merged duplicate books by isbn13")
		}
	}

	if merged > 0 {
		r.log.Info().Int("events_merged", merged).Msg("book deduplication complete")
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
	goodreadsSizeRe   = regexp.MustCompile(`\._SX\d+_\.`)
	bookmarksWpSizeRe = regexp.MustCompile(`(-\d+x\d+)(\.[a-zA-Z]+)$`)
	bookYearParenRe   = regexp.MustCompile(`\s*\(\d{4}\)`)
	bookSubtitleRe    = regexp.MustCompile(`\s*[;:].*`)
)

// normalizeAnimeTitle normalizes an anime title for Sonarr comparison:
// lowercases, replaces punctuation separators with spaces, collapses
// whitespace, and strips trailing/copyrighted season suffixes.
func normalizeAnimeTitle(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.NewReplacer("-", " ", ":", " ", ",", " ", ".", " ", "_", " ").Replace(v)
	v = metaParen.ReplaceAllString(v, "")
	v = trailingFmt.ReplaceAllString(v, "")
	return strings.TrimSpace(strings.Join(strings.Fields(v), " "))
}

func cleanTitleForSearch(title string) string {
	cleaned := html.UnescapeString(title)
	cleaned = metaParen.ReplaceAllString(cleaned, "")
	// Strip colon-suffixes that look like season descriptors.
	// Only strip when the prefix is ≥4 chars to avoid reducing
	// "Re:Zero Season 4" → "Re".
	if parts := strings.SplitN(cleaned, ":", 2); len(parts) == 2 && len(parts[0]) >= 4 {
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

// upgradeGoodreadsImage constrains Goodreads/Amazon image URLs to 500px wide.
func upgradeGoodreadsImage(url string) string {
	if !strings.Contains(url, "m.media-amazon.com") && !strings.Contains(url, "gr-assets") {
		return url
	}
	if goodreadsSizeRe.MatchString(url) {
		return goodreadsSizeRe.ReplaceAllString(url, "._SX500_.")
	}
	if strings.HasSuffix(url, ".jpg") {
		return strings.Replace(url, ".jpg", "._SX500_.jpg", 1)
	}
	return url
}

// upgradeBookmarksImage strips WordPress thumbnail size suffixes from bookmarks.reviews images.
// e.g. "image-200x300.gif" → "image.gif" to get the full-size original.
func upgradeBookmarksImage(url string) string {
	if !strings.Contains(url, "s26162.pcdn.co") {
		return url
	}
	return bookmarksWpSizeRe.ReplaceAllString(url, "$2")
}

// artistNamesMatch does a fuzzy comparison of two artist names,
// handling case differences, diacritics, and curly/smart quotes.
func artistNamesMatch(a, b string) bool {
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u02bc", "'").Replace(s)
		t := norm.NFKD.String(s)
		var out strings.Builder
		for _, r := range t {
			if unicode.Is(unicode.Mn, r) {
				continue
			}
			out.WriteRune(r)
		}
		return out.String()
	}
	return normalize(a) == normalize(b)
}

// normalizeBookKey normalizes a book author or title for fuzzy dedup matching.
// It strips parenthesized years (e.g. "(2026)"), strips subtitle after colon/semicolon,
// lowercases, trims, and normalizes unicode diacritics.
func normalizeBookKey(s string) string {
	s = strings.ToLower(s)
	s = bookYearParenRe.ReplaceAllString(s, "")
	s = bookSubtitleRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	t := norm.NFKD.String(s)
	var out strings.Builder
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
