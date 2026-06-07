package discover

import (
	"strings"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

type FilterResult struct {
	Passed bool
	Reason string
}

var languageNameToISO = map[string]string{
	"english":    "en",
	"french":     "fr",
	"german":     "de",
	"spanish":    "es",
	"japanese":   "ja",
	"korean":     "ko",
	"chinese":    "zh",
	"hindi":      "hi",
	"arabic":     "ar",
	"portuguese": "pt",
	"russian":    "ru",
	"italian":    "it",
	"dutch":      "nl",
	"polish":     "pl",
	"turkish":    "tr",
	"swedish":    "sv",
	"danish":     "da",
	"finnish":    "fi",
	"norwegian":  "no",
	"czech":      "cs",
	"greek":      "el",
	"hebrew":     "he",
	"thai":       "th",
	"vietnamese": "vi",
	"indonesian": "id",
	"malay":      "ms",
	"romanian":   "ro",
	"ukrainian":  "uk",
	"hungarian":  "hu",
}

func normalizeLanguage(lang string) string {
	lower := strings.ToLower(lang)
	if iso, ok := languageNameToISO[lower]; ok {
		return iso
	}
	return lower
}

func filterIsEmpty(f *config.ContentFilter) bool {
	return len(f.AllowedLanguages) == 0 &&
		len(f.BlockedLanguages) == 0 &&
		len(f.BlockedCountries) == 0 &&
		len(f.BlockedGenres) == 0 &&
		len(f.OverrideGenres) == 0
}

func FilterTitle(f *config.ContentFilter, t *model.Title) FilterResult {
	if filterIsEmpty(f) {
		return FilterResult{Passed: true}
	}

	lang := strings.ToLower(t.OriginalLanguage)

	if len(f.AllowedLanguages) > 0 {
		if !containsString(f.AllowedLanguages, lang) {
			return FilterResult{Passed: false, Reason: "language " + t.OriginalLanguage + " not in allowed list"}
		}
	}

	if len(f.BlockedLanguages) > 0 {
		if containsString(f.BlockedLanguages, lang) {
			return FilterResult{Passed: false, Reason: "language " + t.OriginalLanguage + " is blocked"}
		}
	}

	if len(f.BlockedCountries) > 0 && t.OriginCountry != "" {
		countries := strings.Split(t.OriginCountry, ",")
		for _, c := range countries {
			c = strings.TrimSpace(c)
			if containsString(f.BlockedCountries, strings.ToLower(c)) {
				return FilterResult{Passed: false, Reason: "country " + c + " is blocked"}
			}
		}
	}

	if len(f.BlockedGenres) > 0 && t.Genres != "" {
		for _, blocked := range f.BlockedGenres {
			if hasGenre(t.Genres, blocked) {
				return FilterResult{Passed: false, Reason: "genre " + blocked + " is blocked"}
			}
		}
	}

	return FilterResult{Passed: true}
}

func FilterMusic(f *config.ContentFilter, artist *model.Artist, album *model.Album) FilterResult {
	if filterIsEmpty(f) {
		return FilterResult{Passed: true}
	}

	if len(f.BlockedCountries) > 0 && artist.Country != "" {
		if containsString(f.BlockedCountries, strings.ToLower(artist.Country)) {
			return FilterResult{Passed: false, Reason: "country " + artist.Country + " is blocked"}
		}
	}

	if len(f.BlockedGenres) > 0 && album.Genres != "" {
		for _, blocked := range f.BlockedGenres {
			if hasGenre(album.Genres, blocked) {
				return FilterResult{Passed: false, Reason: "genre " + blocked + " is blocked for album"}
			}
		}
	}

	return FilterResult{Passed: true}
}

func FilterBook(f *config.ContentFilter, b *model.Book) FilterResult {
	if filterIsEmpty(f) {
		return FilterResult{Passed: true}
	}

	lang := normalizeLanguage(b.Language)

	if len(f.AllowedLanguages) > 0 {
		if !containsString(f.AllowedLanguages, lang) {
			return FilterResult{Passed: false, Reason: "language " + b.Language + " not in allowed list"}
		}
	}

	if len(f.BlockedLanguages) > 0 {
		if containsString(f.BlockedLanguages, lang) {
			return FilterResult{Passed: false, Reason: "language " + b.Language + " is blocked"}
		}
	}

	if len(f.BlockedGenres) > 0 && b.Tags != "" {
		for _, blocked := range f.BlockedGenres {
			if hasGenre(b.Tags, blocked) {
				return FilterResult{Passed: false, Reason: "tag " + blocked + " is blocked"}
			}
		}
	}

	return FilterResult{Passed: true}
}

func HasOverrideGenre(f *config.ContentFilter, genres string) bool {
	if len(f.OverrideGenres) == 0 || genres == "" {
		return false
	}
	for _, override := range f.OverrideGenres {
		if hasGenre(genres, override) {
			return true
		}
	}
	return false
}

func hasGenre(genres, target string) bool {
	lower := strings.ToLower(genres)
	targetLower := strings.ToLower(target)
	return strings.Contains(lower, targetLower)
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
