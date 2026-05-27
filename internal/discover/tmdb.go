package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TMDBClient struct {
	apiKey      string
	accessToken string
	http        *http.Client
}

func NewTMDBClient(apiKey, accessToken string) *TMDBClient {
	return &TMDBClient{
		apiKey:      apiKey,
		accessToken: accessToken,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type TMDBMultiResult struct {
	Page    int `json:"page"`
	Results []struct {
		ID            int     `json:"id"`
		MediaType     string  `json:"media_type"` // "movie" or "tv"
		Title         string  `json:"title,omitempty"`
		Name          string  `json:"name,omitempty"`
		ReleaseDate   string  `json:"release_date,omitempty"`
		FirstAirDate  string  `json:"first_air_date,omitempty"`
		VoteAverage   float64 `json:"vote_average"`
		GenreIDs      []int   `json:"genre_ids"`
		OriginalTitle string  `json:"original_title,omitempty"`
		OriginalName  string  `json:"original_name,omitempty"`
		Overview      string  `json:"overview"`
		PosterPath    string  `json:"poster_path"`
	} `json:"results"`
}

type TMDBExternalIDs struct {
	IMDbID     string `json:"imdb_id"`
	TVDBID     int    `json:"tvdb_id,omitempty"`
	FacebookID string `json:"facebook_id,omitempty"`
}

type TMDBTVExternalIDs struct {
	IMDbID string `json:"imdb_id"`
	TVDBID int    `json:"tvdb_id,omitempty"`
}

type TMDBEnrichment struct {
	TMDBID           int
	MediaType        string // "movie" or "tv"
	Title            string
	Year             int
	Rating           float64
	IMDbID           string
	TVDBID           int
	Overview         string
	Genres           string // comma-separated
	Runtime          int
	PosterPath       string
	OriginalLanguage string // ISO 639-1
	OriginCountry    string // ISO 3166-1, first country
}

type TMDBDiscoverResult struct {
	Page    int `json:"page"`
	Results []struct {
		ID           int     `json:"id"`
		Title        string  `json:"title,omitempty"`
		Name         string  `json:"name,omitempty"`
		ReleaseDate  string  `json:"release_date,omitempty"`
		FirstAirDate string  `json:"first_air_date,omitempty"`
		VoteAverage  float64 `json:"vote_average"`
		Overview     string  `json:"overview"`
		GenreIDs     []int   `json:"genre_ids"`
	} `json:"results"`
	TotalPages int `json:"total_pages"`
}

type StreamingItem struct {
	TMDBID    int
	Title     string
	Year      int
	MediaType string
	Rating    float64
	Date      string
}

func (c *TMDBClient) DiscoverStreamingMovies(ctx context.Context, startDate, endDate string) ([]StreamingItem, error) {
	u, _ := url.Parse(tmdbBase + "/discover/movie")
	q := u.Query()
	q.Set("primary_release_date.gte", startDate)
	q.Set("primary_release_date.lte", endDate)
	q.Set("sort_by", "popularity.desc")
	q.Set("vote_count.gte", "20")
	u.RawQuery = q.Encode()

	return c.discoverStream(ctx, u, "movie")
}

func (c *TMDBClient) DiscoverStreamingTV(ctx context.Context, startDate, endDate string) ([]StreamingItem, error) {
	u, _ := url.Parse(tmdbBase + "/discover/tv")
	q := u.Query()
	q.Set("first_air_date.gte", startDate)
	q.Set("first_air_date.lte", endDate)
	q.Set("sort_by", "popularity.desc")
	q.Set("vote_count.gte", "20")
	u.RawQuery = q.Encode()

	return c.discoverStream(ctx, u, "tv")
}

func (c *TMDBClient) discoverStream(ctx context.Context, u *url.URL, mediaType string) ([]StreamingItem, error) {
	var items []StreamingItem

	for page := 1; page <= 3; page++ {
		q := u.Query()
		q.Set("page", fmt.Sprintf("%d", page))
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		c.setAuth(req)

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("TMDB discover returned %d", resp.StatusCode)
		}

		var result TMDBDiscoverResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		for _, r := range result.Results {
			title := r.Title
			if title == "" {
				title = r.Name
			}
			date := r.ReleaseDate
			if date == "" {
				date = r.FirstAirDate
			}
			year := 0
			if len(date) >= 4 {
				fmt.Sscanf(date[:4], "%d", &year)
			}

			items = append(items, StreamingItem{
				TMDBID:    r.ID,
				Title:     title,
				Year:      year,
				MediaType: mediaType,
				Rating:    r.VoteAverage,
				Date:      date,
			})
		}

		if page >= result.TotalPages {
			break
		}
	}

	return items, nil
}

type TMDBDetails struct {
	Overview string `json:"overview"`
	Genres   []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	Runtime          int      `json:"runtime"`
	VoteAverage      float64  `json:"vote_average"`
	IMDbID           string   `json:"imdb_id,omitempty"`
	OriginalLanguage string   `json:"original_language"`
	OriginCountry    []string `json:"origin_country"`
}

func (c *TMDBClient) GetMovieDetails(ctx context.Context, tmdbID int) (*TMDBDetails, error) {
	return c.getDetails(ctx, fmt.Sprintf("%s/movie/%d", tmdbBase, tmdbID))
}

func (c *TMDBClient) GetTVDetails(ctx context.Context, tmdbID int) (*TMDBDetails, error) {
	return c.getDetails(ctx, fmt.Sprintf("%s/tv/%d", tmdbBase, tmdbID))
}

func (c *TMDBClient) getDetails(ctx context.Context, url string) (*TMDBDetails, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching TMDB details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB details returned %d", resp.StatusCode)
	}

	var details TMDBDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, err
	}
	return &details, nil
}

