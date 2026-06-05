package discover

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/model"
)

type GoodreadsProvider struct {
	debugURL   string
	allocCtx   context.Context // shared chromedp allocator from runner
	targetYear int
	targetWeek int
	hasTarget  bool
}

var (
	_ ReleaseProvider = (*GoodreadsProvider)(nil)
	_ WeekSettable    = (*GoodreadsProvider)(nil)
)

func NewGoodreadsProvider(debugURL string, allocCtx context.Context) *GoodreadsProvider {
	return &GoodreadsProvider{
		debugURL: debugURL,
		allocCtx: allocCtx,
	}
}

func (p *GoodreadsProvider) Name() string {
	return "goodreads"
}

func (p *GoodreadsProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

type grScrapedBook struct {
	Title        string
	Author       string
	Rating       float64
	RatingsCount int
	Shelvings    int
	ImageURL     string
	Description  string
	URL          string
}

// Scrape fetches the Goodreads popular_by_date page for the target month.
// Since Goodreads only has monthly granularity, we scrape the month(s)
// that overlap with the target ISO week. If the week spans two months
// (e.g. early April week includes late March days), both months are
// scraped and merged.
func (p *GoodreadsProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	// ISO week runs Wednesday–Tuesday. Determine the months of both the
	// start (Wednesday) and end (Tuesday) of the week.
	monday := isoWeekToDate(year, week) // Monday of ISO week
	wed := monday.AddDate(0, 0, 2)      // Wednesday = start of week
	tue := tuesdayOfISOWeek(year, week) // Tuesday = end of week

	wedYear, wedMonth := wed.Year(), wed.Month()
	tueYear, tueMonth := tue.Year(), tue.Month()

	// Scrape the start and end months. Deduplicate when they're the same.
	type ym struct{ y, m int }
	seen := make(map[ym]bool)
	var allItems []grScrapedBook
	for _, ym := range []ym{{wedYear, int(wedMonth)}, {tueYear, int(tueMonth)}} {
		if seen[ym] {
			continue
		}
		seen[ym] = true
		items, err := p.scrapeMonth(ym.y, ym.m)
		if err != nil {
			log.Warn().Err(err).Int("year", ym.y).Int("month", ym.m).Msg("goodreads month scrape failed")
			continue
		}
		allItems = append(allItems, items...)
	}

	if len(allItems) == 0 {
		return nil, fmt.Errorf("goodreads: no books found for %d-W%02d", year, week)
	}

	log.Info().Int("count", len(allItems)).Int("year", year).Int("week", week).Msg("goodreads: found books")

	var result []ScrapedItem
	for _, b := range allItems {
		result = append(result, ScrapedItem{
			Title:          b.Title,
			ArtistName:     b.Author,
			MediaType:      model.MediaTypeBook,
			ReleaseType:    "",
			Source:         "goodreads",
			ImageURL:       b.ImageURL,
			Notes:          b.URL,
			Overview:       b.Description,
			ImdbRating:     b.Rating,
			RatingsCount:   b.RatingsCount,
			ShelvingsCount: b.Shelvings,
		})
	}

	return result, nil
}

func (p *GoodreadsProvider) scrapeMonth(year, month int) ([]grScrapedBook, error) {
	ct, cancelCT := chromedp.NewContext(p.allocCtx)
	defer cancelCT()

	scrapeCtx, cancel := context.WithTimeout(ct, 45*time.Second)
	defer cancel()

	pageURL := fmt.Sprintf("https://www.goodreads.com/book/popular_by_date/%d/%d", year, month)

	// Navigate and wait for initial page load
	if err := chromedp.Run(scrapeCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	// Click "Show more books" up to 5 times to load enough books
	for i := 0; i < 5; i++ {
		var clicked bool
		if err := chromedp.Run(scrapeCtx,
			chromedp.Evaluate(`(function() {
				const btn = Array.from(document.querySelectorAll('button'))
					.find(b => b.textContent.includes('Show more books'));
				if (btn) { btn.click(); return true; }
				return false;
			})()`, &clicked),
			chromedp.Sleep(2*time.Second),
		); err != nil {
			break
		}
		if !clicked {
			break
		}
	}

	var html string
	if err := chromedp.Run(scrapeCtx,
		chromedp.OuterHTML("html", &html),
	); err != nil {
		return nil, fmt.Errorf("get html: %w", err)
	}

	return parseGRPage(html), nil
}

// Regex patterns for parsing Goodreads popular_by_date page
var (
	grBookBlock = regexp.MustCompile(`<article[^>]*class="[^"]*BookListItem[^"]*"[^>]*>(.*?)</article>`)

	grTitle     = regexp.MustCompile(`data-testid="bookTitle"[^>]*>([^<]+)`)
	grAuthor    = regexp.MustCompile(`class="ContributorLink__name"[^>]*>([^<]+)`)
	grRating    = regexp.MustCompile(`data-testid="ratingValue"[^>]*>\s*<span[^>]*>([\d.]+)`)
	grRatings   = regexp.MustCompile(`data-testid="ratingsCount"[^>]*>.*?>([\d,.]+(?:k|K|m|M)?).*ratings`)
	grShelvings = regexp.MustCompile(`(\d[\d,.]*(?:k|K|m|M)?)\s*shelvings`)
	grImage     = regexp.MustCompile(`<img[^>]*\bsrc="([^"]+)"`)
	grDesc      = regexp.MustCompile(`data-testid="contentContainer"[^>]*>\s*<span[^>]*>\s*([^<]+)`)
	grLink      = regexp.MustCompile(`<a[^>]*\bhref="([^"]+)"`)
)

func parseGRPage(html string) []grScrapedBook {
	blocks := grBookBlock.FindAllStringSubmatch(html, -1)

	var books []grScrapedBook
	for _, match := range blocks {
		if len(match) < 2 {
			continue
		}
		block := match[1]

		title := extractMatch(grTitle, block)
		if title == "" {
			continue
		}
		title = htmlUnescape(title)
		title = strings.TrimSpace(title)

		author := extractMatch(grAuthor, block)
		author = htmlUnescape(author)
		author = strings.TrimSpace(author)

		ratingStr := extractMatch(grRating, block)
		rating, _ := strconv.ParseFloat(ratingStr, 64)

		ratingsStr := extractMatch(grRatings, block)
		ratingsCount := parseGRRatings(ratingsStr)

		shelvingsStr := extractMatch(grShelvings, block)
		shelvings := parseGRRatings(shelvingsStr)

		imageURL := extractMatch(grImage, block)

		description := extractMatch(grDesc, block)
		description = htmlUnescape(description)
		description = strings.TrimSpace(description)

		bookURL := extractMatch(grLink, block)

		books = append(books, grScrapedBook{
			Title:        title,
			Author:       author,
			Rating:       rating,
			RatingsCount: ratingsCount,
			Shelvings:    shelvings,
			ImageURL:     imageURL,
			Description:  description,
			URL:          bookURL,
		})
	}

	return books
}

func extractMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func htmlUnescape(s string) string {
	return html.UnescapeString(s)
}

func parseGRRatings(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	multiplier := 1
	switch last := s[len(s)-1]; last {
	case 'k', 'K':
		multiplier = 1000
		s = s[:len(s)-1]
	case 'm', 'M':
		multiplier = 1000000
		s = s[:len(s)-1]
	}

	s = strings.ReplaceAll(s, ",", "")

	if strings.Contains(s, ".") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int(f * float64(multiplier))
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n * multiplier
}
