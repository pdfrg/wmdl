package discover

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/artcache"
	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

type AOTYProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	filter     config.MusicFilterConfig
	allocCtx   context.Context // shared headless-browser allocator; nil disables fetching
}

func NewAOTYProvider(allocCtx context.Context, filter config.MusicFilterConfig) *AOTYProvider {
	return &AOTYProvider{
		filter:   filter,
		allocCtx: allocCtx,
	}
}

func (p *AOTYProvider) Name() string {
	return "albumoftheyear"
}

func (p *AOTYProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *AOTYProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	// Compute the wmdl week (Wed-Tue) as a date range
	weekStart, weekEnd := wmdlWeekRange(year, week)

	var allItems []ScrapedItem
	page := 1

	for {
		items, err := p.scrapePage(page)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		if len(items) == 0 {
			break
		}

		// Track whether any items on this page fall in our window
		var kept int
		var oldestDate time.Time

		for _, item := range items {
			itemDate, err := time.Parse("2006-01-02", item.ReleaseDate)
			if err != nil {
				continue
			}

			if oldestDate.IsZero() || itemDate.Before(oldestDate) {
				oldestDate = itemDate
			}

			if itemDate.Before(weekStart) {
				continue
			}
			if itemDate.After(weekEnd) {
				continue
			}

			// Discard low-quality items right here — no point keeping them
			if !passesMusicFilter(p.filter, item) {
				// Still count as "kept" so pagination works correctly based on date
				kept++
				continue
			}

			item.MediaType = model.MediaTypeMusic
			item.Source = "albumoftheyear"
			allItems = append(allItems, item)
			kept++
		}

		// If all items on this page are older than our window, we're done
		if kept == 0 && !oldestDate.IsZero() && oldestDate.Before(weekStart) {
			break
		}

		page++

		// Safety limit
		if page > 20 {
			break
		}
	}

	// Enrich genres + cover art from individual album pages. One browser
	// visit per album provides both; art goes straight into the poster
	// cache the review TUI reads, keeping review network-free.
	if len(allItems) > 0 {
		p.enrichItems(allItems)
	}

	return allItems, nil
}

// enrichItems visits each album's detail page (concurrency 2 so the browser
// tabs and request pacing don't re-trigger Cloudflare challenges) and
// extracts genre tags. On the same page it snapshots the cover <img> into
// the shared poster cache — the AOTY image CDN sits behind the same
// Cloudflare challenge as the site, so a plain-HTTP fetch from review gets
// 403; pre-caching during discover avoids that entirely.
func (p *AOTYProvider) enrichItems(items []ScrapedItem) {
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup

	fetched, cached, failed := 0, 0, 0

	for i := range items {
		if items[i].AOTYURL == "" {
			continue
		}
		wg.Add(1)
		go func(item *ScrapedItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ct, cancel := chromedp.NewContext(p.allocCtx)
			defer cancel()
			pageCtx, pageCancel := context.WithTimeout(ct, 150*time.Second)
			defer pageCancel()

			if err := chromedp.Run(pageCtx,
				chromedp.Navigate(item.AOTYURL),
				chromedp.WaitReady("body"),
			); err != nil {
				log.Warn().Str("source", "albumoftheyear").Msgf("album page nav failed %s: %v", item.Title, err)
				return
			}
			if err := waitForRealPage(pageCtx, 120*time.Second); err != nil {
				log.Warn().Str("source", "albumoftheyear").Msgf("album page challenge timeout %s: %v", item.Title, err)
				return
			}

			var html string
			if err := chromedp.Run(pageCtx, chromedp.OuterHTML("html", &html)); err == nil {
				item.Genres = extractGenres(html)
			}

			n, err := p.snapshotCover(pageCtx, item)
			if err != nil {
				failed++
				log.Warn().Err(err).Str("source", "albumoftheyear").Msgf("album art fetch failed: %s — %s", item.ArtistName, item.Title)
				return
			}
			switch n {
			case coverFetched:
				fetched++
			case coverCached:
				cached++
			}
		}(&items[i])
	}
	wg.Wait()

	if fetched > 0 || failed > 0 {
		log.Info().Str("source", "albumoftheyear").Msgf("album art: %d fetched, %d already cached, %d failed", fetched, cached, failed)
	}
}

