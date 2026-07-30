package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQbittorrentLogin(t *testing.T) {
	t.Run("success with OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v2/auth/login", r.URL.Path)
			assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK."))
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		assert.NoError(t, c.login(context.Background()))
		assert.True(t, c.loggedIn)
		assert.Len(t, c.cookies, 0)
	})

	t.Run("success with 204", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		assert.NoError(t, c.login(context.Background()))
	})

	t.Run("invalid credentials", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Fails."))
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "wrong")
		assert.ErrorContains(t, c.login(context.Background()), "invalid credentials")
	})

	t.Run("non-OK status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		assert.ErrorContains(t, c.login(context.Background()), "403")
	})

	t.Run("already logged in", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("should not call login again")
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		c.loggedIn = true
		c.cookies = []*http.Cookie{{Name: "SID", Value: "abc"}}
		assert.NoError(t, c.login(context.Background()))
	})
}

func TestQbittorrentAddTorrent(t *testing.T) {
	t.Run("success with JSON response (qbit >=5.2)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				w.WriteHeader(http.StatusNoContent)
				w.Header().Set("Set-Cookie", "SID=abc123")
				return
			}
			assert.Equal(t, "/api/v2/torrents/add", r.URL.Path)
			assert.Equal(t, "http://example.com/torrent.torrent", r.PostFormValue("urls"))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"added_torrent_ids":["torrent1"],"failure_count":0,"pending_count":0,"success_count":1}`))
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		id, err := c.AddTorrent(context.Background(), "http://example.com/torrent.torrent")
		require.NoError(t, err)
		assert.Equal(t, "torrent1", id)
	})

	t.Run("success with old qbittorrent 'Ok.'", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		id, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		require.NoError(t, err)
		assert.Empty(t, id)
	})

	t.Run("re-login on 403", func(t *testing.T) {
		loginCalls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				loginCalls++
				w.WriteHeader(http.StatusNoContent)
				w.Header().Set("Set-Cookie", "SID=abc")
				return
			}
			if loginCalls == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		_, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		require.NoError(t, err)
		assert.Equal(t, 2, loginCalls)
	})

	t.Run("conflict response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusConflict)
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		_, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		assert.ErrorContains(t, err, "duplicate")
	})

	t.Run("accepted response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		defer srv.Close()

		c := NewQbittorrentClient(srv.URL, "user", "pass")
		id, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		require.NoError(t, err)
		assert.Empty(t, id)
	})
}

func TestQbittorrentAddMagnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		assert.Equal(t, "magnet:?xt=urn:btih:abc", r.PostFormValue("urls"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"added_torrent_ids":["magnet1"]}`))
	}))
	defer srv.Close()

	c := NewQbittorrentClient(srv.URL, "user", "pass")
	id, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
	require.NoError(t, err)
	assert.Equal(t, "magnet1", id)
}

func TestQbittorrentAddWithOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		assert.Equal(t, "/downloads", r.PostFormValue("savepath"))
		assert.Equal(t, "movies", r.PostFormValue("category"))
		assert.Equal(t, "true", r.PostFormValue("paused"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"added_torrent_ids":["t1"]}`))
	}))
	defer srv.Close()

	c := NewQbittorrentClient(srv.URL, "user", "pass")
	id, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent",
		WithSavePath("/downloads"),
		WithCategory("movies"),
		WithPaused(true),
	)
	require.NoError(t, err)
	assert.Equal(t, "t1", id)
}

func TestQbittorrentPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/auth/login", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewQbittorrentClient(srv.URL, "user", "pass")
	assert.NoError(t, c.Ping(context.Background()))
}

func TestParseAddResponse(t *testing.T) {
	t.Run("new JSON format", func(t *testing.T) {
		id := parseAddResponse([]byte(`{"added_torrent_ids":["hash1"],"failure_count":0,"pending_count":0,"success_count":1}`))
		assert.Equal(t, "hash1", id)
	})

	t.Run("multiple torrents", func(t *testing.T) {
		id := parseAddResponse([]byte(`{"added_torrent_ids":["hash1","hash2"],"failure_count":0,"pending_count":0,"success_count":2}`))
		assert.Equal(t, "hash1", id)
	})

	t.Run("empty list", func(t *testing.T) {
		id := parseAddResponse([]byte(`{"added_torrent_ids":[],"failure_count":0,"pending_count":0,"success_count":0}`))
		assert.Empty(t, id)
	})

	t.Run("old plain text", func(t *testing.T) {
		id := parseAddResponse([]byte("Ok."))
		assert.Empty(t, id)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		id := parseAddResponse([]byte("not json"))
		assert.Empty(t, id)
	})
}
