package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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
		client:    &http.Client{Timeout: 10 * time.Second},
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
	ID                string `json:"id"`
	Title             string `json:"title"`
	PrimaryType       string `json:"primary-type,omitempty"`
	SecondaryTypeList []struct {
		Name string `json:"name"`
	} `json:"secondary-type-list,omitempty"`
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
	SecondaryTypes   []string
	FirstReleaseDate string
	ArtistMBID       string
	ArtistName       string
	Rating           float64 // MusicBrainz rating (1-5 scale)
	Genres           []string
	Tags             []string
}

// MBArtistDetail holds detailed info about an artist fetched by MBID.
type MBArtistDetail struct {
	MBID           string
	Name           string
	Type           string // "Person" or "Group"
	Country        string // ISO code (e.g. "US", "AU")
	Area           string
	BeginArea      string // birthplace or origin area name
	BeginDate      string
	EndDate        string
	Disambiguation string
	Tags           []string
	Genres         []string
	Rating         float64
	RatingVotes    int
	WikidataURL    string
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

// GetArtist fetches detailed artist info by MBID (not a search — direct lookup).
func (c *MBClient) GetArtist(ctx context.Context, mbid string) (*MBArtistDetail, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/artist/%s?inc=tags+genres+ratings+annotation+url-rels+aliases&fmt=json", mbBase, mbid)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb artist detail: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Country        string `json:"country"`
		Disambiguation string `json:"disambiguation"`
		Area           *struct {
			Name string `json:"name"`
		} `json:"area,omitempty"`
		BeginArea *struct {
			Name string `json:"name"`
		} `json:"begin-area,omitempty"`
		LifeSpan *struct {
			Begin string `json:"begin"`
			End   string `json:"end"`
		} `json:"life-span,omitempty"`
		Tags []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"tags,omitempty"`
		Genres []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"genres,omitempty"`
		Rating *struct {
			Value     float64 `json:"value"`
			VoteCount int     `json:"votes-count"`
		} `json:"rating,omitempty"`
		Relations []struct {
			Type string `json:"type"`
			URL  *struct {
				Resource string `json:"resource"`
			} `json:"url,omitempty"`
		} `json:"relations,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding mb artist detail: %w", err)
	}

	det := &MBArtistDetail{
		MBID:           raw.ID,
		Name:           raw.Name,
		Type:           raw.Type,
		Country:        raw.Country,
		Disambiguation: raw.Disambiguation,
	}
	if raw.Area != nil {
		det.Area = raw.Area.Name
	}
	if raw.BeginArea != nil {
		det.BeginArea = raw.BeginArea.Name
	}
	if raw.LifeSpan != nil {
		det.BeginDate = raw.LifeSpan.Begin
		det.EndDate = raw.LifeSpan.End
	}
	for _, g := range raw.Genres {
		det.Genres = append(det.Genres, g.Name)
	}
	for _, t := range raw.Tags {
		det.Tags = append(det.Tags, t.Name)
	}
	if raw.Rating != nil {
		det.Rating = raw.Rating.Value
		det.RatingVotes = raw.Rating.VoteCount
	}
	for _, r := range raw.Relations {
		if r.URL != nil && r.Type == "wikidata" {
			det.WikidataURL = r.URL.Resource
			break
		}
	}

	return det, nil
}

// SearchReleaseGroup searches for a release group by album title and artist.
// Tries multiple query strategies in order of specificity, returning the first
// high-confidence match.
func (c *MBClient) SearchReleaseGroup(ctx context.Context, albumTitle, artistName string) (*MBReleaseGroupResult, error) {
	cleanTitle := stripTitleParens(albumTitle)
	firstArtist := firstArtistName(artistName)

	queries := []string{
		fmt.Sprintf(`release:"%s" AND artist:"%s"`, albumTitle, artistName),
		fmt.Sprintf(`release:"%s" AND artist:"%s"`, albumTitle, firstArtist),
	}
	if cleanTitle != albumTitle {
		queries = append(queries,
			fmt.Sprintf(`release:"%s" AND artist:"%s"`, cleanTitle, artistName),
			fmt.Sprintf(`release:"%s" AND artist:"%s"`, cleanTitle, firstArtist),
		)
	}

	for _, q := range queries {
		result, err := c.searchReleaseGroupOnce(ctx, q)
		if err != nil {
			// Network/API error — try next fallback
			continue
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

// SearchReleaseGroupByAlbum searches for a release group by album title only
// (no artist constraint). Used as a blind check when the artist-specific search
// finds no match — if the album exists under a different artist, the scrape
// likely associated the wrong artist name.
func (c *MBClient) SearchReleaseGroupByAlbum(ctx context.Context, albumTitle string) (*MBReleaseGroupResult, error) {
	cleanTitle := stripTitleParens(albumTitle)
	queries := []string{
		fmt.Sprintf(`release:"%s"`, albumTitle),
	}
	if cleanTitle != albumTitle {
		queries = append(queries, fmt.Sprintf(`release:"%s"`, cleanTitle))
	}
	for _, q := range queries {
		result, err := c.searchReleaseGroupOnce(ctx, q)
		if err != nil {
			continue
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

func (c *MBClient) searchReleaseGroupOnce(ctx context.Context, query string) (*MBReleaseGroupResult, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/release-group/?query=%s&fmt=json&limit=5", mbBase, url.QueryEscape(query))

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

// stripTitleParens removes trailing parenthetical groups like "(The Piano Versions)".
func stripTitleParens(title string) string {
	re := regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	return strings.TrimSpace(re.ReplaceAllString(title, ""))
}

// firstArtistName returns the artist name before the first conjunction separator.
// "Jeff Parker & ETA IVtet" → "Jeff Parker"
func firstArtistName(name string) string {
	re := regexp.MustCompile(`\s*(&|feat\.|ft\.|with|vs\.|\+|/)\s*.*$`)
	return strings.TrimSpace(re.ReplaceAllString(name, ""))
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

	result := &MBReleaseGroupResult{
		MBID:             detail.ID,
		Title:            detail.Title,
		PrimaryType:      detail.PrimaryType,
		FirstReleaseDate: detail.FirstReleaseDate,
		ArtistMBID:       artistMBID,
		ArtistName:       artistName,
		Rating:           rating,
	}
	for _, st := range detail.SecondaryTypeList {
		result.SecondaryTypes = append(result.SecondaryTypes, st.Name)
	}
	for _, g := range detail.Genres {
		result.Genres = append(result.Genres, g.Name)
	}
	for _, t := range detail.Tags {
		result.Tags = append(result.Tags, t.Name)
	}
	return result, nil
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
