package quality

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	seasonNumPat     = regexp.MustCompile(`(?i)\bseason\s+(\d+)\b`)
	shortSeasonPat   = regexp.MustCompile(`(?i)\bs(\d+)(?:e\d+)?\b`)
	ordinalSeasonPat = regexp.MustCompile(`(?i)(\d+)(?:st|nd|rd|th)\s+season\b`)
	wordSeasonPat    = regexp.MustCompile(`(?i)\bseason\s+(` + seasonWords + `)\b`)
	wordSeasonRevPat = regexp.MustCompile(`(?i)(?:complete\s+)?(` + seasonWords + `)\s+season\b`)

	stripParenPat = regexp.MustCompile(`(?i)\s*\([^)]*\bseason\s*(?:\d+|` + seasonWords + `)[^)]*\)\s*`)
	stripTrailPat = regexp.MustCompile(`(?i)\s+(?:complete\s+)?(?:season\s+(?:\d+|` + seasonWords + `)|(?:\d+)(?:st|nd|rd|th)\s+season|` + seasonWords + `\s+season)\s*$`)
)

const seasonWords = `twenty|nineteen|eighteen|seventeen|sixteen|fifteen|fourteen|thirteen|twelve|eleven|ten|nine|eight|seven|six|five|four|three|two|one|twentieth|nineteenth|eighteenth|seventeenth|sixteenth|fifteenth|fourteenth|thirteenth|twelfth|eleventh|tenth|ninth|eighth|seventh|sixth|fifth|fourth|third|second|first`

var seasonWordMap = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5,
	"sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
	"eleventh": 11, "twelfth": 12, "thirteenth": 13, "fourteenth": 14,
	"fifteenth": 15, "sixteenth": 16, "seventeenth": 17, "eighteenth": 18,
	"nineteenth": 19, "twentieth": 20,
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14,
	"fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20,
}

func ParseSeasonNumber(title string) int {
	lower := strings.ToLower(title)

	// "season N" or "(season N)"
	if m := seasonNumPat.FindStringSubmatch(lower); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}

	// "S{N}" short form
	if m := shortSeasonPat.FindStringSubmatch(lower); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}

	// "Nth Season"
	if m := ordinalSeasonPat.FindStringSubmatch(lower); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}

	// "Season {word}"
	if m := wordSeasonPat.FindStringSubmatch(lower); len(m) > 1 {
		if n, ok := seasonWordMap[m[1]]; ok {
			return n
		}
	}

	// "complete {word} Season" or "{word} Season"
	if m := wordSeasonRevPat.FindStringSubmatch(lower); len(m) > 1 {
		if n, ok := seasonWordMap[m[1]]; ok {
			return n
		}
	}

	return 1
}

var seasonSuffixWords = map[string]bool{
	"first": true, "second": true, "third": true, "fourth": true, "fifth": true,
	"sixth": true, "seventh": true, "eighth": true, "ninth": true, "tenth": true,
	"eleventh": true, "twelfth": true, "thirteenth": true, "fourteenth": true,
	"fifteenth": true, "sixteenth": true, "seventeenth": true, "eighteenth": true,
	"nineteenth": true, "twentieth": true,
	"one": true, "two": true, "three": true, "four": true, "five": true,
	"six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "thirteen": true, "fourteen": true,
	"fifteen": true, "sixteen": true, "seventeen": true, "eighteen": true,
	"nineteen": true, "twenty": true,
}

func StripSeason(title string) string {
	s := title

	// Remove parenthesized season: "Show (season 2)" → "Show"
	s = stripParenPat.ReplaceAllString(s, "")

	// Remove colon-separated season: "Show: Season Fifteen" → "Show"
	if idx := strings.Index(s, ":"); idx >= 0 {
		suffix := strings.TrimSpace(s[idx+1:])
		lower := strings.ToLower(suffix)
		if containsSeasonKeywords(lower) {
			s = strings.TrimSpace(s[:idx])
		}
	}

	// Remove trailing season patterns: "Show season 2" → "Show"
	s = stripTrailPat.ReplaceAllString(s, "")

	return strings.TrimSpace(s)
}

func containsSeasonKeywords(s string) bool {
	if strings.Contains(s, "season") || strings.Contains(s, "complete") {
		return true
	}
	words := strings.Fields(s)
	for _, w := range words {
		if seasonSuffixWords[w] {
			return true
		}
	}
	return false
}
