package discover

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var (
	nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)
	rtDatePat   = regexp.MustCompile(`dateCreated":"(\d{4}-\d{2}-\d{2})`)
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

	// Collect all candidate URLs in priority order
	var candidates []string

	addCandidates := func(baseSlug string) {
		if year > 0 {
			candidates = append(candidates, fmt.Sprintf("https://www.rottentomatoes.com/%s/%s_%d", prefix, baseSlug, year))
		}
		candidates = append(candidates, fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, baseSlug))
	}

	addCandidates(slug)

	// Try year-suffixed _2 variant for movies with same title+year
	if prefix == "m" && year > 0 {
		candidates = append(candidates,
			fmt.Sprintf("https://www.rottentomatoes.com/m/%s_%d_2", slug, year))
	}

	// Try stripping leading articles: "the_", "a_", "an_"
	for _, article := range []string{"the_", "a_", "an_"} {
		if strings.HasPrefix(slug, article) {
			addCandidates(slug[len(article):])
			if prefix == "m" && year > 0 {
				candidates = append(candidates,
					fmt.Sprintf("https://www.rottentomatoes.com/m/%s_%d_2", slug[len(article):], year))
			}
		}
	}

	// Try compact slug (no separators)
	compact := strings.ReplaceAll(slug, "_", "")
	if compact != slug {
		addCandidates(compact)
	}

	// Evaluate candidates: prefer exact year match, then any valid URL
	type scoredURL struct {
		url   string
		year  string
		score int
	}

	var found []scoredURL
	for _, u := range candidates {
		if r.urlExists(u) {
			s := scoredURL{url: u}
			// dateCreated year validation only for movies (TV date reflects
			// the first season's air date, not the current release).
			if prefix != "tv" {
				if yr, ok := r.extractRTYear(u); ok {
					s.year = yr
					if year > 0 && yr == fmt.Sprintf("%d", year) {
						s.score += 2
					}
				}
			}
			// Prefer year-suffixed URLs (more specific)
			if strings.Contains(u, "_"+fmt.Sprintf("%d", year)) {
				s.score += 1
			}
			found = append(found, s)
		}
	}

	if len(found) == 0 {
		return ""
	}

	// Pick the best match
	best := found[0]
	for _, f := range found[1:] {
		if f.score > best.score {
			best = f
		}
	}
	return best.url
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

	// Read enough to find dateCreated in JSON-LD
	buf := make([]byte, 65536)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	m := rtDatePat.FindStringSubmatch(body)
	if len(m) < 2 || len(m[1]) < 4 {
		return "", false
	}
	return m[1][:4], true
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
