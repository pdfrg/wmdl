package quality

import (
	"regexp"
	"strings"
)

type MusicQualityPrefs struct {
	FormatPriority  []string
	BitratePriority []string
	MinSeeders      int
	PreferredGroups []string
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


