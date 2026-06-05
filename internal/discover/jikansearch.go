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

// jikanAnimeExists checks whether Jikan's database has an anime matching the given title.
// Returns (true, nil) if found, (false, nil) if not found, (false, err) on API error.
func jikanAnimeExists(ctx context.Context, title string) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()

	u := fmt.Sprintf("https://api.jikan.moe/v4/anime?q=%s&limit=1", url.QueryEscape(title))

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
		return false, fmt.Errorf("jikan search returned %d", resp.StatusCode)
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
