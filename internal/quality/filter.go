package quality

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

type ParsedRelease struct {
	RawTitle     string
	Resolution   int
	HDR          bool
	Source       string
	Codec        string
	ReleaseGroup string
	Seeders      int
	SizeBytes    int64
	Score        int
	ExtraWords   bool // true if release has non-metadata words after the title

	// Prowlarr-specific
	IndexerID   int
	IndexerName string
	Guid        string
	DownloadURL string
	MagnetURL   string
	InfoHash    string
}

type QualityPrefs struct {
	TargetResolution   int
	PreferHDR          bool
	SourcePriority     []string
	CodecPriority      []string
	PreferredGroups    []string
	MinSeeders         int
	PreferredIndexerID int
}

var (
	resPattern     = regexp.MustCompile(`(?i)(\d{3,4}p|4k|uhd)`)
	srcPattern     = regexp.MustCompile(`(?i)(BluRay|WEB[-.\s]?DL|WebRip|WEBRip|HDTV|REMUX|BDRip|BRRip)`)
	codecPattern   = regexp.MustCompile(`(?i)(x265|x264|h\.?265|h\.?264|hevc|av1)`)
	hdrPattern     = regexp.MustCompile(`(?i)(\bHDR(?:10)?\b|Dolby[.\s]?Vision|DV[.\s]?HDR|DoVi|HLG)`)
	groupPattern   = regexp.MustCompile(`-([a-zA-Z0-9]+(?:\[[^\]]+\])?)$`)
	groupPrefixPat = regexp.MustCompile(`^\[[^\]]+\]\s*`)
)

func Parse(rawTitle string) ParsedRelease {
	r := ParsedRelease{RawTitle: rawTitle}
	clean := stripGroupPrefix(strings.ReplaceAll(rawTitle, ".", " "))

	r.Resolution = parseResolution(clean)
	r.HDR = hdrPattern.MatchString(clean)
	r.Source = parseSource(clean)
	r.Codec = parseCodec(rawTitle)
	r.ReleaseGroup = parseGroup(rawTitle)

	return r
}

// stripGroupPrefix removes leading [group] prefixes from anime-style
// torrent titles, e.g. "[SubsPlease] Show Name" → "Show Name".
func stripGroupPrefix(s string) string {
	for groupPrefixPat.MatchString(s) {
		s = groupPrefixPat.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}

func parseResolution(s string) int {
	m := resPattern.FindString(s)
	if m == "" {
		return 0
	}
	m = strings.ToLower(m)
	switch m {
	case "2160p", "4k", "uhd":
		return 2160
	case "1080p":
		return 1080
	case "720p":
		return 720
	case "480p":
		return 480
	}
	return 0
}

func parseSource(s string) string {
	m := srcPattern.FindString(s)
	if m == "" {
		return ""
	}
	norm := strings.ToLower(strings.ReplaceAll(m, " ", "-"))
	norm = strings.ReplaceAll(norm, ".", "-")
	switch norm {
	case "bluray", "bdrip", "brrip":
		return "bluray"
	case "web-dl":
		return "web-dl"
	case "webrip":
		return "webrip"
	case "hdtv":
		return "hdtv"
	case "remux":
		return "remux"
	}
	return norm
}

func parseCodec(s string) string {
	m := codecPattern.FindString(s)
	if m == "" {
		return ""
	}
	m = strings.ToLower(m)
	m = strings.ReplaceAll(m, ".", "")
	switch m {
	case "h265", "hevc":
		return "h265"
	case "x265":
		return "x265"
	case "h264":
		return "h264"
	case "x264":
		return "x264"
	case "av1":
		return "av1"
	}
	return m
}

func parseGroup(s string) string {
	m := groupPattern.FindString(s)
	if m != "" {
		return strings.TrimPrefix(m, "-")
	}

	// Fallback: space-separated group at end of title
	clean := strings.ReplaceAll(s, ".", " ")
	words := strings.Fields(clean)
	if len(words) > 0 {
		last := words[len(words)-1]
		if len(last) >= 2 && last[0] >= 'A' && last[0] <= 'Z' && !isNonGroupWord(last) {
			return last
		}
	}
	return ""
}

// isNonGroupWord checks if a word is a known metadata keyword that appears at
// the end of a release title but is NOT a release group.
func isNonGroupWord(w string) bool {
	switch strings.ToLower(w) {
	case "hevc", "avc", "aac", "ac3", "dts", "truehd", "atmos",
		"hdr", "hdr10", "dovi", "hlg", "dv",
		"bluray", "web-dl", "webdl", "webrip", "hdtv", "remux", "bdrip", "brrip",
		"x264", "x265", "h264", "h265", "av1",
		"multi", "complete", "proper", "repack", "real", "internal",
		"uhd", "4k", "1080p", "720p", "480p", "2160p":
		return true
	}
	return false
}

func Score(r ParsedRelease, prefs QualityPrefs) int {
	score := 0
	score += resolutionScore(r.Resolution, prefs.TargetResolution)
	score += hdrBonus(r.HDR, prefs.PreferHDR, r.Resolution)
	score += sourceScore(r.Source, prefs.SourcePriority)
	score += codecScore(r.Codec, prefs.CodecPriority)
	score += groupBonus(r.ReleaseGroup, prefs.PreferredGroups)
	score += seederScore(r.Seeders, prefs.MinSeeders)
	score += preferredIndexerBonus(r.IndexerID, prefs.PreferredIndexerID)
	if r.ExtraWords {
		score -= 100000
	}
	return score
}

func preferredIndexerBonus(releaseIndexerID, preferredIndexerID int) int {
	if preferredIndexerID > 0 && releaseIndexerID == preferredIndexerID {
		return 250
	}
	return 0
}

func resolutionScore(res, target int) int {
	if res == 0 || target == 0 {
		return 0
	}
	if res == target {
		return 200
	}
	if res > target {
		return 200
	}
	ratio := float64(res) / float64(target)
	switch {
	case ratio >= 0.5:
		return 100
	case ratio >= 0.3:
		return 50
	default:
		return 0
	}
}

func hdrBonus(hasHDR, prefer bool, resolution int) int {
	if hasHDR && prefer && resolution >= 2160 {
		return 50
	}
	return 0
}

func sourceScore(source string, priority []string) int {
	if source == "" || len(priority) == 0 {
		return 0
	}
	for i, p := range priority {
		if strings.EqualFold(source, p) {
			return (len(priority) - i) * 100 / len(priority)
		}
	}
	return 0
}

func codecScore(codec string, priority []string) int {
	if codec == "" || len(priority) == 0 {
		return 0
	}
	norm := normalizeCodec(codec)
	for i, p := range priority {
		if norm == normalizeCodec(p) {
			return (len(priority) - i) * 80 / len(priority)
		}
	}
	return 0
}

func normalizeCodec(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ".", "")
	switch s {
	case "x264":
		return "h264"
	case "x265":
		return "h265"
	case "hevc":
		return "h265"
	}
	return s
}

