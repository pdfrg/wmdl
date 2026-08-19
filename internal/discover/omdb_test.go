package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRatingsBenignNotFound(t *testing.T) {
	tests := []struct {
		name      string
		omdbError string
	}{
		{"incorrect imdb id", "Incorrect IMDb ID."},
		{"movie not found", "Movie not found!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"Response":"False","Error":"` + tt.omdbError + `"}`))
			}))
			defer srv.Close()

			c := NewOMDBClient("testkey")
			c.http = srv.Client()
			c.baseURL = srv.URL

			_, err := c.FetchRatings(context.Background(), "tt0000000")
			if !isOMDBNotFoundError(err) {
				t.Fatalf("expected benign not-found error, got: %v", err)
			}
		})
	}
}

func TestFetchRatingsOtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Response":"False","Error":"Rate limit exceeded."}`))
	}))
	defer srv.Close()

	c := NewOMDBClient("testkey")
	c.http = srv.Client()
	c.baseURL = srv.URL

	_, err := c.FetchRatings(context.Background(), "tt0000000")
	if err == nil {
		t.Fatal("expected error")
	}
	if isOMDBNotFoundError(err) {
		t.Fatalf("other OMDB errors must not be classified as benign not-found, got: %v", err)
	}
}

func TestFetchRatingsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Title":"Test","Response":"True","imdbRating":"7.4","imdbVotes":"45169","Metascore":"70"}`))
	}))
	defer srv.Close()

	c := NewOMDBClient("testkey")
	c.http = srv.Client()
	c.baseURL = srv.URL

	data, err := c.FetchRatings(context.Background(), "tt0000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.ImdbRating != 7.4 || data.ImdbVotes != 45169 || data.MetacriticScore != 70 {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestIsRTSearchURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.rottentomatoes.com/search?search=In+Love+Forever", true},
		{"https://www.rottentomatoes.com/m/anything", false},
		{"https://www.rottentomatoes.com/tv/anything", false},
		{"https://www.rottentomatoes.com/m/slug_2025", false},
	}
	for _, tt := range tests {
		if got := isRTSearchURL(tt.url); got != tt.want {
			t.Errorf("isRTSearchURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
