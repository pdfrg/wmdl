package discover

import (
	"context"
	"fmt"
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
	targetYear int
	targetWeek int
	hasTarget  bool
}

var (
	_ ReleaseProvider = (*GoodreadsProvider)(nil)
	_ WeekSettable    = (*GoodreadsProvider)(nil)
)

func NewGoodreadsProvider(debugURL string) *GoodreadsProvider {
	return &GoodreadsProvider{
		debugURL: debugURL,
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
	ImageURL     string
	Description  string
	URL          string
}

// Scrape fetches the Goodreads popular_by_date page for the target month.
// Since Goodreads only has monthly granularity, we scrape one month per call.
// The target month is derived from the target ISO week.
func (p *GoodreadsProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	// Map the ISO week to a month for the Goodreads URL
	wed := tuesdayOfISOWeek(year, week)
	month := int(wed.Month())

	items, err := p.scrapeMonth(year, month)
	if err != nil {
		return nil, fmt.Errorf("scraping goodreads: %w", err)
	}

	log.Info().Int("count", len(items)).Int("year", year).Int("month", month).Msg("goodreads: found books")

	// Convert to ScrapedItems
	var result []ScrapedItem
	for _, b := range items {
		result = append(result, ScrapedItem{
			Title:        b.Title,
			ArtistName:   b.Author, // reuse ArtistName field for author
			MediaType:    model.MediaTypeBook,
			ReleaseType:  "",
			Source:       "goodreads",
			ImageURL:     b.ImageURL,
			Notes:        b.URL,
			Overview:     b.Description,
			ImdbRating:   b.Rating, // Goodreads rating stored temporarily
			RatingsCount: b.RatingsCount,
		})
	}

	return result, nil
}

func (p *GoodreadsProvider) scrapeMonth(year, month int) ([]grScrapedBook, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), p.debugURL)
	defer allocCancel()

	ct, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer cancel()

	scrapeCtx, cancel := context.WithTimeout(ct, 45*time.Second)
	defer cancel()

	pageURL := fmt.Sprintf("https://www.goodreads.com/book/popular_by_date/%d/%d", year, month)

	var html string
	if err := chromedp.Run(scrapeCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3*time.Second),
		// Scroll to bottom to trigger lazy loading
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &html),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	return parseGRPage(html), nil
}

// Regex patterns for parsing Goodreads popular_by_date page
var (
	grBookBlock = regexp.MustCompile(`<article[^>]*class="[^"]*BookListItem[^"]*"[^>]*>(.*?)</article>`)

	grTitle   = regexp.MustCompile(`class="[^"]*title[^"]*"[^>]*>\s*<a[^>]*>\s*([^<]+)`)
	grAuthor  = regexp.MustCompile(`class="[^"]*author[^"]*"[^>]*>\s*<a[^>]*>\s*([^<]+)`)
	grRating  = regexp.MustCompile(`class="[^"]*rating[^"]*"[^>]*>\s*([\d.]+)`)
	grRatings = regexp.MustCompile(`class="[^"]*shelvings[^"]*"[^>]*>\s*([\d,.kK]+)`)
	grImage   = regexp.MustCompile(`<img[^>]*src="([^"]+)"[^>]*class="[^"]*cover[^"]*"`)
	grDesc    = regexp.MustCompile(`class="[^"]*description[^"]*"[^>]*>\s*([^<]+)`)
	grLink    = regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*class="[^"]*title[^"]*"`)
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
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	return s
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
