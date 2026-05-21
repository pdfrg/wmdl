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

	primary := []string{slug}
	if year > 0 {
		primary = append(primary, fmt.Sprintf("%s_%d", slug, year))
	}

	for _, s := range primary {
		url := fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, s)
		if r.urlExists(url) {
			return url
		}
	}

	// Try stripping leading articles: "the_", "a_", "an_"
	for _, article := range []string{"the_", "a_", "an_"} {
		if strings.HasPrefix(slug, article) {
			base := slug[len(article):]
			secondary := []string{base}
			if year > 0 {
				secondary = append(secondary, fmt.Sprintf("%s_%d", base, year))
			}
			for _, s := range secondary {
				url := fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, s)
				if r.urlExists(url) {
					return url
				}
			}
		}
	}

	// Try compact slug (no separators)
	compact := strings.ReplaceAll(slug, "_", "")
	if compact != slug {
		tertiary := []string{compact}
		if year > 0 {
			tertiary = append(tertiary, fmt.Sprintf("%s_%d", compact, year))
		}
		for _, s := range tertiary {
			url := fmt.Sprintf("https://www.rottentomatoes.com/%s/%s", prefix, s)
			if r.urlExists(url) {
				return url
			}
		}
	}

	return ""
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
