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

type SonarrClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewSonarrClient(baseURL, apiKey string) *SonarrClient {
	return &SonarrClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type SonarrSeries struct {
	ID                int               `json:"id,omitempty"`
	TVDBID            int               `json:"tvdbId"`
	Title             string            `json:"title"`
	Year              int               `json:"year"`
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

func (s *SonarrClient) Lookup(ctx context.Context, tvdbID int) (*SonarrSeries, error) {
	u := fmt.Sprintf("%s/api/v3/series/lookup?term=tvdb:%d", s.baseURL, tvdbID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr lookup returned %d", resp.StatusCode)
	}

	var series []SonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return nil, nil
	}
	return &series[0], nil
}

func (s *SonarrClient) Exists(ctx context.Context, tvdbID int) (*SonarrSeries, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v3/series", nil)
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr list returned %d", resp.StatusCode)
	}

	var all []SonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	for _, series := range all {
		if series.TVDBID == tvdbID {
			return &series, nil
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

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/v3/series", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr add: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr add returned %d", resp.StatusCode)
	}

	var added SonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&added); err != nil {
		return nil, err
	}
	return &added, nil
}

func (s *SonarrClient) TriggerSeasonSearch(ctx context.Context, seriesID, seasonNumber int) error {
	cmd := sonarrCommand{
		Name:         "SeasonSearch",
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
	}
	body, _ := json.Marshal(cmd)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/v3/command", bytes.NewReader(body))
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
}

func (s *SonarrClient) GetQualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v3/qualityProfile", nil)
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr profiles returned %d", resp.StatusCode)
	}

	var profiles []SonarrQualityProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *SonarrClient) GetLanguageProfiles(ctx context.Context) ([]SonarrLanguageProfile, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v3/languageProfile", nil)
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr language profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr language profiles returned %d", resp.StatusCode)
	}

	var profiles []SonarrLanguageProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *SonarrClient) GetRootFolders(ctx context.Context) ([]SonarrRootFolder, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v3/rootFolder", nil)
	req.Header.Set("X-Api-Key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sonarr root folders: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sonarr root folders returned %d", resp.StatusCode)
	}

	var folders []SonarrRootFolder
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		return nil, err
	}
	return folders, nil
}