func groupBonus(group string, preferred []string) int {
	if group == "" || len(preferred) == 0 {
		return 0
	}
	for _, p := range preferred {
		if strings.EqualFold(group, p) {
			return 50
		}
	}
	return 0
}

var (
	episodeMarkerPat = regexp.MustCompile(`(?i)\bs\d{2}[ .]?e\d{2}\b`)
	seasonEpisodePat = regexp.MustCompile(`(?i)\bseason[.\s]+(\d+)[.\s]+episode[.\s]+(\d+)\b`)
	episodeNumPat    = regexp.MustCompile(`(?i)-\s+(\d{1,3})(?:\s|$|\))`)
	releaseYearPat   = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
)

// ExtractShowName strips torrent metadata from a release title,
// returning the show/movie name portion.
func ExtractShowName(rawTitle string) string {
	s := stripGroupPrefix(strings.NewReplacer(".", " ", "(", " ", ")", " ").Replace(rawTitle))

	firstIdx := len(s)
	for _, pat := range []*regexp.Regexp{
		episodeMarkerPat, shortSeasonPat, seasonEpisodePat, episodeNumPat, seasonNumPat,
		releaseYearPat, resPattern, srcPattern, codecPattern, hdrPattern,
	} {
		loc := pat.FindStringIndex(s)
		if loc != nil && loc[0] < firstIdx {
			firstIdx = loc[0]
		}
	}

	name := strings.TrimSpace(s[:firstIdx])
	name = strings.TrimRight(name, " -")
	return name
}

// FilterRelease checks whether a parsed release should be kept
// based on title similarity, year (movies), and season/episode (TV).
func FilterRelease(r ParsedRelease, searchTitle string, searchYear, searchSeason int, mediaType string) bool {
	// TV/Anime: reject single episodes
	if (mediaType == "tv" || mediaType == "anime") &&
		(episodeMarkerPat.MatchString(r.RawTitle) || seasonEpisodePat.MatchString(r.RawTitle) ||
			episodeNumPat.MatchString(r.RawTitle)) {
		return false
	}

	// TV/Anime: reject wrong season
	if mediaType == "tv" || mediaType == "anime" {
		relSeason := parseReleaseSeason(r.RawTitle)
		if relSeason > 0 && relSeason != searchSeason {
			return false
		}
	}

	// Movies: reject wrong year (diff > 1)
	if mediaType == "movie" {
		relYear := parseReleaseYear(r.RawTitle)
		if relYear > 0 && absInt(relYear-searchYear) > 1 {
			return false
		}
	}

	// Title similarity: all normalized search words must appear in release title
	searchNorm := normalizeRelease(searchTitle)
	releaseNorm := normalizeRelease(r.RawTitle)
	searchWords := strings.Fields(searchNorm)
	if len(searchWords) == 0 {
		return true
	}
	for _, w := range searchWords {
		if !strings.Contains(releaseNorm, w) {
			return false
		}
	}

	// Word order: search words must appear in the same relative order
	if !wordsInOrder(releaseNorm, searchWords) {
		return false
	}

	// Title at start: release must begin with search words (after stripping [group] prefixes)
	if !titleAtStart(releaseNorm, searchWords) {
		return false
	}

	return true
}

