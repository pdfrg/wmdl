package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const mbBase = "https://musicbrainz.org/ws/2"

// MBClient handles MusicBrainz API lookups with rate limiting.
type MBClient struct {
	client    *http.Client
	lastReq   time.Time
	mu        sync.Mutex
	userAgent string
}

func NewMBClient() *MBClient {
	return &MBClient{
		client:    &http.Client{Timeout: 15 * time.Second},
		userAgent: "wmdl/0.1.0 (https://github.com/pdfrg/wmdl)",
	}
}

// mbArtistResult represents search results for an artist.
type mbArtistResult struct {
	Artists []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Score   int    `json:"score"`
		Type    string `json:"type,omitempty"`
		Country string `json:"country,omitempty"`
	} `json:"artists"`
	Count int `json:"count"`
}

// mbReleaseGroupResult represents search results for release groups.
type mbReleaseGroupResult struct {
	ReleaseGroups []struct {
		ID               string `json:"id"`
		Title            string `json:"title"`
		Score            int    `json:"score"`
		PrimaryType      string `json:"primary-type,omitempty"`
		FirstReleaseDate string `json:"first-release-date,omitempty"`
		ArtistCredit     []struct {
			Name   string `json:"name"`
			Artist struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"artist"`
		} `json:"artist-credit,omitempty"`
	} `json:"release-groups"`
	Count int `json:"count"`
}

// mbReleaseGroupDetail holds detailed info about a release group.
type mbReleaseGroupDetail struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	PrimaryType      string `json:"primary-type,omitempty"`
	FirstReleaseDate string `json:"first-release-date,omitempty"`
	Rating           *struct {
		Value     float64 `json:"value"`
		VoteCount int     `json:"vote-count"`
	} `json:"rating,omitempty"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags,omitempty"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres,omitempty"`
	ArtistCredit []struct {
		Name   string `json:"name"`
		Artist struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit,omitempty"`
}

// MBArtistResult is the public result from an artist search.
type MBArtistResult struct {
	MBID    string
	Name    string
	Country string
}

// MBReleaseGroupResult is the public result from a release group search.
type MBReleaseGroupResult struct {
	MBID             string
	Title            string
	PrimaryType      string
	FirstReleaseDate string
	ArtistMBID       string
	ArtistName       string
	Rating           float64 // MusicBrainz rating (1-5 scale)
}

// SearchArtist searches MusicBrainz for an artist by name.
// Returns the best-matching result.
func (c *MBClient) SearchArtist(ctx context.Context, artistName string) (*MBArtistResult, error) {
	c.rateLimit()

	query := url.QueryEscape(fmt.Sprintf(`artist:"%s"`, artistName))
	u := fmt.Sprintf("%s/artist/?query=%s&fmt=json&limit=5", mbBase, query)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb artist search: %w", err)
	}
	defer resp.Body.Close()

	var result mbArtistResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding mb response: %w", err)
	}

	if len(result.Artists) == 0 {
		return nil, nil
	}

	// Return the highest-scored result
	best := &result.Artists[0]
	return &MBArtistResult{
		MBID:    best.ID,
		Name:    best.Name,
		Country: best.Country,
	}, nil
}

// SearchReleaseGroup searches for a release group by album title and artist.
// Returns the best-matching result.
func (c *MBClient) SearchReleaseGroup(ctx context.Context, albumTitle, artistName string) (*MBReleaseGroupResult, error) {
	c.rateLimit()

	query := url.QueryEscape(fmt.Sprintf(`release:"%s" AND artist:"%s"`, albumTitle, artistName))
	u := fmt.Sprintf("%s/release-group/?query=%s&fmt=json&limit=5", mbBase, query)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb release group search: %w", err)
	}
	defer resp.Body.Close()

	var result mbReleaseGroupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding mb response: %w", err)
	}

	if len(result.ReleaseGroups) == 0 {
		return nil, nil
	}

	// Return the best-scored result
	best := &result.ReleaseGroups[0]

	var artistMBID, artistCreditName string
	if len(best.ArtistCredit) > 0 {
		artistCreditName = best.ArtistCredit[0].Name
		artistMBID = best.ArtistCredit[0].Artist.ID
	}

	return &MBReleaseGroupResult{
		MBID:             best.ID,
		Title:            best.Title,
		PrimaryType:      best.PrimaryType,
		FirstReleaseDate: best.FirstReleaseDate,
		ArtistMBID:       artistMBID,
		ArtistName:       artistCreditName,
	}, nil
}

// GetReleaseGroupDetail fetches detailed info about a release group including
// rating, tags, and genres.
func (c *MBClient) GetReleaseGroupDetail(ctx context.Context, mbid string) (*MBReleaseGroupResult, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/release-group/%s?inc=tags+genres+ratings+artist-credits&fmt=json", mbBase, mbid)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb release group detail: %w", err)
	}
	defer resp.Body.Close()

	var detail mbReleaseGroupDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decoding mb detail: %w", err)
	}

	var artistMBID, artistName string
	if len(detail.ArtistCredit) > 0 {
		artistName = detail.ArtistCredit[0].Name
		artistMBID = detail.ArtistCredit[0].Artist.ID
	}

	var rating float64
	if detail.Rating != nil {
		rating = detail.Rating.Value
	}

	return &MBReleaseGroupResult{
		MBID:             detail.ID,
		Title:            detail.Title,
		PrimaryType:      detail.PrimaryType,
		FirstReleaseDate: detail.FirstReleaseDate,
		ArtistMBID:       artistMBID,
		ArtistName:       artistName,
		Rating:           rating,
	}, nil
}

// rateLimit ensures at most 1 request per second to MusicBrainz.
func (c *MBClient) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastReq)
	if elapsed < time.Second {
		time.Sleep(time.Second - elapsed)
	}
	c.lastReq = time.Now()
}
