package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/time/rate"
)

type TMDBClient struct {
	apiKey      string
	accessToken string
	http        *http.Client
	limiter     *rate.Limiter
}

func NewTMDBClient(apiKey, accessToken string) *TMDBClient {
	return &TMDBClient{
		apiKey:      apiKey,
		accessToken: accessToken,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		limiter: rate.NewLimiter(rate.Limit(8), 1),
	}
}

func (c *TMDBClient) Ping(ctx context.Context) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.themoviedb.org/3/configuration", nil)
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb unreachable: %w", err)
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("tmdb: API key rejected (HTTP 401) — check tmdb.api_key or tmdb.access_token in config")
	default:
		return fmt.Errorf("tmdb: unexpected HTTP %d", resp.StatusCode)
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
	CollectionID     int
	CollectionName   string
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
		fmt.Sscanf(date[:4], "%d", &y)
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

const tmdbBase = "https://api.themoviedb.org/3"

func (c *TMDBClient) SearchMulti(ctx context.Context, query string, year int) (*TMDBMultiResult, error) {
	u, _ := url.Parse(tmdbBase + "/search/multi")
	q := u.Query()
	q.Set("query", query)
	if year > 0 {
		q.Set("year", fmt.Sprintf("%d", year))
	}
	u.RawQuery = q.Encode()
	return c.searchJSON(ctx, u)
}

func (c *TMDBClient) SearchMovie(ctx context.Context, query string, year int) (*TMDBMultiResult, error) {
	u, _ := url.Parse(tmdbBase + "/search/movie")
	q := u.Query()
	q.Set("query", query)
	if year > 0 {
		q.Set("year", fmt.Sprintf("%d", year))
	}
	u.RawQuery = q.Encode()
	result, err := c.searchJSON(ctx, u)
	if err != nil {
		return nil, err
	}
	for i := range result.Results {
		result.Results[i].MediaType = "movie"
	}
	return result, nil
}

func (c *TMDBClient) SearchTV(ctx context.Context, query string, year int) (*TMDBMultiResult, error) {
	u, _ := url.Parse(tmdbBase + "/search/tv")
	q := u.Query()
	q.Set("query", query)
	if year > 0 {
		q.Set("first_air_date_year", fmt.Sprintf("%d", year))
	}
	u.RawQuery = q.Encode()
	result, err := c.searchJSON(ctx, u)
	if err != nil {
		return nil, err
	}
	for i := range result.Results {
		result.Results[i].MediaType = "tv"
	}
	return result, nil
}

