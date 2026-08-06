package discover

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/testutil"
)

func loadRPChartsFixture(t *testing.T) []byte {
	t.Helper()
	return testutil.LoadFixture(t, "testdata", "rpcharts.json")
}

func testRPChartsServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
}

func TestRPChartsScrapeAllStations(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 3)

	first := items[0]
	assert.Equal(t, "Ensoulment", first.Title)
	assert.Equal(t, "The The", first.ArtistName)
	assert.Equal(t, 2024, first.Year)
	assert.Equal(t, "2026-07-30", first.ReleaseDate)
	assert.Equal(t, model.MediaTypeMusic, first.MediaType)
	assert.Equal(t, "rpcharts", first.Source)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26434.jpg", first.ImageURL)
	assert.Equal(t, "https://radioparadise.com/music/album/26434", first.Notes)

	// Serenity's Rule 42 entry is excluded by the All Stations selection.
	for _, item := range items {
		assert.NotEqual(t, "Rule 42 - Mellow Ambient", item.Title)
	}
}

func TestRPChartsScrapeExplicitAllSlug(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider([]string{"all"})
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestRPChartsScrapeSpecificStations(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider([]string{"main", "rockit"})
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 3)

	titles := make(map[string]bool)
	for _, item := range items {
		titles[item.Title] = true
	}
	assert.True(t, titles["Ensoulment"])
	assert.True(t, titles["Curio"])
	assert.True(t, titles["Rapture"])
	assert.False(t, titles["Rule 42 - Mellow Ambient"])
}

func TestRPChartsScrapeEmptyWeekly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"version": 1,
			"stations": [
				{"channel": -1, "slug": "", "name": "All Stations",
				 "weekly": {"period": "Week of July 26, 2026", "songs": [], "artists": [], "albums": [], "new_songs": [], "new_artists": [], "new_albums": []}}
			]
		}`))
	}))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestRPChartsScrapeMissingAllStations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version": 1, "stations": [{"channel": 0, "slug": "main", "name": "Main Mix"}]}`))
	}))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestRPChartsScrapeHTTPError(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusInternalServerError, []byte("boom"))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	_, err := p.Scrape()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestRPChartsScrapeInvalidJSON(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, []byte("{not json"))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	_, err := p.Scrape()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding rpcharts data")
}

func TestRPMapItemNoAlbumID(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 3)
	for _, item := range items {
		assert.True(t, strings.HasPrefix(item.Notes, "https://radioparadise.com/music/album/"))
	}
}
