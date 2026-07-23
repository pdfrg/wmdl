package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

type TMDBMultiResult struct {
	Page    int `json:"page"`
	Results []struct {
		ID            int     `json:"id"`
		MediaType     string  `json:"media_type"`
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
	MediaType        string
	Title            string
	Year             int
	Rating           float64
	IMDbID           string
	TVDBID           int
	Overview         string
	Genres           string
	Runtime          int
	PosterPath       string
	OriginalLanguage string
	OriginCountry    string
	CollectionID     int
	CollectionName   string
}

type tmdbCandidate struct {
	enrich *TMDBEnrichment
	score  int
}

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
			_, _ = fmt.Sscanf(releaseDate[:4], "%d", &releaseYear)
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

	extIDs, err := c.getExternalIDs(ctx, enrich.TMDBID, enrich.MediaType)
	if err == nil {
		enrich.IMDbID = extIDs.IMDbID
		enrich.TVDBID = extIDs.TVDBID
	}

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

func matchTitle(qLower, title string) bool {
	tLower := strings.ToLower(title)

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

	for qi < len(qWords) {
		if !isNumeric(qWords[qi]) {
			return false
		}
		qi++
	}

	return true
}

func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(s) > 0
}

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
