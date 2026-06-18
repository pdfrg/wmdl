package discover

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/rs/zerolog/log"
)

type BookshopProvider struct {
	debugURL string
	allocCtx context.Context

	targetYear int
	targetWeek int
	hasTarget  bool
}

var (
	_ ReleaseProvider = (*BookshopProvider)(nil)
	_ WeekSettable    = (*BookshopProvider)(nil)
)

func NewBookshopProvider(debugURL string, allocCtx context.Context) *BookshopProvider {
	return &BookshopProvider{
		debugURL: debugURL,
		allocCtx: allocCtx,
	}
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
	whitespaceRe      = regexp.MustCompile(`\s+`)
)

func (p *BookshopProvider) Scrape() ([]ScrapedItem, error) {
	pageURL := "https://bookshop.org/lists/new-books"

	html, err := p.fetchPage(pageURL)
	if err != nil {
		return nil, fmt.Errorf("bookshop: %w", err)
	}

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

	// Extract image URLs: parse full <img> tag (multi-line safe), then extract
	// srcSet/src and alt attributes independently (order-independent).
	type imgEntry struct {
		url   string
		title string
	}
	var imgEntries []imgEntry
	imgTagRe := regexp.MustCompile(`(?s)<img\s[^>]+>`)
	srcSetRe := regexp.MustCompile(`src[Ss]et="([^"]+)"`)
	altRe := regexp.MustCompile(`alt="bookcover\s+for\s+([^"]+)"`)
	srcRe := regexp.MustCompile(`src="([^"]+)"`)

	for _, tag := range imgTagRe.FindAllString(html, -1) {
		altMatch := altRe.FindStringSubmatch(tag)
		if altMatch == nil {
			continue
		}

		var imgURL string
		if m := srcSetRe.FindStringSubmatch(tag); m != nil {
			// srcSet format: "url1 100w, url2 500w" — take the LAST (largest)
			parts := strings.Split(m[1], ",")
			lastURL := strings.TrimSpace(parts[len(parts)-1])
			if idx := strings.Index(lastURL, " "); idx > 0 {
				imgURL = lastURL[:idx]
			} else {
				imgURL = lastURL
			}
		} else if m := srcRe.FindStringSubmatch(tag); m != nil {
			imgURL = m[1]
		}
		if imgURL == "" {
			continue
		}

		altTitle := htmlUnescape(strings.TrimSpace(altMatch[1]))
		altTitle = whitespaceRe.ReplaceAllString(altTitle, " ")
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

	releaseDateStrFormatted := releaseDate.Format("January 2, 2006")
	var result []ScrapedItem
	for _, b := range books {
		info, ok := bookMap[b.Title]
		if !ok {
			continue
		}
		result = append(result, ScrapedItem{
			Title:       b.Title,
			ArtistName:  info.Author,
			MediaType:   model.MediaTypeBook,
			Source:      "bookshop",
			ImageURL:    info.ImageURL,
			Notes:       fmt.Sprintf("url=%s|ean=%s", info.URL, info.EAN),
			Overview:    info.Desc,
			ReleaseDate: releaseDateStrFormatted,
		})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("bookshop: no books found on page")
	}

	log.Info().Int("count", len(result)).Str("release_date", releaseDateStrFormatted).Msg("bookshop: found books")
	return result, nil
}

func (p *BookshopProvider) fetchPage(url string) (string, error) {
	if p.allocCtx == nil {
		return "", fmt.Errorf("chromedp allocator not available")
	}

	ct, cancel := chromedp.NewContext(p.allocCtx)
	defer cancel()

	pageCtx, pageCancel := context.WithTimeout(ct, 90*time.Second)
	defer pageCancel()

	log.Info().Str("provider", "bookshop").Msg("navigating to new books page")
	if err := chromedp.Run(pageCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	); err != nil {
		return "", fmt.Errorf("navigating to %s: %w", url, err)
	}

	// Wait for Cloudflare challenge to pass (title will be "Just a moment..." initially)
	if err := waitForBookshopPage(ct); err != nil {
		return "", fmt.Errorf("page load: %w", err)
	}

	var html string
	if err := chromedp.Run(ct, chromedp.OuterHTML("html", &html)); err != nil {
		return "", fmt.Errorf("getting page HTML: %w", err)
	}

	return html, nil
}

// waitForBookshopPage polls the page title until the real page loads
// (Cloudflare challenge passes). Times out after 90 seconds.
func waitForBookshopPage(ct context.Context) error {
	waitCtx, waitCancel := context.WithTimeout(ct, 90*time.Second)
	defer waitCancel()

	var title string
	for i := 0; i < 45; i++ {
		if err := chromedp.Run(waitCtx, chromedp.Title(&title)); err != nil {
			return err
		}

		if !strings.Contains(title, "Just a moment") &&
			!strings.Contains(title, "503") &&
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

func extractEANFromHref(href string) string {
	re := regexp.MustCompile(`ean=(\d{13})`)
	if m := re.FindStringSubmatch(href); len(m) > 1 {
		return m[1]
	}
	return ""
}
