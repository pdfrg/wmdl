package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/testutil"
)

func TestNewProwlarrClient(t *testing.T) {
	t.Run("default timeout", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 0, nil)
		assert.Equal(t, "http://example.com", c.baseURL)
		assert.Equal(t, "key", c.apiKey)
	})

	t.Run("custom timeout", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 30, nil)
		assert.NotNil(t, c.http)
	})
}

func TestProwlarrSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "movie", r.URL.Query().Get("type"))
		assert.Equal(t, "test movie", r.URL.Query().Get("query"))
		assert.Equal(t, "50", r.URL.Query().Get("limit"))
		assert.Equal(t, "2000", r.URL.Query().Get("categories"))
		assert.Equal(t, "test-key", r.Header.Get("X-Api-Key"))

		data := testutil.LoadFixture(t, "testdata", "prowlarr_search_movies.json")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "test-key", 10, nil)
	results, err := c.Search(context.Background(), SearchParams{
		Query:      "test movie",
		Type:       "movie",
		Limit:      50,
		Categories: []int{CatMovie},
	})
	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, "The Matrix 2025 1080p WEB-DL x265-GROUP", results[0].RawTitle)
	assert.Equal(t, 45, results[0].Seeders)
	assert.Equal(t, int64(4500000000), results[0].SizeBytes)
}

func TestProwlarrSearchTV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tvsearch", r.URL.Query().Get("type"))
		assert.Equal(t, "5000", r.URL.Query().Get("categories"))
		data := testutil.LoadFixture(t, "testdata", "prowlarr_search_tv.json")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	results, err := c.SearchTV(context.Background(), "show name")
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestProwlarrSearchMovies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "movie", r.URL.Query().Get("type"))
		assert.Equal(t, "2000", r.URL.Query().Get("categories"))
		data := testutil.LoadFixture(t, "testdata", "prowlarr_search_movies.json")
		w.Write(data)
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	results, err := c.SearchMovies(context.Background(), "matrix")
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestProwlarrSearchAnime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tvsearch", r.URL.Query().Get("type"))
		assert.Equal(t, "5070", r.URL.Query().Get("categories"))
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	results, err := c.SearchAnime(context.Background(), "anime show")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestProwlarrSearchMusic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "music", r.URL.Query().Get("type"))
		assert.Equal(t, "3000", r.URL.Query().Get("categories"))
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	results, err := c.SearchMusic(context.Background(), "artist album")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestProwlarrSearchWithIndexerIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "1,9,12", r.URL.Query().Get("indexerIds"))
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	results, err := c.Search(context.Background(), SearchParams{
		Query:      "test",
		IndexerIDs: []int{1, 9, 12},
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestProwlarrSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	_, err := c.Search(context.Background(), SearchParams{Query: "test"})
	assert.ErrorContains(t, err, "500")
}

func TestProwlarrPing(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/indexer", r.URL.Path)
			w.Write([]byte("[]"))
		}))
		defer srv.Close()

		c := NewProwlarrClient(srv.URL, "key", 10, nil)
		assert.NoError(t, c.Ping(context.Background()))
	})

	t.Run("error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		c := NewProwlarrClient(srv.URL, "key", 10, nil)
		assert.Error(t, c.Ping(context.Background()))
	})
}

func TestProwlarrGrab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req grabRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, 1, req.IndexerID)
		assert.Equal(t, "guid-123", req.Guid)

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	assert.NoError(t, c.Grab(context.Background(), 1, "guid-123"))
}

func TestProwlarrGrabError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewProwlarrClient(srv.URL, "key", 10, nil)
	assert.Error(t, c.Grab(context.Background(), 1, "guid"))
}

func TestProwlarrGetIndexerName(t *testing.T) {
	t.Run("cache hit", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 10, nil)
		c.mu.Lock()
		c.indexerNames = map[int]string{1: "CachedIndexer"}
		c.mu.Unlock()
		name := c.GetIndexerName(context.Background(), 1)
		assert.Equal(t, "CachedIndexer", name)
	})

	t.Run("fetch and cache", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/indexer", r.URL.Path)
			data, _ := json.Marshal([]indexerInfo{
				{ID: 1, Name: "FetchedIndexer"},
				{ID: 2, Name: "OtherIndexer"},
			})
			w.Write(data)
		}))
		defer srv.Close()

		c := NewProwlarrClient(srv.URL, "key", 10, nil)
		name := c.GetIndexerName(context.Background(), 1)
		assert.Equal(t, "FetchedIndexer", name)
	})

	t.Run("not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("[]"))
		}))
		defer srv.Close()

		c := NewProwlarrClient(srv.URL, "key", 10, nil)
		name := c.GetIndexerName(context.Background(), 99)
		assert.Equal(t, "indexer 99", name)
	})
}

func TestProwlarrPreferredIndexerIDs(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 10, nil)
		assert.Empty(t, c.PreferredIndexerIDs(CatMovie))
	})

	t.Run("found", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 10, map[string][]int{
			"videos": {5, 9, 12},
		})
		assert.Equal(t, []int{5, 9, 12}, c.PreferredIndexerIDs(CatMovie))
		assert.Equal(t, []int{5, 9, 12}, c.PreferredIndexerIDs(CatTV))
	})

	t.Run("category not mapped", func(t *testing.T) {
		c := NewProwlarrClient("http://example.com", "key", 10, map[string][]int{
			"videos": {5},
		})
		assert.Empty(t, c.PreferredIndexerIDs(CatMusic))
	})
}

func TestConvertToReleases(t *testing.T) {
	pr := []prowlarrRelease{
		{Title: "Movie.2025.1080p.WEB-DL.x264-GROUP", Seeders: 10, Size: 1000, IndexerID: 1, Indexer: "TL", Guid: "g1", DownloadURL: "http://dl", MagnetURL: "magnet:?", InfoHash: "abc", Protocol: "torrent"},
		{Title: "Movie.2025.1080p.WEB-DL.x265-GROUP", Seeders: 5, Size: 2000, IndexerID: 2, Indexer: "IPT", Guid: "g2", Protocol: "nzb"},
		{Title: "Movie.2025.1080p.WEB-DL.x265-GROUP2", Seeders: 20, Size: 3000, IndexerID: 2, Indexer: "IPT", Guid: "g3", Protocol: "torrent"},
	}

	result := convertToReleases(pr)
	assert.Len(t, result, 2)
	assert.Equal(t, "Movie.2025.1080p.WEB-DL.x264-GROUP", result[0].RawTitle)
	assert.Equal(t, 10, result[0].Seeders)
	assert.Equal(t, int64(1000), result[0].SizeBytes)
	assert.Equal(t, 1, result[0].IndexerID)
	assert.Equal(t, "TL", result[0].IndexerName)
	assert.Equal(t, "g1", result[0].Guid)
	assert.Equal(t, "http://dl", result[0].DownloadURL)
	assert.Equal(t, "magnet:?", result[0].MagnetURL)
	assert.Equal(t, "abc", result[0].InfoHash)
}

func TestCategoryKey(t *testing.T) {
	tests := []struct {
		cat  int
		want string
	}{
		{CatMovie, "videos"},
		{CatTV, "videos"},
		{CatAnime, "anime"},
		{CatMusic, "music"},
		{CatBook, "ebooks"},
		{CatBookMags, "ebooks"},
		{CatBookEbook, "ebooks"},
		{CatBookComic, "ebooks"},
		{CatAudioAudiobook, "audiobooks"},
		{9999, ""},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, categoryKey(tt.cat))
		})
	}
}
