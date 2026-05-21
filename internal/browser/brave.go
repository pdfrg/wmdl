package browser

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"
)

func EnsureRunning(debugPort int, profile string) (func() error, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", debugPort)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil, nil
	}

	userDataDir := filepath.Join(os.ExpandEnv("$HOME/.local/share/wmd/brave"), profile)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return nil, fmt.Errorf("creating brave data dir: %w", err)
	}

	cmd := exec.Command("brave",
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--new-window",
		"about:blank",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launching brave: %w", err)
	}

	// Wait for it to start listening
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return cmd.Process.Kill, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	cmd.Process.Kill()
	return nil, fmt.Errorf("brave started but not listening on %s within 15s", addr)
}

func ListTabs(ctx context.Context, debugURL string) ([]Tab, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, debugURL)
	defer allocCancel()

	// Need at least a short-lived context to trigger the target listing
	ct, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Give the allocator time to discover targets
	time.Sleep(500 * time.Millisecond)

	targets, err := chromedp.Targets(ct)
	if err != nil {
		return nil, fmt.Errorf("listing targets: %w", err)
	}

	var tabs []Tab
	for _, t := range targets {
		// Only page tabs, not DevTools, extensions, etc.
		if !isPageTarget(t.Type) {
			continue
		}
		url := t.URL
		if url == "about:blank" || url == "chrome://newtab/" {
			continue
		}
		tabs = append(tabs, Tab{
			Title: t.Title,
			URL:   url,
		})
	}

	return tabs, nil
}

func isPageTarget(targetType string) bool {
	return targetType == "page"
}

type Tab struct {
	Title string
	URL   string
}
