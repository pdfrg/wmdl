package quality

import "testing"

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
