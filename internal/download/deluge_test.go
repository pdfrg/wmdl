package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelugeLogin(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req delugeRequest
			json.NewDecoder(r.Body).Decode(&req)
			assert.Equal(t, "auth.login", req.Method)
			assert.Equal(t, "pass", req.Params[0])

			w.Write([]byte(`{"result": true, "error": null, "id": 1}`))
		}))
		defer srv.Close()

		c := NewDelugeClient(srv.URL, "pass")
		assert.NoError(t, c.login(context.Background()))
	})

	t.Run("wrong password", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"result": false, "error": null, "id": 1}`))
		}))
		defer srv.Close()

		c := NewDelugeClient(srv.URL, "wrong")
		assert.ErrorContains(t, c.login(context.Background()), "login failed")
	})

	t.Run("error response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"result": null, "error": {"message": "auth error", "code": 1}, "id": 1}`))
		}))
		defer srv.Close()

		c := NewDelugeClient(srv.URL, "pass")
		assert.ErrorContains(t, c.login(context.Background()), "auth error")
	})
}

func TestDelugeAddTorrent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req delugeRequest
			json.NewDecoder(r.Body).Decode(&req)

			if callCount == 0 {
				assert.Equal(t, "auth.login", req.Method)
				w.Write([]byte(`{"result": true, "error": null, "id": 1}`))
			} else {
				assert.Equal(t, "core.add_torrent_url", req.Method)
				assert.Equal(t, "http://example.com/t.torrent", req.Params[0])
				opts := req.Params[1].(map[string]interface{})
				assert.Equal(t, "/downloads", opts["download_location"])
				w.Write([]byte(`{"result": "torrent_hash_abc", "error": null, "id": 2}`))
			}
			callCount++
		}))
		defer srv.Close()

		c := NewDelugeClient(srv.URL, "pass")
		id, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent", WithSavePath("/downloads"))
		require.NoError(t, err)
		assert.Equal(t, "torrent_hash_abc", id)
	})

	t.Run("failed add", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"result": null, "error": {"message": "add failed", "code": 2}, "id": 1}`))
		}))
		defer srv.Close()

		c := NewDelugeClient(srv.URL, "pass")
		_, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		assert.ErrorContains(t, err, "add failed")
	})
}

func TestDelugeAddMagnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req delugeRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "auth.login" {
			w.Write([]byte(`{"result": true, "error": null, "id": 1}`))
			return
		}
		assert.Equal(t, "core.add_torrent_url", req.Method)
		assert.Equal(t, "magnet:?xt=urn:btih:abc", req.Params[0])
		w.Write([]byte(`{"result": "magnet_hash", "error": null, "id": 2}`))
	}))
	defer srv.Close()

	c := NewDelugeClient(srv.URL, "pass")
	id, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
	require.NoError(t, err)
	assert.Equal(t, "magnet_hash", id)
}

func TestDelugePing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result": true, "error": null, "id": 1}`))
	}))
	defer srv.Close()

	c := NewDelugeClient(srv.URL, "pass")
	assert.NoError(t, c.Ping(context.Background()))
}