// extractGenres pulls the genre tags out of an album detail page's HTML.
func extractGenres(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	var genres []string
	doc.Find("a[href*='/genre/']").Each(func(i int, a *goquery.Selection) {
		g := strings.TrimSpace(a.Text())
		if g != "" {
			genres = append(genres, g)
		}
	})

	return strings.Join(genres, ", ")
}

const (
	coverFetched coverResult = iota
	coverCached
	coverMissing
)

type coverResult int

// snapshotCover captures the cover <img> rendered on the album detail page
// the tab is currently on, writing it to the review poster cache. Returns
// coverMissing when the item has no art or the img isn't loaded.
func (p *AOTYProvider) snapshotCover(pageCtx context.Context, item *ScrapedItem) (coverResult, error) {
	if item.ImageURL == "" {
		return coverMissing, nil
	}
	cachePath, err := artcache.AlbumArtPath(item.ImageURL)
	if err != nil {
		return coverMissing, err
	}
	if _, err := os.Stat(cachePath); err == nil {
		return coverCached, nil
	}

	// The cover <img> on the detail page carries the same filename as the
	// poster_path we stored, just in a smaller size variant (e.g. 375x0).
	file := item.ImageURL
	if i := strings.Index(file, "/album/"); i >= 0 {
		file = file[i+len("/album/"):]
	}
	if q := strings.IndexByte(file, '?'); q >= 0 {
		file = file[:q]
	}

	sel := fmt.Sprintf("img[src*=%q]", file)
	if err := chromedp.Run(pageCtx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
	); err != nil {
		return coverMissing, fmt.Errorf("cover img %s not loaded on detail page: %w", file, err)
	}

	var buf []byte
	if err := chromedp.Run(pageCtx, chromedp.Screenshot(sel, &buf, chromedp.NodeVisible)); err != nil {
		return coverMissing, fmt.Errorf("screenshotting cover %s: %w", file, err)
	}

	img, err := png.Decode(bytes.NewReader(buf))
	if err != nil {
		return coverMissing, fmt.Errorf("decoding screenshot: %w", err)
	}

	f, err := os.Create(cachePath)
	if err != nil {
		return coverMissing, fmt.Errorf("creating cache file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		return coverMissing, fmt.Errorf("encoding cache png: %w", err)
	}
	return coverFetched, nil
}

// fetchPage wraps fetchPageOnce with a single retry on transient errors
// (WebSocket disconnect, Cloudflare timeout, Varnish 503).
func (p *AOTYProvider) fetchPage(url string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
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

func (p *AOTYProvider) fetchPageOnce(url string) (string, error) {
	if p.allocCtx == nil {
		return "", fmt.Errorf("chromedp allocator not available")
	}

	ct, cancel := chromedp.NewContext(p.allocCtx)
	defer cancel()

	pageCtx, pageCancel := context.WithTimeout(ct, 120*time.Second)
	defer pageCancel()

	if err := chromedp.Run(pageCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	); err != nil {
		return "", fmt.Errorf("navigating to %s: %w", url, err)
	}

	if err := waitForRealPage(pageCtx, 120*time.Second); err != nil {
		return "", fmt.Errorf("cloudflare challenge: %w", err)
	}

	var html string
	if err := chromedp.Run(pageCtx, chromedp.OuterHTML("html", &html)); err != nil {
		return "", fmt.Errorf("getting page HTML: %w", err)
	}

	return html, nil
}

func (p *AOTYProvider) scrapePage(page int) ([]ScrapedItem, error) {
	url := fmt.Sprintf("https://www.albumoftheyear.org/releases/%d/", page)
	if page <= 1 {
		url = "https://www.albumoftheyear.org/releases/"
	}

	html, err := p.fetchPage(url)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parsing html: %w", err)
	}

	var items []ScrapedItem

	doc.Find("div.albumBlock").Each(func(i int, s *goquery.Selection) {
		item := p.parseBlock(s)
		if item != nil {
			items = append(items, *item)
		}
	})

	return items, nil
}

func (p *AOTYProvider) parseBlock(s *goquery.Selection) *ScrapedItem {
	// Determine album type from data-type attribute
	albumTypeStr, _ := s.Attr("data-type")
	albumType := model.AlbumType(albumTypeStr)

	// Exclude types that don't integrate with Lidarr
	switch albumType {
	case model.AlbumTypeMixtape, model.AlbumTypeCompilation, model.AlbumTypeReissue, model.AlbumTypeSingle:
		return nil
	}

	artistName := strings.TrimSpace(s.Find("div.artistTitle").First().Text())
	albumTitle := strings.TrimSpace(s.Find("div.albumTitle").First().Text())
	if artistName == "" || albumTitle == "" {
		return nil
	}

	// Parse date and album sub-type from type div
	typeStr := strings.TrimSpace(s.Find("div.type").First().Text())
	releaseDate := parseAOTYDate(typeStr)

	// Parse scores
	var criticScore, userScore float64
	var criticCount, userCount int

	s.Find("div.ratingRow").Each(func(i int, row *goquery.Selection) {
		ratingText := strings.TrimSpace(row.Find("div.ratingText").First().Text())
		ratingStr := strings.TrimSpace(row.Find("div.rating").First().Text())
		if ratingStr == "" {
			return
		}

		score, err := strconv.ParseFloat(ratingStr, 64)
		if err != nil {
			return
		}

		// Extract count from parenthetical e.g. "(15)" or "(1,234)"
		countStr := ""
		row.Find("div.ratingText").Each(func(j int, rt *goquery.Selection) {
			t := strings.TrimSpace(rt.Text())
			if strings.HasPrefix(t, "(") {
				countStr = strings.Trim(t, "()")
				countStr = strings.ReplaceAll(countStr, ",", "")
			}
		})
		count, _ := strconv.Atoi(countStr)

		if strings.Contains(ratingText, "critic") {
			criticScore = score
			criticCount = count
		} else if strings.Contains(ratingText, "user") {
			userScore = score
			userCount = count
		}
	})

	mustHear := s.Find("div.mustHear, div.image.mustHear").Length() > 0

	// Extract cover art URL from div.image
	imageURL := extractImageURL(s.Find("div.image").First())

	// Extract AOTY detail page URL from the <a> tag wrapping the block
	aotyURL := ""
	if href, exists := s.Find("a.albumBlock").Attr("href"); exists {
		aotyURL = "https://www.albumoftheyear.org" + href
	} else if href, exists := s.Find("a").First().Attr("href"); exists {
		if strings.HasPrefix(href, "/album/") {
			aotyURL = "https://www.albumoftheyear.org" + href
		}
	}

	// Extract year from date
	year := 0
	if t, err := time.Parse("2006-01-02", releaseDate); err == nil {
		year = t.Year()
	}

	return &ScrapedItem{
		Title:           albumTitle,
		Year:            year,
		ReleaseDate:     releaseDate,
		ArtistName:      artistName,
		AlbumType:       albumType,
		AOTYCriticScore: criticScore,
		AOTYCriticCount: criticCount,
		AOTYUserScore:   userScore,
		AOTYUserCount:   userCount,
		AOTYMustHear:    mustHear,
		ImageURL:        imageURL,
		AOTYURL:         aotyURL,
	}
}

// extractImageURL tries to find a cover art URL from a div.image element.
// AOTY uses various formats: inline style, img tag, or data-src.
func extractImageURL(sel *goquery.Selection) string {
	if sel.Length() == 0 {
		return ""
	}

	var u string

	// Strategy 1: style="background-image: url(...)"
	if style, exists := sel.Attr("style"); exists {
		if idx := strings.Index(style, "url("); idx >= 0 {
			urlStart := idx + 4
			end := strings.LastIndex(style, ")")
			if end > urlStart {
				u = strings.Trim(style[urlStart:end], "'\" ")
				if strings.HasPrefix(u, "//") {
					u = "https:" + u
				}
			}
		}
	}

	// Strategy 2: <img src="...">
	if u == "" {
		if src, exists := sel.Find("img").First().Attr("src"); exists {
			u = src
			if strings.HasPrefix(u, "//") {
				u = "https:" + u
			}
		}
	}

	// Strategy 3: data-src attribute on the element itself or a child
	if u == "" {
		for _, attr := range []string{"data-src", "data-lazy", "data-original"} {
			if val, exists := sel.Attr(attr); exists && val != "" {
				u = val
				if strings.HasPrefix(u, "//") {
					u = "https:" + u
				}
				break
			}
		}
	}

	// Upgrade to larger size: /200x0/ → /500x0/
	if u != "" && strings.HasPrefix(u, "http") {
		u = strings.Replace(u, "/200x0/", "/500x0/", 1)
	}
	return u
}

func parseAOTYDate(s string) string {
	s = strings.Split(s, " • ")[0]
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Build a map of month names to numbers
	months := map[string]string{
		"january": "01", "february": "02", "march": "03", "april": "04",
		"may": "05", "june": "06", "july": "07", "august": "08",
		"september": "09", "october": "10", "november": "11", "december": "12",
		"jan": "01", "feb": "02", "mar": "03", "apr": "04",
		"jun": "06", "jul": "07", "aug": "08", "sep": "09",
		"oct": "10", "nov": "11", "dec": "12",
	}

	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return ""
	}

	monthNum, ok := months[strings.ToLower(parts[0])]
	if !ok {
		return ""
	}

	day, err := strconv.Atoi(parts[1])
	if err != nil || day < 1 || day > 31 {
		return ""
	}

	now := time.Now()
	year := now.Year()

	// Build the date
	dateStr := fmt.Sprintf("%04d-%s-%02d", year, monthNum, day)
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		// If the parsed date is more than 30 days in the future, it's probably
		// from a previous December being shown in January context
		if t.After(now.Add(30 * 24 * time.Hour)) {
			dateStr = fmt.Sprintf("%04d-%s-%02d", year-1, monthNum, day)
		}
		return dateStr
	}

	return ""
}