// FilterReleases bulk-filters a slice of parsed releases.
func FilterReleases(releases []ParsedRelease, searchTitle string, searchYear, searchSeason int, mediaType string) []ParsedRelease {
	var filtered []ParsedRelease
	for _, r := range releases {
		if FilterRelease(r, searchTitle, searchYear, searchSeason, mediaType) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// IsExactTitleMatch checks whether a release title is an exact match for the
// search title — no extra non-metadata words between the title and the first
// quality marker (year, resolution, source, codec, season/episode, HDR).
func IsExactTitleMatch(releaseTitle, searchTitle string) bool {
	extracted := normalizeTitle(ExtractShowName(releaseTitle))
	search := normalizeTitle(searchTitle)
	return extracted == search
}

// PartitionReleases filters releases and splits them into exact and fuzzy
// buckets. Fuzzy releases have extra non-metadata words after the title and
// have their ExtraWords flag set to true.
func PartitionReleases(releases []ParsedRelease, searchTitle string, searchYear, searchSeason int, mediaType string) (exact, fuzzy []ParsedRelease) {
	for _, r := range releases {
		if !FilterRelease(r, searchTitle, searchYear, searchSeason, mediaType) {
			continue
		}
		if IsExactTitleMatch(r.RawTitle, searchTitle) {
			exact = append(exact, r)
		} else {
			r.ExtraWords = true
			fuzzy = append(fuzzy, r)
		}
	}
	return
}

func parseReleaseSeason(rawTitle string) int {
	if m := shortSeasonPat.FindStringSubmatch(rawTitle); len(m) > 1 {
		n, err := strconv.Atoi(m[1])
		if err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	if m := seasonNumPat.FindStringSubmatch(rawTitle); len(m) > 1 {
		n, err := strconv.Atoi(m[1])
		if err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	return 0
}

func parseReleaseYear(rawTitle string) int {
	matches := releaseYearPat.FindStringSubmatch(rawTitle)
	if len(matches) > 1 {
		n, err := strconv.Atoi(matches[1])
		if err == nil && n >= 1990 && n <= 2030 {
			return n
		}
	}
	return 0
}

func normalizeTitle(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}

func normalizeRelease(rawTitle string) string {
	s := strings.ToLower(rawTitle)
	s = strings.NewReplacer(
		".", " ", "-", " ", "_", " ",
		":", " ",
		"(", " ", ")", " ", "!", " ", "?", " ",
		"–", " ", "—", " ",
		"'", "", "`", "", "’", "", "‘", "",
	).Replace(s)
	// Collapse multiple spaces
	return strings.Join(strings.Fields(s), " ")
}

func wordsInOrder(releaseNorm string, searchWords []string) bool {
	releaseWords := strings.Fields(releaseNorm)
	j := 0
	for _, rw := range releaseWords {
		if j < len(searchWords) && rw == searchWords[j] {
			j++
		}
	}
	return j == len(searchWords)
}

func titleAtStart(releaseNorm string, searchWords []string) bool {
	releaseWords := strings.Fields(releaseNorm)
	// Strip leading [group] prefix (anime convention)
	for len(releaseWords) > 0 && strings.HasPrefix(releaseWords[0], "[") {
		releaseWords = releaseWords[1:]
	}
	if len(releaseWords) < len(searchWords) {
		return false
	}
	// Check direct start match first
	if hasPrefixWords(releaseWords, searchWords) {
		return true
	}
	// If there's a pipe separator, try from after it (dual-language titles)
	for i, w := range releaseWords {
		if w == "|" && len(releaseWords[i+1:]) >= len(searchWords) {
			return hasPrefixWords(releaseWords[i+1:], searchWords)
		}
	}
	return false
}

func hasPrefixWords(words, prefix []string) bool {
	if len(words) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if words[i] != w {
			return false
		}
	}
	return true
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func seederScore(seeders, minSeeders int) int {
	if minSeeders > 0 && seeders < minSeeders {
		return -200
	}
	if seeders <= 0 {
		return 0
	}
	s := math.Log2(float64(seeders + 1))
	s = s * 6
	if s > 40 {
		return 40
	}
	return int(s)
}

func Best(releases []ParsedRelease, prefs QualityPrefs) *ParsedRelease {
	if len(releases) == 0 {
		return nil
	}
	best := &releases[0]
	best.Score = Score(*best, prefs)
	for i := 1; i < len(releases); i++ {
		s := Score(releases[i], prefs)
		releases[i].Score = s
		if s > best.Score {
			best = &releases[i]
		}
	}
	return best
}

func SortAndTop(releases []ParsedRelease, prefs QualityPrefs, n int) []ParsedRelease {
	for i := range releases {
		releases[i].Score = Score(releases[i], prefs)
	}

	// Quick selection sort of top N
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
