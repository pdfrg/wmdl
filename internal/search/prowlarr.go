package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/quality"
)

type ProwlarrClient struct {
	baseURL           string
	apiKey            string
	http              *http.Client
	indexerNames      map[int]string
	indexerByCategory map[string]int
}

type indexerInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func NewProwlarrClient(baseURL, apiKey string, timeoutSec int, indexerByCategory map[string]int) *ProwlarrClient {
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
	CatMovie = 2000 // Movies (parent)
	CatTV    = 5000 // TV (parent)
	CatMusic = 3000 // Music (parent)
	CatAnime = 5070 // TV/Anime (Newznab standard)
)

func categoryKey(cat int) string {
	switch cat {
	case CatMovie, CatTV, CatAnime:
		return "videos"
	case CatMusic:
		return "music"
	}
	return ""
}

func (p *ProwlarrClient) PreferredIndexerID(category int) int {
	if p.indexerByCategory == nil {
		return 0
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
	IndexerID  int
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
	if params.IndexerID > 0 {
		q.Set("indexerIds", fmt.Sprintf("%d", params.IndexerID))
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
		return nil, fmt.Errorf("prowlarr returned %d", resp.StatusCode)
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
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prowlarr ping returned %d", resp.StatusCode)
	}
	return nil
}

func (p *ProwlarrClient) GetIndexerName(ctx context.Context, id int) string {
	if p.indexerNames == nil {
		p.indexerNames = make(map[int]string)
	}
	if name, ok := p.indexerNames[id]; ok {
		return name
	}

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

	for _, idx := range indexers {
		p.indexerNames[idx.ID] = idx.Name
	}
	if name, ok := p.indexerNames[id]; ok {
		return name
	}
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
