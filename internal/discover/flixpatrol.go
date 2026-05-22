package discover

import (
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pdfrg/wmd/internal/model"
)

type FlixPatrolProvider struct {
	http *http.Client
}

func NewFlixPatrolProvider(_ string) *FlixPatrolProvider {
	return &FlixPatrolProvider{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *FlixPatrolProvider) Name() string {
	return "flixpatrol"
}

type flixItem struct {
	Date           string
	Title          string
	Year           int
	MediaType      model.MediaType
	IMDbRating     float64
	RTCriticsScore float64
	YoutubeView    int64
}

var (
	flixDatePat    = regexp.MustCompile(`text-sm sm:text-base">([^<]+)`)
	flixTitlePat   = regexp.MustCompile(`group-hover:underline">\s*([^<]+?)\s*</div>`)
	flixIMDbPat    = regexp.MustCompile(`(\d+\.\d+)/10`)
	flixRTPat      = regexp.MustCompile(`<span>(\d+)%</span>`)
	flixYTViewsPat = regexp.MustCompile(`title="([\d,]+) views"`)
	flixTVPat      = regexp.MustCompile(`TV Show`)
	flixMoviePat   = regexp.MustCompile(`Movie`)
	flixTitleYear  = regexp.MustCompile(`\((\d{4})\)`)
)

func (f *FlixPatrolProvider) Scrape() ([]ScrapedItem, error) {
	now := time.Now()
	physicalTue := mostRecentTuesday(now)
	streamTue := truncateToDay(physicalTue.AddDate(0, -2, 0))
	streamStart := truncateToDay(streamTue.AddDate(0, 0, -6))

	startStr := streamStart.Format("Jan 2")
	endStr := streamTue.Format("Jan 2")
	log.Printf("  FlixPatrol target: %s – %s", startStr, endStr)

	var allItems []flixItem
pageLoop:
	for page := 1; page <= 30; page++ {
		items, err := f.fetchPage(page, streamStart, streamTue)
		if err != nil {
			log.Printf("  FlixPatrol page %d: %v", page, err)
			continue
		}
		if len(items) == 0 {
			break
		}
		allItems = append(allItems, items...)

		// Pages go newest-to-oldest. Once we hit any pre-window date,
		// remaining pages will all be older — stop.
		for _, it := range items {
			d := parseFlixDate(it.Date)
			if !d.IsZero() && d.Before(streamStart) {
				log.Printf("  FlixPatrol: found pre-window date %s on page %d, stopping", it.Date, page)
				break pageLoop
			}
		}
	}

	// Filter to the target date range
	var filtered []flixItem
	for _, item := range allItems {
		d := parseFlixDate(item.Date)
		if !d.IsZero() && (d.Before(streamStart) || d.After(streamTue)) {
			continue
		}
		filtered = append(filtered, item)
	}

	log.Printf("  FlixPatrol: %d in target range", len(filtered))

	var results []ScrapedItem
	for _, item := range filtered {
		if item.IMDbRating == 0 && item.RTCriticsScore == 0 && item.YoutubeView == 0 {
			continue
		}
		d := parseFlixDate(item.Date)
		dateStr := ""
		if !d.IsZero() {
			dateStr = d.Format("2006-01-02")
		}
		results = append(results, ScrapedItem{
			Title:          cleanFlixTitle(item.Title),
			Year:           item.Year,
			MediaType:      item.MediaType,
			ReleaseType:    model.ReleaseStreaming,
			ReleaseDate:    dateStr,
			ImdbRating:     item.IMDbRating,
			RTCriticsScore: item.RTCriticsScore,
			YoutubeViews:   item.YoutubeView,
		})
	}

	return results, nil
}

func (f *FlixPatrolProvider) fetchPage(page int, windowStart, windowEnd time.Time) ([]flixItem, error) {
	url := fmt.Sprintf("https://flixpatrol.com/calendar/new/titles/streaming/right-now/%d/", page)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseFlixRows(string(body)), nil
}

func parseFlixRows(html string) []flixItem {
	var items []flixItem
	rows := strings.Split(html, `<tr class="table-group">`)
	for _, row := range rows[1:] {
		item := parseFlixRow(row)
		if item.Title != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseFlixRow(row string) flixItem {
	var item flixItem

	if m := flixDatePat.FindStringSubmatch(row); len(m) > 1 {
		item.Date = strings.TrimSpace(m[1])
	}
	if m := flixTitlePat.FindStringSubmatch(row); len(m) > 1 {
		item.Title = strings.TrimSpace(m[1])
	}
	if flixTVPat.MatchString(row) {
		item.MediaType = model.MediaTypeTV
	} else if flixMoviePat.MatchString(row) {
		item.MediaType = model.MediaTypeMovie
	}
	if m := flixTitleYear.FindStringSubmatch(item.Title); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
			item.Year = y
		}
	}
	// Fall back to year from date
	if item.Year == 0 {
		item.Year = time.Now().Year()
	}
	if m := flixIMDbPat.FindStringSubmatch(row); len(m) > 1 {
		item.IMDbRating, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := flixRTPat.FindStringSubmatch(row); len(m) > 1 {
		item.RTCriticsScore, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := flixYTViewsPat.FindStringSubmatch(row); len(m) > 1 {
		cleaned := strings.ReplaceAll(m[1], ",", "")
		item.YoutubeView, _ = strconv.ParseInt(cleaned, 10, 64)
	}

	return item
}

func cleanFlixTitle(title string) string {
	title = html.UnescapeString(title)
	title = flixTitleYear.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

func parseFlixDate(dateStr string) time.Time {
	t, err := time.Parse("Jan 2", dateStr)
	if err != nil {
		return time.Time{}
	}
	return truncateToDay(t.AddDate(time.Now().Year(), 0, 0))
}

func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
