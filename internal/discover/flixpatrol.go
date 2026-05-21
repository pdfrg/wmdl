package discover

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/pdfrg/wmd/internal/model"
)

type FlixPatrolProvider struct {
	debugURL string
}

func NewFlixPatrolProvider(debugURL string) *FlixPatrolProvider {
	return &FlixPatrolProvider{debugURL: debugURL}
}

func (f *FlixPatrolProvider) Name() string {
	return "flixpatrol"
}

type flixItem struct {
	Date        string // "Mar 20"
	Title       string
	Year        int
	MediaType   model.MediaType
	Country     string
	Platform    string
	IMDbRating  float64
	RTScores    float64 // percentage 0-100
	YoutubeView int64
	Ranking     int
}

func (f *FlixPatrolProvider) Scrape() ([]ScrapedItem, error) {
	// Scan pages 15-17 to find the target date window
	// The date range we want is approximately March 13-19
	var allItems []flixItem

	for page := 15; page <= 17; page++ {
		items, err := f.scrapePage(page)
		if err != nil {
			log.Printf("  FlixPatrol page %d: %v", page, err)
			continue
		}
		allItems = append(allItems, items...)
	}

	// Filter by date range: March 13-19
	var filtered []flixItem
	for _, item := range allItems {
		month := parseMonth(item.Date)
		day := parseDay(item.Date)
		if month == 3 && day >= 13 && day <= 19 {
			filtered = append(filtered, item)
		}
	}

	// Keep items that have at least one rating signal present (even low scores).
	// Entries with 0/3 metrics (no IMDb, no RT, no YouTube) are fringe content.
	var keep []flixItem
	for _, item := range filtered {
		if item.IMDbRating > 0 || item.RTScores > 0 || item.YoutubeView > 0 {
			keep = append(keep, item)
		}
	}

	log.Printf("  FlixPatrol: %d total, %d in date range, %d with ratings", len(allItems), len(filtered), len(keep))

	var results []ScrapedItem
	for _, item := range keep {
		releaseDate := fmt.Sprintf("2026-%02d-%02d", 3, parseDay(item.Date))
		results = append(results, ScrapedItem{
			Title:       cleanTitle(item.Title),
			Year:        item.Year,
			MediaType:   item.MediaType,
			ReleaseType: model.ReleaseStreaming,
			ReleaseDate: releaseDate,
		})
	}

	return results, nil
}

func (f *FlixPatrolProvider) scrapePage(page int) ([]flixItem, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), f.debugURL)
	defer allocCancel()

	ct, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel := context.WithTimeout(ct, 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://flixpatrol.com/calendar/new/titles/streaming/right-now/%d/", page)

	var html string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(4*time.Second),
		chromedp.OuterHTML("html", &html),
	); err != nil {
		return nil, fmt.Errorf("loading page: %w", err)
	}

	return parseFlixRows(html), nil
}

var imdbRatingPattern = regexp.MustCompile(`(\d+\.\d+)/10`)
var rtScorePattern = regexp.MustCompile(`(\d+)%`)
var youtubeViewPattern = regexp.MustCompile(`title="(\d+) views"`)

func parseFlixRows(html string) []flixItem {
	var items []flixItem

	rows := strings.Split(html, `<tr class="table-group">`)
	for _, row := range rows[1:] { // Skip first split part (before first row)
		item := parseFlixRow(row)
		if item.Title != "" {
			items = append(items, item)
		}
	}

	return items
}

func parseFlixRow(row string) flixItem {
	var item flixItem

	// Extract date: <div class="text-sm sm:text-base">Mar 20</div>
	item.Date = extractBetween(row, `text-sm sm:text-base">`, `</div>`)

	// Extract title
	item.Title = extractBetween(row, `group-hover:underline">`, `</div>`)
	item.Title = strings.TrimSpace(item.Title)

	// Determine media type
	if strings.Contains(row, `TV Show`) {
		item.MediaType = model.MediaTypeTV
	} else if strings.Contains(row, `Movie`) {
		item.MediaType = model.MediaTypeMovie
	}

	// Extract year from title or date
	item.Year = extractYearFromTitle(item.Title)

	// Extract IMDb rating
	if m := imdbRatingPattern.FindStringSubmatch(row); len(m) > 1 {
		item.IMDbRating, _ = strconv.ParseFloat(m[1], 64)
	}

	// Extract RT score
	if m := rtScorePattern.FindStringSubmatch(row); len(m) > 1 {
		score, _ := strconv.ParseFloat(m[1], 64)
		item.RTScores = score
	}

	// Extract YouTube views
	if m := youtubeViewPattern.FindStringSubmatch(row); len(m) > 1 {
		item.YoutubeView, _ = strconv.ParseInt(m[1], 10, 64)
	}

	return item
}

func extractBetween(s, open, close string) string {
	i := strings.Index(s, open)
	if i == -1 {
		return ""
	}
	start := i + len(open)
	j := strings.Index(s[start:], close)
	if j == -1 {
		return ""
	}
	return strings.TrimSpace(s[start : start+j])
}

var titleYearPattern = regexp.MustCompile(`\((\d{4})\)`)

func extractYearFromTitle(title string) int {
	if m := titleYearPattern.FindStringSubmatch(title); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
			return y
		}
	}
	return 0
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

func parseMonth(date string) int {
	parts := strings.Fields(date)
	if len(parts) < 1 {
		return 0
	}
	m := strings.ToLower(parts[0][:3])
	return monthNames[m]
}

func parseDay(date string) int {
	parts := strings.Fields(date)
	if len(parts) < 2 {
		return 0
	}
	day, _ := strconv.Atoi(parts[1])
	return day
}
