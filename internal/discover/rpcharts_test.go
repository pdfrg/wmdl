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
	assert.Equal(t, 2025, first.Year)
	assert.Equal(t, "2026-07-30", first.ReleaseDate)
	assert.Equal(t, model.MediaTypeMusic, first.MediaType)
	assert.Equal(t, "rpcharts", first.Source)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26434.jpg", first.ImageURL)
	assert.Equal(t, "https://radioparadise.com/music/album/26434|stations: All Stations", first.Notes)

	// Serenity's Rule 42 entry is excluded by the All Stations selection.
	for _, item := range items {
		assert.NotEqual(t, "Rule 42 - Mellow Ambient", item.Title)
	}
}

// TestRPChartsScrapeFiltersOldBackCatalog verifies that the top-albums list
// is filtered to the current and previous release years, dropping rarely
// played back-catalog entries. The fixture's "Old Back Catalog" (year 1990)
// must be excluded while current/current-1 entries are kept.
func TestRPChartsScrapeFiltersOldBackCatalog(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 3)

	for _, item := range items {
		assert.GreaterOrEqual(t, item.Year, time.Now().Year()-1, "title %q", item.Title)
		assert.NotEqual(t, "Old Back Catalog", item.Title)
	}
}

// TestRPChartsScrapeUsesAlbumsNotNewAlbums verifies that selectedAlbums reads
// the weekly top "albums" list rather than the "new_albums" list.
func TestRPChartsScrapeUsesAlbumsNotNewAlbums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"version": 1,
			"stations": [
				{"channel": -1, "slug": "", "name": "All Stations",
				 "weekly": {"period": "p", "songs": [], "artists": [],
				  "albums": [{"rank": 1, "name": "Top Album", "sub": "A", "year": 2026, "album_id": "1"}],
				  "new_albums": [{"rank": 1, "name": "Brand New Album", "sub": "B", "year": 2026, "album_id": "2"}]}}
			]
		}`))
	}))
	defer srv.Close()

	p := NewRPChartsProvider(nil)
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Top Album", items[0].Title)
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

func TestRPChartsScrapeStationNamesInNotes(t *testing.T) {
	srv := testRPChartsServer(t, http.StatusOK, loadRPChartsFixture(t))
	defer srv.Close()

	p := NewRPChartsProvider([]string{"main", "rockit", "serenity"})
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)

	// Station display names are appended to the notes after the album URL.
	byTitle := make(map[string]string)
	for _, item := range items {
		byTitle[item.Title] = item.Notes
	}
	assert.Equal(t,
		"https://radioparadise.com/music/album/26434|stations: Main Mix",
		byTitle["Ensoulment"])
	assert.Equal(t,
		"https://radioparadise.com/music/album/26501|stations: Main Mix",
		byTitle["Curio"])
	assert.Equal(t,
		"https://radioparadise.com/music/album/99999|stations: Serenity",
		byTitle["Rule 42 - Mellow Ambient"])
}

// TestRPChartsScrapeMergesStationsForDuplicateAlbums verifies that an album
// appearing on several selected stations is emitted once, with all station
// display names (deduplicated) in its notes.
func TestRPChartsScrapeMergesStationsForDuplicateAlbums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"version": 1,
			"stations": [
				{"channel": 0, "slug": "main", "name": "Main Mix",
				 "weekly": {"period": "p", "albums": [
					{"rank": 1, "name": "Shared Album", "sub": "Artist", "year": 2026, "album_id": "1"},
					{"rank": 2, "name": "Main Only", "sub": "Artist", "year": 2026, "album_id": "2"}]}},
				{"channel": 2, "slug": "rockit", "name": "RockIt!",
				 "weekly": {"period": "p", "albums": [
					{"rank": 1, "name": "Shared Album", "sub": "Artist", "year": 2026, "album_id": "1"}]}}
			]
		}`))
	}))
	defer srv.Close()

	p := NewRPChartsProvider([]string{"main", "rockit"})
	p.endpoint = srv.URL

	items, err := p.Scrape()
	require.NoError(t, err)
	require.Len(t, items, 2)

	byTitle := make(map[string]string)
	for _, item := range items {
		byTitle[item.Title] = item.Notes
	}
	assert.Equal(t,
		"https://radioparadise.com/music/album/1|stations: Main Mix, RockIt!",
		byTitle["Shared Album"])
	assert.Equal(t,
		"https://radioparadise.com/music/album/2|stations: Main Mix",
		byTitle["Main Only"])
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
	require.Len(t, items, 9)

	for _, item := range items {
		assert.NotEmpty(t, item.Title)
		assert.NotEmpty(t, item.ArtistName)
		assert.GreaterOrEqual(t, item.Year, time.Now().Year()-1, "title %q", item.Title)
		assert.Equal(t, model.MediaTypeMusic, item.MediaType)
		assert.Equal(t, "rpcharts", item.Source)
		assert.Contains(t, item.ReleaseDate, "-")
		_, err := time.Parse("2006-01-02", item.ReleaseDate)
		assert.NoError(t, err, "ReleaseDate %q should be YYYY-MM-DD", item.ReleaseDate)
		assert.True(t, strings.HasPrefix(item.Notes, "https://radioparadise.com/music/album/"), "Notes %q", item.Notes)
		assert.NotEmpty(t, item.ImageURL)
	}

	// The top-albums list, filtered to current/current-1 release years.
	// Ensoulment (2024) and the older back catalog are dropped.
	first := items[0]
	assert.Equal(t, "I Built You a Tower", first.Title)
	assert.Equal(t, "Death Cab for Cutie", first.ArtistName)
	assert.Equal(t, 2026, first.Year)
	assert.Equal(t, "2026-05-05", first.ReleaseDate)
	assert.Equal(t, "https://radioparadise.com/music/album/27518|stations: All Stations", first.Notes)

	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	assert.NotContains(t, titles, "Ensoulment")
}
