package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/rs/zerolog/log"
)

type RTRatings struct {
	URL               string
	CriticsScore      float64
	AudienceScore     float64
	AudienceRealScore float64
	RealVotes         int
}

// rtPool is a pool of audience ratings from the RT media-scorecard JSON.
type rtPool struct {
	ScoreType     string `json:"scoreType"` // "VERIFIED" or "ALL"
	Score         string `json:"score"`     // percentage as string, e.g. "97"
	LikedCount    *int   `json:"likedCount"`
	NotLikedCount *int   `json:"notLikedCount"`
}

type rtScorecardJSON struct {
	AudienceScore *rtPool    `json:"audienceScore"`
	CriticsScore  *rtPool    `json:"criticsScore"`
	Overlay       *rtOverlay `json:"overlay"`
}

type rtOverlay struct {
	AudienceAll      *rtPool `json:"audienceAll"`
	AudienceVerified *rtPool `json:"audienceVerified"`
	CriticsAll       *rtPool `json:"criticsAll"`
	CriticsTop       *rtPool `json:"criticsTop"`
}

var scorecardRE = regexp.MustCompile(
	`id="media-scorecard-json"[^>]*type="application/json"[^>]*>([^<]*)</script>`)

// ScrapeRTRatings fetches an RT movie/TV page, extracts the embedded
// media-scorecard-json, and returns headline + real audience scores.
func ScrapeRTRatings(ctx context.Context, rtURL string) *RTRatings {
	ratings := &RTRatings{URL: rtURL}

	data, err := fetchScorecard(ctx, rtURL)
	if err != nil {
		log.Warn().Err(err).Str("url", rtURL).Msg("RT scrape failed")
		return ratings
	}

	// --- Critics score ---
	if data.CriticsScore != nil {
		if s := parsePct(data.CriticsScore.Score); s > 0 {
			ratings.CriticsScore = s
		}
	}

	// --- Audience score ---
	if data.AudienceScore != nil {
		if s := parsePct(data.AudienceScore.Score); s > 0 {
			ratings.AudienceScore = s
		}
	}

	// --- Fallback: overlay audience (ALL pool) ---
	if ratings.AudienceScore == 0 {
		if ov := data.Overlay; ov != nil && ov.AudienceAll != nil {
			if s := parsePct(ov.AudienceAll.Score); s > 0 {
				ratings.AudienceScore = s
			}
		}
	}

	// --- Fallback: overlay critics ---
	if ratings.CriticsScore == 0 {
		if ov := data.Overlay; ov != nil && ov.CriticsAll != nil {
			if s := parsePct(ov.CriticsAll.Score); s > 0 {
				ratings.CriticsScore = s
			}
		}
	}

	// --- Real audience score ---
	computeRealScore(ratings, data)

	return ratings
}

func fetchScorecard(ctx context.Context, rtURL string) (*rtScorecardJSON, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rtURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	m := scorecardRE.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("media-scorecard-json not found")
	}

	var data rtScorecardJSON
	if err := json.Unmarshal(m[1], &data); err != nil {
		return nil, fmt.Errorf("json parse: %w", err)
	}
	return &data, nil
}

// computeRealScore computes the unverified-only audience score from the
// verified push pool subtracted from the all-audience pool. Only computed
// when the headline score type is VERIFIED — meaning there IS a push pool
// to strip out.
func computeRealScore(r *RTRatings, data *rtScorecardJSON) {
	ov := data.Overlay
	if ov == nil {
		return
	}

	// Only meaningful when the headline represents a VERIFIED (push) pool.
	// For ALL-type headlines the headline IS the real score already.
	if data.AudienceScore == nil || data.AudienceScore.ScoreType != "VERIFIED" {
		return
	}

	aa := ov.AudienceAll
	av := ov.AudienceVerified

	hasAll := aa != nil && aa.LikedCount != nil && aa.NotLikedCount != nil
	hasVer := av != nil && av.LikedCount != nil && av.NotLikedCount != nil

	if !hasAll || !hasVer {
		return
	}

	allLikes := *aa.LikedCount
	allDislikes := *aa.NotLikedCount
	verLikes := *av.LikedCount
	verDislikes := *av.NotLikedCount

	unvLikes := allLikes - verLikes
	unvDislikes := allDislikes - verDislikes
	unvTotal := unvLikes + unvDislikes

	if unvTotal < 30 {
		return
	}

	realScore := float64(unvLikes) / float64(unvTotal) * 100

	r.AudienceRealScore = realScore
	r.RealVotes = unvTotal
}