// passesMusicFilter checks whether a scraped music item meets quality thresholds.
// Standard types (LP, EP, soundtrack): require score + review count.
// Special types (live, remix, box set): score only, no review count.
func passesMusicFilter(ft config.MusicFilterConfig, item ScrapedItem) bool {
	isStandard := item.AlbumType == model.AlbumTypeLP ||
		item.AlbumType == model.AlbumTypeEP ||
		item.AlbumType == model.AlbumTypeSoundtrack

	if isStandard {
		return (item.AOTYCriticScore >= float64(ft.MinCriticScore) && item.AOTYCriticCount >= ft.MinCriticReviews) ||
			(item.AOTYUserScore >= float64(ft.MinUserScore) && item.AOTYUserCount >= ft.MinUserRatings) ||
			(ft.IncludeMustHear && item.AOTYMustHear)
	}

	return (ft.MinCriticScore > 0 && item.AOTYCriticScore >= float64(ft.MinCriticScore)) ||
		(ft.MinUserScore > 0 && item.AOTYUserScore >= float64(ft.MinUserScore)) ||
		(ft.IncludeMustHear && item.AOTYMustHear)
}

// wmdlWeekRange computes the Wed-to-Tue date range for an ISO week.
func wmdlWeekRange(year, week int) (start, end time.Time) {
	// Get the Tuesday (last day of wmdl week) from the ISO week
	// ISO week ends on Sunday, but wmdl week ends on Tuesday.
	// Tuesday of ISO week = Monday + 1.
	monday := isoWeekToDate(year, week)
	tuesday := monday.AddDate(0, 0, 1)

	// wmdl week: Wed to Tue. Wednesday = Tuesday - 6 days.
	wednesday := tuesday.AddDate(0, 0, -6)

	return wednesday, tuesday
}
