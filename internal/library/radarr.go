package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RadarrClient struct {
	baseURL     string
	apiKey      string
	http        *http.Client
	moviesCache []RadarrMovie
}

func NewRadarrClient(baseURL, apiKey string, timeout int) *RadarrClient {
	if timeout <= 0 {
		timeout = 120
	}
	return &RadarrClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

type RadarrMovie struct {
	ID                  int                  `json:"id,omitempty"`
	TMDBID              int                  `json:"tmdbId"`
	Title               string               `json:"title"`
	Year                int                  `json:"year"`
	Overview            string               `json:"overview"`
	Monitored           bool                 `json:"monitored"`
	MinimumAvailability string               `json:"minimumAvailability"`
	QualityProfileID    int                  `json:"qualityProfileId"`
	RootFolderPath      string               `json:"rootFolderPath"`
	Folder              string               `json:"folder,omitempty"`
	Images              []RadarrImage        `json:"images,omitempty"`
	Collection          *RadarrCollectionRef `json:"collection,omitempty"`
	HasFile             bool                 `json:"hasFile,omitempty"`
	IsExisting          bool                 `json:"isExisting,omitempty"`
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
	Name   string `json:"title"`
	TMDBID int    `json:"tmdbId"`
}

type RadarrCollection struct {
	ID     int           `json:"id"`
	Name   string        `json:"title"`
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

func (r *RadarrClient) Ping(ctx context.Context) error {
	var health []map[string]interface{}
	return r.retry(ctx, func() error {
		return r.get(ctx, "/api/v3/health", &health)
	})
}

func (r *RadarrClient) retry(ctx context.Context, fn func() error) error {
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

func (r *RadarrClient) SetAllMovies(movies []RadarrMovie) {
	r.moviesCache = movies
}

func (r *RadarrClient) GetAllMovies(ctx context.Context) ([]RadarrMovie, error) {
	if r.moviesCache != nil {
		return r.moviesCache, nil
	}

	var movies []RadarrMovie
	err := r.retry(ctx, func() error {
		return r.get(ctx, "/api/v3/movie", &movies)
	})
	if err != nil {
		return nil, err
	}
	r.moviesCache = movies
	return movies, nil
}

func (r *RadarrClient) Lookup(ctx context.Context, tmdbID int) (*RadarrMovie, error) {
	u := fmt.Sprintf("/api/v3/movie/lookup?term=tmdb:%d", tmdbID)
	var movies []RadarrMovie
	err := r.retry(ctx, func() error {
		return r.get(ctx, u, &movies)
	})
	if err != nil {
		return nil, fmt.Errorf("radarr lookup: %w", err)
	}
	if len(movies) == 0 {
		return nil, nil
	}
	return &movies[0], nil
}

func (r *RadarrClient) LookupByTitle(ctx context.Context, title string) (*RadarrMovie, error) {
	u := fmt.Sprintf("/api/v3/movie/lookup?term=%s", url.QueryEscape(title))
	var movies []RadarrMovie
	err := r.retry(ctx, func() error {
		return r.get(ctx, u, &movies)
	})
	if err != nil {
		return nil, fmt.Errorf("radarr lookup by title: %w", err)
	}
	if len(movies) == 0 {
		return nil, nil
	}
	return &movies[0], nil
}

func (r *RadarrClient) Exists(ctx context.Context, tmdbID int) (*RadarrMovie, error) {
	movies, err := r.GetAllMovies(ctx)
	if err != nil {
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

	var added RadarrMovie
	err = r.retry(ctx, func() error {
		return r.post(ctx, "/api/v3/movie", body, &added)
	})
	if err != nil {
		return nil, err
	}
	return &added, nil
}

func (r *RadarrClient) GetCollections(ctx context.Context) ([]RadarrCollection, error) {
	var collections []RadarrCollection
	err := r.retry(ctx, func() error {
		return r.get(ctx, "/api/v3/collection", &collections)
	})
	if err != nil {
		return nil, err
	}
	return collections, nil
}

func (r *RadarrClient) GetQualityProfiles(ctx context.Context) ([]RadarrQualityProfile, error) {
	var profiles []RadarrQualityProfile
	err := r.retry(ctx, func() error {
		return r.get(ctx, "/api/v3/qualityProfile", &profiles)
	})
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (r *RadarrClient) GetRootFolders(ctx context.Context) ([]RadarrRootFolder, error) {
	var folders []RadarrRootFolder
	err := r.retry(ctx, func() error {
		return r.get(ctx, "/api/v3/rootFolder", &folders)
	})
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *RadarrClient) TriggerSearch(ctx context.Context, movieID int) error {
	return r.retry(ctx, func() error {
		cmd := radarrCommand{
			Name:     "MoviesSearch",
			MovieIDs: []int{movieID},
		}
		body, err := json.Marshal(cmd)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/api/v3/command", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("X-Api-Key", r.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.http.Do(req)
		if err != nil {
			return fmt.Errorf("radarr search: %w", err)
		}
		defer resp.Body.Close()
		defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("radarr search returned %d", resp.StatusCode)
		}
		return nil
	})
}

func (r *RadarrClient) get(ctx context.Context, path string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", r.apiKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("radarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("radarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

func (r *RadarrClient) post(ctx context.Context, path string, body []byte, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("radarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("radarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}
