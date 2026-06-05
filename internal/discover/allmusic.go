package discover

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/model"
)

type AllMusicProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	debugURL   string
	allocCtx   context.Context
}

var (
	_ ReleaseProvider = (*AllMusicProvider)(nil)
	_ WeekSettable    = (*AllMusicProvider)(nil)
)

func NewAllMusicProvider(debugURL string, allocCtx context.Context) *AllMusicProvider {
	return &AllMusicProvider{
		debugURL: debugURL,
		allocCtx: allocCtx,
	}
}

func (p *AllMusicProvider) Name() string {
	return "allmusic"
}

func (p *AllMusicProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

// Scrape checks if the target Wed-Tue week contains the 1st day of any month.
// If so, it scrapes AllMusic Editor's Choice for the PREVIOUS month.
// Example: week 23 (Wed May 27 – Tue Jun 2) contains June 1 → scrape May 2026.
func (p *AllMusicProvider) Scrape() ([]ScrapedItem, error) {
	now := time.Now()
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = now.ISOWeek()
	}

	weekStart, weekEnd := wmdlWeekRange(year, week)
	weekFmt := weekStart.Format("Jan 2") + " – " + weekEnd.Format("Jan 2")

	scrapeMonth, scrapeYear := computeAllMusicTarget(weekStart, weekEnd)
	if scrapeMonth == 0 {
		log.Info().Str("provider", "allmusic").Msgf("no month boundary (ISO %d-W%02d: %s), skipping", year, week, weekFmt)
		return nil, nil
	}

	// Don't scrape future months
	if scrapeYear > now.Year() || (scrapeYear == now.Year() && scrapeMonth > int(now.Month())) {
		log.Info().Str("provider", "allmusic").Msgf("scrape target %s %d is in the future, skipping", time.Month(scrapeMonth), scrapeYear)
		return nil, nil
	}

	log.Info().Str("provider", "allmusic").Msgf("scraping %s %d", time.Month(scrapeMonth), scrapeYear)
	return p.scrapeMonth(scrapeYear, scrapeMonth, year, week)
}

// computeAllMusicTarget finds the month boundary within the week range.
// Returns the year and month to scrape (the month BEFORE the boundary).
func computeAllMusicTarget(start, end time.Time) (int, int) {
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Day() == 1 {
			prev := d.AddDate(0, -1, 0)
			return int(prev.Month()), prev.Year()
		}
	}
	return 0, 0
}

func (p *AllMusicProvider) scrapeMonth(scrapeYear, scrapeMonth, progYear, progWeek int) ([]ScrapedItem, error) {
	monthStr := strings.ToLower(time.Month(scrapeMonth).String())
	targetURL := fmt.Sprintf("https://www.allmusic.com/newreleases/editorschoice/%s-%d", monthStr, scrapeYear)

	html, err := p.fetchPage(targetURL)
	if err != nil {
		return nil, fmt.Errorf("fetching allmusic page: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parsing html: %w", err)
	}

	var items []ScrapedItem
	doc.Find("article.editorsChoiceItem").Each(func(i int, s *goquery.Selection) {
		item := p.parseBlock(s, scrapeYear, scrapeMonth, progYear, progWeek)
		if item != nil {
			items = append(items, *item)
		}
	})

	log.Info().Str("provider", "allmusic").Msgf("found %d editor's choice entries", len(items))
	return items, nil
}

// fetchPage wraps fetchPageOnce with a single retry on transient errors
// (WebSocket disconnect, Cloudflare timeout, Varnish 503).
func (p *AllMusicProvider) fetchPage(url string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			log.Info().Str("provider", "allmusic").Msg("retrying fetch after transient error")
			select {
			case <-p.allocCtx.Done():
				return "", p.allocCtx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		html, err := p.fetchPageOnce(url)
		if err == nil {
			return html, nil
		}
		lastErr = err
		if !isTransientError(err) {
			break
		}
	}
	return "", lastErr
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(err.Error(), "backend fetch failed (503)")
}

func (p *AllMusicProvider) fetchPageOnce(url string) (string, error) {
	if p.allocCtx == nil {
		return "", fmt.Errorf("chromedp allocator not available")
	}

	ct, cancel := chromedp.NewContext(p.allocCtx)
	defer cancel()

	pageCtx, pageCancel := context.WithTimeout(ct, 120*time.Second)
	defer pageCancel()

	log.Info().Str("provider", "allmusic").Msg("navigating to editors choice page")
	if err := chromedp.Run(pageCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	); err != nil {
		return "", fmt.Errorf("navigating to %s: %w", url, err)
	}

	if err := waitForRealPage(ct); err != nil {
		return "", fmt.Errorf("Cloudflare challenge: %w", err)
	}

	var html string
	if err := chromedp.Run(ct, chromedp.OuterHTML("html", &html)); err != nil {
		return "", fmt.Errorf("getting page HTML: %w", err)
	}

	return html, nil
}

