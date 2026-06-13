package quality

import "testing"

func TestParseCodec(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		// x265 without dot
		{"Show.S01.1080p.WEB-DL.x265-GROUP", "x265"},
		// h.265 with dot
		{"Show.S01.1080p.WEB-DL.H.265-GROUP", "h265"},
		// h265 without dot
		{"Show.1080p.WEB-DL.h265-GROUP", "h265"},
		// hevc
		{"Show.2160p.WEB-DL.HEVC-GROUP", "h265"},
		// x264
		{"Movie.2025.1080p.BluRay.x264-GROUP", "x264"},
		// h.264 with dot
		{"Movie.2025.1080p.WEB-DL.H.264-GROUP", "h264"},
		// h264 without dot
		{"Movie.2025.1080p.WEB-DL.h264-GROUP", "h264"},
		// av1
		{"Show.1080p.WEB-DL.AV1-GROUP", "av1"},
		// no codec
		{"Show.1080p.WEB-DL-GROUP", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := Parse(tt.raw).Codec
			if got != tt.want {
				t.Errorf("Parse(%q).Codec = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExtractShowName(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"Eva.Lasting.S04.1080p.WEB-DL.x265-ION10", "Eva Lasting"},
		{"Call.the.Midwife.S15.1080p.WEB-DL.AAC2.0.H.264-CMRG", "Call the Midwife"},
		{"Show.Name.2025.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10Plus.HEVC-NAME", "Show Name"},
		{"Movie Title 2025 1080p BluRay x264-GROUP", "Movie Title"},
		{"Series Name - S02 Complete 1080p", "Series Name"},
		{"Dune.Part.Two.2024.2160p.WEB-DL.x265", "Dune Part Two"},
		{"No.Metadata.Here", "No Metadata Here"},
		{"Show.Name.S01E01.1080p.x264-GROUP", "Show Name"},
		{"Show.Season.01.Episode.02.1080p.WEB-DL.x265-GROUP", "Show"},
		// Anime-style [group] prefix stripped
		{"[SubsPlease] Show Name S01E01 1080p x264", "Show Name"},
		// Anime: standalone episode number
		{"[SubsPlease] Golden Kamuy Final Season - 08 [1080p CR WEB-DL AVC AAC]", "Golden Kamuy Final Season"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := ExtractShowName(tt.raw)
			if got != tt.want {
				t.Errorf("ExtractShowName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFilterRelease(t *testing.T) {
	tests := []struct {
		raw          string
		searchTitle  string
		searchYear   int
		searchSeason int
		mediaType    string
		want         bool
	}{
		// TV: correct season, season pack
		{"Eva.Lasting.S04.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", true},
		// TV: wrong season
		{"Eva.Lasting.S03.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", false},
		// TV: single episode
		{"Eva.Lasting.S04E03.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", false},
		// TV: season pack without season marker (complete series)
		{"Eva.Lasting.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", true},
		// TV: title words must match
		{"Other.Show.S04.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", false},
		// Movie: exact match
		{"Movie.Title.2025.1080p.BluRay.x264-GROUP", "Movie Title", 2025, 0, "movie", true},
		// Movie: year off by 1
		{"Movie.Title.2024.1080p.BluRay.x264-GROUP", "Movie Title", 2025, 0, "movie", true},
		// Movie: year off by 2
		{"Movie.Title.2023.1080p.BluRay.x264-GROUP", "Movie Title", 2025, 0, "movie", false},
		// Movie: wrong title
		{"Wrong.Movie.2025.1080p.BluRay.x264-GROUP", "Movie Title", 2025, 0, "movie", false},
		// Movie: Dune Part Two match
		{"Dune.Part.Two.2025.2160p.WEB-DL.x265-GROUP", "Dune Part Two", 2025, 0, "movie", true},
		// Movie: Dune (shorter search) matches Dune Part Two
		{"Dune.Part.Two.2025.2160p.WEB-DL.x265-GROUP", "Dune", 2025, 0, "movie", true},
		// No year in release — accept (can't verify)
		{"Movie Title WEBDL-1080p", "Movie Title", 2025, 0, "movie", true},
		// Word order: reversed words should not match
		{"One.Fine.Day.2024.1080p.WEB-DL-GROUP", "Day One", 2024, 0, "movie", false},
		// Word order: correct order should match
		{"Day.One.2024.1080p.WEB-DL-GROUP", "Day One", 2024, 0, "movie", true},
		// Word order: "One Day" in wrong order for "Day One"
		{"One.Day.2024.1080p.WEB-DL-GROUP", "Day One", 2024, 0, "movie", false},
		// Word order: "One Day" correct order
		{"One.Day.2024.1080p.WEB-DL-GROUP", "One Day", 2024, 0, "movie", true},
		// Word order: multi-word correct order
		{"Cult.Massacre.One.Day.in.Jonestown.2025.1080p.WEB-DL-GROUP", "Day One", 2025, 0, "movie", false},
		// Title at start: extra words before search title should reject
		{"A.Quiet.Place.Day.One.2024.1080p.WEB-DL-GROUP", "Day One", 2024, 0, "movie", false},
		// Title at start: correct position should accept
		{"Day.One.2024.1080p.WEB-DL-GROUP", "Day One", 2024, 0, "movie", true},
		// Title at start: unrelated title before search words
		{"Eva.Berger.Eva.Lasting.Love.2024.WEB-DL-GROUP", "Eva Lasting", 2024, 0, "movie", false},
		// Title at start: Spanish title not at start
		{"Invasion.en.la.oficina.2024.WEB-DL-GROUP", "La oficina", 2024, 0, "movie", false},
		// Title at start: correct Spanish title
		{"La.oficina.S01.1080p.WEB-DL-GROUP", "La oficina", 0, 1, "tv", true},
		// Title at start: anime-style [group] prefix
		{"[Group].Eva.Lasting.S04.1080p.WEB-DL.x265-GROUP", "Eva Lasting", 0, 4, "tv", true},
		// Title at start: single word, extra words before
		{"A.Quiet.Place.Dune.2024.1080p.WEB-DL-GROUP", "Dune", 2024, 0, "movie", false},
		// Title at start: single word at start
		{"Dune.2024.1080p.WEB-DL-GROUP", "Dune", 2024, 0, "movie", true},
		// Title at start: single word, multi-word release with matching start
		{"Dune.Part.Two.2025.2160p.WEB-DL.x265-GROUP", "Dune", 2025, 0, "movie", true},
		// Episode marker: S01.E03 format should be rejected
		{"Show.S01.E03.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "tv", false},
		// Episode marker: S01E03 format still rejected
		{"Show.S01E03.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "tv", false},
		// Episode marker: S01-only (no episode) is a season pack
		{"Show.S01.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "tv", true},
		// Anime: single episode S01E02 should be rejected
		{"Show.S01E02.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "anime", false},
		// Anime: season pack should be accepted
		{"Show.S01.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "anime", true},
		// Anime: wrong season should be rejected
		{"Show.S03.1080p.WEB-DL.x265-GROUP", "Show", 0, 4, "anime", false},
		// TV: "season N episode M" format rejected
		{"Show.Season.01.Episode.02.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "tv", false},
		// Anime: "season N episode M" format rejected
		{"Show.Season.02.Episode.05.1080p.WEB-DL.x265-GROUP", "Show", 0, 2, "anime", false},
		// Anime: "season N episode M" with season pack (no episode match)
		{"Show.Season.01.1080p.WEB-DL.x265-GROUP", "Show", 0, 1, "anime", true},
		// Dual-language title with pipe: search words after pipe
		{"[DB] Golden Kamuy: Saishuushou | Golden Kamuy Final Season [Dual Audio 10bit 1080p][HEVC-x265]", "Golden Kamuy Final Season", 0, 1, "anime", true},
		// Anime: standalone episode number " - 08 " format (single season)
		{"[Erai-raws] Golden Kamuy Final Season - 08 [1080p CR WEB-DL AVC AAC][MultiSub]", "Golden Kamuy Final Season", 0, 1, "anime", false},
		// Anime: standalone episode number " - 13 " with parenthetical resolution
		{"[SubsPlease] Fate Strange Fake - 13 (1080p) [E21C372E].mkv", "Fate Strange Fake", 0, 1, "anime", false},
		// Anime: season pack (no episode number) should still be accepted
		{"[SubsPlease] Fate Strange Fake - 1080p [E21C372E].mkv", "Fate Strange Fake", 0, 1, "anime", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			r := ParsedRelease{RawTitle: tt.raw}
			got := FilterRelease(r, tt.searchTitle, tt.searchYear, tt.searchSeason, tt.mediaType)
			if got != tt.want {
				t.Errorf("FilterRelease(%q, %q, %d, %d, %q) = %v, want %v",
					tt.raw, tt.searchTitle, tt.searchYear, tt.searchSeason, tt.mediaType, got, tt.want)
			}
		})
	}
}

func TestParseGroup(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		// Dash-separated group (existing behavior)
		{"Show.1080p.WEB-DL.x264-GROUP", "GROUP"},
		// Space-separated group after resolution/source/codec
		{"Show.1080p.WEB-DL.x265.5.1 BONE", "BONE"},
		// Space-separated group without audio channels
		{"Movie.2025.1080p.WEB-DL.x264 BONE", "BONE"},
		// Dash-separated group with brackets (anime)
		{"Show.1080p.WEB-DL.x264-GROUP[1]", "GROUP[1]"},
		// No group — ends with codec
		{"Show.1080p.WEB-DL.x264", ""},
		// No group — ends with known non-group word (HEVC)
		{"Show.1080p.WEB-DL.HEVC", ""},
		// No group — ends with known non-group word (HDR)
		{"Show.1080p.WEB-DL.HDR", ""},
		// No group — last word is lowercase (codec)
		{"Show.1080p.WEB-DL.x265", ""},
		// Space-separated group with dot-separated audio
		{"Show.1080p.WEB-DL.x265.5.1.AC3.FLUX", "FLUX"},
		// Group not detected for known audio keyword
		{"Show.1080p.WEB-DL.x265.5.1.AC3.DTS", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := Parse(tt.raw).ReleaseGroup
			if got != tt.want {
				t.Errorf("Parse(%q).ReleaseGroup = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseSource(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		// Standard WEB-DL
		{"Show.1080p.WEB-DL.x264", "web-dl"},
		// WEB.DL (dots → spaces in clean)
		{"Show.1080p.WEB.DL.x264", "web-dl"},
		// WEB DL with space in raw title
		{"Show.1080p WEB DL x264", "web-dl"},
		// BluRay
		{"Movie.1080p.BluRay.x264", "bluray"},
		// WebRip
		{"Show.1080p.WebRip.x264", "webrip"},
		// HDTV
		{"Show.1080p.HDTV.x264", "hdtv"},
		// No source
		{"Show.1080p.x264", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := Parse(tt.raw).Source
			if got != tt.want {
				t.Errorf("Parse(%q).Source = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseHDR(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		// HDRip should NOT be detected as HDR
		{"Movie.2025.1080p.HDRip.x264-GROUP", false},
		// HDRip with dots
		{"Movie.2025.1080p.HD.Rip.x264-GROUP", false},
		// Actual HDR
		{"Movie.2025.2160p.WEB-DL.DV.HDR10.HEVC-GROUP", true},
		// HDR10
		{"Show.2160p.WEB-DL.HDR10.x265", true},
		// Dolby Vision
		{"Show.2160p.WEB-DL.DolbyVision.HEVC-GROUP", true},
		// No HDR
		{"Show.1080p.WEB-DL.x264", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := Parse(tt.raw).HDR
			if got != tt.want {
				t.Errorf("Parse(%q).HDR = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
