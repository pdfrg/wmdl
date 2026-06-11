package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SonarrClient struct {
	baseURL     string
	apiKey      string
	http        *http.Client
	seriesCache []SonarrSeries
}

func NewSonarrClient(baseURL, apiKey string, timeout int) *SonarrClient {
	if timeout <= 0 {
		timeout = 120
	}
	return &SonarrClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

type SonarrSeries struct {
	ID                int               `json:"id,omitempty"`
	TVDBID            int               `json:"tvdbId"`
	Title             string            `json:"title"`
	Year              int               `json:"year"`
	Overview          string            `json:"overview"`
	Monitored         bool              `json:"monitored"`
	SeasonFolder      bool              `json:"seasonFolder"`
	QualityProfileID  int               `json:"qualityProfileId"`
	LanguageProfileID int               `json:"languageProfileId"`
	RootFolderPath    string            `json:"rootFolderPath"`
	Seasons           []SonarrSeason    `json:"seasons"`
	Images            []SonarrImage     `json:"images,omitempty"`
	AddOptions        *sonarrAddOptions `json:"addOptions,omitempty"`
}

type SonarrSeason struct {
	SeasonNumber int                `json:"seasonNumber"`
	Monitored    bool               `json:"monitored"`
	Statistics   *SonarrSeasonStats `json:"statistics,omitempty"`
}

type SonarrSeasonStats struct {
	EpisodeCount      int `json:"episodeCount"`
	EpisodeFileCount  int `json:"episodeFileCount"`
	TotalEpisodeCount int `json:"totalEpisodeCount"`
}

type SonarrImage struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

type sonarrAddOptions struct {
	SearchForMissingEpisodes bool `json:"searchForMissingEpisodes"`
}

type SonarrQualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type SonarrLanguageProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type SonarrRootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type sonarrCommand struct {
	Name         string `json:"name"`
	SeriesID     int    `json:"seriesId,omitempty"`
	SeasonNumber int    `json:"seasonNumber,omitempty"`
	EpisodeIDs   []int  `json:"episodeIds,omitempty"`
}

func (s *SonarrClient) Ping(ctx context.Context) error {
	var health []map[string]interface{}
	return s.retry(ctx, func() error {
		return s.get(ctx, "/api/v3/health", &health)
	})
}

func (s *SonarrClient) retry(ctx context.Context, fn func() error) error {
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

func (s *SonarrClient) SetAllSeries(series []SonarrSeries) {
	s.seriesCache = series
}

func (s *SonarrClient) GetAllSeries(ctx context.Context) ([]SonarrSeries, error) {
	if s.seriesCache != nil {
		return s.seriesCache, nil
	}

	var series []SonarrSeries
	err := s.retry(ctx, func() error {
		return s.get(ctx, "/api/v3/series", &series)
	})
	if err != nil {
		return nil, err
	}
	s.seriesCache = series
	return series, nil
}

func (s *SonarrClient) Lookup(ctx context.Context, tvdbID int) (*SonarrSeries, error) {
	u := fmt.Sprintf("/api/v3/series/lookup?term=tvdb:%d", tvdbID)
	var series []SonarrSeries
	err := s.retry(ctx, func() error {
		return s.get(ctx, u, &series)
	})
	if err != nil {
		return nil, fmt.Errorf("sonarr lookup: %w", err)
	}
	if len(series) == 0 {
		return nil, nil
	}
	return &series[0], nil
}

func (s *SonarrClient) LookupByTitle(ctx context.Context, title string) (*SonarrSeries, error) {
	u := fmt.Sprintf("/api/v3/series/lookup?term=%s", url.QueryEscape(title))
	var series []SonarrSeries
	err := s.retry(ctx, func() error {
		return s.get(ctx, u, &series)
	})
	if err != nil {
		return nil, fmt.Errorf("sonarr lookup by title: %w", err)
	}
	if len(series) == 0 {
		return nil, nil
	}
	return &series[0], nil
}

func (s *SonarrClient) Exists(ctx context.Context, tvdbID int) (*SonarrSeries, error) {
	series, err := s.GetAllSeries(ctx)
	if err != nil {
		return nil, err
	}
	for _, ser := range series {
		if ser.TVDBID == tvdbID {
			return &ser, nil
		}
	}
	return nil, nil
}

type AddSeriesOptions struct {
	Monitored         bool
	SeasonFolder      bool
	QualityProfileID  int
	LanguageProfileID int
	RootFolderPath    string
	Seasons           []SonarrSeason
	SearchForMissing  bool
}

func (s *SonarrClient) Add(ctx context.Context, tvdbID int, title string, year int, opts AddSeriesOptions) (*SonarrSeries, error) {
	series := SonarrSeries{
		TVDBID:            tvdbID,
		Title:             title,
		Year:              year,
		Monitored:         opts.Monitored,
		SeasonFolder:      opts.SeasonFolder,
		QualityProfileID:  opts.QualityProfileID,
		LanguageProfileID: opts.LanguageProfileID,
		RootFolderPath:    opts.RootFolderPath,
		Seasons:           opts.Seasons,
		AddOptions: &sonarrAddOptions{
			SearchForMissingEpisodes: opts.SearchForMissing,
		},
	}

	body, err := json.Marshal(series)
	if err != nil {
		return nil, err
	}

	var added SonarrSeries
	err = s.retry(ctx, func() error {
		return s.post(ctx, "/api/v3/series", body, &added)
	})
	if err != nil {
		return nil, err
	}
	return &added, nil
}

func (s *SonarrClient) TriggerSeasonSearch(ctx context.Context, seriesID, seasonNumber int) error {
	return s.retry(ctx, func() error {
		cmd := sonarrCommand{
			Name:         "SeasonSearch",
			SeriesID:     seriesID,
			SeasonNumber: seasonNumber,
		}
		body, err := json.Marshal(cmd)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/v3/command", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("X-Api-Key", s.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.http.Do(req)
		if err != nil {
			return fmt.Errorf("sonarr season search: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("sonarr season search returned %d", resp.StatusCode)
		}
		return nil
	})
}

func (s *SonarrClient) GetQualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	var profiles []SonarrQualityProfile
	err := s.retry(ctx, func() error {
		return s.get(ctx, "/api/v3/qualityProfile", &profiles)
	})
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *SonarrClient) GetLanguageProfiles(ctx context.Context) ([]SonarrLanguageProfile, error) {
	var profiles []SonarrLanguageProfile
	err := s.retry(ctx, func() error {
		return s.get(ctx, "/api/v3/languageProfile", &profiles)
	})
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *SonarrClient) GetRootFolders(ctx context.Context) ([]SonarrRootFolder, error) {
	var folders []SonarrRootFolder
	err := s.retry(ctx, func() error {
		return s.get(ctx, "/api/v3/rootFolder", &folders)
	})
	if err != nil {
		return nil, err
	}
	return folders, nil
}

func (s *SonarrClient) GetSeries(ctx context.Context, seriesID int) (*SonarrSeries, error) {
	u := fmt.Sprintf("/api/v3/series/%d", seriesID)
	var series SonarrSeries
	err := s.retry(ctx, func() error {
		return s.get(ctx, u, &series)
	})
	if err != nil {
		return nil, err
	}
	return &series, nil
}

func (s *SonarrClient) get(ctx context.Context, path string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("sonarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sonarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

func (s *SonarrClient) post(ctx context.Context, path string, body []byte, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("sonarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sonarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}
