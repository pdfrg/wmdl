package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSonarrNewClient(t *testing.T) {
	c := NewSonarrClient("http://example.com", "key", 0)
	assert.Equal(t, "http://example.com", c.baseURL)
	assert.Equal(t, "key", c.apiKey)
}

func TestSonarrPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.Ping(context.Background()))
}

func TestSonarrGetQualityProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"HD-1080p"},{"id":2,"name":"HD-4K"}]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	profiles, err := c.GetQualityProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.Equal(t, "HD-1080p", profiles[0].Name)
}

func TestSonarrGetLanguageProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"English"},{"id":2,"name":"Any"}]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	profiles, err := c.GetLanguageProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.Equal(t, "English", profiles[0].Name)
}

func TestSonarrGetRootFolders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"path":"/tv"}]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	folders, err := c.GetRootFolders(context.Background())
	require.NoError(t, err)
	require.Len(t, folders, 1)
	assert.Equal(t, "/tv", folders[0].Path)
}

func TestSonarrLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":99,"tvdbId":121361,"title":"Breaking Bad","year":2008,"monitored":true,"seasonFolder":true,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/tv","seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	series, err := c.Lookup(context.Background(), 121361)
	require.NoError(t, err)
	require.NotNil(t, series)
	assert.Equal(t, "Breaking Bad", series.Title)
	assert.Equal(t, 2008, series.Year)
}

func TestSonarrLookupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	series, err := c.Lookup(context.Background(), 999)
	require.NoError(t, err)
	assert.Nil(t, series)
}

func TestSonarrLookupByTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":99,"tvdbId":121361,"title":"Breaking Bad","year":2008,"monitored":true,"seasonFolder":true,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/tv","seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	series, err := c.LookupByTitle(context.Background(), "Breaking Bad")
	require.NoError(t, err)
	require.NotNil(t, series)
	assert.Equal(t, 121361, series.TVDBID)
}

func TestSonarrGetSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":99,"tvdbId":121361,"title":"Breaking Bad","year":2008,"monitored":true,"seasonFolder":true,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/tv","seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	series, err := c.GetSeries(context.Background(), 99)
	require.NoError(t, err)
	require.NotNil(t, series)
	assert.Equal(t, "Breaking Bad", series.Title)
}

func TestSonarrAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SonarrSeries
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, 121361, req.TVDBID)
		assert.Equal(t, "Breaking Bad", req.Title)
		assert.True(t, req.Monitored)
		assert.True(t, req.SeasonFolder)
		assert.Equal(t, 1, req.QualityProfileID)

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":99,"tvdbId":121361,"title":"Breaking Bad","year":2008,"monitored":true,"seasonFolder":true,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/tv","seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	series, err := c.Add(context.Background(), 121361, "Breaking Bad", 2008, AddSeriesOptions{
		Monitored:        true,
		SeasonFolder:     true,
		QualityProfileID: 1,
		RootFolderPath:   "/tv",
		Seasons:          []SonarrSeason{{SeasonNumber: 1, Monitored: true}},
		SearchForMissing: true,
	})
	require.NoError(t, err)
	require.NotNil(t, series)
	assert.Equal(t, 99, series.ID)
}

func TestSonarrExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"tvdbId":121361,"title":"Breaking Bad","year":2008},
			{"tvdbId":81189,"title":"Better Call Saul","year":2015}
		]`))
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)

	found, err := c.Exists(context.Background(), 121361)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "Breaking Bad", found.Title)

	notFound, err := c.Exists(context.Background(), 999)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestSonarrTriggerSeasonSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cmd sonarrCommand
		json.NewDecoder(r.Body).Decode(&cmd)
		assert.Equal(t, "SeasonSearch", cmd.Name)
		assert.Equal(t, 99, cmd.SeriesID)
		assert.Equal(t, 1, cmd.SeasonNumber)

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.TriggerSeasonSearch(context.Background(), 99, 1))
}

func TestSonarrGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	err := c.get(context.Background(), "/api/v3/health", nil)
	assert.ErrorContains(t, err, "401")
}

func TestSonarrPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewSonarrClient(srv.URL, "key", 10)
	err := c.post(context.Background(), "/api/v3/series", []byte(`{}`), nil)
	assert.ErrorContains(t, err, "400")
}

func TestSonarrGetAllSeriesCacheHit(t *testing.T) {
	c := NewSonarrClient("http://example.com", "key", 10)
	c.seriesCache = []SonarrSeries{{Title: "Cached Series", TVDBID: 1}}

	series, err := c.GetAllSeries(context.Background())
	require.NoError(t, err)
	require.Len(t, series, 1)
	assert.Equal(t, "Cached Series", series[0].Title)
}

func TestSonarrSetAllSeries(t *testing.T) {
	c := NewSonarrClient("http://example.com", "key", 10)
	c.SetAllSeries([]SonarrSeries{{Title: "Set Series"}})
	assert.Len(t, c.seriesCache, 1)
}
