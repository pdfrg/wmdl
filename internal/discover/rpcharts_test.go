package discover

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/testutil"
)

func loadRPChartsFixture(t *testing.T) []byte {
	t.Helper()
	return testutil.LoadFixture(t, "testdata", "rpcharts.json")
}

func loadRPChartsLiveFixture(t *testing.T) []byte {
	t.Helper()
	return testutil.LoadFixture(t, "testdata", "rpcharts_live.json")
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

func TestRPChartsScrapeLiveFixture(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsLiveFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider([]string{"all"})
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 20)

	for _, item := range items {
		assert.NotEmpty(t, item.Title)
		assert.NotEmpty(t, item.ArtistName)
		assert.Greater(t, item.Year, 0)
		assert.Equal(t, model.MediaTypeMusic, item.MediaType)
		assert.Equal(t, "rpcharts", item.Source)
		assert.Contains(t, item.ReleaseDate, "-")
		_, err := time.Parse("2006-01-02", item.ReleaseDate)
		assert.NoError(t, err, "ReleaseDate %q should be YYYY-MM-DD", item.ReleaseDate)
		assert.True(t, strings.HasPrefix(item.Notes, "https://radioparadise.com/music/album/"), "Notes %q", item.Notes)
		assert.NotEmpty(t, item.ImageURL)
	}

	first := items[0]
	assert.Equal(t, "Dirt", first.Title)
	assert.Equal(t, "Alice In Chains", first.ArtistName)
	assert.Equal(t, 1993, first.Year)
	assert.Equal(t, "2026-07-31", first.ReleaseDate)
	assert.Equal(t, "https://radioparadise.com/music/album/30592", first.Notes)
}
