package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"
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
		log.Warn().Err(err).Msg("RT navigate failed")
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

type RTSearchResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Year  int    `json:"year"`
	Type  string `json:"type"` // "movie" or "tv"
}

// rtSearchJS extracts search results from RT's search page using the
// actual page structure: <search-page-media-row> elements inside
// <search-page-result type="movie|tvSeries"> containers.
var rtSearchJS = `
(() => {
  const rows = document.querySelectorAll('search-page-media-row');
  const results = [];
  for (const row of rows) {
    const parent = row.closest('search-page-result');
    const type = parent ? parent.getAttribute('type') : '';
    const titleLink = row.querySelector('a[data-qa="info-name"]');
    if (!titleLink) continue;
    const title = titleLink.textContent.trim();
    const url = titleLink.getAttribute('href') || '';
    if (!url || !title) continue;
    const yearStr = row.getAttribute('release-year') || '';
    const year = yearStr ? parseInt(yearStr, 10) : 0;
    // Map RT's type naming to ours: "tvSeries" -> "tv"
    const mappedType = type === 'tvSeries' ? 'tv' : (type === 'movie' ? 'movie' : '');
    if (!mappedType) continue;
    results.push({
      url: url.startsWith('http') ? url : 'https://www.rottentomatoes.com' + url,
      title: title,
      year: year,
      type: mappedType
    });
  }
  return JSON.stringify(results.slice(0, 30));
})()
`

// SearchRTSite uses chromedp to navigate RT search and scrape the best matching
// result URL. Only called as a fallback when URL guessing fails.
func SearchRTSite(ctx context.Context, allocCtx context.Context, title string, year int, mediaType string) string {
	ct, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {}))
	defer cancel()

	searchCtx, cancel := context.WithTimeout(ct, 15*time.Second)
	defer cancel()

	searchURL := fmt.Sprintf("https://www.rottentomatoes.com/search?search=%s", url.QueryEscape(title))

	var resultsJSON string
	if err := chromedp.Run(searchCtx,
		chromedp.Navigate(searchURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(rtSearchJS, &resultsJSON),
	); err != nil {
		log.Warn().Err(err).Msg("RT search navigate failed")
		return ""
	}

	var results []RTSearchResult
	if err := json.Unmarshal([]byte(resultsJSON), &results); err != nil {
		log.Warn().Err(err).Msg("RT search parse failed")
		return ""
	}

	if len(results) == 0 {
		return ""
	}

	// Filter by media type
	var byType []RTSearchResult
	for _, r := range results {
		if r.Type == mediaType {
			byType = append(byType, r)
		}
	}
	if len(byType) == 0 {
		byType = results
	}

	// Pick best by year proximity (prefer exact, then ±1)
	searchTitle := strings.ToLower(title)
	best := byType[0]
	bestScore := scoreSearchResult(best, year, searchTitle)
	for _, r := range byType[1:] {
		if s := scoreSearchResult(r, year, searchTitle); s > bestScore {
			best = r
			bestScore = s
		}
	}

	// Only return if it's a reasonable match (year within ±1, or any year if
	// search year is 0)
	if year > 0 && best.Year > 0 {
		diff := year - best.Year
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			return ""
		}
	}

	return best.URL
}

// scoreSearchResult scores an RT search result by year proximity and title
// match. Returns a large negative score if there's no meaningful title overlap
// (at least one word longer than 3 chars in common), to prevent returning
// completely unrelated results.
func scoreSearchResult(r RTSearchResult, searchYear int, searchTitle string) int {
	// Require at least one significant word overlap between search and result
	title := strings.ToLower(r.Title)
	if !hasWordOverlap(searchTitle, title) {
		return -100000
	}

	score := 0

	// Year score
	if searchYear > 0 && r.Year > 0 {
		diff := searchYear - r.Year
		if diff < 0 {
			diff = -diff
		}
		switch diff {
		case 0:
			score += 5
		case 1:
			score += 2
		}
	}

	// Title match bonus
	if strings.Contains(title, searchTitle) {
		score += 2
	}

	return score
}

// hasWordOverlap checks if two strings share at least one word longer than 3
// characters. This prevents RT search from matching completely unrelated titles.
func hasWordOverlap(a, b string) bool {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	for _, wa := range wordsA {
		if len(wa) <= 3 {
			continue
		}
		for _, wb := range wordsB {
			if strings.EqualFold(wa, wb) {
				return true
			}
		}
	}
	return false
}
