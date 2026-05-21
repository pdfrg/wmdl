package discover

import (
	"context"
	"fmt"
	"time"

	"github.com/mds/wmd/internal/model"
)

type TMDBStreamingProvider struct {
	client *TMDBClient
}

func NewTMDBStreamingProvider(client *TMDBClient) *TMDBStreamingProvider {
	return &TMDBStreamingProvider{client: client}
}

func (t *TMDBStreamingProvider) Name() string {
	return "tmdb-streaming"
}

func (t *TMDBStreamingProvider) Scrape() ([]ScrapedItem, error) {
	now := time.Now()
	twoMonthsAgo := now.AddDate(0, -2, 0)

	// Find the Tuesday that starts the target week (two months back)
	streamStart := mostRecentTuesday(twoMonthsAgo)
	// Go back one more week to handle month boundaries
	if streamStart.After(twoMonthsAgo) {
		streamStart = streamStart.AddDate(0, 0, -7)
	}
	streamEnd := streamStart.AddDate(0, 0, 6)

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
		})
	}

	return items, nil
}
