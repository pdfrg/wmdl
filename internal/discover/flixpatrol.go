package discover

import (
	"context"
	"fmt"
	"html"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/model"
)

type FlixPatrolProvider struct {
	debugURL        string
	allocCtx        context.Context
	targetYear      int
	targetWeek      int
	hasTargetWeek   bool
	mediaTypeFilter model.MediaType
	fetchPageFn     func(f *FlixPatrolProvider, page int, windowStart, windowEnd time.Time) ([]flixItem, error)
	paceFn          func(page int)
	// paceFn pauses briefly before each page load after the first so we
	// don't strobe the site with back-to-back navigations.
}

var (
	_ ReleaseProvider = (*FlixPatrolProvider)(nil)
	_ WeekSettable    = (*FlixPatrolProvider)(nil)
)

func NewFlixPatrolProvider(debugURL string, allocCtx context.Context) *FlixPatrolProvider {
	return &FlixPatrolProvider{
		debugURL:    debugURL,
		allocCtx:    allocCtx,
		fetchPageFn: (*FlixPatrolProvider).fetchPage,
	}
}

func (f *FlixPatrolProvider) Name() string {
	return "flixpatrol"
}

func (f *FlixPatrolProvider) SetMediaTypeFilter(mt model.MediaType) {
	f.mediaTypeFilter = mt
}

func (f *FlixPatrolProvider) SetWeekRange(year, week int) {
	f.targetYear = year
	f.targetWeek = week
	f.hasTargetWeek = true
}

type flixItem struct {
	Date          string
	Title         string
	Year          int
	MediaType     model.MediaType
	IMDbRating    float64
	YoutubeView   int64
	hasAnimeGenre bool
}

var (
	flixDatePat     = regexp.MustCompile(`text-sm sm:text-base">([^<]+)`)
	flixTitlePat    = regexp.MustCompile(`group-hover:underline">\s*([^<]+?)\s*</div>`)
	flixIMDbPat     = regexp.MustCompile(`(\d+\.\d+)/10`)
	flixYTViewsPat  = regexp.MustCompile(`title="([\d,]+) views"`)
	flixTVPat       = regexp.MustCompile(`TV Show`)
	flixMoviePat    = regexp.MustCompile(`Movie`)
	flixTitleYear   = regexp.MustCompile(`\((\d{4})\)`)
	flixPremierePat = regexp.MustCompile(`title="Premiere">\s*<div>\s*<span[^>]*>\d{2}/\d{2}/</span>(\d{4})`)
	flixAnimePat    = regexp.MustCompile(`<span>Anime</span>`)
)

