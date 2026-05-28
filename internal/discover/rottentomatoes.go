package discover

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	nonAlphaNum    = regexp.MustCompile(`[^a-z0-9]+`)
	rtDatePat      = regexp.MustCompile(`dateCreated":"(\d{4}-\d{2}-\d{2})`)
	rtYearFallback = regexp.MustCompile(`releaseYear":"(\d{4})"`)
)

var translitMap = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c",
	"&", "and",
	"'", "",
	"’", "",
)

type RTFinder struct {
	http *http.Client
}

func NewRTFinder() *RTFinder {
	return &RTFinder{
		http: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func (r *RTFinder) FindURL(title string, year int, mediaType string) string {
	slug := slugify(title)
	prefix := "m"
	if mediaType == "tv" {
		prefix = "tv"
	}

	// Collect all candidate URLs
	var candidates []string

	searchYears := []int{year}
	if year > 0 {
		if year-1 > 0 {
			searchYears = []int{year, year - 1, year + 1}
		} else {
			searchYears = []int{year, year + 1}
		}
	}

	addCandidates := func(baseSlug string) {
		for _, y := range searchYears {
			candidates = append(candidates,
				fmt.Sprintf("https://www.rottentomatoes.com/%s/%s_%d", prefix, baseSlug, y))
		}
		candidates = append(candidates,
			fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, baseSlug))
	}

	addCandidates(slug)

	// _2 variant for movies with same title+year
	if prefix == "m" {
		for _, y := range searchYears {
			candidates = append(candidates,
				fmt.Sprintf("https://www.rottentomatoes.com/m/%s_%d_2", slug, y))
		}
	}

	// Strip leading articles: "the_", "a_", "an_"
	for _, article := range []string{"the_", "a_", "an_"} {
		if strings.HasPrefix(slug, article) {
			addCandidates(slug[len(article):])
			if prefix == "m" {
				for _, y := range searchYears {
					candidates = append(candidates,
						fmt.Sprintf("https://www.rottentomatoes.com/m/%s_%d_2", slug[len(article):], y))
				}
			}
		}
	}

	// Compact slug (no separators)
	compact := strings.ReplaceAll(slug, "_", "")
	if compact != slug {
		addCandidates(compact)
	}

	// Evaluate candidates
	type scoredURL struct {
		url      string
		pageYear int // 0 = not extracted
		score    int
	}

	var found []scoredURL
	for _, u := range candidates {
		if !r.urlExists(u) {
			continue
		}
		s := scoredURL{url: u}

		// URL suffix year scoring (both movies and TV)
		if year > 0 {
			if suffYr := extractURLYear(u); suffYr > 0 {
				diff := year - suffYr
				if diff < 0 {
					diff = -diff
				}
				switch diff {
				case 0:
					s.score += 5
				case 1:
					s.score += 2
				default:
					s.score -= 3
				}
			}
		}

		// Page dateCreated year validation (movies only)
		if prefix != "tv" && year > 0 {
			if yrStr, ok := r.extractRTYear(u); ok {
				pgYr, _ := strconv.Atoi(yrStr)
				if pgYr > 0 {
					s.pageYear = pgYr
					diff := year - pgYr
					if diff < 0 {
						diff = -diff
					}
					switch diff {
					case 0:
						s.score += 5
					case 1:
						s.score += 2
					default:
						s.score -= 3
					}
				}
			}
		}

		found = append(found, s)
	}

	if len(found) == 0 {
		// Return RT search URL as fallback
		return fmt.Sprintf("https://www.rottentomatoes.com/search?search=%s", url.QueryEscape(title))
	}

	// Pick the best match
	best := found[0]
	for _, f := range found[1:] {
		if f.score > best.score {
			best = f
		}
	}

	// If the best match is a bare slug (no year suffix), we need year
	// verification. Reject it if:
	//   - Page year was extracted and differs by >1
	//   - Page year could NOT be extracted (pageYear == 0) — can't verify
	// In both cases, return the search URL as fallback instead.
	if year > 0 && extractURLYear(best.url) == 0 {
		if best.pageYear == 0 {
			return fmt.Sprintf("https://www.rottentomatoes.com/search?search=%s", url.QueryEscape(title))
		}
		diff := year - best.pageYear
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			return fmt.Sprintf("https://www.rottentomatoes.com/search?search=%s", url.QueryEscape(title))
		}
	}

	return best.url
}

// extractURLYear extracts the year from a year-suffixed RT URL.
// Handles patterns like /m/slug_2025 and /m/slug_2025_2.
func extractURLYear(u string) int {
	re := regexp.MustCompile(`_(\d{4})(?:_\d+)?$`)
	m := re.FindStringSubmatch(u)
	if len(m) > 1 {
		y, err := strconv.Atoi(m[1])
		if err == nil {
			return y
		}
	}
	return 0
}

func (r *RTFinder) extractRTYear(url string) (string, bool) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := r.http.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	// Read enough to find year info
	buf := make([]byte, 65536)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	// Try JSON-LD dateCreated first (most reliable)
	if m := rtDatePat.FindStringSubmatch(body); len(m) > 1 && len(m[1]) >= 4 {
		return m[1][:4], true
	}

	// Fallback: look for releaseYear field in embedded JSON data (present on
	// most RT movie pages even without dateCreated in JSON-LD).
	if m := rtYearFallback.FindStringSubmatch(body); len(m) > 1 {
		return m[1], true
	}

	return "", false
}

func (r *RTFinder) urlExists(url string) bool {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := r.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func slugify(title string) string {
	title = strings.ToLower(title)
	title = translitMap.Replace(title)

	var b strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	slug := nonAlphaNum.ReplaceAllString(b.String(), "_")
	slug = strings.Trim(slug, "_")
	return slug
}
