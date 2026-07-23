package discover

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

type TMDBClient struct {
	apiKey      string
	accessToken string
	http        *http.Client
	limiter     *rate.Limiter
}

const tmdbBase = "https://api.themoviedb.org/3"

func NewTMDBClient(apiKey, accessToken string) *TMDBClient {
	return &TMDBClient{
		apiKey:      apiKey,
		accessToken: accessToken,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		limiter: rate.NewLimiter(rate.Limit(8), 1),
	}
}

func (c *TMDBClient) Ping(ctx context.Context) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tmdbBase+"/configuration", nil)
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb unreachable: %w", err)
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("tmdb: API key rejected (HTTP 401) — check tmdb.api_key or tmdb.access_token in config")
	default:
		return fmt.Errorf("tmdb: unexpected HTTP %d", resp.StatusCode)
	}
}

func (c *TMDBClient) setAuth(req *http.Request) {
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	} else if c.apiKey != "" {
		q := req.URL.Query()
		q.Set("api_key", c.apiKey)
		req.URL.RawQuery = q.Encode()
	}
}
