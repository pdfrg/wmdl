package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type RadarrClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewRadarrClient(baseURL, apiKey string) *RadarrClient {
	return &RadarrClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type RadarrMovie struct {
	ID                  int                  `json:"id,omitempty"`
	TMDBID              int                  `json:"tmdbId"`
	Title               string               `json:"title"`
	Year                int                  `json:"year"`
	Monitored           bool                 `json:"monitored"`
	MinimumAvailability string               `json:"minimumAvailability"`
	QualityProfileID    int                  `json:"qualityProfileId"`
	RootFolderPath      string               `json:"rootFolderPath"`
	Folder              string               `json:"folder,omitempty"`
	Images              []RadarrImage        `json:"images,omitempty"`
	Collection          *RadarrCollectionRef `json:"collection,omitempty"`
	HasFile             bool                 `json:"hasFile,omitempty"`
	IsAvailable         bool                 `json:"isAvailable,omitempty"`
	Status              string               `json:"status,omitempty"`
	AddOptions          *radarrAddOptions    `json:"addOptions,omitempty"`
}

type radarrAddOptions struct {
	SearchForMovie bool `json:"searchForMovie"`
}

type RadarrImage struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

type RadarrCollectionRef struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	TMDBID int    `json:"tmdbId"`
}

type RadarrCollection struct {
	ID     int           `json:"id"`
	Name   string        `json:"name"`
	TMDBID int           `json:"tmdbId"`
	Movies []RadarrMovie `json:"movies"`
}

type RadarrQualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RadarrRootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type radarrCommand struct {
	Name     string `json:"name"`
	MovieIDs []int  `json:"movieIds,omitempty"`
}

func (r *RadarrClient) Lookup(ctx context.Context, tmdbID int) (*RadarrMovie, error) {
	u := fmt.Sprintf("%s/api/v3/movie/lookup?term=tmdb:%d", r.baseURL, tmdbID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr lookup returned %d", resp.StatusCode)
	}

	var movies []RadarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, err
	}
	if len(movies) == 0 {
		return nil, nil
	}
	return &movies[0], nil
}

func (r *RadarrClient) Exists(ctx context.Context, tmdbID int) (*RadarrMovie, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v3/movie", nil)
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr list returned %d", resp.StatusCode)
	}

	var movies []RadarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, err
	}
	for _, m := range movies {
		if m.TMDBID == tmdbID {
			return &m, nil
		}
	}
	return nil, nil
}

type AddMovieOptions struct {
	Monitored           bool
	MinimumAvailability string
	QualityProfileID    int
	RootFolderPath      string
	SearchNow           bool
}

func (r *RadarrClient) Add(ctx context.Context, tmdbID int, title string, year int, opts AddMovieOptions) (*RadarrMovie, error) {
	movie := RadarrMovie{
		TMDBID:              tmdbID,
		Title:               title,
		Year:                year,
		Monitored:           opts.Monitored,
		MinimumAvailability: opts.MinimumAvailability,
		QualityProfileID:    opts.QualityProfileID,
		RootFolderPath:      opts.RootFolderPath,
		AddOptions: &radarrAddOptions{
			SearchForMovie: opts.SearchNow,
		},
	}

	body, err := json.Marshal(movie)
	if err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/api/v3/movie", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr add: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr add returned %d", resp.StatusCode)
	}

	var added RadarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&added); err != nil {
		return nil, err
	}
	return &added, nil
}

func (r *RadarrClient) GetCollections(ctx context.Context) ([]RadarrCollection, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v3/collection", nil)
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr collections: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr collections returned %d", resp.StatusCode)
	}

	var collections []RadarrCollection
	if err := json.NewDecoder(resp.Body).Decode(&collections); err != nil {
		return nil, err
	}
	return collections, nil
}

func (r *RadarrClient) GetQualityProfiles(ctx context.Context) ([]RadarrQualityProfile, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v3/qualityProfile", nil)
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr profiles returned %d", resp.StatusCode)
	}

	var profiles []RadarrQualityProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (r *RadarrClient) GetRootFolders(ctx context.Context) ([]RadarrRootFolder, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v3/rootFolder", nil)
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radarr root folders: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radarr root folders returned %d", resp.StatusCode)
	}

	var folders []RadarrRootFolder
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *RadarrClient) TriggerSearch(ctx context.Context, movieID int) error {
	cmd := radarrCommand{
		Name:     "MoviesSearch",
		MovieIDs: []int{movieID},
	}
	body, _ := json.Marshal(cmd)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/api/v3/command", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("radarr search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("radarr search returned %d", resp.StatusCode)
	}
	return nil
}
