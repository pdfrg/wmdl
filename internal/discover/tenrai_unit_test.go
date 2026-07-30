package discover

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestTenraiToScrapedItem(t *testing.T) {
	p := &TenraiAnimeProvider{}
	a := tenraiAnime{
		MalID:    1,
		Title:    "Test Anime",
		TitleEn:  "Test Anime English",
		Type:     "TV",
		Episodes: 12,
		Status:   "finished_airing",
		Source:   "original",
		Score:    8.5,
		ScoredBy: 1000,
		Rank:     100,
		Members:  50000,
		Synopsis: "A great anime.",
		Rating:   "PG-13",
		Year:     2025,
		Images: struct {
			JPG struct {
				LargeImageURL string `json:"large_image_url"`
			} `json:"jpg"`
		}{
			JPG: struct {
				LargeImageURL string `json:"large_image_url"`
			}{LargeImageURL: "https://example.com/img.jpg"},
		},
		Genres: []tenraiNamedItem{
			{Name: "Action"}, {Name: "Sci-Fi"},
		},
		Studios: []struct {
			Name string `json:"name"`
		}{{Name: "Studio 1"}},
		Themes:       []tenraiNamedItem{{Name: "Mecha"}},
		Demographics: []tenraiNamedItem{{Name: "Shounen"}},
	}

	item := p.toScrapedItem(a, "tenrai")
	assert.Equal(t, "Test Anime English", item.Title)
	assert.Equal(t, 2025, item.Year)
	assert.Equal(t, model.MediaTypeAnime, item.MediaType)
	assert.Equal(t, model.ReleaseStreaming, item.ReleaseType)
	assert.Equal(t, "tenrai", item.Source)
	assert.Equal(t, 1, item.MalID)
	assert.Equal(t, 8.5, item.ImdbRating)
	assert.Equal(t, "TV", item.AnimeType)
	assert.Equal(t, 12, item.AnimeEpisodes)
	assert.Equal(t, "Studio 1", item.AnimeStudio)
	assert.Equal(t, "Action, Sci-Fi", item.Genres)
}

func TestTenraiToScrapedItemB(t *testing.T) {
	p := &TenraiAnimeProvider{}
	a := tenraiAnime{
		MalID:    2,
		Title:    "Airing Anime",
		TitleEn:  "Airing Anime English",
		Type:     "TV",
		Episodes: 24,
		Status:   "currently_airing",
		Score:    7.5,
		Members:  10000,
		Aired: struct {
			From string `json:"from"`
			To   string `json:"to"`
		}{From: "2025-01-01T00:00:00+00:00", To: "2025-06-30T00:00:00+00:00"},
	}

	item := p.toScrapedItemB(a)
	assert.Equal(t, "Airing Anime English", item.Title)
	assert.Equal(t, "tenrai-airing", item.Source)
	assert.Contains(t, item.Notes, "airing|end=2025-06-30|eps=24")
}

func TestJoinTenraiNames(t *testing.T) {
	assert.Equal(t, "", joinTenraiNames(nil))
	assert.Equal(t, "Action", joinTenraiNames([]tenraiNamedItem{{Name: "Action"}}))
	assert.Equal(t, "Action, Sci-Fi", joinTenraiNames([]tenraiNamedItem{{Name: "Action"}, {Name: "Sci-Fi"}}))
}

func TestTenraiFetchPage(t *testing.T) {
	resp := tenraiAnimeResponse{
		Pagination: tenraiPagination{
			HasNextPage: false,
			Items: struct {
				Count int `json:"count"`
				Total int `json:"total"`
			}{Count: 1, Total: 1},
		},
		Data: []tenraiAnime{{
			MalID: 1, Title: "Test", Type: "TV",
			Score: 8.0, Members: 1000,
			Aired: struct {
				From string `json:"from"`
				To   string `json:"to"`
			}{From: "2025-01-01T00:00:00+00:00", To: "2025-06-30T00:00:00+00:00"},
			Images: struct {
				JPG struct {
					LargeImageURL string `json:"large_image_url"`
				} `json:"jpg"`
			}{},
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/anime", r.URL.Path)
		assert.Equal(t, "complete", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewTenraiAnimeProvider(config.AnimeConfig{})
	p.client = srv.Client()

	results, err := p.fetchPage(srv.URL + "/v1/anime?status=complete&page=1&limit=25")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Test", results[0].Title)
	assert.Equal(t, 8.0, results[0].Score)
}

func TestTenraiFetchPageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal error`))
	}))
	defer srv.Close()

	p := NewTenraiAnimeProvider(config.AnimeConfig{})
	p.client = srv.Client()

	_, err := p.fetchPage(srv.URL + "/anime")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenrai returned 500")
}
