package discover

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"
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

// maxTurnstileClicks bounds how many times waitForRealPage will attempt to
// click a Cloudflare Turnstile checkbox before giving up and letting the
// timeout fire.
const maxTurnstileClicks = 15

// turnstileBoxJS returns the viewport rect of the first visible Cloudflare
// Turnstile widget container, or null. The interactive checkbox sits near the
// left edge, vertically centered, so the caller can aim synthetic mouse events
// at it from the top-level document (the challenge iframe itself is
// cross-origin and cannot be reached via DOM).
const turnstileBoxJS = `(() => {
	const sels = [
		'iframe[src*="challenges.cloudflare.com"]',
		'div.cf-turnstile',
		'[id^="cf-chl-widget"]',
	];
	for (const s of sels) {
		for (const el of document.querySelectorAll(s)) {
			const r = el.getBoundingClientRect();
			if (r.width > 0 && r.height > 0) {
				return {x: r.x, y: r.y, w: r.width, h: r.height};
			}
		}
	}
	return null;
})()`

type turnstileBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func isChallengeTitle(title string) bool {
	return strings.Contains(title, "Just a moment") ||
		strings.Contains(title, "503") ||
		strings.Contains(title, "Error")
}

// tryClickTurnstile attempts to dismiss a Cloudflare Turnstile challenge.
// Two strategies are tried:
//
//  1. An inline widget in the top-level document (older Cloudflare variants),
//     located via DOM and clicked at its checkbox position.
//  2. The current OOPIF variant, where the checkbox lives inside a separate
//     cross-origin `iframe` CDP target (`challenges.cloudflare.com`). The
//     widget's DOM is not reachable from the page, so we attach to the iframe
//     target and click at the standard 300x65 widget checkbox position.
//
// If oopifCtx/oopifCancel are non-nil, the OOPIF session is created once and
// reused across calls (the caller retries until the page clears); reusing the
// session avoids re-attaching a CDP session to the iframe on every poll.
// Returns true if a click was dispatched.
func tryClickTurnstile(ct context.Context, oopifCtx *context.Context, oopifCancel *context.CancelFunc) (bool, error) {
	if clicked, err := tryClickInlineWidget(ct); err != nil || clicked {
		return clicked, err
	}
	return tryClickOOPIFCheckbox(ct, oopifCtx, oopifCancel)
}

// tryClickInlineWidget looks for a visible Turnstile widget in the top-level
// document and clicks its checkbox (left edge, vertically centered).
func tryClickInlineWidget(ct context.Context) (bool, error) {
	var box *turnstileBox
	if err := chromedp.Run(ct, chromedp.EvaluateAsDevTools(turnstileBoxJS, &box)); err != nil {
		return false, err
	}
	if box == nil || box.W <= 0 || box.H <= 0 {
		return false, nil
	}

	clickX := box.X + math.Min(30, box.W*0.25)
	clickY := box.Y + box.H/2
	err := chromedp.Run(ct,
		chromedp.MouseEvent(input.MouseMoved, clickX, clickY),
		chromedp.MouseClickXY(clickX, clickY),
	)
	if err != nil {
		return true, fmt.Errorf("clicking turnstile checkbox at (%.0f, %.0f): %w", clickX, clickY, err)
	}
	log.Info().Msgf("turnstile: clicked inline challenge checkbox at (%.0f, %.0f)", clickX, clickY)
	return true, nil
}

// turnstileCheckboxPoints are candidate checkbox centers within the standard
// 300x65 Turnstile widget, in widget-local (iframe-relative) pixels.
var turnstileCheckboxPoints = [][2]float64{{21, 33}, {30, 32}}

// tryClickOOPIFCheckbox finds the Cloudflare challenge iframe target (an
// out-of-process iframe carrying the interactive checkbox) and clicks it.
func tryClickOOPIFCheckbox(ct context.Context, oopifCtx *context.Context, oopifCancel *context.CancelFunc) (bool, error) {
	var iframeID target.ID
	err := chromedp.Run(ct, chromedp.ActionFunc(func(ctx context.Context) error {
		infos, err := target.GetTargets().Do(ctx)
		if err != nil {
			return err
		}
		for _, ti := range infos {
			if ti.Type == "iframe" && strings.Contains(ti.URL, "challenges.cloudflare.com") {
				iframeID = ti.TargetID
				return nil
			}
		}
		return nil
	}))
	if err != nil {
		return false, err
	}
	if iframeID == "" {
		return false, nil
	}

	// Build (or reuse) a session attached to the challenge iframe target.
	var base = ct
	if oopifCtx != nil {
		if *oopifCtx == nil {
			c, cancel := chromedp.NewContext(ct, chromedp.WithTargetID(iframeID))
			*oopifCtx = c
			*oopifCancel = cancel
		}
		base = *oopifCtx
	}

	// Allow generous time for the click round-trip on the reused session.
	clickCtx, clickCancel := context.WithTimeout(base, 25*time.Second)
	defer clickCancel()

	clicked := false
	for _, pt := range turnstileCheckboxPoints {
		if err := chromedp.Run(clickCtx,
			chromedp.MouseEvent(input.MouseMoved, pt[0], pt[1]),
			chromedp.MouseClickXY(pt[0], pt[1]),
		); err != nil {
			log.Debug().Err(err).Msg("turnstile: OOPIF checkbox click failed")
		} else {
			clicked = true
		}
	}
	if clicked {
		log.Info().Msg("turnstile: clicked OOPIF challenge checkbox")
	}
	return clicked, nil
}

func waitForRealPage(ct context.Context, timeout time.Duration) error {
	waitCtx, waitCancel := context.WithTimeout(ct, timeout)
	defer waitCancel()

	// A single reused OOPIF session (created lazily on the first challenge click)
	// so we don't re-attach a CDP session to the challenge iframe every poll.
	var oopifCtx context.Context
	var oopifCancel context.CancelFunc
	defer func() {
		if oopifCancel != nil {
			oopifCancel()
		}
	}()

	var title string
	// ~60 attempts with 2s sleep between each
	attempts := int(timeout / (2 * time.Second))
	clicks := 0
	var lastClick time.Time
	for i := 0; i < attempts; i++ {
		if err := retry(5, 500*time.Millisecond, func() error {
			return chromedp.Run(waitCtx, chromedp.Title(&title))
		}); err != nil {
			return err
		}

		if title != "" && !isChallengeTitle(title) {
			return nil
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

		// Interactive Turnstile challenges never clear on their own — try
		// clicking the checkbox while we wait. Paced to at most one attempt
		// every 3s so repeated polls don't machine-gun the widget.
		if clicks < maxTurnstileClicks && time.Since(lastClick) >= 3*time.Second {
			clicked, err := tryClickTurnstile(waitCtx, &oopifCtx, &oopifCancel)
			if err != nil {
				log.Debug().Err(err).Msg("turnstile: click attempt failed")
			} else if clicked {
				clicks++
				lastClick = time.Now()
			}
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for real page, last title: %q: %w", title, context.DeadlineExceeded)
}
