package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// launchTestBrowser starts a throwaway headless Chromium for tests that need
// real CDP input dispatching. Skips when no browser binary is available so
// offline environments still pass `go test ./...`.
func launchTestBrowser(t *testing.T) context.Context {
	t.Helper()

	var execPath string
	for _, b := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome"} {
		if p, err := exec.LookPath(b); err == nil {
			execPath = p
			break
		}
	}
	if execPath == "" {
		t.Skip("no chromium binary in PATH; skipping browser-backed test")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.Headless,
		chromedp.Flag("disable-gpu", true),
		// CI runners (and most containers) run as root with unprivileged
		// user namespaces disabled, so Chromium's sandbox is unusable and the
		// browser refuses to start. These flags are the standard headless
		// Chrome workaround for CI/containers and are fine for a throwaway
		// test browser. Offline dev machines without a browser still skip via
		// the LookPath check above.
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	dataDir, err := os.MkdirTemp("", "wmdl-chromium-profile")
	require.NoError(t, err)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ct, ctCancel := chromedp.NewContext(allocCtx)
	t.Cleanup(func() {
		ctCancel()
		allocCancel()
		// Only remove the profile once the browser has been killed, otherwise
		// Chromium races the cleanup by flushing files during RemoveAll.
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(dataDir)
	})
	return ct
}

const fakeChallengeHTML = `<!DOCTYPE html>
<html><head><title>Just a moment...</title></head>
<body>
<div id="cf-chl-widget-0" style="position:absolute;left:100px;top:50px;width:300px;height:65px;background:#ddd;">
  <input type="checkbox" style="margin:24px 0 0 20px;">
</div>
<script>
document.getElementById('cf-chl-widget-0').addEventListener('mousedown', function () {
  setTimeout(function () { document.title = 'New Releases | FlixPatrol'; }, 50);
});
</script>
</body></html>`

func TestWaitForRealPageTurnstileAutoClick(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(fakeChallengeHTML))
	}))
	defer srv.Close()

	ct := launchTestBrowser(t)

	if err := chromedp.Run(ct,
		chromedp.Navigate(srv.URL),
		chromedp.WaitReady("body"),
	); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	start := time.Now()
	if err := waitForRealPage(ct, 30*time.Second); err != nil {
		t.Fatalf("waitForRealPage did not clear fake challenge: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Second {
		t.Errorf("waitForRealPage took %v; click likely never dispatched", elapsed)
	}

	var title string
	if err := chromedp.Run(ct, chromedp.Title(&title)); err != nil {
		t.Fatalf("final title: %v", err)
	}
	if title != "New Releases | FlixPatrol" {
		t.Errorf("title = %q, want cleared challenge title", title)
	}
}

func TestWaitForRealPageNoChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Fine Page</title></head><body>ok</body></html>`))
	}))
	defer srv.Close()

	ct := launchTestBrowser(t)

	if err := chromedp.Run(ct,
		chromedp.Navigate(srv.URL),
		chromedp.WaitReady("body"),
	); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	if err := waitForRealPage(ct, 15*time.Second); err != nil {
		t.Fatalf("waitForRealPage on clean page: %v", err)
	}
}

func TestTryClickTurnstileAbsentWidget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>No Widget</title></head><body>plain</body></html>`))
	}))
	defer srv.Close()

	ct := launchTestBrowser(t)

	if err := chromedp.Run(ct,
		chromedp.Navigate(srv.URL),
		chromedp.WaitReady("body"),
	); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	clicked, err := tryClickTurnstile(ct, nil, nil)
	if err != nil {
		t.Fatalf("tryClickTurnstile error: %v", err)
	}
	if clicked {
		t.Error("tryClickTurnstile reported a click with no widget present")
	}
}

func TestIsChallengeTitle(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Just a moment...", true},
		{"Just a moment...", true},
		{"Just a moment - please wait", true},
		{"503 Service Unavailable", true},
		{"Error | Site", true},
		{"FlixPatrol - Streaming Calendar", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isChallengeTitle(tc.title); got != tc.want {
			t.Errorf("isChallengeTitle(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}