func (c *TMDBClient) searchJSON(ctx context.Context, u *url.URL) (*TMDBMultiResult, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

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
	res, err := c.searchWithFallback(ctx, query, year, preferType)
	if err != nil {
		return nil, err
	}

	qLower := strings.ToLower(query)

	var best *tmdbCandidate

	for _, r := range res.Results {
		if preferType == "" && r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}

		if preferType != "" && r.MediaType != preferType {
			continue
		}

		title := r.Title
		if title == "" {
			title = r.Name
		}
		origTitle := r.OriginalTitle
		if origTitle == "" {
			origTitle = r.OriginalName
		}

		titleLower := strings.ToLower(title)
		origLower := strings.ToLower(origTitle)

		var matchScore int
		if titleLower == qLower || (origLower != "" && origLower == qLower) {
			matchScore = 20
		} else if matchTitle(qLower, title) || (origLower != "" && matchTitle(qLower, origTitle)) {
			matchScore = 10
		} else {
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

		yearScore := 0
		if year > 0 && releaseYear > 0 {
			diff := year - releaseYear
			if diff < 0 {
				diff = -diff
			}
			switch diff {
			case 0:
				yearScore = 5
			case 1:
				yearScore = 2
			default:
				yearScore = -3
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
			score: matchScore + yearScore,
		}

		if best == nil || cand.score > best.score ||
			(cand.score == best.score && cand.enrich.Year > best.enrich.Year) {
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

		if enrich.MediaType == "movie" && details.BelongsToCollection != nil {
			enrich.CollectionID = details.BelongsToCollection.ID
			enrich.CollectionName = details.BelongsToCollection.Name
		}
	}

	return enrich, nil
}

// enrichByID builds a TMDBEnrichment from a known TMDB ID, skipping the
// search/scoring phase. Used when the TMDB ID is already known (e.g. from
// the tmdb-discover scraper).
func (c *TMDBClient) enrichByID(ctx context.Context, tmdbID int, mediaType string, rating float64, title string, year int) (*TMDBEnrichment, error) {
	enrich := &TMDBEnrichment{
		TMDBID:    tmdbID,
		MediaType: mediaType,
		Title:     title,
		Year:      year,
		Rating:    rating,
	}

	extIDs, err := c.getExternalIDs(ctx, tmdbID, mediaType)
	if err == nil {
		enrich.IMDbID = extIDs.IMDbID
		enrich.TVDBID = extIDs.TVDBID
	}

	var details *TMDBDetails
	if mediaType == "movie" {
		details, err = c.GetMovieDetails(ctx, tmdbID)
	} else {
		details, err = c.GetTVDetails(ctx, tmdbID)
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
		if mediaType == "movie" && details.BelongsToCollection != nil {
			enrich.CollectionID = details.BelongsToCollection.ID
			enrich.CollectionName = details.BelongsToCollection.Name
		}
		enrich.PosterPath = details.PosterPath
	}

	return enrich, nil
}

// searchWithFallback tries a type-specific search with year, then without,
// then falls back to multi-search when type is unknown.
func (c *TMDBClient) searchWithFallback(ctx context.Context, query string, year int, preferType string) (*TMDBMultiResult, error) {
	if preferType != "" {
		var res *TMDBMultiResult
		var err error

		if preferType == "movie" {
			res, err = c.SearchMovie(ctx, query, year)
		} else {
			res, err = c.SearchTV(ctx, query, year)
		}
		if err != nil {
			return nil, err
		}
		if len(res.Results) > 0 {
			return res, nil
		}

		// Year-filtered returned nothing — retry without year
		if preferType == "movie" {
			res, err = c.SearchMovie(ctx, query, 0)
		} else {
			res, err = c.SearchTV(ctx, query, 0)
		}
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	return c.SearchMulti(ctx, query, year)
}

// matchTitle checks if query matches the start of title at word boundaries.
// Query words must correspond to the initial words of the result title.
// Leading articles (the, a, an) in the title are skipped.
// Non-alphanumeric characters are stripped from each word for matching,
// so "(good" and "good" are treated as the same word.
// Standalone numeric tokens in the query (e.g. sequel numbers like "2")
// are silently skipped during matching, so "Ready or Not 2 - Here I Come"
// can match the TMDB title "Ready or Not: Here I Come".
func matchTitle(qLower, title string) bool {
	tLower := strings.ToLower(title)

	// Skip one leading article in the title
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(tLower, art) {
			tLower = tLower[len(art):]
			break
		}
	}

	qWords := tokenize(qLower)
	tWords := tokenize(tLower)

	if len(qWords) == 0 || len(tWords) == 0 {
		return false
	}

	// Walk both token lists. Skip standalone numeric query tokens
	// (e.g. sequel numbers) that don't appear in the TMDB title.
	qi := 0
	for ti := 0; ti < len(tWords) && qi < len(qWords); ti++ {
		for qi < len(qWords) && isNumeric(qWords[qi]) {
			qi++
		}
		if qi >= len(qWords) {
			break
		}
		if !strings.HasPrefix(qWords[qi], tWords[ti]) {
			return false
		}
		qi++
	}

	// Any remaining query tokens must be numeric
	for qi < len(qWords) {
		if !isNumeric(qWords[qi]) {
			return false
		}
		qi++
	}

	return true
}

// isNumeric reports whether every character in s is a digit.
func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(s) > 0
}

// tokenize splits a string into words, stripping non-alphanumeric characters
// from each word. This ensures "(good" normalizes to "good" for matching.
func tokenize(s string) []string {
	fields := strings.Fields(s)
	result := make([]string, 0, len(fields))
	for _, f := range fields {
		var b strings.Builder
		for _, r := range f {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(unicode.ToLower(r))
			}
		}
		if b.Len() > 0 {
			result = append(result, b.String())
		}
	}
	return result
}

func (c *TMDBClient) getExternalIDs(ctx context.Context, tmdbID int, mediaType string) (*TMDBExternalIDs, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

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

func (c *TMDBClient) setAuth(req *http.Request) {
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	} else if c.apiKey != "" {
		q := req.URL.Query()
		q.Set("api_key", c.apiKey)
		req.URL.RawQuery = q.Encode()
	}
}
