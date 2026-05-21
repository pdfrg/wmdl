package discover

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

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
	if year > 0 {
		slug = fmt.Sprintf("%s_%d", slug, year)
	}

	prefix := "m"
	if mediaType == "tv" {
		prefix = "tv"
	}

	url := fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, slug)

	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36")

	resp, err := r.http.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return url
	}

	// Try without year
	if year > 0 {
		return r.FindURL(title, 0, mediaType)
	}

	return ""
}

func slugify(title string) string {
	title = strings.ToLower(title)
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
