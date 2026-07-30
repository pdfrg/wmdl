package discover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func retry(n int, delay time.Duration, fn func() error) error {
	var lastErr error
	for i := 0; i < n; i++ {
		if err := fn(); err != nil {
			lastErr = err
			time.Sleep(delay)
			continue
		}
		return nil
	}
	return lastErr
}

func waitForRealPage(ct context.Context, timeout time.Duration) error {
	waitCtx, waitCancel := context.WithTimeout(ct, timeout)
	defer waitCancel()

	var title string
	// ~60 attempts with 2s sleep between each
	attempts := int(timeout / (2 * time.Second))
	for i := 0; i < attempts; i++ {
		if err := retry(5, 500*time.Millisecond, func() error {
			return chromedp.Run(waitCtx, chromedp.Title(&title))
		}); err != nil {
			return err
		}

		if strings.Contains(title, "503") || strings.Contains(title, "Error") {
			var html string
			if err := retry(3, 500*time.Millisecond, func() error {
				return chromedp.Run(waitCtx, chromedp.OuterHTML("html", &html))
			}); err == nil {
				if strings.Contains(html, "503 Backend fetch failed") {
					return fmt.Errorf("backend fetch failed (503)")
				}
			}
		}

		if !strings.Contains(title, "Just a moment") &&
			!strings.Contains(title, "503") &&
			!strings.Contains(title, "Error") &&
			title != "" {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for real page, last title: %q: %w", title, context.DeadlineExceeded)
}
