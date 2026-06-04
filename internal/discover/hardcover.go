package discover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HardcoverClient struct {
	apiKey string
	http   *http.Client
}

type HCBookResult struct {
	ID          int
	Title       string
	Subtitle    string
	Description string

	ISBN10       string
	ISBN13       string
	ASIN         string
	Pages        int
	AudioSeconds int
	ReleaseDate  string
	ReleaseYear  int

	Rating       float64
	RatingsCount int
	UsersCount   int

	ImageURL     string
	Language     string
	Publisher    string
	Tags         []string
	LiteraryType string // "fiction" or "nonfiction"

	Author *HCAuthorResult
	OLID   string // Open Library ID
}

type HCAuthorResult struct {
	ID        int
	Name      string
	Bio       string
	BornDate  string
	DeathDate string
	ImageURL  string
	OLID      string
}

func NewHardcoverClient(apiKey string) *HardcoverClient {
	// Accept either "Bearer xxx" or bare token from config
	token := strings.TrimPrefix(apiKey, "Bearer ")
	token = strings.TrimSpace(token)
	return &HardcoverClient{
		apiKey: token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

const hardcoverAPI = "https://api.hardcover.app/v1/graphql"

// Hardcover API rate limit: 60 requests/minute.
var hcLimiter = time.NewTicker(time.Second)

func (c *HardcoverClient) query(ctx context.Context, query string, vars map[string]any) ([]byte, error) {
	body := map[string]any{"query": query, "variables": vars}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}

		// Wait for rate limiter
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-hcLimiter.C:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hardcoverAPI, bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "wmdl/1.0")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("hardcover api: status=%d body=%s", resp.StatusCode, string(respBody))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("hardcover api: status=%d body=%s", resp.StatusCode, string(respBody))
		}
		return respBody, nil
	}
	return nil, lastErr
}

func (c *HardcoverClient) GetBook(ctx context.Context, hcID int) (*HCBookResult, error) {
	q := `query GetBook($id: Int!) {
		books(where: {id: {_eq: $id}}, limit: 1) {
			id title subtitle description
			pages audio_seconds release_date release_year
			rating ratings_count users_count
			image { url }
			default_physical_edition { isbn_13 isbn_10 asin publisher { name } }
			default_ebook_edition { isbn_13 isbn_10 asin publisher { name } }
			default_audio_edition { isbn_13 isbn_10 asin publisher { name } }
			literary_type_id
			cached_tags
			contributions {
				author { id name bio born_date death_date image { url } identifiers }
			}
		}
	}`
	return c.searchBooks(ctx, q, map[string]any{"id": hcID})
}

