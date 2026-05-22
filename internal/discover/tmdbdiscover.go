package discover

import (
	"context"
	"fmt"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

type TMDBDiscoverProvider struct {
	client        *TMDBClient
	targetYear    int
	targetWeek    int
	hasTargetWeek bool
}

func NewTMDBDiscoverProvider(client *TMDBClient) *TMDBDiscoverProvider {
	return &TMDBDiscoverProvider{client: client}
}

func (t *TMDBDiscoverProvider) Name() string {
	return "tmdb-discover"
}

func (t *TMDBDiscoverProvider) SetWeekRange(year, week int) {
	t.targetYear = year
	t.targetWeek = week
	t.hasTargetWeek = true
}

func (t *TMDBDiscoverProvider) Scrape() ([]ScrapedItem, error) {
	var physicalTue time.Time

	if t.hasTargetWeek {
		physicalTue = tuesdayOfISOWeek(t.targetYear, t.targetWeek)
	} else {
		now := time.Now()
		physicalTue = mostRecentTuesday(now)
	}
	streamTue := physicalTue.AddDate(0, -2, 0)
	streamStart := streamTue.AddDate(0, 0, -6) // previous Wednesday
	streamEnd := streamTue                     // this Tuesday

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var items []ScrapedItem

	startStr := streamStart.Format("2006-01-02")
	endStr := streamEnd.Format("2006-01-02")

	// Discover movies
	movies, err := t.client.DiscoverStreamingMovies(ctx, startStr, endStr)
	if err != nil {
		return nil, fmt.Errorf("discovering streaming movies: %w", err)
	}
	for _, m := range movies {
		items = append(items, ScrapedItem{
			Title:       m.Title,
			Year:        m.Year,
			MediaType:   model.MediaTypeMovie,
			ReleaseType: model.ReleaseStreaming,
			ReleaseDate: m.Date,
			Source:      "tmdb-discover",
		})
	}

	// Discover TV
	tvshows, err := t.client.DiscoverStreamingTV(ctx, startStr, endStr)
	if err != nil {
		return nil, fmt.Errorf("discovering streaming tv: %w", err)
	}
	for _, t := range tvshows {
		items = append(items, ScrapedItem{
			Title:       t.Title,
			Year:        t.Year,
			MediaType:   model.MediaTypeTV,
			ReleaseType: model.ReleaseStreaming,
			ReleaseDate: t.Date,
			Source:      "tmdb-discover",
		})
	}

	return items, nil
}
