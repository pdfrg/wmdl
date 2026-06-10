package discover

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/model"
)

const (
	goodreadsBlogName     = "goodreads_blog"
	grBlogPostTypeWeekly  = "weekly"
	grBlogPostTypeEditors = "editors"

	grBlogNewsURL     = "https://www.goodreads.com/news?page=%d"
	grBlogMaxPages    = 18
	grBlogHTTPTimeout = 15 * time.Second

	grBlogNotesFmt = "goodreads_blog:%s|url=%s"
)

type GoodreadsBlogProvider struct {
	targetYear int
	targetWeek int
	hasTarget  bool
	client     *http.Client
}

var (
	_ ReleaseProvider = (*GoodreadsBlogProvider)(nil)
	_ WeekSettable    = (*GoodreadsBlogProvider)(nil)

	grBlogMultiSpaceRe = regexp.MustCompile(`\s+`)
)

func NewGoodreadsBlogProvider() *GoodreadsBlogProvider {
	return &GoodreadsBlogProvider{
		client: &http.Client{Timeout: grBlogHTTPTimeout},
	}
}

func (p *GoodreadsBlogProvider) Name() string {
	return goodreadsBlogName
}

func (p *GoodreadsBlogProvider) SetWeekRange(year, week int) {
	p.targetYear = year
	p.targetWeek = week
	p.hasTarget = true
}

func (p *GoodreadsBlogProvider) Scrape() ([]ScrapedItem, error) {
	year, week := p.targetYear, p.targetWeek
	if !p.hasTarget {
		year, week = time.Now().ISOWeek()
	}

	monday := isoWeekToDate(year, week)
	wedStart := monday.AddDate(0, 0, 2)
	tueEnd := tuesdayOfISOWeek(year, week)

	var matched []grBlogPost
	for page := 1; page <= grBlogMaxPages; page++ {
		url := fmt.Sprintf(grBlogNewsURL, page)
		posts, done, err := p.fetchNewsPage(url, wedStart, tueEnd)
		if err != nil {
			log.Warn().Err(err).Int("page", page).Msg("goodreads_blog: news page fetch failed")
			continue
		}
		matched = append(matched, posts...)
		if done {
			break
		}
	}

	if len(matched) == 0 {
		return nil, fmt.Errorf("goodreads_blog: no matching posts found for %d-W%02d", year, week)
	}

	log.Info().Int("posts", len(matched)).Int("year", year).Int("week", week).
		Msg("goodreads_blog: found matching posts")

	var result []ScrapedItem
	for _, post := range matched {
		books, err := p.fetchBlogPost(post.URL)
		if err != nil {
			log.Warn().Err(err).Str("url", post.URL).Msg("goodreads_blog: blog post fetch failed")
			continue
		}
		pt := detectPostType(post.URL)
		for _, b := range books {
			notes := fmt.Sprintf(grBlogNotesFmt, pt, post.URL)
			result = append(result, ScrapedItem{
				Title:      b.Title,
				ArtistName: b.Author,
				MediaType:  model.MediaTypeBook,
				Source:     goodreadsBlogName,
				Notes:      notes,
				ImageURL:   b.ImageURL,
				Overview:   b.Description,
			})
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("goodreads_blog: no books found for %d-W%02d", year, week)
	}

	log.Info().Int("count", len(result)).Int("year", year).Int("week", week).
		Msg("goodreads_blog: found books")

	return result, nil
}

type grBlogPost struct {
	URL  string
	Date time.Time
}

type grBlogBook struct {
	Title       string
	Author      string
	ImageURL    string
	Description string
	BookURL     string
}

func (p *GoodreadsBlogProvider) fetchNewsPage(url string, wedStart, tueEnd time.Time) ([]grBlogPost, bool, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, false, err
	}

	now := time.Now()
	var posts []grBlogPost
	allOlder := true

	doc.Find("div.editorialCard").Each(func(_ int, card *goquery.Selection) {
		dateStr := strings.TrimSpace(card.Find("small.editorialCard__timestamp a").Text())
		postDate, ok := parseNewsDate(dateStr, now)
		if !ok {
			return
		}

		if postDate.Before(wedStart) {
			return
		}
		allOlder = false

		if postDate.After(tueEnd) || postDate.Before(wedStart) {
			return
		}

		title := strings.TrimSpace(card.Find("div.editorialCard__title a").Text())
		if !isRelevantPost(title) {
			return
		}

		href, ok := card.Find("div.editorialCard__title a").Attr("href")
		if !ok {
			return
		}

		posts = append(posts, grBlogPost{URL: href, Date: postDate})
	})

	return posts, allOlder, nil
}

func (p *GoodreadsBlogProvider) fetchBlogPost(url string) ([]grBlogBook, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseBlogPostBooks(string(body))
}

func parseBlogPostBooks(pageHTML string) ([]grBlogBook, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil, err
	}

	tooltips := doc.Find("div.js-dataTooltip.overflow")
	rows := doc.Find("div.bookInfoFullRow")
	n := tooltips.Length()
	if rows.Length() < n {
		n = rows.Length()
	}

	var books []grBlogBook
	for i := 0; i < n; i++ {
		row := rows.Eq(i)
		tt := tooltips.Eq(i)

		title := strings.TrimSpace(row.Find("div.bookTitle > i > a").Text())
		if title == "" {
			continue
		}

		author := strings.TrimSpace(row.Find("div.bookTitle > a").Text())
		author = htmlUnescape(author)
		author = grBlogMultiSpaceRe.ReplaceAllString(author, " ")

		bookURL, _ := row.Find("div.bookTitle > i > a").Attr("href")
		bookURL = normalizeURL(bookURL)

		imgSrc, _ := tt.Find("img.oneAcrossImage").Attr("src")

		desc := strings.TrimSpace(row.Find("div.bookDescription").Text())
		desc = htmlUnescape(desc)

		books = append(books, grBlogBook{
			Title:       htmlUnescape(title),
			Author:      author,
			ImageURL:    imgSrc,
			Description: desc,
			BookURL:     bookURL,
		})
	}

	return books, nil
}

func isRelevantPost(title string) bool {
	return strings.Contains(title, "New Books Recommended by Readers This Week") ||
		strings.Contains(title, "Editors Share Their")
}

func detectPostType(postURL string) string {
	if strings.Contains(postURL, "editors-share-their") ||
		strings.Contains(postURL, "Editors Share Their") {
		return grBlogPostTypeEditors
	}
	return grBlogPostTypeWeekly
}

func parseNewsDate(dateStr string, now time.Time) (time.Time, bool) {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return time.Time{}, false
	}

	last := dateStr[len(dateStr)-1]
	if n, err := strconv.Atoi(dateStr[:len(dateStr)-1]); err == nil {
		switch last {
		case 'h':
			return now.Add(-time.Duration(n) * time.Hour), true
		case 'd':
			return now.Add(-time.Duration(n) * 24 * time.Hour), true
		}
	}

	for _, f := range []string{"Jan 02", "Jan 2"} {
		t, err := time.Parse(f, dateStr)
		if err == nil {
			t = t.AddDate(now.Year(), 0, 0)
			if t.After(now.Add(30 * 24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t, true
		}
	}

	return time.Time{}, false
}

func normalizeURL(url string) string {
	if url == "" {
		return ""
	}
	if strings.HasPrefix(url, "/") {
		return "https://www.goodreads.com" + url
	}
	return url
}
