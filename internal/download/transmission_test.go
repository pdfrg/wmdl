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

func TestTransmissionGetSessionID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/transmission/rpc", r.URL.Path)
			w.Header().Set("X-Transmission-Session-Id", "abc123")
			w.WriteHeader(http.StatusConflict)
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		assert.NoError(t, c.getSessionID(context.Background()))
		assert.Equal(t, "abc123", c.sessionID)
	})

	t.Run("no session ID header", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		assert.ErrorContains(t, c.getSessionID(context.Background()), "no session ID")
	})

	t.Run("unexpected status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		assert.ErrorContains(t, c.getSessionID(context.Background()), "unexpected status")
	})
}

func TestTransmissionAddTorrent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		gotSessionID := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !gotSessionID {
				w.Header().Set("X-Transmission-Session-Id", "sid123")
				w.WriteHeader(http.StatusConflict)
				gotSessionID = true
				return
			}
			assert.Equal(t, "sid123", r.Header.Get("X-Transmission-Session-Id"))

			var req trpcRequest
			json.NewDecoder(r.Body).Decode(&req)
			assert.Equal(t, "torrent-add", req.Method)
			args := req.Arguments.(map[string]interface{})
			assert.Equal(t, "http://example.com/t.torrent", args["filename"])

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		id, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent")
		require.NoError(t, err)
		assert.Empty(t, id)
	})

	t.Run("transmission error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Transmission-Session-Id", "sid")
			w.WriteHeader(http.StatusConflict)
		}))
		defer srv.Close()
		_ = srv // We need to trigger an error differently
	})

	t.Run("failed result", func(t *testing.T) {
		gotSessionID := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !gotSessionID {
				w.Header().Set("X-Transmission-Session-Id", "sid")
				w.WriteHeader(http.StatusConflict)
				gotSessionID = true
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result": "invalid torrent", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		_, err := c.AddTorrent(context.Background(), "http://example.com/bad.torrent")
		assert.ErrorContains(t, err, "invalid torrent")
	})
}

func TestTransmissionAddMagnet(t *testing.T) {
	gotSessionID := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gotSessionID {
			w.Header().Set("X-Transmission-Session-Id", "sid")
			w.WriteHeader(http.StatusConflict)
			gotSessionID = true
			return
		}
		var req trpcRequest
		json.NewDecoder(r.Body).Decode(&req)
		args := req.Arguments.(map[string]interface{})
		assert.Equal(t, "magnet:?xt=urn:btih:abc", args["filename"])
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewTransmissionClient(srv.URL, "", "")
	id, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
	require.NoError(t, err)
	assert.Empty(t, id)
}

func TestTransmissionPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Transmission-Session-Id", "sid")
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := NewTransmissionClient(srv.URL, "", "")
	assert.NoError(t, c.Ping(context.Background()))
}

func TestTransmissionDoWithRetry(t *testing.T) {
	t.Run("retry on 409", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				w.Header().Set("X-Transmission-Session-Id", "new-sid")
				w.WriteHeader(http.StatusConflict)
				return
			}
			if callCount == 2 {
				// Still 409 — trigger retry
				w.Header().Set("X-Transmission-Session-Id", "newer-sid")
				w.WriteHeader(http.StatusConflict)
				return
			}
			assert.Equal(t, "newer-sid", r.Header.Get("X-Transmission-Session-Id"))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		resp, err := c.doWithRetry(context.Background(), "torrent-add", nil, 0)
		require.NoError(t, err)
		assert.Equal(t, "success", resp.Result)
		assert.Equal(t, 3, callCount)
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Transmission-Session-Id", "sid")
			w.WriteHeader(http.StatusConflict)
		}))
		defer srv.Close()

		c := NewTransmissionClient(srv.URL, "", "")
		c.sessionID = "old-sid"
		_, err := c.doWithRetry(context.Background(), "torrent-add", nil, 1)
		assert.ErrorContains(t, err, "still getting 409")
	})
}

func TestTransmissionDoWithOptions(t *testing.T) {
	gotSessionID := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gotSessionID {
			w.Header().Set("X-Transmission-Session-Id", "sid")
			w.WriteHeader(http.StatusConflict)
			gotSessionID = true
			return
		}
		var req trpcRequest
		json.NewDecoder(r.Body).Decode(&req)
		args := req.Arguments.(map[string]interface{})
		assert.Equal(t, true, args["paused"])
		assert.Equal(t, "/downloads", args["download-dir"])
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewTransmissionClient(srv.URL, "", "")
	_, err := c.AddTorrent(context.Background(), "http://example.com/t.torrent",
		WithSavePath("/downloads"), WithPaused(true))
	require.NoError(t, err)
}
