//go:build integration

package discover

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/browser"
)

// TestFlixPatrolLiveTurnstile scrapes flixpatrol.com through a real browser,
// exercising the automatic Turnstile checkbox click when Cloudflare challenges
// the session. Uses its own debug port so it never disturbs a live wmdl run.
func TestFlixPatrolLiveTurnstile(t *testing.T) {
	binary := os.Getenv("WMDL_BROWSER_BINARY")
	if binary == "" {
		binary = "chromium"
	}

	const testPort = 9223
	kill, err := browser.EnsureRunning(binary, testPort, "wmd-review", false)
	require.NoError(t, err)
	defer func() { _ = kill() }()

	debugURL := fmt.Sprintf("http://127.0.0.1:%d", testPort)
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), debugURL)
	defer allocCancel()

	p := NewFlixPatrolProvider(debugURL, allocCtx)
	y, w := time.Now().ISOWeek()
	p.SetWeekRange(y, w)

	items, err := p.Scrape()
	require.NoError(t, err, "scrape should clear any Turnstile challenge automatically")
	t.Logf("flixpatrol live: %d items in target range", len(items))

	for _, item := range items {
		assert.NotEmpty(t, item.Title)
		assert.Equal(t, "flixpatrol", item.Source)
	}
}
