package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// malAnimeExists checks whether Tenrai has an anime matching the given title.
// Returns (true, nil) if found, (false, nil) if not found, (false, err) if
// the backend fails.
//
// Formerly used Jikan as a fallback; the Jikan public API was discontinued
// (brownout September 1, 2026, shutdown October 1, 2026 — MyAnimeList blocks
// its scrapers). Tenrai is the MAL-backed drop-in.
func malAnimeExists(ctx context.Context, title string) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	return searchAPI(ctx, client, "https://api.tenrai.org/v1/anime?q=%s&limit=1", title)
}

func searchAPI(ctx context.Context, client *http.Client, urlTemplate, title string) (bool, error) {
	u := fmt.Sprintf(urlTemplate, url.QueryEscape(title))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "wmdl/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("search returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var result jikanAnimeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return false, err
	}

	return len(result.Data) > 0, nil
}

// jikanAnimeResponse is the anime list response shape shared by the Jikan v4
// API and Tenrai (its drop-in fork).
type jikanAnimeResponse struct {
	Data []jikanAnime `json:"data"`
}

type jikanAnime struct {
	MalID int    `json:"mal_id"`
	Title string `json:"title"`
}

// parseJikanTime parses the date formats used by Jikan/Tenrai aired fields
// (RFC3339 with optional seconds/timezone, or bare date).
func parseJikanTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}
	t, err = time.Parse("2006-01-02T00:00:00+00:00", s)
	if err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
