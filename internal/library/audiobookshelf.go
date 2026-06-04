package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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

type UploadResult struct {
	LibraryItemID string `json:"libraryItemId"`
}

// UploadBook uploads a book to Audiobookshelf.
// files is a list of absolute paths to the book files.
// Uses the first folder in the configured library.
func (c *AudiobookshelfClient) UploadBook(ctx context.Context, files []string, author, title, series string) (*UploadResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files to upload")
	}

	// Get the first folder for this library
	folders, err := c.GetLibraryFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting library folders: %w", err)
	}
	if len(folders) == 0 {
		return nil, fmt.Errorf("no folders found in library %s", c.libraryID)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// Form fields
	if err := w.WriteField("title", title); err != nil {
		return nil, fmt.Errorf("write title: %w", err)
	}
	if author != "" {
		if err := w.WriteField("author", author); err != nil {
			return nil, fmt.Errorf("write author: %w", err)
		}
	}
	if series != "" {
		if err := w.WriteField("series", series); err != nil {
			return nil, fmt.Errorf("write series: %w", err)
		}
	}
	if err := w.WriteField("library", c.libraryID); err != nil {
		return nil, fmt.Errorf("write library: %w", err)
	}
	if err := w.WriteField("folder", folders[0].ID); err != nil {
		return nil, fmt.Errorf("write folder: %w", err)
	}

	// Attach files
	for i, filePath := range files {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("open file %s: %w", filePath, err)
		}

		fw, err := w.CreateFormFile(fmt.Sprintf("%d", i), filepath.Base(filePath))
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("create form file: %w", err)
		}
		if _, err := io.Copy(fw, f); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("copy file: %w", err)
		}
		_ = f.Close()
	}
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/upload", &buf)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

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
		return nil, fmt.Errorf("audiobookshelf upload: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result UploadResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return &result, nil
}

// MatchItem triggers metadata matching for a library item.
func (c *AudiobookshelfClient) MatchItem(ctx context.Context, itemID, provider string) error {
	payload := map[string]string{"provider": provider}
	if provider == "" {
		payload["provider"] = "google"
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/items/"+itemID+"/match", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("audiobookshelf match: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	return nil
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
