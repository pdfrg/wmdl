package discover

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/model"
)

type FlixPatrolProvider struct {
	http            *http.Client
	targetYear      int
	targetWeek      int
	hasTargetWeek   bool
	mediaTypeFilter model.MediaType
}

var (
	_ ReleaseProvider = (*FlixPatrolProvider)(nil)
	_ WeekSettable    = (*FlixPatrolProvider)(nil)
)

func NewFlixPatrolProvider(_ string) *FlixPatrolProvider {
	return &FlixPatrolProvider{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *FlixPatrolProvider) Name() string {
	return "flixpatrol"
}

func (f *FlixPatrolProvider) SetMediaTypeFilter(mt model.MediaType) {
	f.mediaTypeFilter = mt
}

func (f *FlixPatrolProvider) SetWeekRange(year, week int) {
	f.targetYear = year
	f.targetWeek = week
	f.hasTargetWeek = true
}

type flixItem struct {
	Date           string
	Title          string
	Year           int
	MediaType      model.MediaType
	IMDbRating     float64
	RTCriticsScore float64
	YoutubeView    int64
	hasAnimeGenre  bool
}

var (
	flixDatePat     = regexp.MustCompile(`text-sm sm:text-base">([^<]+)`)
	flixTitlePat    = regexp.MustCompile(`group-hover:underline">\s*([^<]+?)\s*</div>`)
	flixIMDbPat     = regexp.MustCompile(`(\d+\.\d+)/10`)
	flixRTPat       = regexp.MustCompile(`<span>(\d+)%</span>`)
	flixYTViewsPat  = regexp.MustCompile(`title="([\d,]+) views"`)
	flixTVPat       = regexp.MustCompile(`TV Show`)
	flixMoviePat    = regexp.MustCompile(`Movie`)
	flixTitleYear   = regexp.MustCompile(`\((\d{4})\)`)
	flixPremierePat = regexp.MustCompile(`title="Premiere">\s*<div>\s*<span[^>]*>\d{2}/\d{2}/</span>(\d{4})`)
	flixAnimePat    = regexp.MustCompile(`<span>Anime</span>`)
)

func (f *FlixPatrolProvider) Scrape() ([]ScrapedItem, error) {
	var physicalTue time.Time
	if f.hasTargetWeek {
		physicalTue = tuesdayOfISOWeek(f.targetYear, f.targetWeek)
	} else {
		now := time.Now()
		physicalTue = mostRecentTuesday(now)
	}
	streamTue := truncateToDay(physicalTue.AddDate(0, -2, 0))
	streamStart := truncateToDay(streamTue.AddDate(0, 0, -6))

	startStr := streamStart.Format("Jan 2")
	endStr := streamTue.Format("Jan 2")
	log.Info().Msgf("FlixPatrol target: %s – %s", startStr, endStr)

	var allItems []flixItem
pageLoop:
	for page := 1; page <= 30; page++ {
		items, err := f.fetchPage(page, streamStart, streamTue)
		if err != nil {
			log.Warn().Err(err).Msgf("FlixPatrol page %d failed", page)
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
				log.Debug().Msgf("FlixPatrol: found pre-window date %s on page %d, stopping", it.Date, page)
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

	log.Info().Msgf("FlixPatrol: %d in target range", len(filtered))

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
		scraped := ScrapedItem{
			Title:          cleanFlixTitle(item.Title),
			Year:           item.Year,
			MediaType:      item.MediaType,
			ReleaseType:    model.ReleaseStreaming,
			ReleaseDate:    dateStr,
			ImdbRating:     item.IMDbRating,
			RTCriticsScore: item.RTCriticsScore,
			YoutubeViews:   item.YoutubeView,
			Source:         "flixpatrol",
		}
		if item.hasAnimeGenre {
			scraped.Genres = "Anime"
		}
		if f.mediaTypeFilter != "" && scraped.MediaType != f.mediaTypeFilter {
			continue
		}
		results = append(results, scraped)
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
	if flixAnimePat.MatchString(row) {
		item.hasAnimeGenre = true
	}
	if m := flixTitleYear.FindStringSubmatch(item.Title); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
			item.Year = y
		}
	}
	// Fall back to year from premiere date in the row HTML.
	// FlixPatrol provides a full premiere date (MM/DD/YYYY) in a tooltip
	// that is more reliable than defaulting to current year.
	if item.Year == 0 {
		if m := flixPremierePat.FindStringSubmatch(row); len(m) > 1 {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2100 {
				item.Year = y
			}
		}
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
	now := time.Now()
	year := now.Year()
	if now.Month() < t.Month() {
		year--
	}
	return truncateToDay(t.AddDate(year, 0, 0))
}

func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
