package process

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

func TestAddReleaseToClient(t *testing.T) {
	ctx := context.Background()
	log := zerolog.Nop()

	newSrv := func(fn http.HandlerFunc) (*httptest.Server, *search.ProwlarrClient) {
		srv := httptest.NewServer(fn)
		c := search.NewProwlarrClient(srv.URL, "key", 10, nil)
		return srv, c
	}

	t.Run("download URL pushes torrent bytes", func(t *testing.T) {
		srv, prowl := newSrv(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Write([]byte("d8:announce"))
		})
		defer srv.Close()

		dl := &download.MockClient{}
		dl.On("AddTorrentData", ctx, "Album-FLAC.torrent", []byte("d8:announce"), mock.Anything).Return("hash1", nil)

		rel := quality.ParsedRelease{
			RawTitle:    "Album-FLAC",
			DownloadURL: srv.URL + "/42/download?link=abc",
			MagnetURL:   "magnet:?xt=urn:btih:def",
			IndexerID:   42,
		}
		tid, err := addReleaseToClient(ctx, log, dl, prowl, rel, "Albums")
		require.NoError(t, err)
		assert.Equal(t, "hash1", tid)
		dl.AssertExpectations(t)
	})

	t.Run("fetch failure falls back to magnet", func(t *testing.T) {
		srv, prowl := newSrv(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		dl := &download.MockClient{}
		dl.On("AddMagnet", ctx, "magnet:?xt=urn:btih:def", mock.Anything).Return("hash2", nil)

		rel := quality.ParsedRelease{
			RawTitle:    "Album",
			DownloadURL: srv.URL + "/42/download",
			MagnetURL:   "magnet:?xt=urn:btih:def",
		}
		tid, err := addReleaseToClient(ctx, log, dl, prowl, rel, "Albums")
		require.NoError(t, err)
		assert.Equal(t, "hash2", tid)
		dl.AssertExpectations(t)
	})

	t.Run("fetch failure without magnet returns error", func(t *testing.T) {
		srv, prowl := newSrv(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer srv.Close()

		dl := &download.MockClient{}
		rel := quality.ParsedRelease{RawTitle: "Album", DownloadURL: srv.URL + "/42/download"}
		_, err := addReleaseToClient(ctx, log, dl, prowl, rel, "Albums")
		assert.Error(t, err)
	})

	t.Run("magnet only uses AddMagnet", func(t *testing.T) {
		prowl := search.NewProwlarrClient("http://127.0.0.1:1", "key", 10, nil)
		dl := &download.MockClient{}
		dl.On("AddMagnet", ctx, "magnet:?xt=urn:btih:def", mock.Anything).Return("hash3", nil)

		rel := quality.ParsedRelease{RawTitle: "Album", MagnetURL: "magnet:?xt=urn:btih:def"}
		tid, err := addReleaseToClient(ctx, log, dl, prowl, rel, "Music")
		require.NoError(t, err)
		assert.Equal(t, "hash3", tid)
		dl.AssertExpectations(t)
	})

	t.Run("no URL or magnet returns error", func(t *testing.T) {
		prowl := search.NewProwlarrClient("http://127.0.0.1:1", "key", 10, nil)
		dl := &download.MockClient{}
		rel := quality.ParsedRelease{RawTitle: "Album"}
		_, err := addReleaseToClient(ctx, log, dl, prowl, rel, "Albums")
		assert.Error(t, err)
	})

	t.Run("nil download client returns error", func(t *testing.T) {
		prowl := search.NewProwlarrClient("http://127.0.0.1:1", "key", 10, nil)
		rel := quality.ParsedRelease{RawTitle: "Album", MagnetURL: "magnet:?xt=urn:btih:def"}
		_, err := addReleaseToClient(ctx, log, nil, prowl, rel, "Albums")
		assert.Error(t, err)
	})
}

func TestMusicQuality(t *testing.T) {
	assert.Equal(t, "FLAC", musicQuality(quality.ParsedRelease{Codec: "FLAC"}))
	assert.Equal(t, "1080p", musicQuality(quality.ParsedRelease{Resolution: 1080}))
	assert.Equal(t, "", musicQuality(quality.ParsedRelease{}))
}
