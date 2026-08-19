package discover

import (
	"errors"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

var errBrowserDown = errors.New("browser down")

func TestBuildProvidersMarksBrowserScrapersFailedWhenBrowserDown(t *testing.T) {
	cfg := notificationTestConfig()
	r := &Runner{
		log:              zerolog.Nop(),
		cfg:              cfg,
		failedScrapers:   make(map[string]string),
		browserErr:       errBrowserDown,
		targetYear:       2026,
		targetWeek:       33,
		hasTargetWeek:    true,
		mediaTypeFilters: map[model.MediaType]bool{model.MediaTypeMovie: true},
	}
	// browserCtx is nil: browser-required scrapers are skipped and recorded.
	_, err := r.buildProviders(true, false, false, false, false)
	require.NoError(t, err)
	require.Contains(t, r.failedScrapers, "flixpatrol")
	require.Equal(t, "browser unavailable: browser down", r.failedScrapers["flixpatrol"])
}
