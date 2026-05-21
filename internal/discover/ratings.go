package discover

import (
	"context"
	"fmt"
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

func ScrapeRTRatings(ctx context.Context, rtURL string) (*RTRatings, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, "http://127.0.0.1:9222")
	defer allocCancel()

	ct, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ct, 30*time.Second)
	defer cancel()

	var criticsText, audienceText string

	err := chromedp.Run(ctx,
		chromedp.Navigate(rtURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.Text(`[data-qa="critics-score"]`, &criticsText, chromedp.ByQueryAll),
		chromedp.Text(`[data-qa="audience-score"]`, &audienceText, chromedp.ByQueryAll),
	)
	if err != nil {
		return nil, fmt.Errorf("scraping RT page: %w", err)
	}

	ratings := &RTRatings{URL: rtURL}

	if criticsText != "" {
		ratings.CriticsScore = parsePercent(criticsText)
	}
	if audienceText != "" {
		ratings.AudienceScore = parsePercent(audienceText)
	}

	return ratings, nil
}

var percentPattern = regexp.MustCompile(`(\d+)%`)

func parsePercent(s string) float64 {
	m := percentPattern.FindStringSubmatch(s)
	if len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 0
}