func (f *FlixPatrolProvider) Scrape() ([]ScrapedItem, error) {
	var streamTue time.Time
	if f.hasTargetWeek {
		streamTue = tuesdayOfISOWeek(f.targetYear, f.targetWeek)
	} else {
		now := time.Now()
		streamTue = mostRecentTuesday(now)
	}
	streamTue = truncateToDay(streamTue)
	streamStart := truncateToDay(streamTue.AddDate(0, 0, -6))

	startStr := streamStart.Format("Jan 2")
	endStr := streamTue.Format("Jan 2")
	log.Info().Msgf("FlixPatrol target: %s – %s", startStr, endStr)

	// Pacing defaults on only when a real browser is attached (allocCtx
	// non-nil) so unit tests with stubbed fetchPageFn stay fast. Each page
	// still gets a fresh tab: sharing one tab across navigations made every
	// Navigate wait ~2min for the full page load event (Cloudflare
	// tarpitting the reused session), while fresh tabs load fast.
	if f.allocCtx != nil && f.paceFn == nil {
		f.paceFn = paceFlixPage
	}

	var allItems []flixItem
	var rawRows int
pageLoop:
	for page := 1; page <= 30; page++ {
		// Human-like pause between page loads (skipped before page 1).
		if page > 1 && f.paceFn != nil {
			f.paceFn(page)
		}
		items, err := f.fetchPageFn(f, page, streamStart, streamTue)
		if err != nil {
			// A failure on the first page means the site (or our browser
			// session) is unavailable, so surface it as a scrape error rather
			// than silently reporting zero items. Later-page failures after a
			// successful first page are logged and skipped — we still have
			// partial data.
			if page == 1 {
				return nil, fmt.Errorf("flixpatrol page %d: %w", page, err)
			}
			log.Warn().Err(err).Msgf("FlixPatrol page %d failed", page)
			continue
		}
		// An empty parse on page 1 almost always means the page hadn't
		// finished rendering (or a challenge page slipped through), not
		// an empty calendar — retry once before giving up, and fail
		// loudly rather than reporting a silent zero.
		if page == 1 && len(items) == 0 {
			log.Warn().Msg("FlixPatrol page 1 parsed 0 rows, retrying once")
			retryItems, retryErr := f.fetchPageFn(f, page, streamStart, streamTue)
			if retryErr != nil {
				return nil, fmt.Errorf("flixpatrol page %d retry: %w", page, retryErr)
			}
			items = retryItems
			if len(items) == 0 {
				return nil, fmt.Errorf("flixpatrol page 1: empty parse after retry (0 rows, possible challenge/incomplete load)")
			}
		}
		if len(items) == 0 {
			break
		}
		rawRows += len(items)
		allItems = append(allItems, items...)

		// Pages go newest-to-oldest. Once we hit any pre-window date,
		// remaining pages will all be older — stop.
		for _, it := range items {
			d := parseFlixDate(it.Date)
			if !d.IsZero() && d.Before(streamStart) {
				log.Debug().Msgf("FlixPatrol: found pre-window date %s on page %d, stopping", it.Date, page)
				break pageLoop
			}
		}
	}

	// Filter to the target date range
	var filtered []flixItem
	for _, item := range allItems {
		d := parseFlixDate(item.Date)
		if !d.IsZero() && (d.Before(streamStart) || d.After(streamTue)) {
			continue
		}
		filtered = append(filtered, item)
	}

	log.Info().Msgf("FlixPatrol: %d in target range (%d raw rows)", len(filtered), rawRows)

	var results []ScrapedItem
	for _, item := range filtered {
		if item.IMDbRating == 0 && item.YoutubeView == 0 {
			continue
		}
		d := parseFlixDate(item.Date)
		dateStr := ""
		if !d.IsZero() {
			dateStr = d.Format("2006-01-02")
		}
		scraped := ScrapedItem{
			Title:        cleanFlixTitle(item.Title),
			Year:         item.Year,
			MediaType:    item.MediaType,
			ReleaseType:  model.ReleaseStreaming,
			ReleaseDate:  dateStr,
			ImdbRating:   item.IMDbRating,
			YoutubeViews: item.YoutubeView,
			Source:       "flixpatrol",
		}
		if item.hasAnimeGenre {
			scraped.Genres = "Anime"
		}
		if f.mediaTypeFilter != "" && scraped.MediaType != f.mediaTypeFilter {
			continue
		}
		results = append(results, scraped)
	}

	return results, nil
}

