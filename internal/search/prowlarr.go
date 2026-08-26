package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pdfrg/wmdl/internal/quality"
)

type ProwlarrClient struct {
	baseURL           string
	apiKey            string
	http              *http.Client
	mu                sync.Mutex
	indexerNames      map[int]string
	indexerByCategory map[string][]int
}

type indexerInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func NewProwlarrClient(baseURL, apiKey string, timeoutSec int, indexerByCategory map[string][]int) *ProwlarrClient {
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	return &ProwlarrClient{
		baseURL:           baseURL,
		apiKey:            apiKey,
		http:              &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		indexerByCategory: indexerByCategory,
	}
}

type prowlarrRelease struct {
	Title       string `json:"title"`
	Guid        string `json:"guid"`
	IndexerID   int    `json:"indexerId"`
	Indexer     string `json:"indexer"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
	InfoHash    string `json:"infoHash"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	Size        int64  `json:"size"`
	Protocol    string `json:"protocol"`
	PublishDate string `json:"publishDate"`
	Categories  []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"categories"`
}

// Newznab category IDs for narrowing search results.
const (
	CatMovie          = 2000 // Movies (parent)
	CatTV             = 5000 // TV (parent)
	CatMusic          = 3000 // Music (parent)
	CatAudioAudiobook = 3030 // Audio/Audiobook
	CatAnime          = 5070 // TV/Anime (Newznab standard)
	CatBook           = 7000 // Books (parent)
	CatBookMags       = 7010 // Books/Magazines
	CatBookEbook      = 7020 // Books/E-Books
	CatBookComic      = 7030 // Books/Comics
)

func categoryKey(cat int) string {
	switch cat {
	case CatMovie, CatTV:
		return "videos"
	case CatAnime:
		return "anime"
	case CatMusic:
		return "music"
	case CatBook, CatBookMags, CatBookEbook, CatBookComic:
		return "ebooks"
	case CatAudioAudiobook:
		return "audiobooks"
	}
	return ""
}

// PreferredIndexerIDs returns the configured preferred Prowlarr indexer IDs
// for a given category, in descending order of preference. An empty list
// means "search all indexers".
func (p *ProwlarrClient) PreferredIndexerIDs(category int) []int {
	if p.indexerByCategory == nil {
		return nil
	}
	return p.indexerByCategory[categoryKey(category)]
}

func (p *ProwlarrClient) SearchAnime(ctx context.Context, query string) ([]quality.ParsedRelease, error) {
	return p.Search(ctx, SearchParams{
		Query:      query,
		Type:       "tvsearch",
		Limit:      50,
		Categories: []int{CatAnime},
	})
}

type SearchParams struct {
	Query      string
	Type       string // "search", "movie", "tvsearch"
	IndexerIDs []int  // preferred indexer IDs to restrict the search to (empty = all)
	Limit      int
	Categories []int // Newznab category IDs to restrict search to
}

func (p *ProwlarrClient) Search(ctx context.Context, params SearchParams) ([]quality.ParsedRelease, error) {
	u, err := url.Parse(p.baseURL + "/api/v1/search")
	if err != nil {
		return nil, fmt.Errorf("parsing prowlarr url: %w", err)
	}
	q := u.Query()
	q.Set("query", params.Query)
	if params.Type != "" {
		q.Set("type", params.Type)
	}
	if len(params.IndexerIDs) > 0 {
		for _, id := range params.IndexerIDs {
			q.Add("indexerIds", strconv.Itoa(id))
		}
	}
	if params.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", params.Limit))
	} else {
		q.Set("limit", "50")
	}
	if len(params.Categories) > 0 {
		catStrs := make([]string, len(params.Categories))
		for i, c := range params.Categories {
			catStrs[i] = fmt.Sprintf("%d", c)
		}
		q.Set("categories", strings.Join(catStrs, ","))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("prowlarr returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var results []prowlarrRelease
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding prowlarr response: %w", err)
	}

	return convertToReleases(results), nil
}

func (p *ProwlarrClient) Ping(ctx context.Context) error {
	u := p.baseURL + "/api/v1/indexer"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr ping: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr ping returned %d", resp.StatusCode)
	}
	return nil
}

// FetchTorrent downloads the raw .torrent file for a release from Prowlarr's
// proxy download URL (downloadUrl). Fetching here (rather than handing the URL
// to a torrent client) lets wmdl control retries and detect failures instead of
// relying on the client's asynchronous URL fetch.
func (p *ProwlarrClient) FetchTorrent(ctx context.Context, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prowlarr fetch torrent: creating request: %w", err)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr fetch torrent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("prowlarr fetch torrent returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return nil, fmt.Errorf("prowlarr fetch torrent: reading body: %w", err)
	}
	return data, nil
}

func (p *ProwlarrClient) GetIndexerName(ctx context.Context, id int) string {
	p.mu.Lock()
	if p.indexerNames == nil {
		p.indexerNames = make(map[int]string)
	}
	if name, ok := p.indexerNames[id]; ok {
		p.mu.Unlock()
		return name
	}
	p.mu.Unlock()

	u := p.baseURL + "/api/v1/indexer"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Sprintf("indexer %d", id)
	}
	req.Header.Set("X-Api-Key", p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Sprintf("indexer %d", id)
	}
	defer resp.Body.Close()

	var indexers []indexerInfo
	if err := json.NewDecoder(resp.Body).Decode(&indexers); err != nil {
		return fmt.Sprintf("indexer %d", id)
	}

	p.mu.Lock()
	// Re-check cache: another goroutine may have populated it while we fetched
	if name, ok := p.indexerNames[id]; ok {
		p.mu.Unlock()
		return name
	}
	for _, idx := range indexers {
		p.indexerNames[idx.ID] = idx.Name
	}
	if name, ok := p.indexerNames[id]; ok {
		p.mu.Unlock()
		return name
	}
	p.mu.Unlock()
	return fmt.Sprintf("indexer %d", id)
}

func (p *ProwlarrClient) SearchMovies(ctx context.Context, query string) ([]quality.ParsedRelease, error) {
	return p.Search(ctx, SearchParams{
		Query:      query,
		Type:       "movie",
		Limit:      50,
		Categories: []int{CatMovie},
	})
}

func (p *ProwlarrClient) SearchTV(ctx context.Context, query string) ([]quality.ParsedRelease, error) {
	return p.Search(ctx, SearchParams{
		Query:      query,
		Type:       "tvsearch",
		Limit:      50,
		Categories: []int{CatTV},
	})
}

func (p *ProwlarrClient) SearchMusic(ctx context.Context, query string) ([]quality.ParsedRelease, error) {
	return p.Search(ctx, SearchParams{
		Query:      query,
		Type:       "music",
		Limit:      50,
		Categories: []int{CatMusic},
	})
}

type grabRequest struct {
	IndexerID int    `json:"indexerId"`
	Guid      string `json:"guid"`
}

func (p *ProwlarrClient) Grab(ctx context.Context, indexerID int, guid string) error {
	body, err := json.Marshal(grabRequest{IndexerID: indexerID, Guid: guid})
	if err != nil {
		return err
	}

	u := p.baseURL + "/api/v1/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr grab: %w", err)
	}
	defer resp.Body.Close()
	defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr grab returned %d", resp.StatusCode)
	}
	return nil
}

func convertToReleases(pr []prowlarrRelease) []quality.ParsedRelease {
	releases := make([]quality.ParsedRelease, 0, len(pr))
	for _, r := range pr {
		if r.Protocol != "torrent" {
			continue
		}
		parsed := quality.Parse(r.Title)
		parsed.Seeders = r.Seeders
		parsed.SizeBytes = r.Size
		parsed.RawTitle = r.Title
		parsed.IndexerID = r.IndexerID
		parsed.IndexerName = r.Indexer
		parsed.Guid = r.Guid
		parsed.DownloadURL = r.DownloadURL
		parsed.MagnetURL = r.MagnetURL
		parsed.InfoHash = r.InfoHash

		releases = append(releases, parsed)
	}
	return releases
}
