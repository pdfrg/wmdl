package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

type OMDBClient struct {
	http    *http.Client
	apiKey  string
	baseURL string
	limiter *rate.Limiter
}

type OMDBResponse struct {
	Title      string            `json:"Title"`
	Year       string            `json:"Year"`
	Rated      string            `json:"Rated"`
	Released   string            `json:"Released"`
	Runtime    string            `json:"Runtime"`
	Genre      string            `json:"Genre"`
	Director   string            `json:"Director"`
	Writer     string            `json:"Writer"`
	Actors     string            `json:"Actors"`
	Plot       string            `json:"Plot"`
	Language   string            `json:"Language"`
	Country    string            `json:"Country"`
	Awards     string            `json:"Awards"`
	Poster     string            `json:"Poster"`
	Ratings    []OMDBRatingEntry `json:"Ratings"`
	Metascore  string            `json:"Metascore"`
	ImdbRating string            `json:"imdbRating"`
	ImdbVotes  string            `json:"imdbVotes"`
	ImdbID     string            `json:"imdbID"`
	Type       string            `json:"Type"`
	DVD        string            `json:"DVD"`
	BoxOffice  string            `json:"BoxOffice"`
	Production string            `json:"Production"`
	Website    string            `json:"Website"`
	Response   string            `json:"Response"`
	Error      string            `json:"Error,omitempty"`
}

type OMDBRatingEntry struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

type OMDBData struct {
	ImdbRating      float64
	MetacriticScore float64
	ImdbVotes       int64
	Awards          string
	BoxOffice       string
	Director        string
	Writer          string
	Actors          string
}

func NewOMDBClient(apiKey string) *OMDBClient {
	return &OMDBClient{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiKey:  apiKey,
		baseURL: "https://www.omdbapi.com",
		limiter: rate.NewLimiter(rate.Limit(10), 1),
	}
}

// errOMDBNotFound is returned when OMDB reports that an IMDb ID is unknown
// (not yet in OMDB's database). This is expected for very new or obscure
// titles whose TMDB-provided ID hasn't propagated to OMDB, so callers can
// treat it as a benign miss rather than a real failure.
var errOMDBNotFound = errors.New("OMDB: ID not found")

// isOMDBNotFoundError reports whether err is the benign "ID not in OMDB" miss.
func isOMDBNotFoundError(err error) bool {
	return errors.Is(err, errOMDBNotFound)
}

func (c *OMDBClient) FetchRatings(ctx context.Context, imdbID string) (*OMDBData, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}

	url := fmt.Sprintf("%s/?i=%s&apikey=%s", c.baseURL, imdbID, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching OMDB data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OMDB returned %d for %s", resp.StatusCode, imdbID)
	}

	var omdbResp OMDBResponse
	if err := json.NewDecoder(resp.Body).Decode(&omdbResp); err != nil {
		return nil, fmt.Errorf("decoding OMDB response: %w", err)
	}

	if omdbResp.Response == "False" {
		switch omdbResp.Error {
		case "Incorrect IMDb ID.", "Movie not found!":
			return nil, fmt.Errorf("%w (%s)", errOMDBNotFound, imdbID)
		default:
			return nil, fmt.Errorf("OMDB error for %s: %s", imdbID, omdbResp.Error)
		}
	}

	result := &OMDBData{}

	if omdbResp.ImdbRating != "" && omdbResp.ImdbRating != "N/A" {
		if v, err := strconv.ParseFloat(omdbResp.ImdbRating, 64); err == nil {
			result.ImdbRating = v
		}
	}

	if omdbResp.Metascore != "" && omdbResp.Metascore != "N/A" {
		if v, err := strconv.ParseFloat(omdbResp.Metascore, 64); err == nil {
			result.MetacriticScore = v
		}
	}

	if omdbResp.ImdbVotes != "" && omdbResp.ImdbVotes != "N/A" {
		cleaned := strings.ReplaceAll(omdbResp.ImdbVotes, ",", "")
		if v, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
			result.ImdbVotes = v
		}
	}

	if omdbResp.Awards != "" && omdbResp.Awards != "N/A" {
		result.Awards = omdbResp.Awards
	}
	if omdbResp.BoxOffice != "" && omdbResp.BoxOffice != "N/A" {
		result.BoxOffice = omdbResp.BoxOffice
	}
	if omdbResp.Director != "" && omdbResp.Director != "N/A" {
		result.Director = omdbResp.Director
	}
	if omdbResp.Writer != "" && omdbResp.Writer != "N/A" {
		result.Writer = omdbResp.Writer
	}
	if omdbResp.Actors != "" && omdbResp.Actors != "N/A" {
		result.Actors = omdbResp.Actors
	}

	return result, nil
}
