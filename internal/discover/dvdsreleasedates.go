package discover

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/pdfrg/wmdl/internal/model"
)

type DVDReleaseDates struct {
	http            *http.Client
	targetYear      int
	targetWeek      int
	hasTargetWeek   bool
	mediaTypeFilter model.MediaType
}

var (
	_ ReleaseProvider = (*DVDReleaseDates)(nil)
	_ WeekSettable    = (*DVDReleaseDates)(nil)
)

func NewDVDReleaseDates() *DVDReleaseDates {
	return &DVDReleaseDates{
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (d *DVDReleaseDates) Name() string {
	return "dvdsreleasedates"
}

func (d *DVDReleaseDates) SetMediaTypeFilter(mt model.MediaType) {
	d.mediaTypeFilter = mt
}

func (d *DVDReleaseDates) SetWeekRange(year, week int) {
	d.targetYear = year
	d.targetWeek = week
	d.hasTargetWeek = true
}

func (d *DVDReleaseDates) Scrape() ([]ScrapedItem, error) {
	now := time.Now()
	targetDate := mostRecentTuesday(now)
	targetStr := targetDate.Format("January 2, 2006")

	var url string
	if d.hasTargetWeek {
		url = d.historicalURL()
	} else {
		url = "https://www.dvdsreleasedates.com/releases/"
	}

	reqCtx, reqCancel := context.WithTimeout(context.Background(), d.http.Timeout)
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching dvdsreleasedates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dvdsreleasedates returned %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing dvdsreleasedates: %w", err)
	}

	var items []ScrapedItem

	if d.hasTargetWeek {
		items = d.scrapeHistorical(doc, reqCtx)
	} else {
		items = d.scrapeCurrent(doc, targetDate, targetStr, reqCtx)
	}

	return items, nil
}

func mostRecentTuesday(t time.Time) time.Time {
	offset := (int(t.Weekday()) - 2 + 7) % 7 // Tuesday = 2
	t2 := t.AddDate(0, 0, -offset)
	return time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, time.UTC)
}

var yearPattern = regexp.MustCompile(`(\d{4})`)

func extractYear(imgAlt, imgSrc, title string) int {
	// Try to get year from image src (e.g., "GOAT-2026.jpg"), then img alt,
	// then title text.
	for _, s := range []string{imgSrc, imgAlt, title} {
		matches := yearPattern.FindStringSubmatch(s)
		if len(matches) > 1 {
			if y, err := strconv.Atoi(matches[1]); err == nil && y >= 1900 && y <= 2100 {
				return y
			}
		}
	}
	return 0
}

func (d *DVDReleaseDates) historicalURL() string {
	// Anchor on the week's Tuesday, not its Monday: the site organizes month
	// pages by Tuesday release dates, and when a month ends on a Monday
	// (e.g. Aug 31, 2026) the Monday and Tuesday fall in different months.
	t := tuesdayOfISOWeek(d.targetYear, d.targetWeek)
	monthNum := int(t.Month())
	monthName := strings.ToLower(t.Month().String())
	return fmt.Sprintf("https://www.dvdsreleasedates.com/releases/%d/%d/new-dvd-releases-%s-%d",
		t.Year(), monthNum, monthName, t.Year())
}

func (d *DVDReleaseDates) scrapeHistorical(doc *goquery.Document, ctx context.Context) []ScrapedItem {
	weekMon := isoWeekToDate(d.targetYear, d.targetWeek)
	weekSun := weekMon.AddDate(0, 0, 6)

	var items []ScrapedItem
	doc.Find("td.reldate").Each(func(_ int, sel *goquery.Selection) {
		dateText := strings.TrimSpace(sel.Text())
		// Strip leading weekday name (e.g. "Tuesday ")
		if idx := strings.Index(dateText, " "); idx >= 0 {
			dateText = dateText[idx+1:]
		}
		// Strip trailing distance text (e.g. "(this week)", "(3 weeks ago)")
		if idx := strings.Index(dateText, "("); idx >= 0 {
			dateText = strings.TrimSpace(dateText[:idx])
		}
		releaseDate, err := time.Parse("January 2, 2006", dateText)
		if err != nil {
			return
		}
		if releaseDate.Before(weekMon) || releaseDate.After(weekSun) {
			return
		}
		dateStr := releaseDate.Format("2006-01-02")
		sel.Closest("table").Find("td.dvdcell").Each(func(_ int, cell *goquery.Selection) {
			item := d.parseDVDCell(cell, dateStr, ctx)
			if item != nil {
				items = append(items, *item)
			}
		})
	})
	return items
}

func (d *DVDReleaseDates) scrapeCurrent(doc *goquery.Document, targetDate time.Time, targetStr string, ctx context.Context) []ScrapedItem {
	var items []ScrapedItem
	doc.Find("td.reldate").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		distance := sel.Find("div.distance").Text()
		if !strings.Contains(distance, "this week") && !strings.Contains(sel.Text(), targetStr) {
			return true
		}
		dateStr := targetDate.Format("2006-01-02")
		sel.Closest("table").Find("td.dvdcell").Each(func(_ int, cell *goquery.Selection) {
			item := d.parseDVDCell(cell, dateStr, ctx)
			if item != nil {
				items = append(items, *item)
			}
		})
		return false
	})
	return items
}

