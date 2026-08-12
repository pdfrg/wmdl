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

func TestLidarrNewClient(t *testing.T) {
	c := NewLidarrClient("http://example.com", "key", 0)
	assert.Equal(t, "http://example.com", c.baseURL)
	assert.Equal(t, "key", c.apiKey)
}

func TestLidarrArtistMusicBrainzID(t *testing.T) {
	t.Run("uses foreignArtistId when mbId absent", func(t *testing.T) {
		a := LidarrArtist{ForeignArtistID: "09885b8e-f235-4b80-a02a-055539493173"}
		assert.Equal(t, "09885b8e-f235-4b80-a02a-055539493173", a.MusicBrainzID())
	})
	t.Run("prefers mbId when present", func(t *testing.T) {
		a := LidarrArtist{MBID: "mbid-a", ForeignArtistID: "mbid-b"}
		assert.Equal(t, "mbid-a", a.MusicBrainzID())
	})
}

func TestLidarrGetArtistMatchesByForeignArtistID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"id":1,"foreignArtistId":"a2f69394-0d29-4f8d-b981-61e0789b1a3d","artistName":"A"},
			{"id":2,"foreignArtistId":"09885b8e-f235-4b80-a02a-055539493173","artistName":"The All-American Rejects"}
		]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	got, err := c.GetArtist(context.Background(), "09885b8e-f235-4b80-a02a-055539493173")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "The All-American Rejects", got.ArtistName)
}

func TestLidarrPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.Ping(context.Background()))
}

func TestLidarrGetQualityProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"Any"},{"id":2,"name":"Lossless"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	profiles, err := c.GetQualityProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.Equal(t, "Any", profiles[0].Name)

	// Second call uses cache
	profiles, err = c.GetQualityProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
}

func TestLidarrGetMetadataProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"Standard"},{"id":2,"name":"Detailed"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	profiles, err := c.GetMetadataProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.Equal(t, "Standard", profiles[0].Name)
}

func TestLidarrGetRootFolders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"path":"/music"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	folders, err := c.GetRootFolders(context.Background())
	require.NoError(t, err)
	require.Len(t, folders, 1)
	assert.Equal(t, "/music", folders[0].Path)
}

func TestLidarrLookupArtist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":7,"foreignArtistId":"b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d","artistName":"Radiohead","monitored":false,"monitorNewAlbums":false,"qualityProfileId":1,"metadataProfileId":1,"rootFolderPath":"/music"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	artist, err := c.LookupArtist(context.Background(), "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d")
	require.NoError(t, err)
	require.NotNil(t, artist)
	assert.Equal(t, "Radiohead", artist.ArtistName)
}

func TestLidarrLookupArtistNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	artist, err := c.LookupArtist(context.Background(), "nonexistent-mbid")
	require.NoError(t, err)
	assert.Nil(t, artist)
}

func TestLidarrGetArtist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"foreignArtistId":"a","artistName":"Radiohead","mbId":"b10bbbfc"},
			{"foreignArtistId":"b","artistName":"Nirvana","mbId":"other-mbid"}
		]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	found, err := c.GetArtist(context.Background(), "b10bbbfc")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "Radiohead", found.ArtistName)

	notFound, err := c.GetArtist(context.Background(), "no-such-mbid")
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestLidarrAddArtist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		var req LidarrArtist
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d", req.ForeignArtistID)
		assert.Equal(t, "Radiohead", req.ArtistName)
		assert.True(t, req.Monitored)
		assert.True(t, req.MonitorNewAlbums)
		assert.True(t, req.AddOptions.SearchForNewAlbum)

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":7,"foreignArtistId":"b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d","artistName":"Radiohead"}`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	artist, err := c.AddArtist(context.Background(), "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d", "Radiohead", AddArtistOptions{
		Monitored:         true,
		MonitorNewAlbums:  true,
		QualityProfileID:  1,
		MetadataProfileID: 1,
		RootFolderPath:    "/music",
		SearchNow:         true,
	})
	require.NoError(t, err)
	require.NotNil(t, artist)
	assert.Equal(t, "Radiohead", artist.ArtistName)
}

func TestLidarrLookupAlbum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"foreignAlbumId":"some-mbid","title":"OK Computer","artistId":7}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	album, err := c.LookupAlbum(context.Background(), "some-mbid")
	require.NoError(t, err)
	require.NotNil(t, album)
	assert.Equal(t, "OK Computer", album.Title)
}

