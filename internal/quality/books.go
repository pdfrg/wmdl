package quality

import (
	"regexp"
	"strings"
)

type BookQualityPrefs struct {
	EbookFormatPriority     []string
	AudiobookFormatPriority []string
	MinSeeders              int
	PreferredGroups         []string
}

type ParsedBookRelease struct {
	ParsedRelease
	EbookFormat     string // epub, mobi, pdf, azw3, etc. (empty if audiobook)
	AudiobookFormat string // m4b, mp3, flac, etc. (empty if ebook)
	IsAudiobook     bool
	IsEbook         bool
}

var (
	bookEbookFormatPat  = regexp.MustCompile(`(?i)\b(EPUB|MOBI|AZW3?|PDF|DOCX?|TXT|RTF|CBR|CBZ|PRC|LRF|PDB)\b`)
	bookAudioFormatPat  = regexp.MustCompile(`(?i)\b(M4B|MP3|FLAC|AAC|OGG|OPUS|WMA|WAV|AIFF)\b`)
	bookBitratePat      = regexp.MustCompile(`(?i)\b(V0|V1|V2|VBR|CBR|320|256|192|128|64)\b`)
	bookAudioBitratePat = regexp.MustCompile(`(?i)\b(\d+)\s*kbps\b`)
	bookNarratorPat     = regexp.MustCompile(`(?i)\b(read\s+by|narrated\s+by|narrator)\b`)
	bookGroupPat        = regexp.MustCompile(`-([a-zA-Z0-9]+(?:\[[^\]]+\])?)$`)
)

func ParseBookRelease(rawTitle string) ParsedBookRelease {
	r := ParsedBookRelease{
		ParsedRelease: ParsedRelease{RawTitle: rawTitle},
	}
	clean := strings.ReplaceAll(rawTitle, ".", " ")
	clean = strings.ReplaceAll(clean, "_", " ")

	// Check for audiobook indicators first
	if bookNarratorPat.MatchString(clean) {
		r.IsAudiobook = true
	}

	// Parse formats
	ebookFmt := bookEbookFormatPat.FindString(clean)
	audioFmt := bookAudioFormatPat.FindString(clean)

	// Determine type based on context and format
	// If the title has ebook format, it's an ebook (unless also audiobook)
	if ebookFmt != "" && !r.IsAudiobook {
		// Prefer ebook format detection unless it's clearly an audiobook
		r.IsEbook = true
		r.EbookFormat = strings.ToLower(ebookFmt)
	}

	if audioFmt != "" {
		r.IsAudiobook = true
		r.AudiobookFormat = strings.ToLower(audioFmt)
	}

	// If no format detected but title contains common audiobook terms
	if !r.IsEbook && !r.IsAudiobook {
		if strings.Contains(clean, "audiobook") || strings.Contains(clean, "audio book") {
			r.IsAudiobook = true
			r.AudiobookFormat = "mp3" // default if unspecified
		}
	}

	// Parse bitrate
	bitrate := bookBitratePat.FindString(clean)
	if bitrate != "" {
		r.Codec = strings.ToLower(bitrate)
	}

	// Parse kbps bitrate
	if m := bookAudioBitratePat.FindStringSubmatch(clean); len(m) > 1 {
		if r.Codec == "" || r.Codec == "lossless" || len(r.Codec) < 5 {
			r.Codec = m[1] + "kbps"
		}
	}

	// Extract release group
	if m := bookGroupPat.FindStringSubmatch(rawTitle); len(m) > 1 {
		r.ReleaseGroup = m[1]
	}

	return r
}

func ScoreBook(r ParsedBookRelease, prefs BookQualityPrefs) int {
	score := 0

	if r.IsEbook && r.EbookFormat != "" {
		score += bookFormatScore(r.EbookFormat, prefs.EbookFormatPriority)
	}
	if r.IsAudiobook && r.AudiobookFormat != "" {
		score += bookFormatScore(r.AudiobookFormat, prefs.AudiobookFormatPriority)
	}

	score += groupBonus(r.ReleaseGroup, prefs.PreferredGroups)
	score += seederScore(r.Seeders, prefs.MinSeeders)

	return score
}

func bookFormatScore(format string, priority []string) int {
	if format == "" || len(priority) == 0 {
		return 0
	}
	norm := strings.ToLower(format)
	for i, p := range priority {
		if strings.EqualFold(norm, p) {
			return (len(priority) - i) * 100 / len(priority)
		}
	}
	return 0
}

func PartitionBookReleases(releases []ParsedBookRelease, author, title string) (exact, fuzzy []ParsedBookRelease) {
	authorWords := strings.Fields(strings.ToLower(author))
	titleWords := strings.Fields(strings.ToLower(title))

	for _, r := range releases {
		cleanTitle := strings.ToLower(r.RawTitle)

		if wordsMatchBook(cleanTitle, authorWords, titleWords) {
			exact = append(exact, r)
		} else {
			fuzzy = append(fuzzy, r)
		}
	}
	return
}

// PartitionBookReleasesRaw partitions raw Prowlarr results (pre-parse).
func PartitionBookReleasesRaw(releases []ParsedRelease, author, title string) (exact, fuzzy []ParsedRelease) {
	authorWords := strings.Fields(strings.ToLower(author))
	titleWords := strings.Fields(strings.ToLower(title))

	for _, r := range releases {
		cleanTitle := strings.ToLower(r.RawTitle)

		if wordsMatchBook(cleanTitle, authorWords, titleWords) {
			exact = append(exact, r)
		} else {
			fuzzy = append(fuzzy, r)
		}
	}
	return
}

func wordsMatchBook(clean string, authorWords, titleWords []string) bool {
	allMatch := true
	for _, w := range authorWords {
		if !strings.Contains(clean, w) {
			allMatch = false
			break
		}
	}
	if !allMatch {
		return false
	}
	for _, w := range titleWords {
		if !strings.Contains(clean, w) {
			return false
		}
	}
	return true
}

func SortBookTop(releases []ParsedBookRelease, prefs BookQualityPrefs, n int) []ParsedBookRelease {
	scores := make([]int, len(releases))
	for i, r := range releases {
		scores[i] = ScoreBook(r, prefs)
	}

	// Selection sort into top N
	result := make([]ParsedBookRelease, 0, n)
	used := make([]bool, len(releases))

	for len(result) < n && len(result) < len(releases) {
		bestIdx := -1
		for i := range releases {
			if used[i] {
				continue
			}
			if bestIdx < 0 || scores[i] > scores[bestIdx] {
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			break
		}
		used[bestIdx] = true
		result = append(result, releases[bestIdx])
	}

	return result
}