func (d *DVDReleaseDates) parseDVDCell(cell *goquery.Selection, releaseDate string, ctx context.Context) *ScrapedItem {
	link := cell.Find("a[style*='color:#000']")
	title := strings.TrimSpace(link.Text())
	if title == "" {
		return nil
	}

	imgAlt, _ := cell.Find("img.movieimg").Attr("alt")
	imgSrc, _ := cell.Find("img.movieimg").Attr("src")
	year := extractYear(imgAlt, imgSrc, title)

	mediaType := d.detectMediaType(title, cell)

	if d.mediaTypeFilter != "" && mediaType != d.mediaTypeFilter {
		return nil
	}

	// Fetch detail page for accurate production year and resolve uncertain type.
	// The listing image src has the DVD release year (e.g., "Dreams-2026.jpg")
	// which may differ from the production year shown in <h1>Title (YYYY)</h1>.
	if href, ok := link.Attr("href"); ok && href != "" {
		time.Sleep(200 * time.Millisecond)
		detailCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		mt, detYear := d.fetchDetailPage(detailCtx, href)
		if detYear > 0 {
			year = detYear
		}
		if mt != "" {
			mediaType = mt
		}
	}
	if mediaType == "" {
		mediaType = model.MediaTypeMovie // last resort
	}

	imdbID := ""
	var imdbRating float64
	if imdbLink := cell.Find("td.imdblink a[href*='imdb.com']"); imdbLink.Length() > 0 {
		href, _ := imdbLink.Attr("href")
		if parts := strings.Split(href, "/"); len(parts) > 0 {
			for i, p := range parts {
				if strings.HasPrefix(p, "tt") && i > 0 {
					imdbID = p
					break
				}
			}
		}
		if r, err := strconv.ParseFloat(strings.TrimSpace(imdbLink.Text()), 64); err == nil {
			imdbRating = r
		}
	}

	return &ScrapedItem{
		Title:       cleanTitle(title),
		Year:        year,
		MediaType:   mediaType,
		ReleaseType: model.ReleasePhysical,
		ReleaseDate: releaseDate,
		ImdbID:      imdbID,
		ImdbRating:  imdbRating,
		Source:      "dvdsreleasedates",
	}
}

// detectMediaType attempts to determine movie vs TV from the cell's title text
// and rating badge in td.imdblink.right (e.g., "R", "PG-13" for movies,
// or "TV-MA", "TV-14" for TV shows).
func (d *DVDReleaseDates) detectMediaType(title string, cell *goquery.Selection) model.MediaType {
	lower := strings.ToLower(title)

	// Title-based detection
	if strings.Contains(lower, "season") ||
		strings.Contains(lower, "complete") ||
		strings.Contains(lower, "tv series") ||
		strings.Contains(lower, "mini-series") ||
		strings.Contains(lower, "miniseries") {
		return model.MediaTypeTV
	}

	// Rating badge from td.imdblink.right — e.g., "R", "PG-13", "TV-MA"
	ratingBadge := strings.TrimSpace(cell.Find("td.imdblink.right").Text())
	ratingLower := strings.ToLower(ratingBadge)

	// TV-style ratings
	if strings.HasPrefix(ratingLower, "tv-") ||
		strings.HasPrefix(ratingLower, "tv") {
		return model.MediaTypeTV
	}

	// Movie-style ratings (common ones)
	switch ratingLower {
	case "r", "pg-13", "pg", "g", "nc-17", "nr":
		return model.MediaTypeMovie
	}

	return ""
}

// fetchDetailPage fetches the item's detail page to determine movie vs TV and
// extract the actual production year from the <h1> tag in "Title (YYYY)" format.
// The listing page's image src has the DVD release year (may differ from production year).
func (d *DVDReleaseDates) fetchDetailPage(ctx context.Context, href string) (model.MediaType, int) {
	u := "https://www.dvdsreleasedates.com" + href
	if !strings.HasPrefix(href, "/") {
		u = href
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")
	resp, err := d.http.Do(req)
	if err != nil {
		return "", 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", 0
	}

	// Media type from page text
	var mediaType model.MediaType
	text := doc.Text()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "tv series") || strings.Contains(lower, "first air date") {
		mediaType = model.MediaTypeTV
	} else if strings.Contains(lower, "theater date") {
		mediaType = model.MediaTypeMovie
	}

	// Production year from <h1> in "Dreams (2025)" format
	year := 0
	h1Text := doc.Find("h1").First().Text()
	if m := yearPattern.FindStringSubmatch(h1Text); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
			year = y
		}
	}

	return mediaType, year
}

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimSuffix(title, " DVD")
	title = strings.TrimSuffix(title, " Blu-ray")
	title = strings.TrimSuffix(title, " 4K")
	return strings.TrimSpace(title)
}
