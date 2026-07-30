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

func TestRadarrNewClient(t *testing.T) {
	t.Run("default timeout", func(t *testing.T) {
		c := NewRadarrClient("http://example.com", "key", 0)
		assert.Equal(t, "http://example.com", c.baseURL)
		assert.Equal(t, "key", c.apiKey)
	})

	t.Run("trailing slash stripped", func(t *testing.T) {
		c := NewRadarrClient("http://example.com/", "key", 10)
		assert.Equal(t, "http://example.com", c.baseURL)
	})
}

func TestRadarrPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.Ping(context.Background()))
}

func TestRadarrGetQualityProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"Any"},{"id":2,"name":"HD-1080p"},{"id":3,"name":"HD-4K"}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	profiles, err := c.GetQualityProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 3)
	assert.Equal(t, 1, profiles[0].ID)
	assert.Equal(t, "Any", profiles[0].Name)
	assert.Equal(t, "HD-4K", profiles[2].Name)
}

func TestRadarrGetRootFolders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	folders, err := c.GetRootFolders(context.Background())
	require.NoError(t, err)
	require.Len(t, folders, 1)
	assert.Equal(t, "/movies", folders[0].Path)
}

func TestRadarrLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tmdbId":603,"title":"The Matrix","year":1999}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	movie, err := c.Lookup(context.Background(), 603)
	require.NoError(t, err)
	require.NotNil(t, movie)
	assert.Equal(t, "The Matrix", movie.Title)
	assert.Equal(t, 1999, movie.Year)
}

func TestRadarrLookupNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	movie, err := c.Lookup(context.Background(), 999)
	require.NoError(t, err)
	assert.Nil(t, movie)
}

func TestRadarrLookupByTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tmdbId":603,"title":"The Matrix","year":1999}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	movie, err := c.LookupByTitle(context.Background(), "The Matrix")
	require.NoError(t, err)
	require.NotNil(t, movie)
	assert.Equal(t, 603, movie.TMDBID)
}

func TestRadarrAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RadarrMovie
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, 603, req.TMDBID)
		assert.Equal(t, "The Matrix", req.Title)
		assert.Equal(t, 1999, req.Year)
		assert.True(t, req.Monitored)
		assert.Equal(t, "physical/web", req.MinimumAvailability)
		assert.Equal(t, 1, req.QualityProfileID)
		assert.Equal(t, "/movies", req.RootFolderPath)

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42,"tmdbId":603,"title":"The Matrix","year":1999,"overview":"","monitored":true,"minimumAvailability":"announced","qualityProfileId":1,"rootFolderPath":"/movies","folder":"/movies/The Matrix (1999)","hasFile":true,"isExisting":false,"isAvailable":true,"status":"released"}`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	movie, err := c.Add(context.Background(), 603, "The Matrix", 1999, AddMovieOptions{
		Monitored:           true,
		MinimumAvailability: "physical/web",
		QualityProfileID:    1,
		RootFolderPath:      "/movies",
		SearchNow:           true,
	})
	require.NoError(t, err)
	require.NotNil(t, movie)
	assert.Equal(t, 42, movie.ID)
	assert.Equal(t, "The Matrix", movie.Title)
	assert.True(t, movie.HasFile)
}

func TestRadarrExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"tmdbId":603,"title":"The Matrix","year":1999},
			{"tmdbId":680,"title":"Pulp Fiction","year":1994}
		]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)

	found, err := c.Exists(context.Background(), 603)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "The Matrix", found.Title)

	notFound, err := c.Exists(context.Background(), 999)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestRadarrExistsCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`[{"tmdbId":603,"title":"The Matrix","year":1999}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	c.Exists(context.Background(), 603)
	c.Exists(context.Background(), 603)
	assert.Equal(t, 1, calls, "should use cache on second call")
}

func TestRadarrGetCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"title":"The Matrix Collection","tmdbId":107,"movies":[]}]`))
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	cols, err := c.GetCollections(context.Background())
	require.NoError(t, err)
	require.Len(t, cols, 1)
	assert.Equal(t, "The Matrix Collection", cols[0].Name)
	assert.Equal(t, 107, cols[0].TMDBID)
}

func TestRadarrTriggerSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cmd radarrCommand
		json.NewDecoder(r.Body).Decode(&cmd)
		assert.Equal(t, "MoviesSearch", cmd.Name)
		assert.Equal(t, []int{42}, cmd.MovieIDs)

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.TriggerSearch(context.Background(), 42))
}

func TestRadarrGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	err := c.get(context.Background(), "/api/v3/health", nil)
	assert.ErrorContains(t, err, "401")
}

func TestRadarrPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewRadarrClient(srv.URL, "key", 10)
	err := c.post(context.Background(), "/api/v3/movie", []byte(`{}`), nil)
	assert.ErrorContains(t, err, "400")
}

func TestRadarrGetAllMoviesCacheHit(t *testing.T) {
	c := NewRadarrClient("http://example.com", "key", 10)
	c.moviesCache = []RadarrMovie{{Title: "Cached Movie", TMDBID: 1}}

	movies, err := c.GetAllMovies(context.Background())
	require.NoError(t, err)
	require.Len(t, movies, 1)
	assert.Equal(t, "Cached Movie", movies[0].Title)
}

func TestRadarrSetAllMovies(t *testing.T) {
	c := NewRadarrClient("http://example.com", "key", 10)
	c.SetAllMovies([]RadarrMovie{{Title: "Set Movie"}})
	assert.NotNil(t, c.moviesCache)
	assert.Len(t, c.moviesCache, 1)
}
