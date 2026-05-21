package discover

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"time"

	"github.com/chromedp/chromedp"
)

type RTRatings struct {
	URL           string
	CriticsScore  float64
	AudienceScore float64
}

func ScrapeRTRatings(ctx context.Context, allocCtx context.Context, rtURL string) *RTRatings {
	ratings := &RTRatings{URL: rtURL}

	ct, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer cancel()

	scrapeCtx, cancel := context.WithTimeout(ct, 20*time.Second)
	defer cancel()

	if err := chromedp.Run(scrapeCtx,
		chromedp.Navigate(rtURL),
		chromedp.WaitReady("body"),
	); err != nil {
		log.Printf("    RT navigate failed: %v", err)
		return ratings
	}

	// Wait for the scorecard to render, with a short grace period
	_ = chromedp.Run(scrapeCtx, chromedp.WaitVisible("media-scorecard", chromedp.ByQuery))

	// Scores are in light DOM rt-text elements slotted into the web component.
	// Use collapsed scores first (preferred), then full scores as fallback.
	ratings.CriticsScore = extractPct(scrapeCtx,
		`document.querySelector('rt-text[slot="collapsed-critics-score"]')?.textContent?.trim() || ''`)
	if ratings.CriticsScore == 0 {
		ratings.CriticsScore = extractPct(scrapeCtx,
			`document.querySelector('rt-text[slot="critics-score"]')?.textContent?.trim() || ''`)
	}

	ratings.AudienceScore = extractPct(scrapeCtx,
		`document.querySelector('rt-text[slot="collapsed-audience-score"]')?.textContent?.trim() || ''`)
	if ratings.AudienceScore == 0 {
		ratings.AudienceScore = extractPct(scrapeCtx,
			`document.querySelector('rt-text[slot="audience-score"]')?.textContent?.trim() || ''`)
	}

	return ratings
}

func extractPct(ctx context.Context, js string) float64 {
	var text string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &text)); err != nil || text == "" {
		return 0
	}
	m := percentPattern.FindStringSubmatch(text)
	if len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 0
}

var percentPattern = regexp.MustCompile(`(\d+)%`)