// retry calls fn up to n times with the given delay between attempts.
func retry(n int, delay time.Duration, fn func() error) error {
	var lastErr error
	for i := 0; i < n; i++ {
		if err := fn(); err != nil {
			lastErr = err
			time.Sleep(delay)
			continue
		}
		return nil
	}
	return lastErr
}

// waitForRealPage polls the page title until the real page loads (Cloudflare
// challenge passes) or a Varnish 503 is detected. Times out after 120 seconds.
// Title reads are retried on transient errors — the Cloudflare challenge
// redirect briefly resets the CDP session, and chromedp.Title can return
// context.Canceled during that window.
func waitForRealPage(ct context.Context) error {
	waitCtx, waitCancel := context.WithTimeout(ct, 120*time.Second)
	defer waitCancel()

	var title string
	for i := 0; i < 60; i++ {
		if err := retry(5, 500*time.Millisecond, func() error {
			return chromedp.Run(waitCtx, chromedp.Title(&title))
		}); err != nil {
			return err
		}

		// Check for Varnish 503 immediately (title contains "503" or "Error")
		if strings.Contains(title, "503") || strings.Contains(title, "Error") {
			var html string
			if err := retry(3, 500*time.Millisecond, func() error {
				return chromedp.Run(waitCtx, chromedp.OuterHTML("html", &html))
			}); err == nil {
				if strings.Contains(html, "503 Backend fetch failed") {
					return fmt.Errorf("backend fetch failed (503)")
				}
			}
		}

		if !strings.Contains(title, "Just a moment") &&
			!strings.Contains(title, "503") &&
			!strings.Contains(title, "Error") &&
			title != "" {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for real page, last title: %q", title)
}

var allMusicBracketSuffix = strings.NewReplacer(
	"[2 CD]", "",
	"[2LP]", "",
	"[CD]", "",
	"[LP]", "",
	" [Deluxe Edition]", "",
	" [Deluxe]", "",
	" [Super Deluxe]", "",
)

// allMusicCleanTitle strips AllMusic-specific formatting from album titles.
func allMusicCleanTitle(title string) string {
	cleaned := allMusicBracketSuffix.Replace(title)
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}

func (p *AllMusicProvider) parseBlock(s *goquery.Selection, scrapeYear, scrapeMonth, progYear, progWeek int) *ScrapedItem {
	artist := strings.TrimSpace(s.Find("div.artist a").First().Text())
	album := strings.TrimSpace(s.Find("h2.title a").First().Text())
	if artist == "" || album == "" {
		return nil
	}

	// Extract AllMusic rating from div.allmusicRating aria-label
	rating := 0.0
	s.Find("div.allmusicRating").Each(func(i int, rEl *goquery.Selection) {
		if label, exists := rEl.Attr("aria-label"); exists {
			var r float64
			if _, err := fmt.Sscanf(label, "AllMusic rating: %f", &r); err == nil {
				rating = r
			}
		}
	})

	// Extract AllMusic detail page URL
	albumURL := ""
	if href, exists := s.Find("h2.title a").First().Attr("href"); exists {
		if strings.HasPrefix(href, "/") {
			albumURL = "https://www.allmusic.com" + href
		} else {
			albumURL = href
		}
	}

	// Extract cover art URL (use 400px version)
	imageURL := ""
	s.Find("img.ecCoverImg").Each(func(i int, img *goquery.Selection) {
		if src, exists := img.Attr("src"); exists {
			// Upgrade to 400px if it's a smaller size
			imageURL = strings.Replace(src, "/120/", "/400/", 1)
			imageURL = strings.Replace(imageURL, "/220/", "/400/", 1)
		}
	})

	// Build release date
	releaseDate := fmt.Sprintf("%04d-%02d-01", scrapeYear, scrapeMonth)

	title := allMusicCleanTitle(album)

	return &ScrapedItem{
		Title:          title,
		ArtistName:     artist,
		Year:           scrapeYear,
		MediaType:      model.MediaTypeMusic,
		ReleaseDate:    releaseDate,
		ImageURL:       imageURL,
		AllMusicURL:    albumURL,
		AllMusicRating: rating,
		Source:         "allmusic",
	}
}
