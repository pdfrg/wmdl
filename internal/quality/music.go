package quality

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type MusicQualityPrefs struct {
	FormatPriority      []string
	BitratePriority     []string
	MinSeeders          int
	PreferredGroups     []string
	PreferredIndexerIDs []int // ordered by preference (earlier = more preferred)
}

var (
	musicFormatPat   = regexp.MustCompile(`(?i)\b(FLAC|MP3|AAC|M4A|APE|OGG|OPUS|WAV|AIFF|ALAC|DSD|WMA)\b`)
	musicLosslessPat = regexp.MustCompile(`(?i)\b(Lossless|CD|EAC|log|cue|24bit|96kHz|192kHz|Hi-Res|HD)\b`)
	musicBitratePat  = regexp.MustCompile(`(?i)\b(V0|V1|V2|VBR|CBR|320|256|192|128)\b`)
	musicGroupPat    = regexp.MustCompile(`-([a-zA-Z0-9]+(?:\[[^\]]+\])?)$`)
)

func ParseMusicRelease(rawTitle string) ParsedRelease {
	r := ParsedRelease{RawTitle: rawTitle}
	clean := strings.ReplaceAll(rawTitle, ".", " ")

	format := musicFormatPat.FindString(clean)
	if format != "" {
		r.Source = strings.ToLower(format)
	}

	if r.Source == "" && musicLosslessPat.MatchString(clean) {
		r.Source = "lossless"
	}

	bitrate := musicBitratePat.FindString(clean)
	if bitrate != "" {
		r.Codec = strings.ToLower(bitrate)
	}

	// Extract release group
	if m := musicGroupPat.FindStringSubmatch(rawTitle); len(m) > 1 {
		r.ReleaseGroup = m[1]
	}

	return r
}

func ScoreMusic(r ParsedRelease, prefs MusicQualityPrefs) int {
	score := 0
	score += musicFormatScore(r.Source, prefs.FormatPriority)
	score += musicBitrateScore(r.Codec, prefs.BitratePriority)
	score += groupBonus(r.ReleaseGroup, prefs.PreferredGroups)
	score += seederScore(r.Seeders, prefs.MinSeeders)
	score += preferredIndexerBonus(r.IndexerID, prefs.PreferredIndexerIDs)
	return score
}

func musicFormatScore(format string, priority []string) int {
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

func musicBitrateScore(bitrate string, priority []string) int {
	if bitrate == "" || len(priority) == 0 {
		return 0
	}
	norm := strings.ToLower(bitrate)
	for i, p := range priority {
		if strings.EqualFold(norm, p) {
			return (len(priority) - i) * 80 / len(priority)
		}
	}
	return 0
}

func BestMusic(releases []ParsedRelease, prefs MusicQualityPrefs) *ParsedRelease {
	if len(releases) == 0 {
		return nil
	}
	best := &releases[0]
	best.Score = ScoreMusic(*best, prefs)
	for i := 1; i < len(releases); i++ {
		s := ScoreMusic(releases[i], prefs)
		releases[i].Score = s
		if s > best.Score {
			best = &releases[i]
		}
	}
	return best
}

// StripAccents replaces accented characters with their ASCII equivalents
// using NFKD decomposition and stripping combining marks.
func StripAccents(s string) string {
	t := norm.NFKD.String(s)
	var out strings.Builder
	out.Grow(len(t))
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func normalizeMusic(s string) string {
	s = StripAccents(s)
	s = strings.ToLower(s)
	s = strings.NewReplacer(
		".", " ", "-", " ", "_", " ",
		"'", "", "`", "", "’", "", "‘", "",
		"&amp;", "", "&", "", "+", "",
	).Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func CleanArtist(raw string) string {
	s := strings.TrimSpace(raw)
	// Split on collaborator markers, take the first artist
	for _, sep := range []string{" feat. ", " ft. ", " featuring ", " feat ", " ft "} {
		if idx := strings.Index(strings.ToLower(s), sep); idx > 0 {
			s = s[:idx]
		}
	}
	if idx := strings.Index(s, ", "); idx > 0 {
		s = s[:idx]
	}
	// " & " — but watch for "&" at end of band name (e.g. "M&Ms")
	// Only split if " & " is followed by at least 2 chars
	if idx := strings.Index(s, " & "); idx > 0 && len(s)-idx-3 >= 2 {
		s = s[:idx]
	}
	if idx := strings.Index(s, " vs. "); idx > 0 {
		s = s[:idx]
	}
	// Strip parenthetical annotations
	if idx := strings.Index(s, " ("); idx > 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func CleanAlbum(raw string) string {
	s := strings.TrimSpace(raw)
	// Strip subtitle after ": "
	if idx := strings.Index(s, ": "); idx > 0 {
		s = s[:idx]
	}
	// Strip trailing " - EP", " - Single", " - Bonus" etc.
	cleanSuffixPat := regexp.MustCompile(`\s+-\s+(EP|Single|Bonus|Deluxe|Edition|Remaster|Remix)\s*$`)
	s = cleanSuffixPat.ReplaceAllString(s, "")
	// Strip trailing parenthetical qualifiers (anything in parens at the end)
	for {
		stripped := trailingParenPat.ReplaceAllString(s, "")
		stripped = strings.TrimSpace(stripped)
		if stripped == s {
			break
		}
		s = stripped
	}
	// Strip trailing ", Pt." / ", No." etc.
	numberedPat := regexp.MustCompile(`,\s*(Pt|No|Vol)\.?\s*\d+\s*$`)
	s = numberedPat.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var (
	trailingParenPat = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
)

// PartitionMusicReleases filters music releases and splits them into exact and
// fuzzy buckets based on artist/album word matching.
func PartitionMusicReleases(releases []ParsedRelease, artist, album string) (exact, fuzzy []ParsedRelease) {
	cleanArtist := CleanArtist(artist)
	cleanAlbum := CleanAlbum(album)
	cleanSearch := normalizeMusic(cleanArtist + " " + cleanAlbum)
	searchWords := strings.Fields(cleanSearch)
	artistWords := strings.Fields(normalizeMusic(cleanArtist))

	if len(searchWords) == 0 || len(artistWords) == 0 {
		return releases, nil
	}

	for _, r := range releases {
		norm := normalizeMusic(r.RawTitle)
		if !wordsInOrder(norm, searchWords) {
			continue
		}
		if titleAtStart(norm, artistWords) {
			exact = append(exact, r)
		} else {
			r.ExtraWords = true
			fuzzy = append(fuzzy, r)
		}
	}
	return
}

func SortMusicTop(releases []ParsedRelease, prefs MusicQualityPrefs, n int) []ParsedRelease {
	for i := range releases {
		releases[i].Score = ScoreMusic(releases[i], prefs)
	}
	for i := 0; i < n && i < len(releases); i++ {
		bestIdx := i
		for j := i + 1; j < len(releases); j++ {
			if releases[j].Score > releases[bestIdx].Score {
				bestIdx = j
			}
		}
		releases[i], releases[bestIdx] = releases[bestIdx], releases[i]
	}
	if n > len(releases) {
		n = len(releases)
	}
	return releases[:n]
}
