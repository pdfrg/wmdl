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

// malAnimeExists checks whether Tenrai (primary) or Jikan (fallback) has an
// anime matching the given title. Returns (true, nil) if found, (false, nil)
// if not found, (false, err) if all backends fail.
func malAnimeExists(ctx context.Context, title string) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()

	// Try Tenrai first
	found, err := searchAPI(ctx, client, "https://api.tenrai.org/v1/anime?q=%s&limit=1", title)
	if err == nil {
		return found, nil
	}

	// Fallback to Jikan
	return searchAPI(ctx, client, "https://api.jikan.moe/v4/anime?q=%s&limit=1", title)
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
