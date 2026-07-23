package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

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

type TMDBTVSeason struct {
	SeasonNumber int    `json:"season_number"`
	AirDate      string `json:"air_date"`
	EpisodeCount int    `json:"episode_count"`
}

func (s *TMDBTVSeason) AirDatePassed() bool {
	if s.AirDate == "" {
		return false
	}
	return s.AirDate <= time.Now().Format("2006-01-02")
}

type TMDBDetails struct {
	Title        string `json:"title,omitempty"`
	Name         string `json:"name,omitempty"`
	ReleaseDate  string `json:"release_date,omitempty"`
	FirstAirDate string `json:"first_air_date,omitempty"`
	Overview     string `json:"overview"`
	Genres       []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	Runtime             int      `json:"runtime"`
	VoteAverage         float64  `json:"vote_average"`
	IMDbID              string   `json:"imdb_id,omitempty"`
	OriginalLanguage    string   `json:"original_language"`
	OriginCountry       []string `json:"origin_country"`
	BelongsToCollection *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"belongs_to_collection"`
	Seasons    []TMDBTVSeason `json:"seasons,omitempty"`
	PosterPath string         `json:"poster_path"`
}

func (d *TMDBDetails) DisplayTitle() string {
	if d.Title != "" {
		return d.Title
	}
	return d.Name
}

func (d *TMDBDetails) DisplayYear() int {
	date := d.ReleaseDate
	if date == "" {
		date = d.FirstAirDate
	}
	if len(date) >= 4 {
		var y int
		_, _ = fmt.Sscanf(date[:4], "%d", &y)
		return y
	}
	return 0
}

type TMDBCollectionPart struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
}

type TMDBCollection struct {
	ID    int                  `json:"id"`
	Name  string               `json:"name"`
	Parts []TMDBCollectionPart `json:"parts"`
}

type TMDBContentRatings struct {
	Results []struct {
		Iso3166_1 string `json:"iso_3166_1"`
		Rating    string `json:"rating"`
	} `json:"results"`
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
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

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
				_, _ = fmt.Sscanf(date[:4], "%d", &year)
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

func (c *TMDBClient) GetCollection(ctx context.Context, collectionID int) (*TMDBCollection, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/collection/%d", tmdbBase, collectionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching TMDB collection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB collection returned %d", resp.StatusCode)
	}
	var coll TMDBCollection
	if err := json.NewDecoder(resp.Body).Decode(&coll); err != nil {
		return nil, err
	}
	return &coll, nil
}

func (c *TMDBClient) GetMovieDetails(ctx context.Context, tmdbID int) (*TMDBDetails, error) {
	return c.getDetails(ctx, fmt.Sprintf("%s/movie/%d", tmdbBase, tmdbID))
}

func (c *TMDBClient) GetTVDetails(ctx context.Context, tmdbID int) (*TMDBDetails, error) {
	return c.getDetails(ctx, fmt.Sprintf("%s/tv/%d", tmdbBase, tmdbID))
}

func (c *TMDBClient) getDetails(ctx context.Context, url string) (*TMDBDetails, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

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

func (c *TMDBClient) GetTVRating(ctx context.Context, tmdbID int) string {
	if err := c.limiter.Wait(ctx); err != nil {
		return ""
	}

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

func (c *TMDBClient) GetUSCertification(ctx context.Context, tmdbID int, mediaType string) string {
	if err := c.limiter.Wait(ctx); err != nil {
		return ""
	}

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