func (f *FlixPatrolProvider) fetchPage(page int, windowStart, windowEnd time.Time) ([]flixItem, error) {
	// Each page gets a fresh tab: sharing one tab across navigations made
	// every Navigate wait ~2min for the full page load event, while fresh
	// tabs load fast. Callers pace navigations via paceFn.
	if f.allocCtx == nil {
		return nil, fmt.Errorf("chromedp allocator not available")
	}
	ct, cancel := chromedp.NewContext(f.allocCtx)
	defer cancel()
	pageCtx, pageCancel := context.WithTimeout(ct, 120*time.Second)
	defer pageCancel()

	// All CDP work for this page is scoped to pageCtx so no single page
	// can outlive the per-page timeout. (An earlier version ran
	// waitForRealPage/OuterHTML on the parent tab ctx, which has no
	// deadline — a stuck challenge page hung the whole scrape.)
	// Later pages get a shorter challenge wait: partial data beats a dead
	// run. Page 1 keeps the full 120s since its failure is fatal.
	challengeTimeout := 120 * time.Second
	if page > 1 {
		challengeTimeout = 60 * time.Second
	}

	pageStart := time.Now()
	url := fmt.Sprintf("https://flixpatrol.com/calendar/new/titles/streaming/right-now/%d/", page)
	log.Debug().Str("provider", "flixpatrol").Msgf("navigating to page %d", page)
	if err := chromedp.Run(pageCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	); err != nil {
		return nil, fmt.Errorf("navigating to page %d: %w", page, err)
	}
	navElapsed := time.Since(pageStart)

	if err := waitForRealPage(pageCtx, challengeTimeout); err != nil {
		return nil, fmt.Errorf("page %d cloudflare challenge (%v elapsed): %w", page, time.Since(pageStart).Round(time.Second), err)
	}
	challengeElapsed := time.Since(pageStart)

	var html string
	if err := chromedp.Run(pageCtx, chromedp.OuterHTML("html", &html)); err != nil {
		return nil, fmt.Errorf("page %d getting HTML: %w", page, err)
	}

	items := parseFlixRows(html)
	log.Debug().Str("provider", "flixpatrol").Msgf("page %d done in %v (navigate %v, challenge-cleared %v): %d bytes HTML -> %d rows", page, time.Since(pageStart).Round(time.Second), navElapsed.Round(time.Second), challengeElapsed.Round(time.Second), len(html), len(items))
	return items, nil
}

// flixPaceBounds is the randomized pause before each FlixPatrol page load
// after the first: 2–5s. Enough to break the machine-gun cadence without
// adding much wall-clock time (worst case ~2min across 30 pages; typical
// runs stop after far fewer).
const (
	flixPaceMin = 2 * time.Second
	flixPaceJit = 3 * time.Second
)

func paceFlixPage(page int) {
	d := flixPaceMin + time.Duration(rand.Int63n(int64(flixPaceJit)))
	log.Debug().Str("provider", "flixpatrol").Msgf("pacing %v before page %d", d.Round(time.Second), page)
	time.Sleep(d)
}

func parseFlixRows(html string) []flixItem {
	var items []flixItem
	rows := strings.Split(html, `<tr class="table-group">`)
	for _, row := range rows[1:] {
		item := parseFlixRow(row)
		if item.Title != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseFlixRow(row string) flixItem {
	var item flixItem

	if m := flixDatePat.FindStringSubmatch(row); len(m) > 1 {
		item.Date = strings.TrimSpace(m[1])
	}
	if m := flixTitlePat.FindStringSubmatch(row); len(m) > 1 {
		item.Title = strings.TrimSpace(m[1])
	}
	if flixTVPat.MatchString(row) {
		item.MediaType = model.MediaTypeTV
	} else if flixMoviePat.MatchString(row) {
		item.MediaType = model.MediaTypeMovie
	}
	if flixAnimePat.MatchString(row) {
		item.hasAnimeGenre = true
	}
	if m := flixTitleYear.FindStringSubmatch(item.Title); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
			item.Year = y
		}
	}
	// Fall back to year from premiere date in the row HTML.
	// FlixPatrol provides a full premiere date (MM/DD/YYYY) in a tooltip
	// that is more reliable than defaulting to current year.
	if item.Year == 0 {
		if m := flixPremierePat.FindStringSubmatch(row); len(m) > 1 {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
				item.Year = y
			}
		}
	}
	if m := flixIMDbPat.FindStringSubmatch(row); len(m) > 1 {
		item.IMDbRating, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := flixYTViewsPat.FindStringSubmatch(row); len(m) > 1 {
		cleaned := strings.ReplaceAll(m[1], ",", "")
		item.YoutubeView, _ = strconv.ParseInt(cleaned, 10, 64)
	}

	return item
}

func cleanFlixTitle(title string) string {
	title = html.UnescapeString(title)
	title = flixTitleYear.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

func parseFlixDate(dateStr string) time.Time {
	t, err := time.Parse("Jan 2", dateStr)
	if err != nil {
		return time.Time{}
	}
	now := time.Now()
	year := now.Year()
	if now.Month() < t.Month() {
		year--
	}
	return truncateToDay(t.AddDate(year, 0, 0))
}

func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
