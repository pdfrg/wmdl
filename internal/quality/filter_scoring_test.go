package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolutionScore(t *testing.T) {
	tests := []struct {
		name   string
		res    int
		target int
		want   int
	}{
		{"zero res", 0, 1080, 0},
		{"zero target", 1080, 0, 0},
		{"exact match", 1080, 1080, 200},
		{"above target", 2160, 1080, 200},
		{"half target", 540, 1080, 100},
		{"below half", 360, 1080, 50},
		{"far below", 240, 1080, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolutionScore(tt.res, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHDRBonus(t *testing.T) {
	tests := []struct {
		name       string
		hasHDR     bool
		prefer     bool
		resolution int
		want       int
	}{
		{"no hdr", false, true, 2160, 0},
		{"not preferred", true, false, 2160, 0},
		{"below 4k", true, true, 1080, 0},
		{"4k + hdr + prefer", true, true, 2160, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hdrBonus(tt.hasHDR, tt.prefer, tt.resolution)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSourceScore(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		priority []string
		want     int
	}{
		{"empty source", "", []string{"bluray", "web-dl"}, 0},
		{"empty priority", "bluray", nil, 0},
		{"first priority", "bluray", []string{"bluray", "web-dl", "hdtv"}, 100},
		{"second priority", "web-dl", []string{"bluray", "web-dl", "hdtv"}, 66},
		{"last priority", "hdtv", []string{"bluray", "web-dl", "hdtv"}, 33},
		{"not in priority", "remux", []string{"bluray", "web-dl"}, 0},
		{"case insensitive", "BLURAY", []string{"bluray", "web-dl"}, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sourceScore(tt.source, tt.priority)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCodecScore(t *testing.T) {
	tests := []struct {
		name     string
		codec    string
		priority []string
		want     int
	}{
		{"empty codec", "", []string{"h265", "h264"}, 0},
		{"empty priority", "h265", nil, 0},
		{"first priority h265", "h265", []string{"h265", "h264"}, 80},
		{"x265 normalized to h265", "x265", []string{"h265", "h264"}, 80},
		{"hevc normalized to h265", "HEVC", []string{"h265", "h264"}, 80},
		{"second priority h264", "h264", []string{"h265", "h264"}, 40},
		{"x264 normalized to h264", "x264", []string{"h265", "h264"}, 40},
		{"not in priority", "av1", []string{"h265", "h264"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codecScore(tt.codec, tt.priority)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeCodec(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"x264", "h264"},
		{"x265", "h265"},
		{"hevc", "h265"},
		{"h265", "h265"},
		{"h264", "h264"},
		{"av1", "av1"},
		{"H.265", "h265"},
		{"H264", "h264"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeCodec(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGroupBonus(t *testing.T) {
	tests := []struct {
		name      string
		group     string
		preferred []string
		want      int
	}{
		{"empty group", "", []string{"GROUP"}, 0},
		{"empty preferred", "GROUP", nil, 0},
		{"match", "GROUP", []string{"GROUP"}, 50},
		{"case insensitive", "group", []string{"GROUP"}, 50},
		{"no match", "OTHER", []string{"GROUP"}, 0},
		{"multiple preferred", "B", []string{"A", "B", "C"}, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupBonus(tt.group, tt.preferred)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSeederScore(t *testing.T) {
	tests := []struct {
		name      string
		seeders   int
		minSeed   int
		wantRange [2]int // [min, max] inclusive
	}{
		{"below min seeders", 0, 10, [2]int{-200, -200}},
		{"zero seeders no min", 0, 0, [2]int{0, 0}},
		{"one seeder", 1, 0, [2]int{6, 6}},
		{"seven seeders", 7, 0, [2]int{18, 18}},
		{"lots of seeders capped", 1000, 0, [2]int{40, 40}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := seederScore(tt.seeders, tt.minSeed)
			assert.GreaterOrEqual(t, got, tt.wantRange[0])
			assert.LessOrEqual(t, got, tt.wantRange[1])
		})
	}
}

func TestPreferredIndexerBonus(t *testing.T) {
	deep := make([]int, 27)
	for i := range deep {
		deep[i] = i + 1
	}
	tests := []struct {
		name         string
		releaseID    int
		preferredIDs []int
		want         int
	}{
		{"no preferred", 1, nil, 0},
		{"empty list", 1, []int{}, 0},
		{"first rank", 1, []int{1, 2, 3}, 250},
		{"second rank", 2, []int{1, 2, 3}, 240},
		{"third rank", 3, []int{1, 2, 3}, 230},
		{"deep rank floors at zero", 26, deep, 0},
		{"not in list", 9, []int{1, 2, 3}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preferredIndexerBonus(tt.releaseID, tt.preferredIDs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScore(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution:    1080,
		PreferHDR:           true,
		SourcePriority:      []string{"bluray", "web-dl", "hdtv"},
		CodecPriority:       []string{"h265", "h264"},
		PreferredGroups:     []string{"GRP"},
		MinSeeders:          0,
		PreferredIndexerIDs: nil,
	}

	tests := []struct {
		name  string
		rel   ParsedRelease
		check func(t *testing.T, score int)
	}{
		{
			"empty release",
			ParsedRelease{},
			func(t *testing.T, s int) { assert.Equal(t, 0, s) },
		},
		{
			"perfect 1080p bluray h264",
			ParsedRelease{Resolution: 1080, Source: "bluray", Codec: "h264", Seeders: 100},
			func(t *testing.T, s int) {
				assert.Greater(t, s, 200)
			},
		},
		{
			"4k hdr preferred",
			ParsedRelease{Resolution: 2160, HDR: true, Source: "web-dl", Codec: "h265", Seeders: 50},
			func(t *testing.T, s int) {
				// 2160 gets 200 (>= 1080 target), hdr gets 50, web-dl gets 66, h265 gets 80
				assert.Greater(t, s, 350)
			},
		},
		{
			"extra words penalty",
			ParsedRelease{Resolution: 1080, Source: "web-dl", Codec: "h264", Seeders: 10, ExtraWords: true},
			func(t *testing.T, s int) {
				assert.Negative(t, s)
			},
		},
		{
			"preferred group bonus",
			ParsedRelease{Resolution: 720, Source: "hdtv", Codec: "h264", Seeders: 5, ReleaseGroup: "GRP"},
			func(t *testing.T, s int) {
				assert.Greater(t, s, 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, Score(tt.rel, prefs))
		})
	}
}

func TestBest(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 1080,
		SourcePriority:   []string{"bluray", "web-dl"},
	}

	tests := []struct {
		name      string
		releases  []ParsedRelease
		wantNil   bool
		wantTitle string
	}{
		{"empty slice", nil, true, ""},
		{"single release", []ParsedRelease{{RawTitle: "only", Resolution: 1080}}, false, "only"},
		{"picks highest score",
			[]ParsedRelease{
				{RawTitle: "low", Resolution: 480},
				{RawTitle: "high", Resolution: 1080},
				{RawTitle: "mid", Resolution: 720},
			},
			false, "high"},
		{"prefers better source",
			[]ParsedRelease{
				{RawTitle: "hdtv", Resolution: 1080, Source: "hdtv"},
				{RawTitle: "bluray", Resolution: 1080, Source: "bluray"},
			},
			false, "bluray"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Best(tt.releases, prefs)
			if tt.wantNil {
				assert.Nil(t, result)
				return
			}
			assert.NotNil(t, result)
			assert.Equal(t, tt.wantTitle, result.RawTitle)
		})
	}
}

func TestSortAndTop(t *testing.T) {
	prefs := QualityPrefs{
		TargetResolution: 1080,
		SourcePriority:   []string{"bluray"},
	}

	releases := []ParsedRelease{
		{RawTitle: "C", Resolution: 480},
		{RawTitle: "A", Resolution: 1080},
		{RawTitle: "B", Resolution: 720},
	}

	t.Run("top 2", func(t *testing.T) {
		result := SortAndTop(releases, prefs, 2)
		assert.Len(t, result, 2)
		assert.Equal(t, "A", result[0].RawTitle)
	})

	t.Run("top more than length", func(t *testing.T) {
		result := SortAndTop(releases, prefs, 10)
		assert.Len(t, result, 3)
	})

	t.Run("top 0", func(t *testing.T) {
		result := SortAndTop(releases, prefs, 0)
		assert.Empty(t, result)
	})
}

func TestIsExactTitleMatch(t *testing.T) {
	tests := []struct {
		release string
		search  string
		want    bool
	}{
		{"Movie.Title.2025.1080p.WEB-DL.x265-GROUP", "Movie Title", true},
		{"Movie.Title.2025.1080p.WEB-DL.x265-GROUP", "Movie Name", false},
		{"Show.Name.S01.1080p.WEB-DL.x265-GROUP", "Show Name", true},
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP", "The Matrix", true},
		{"Dune.Part.Two.2024.2160p.WEB-DL", "Dune Part Two", true},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			got := IsExactTitleMatch(tt.release, tt.search)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterReleases(t *testing.T) {
	releases := []ParsedRelease{
		{RawTitle: "Movie.One.2025.1080p.WEB-DL.x264-GROUP"},
		{RawTitle: "Movie.Two.2025.1080p.WEB-DL.x264-GROUP"},
		{RawTitle: "Other.Thing.2025.1080p.WEB-DL.x264-GROUP"},
	}

	result := FilterReleases(releases, "Movie One", 2025, 0, "movie")
	assert.Len(t, result, 1)
	assert.Equal(t, "Movie.One.2025.1080p.WEB-DL.x264-GROUP", result[0].RawTitle)
}

func TestPartitionReleases(t *testing.T) {
	releases := []ParsedRelease{
		{RawTitle: "Dune.Part.Two.2024.2160p.WEB-DL.x265-GROUP"},
		// Fuzzy: has extra words after the title that aren't metadata
		{RawTitle: "Dune.Part.Two.Extended.Edition.2024.2160p.WEB-DL.x265-GROUP"},
	}

	exact, fuzzy := PartitionReleases(releases, "Dune Part Two", 2024, 0, "movie")
	assert.Len(t, exact, 1)
	assert.Len(t, fuzzy, 1)
	if len(fuzzy) > 0 {
		assert.True(t, fuzzy[0].ExtraWords)
	}
}