func (c *HardcoverClient) SearchBook(ctx context.Context, title, author string) (*HCBookResult, error) {
	// Use the Typesense search endpoint (text operators like _ilike are disabled on this server).
	searchQuery := `query SearchBook($query: String!) {
		search(query: $query, query_type: "Book", per_page: 5) {
			results
		}
	}`

	q := strings.TrimSpace(title + " " + author)

	respBody, err := c.query(ctx, searchQuery, map[string]any{"query": q})
	if err != nil {
		return nil, err
	}

	var searchResp struct {
		Data struct {
			Search struct {
				Results json.RawMessage `json:"results"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, fmt.Errorf("unmarshal search: %w", err)
	}

	if len(searchResp.Data.Search.Results) == 0 {
		return nil, nil
	}

	var sr struct {
		Hits []struct {
			Document struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(searchResp.Data.Search.Results, &sr); err != nil {
		return nil, fmt.Errorf("unmarshal results: %w", err)
	}

	if len(sr.Hits) == 0 {
		return nil, nil
	}

	firstDoc := sr.Hits[0].Document
	if firstDoc.ID == "" {
		return nil, nil
	}

	if title != "" && !strings.Contains(strings.ToLower(firstDoc.Title), strings.ToLower(title)) {
		return nil, nil
	}

	id, err := strconv.Atoi(firstDoc.ID)
	if err != nil {
		return nil, fmt.Errorf("parse hc id: %w", err)
	}

	return c.GetBook(ctx, id)
}

func (c *HardcoverClient) searchBooks(ctx context.Context, query string, vars map[string]any) (*HCBookResult, error) {
	respBody, err := c.query(ctx, query, vars)
	if err != nil {
		return nil, err
	}

	var gqlResp struct {
		Data struct {
			Books []json.RawMessage `json:"books"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if len(gqlResp.Data.Books) == 0 {
		return nil, nil
	}

	return c.decodeBook(gqlResp.Data.Books[0])
}

func (c *HardcoverClient) decodeBook(raw json.RawMessage) (*HCBookResult, error) {
	var rawBook struct {
		ID             int             `json:"id"`
		Title          string          `json:"title"`
		Subtitle       string          `json:"subtitle"`
		Description    string          `json:"description"`
		Pages          *int            `json:"pages"`
		AudioSeconds   *int            `json:"audio_seconds"`
		ReleaseDate    string          `json:"release_date"`
		ReleaseYear    *int            `json:"release_year"`
		Rating         float64         `json:"rating"`
		RatingsCount   int             `json:"ratings_count"`
		UsersCount     int             `json:"users_count"`
		LiteraryTypeID *int            `json:"literary_type_id"`
		CachedTags     json.RawMessage `json:"cached_tags"`

		Image    json.RawMessage `json:"image"`
		Language json.RawMessage `json:"language"`

		DefaultPhysicalEdition json.RawMessage `json:"default_physical_edition"`
		DefaultEbookEdition    json.RawMessage `json:"default_ebook_edition"`
		DefaultAudioEdition    json.RawMessage `json:"default_audio_edition"`

		BookMappings  []json.RawMessage `json:"book_mappings"`
		Contributions []json.RawMessage `json:"contributions"`
	}
	if err := json.Unmarshal(raw, &rawBook); err != nil {
		return nil, fmt.Errorf("decode book: %w", err)
	}

	res := &HCBookResult{
		ID:           rawBook.ID,
		Title:        rawBook.Title,
		Subtitle:     rawBook.Subtitle,
		Description:  rawBook.Description,
		Rating:       rawBook.Rating,
		RatingsCount: rawBook.RatingsCount,
		UsersCount:   rawBook.UsersCount,
	}

	if rawBook.CachedTags != nil {
		var tagMap map[string][]struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal(rawBook.CachedTags, &tagMap) == nil {
			for _, entries := range tagMap {
				for _, entry := range entries {
					if entry.Tag != "" {
						res.Tags = append(res.Tags, entry.Tag)
					}
				}
			}
		}
	}

	if rawBook.Pages != nil {
		res.Pages = *rawBook.Pages
	}
	if rawBook.AudioSeconds != nil {
		res.AudioSeconds = *rawBook.AudioSeconds
	}
	if rawBook.ReleaseYear != nil {
		res.ReleaseYear = *rawBook.ReleaseYear
	}
	res.ReleaseDate = rawBook.ReleaseDate

	if rawBook.LiteraryTypeID != nil {
		switch *rawBook.LiteraryTypeID {
		case 1:
			res.LiteraryType = "fiction"
		case 2:
			res.LiteraryType = "nonfiction"
		}
	}

	if rawBook.Image != nil {
		var img struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(rawBook.Image, &img) == nil {
			res.ImageURL = img.URL
		}
	}

	if rawBook.Language != nil {
		var lang struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(rawBook.Language, &lang) == nil {
			res.Language = lang.Language
		}
	}

	// Extract ISBN/ASIN from editions (prefer ebook, then physical, then audio)
	editions := []json.RawMessage{
		rawBook.DefaultEbookEdition,
		rawBook.DefaultPhysicalEdition,
		rawBook.DefaultAudioEdition,
	}
	for _, ed := range editions {
		if ed == nil {
			continue
		}
		var edition struct {
			ISBN13    string `json:"isbn_13"`
			ISBN10    string `json:"isbn_10"`
			ASIN      string `json:"asin"`
			Publisher struct {
				Name string `json:"name"`
			} `json:"publisher"`
		}
		if json.Unmarshal(ed, &edition) != nil {
			continue
		}
		if res.ISBN13 == "" {
			res.ISBN13 = edition.ISBN13
		}
		if res.ISBN10 == "" {
			res.ISBN10 = edition.ISBN10
		}
		if res.ASIN == "" {
			res.ASIN = edition.ASIN
		}
		if res.Publisher == "" {
			res.Publisher = edition.Publisher.Name
		}
	}

	// Extract external mappings (OLID, etc.)
	for _, m := range rawBook.BookMappings {
		var mapping struct {
			Source     string `json:"source"`
			ExternalID string `json:"external_id"`
		}
		if json.Unmarshal(m, &mapping) != nil {
			continue
		}
		if mapping.Source == "openlibrary" && res.OLID == "" {
			res.OLID = mapping.ExternalID
		}
	}

	// Extract author info
	for _, c := range rawBook.Contributions {
		var contrib struct {
			Author json.RawMessage `json:"author"`
		}
		if json.Unmarshal(c, &contrib) != nil {
			continue
		}
		var author struct {
			ID          int             `json:"id"`
			Name        string          `json:"name"`
			Bio         string          `json:"bio"`
			BornDate    string          `json:"born_date"`
			DeathDate   string          `json:"death_date"`
			Image       json.RawMessage `json:"image"`
			Identifiers json.RawMessage `json:"identifiers"`
		}
		if json.Unmarshal(contrib.Author, &author) != nil {
			continue
		}
		ha := &HCAuthorResult{
			ID:        author.ID,
			Name:      author.Name,
			Bio:       author.Bio,
			BornDate:  author.BornDate,
			DeathDate: author.DeathDate,
		}
		if author.Image != nil {
			var img struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(author.Image, &img) == nil {
				ha.ImageURL = img.URL
			}
		}
		if author.Identifiers != nil {
			var ids struct {
				OpenLibrary []string `json:"openlibrary"`
			}
			if json.Unmarshal(author.Identifiers, &ids) == nil && len(ids.OpenLibrary) > 0 {
				ha.OLID = ids.OpenLibrary[0]
			}
		}
		res.Author = ha
		break // only primary author
	}

	return res, nil
}

func (c *HardcoverClient) SearchAuthor(ctx context.Context, name string) (*HCAuthorResult, error) {
	q := `query SearchAuthor($name: String!) {
		authors(where: {name: {_eq: $name}}, limit: 1) {
			id name bio born_date death_date
			image { url }
			identifiers
		}
	}`
	respBody, err := c.query(ctx, q, map[string]any{"name": name})
	if err != nil {
		return nil, err
	}

	var gqlResp struct {
		Data struct {
			Authors []json.RawMessage `json:"authors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if len(gqlResp.Data.Authors) == 0 {
		return nil, nil
	}

	var rawAuthor struct {
		ID          int             `json:"id"`
		Name        string          `json:"name"`
		Bio         string          `json:"bio"`
		BornDate    string          `json:"born_date"`
		DeathDate   string          `json:"death_date"`
		Image       json.RawMessage `json:"image"`
		Identifiers json.RawMessage `json:"identifiers"`
	}
	if err := json.Unmarshal(gqlResp.Data.Authors[0], &rawAuthor); err != nil {
		return nil, fmt.Errorf("decode author: %w", err)
	}

	res := &HCAuthorResult{
		ID:        rawAuthor.ID,
		Name:      rawAuthor.Name,
		Bio:       rawAuthor.Bio,
		BornDate:  rawAuthor.BornDate,
		DeathDate: rawAuthor.DeathDate,
	}

	if rawAuthor.Image != nil {
		var img struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(rawAuthor.Image, &img) == nil {
			res.ImageURL = img.URL
		}
	}

	if rawAuthor.Identifiers != nil {
		var ids struct {
			OpenLibrary []string `json:"openlibrary"`
		}
		if json.Unmarshal(rawAuthor.Identifiers, &ids) == nil && len(ids.OpenLibrary) > 0 {
			res.OLID = ids.OpenLibrary[0]
		}
	}

	return res, nil
}