const tmdbBase = "https://api.themoviedb.org/3"

func (c *TMDBClient) SearchMulti(ctx context.Context, query string, year int) (*TMDBMultiResult, error) {
	u, _ := url.Parse(tmdbBase + "/search/multi")
	q := u.Query()
	q.Set("query", query)
	if year > 0 {
		q.Set("year", fmt.Sprintf("%d", year))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching TMDB: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB search returned %d", resp.StatusCode)
	}

	var result TMDBMultiResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding TMDB response: %w", err)
	}
	return &result, nil
}

type tmdbCandidate struct {
	enrich *TMDBEnrichment
	score  int
}

func (c *TMDBClient) Enrich(ctx context.Context, query string, year int) (*TMDBEnrichment, error) {
	return c.enrichWithPrefs(ctx, query, year, "")
}

func (c *TMDBClient) enrichWithPrefs(ctx context.Context, query string, year int, preferType string) (*TMDBEnrichment, error) {
	res, err := c.SearchMulti(ctx, query, year)
	if err != nil {
		return nil, err
	}

	qLower := strings.ToLower(query)

	var best *tmdbCandidate

	for _, r := range res.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}

		if preferType != "" && r.MediaType != preferType {
			continue
		}

		title := r.Title
		if title == "" {
			title = r.Name
		}

		// Exclude results whose title doesn't start with the search query.
		// Prevents false matches like "Baki Dou: The Invincible Samurai"
		// when searching for "INVINCIBLE".
		if !strings.HasPrefix(strings.ToLower(title), qLower) {
			continue
		}

		releaseDate := r.ReleaseDate
		if releaseDate == "" {
			releaseDate = r.FirstAirDate
		}
		releaseYear := 0
		if len(releaseDate) >= 4 {
			fmt.Sscanf(releaseDate[:4], "%d", &releaseYear)
		}

		score := 0
		if year > 0 && releaseYear > 0 {
			diff := year - releaseYear
			if diff < 0 {
				diff = -diff
			}
			if diff == 0 {
				score += 3
			} else if diff <= 1 {
				score += 1
			}
		}

		cand := &tmdbCandidate{
			enrich: &TMDBEnrichment{
				TMDBID:     r.ID,
				MediaType:  r.MediaType,
				Title:      title,
				Year:       releaseYear,
				Rating:     r.VoteAverage,
				PosterPath: r.PosterPath,
			},
			score: score,
		}

		if best == nil || cand.score > best.score {
			best = cand
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no TMDB results for %q (%d)", query, year)
	}

	enrich := best.enrich

	// Fetch external IDs for IMDb/TVDB
	extIDs, err := c.getExternalIDs(ctx, enrich.TMDBID, enrich.MediaType)
	if err == nil {
		enrich.IMDbID = extIDs.IMDbID
		enrich.TVDBID = extIDs.TVDBID
	}

	// Fetch details (overview, genres, runtime)
	var details *TMDBDetails
	if enrich.MediaType == "movie" {
		details, err = c.GetMovieDetails(ctx, enrich.TMDBID)
	} else {
		details, err = c.GetTVDetails(ctx, enrich.TMDBID)
	}
	if err == nil && details != nil {
		enrich.Overview = details.Overview
		var genreNames []string
		for _, g := range details.Genres {
			genreNames = append(genreNames, g.Name)
		}
		enrich.Genres = strings.Join(genreNames, ", ")
		enrich.Runtime = details.Runtime
		enrich.OriginalLanguage = details.OriginalLanguage
		if len(details.OriginCountry) > 0 {
			n := min(len(details.OriginCountry), 3)
			enrich.OriginCountry = strings.Join(details.OriginCountry[:n], ",")
		}
	}

	return enrich, nil
}

