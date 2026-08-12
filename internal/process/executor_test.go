package process

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/library"
)

func TestComputeLibrarySkips(t *testing.T) {
	tests := []struct {
		name  string
		scope LibraryScope
		down  map[string]bool
		want  LibrarySkips
	}{
		{
			name:  "all services needed and healthy",
			scope: LibraryScope{Radarr: true, Sonarr: true, Lidarr: true, Book: true},
			want:  LibrarySkips{},
		},
		{
			name:  "book down skips only book",
			scope: LibraryScope{Radarr: true, Sonarr: true, Lidarr: true, Book: true},
			down:  map[string]bool{"book": true},
			want:  LibrarySkips{Book: true},
		},
		{
			name:  "multiple down services",
			scope: LibraryScope{Radarr: true, Sonarr: true, Lidarr: true, Book: true},
			down:  map[string]bool{"radarr": true, "lidarr": true},
			want:  LibrarySkips{Radarr: true, Lidarr: true},
		},
		{
			name:  "service not needed is skipped",
			scope: LibraryScope{Radarr: true},
			want:  LibrarySkips{Sonarr: true, Lidarr: true, Book: true},
		},
		{
			name:  "no scope skips everything",
			scope: LibraryScope{},
			want:  LibrarySkips{Radarr: true, Sonarr: true, Lidarr: true, Book: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeLibrarySkips(tt.scope, tt.down)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHealthCheckScoped(t *testing.T) {
	downSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer downSrv.Close()

	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer upSrv.Close()

	exec := &Executor{
		log:    zerolog.Nop(),
		radarr: library.NewRadarrClient(downSrv.URL, "key", 5),
		sonarr: library.NewSonarrClient(upSrv.URL, "key", 5),
		lidarr: library.NewLidarrClient(downSrv.URL, "key", 5),
	}

	// HealthCheck bounds each ping with its own short deadline, so a down
	// service is detected quickly regardless of the caller's context. One down
	// service is warned, and each HealthCheck call below checks a healthy
	// service before a down one.
	check := func(scope LibraryScope) HealthCheckResult {
		return exec.HealthCheck(context.Background(), false, false, scope)
	}

	t.Run("in-scope down service is warned, healthy one is not", func(t *testing.T) {
		// Radarr (healthy) is checked before Sonarr (down) in the fixed order.
		exec.radarr = library.NewRadarrClient(upSrv.URL, "key", 5)
		exec.sonarr = library.NewSonarrClient(downSrv.URL, "key", 5)
		res := check(LibraryScope{Radarr: true, Sonarr: true})

		require.Len(t, res.Warnings, 1)
		assert.Contains(t, res.Warnings[0], "Sonarr")
		assert.False(t, res.DownLibrary["radarr"])
		assert.True(t, res.DownLibrary["sonarr"])
	})

	t.Run("radarr down alone is warned", func(t *testing.T) {
		exec.radarr = library.NewRadarrClient(downSrv.URL, "key", 5)
		exec.sonarr = library.NewSonarrClient(upSrv.URL, "key", 5)
		res := check(LibraryScope{Radarr: true})

		require.Len(t, res.Warnings, 1)
		assert.Contains(t, res.Warnings[0], "Radarr")
		assert.True(t, res.DownLibrary["radarr"])
		assert.False(t, res.DownLibrary["sonarr"])
		assert.False(t, res.DownLibrary["lidarr"])
		assert.False(t, res.DownLibrary["book"])
	})

	t.Run("out-of-scope down services are not checked", func(t *testing.T) {
		// Radarr and Lidarr point at the down server but are out of scope.
		res := check(LibraryScope{Sonarr: true})

		require.Len(t, res.Warnings, 0)
		assert.False(t, res.DownLibrary["radarr"])
		assert.False(t, res.DownLibrary["lidarr"])
		assert.False(t, res.DownLibrary["book"])
	})

	t.Run("lidarr in scope and down is warned", func(t *testing.T) {
		res := check(LibraryScope{Lidarr: true})

		require.Len(t, res.Warnings, 1)
		assert.Contains(t, res.Warnings[0], "Lidarr")
		assert.True(t, res.DownLibrary["lidarr"])
	})
}

func TestHealthCheckFailsFastOnHungService(t *testing.T) {
	hangSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer hangSrv.Close()

	exec := &Executor{
		log:    zerolog.Nop(),
		sonarr: library.NewSonarrClient(hangSrv.URL, "key", 10),
	}

	start := time.Now()
	res := exec.HealthCheck(context.Background(), false, false, LibraryScope{Sonarr: true})
	elapsed := time.Since(start)

	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "Sonarr")
	assert.True(t, res.DownLibrary["sonarr"])
	assert.Less(t, elapsed, 10*time.Second, "down service must be detected quickly, took %v", elapsed)
}

func TestPreWarmSkipsDisabledServices(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("skipped service makes no requests", func(t *testing.T) {
		exec := &Executor{
			log:    zerolog.Nop(),
			radarr: library.NewRadarrClient(srv.URL, "key", 5),
		}
		exec.SetLibrarySkips(LibrarySkips{Radarr: true})

		exec.PreWarmRadarr(ctx)
		assert.Equal(t, int32(0), atomic.LoadInt32(&requests))
	})

	t.Run("enabled service still pre-warms", func(t *testing.T) {
		d, err := db.Open(t.TempDir() + "/wmdl-test.db")
		require.NoError(t, err)
		t.Cleanup(func() { d.Close() })
		require.NoError(t, d.Migrate(ctx))

		exec := &Executor{
			log:    zerolog.Nop(),
			db:     d,
			radarr: library.NewRadarrClient(srv.URL, "key", 5),
		}

		exec.PreWarmRadarr(ctx)
		assert.Equal(t, int32(1), atomic.LoadInt32(&requests))
	})
}
