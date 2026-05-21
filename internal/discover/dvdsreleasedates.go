package discover

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"

	"github.com/pdfrg/wmd/internal/model"
)

type DVDReleaseDates struct{}

func NewDVDReleaseDates() *DVDReleaseDates {
	return &DVDReleaseDates{}
}

func (d *DVDReleaseDates) Name() string {
	return "dvdsreleasedates"
}

func (d *DVDReleaseDates) Scrape() ([]ScrapedItem, error) {
	now := time.Now()
	// Physical releases come out on Tuesdays. On Wednesday we want "this week" = today's
	// or most recent Tuesday. The page has sections with "(this week)", "(last week)", etc.
	targetDate := mostRecentTuesday(now)

	c := colly.NewCollector(
		colly.AllowedDomains("www.dvdsreleasedates.com", "dvdsreleasedates.com"),
	)

	var items []ScrapedItem
	var foundTarget bool

	c.OnHTML("td.reldate", func(e *colly.HTMLElement) {
		text := e.Text
		// Check if this section is "(this week)" or matches our target
		distance := e.ChildText("div.distance")
		foundTarget = strings.Contains(distance, "this week") || strings.Contains(text, targetDate.Format("January 2, 2006"))
	})

	c.OnHTML("td.dvdcell", func(e *colly.HTMLElement) {
		if !foundTarget {
			return
		}

		title := strings.TrimSpace(e.ChildText("a[style*='color:#000']"))
		if title == "" {
			return
		}

		imgAlt := e.ChildAttr("img.movieimg", "alt")
		year := extractYear(imgAlt, title)

		// Determine media type from title patterns
		mediaType := model.MediaTypeMovie
		lower := strings.ToLower(title)
		if strings.Contains(lower, "season") || strings.Contains(lower, "complete") {
			mediaType = model.MediaTypeTV
		}

		items = append(items, ScrapedItem{
			Title:       cleanTitle(title),
			Year:        year,
			ReleaseType: model.ReleasePhysical,
			ReleaseDate: targetDate.Format("2006-01-02"),
			MediaType:   mediaType,
		})
	})

	url := fmt.Sprintf("https://www.dvdsreleasedates.com/releases/")
	if err := c.Visit(url); err != nil {
		return nil, fmt.Errorf("scraping dvdsreleasedates: %w", err)
	}

	return items, nil
}

func mostRecentTuesday(t time.Time) time.Time {
	daysSinceTuesday := int(t.Weekday() - time.Tuesday)
	if daysSinceTuesday < 0 {
		daysSinceTuesday += 7
	}
	return t.AddDate(0, 0, -daysSinceTuesday)
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

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimSuffix(title, " DVD")
	title = strings.TrimSuffix(title, " Blu-ray")
	title = strings.TrimSuffix(title, " 4K")
	return strings.TrimSpace(title)
}
