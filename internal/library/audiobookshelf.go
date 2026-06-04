package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type AudiobookshelfClient struct {
	baseURL   string
	apiKey    string
	libraryID string
	http      *http.Client
}

func NewAudiobookshelfClient(baseURL, apiKey, libraryID string, timeout int) *AudiobookshelfClient {
	if timeout <= 0 {
		timeout = 60
	}
	return &AudiobookshelfClient{
		baseURL:   baseURL,
		apiKey:    apiKey,
		libraryID: libraryID,
		http: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

func (c *AudiobookshelfClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ping", nil)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("audiobookshelf ping: status=%d", resp.StatusCode)
	}
	return nil
}

type ABSLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *AudiobookshelfClient) GetLibraries(ctx context.Context) ([]ABSLibrary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/libraries", nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audiobookshelf: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		Libraries []ABSLibrary `json:"libraries"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return result.Libraries, nil
}

type ABSFolder struct {
	ID       string `json:"id"`
	FullPath string `json:"fullPath"`
}

func (c *AudiobookshelfClient) GetLibraryFolders(ctx context.Context) ([]ABSFolder, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/libraries/"+c.libraryID, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audiobookshelf: status=%d", resp.StatusCode)
	}

	var result struct {
		Library struct {
			Folders []ABSFolder `json:"folders"`
		} `json:"library"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return result.Library.Folders, nil
}

type ABSLibraryItem struct {
	ID    string `json:"id"`
	Media struct {
		Metadata struct {
			Title      string `json:"title"`
			AuthorName string `json:"authorName"`
			ISBN       string `json:"isbn"`
			ASIN       string `json:"asin"`
		} `json:"metadata"`
	} `json:"media"`
}

// GetLibraryItems returns all items in the configured library.
func (c *AudiobookshelfClient) GetLibraryItems(ctx context.Context) ([]ABSLibraryItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/libraries/"+c.libraryID+"/items", nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audiobookshelf items: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		Results []ABSLibraryItem `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return result.Results, nil
}

// TriggerScan triggers a library scan for new files.
func (c *AudiobookshelfClient) TriggerScan(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/libraries/"+c.libraryID+"/scan", nil)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("audiobookshelf scan: status=%d", resp.StatusCode)
	}
	return nil
}
