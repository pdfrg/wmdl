package discover

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/rs/zerolog/log"
)

type BookMarksProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
}

var (
	_ ReleaseProvider = (*BookMarksProvider)(nil)
	_ WeekSettable    = (*BookMarksProvider)(nil)
)

func NewBookMarksProvider() *BookMarksProvider {
	return &BookMarksProvider{}
}

func (p *BookMarksProvider) Name() string {
	return "bookmarks"
}

func (p *BookMarksProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

type bmListEntry struct {
	Slug     string
	Title    string
	Author   string
	Genre    string
	Verdict  string
	ImageURL string
}

type bmBookDetail struct {
	ReleaseDate  string // ISO format "2006-01-02"
	ISBN         string
	Publisher    string
	Description  string
	Tags         string
	Verdict      string // Overall: Rave/Positive/Mixed/Pan
	TotalReviews int
}

var (
	bmSlugRe    = regexp.MustCompile(`data-slug="([^"]+)"`)
	bmTitleRe   = regexp.MustCompile(`<div class="latest_book_title[^"]*">\s*([^<]+)`)
	bmAuthorRe  = regexp.MustCompile(`<div class="latest_book_author">\s*([^<]+)`)
	bmGenreRe   = regexp.MustCompile(`<div class="book_genre">\s*([^<]+)`)
	bmVerdictRe = regexp.MustCompile(`<span class="featured_book_review_index\s+(\w+)">\s*(\w+)`)
	bmImgRe     = regexp.MustCompile(`<img[^>]*src="([^"]+)"[^>]*class="latest_book_image"`)

	// Individual book page patterns
	bmDetailDateRe    = regexp.MustCompile(`itemprop="datePublished" content="([^"]+)"`)
	bmDetailISBNRe    = regexp.MustCompile(`bookshop\.org/a/\d+/(\d{13})`)
	bmDetailPubRe     = regexp.MustCompile(`itemprop="publisher"[^>]*>.*?<span itemprop="name">\s*([^<]+)`)
	bmDetailDescRe    = regexp.MustCompile(`<div class="book_manual_description">\s*([^<]+)`)
	bmDetailTagsRe    = regexp.MustCompile(`name="keywords"\s*content="([^"]+)"`)
	bmDetailVerdictRe = regexp.MustCompile(`overall rating of (\w+) based on (\d+)`)
)

func fetchBody(url string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (p *BookMarksProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	// Step 1: fetch the-latest page for the book list
	body, err := fetchBody("https://bookmarks.reviews/the-latest/")
	if err != nil {
		return nil, fmt.Errorf("bookmarks: fetch list: %w", err)
	}
	html := string(body)

	// Parse slugs, titles, authors, genres, verdicts, images
	slugs := bmSlugRe.FindAllStringSubmatch(html, -1)
	titles := bmTitleRe.FindAllStringSubmatch(html, -1)
	authors := bmAuthorRe.FindAllStringSubmatch(html, -1)
	genres := bmGenreRe.FindAllStringSubmatch(html, -1)
	verdicts := bmVerdictRe.FindAllStringSubmatch(html, -1)
	images := bmImgRe.FindAllStringSubmatch(html, -1)

	entryCount := min(len(slugs), len(titles), len(authors), len(genres), len(verdicts), len(images))
	if entryCount == 0 {
		return nil, fmt.Errorf("bookmarks: no entries found on the-latest page")
	}

	entries := make([]bmListEntry, 0, entryCount)
	for i := 0; i < entryCount; i++ {
		slug := strings.TrimSpace(slugs[i][1])
		title := htmlUnescape(strings.TrimSpace(titles[i][1]))
		author := htmlUnescape(strings.TrimSpace(authors[i][1]))
		genre := strings.TrimSpace(genres[i][1])
		verdict := strings.TrimSpace(verdicts[i][1])
		image := strings.TrimSpace(images[i][1])

		if slug == "" || title == "" {
			continue
		}

		entries = append(entries, bmListEntry{
			Slug:     slug,
			Title:    title,
			Author:   author,
			Genre:    genre,
			Verdict:  verdict,
			ImageURL: image,
		})
	}

	log.Debug().Int("entries", len(entries)).Msg("bookmarks: fetched entries from the-latest")

	// Compute target WMDL week boundaries
	targetEnd := tuesdayOfISOWeek(year, week)
	targetStart := targetEnd.AddDate(0, 0, -6)

	// Step 2: fetch individual book pages for details
	type bookResult struct {
		entry  bmListEntry
		detail *bmBookDetail
		err    error
	}

	resultCh := make(chan bookResult, len(entries))
	sem := make(chan struct{}, 3) // 3 concurrent fetches
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, entry := range entries {
		wg.Add(1)
		go func(e bmListEntry) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				resultCh <- bookResult{entry: e, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			detail, err := p.fetchBookDetail(ctx, e.Slug)
			resultCh <- bookResult{entry: e, detail: detail, err: err}
		}(entry)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results, filtering to target week
	var results []ScrapedItem
	for r := range resultCh {
		if r.err != nil {
			log.Warn().Err(r.err).Str("slug", r.entry.Slug).Msg("bookmarks: detail fetch failed, skipping")
			continue
		}
		if r.detail == nil || r.detail.ReleaseDate == "" {
			log.Debug().Str("slug", r.entry.Slug).Msg("bookmarks: no release date, skipping")
			continue
		}

		// Parse release date and check if it falls in the target week
		releaseDate, err := time.Parse("2006-01-02", r.detail.ReleaseDate)
		if err != nil {
			log.Debug().Str("slug", r.entry.Slug).Str("date", r.detail.ReleaseDate).Msg("bookmarks: unparseable date, skipping")
			continue
		}

		if releaseDate.Before(targetStart) || releaseDate.After(targetEnd) {
			continue
		}

		overview := r.detail.Description
		if overview == "" {
			overview = r.entry.Title
		}

		notes := fmt.Sprintf("slug=%s|url=%s", r.entry.Slug, "https://bookmarks.reviews/reviews/"+r.entry.Slug+"/")
		if r.detail.ISBN != "" {
			notes += "|isbn=" + r.detail.ISBN
		}
		if r.detail.Publisher != "" {
			notes += "|publisher=" + r.detail.Publisher
		}
		if r.detail.TotalReviews > 0 {
			notes += fmt.Sprintf("|verdict=%s|total=%d", r.detail.Verdict, r.detail.TotalReviews)
		}

		results = append(results, ScrapedItem{
			Title:        r.entry.Title,
			ArtistName:   r.entry.Author,
			MediaType:    model.MediaTypeBook,
			Source:       "bookmarks",
			ImageURL:     r.entry.ImageURL,
			ImdbRating:   0, // Book Marks doesn't provide numeric ratings
			RatingsCount: 0, // Book Marks doesn't provide numeric counts
			Notes:        notes,
			Overview:     overview,
			Genres:       r.entry.Genre + ", " + r.detail.Tags,
			ReleaseDate:  r.detail.ReleaseDate,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("bookmarks: no books found for %d-W%02d", year, week)
	}

	log.Info().Int("count", len(results)).Int("year", year).Int("week", week).Msg("bookmarks: found books")
	return results, nil
}

func (p *BookMarksProvider) fetchBookDetail(ctx context.Context, slug string) (*bmBookDetail, error) {
	url := "https://bookmarks.reviews/reviews/" + slug + "/"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(body)

	detail := &bmBookDetail{}

	// Release date
	if m := bmDetailDateRe.FindStringSubmatch(html); len(m) > 1 {
		detail.ReleaseDate = m[1]
	}

	// ISBN
	if m := bmDetailISBNRe.FindStringSubmatch(html); len(m) > 1 {
		detail.ISBN = m[1]
	}

	// Publisher
	if m := bmDetailPubRe.FindStringSubmatch(html); len(m) > 1 {
		detail.Publisher = htmlUnescape(strings.TrimSpace(m[1]))
	}

	// Description
	if m := bmDetailDescRe.FindStringSubmatch(html); len(m) > 1 {
		detail.Description = htmlUnescape(strings.TrimSpace(m[1]))
	}

	// Tags from meta keywords
	if m := bmDetailTagsRe.FindStringSubmatch(html); len(m) > 1 {
		detail.Tags = m[1]
		// Remove author name from tags, keep only categories
		if author := extractAuthorFromTags(detail.Tags); author != "" {
			detail.Tags = strings.ReplaceAll(detail.Tags, ","+author, "")
			detail.Tags = strings.ReplaceAll(detail.Tags, author+",", "")
			detail.Tags = strings.ReplaceAll(detail.Tags, author, "")
		}
		detail.Tags = strings.Trim(detail.Tags, ", ")
	}

	// Overall verdict + review count from meta description
	if m := bmDetailVerdictRe.FindStringSubmatch(html); len(m) > 2 {
		detail.Verdict = m[1]
		if n, err := fmt.Sscanf(m[2], "%d", &detail.TotalReviews); err != nil || n != 1 {
			detail.TotalReviews = 0
		}
	}

	return detail, nil
}

// extractAuthorFromTags attempts to find the author name portion from the
// meta keywords string which is formatted as "Fiction,Hottest Books of the Season,Literary,AuthorName".
// It's a best-effort heuristic — the last comma-separated token may be the author.
func extractAuthorFromTags(tags string) string {
	parts := strings.Split(tags, ",")
	if len(parts) <= 1 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	// If it looks like a name (2-4 words, proper case), it might be the author
	if strings.Count(last, " ") >= 1 && strings.Count(last, " ") <= 4 {
		return last
	}
	return ""
}
