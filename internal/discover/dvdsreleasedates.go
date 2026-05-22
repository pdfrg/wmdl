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
	http          *http.Client
	targetYear    int
	targetWeek    int
	hasTargetWeek bool
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
		items = d.scrapeHistorical(doc)
	} else {
		items = d.scrapeCurrent(doc, targetDate, targetStr)
	}

	return items, nil
}

func mostRecentTuesday(t time.Time) time.Time {
	offset := (int(t.Weekday()) - 2 + 7) % 7 // Tuesday = 2
	t2 := t.AddDate(0, 0, -offset)
	return time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, time.UTC)
}

var yearPattern = regexp.MustCompile(`(\d{4})`)

func extractYear(imgAlt, title string) int {
	// Try to get year from IMDB-style title: "GOAT (2026)" or "GOAT DVD Release Date"
	for _, s := range []string{imgAlt, title} {
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
	t := isoWeekToDate(d.targetYear, d.targetWeek)
	monthNum := int(t.Month())
	monthName := strings.ToLower(t.Month().String())
	return fmt.Sprintf("https://www.dvdsreleasedates.com/releases/%d/%d/new-dvd-releases-%s-%d",
		t.Year(), monthNum, monthName, t.Year())
}

func (d *DVDReleaseDates) scrapeHistorical(doc *goquery.Document) []ScrapedItem {
	weekMon := isoWeekToDate(d.targetYear, d.targetWeek)
	weekSun := weekMon.AddDate(0, 0, 6)

	var items []ScrapedItem
	doc.Find("td.reldate").Each(func(_ int, sel *goquery.Selection) {
		dateText := strings.TrimSpace(sel.Text())
		releaseDate, err := time.Parse("January 2, 2006", dateText)
		if err != nil {
			return
		}
		if releaseDate.Before(weekMon) || releaseDate.After(weekSun) {
			return
		}
		dateStr := releaseDate.Format("2006-01-02")
		sel.Closest("table").Find("td.dvdcell").Each(func(_ int, cell *goquery.Selection) {
			item := d.parseDVDCell(cell, dateStr)
			if item != nil {
				items = append(items, *item)
			}
		})
	})
	return items
}

func (d *DVDReleaseDates) scrapeCurrent(doc *goquery.Document, targetDate time.Time, targetStr string) []ScrapedItem {
	var items []ScrapedItem
	doc.Find("td.reldate").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		distance := sel.Find("div.distance").Text()
		if !strings.Contains(distance, "this week") && !strings.Contains(sel.Text(), targetStr) {
			return true
		}
		dateStr := targetDate.Format("2006-01-02")
		sel.Closest("table").Find("td.dvdcell").Each(func(_ int, cell *goquery.Selection) {
			item := d.parseDVDCell(cell, dateStr)
			if item != nil {
				items = append(items, *item)
			}
		})
		return false
	})
	return items
}

func (d *DVDReleaseDates) parseDVDCell(cell *goquery.Selection, releaseDate string) *ScrapedItem {
	title := strings.TrimSpace(cell.Find("a[style*='color:#000']").Text())
	if title == "" {
		return nil
	}

	imgAlt, _ := cell.Find("img.movieimg").Attr("alt")
	year := extractYear(imgAlt, title)

	mediaType := model.MediaTypeMovie
	lower := strings.ToLower(title)
	if strings.Contains(lower, "season") || strings.Contains(lower, "complete") {
		mediaType = model.MediaTypeTV
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

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimSuffix(title, " DVD")
	title = strings.TrimSuffix(title, " Blu-ray")
	title = strings.TrimSuffix(title, " 4K")
	return strings.TrimSpace(title)
}
