package discover

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/rs/zerolog/log"
)

type BookshopProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
}

var (
	_ ReleaseProvider = (*BookshopProvider)(nil)
	_ WeekSettable    = (*BookshopProvider)(nil)
)

func NewBookshopProvider() *BookshopProvider {
	return &BookshopProvider{}
}

func (p *BookshopProvider) Name() string {
	return "bookshop"
}

func (p *BookshopProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

var (
	bsNewReleasesDate = regexp.MustCompile(`New Releases:\s*(\w+\s+\d+,\s*\d{4})`)
	bsBookBlock       = regexp.MustCompile(`<h1[^>]*class="[^"]*title[^"]*"[^>]*>(.*?)</h1>`)
)

func (p *BookshopProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	pageURL := "https://bookshop.org/lists/new-books"

	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bookshop: request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: false, // Cloudflare blocks Go HTTP/2
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bookshop: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bookshop: read: %w", err)
	}

	html := string(body)

	// Parse the "New Releases: June 2, 2026" date from the page
	dateMatch := bsNewReleasesDate.FindStringSubmatch(html)
	if len(dateMatch) < 2 {
		return nil, fmt.Errorf("bookshop: no release date found on page")
	}

	releaseDateStr := dateMatch[1]
	releaseDate, err := time.Parse("January 2, 2006", releaseDateStr)
	if err != nil {
		// Try alternate format
		releaseDate, err = time.Parse("Jan 2, 2006", releaseDateStr)
		if err != nil {
			return nil, fmt.Errorf("bookshop: parse date %q: %w", releaseDateStr, err)
		}
	}

	// The release date is typically a Tuesday (WMDL anchor).
	// Compute the ISO week from this date to compare with target.
	pageYear, pageWeek := releaseDate.ISOWeek()

	// Extract all book title blocks (appear twice: mobile + desktop)
	titleMatches := bsBookBlock.FindAllStringSubmatch(html, -1)
	if len(titleMatches) == 0 {
		return nil, fmt.Errorf("bookshop: no books found on page")
	}

	// Deduplicate by title (each book appears twice: mobile + desktop)
	seen := make(map[string]bool)
	var books []struct {
		Title       string
		Author      string
		ImageURL    string
		EAN         string
		Description string
	}

	for _, m := range titleMatches {
		title := strings.TrimSpace(m[1])
		title = htmlUnescape(title)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true

		// Skip non-book h1s that appear on the page
		if strings.HasPrefix(title, "Our mission") || title == "New Books" {
			continue
		}

		books = append(books, struct {
			Title       string
			Author      string
			ImageURL    string
			EAN         string
			Description string
		}{Title: title})
	}

	// Re-extract with context for author/image/EAN
	// Use a different approach: find book blocks by looking at the <a> tag containing the title
	bookLinkPattern := regexp.MustCompile(`<a[^>]*href="(/p/books/[^"]*)"[^>]*>.*?<h1[^>]*class="[^"]*title[^"]*"[^>]*>([^<]+)</h1>.*?<p[^>]*class="flex items-end text-sm"[^>]*>([^<]+)`)
	linkMatches := bookLinkPattern.FindAllStringSubmatch(html, -1)

	bookMap := make(map[string]struct {
		Author   string
		EAN      string
		ImageURL string
		Desc     string
		URL      string
	})

	for _, m := range linkMatches {
		if len(m) < 4 {
			continue
		}
		href := m[1]
		title := strings.TrimSpace(htmlUnescape(m[2]))
		author := strings.TrimSpace(htmlUnescape(m[3]))
		author = regexp.MustCompile(`\s+`).ReplaceAllString(author, " ")

		entry := bookMap[title]
		entry.Author = author
		entry.EAN = extractEANFromHref(href)
		entry.URL = "https://bookshop.org" + htmlUnescape(href)
		bookMap[title] = entry
	}

	// Extract image URLs from srcSet attributes
	type imgEntry struct {
		url   string
		title string
	}
	var imgEntries []imgEntry
	imgBlock := regexp.MustCompile(`<img[^>]*srcSet="([^"]+)"[^>]*alt="bookcover for ([^"]+)"`)
	for _, m := range imgBlock.FindAllStringSubmatch(html, -1) {
		if len(m) < 3 {
			continue
		}
		imgURL := m[1]
		if idx := strings.Index(imgURL, " "); idx > 0 {
			imgURL = imgURL[:idx]
		}
		altTitle := htmlUnescape(strings.TrimSpace(m[2]))
		imgEntries = append(imgEntries, imgEntry{imgURL, altTitle})
	}

	for _, ie := range imgEntries {
		if e, ok := bookMap[ie.title]; ok {
			e.ImageURL = ie.url
			bookMap[ie.title] = e
		}
	}

	// Extract descriptions — they appear after the desktop title block
	descPattern := regexp.MustCompile(`<h1 class="title">([^<]+)</h1>.*?<p class="text-sm lg:text-base">([^<]+)`)
	descMatches := descPattern.FindAllStringSubmatch(html, -1)
	for _, m := range descMatches {
		if len(m) < 3 {
			continue
		}
		title := strings.TrimSpace(htmlUnescape(m[1]))
		desc := strings.TrimSpace(htmlUnescape(m[2]))
		if e, ok := bookMap[title]; ok {
			e.Desc = desc
			bookMap[title] = e
		}
	}

	var result []ScrapedItem
	if pageYear == year && pageWeek == week {
		for _, b := range books {
			info, ok := bookMap[b.Title]
			if !ok {
				continue
			}
			result = append(result, ScrapedItem{
				Title:      b.Title,
				ArtistName: info.Author,
				MediaType:  model.MediaTypeBook,
				Source:     "bookshop",
				ImageURL:   info.ImageURL,
				Notes:      fmt.Sprintf("url=%s|ean=%s", info.URL, info.EAN),
				Overview:   info.Desc,
			})
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("bookshop: no books found for %d-W%02d", year, week)
	}

	log.Info().Int("count", len(result)).Int("year", year).Int("week", week).Msg("bookshop: found books")
	return result, nil
}

func extractEANFromHref(href string) string {
	re := regexp.MustCompile(`ean=(\d{13})`)
	if m := re.FindStringSubmatch(href); len(m) > 1 {
		return m[1]
	}
	return ""
}