func (c *TMDBClient) getExternalIDs(ctx context.Context, tmdbID int, mediaType string) (*TMDBExternalIDs, error) {
	u := fmt.Sprintf("%s/%s/%d/external_ids", tmdbBase, mediaType, tmdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("external_ids returned %d", resp.StatusCode)
	}

	var ext TMDBExternalIDs
	if err := json.NewDecoder(resp.Body).Decode(&ext); err != nil {
		return nil, err
	}
	return &ext, nil
}

type TMDBContentRatings struct {
	Results []struct {
		Iso3166_1 string `json:"iso_3166_1"`
		Rating    string `json:"rating"`
	} `json:"results"`
}

func (c *TMDBClient) GetTVRating(ctx context.Context, tmdbID int) string {
	path := fmt.Sprintf("%s/tv/%d/content_ratings", tmdbBase, tmdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ""
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result TMDBContentRatings
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	for _, r := range result.Results {
		if r.Iso3166_1 == "US" && r.Rating != "" {
			return r.Rating
		}
	}
	return ""
}

type TMDBReleaseDateResult struct {
	Results []TMDBReleaseDateEntry `json:"results"`
}

type TMDBReleaseDateEntry struct {
	Iso3166_1    string            `json:"iso_3166_1"`
	ReleaseDates []TMDBReleaseDate `json:"release_dates"`
}

type TMDBReleaseDate struct {
	Certification string `json:"certification"`
	Type          int    `json:"type"`
}

func (c *TMDBClient) GetUSCertification(ctx context.Context, tmdbID int, mediaType string) string {
	path := fmt.Sprintf("%s/%s/%d/release_dates", tmdbBase, mediaType, tmdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ""
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result TMDBReleaseDateResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	for _, entry := range result.Results {
		if entry.Iso3166_1 == "US" {
			for _, rd := range entry.ReleaseDates {
				if rd.Certification != "" {
					return rd.Certification
				}
			}
		}
	}
	return ""
}

func (c *TMDBClient) setAuth(req *http.Request) {
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	} else if c.apiKey != "" {
		q := req.URL.Query()
		q.Set("api_key", c.apiKey)
		req.URL.RawQuery = q.Encode()
	}
}