func parsePct(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

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

	// Pick best by title similarity + year proximity
	searchTitle := strings.ToLower(title)
	best := byType[0]
	bestScore := scoreSearchResult(best, year, searchTitle)
	for _, r := range byType[1:] {
		if s := scoreSearchResult(r, year, searchTitle); s > bestScore {
			best = r
			bestScore = s
		}
	}

	// Reject if even the best match has insufficient title overlap
	if bestScore < 0 {
		return ""
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

// scoreSearchResult scores an RT search result by word-order-sensitive title
// similarity and year proximity. Returns a large negative score if there's no
// meaningful title overlap (insufficient LCS of significant words).
func scoreSearchResult(r RTSearchResult, searchYear int, searchTitle string) int {
	ts, sufficient := titleMatchScore(searchTitle, r.Title)
	if !sufficient {
		return -100000
	}

	score := ts * 20

	if searchYear > 0 && r.Year > 0 {
		diff := searchYear - r.Year
		if diff < 0 {
			diff = -diff
		}
		switch diff {
		case 0:
			score += 50
		case 1:
			score += 20
		default:
			score -= 30
		}
	}

	searchLower := strings.ToLower(searchTitle)
	resultLower := strings.ToLower(r.Title)
	if strings.Contains(resultLower, searchLower) {
		score += 100
	}

	return score
}

// stopWords are common English words (>3 chars) that shouldn't count as
// significant for title matching, to prevent false positives like "this".
var stopWords = map[string]bool{
	"this": true, "that": true, "with": true, "from": true,
	"what": true, "where": true, "when": true, "which": true,
	"their": true, "they": true, "have": true, "been": true,
	"were": true, "will": true, "would": true, "could": true,
	"there": true, "also": true, "than": true, "then": true,
	"very": true, "just": true, "like": true, "more": true,
	"some": true, "them": true, "into": true, "over": true,
	"such": true, "each": true, "other": true, "about": true,
	"your": true,
}

var wordSplit = regexp.MustCompile(`[^a-z0-9]+`)

// significantWords extracts meaningful words from a title, excluding short
// words (≤3 chars) and common stop words.
func significantWords(title string) []string {
	title = strings.ToLower(title)
	parts := wordSplit.Split(title, -1)
	var out []string
	for _, p := range parts {
		if len(p) <= 3 {
			continue
		}
		if stopWords[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// lcsLen returns the length of the longest common subsequence between two
// string slices, preserving element order.
func lcsLen(a, b []string) int {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[m][n]
}

// titleMatchScore evaluates whether an RT search result title sufficiently
// matches the search title using order-aware word matching (LCS).
// Returns a similarity score (0-100) and whether the match is sufficient.
//
// Rules:
//   - Single significant search word: sufficient if it appears in the result.
//   - Multiple search words: sufficient if LCS ≥ 2 AND LCS ≥ 50% of search words.
func titleMatchScore(searchTitle, resultTitle string) (int, bool) {
	sWords := significantWords(searchTitle)
	rWords := significantWords(resultTitle)

	if len(sWords) == 0 || len(rWords) == 0 {
		return 0, false
	}

	lcs := lcsLen(sWords, rWords)

	if len(sWords) == 1 {
		if lcs >= 1 {
			return 100, true
		}
		return 0, false
	}

	ratio := float64(lcs) / float64(len(sWords))
	sufficient := lcs >= 2 && ratio >= 0.5
	if !sufficient {
		return 0, false
	}
	score := int(ratio * 100)
	if score > 100 {
		score = 100
	}
	return score, true
}
