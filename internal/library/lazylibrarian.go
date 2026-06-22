package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

type LazyLibrarianClient struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	booksCache []model.BookStatus // in-memory cache from pre-warm
}

func NewLazyLibrarianClient(baseURL, apiKey string, timeout int) *LazyLibrarianClient {
	if timeout <= 0 {
		timeout = 120
	}
	return &LazyLibrarianClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

var _ BookClient = (*LazyLibrarianClient)(nil)

func (c *LazyLibrarianClient) retry(ctx context.Context, fn func() error) error {
	var err error
	delays := []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}
	for i := 0; i <= len(delays); i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[i-1]):
			}
		}
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}

func (c *LazyLibrarianClient) Ping(ctx context.Context) error {
	return c.retry(ctx, func() error {
		return c.doOK(ctx, url.Values{"cmd": {"getVersion"}})
	})
}

func (c *LazyLibrarianClient) AddAuthor(ctx context.Context, authorID string, fetchBooks bool) (*model.AuthorResult, error) {
	params := url.Values{
		"cmd": {"addAuthorID"},
		"id":  {authorID},
	}
	if fetchBooks {
		params.Set("books", "true")
	}
	if err := c.retry(ctx, func() error {
		return c.doOK(ctx, params)
	}); err != nil {
		return nil, err
	}
	return &model.AuthorResult{AuthorID: authorID}, nil
}

func (c *LazyLibrarianClient) AddBook(ctx context.Context, bookID string) (*model.BookResult, error) {
	params := url.Values{
		"cmd": {"addBook"},
		"id":  {bookID},
	}
	if err := c.retry(ctx, func() error {
		return c.doOK(ctx, params)
	}); err != nil {
		return nil, err
	}
	return &model.BookResult{BookID: bookID}, nil
}

func (c *LazyLibrarianClient) formatParam(f model.BookFormat) string {
	switch f {
	case model.BookFormatAudiobook:
		return "AudioBook"
	default:
		return "eBook"
	}
}

func (c *LazyLibrarianClient) QueueBook(ctx context.Context, bookID string, format model.BookFormat) error {
	params := url.Values{
		"cmd":  {"queueBook"},
		"id":   {bookID},
		"type": {c.formatParam(format)},
	}
	return c.retry(ctx, func() error {
		return c.doOK(ctx, params)
	})
}

func (c *LazyLibrarianClient) UnqueueBook(ctx context.Context, bookID string, format model.BookFormat) error {
	params := url.Values{
		"cmd":  {"unqueueBook"},
		"id":   {bookID},
		"type": {c.formatParam(format)},
	}
	return c.retry(ctx, func() error {
		return c.doOK(ctx, params)
	})
}

func (c *LazyLibrarianClient) GetBookStatus(ctx context.Context, bookID string) (*model.BookStatus, error) {
	// Use in-memory cache if available
	if c.booksCache != nil {
		for _, b := range c.booksCache {
			if b.BookID == bookID {
				return &b, nil
			}
		}
		return nil, fmt.Errorf("lazylibrarian: book %s not found in cache", bookID)
	}
	params := url.Values{
		"cmd": {"getAllBooks"},
	}
	var books []llBook
	if err := c.retry(ctx, func() error {
		return c.doJSON(ctx, params, &books)
	}); err != nil {
		return nil, err
	}
	for _, b := range books {
		if b.BookID == bookID {
			return &model.BookStatus{
				BookID:      b.BookID,
				Title:       b.Title,
				Status:      b.Status,
				AudioStatus: b.AudioStatus,
				BookFile:    b.BookFile,
				AudioFile:   b.AudioFile,
				Isbn:        b.BookIsbn,
			}, nil
		}
	}
	return nil, fmt.Errorf("lazylibrarian: book %s not found", bookID)
}

func (c *LazyLibrarianClient) SetAllBooks(books []model.BookStatus) {
	c.booksCache = books
}

func (c *LazyLibrarianClient) GetAllBooks(ctx context.Context) ([]model.BookStatus, error) {
	if c.booksCache != nil {
		return c.booksCache, nil
	}
	params := url.Values{
		"cmd": {"getAllBooks"},
	}
	var books []llBook
	if err := c.retry(ctx, func() error {
		return c.doJSON(ctx, params, &books)
	}); err != nil {
		return nil, err
	}
	result := make([]model.BookStatus, len(books))
	for i, b := range books {
		result[i] = model.BookStatus{
			BookID:      b.BookID,
			Title:       b.Title,
			Status:      b.Status,
			AudioStatus: b.AudioStatus,
			BookFile:    b.BookFile,
			AudioFile:   b.AudioFile,
			Isbn:        b.BookIsbn,
		}
	}
	return result, nil
}

func (c *LazyLibrarianClient) GetSeriesMembers(ctx context.Context, seriesID string) ([]*model.SeriesMember, error) {
	params := url.Values{
		"cmd":    {"getSeriesMembers"},
		"series": {seriesID},
	}
	var raw []llSeriesMember
	if err := c.retry(ctx, func() error {
		return c.doJSON(ctx, params, &raw)
	}); err != nil {
		return nil, err
	}
	members := make([]*model.SeriesMember, 0, len(raw))
	for _, m := range raw {
		members = append(members, &model.SeriesMember{
			Position:   m.Position,
			Title:      m.Title,
			AuthorName: m.AuthorName,
			AuthorID:   m.AuthorID,
			BookID:     m.BookID,
			PubDate:    m.PubDate,
		})
	}
	return members, nil
}

func (c *LazyLibrarianClient) ImportAlternate(ctx context.Context, dir string, format model.BookFormat) error {
	params := url.Values{
		"cmd":     {"importAlternate"},
		"library": {c.formatParam(format)},
	}
	if dir != "" {
		params.Set("dir", dir)
	}
	return c.retry(ctx, func() error {
		return c.doOK(ctx, params)
	})
}

func (c *LazyLibrarianClient) doOK(ctx context.Context, params url.Values) error {
	resp, err := c.get(ctx, params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("lazylibrarian: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lazylibrarian: %s returned %d: %s", params.Get("cmd"), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *LazyLibrarianClient) doJSON(ctx context.Context, params url.Values, dst interface{}) error {
	resp, err := c.get(ctx, params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("lazylibrarian: %s returned %d: reading body: %w", params.Get("cmd"), resp.StatusCode, err)
		}
		return fmt.Errorf("lazylibrarian: %s returned %d: %s", params.Get("cmd"), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("lazylibrarian: parsing %s response: %w", params.Get("cmd"), err)
	}
	return nil
}

func (c *LazyLibrarianClient) get(ctx context.Context, params url.Values) (*http.Response, error) {
	params.Set("apikey", c.apiKey)
	u := c.baseURL + "/api?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("lazylibrarian: creating request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lazylibrarian: %s request: %w", params.Get("cmd"), err)
	}
	return resp, nil
}

type llBook struct {
	BookID      string `json:"bookid"`
	Title       string `json:"bookname"`
	Status      string `json:"status"`
	AudioStatus string `json:"audiostatus"`
	BookFile    string `json:"bookfile"`
	AudioFile   string `json:"audiofile"`
	BookIsbn    string `json:"bookisbn"`
}

type llSeriesMember struct {
	Position   int    `json:"position"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	AuthorID   string `json:"author_id"`
	BookID     string `json:"book_id"`
	PubDate    string `json:"pubdate"`
}
