package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type IMDbAPIClient struct {
	http *http.Client
}

type IMDbAPITitle struct {
	ID         string             `json:"id"`
	Rating     *IMDbAPIRating     `json:"rating,omitempty"`
	Metacritic *IMDbAPIMetacritic `json:"metacritic,omitempty"`
}

type IMDbAPIRating struct {
	AggregateRating float64 `json:"aggregateRating"`
	VoteCount       int     `json:"voteCount"`
}

type IMDbAPIMetacritic struct {
	Score       int `json:"score"`
	ReviewCount int `json:"reviewCount"`
}

type IMDbAPIRatings struct {
	ImdbRating      float64
	MetacriticScore float64
}

func NewIMDbAPIClient() *IMDbAPIClient {
	return &IMDbAPIClient{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *IMDbAPIClient) FetchRatings(ctx context.Context, imdbID string) (*IMDbAPIRatings, error) {
	url := fmt.Sprintf("https://api.imdbapi.dev/titles/%s", imdbID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching IMDb data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IMDbAPI returned %d for %s", resp.StatusCode, imdbID)
	}

	var title IMDbAPITitle
	if err := json.NewDecoder(resp.Body).Decode(&title); err != nil {
		return nil, fmt.Errorf("decoding IMDbAPI response: %w", err)
	}

	result := &IMDbAPIRatings{}
	if title.Rating != nil {
		result.ImdbRating = title.Rating.AggregateRating
	}
	if title.Metacritic != nil {
		result.MetacriticScore = float64(title.Metacritic.Score)
	}

	return result, nil
}