func TestLidarrAddAlbum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		var req LidarrAlbum
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "album-mbid", req.ForeignAlbumID)
		assert.Equal(t, 7, req.ArtistID)
		assert.Equal(t, "OK Computer", req.Title)
		assert.True(t, req.AddOptions.SearchForNewAlbum)

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"foreignAlbumId":"album-mbid","title":"OK Computer","artistId":7,"id":42}`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	album, err := c.AddAlbum(context.Background(), "album-mbid", 7, "OK Computer", AddAlbumOptions{
		Monitored: true,
		SearchNow: true,
	})
	require.NoError(t, err)
	require.NotNil(t, album)
	assert.Equal(t, 42, album.ID)
}

func TestLidarrTriggerAlbumSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cmd lidarrCommand
		json.NewDecoder(r.Body).Decode(&cmd)
		assert.Equal(t, "AlbumSearch", cmd.Name)
		assert.Equal(t, []int{42, 43}, cmd.AlbumIDs)

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	assert.NoError(t, c.TriggerAlbumSearch(context.Background(), []int{42, 43}))
}

func TestLidarrResolveQualityProfileID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"Any"},{"id":2,"name":"Lossless"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)

	t.Run("empty returns 1", func(t *testing.T) {
		id, err := c.ResolveQualityProfileID(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, 1, id)
	})

	t.Run("found by name", func(t *testing.T) {
		id, err := c.ResolveQualityProfileID(context.Background(), "Lossless")
		require.NoError(t, err)
		assert.Equal(t, 2, id)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := c.ResolveQualityProfileID(context.Background(), "Nonexistent")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestLidarrResolveMetadataProfileID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1,"name":"Standard"},{"id":2,"name":"Detailed"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)

	t.Run("empty returns 1", func(t *testing.T) {
		id, err := c.ResolveMetadataProfileID(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, 1, id)
	})

	t.Run("found by name", func(t *testing.T) {
		id, err := c.ResolveMetadataProfileID(context.Background(), "Detailed")
		require.NoError(t, err)
		assert.Equal(t, 2, id)
	})
}

func TestLidarrGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	err := c.get(context.Background(), "/api/v1/health", nil)
	assert.ErrorContains(t, err, "401")
}

func TestLidarrPostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	err := c.post(context.Background(), "/api/v1/artist", []byte(`{}`), nil)
	assert.ErrorContains(t, err, "400")
}

func TestLidarrSetAllArtists(t *testing.T) {
	c := NewLidarrClient("http://example.com", "key", 10)
	c.SetAllArtists([]LidarrArtist{{ArtistName: "Test Artist"}})
	assert.Len(t, c.artistCache, 1)
}

func TestLidarrSetAllAlbums(t *testing.T) {
	c := NewLidarrClient("http://example.com", "key", 10)
	c.SetAllAlbums([]LidarrAlbum{{Title: "Test Album"}})
	assert.Len(t, c.albumCache, 1)
}

func TestLidarrGetAllArtistsCacheHit(t *testing.T) {
	c := NewLidarrClient("http://example.com", "key", 10)
	c.artistCache = []LidarrArtist{{ArtistName: "Cached Artist"}}

	artists, err := c.GetAllArtists(context.Background())
	require.NoError(t, err)
	require.Len(t, artists, 1)
	assert.Equal(t, "Cached Artist", artists[0].ArtistName)
}

func TestLidarrGetAllAlbumsCacheHit(t *testing.T) {
	c := NewLidarrClient("http://example.com", "key", 10)
	c.albumCache = []LidarrAlbum{{Title: "Cached Album"}}

	albums, err := c.GetAllAlbums(context.Background())
	require.NoError(t, err)
	require.Len(t, albums, 1)
	assert.Equal(t, "Cached Album", albums[0].Title)
}

func TestSanitizeDirName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Normal Band", "Normal Band"},
		{"AC/DC", "AC_DC"},
		{"Rock: The Album", "Rock_ The Album"},
		{"A<B>C", "A_B_C"},
		{`"Quoted"`, "_Quoted_"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeDirName(tt.input))
		})
	}
}

func TestLidarrGetQualityProfilesCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`[{"id":1,"name":"Any"}]`))
	}))
	defer srv.Close()

	c := NewLidarrClient(srv.URL, "key", 10)
	c.GetQualityProfiles(context.Background())
	c.GetQualityProfiles(context.Background())
	assert.Equal(t, 1, calls, "second call should use cache")
}
