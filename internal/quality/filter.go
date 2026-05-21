package quality

import (
	"math"
	"regexp"
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

	// Prowlarr-specific
	IndexerID  int
	Guid       string
	DownloadURL string
	MagnetURL   string
	InfoHash    string
}

type QualityPrefs struct {
	TargetResolution int
	PreferHDR        bool
	SourcePriority   []string
	CodecPriority    []string
	PreferredGroups  []string
	MinSeeders       int
}

var (
	resPattern = regexp.MustCompile(`(?i)(\d{3,4}p|4k|uhd)`)
	srcPattern = regexp.MustCompile(`(?i)(BluRay|WEB-DL|WebRip|WEBRip|HDTV|REMUX|BDRip|BRRip)`)
	codecPattern = regexp.MustCompile(`(?i)(x265|x264|h\.?265|h\.?264|hevc|av1)`)
	hdrPattern = regexp.MustCompile(`(?i)(HDR(?:10)?|Dolby[.\s]?Vision|DV[.\s]?HDR|DoVi|HLG)`)
	groupPattern = regexp.MustCompile(`-([a-zA-Z0-9]+(?:\[[^\]]+\])?)$`)
)

func Parse(rawTitle string) ParsedRelease {
	r := ParsedRelease{RawTitle: rawTitle}
	clean := strings.ReplaceAll(rawTitle, ".", " ")

	r.Resolution = parseResolution(clean)
	r.HDR = hdrPattern.MatchString(clean)
	r.Source = parseSource(clean)
	r.Codec = parseCodec(clean)
	r.ReleaseGroup = parseGroup(rawTitle)

	return r
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
	switch strings.ToLower(m) {
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
	return m
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
	if m == "" {
		return ""
	}
	return strings.TrimPrefix(m, "-")
}

func Score(r ParsedRelease, prefs QualityPrefs) int {
	score := 0
	score += resolutionScore(r.Resolution, prefs.TargetResolution)
	score += hdrBonus(r.HDR, prefs.PreferHDR, r.Resolution)
	score += sourceScore(r.Source, prefs.SourcePriority)
	score += codecScore(r.Codec, prefs.CodecPriority)
	score += groupBonus(r.ReleaseGroup, prefs.PreferredGroups)
	score += seederScore(r.Seeders, prefs.MinSeeders)
	return score
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
