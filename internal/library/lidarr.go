package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LidarrClient struct {
	baseURL         string
	apiKey          string
	http            *http.Client
	artistCache     []LidarrArtist
	albumCache      []LidarrAlbum
	profileCache    []LidarrQualityProfile
	metadataCache   []LidarrMetadataProfile
	rootFolderCache []LidarrRootFolder
}

func NewLidarrClient(baseURL, apiKey string, timeout int) *LidarrClient {
	if timeout <= 0 {
		timeout = 120
	}
	return &LidarrClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

type LidarrArtist struct {
	ID                int                `json:"id,omitempty"`
	ForeignArtistID   string             `json:"foreignArtistId"`
	ArtistName        string             `json:"artistName"`
	MBID              string             `json:"mbId,omitempty"`
	Monitored         bool               `json:"monitored"`
	MonitorNewAlbums  bool               `json:"monitorNewAlbums"`
	QualityProfileID  int                `json:"qualityProfileId"`
	MetadataProfileID int                `json:"metadataProfileId"`
	RootFolderPath    string             `json:"rootFolderPath"`
	Path              string             `json:"path,omitempty"`
	Genres            []string           `json:"genres,omitempty"`
	Images            []LidarrImage      `json:"images,omitempty"`
	AddOptions        *LidarrAddOptions  `json:"addOptions,omitempty"`
	Statistics        *LidarrArtistStats `json:"statistics,omitempty"`
}

type LidarrAddOptions struct {
	SearchForNewAlbum bool   `json:"searchForNewAlbum"`
	Monitor           string `json:"monitor,omitempty"`
}

// MusicBrainzID returns the artist's MusicBrainz ID. Lidarr v2 exposes it as
// foreignArtistId on list endpoints; the mbId field is frequently absent, so
// it is used only as a fallback.
func (a LidarrArtist) MusicBrainzID() string {
	if a.MBID != "" {
		return a.MBID
	}
	return a.ForeignArtistID
}

type LidarrArtistStats struct {
	AlbumCount int `json:"albumCount,omitempty"`
}

type LidarrImage struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

type LidarrQualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type LidarrMetadataProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type LidarrRootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type LidarrAlbum struct {
	ID             int                    `json:"id,omitempty"`
	ForeignAlbumID string                 `json:"foreignAlbumId"`
	ArtistID       int                    `json:"artistId"`
	Title          string                 `json:"title"`
	Monitored      bool                   `json:"monitored"`
	ReleaseDate    string                 `json:"releaseDate,omitempty"`
	AlbumType      string                 `json:"albumType,omitempty"`
	Statistics     *LidarrAlbumStatistics `json:"statistics,omitempty"`
	AddOptions     *LidarrAlbumAddOptions `json:"addOptions,omitempty"`
}

// LidarrAlbumStatistics reports how much of an album is present on disk.
// TrackFileCount > 0 means files have actually been downloaded.
type LidarrAlbumStatistics struct {
	TrackFileCount  int     `json:"trackFileCount"`
	TrackCount      int     `json:"trackCount"`
	TotalTrackCount int     `json:"totalTrackCount"`
	SizeOnDisk      int64   `json:"sizeOnDisk"`
	PercentOfTracks float64 `json:"percentOfTracks"`
}

type LidarrAlbumAddOptions struct {
	SearchForNewAlbum bool `json:"searchForNewAlbum"`
}

type lidarrCommand struct {
	Name      string `json:"name"`
	AlbumIDs  []int  `json:"albumIds,omitempty"`
	ArtistIDs []int  `json:"artistIds,omitempty"`
}

func (c *LidarrClient) SetAllArtists(artists []LidarrArtist) {
	c.artistCache = artists
}

func (c *LidarrClient) SetAllAlbums(albums []LidarrAlbum) {
	c.albumCache = albums
}

func (c *LidarrClient) Ping(ctx context.Context) error {
	var health []map[string]interface{}
	return c.get(ctx, "/api/v1/health", &health)
}

func (c *LidarrClient) retry(ctx context.Context, fn func() error) error {
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

func (c *LidarrClient) GetAllArtists(ctx context.Context) ([]LidarrArtist, error) {
	if c.artistCache != nil {
		return c.artistCache, nil
	}
	var artists []LidarrArtist
	err := c.retry(ctx, func() error {
		return c.get(ctx, "/api/v1/artist", &artists)
	})
	if err != nil {
		return nil, err
	}
	c.artistCache = artists
	return artists, nil
}

func (c *LidarrClient) GetAllAlbums(ctx context.Context) ([]LidarrAlbum, error) {
	if c.albumCache != nil {
		return c.albumCache, nil
	}
	var albums []LidarrAlbum
	err := c.retry(ctx, func() error {
		return c.get(ctx, "/api/v1/album", &albums)
	})
	if err != nil {
		return nil, err
	}
	c.albumCache = albums
	return albums, nil
}

func (c *LidarrClient) GetQualityProfiles(ctx context.Context) ([]LidarrQualityProfile, error) {
	if c.profileCache != nil {
		return c.profileCache, nil
	}
	var profiles []LidarrQualityProfile
	err := c.retry(ctx, func() error {
		return c.get(ctx, "/api/v1/qualityprofile", &profiles)
	})
	if err != nil {
		return nil, err
	}
	c.profileCache = profiles
	return profiles, nil
}

func (c *LidarrClient) GetMetadataProfiles(ctx context.Context) ([]LidarrMetadataProfile, error) {
	if c.metadataCache != nil {
		return c.metadataCache, nil
	}
	var profiles []LidarrMetadataProfile
	err := c.retry(ctx, func() error {
		return c.get(ctx, "/api/v1/metadataprofile", &profiles)
	})
	if err != nil {
		return nil, err
	}
	c.metadataCache = profiles
	return profiles, nil
}

func (c *LidarrClient) GetRootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	if c.rootFolderCache != nil {
		return c.rootFolderCache, nil
	}
	var folders []LidarrRootFolder
	err := c.retry(ctx, func() error {
		return c.get(ctx, "/api/v1/rootfolder", &folders)
	})
	if err != nil {
		return nil, err
	}
	c.rootFolderCache = folders
	return folders, nil
}

func (c *LidarrClient) ResolveQualityProfileID(ctx context.Context, profileName string) (int, error) {
	if profileName == "" {
		return 1, nil
	}
	profiles, err := c.GetQualityProfiles(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range profiles {
		if strings.EqualFold(p.Name, profileName) {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("quality profile %q not found in Lidarr", profileName)
}

func (c *LidarrClient) ResolveMetadataProfileID(ctx context.Context, profileName string) (int, error) {
	if profileName == "" {
		return 1, nil
	}
	profiles, err := c.GetMetadataProfiles(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range profiles {
		if strings.EqualFold(p.Name, profileName) {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("metadata profile %q not found in Lidarr", profileName)
}

func (c *LidarrClient) LookupArtist(ctx context.Context, mbid string) (*LidarrArtist, error) {
	u := fmt.Sprintf("/api/v1/artist/lookup?term=mbid:%s", mbid)
	var artists []LidarrArtist
	err := c.retry(ctx, func() error {
		return c.get(ctx, u, &artists)
	})
	if err != nil {
		return nil, fmt.Errorf("lidarr artist lookup: %w", err)
	}
	if len(artists) == 0 {
		return nil, nil
	}
	return &artists[0], nil
}

func (c *LidarrClient) GetArtist(ctx context.Context, mbid string) (*LidarrArtist, error) {
	artists, err := c.GetAllArtists(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range artists {
		if a.MusicBrainzID() == mbid {
			return &a, nil
		}
	}
	return nil, nil
}

type AddArtistOptions struct {
	Monitored         bool
	MonitorNewAlbums  bool
	QualityProfileID  int
	MetadataProfileID int
	RootFolderPath    string
	Monitor           string // all, future, missing, latest, none
	SearchNow         bool
}

func (c *LidarrClient) AddArtist(ctx context.Context, mbid string, name string, opts AddArtistOptions) (*LidarrArtist, error) {
	artistPath := opts.RootFolderPath
	if !strings.HasSuffix(artistPath, "/") {
		artistPath += "/"
	}
	artistPath += sanitizeDirName(name)

	artist := LidarrArtist{
		ForeignArtistID:   mbid,
		ArtistName:        name,
		MBID:              mbid,
		Monitored:         opts.Monitored,
		MonitorNewAlbums:  opts.MonitorNewAlbums,
		QualityProfileID:  opts.QualityProfileID,
		MetadataProfileID: opts.MetadataProfileID,
		RootFolderPath:    opts.RootFolderPath,
		Path:              artistPath,
		AddOptions: &LidarrAddOptions{
			SearchForNewAlbum: opts.SearchNow,
			Monitor:           opts.Monitor,
		},
	}

	body, err := json.Marshal(artist)
	if err != nil {
		return nil, err
	}

	var added LidarrArtist
	err = c.retry(ctx, func() error {
		return c.post(ctx, "/api/v1/artist", body, &added)
	})
	if err != nil {
		return nil, err
	}
	return &added, nil
}

func (c *LidarrClient) LookupAlbum(ctx context.Context, mbid string) (*LidarrAlbum, error) {
	u := fmt.Sprintf("/api/v1/album/lookup?term=mbid:%s", mbid)
	var albums []LidarrAlbum
	err := c.retry(ctx, func() error {
		return c.get(ctx, u, &albums)
	})
	if err != nil {
		return nil, fmt.Errorf("lidarr album lookup: %w", err)
	}
	if len(albums) == 0 {
		return nil, nil
	}
	return &albums[0], nil
}

type AddAlbumOptions struct {
	Monitored bool
	SearchNow bool
}

func (c *LidarrClient) AddAlbum(ctx context.Context, mbid string, artistID int, title string, opts AddAlbumOptions) (*LidarrAlbum, error) {
	album := LidarrAlbum{
		ForeignAlbumID: mbid,
		ArtistID:       artistID,
		Title:          title,
		Monitored:      opts.Monitored,
		AddOptions: &LidarrAlbumAddOptions{
			SearchForNewAlbum: opts.SearchNow,
		},
	}

	body, err := json.Marshal(album)
	if err != nil {
		return nil, err
	}

	var added LidarrAlbum
	err = c.retry(ctx, func() error {
		return c.post(ctx, "/api/v1/album", body, &added)
	})
	if err != nil {
		return nil, err
	}
	return &added, nil
}

func (c *LidarrClient) TriggerAlbumSearch(ctx context.Context, albumIDs []int) error {
	return c.retry(ctx, func() error {
		cmd := lidarrCommand{
			Name:     "AlbumSearch",
			AlbumIDs: albumIDs,
		}
		body, err := json.Marshal(cmd)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/command", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("X-Api-Key", c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("lidarr search: %w", err)
		}
		defer resp.Body.Close()
		defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("lidarr search returned %d", resp.StatusCode)
		}
		return nil
	})
}

func (c *LidarrClient) get(ctx context.Context, path string, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lidarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lidarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

func (c *LidarrClient) post(ctx context.Context, path string, body []byte, dst interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lidarr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lidarr returned %d", resp.StatusCode)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

func sanitizeDirName(name string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	return strings.TrimSpace(r.Replace(name))
}
