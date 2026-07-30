package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMusicRelease(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ParsedRelease
	}{
		{
			"flac format",
			"Artist.Album.FLAC-GROUP",
			ParsedRelease{RawTitle: "Artist.Album.FLAC-GROUP", Source: "flac", ReleaseGroup: "GROUP"},
		},
		{
			"mp3 320",
			"Artist.Album.MP3.320-GRP",
			ParsedRelease{RawTitle: "Artist.Album.MP3.320-GRP", Source: "mp3", Codec: "320", ReleaseGroup: "GRP"},
		},
		{
			"mp3 v0",
			"Artist.Album.MP3.V0-GRP",
			ParsedRelease{RawTitle: "Artist.Album.MP3.V0-GRP", Source: "mp3", Codec: "v0", ReleaseGroup: "GRP"},
		},
		{
			"aac format",
			"Artist-Album-AAC-GROUP",
			ParsedRelease{RawTitle: "Artist-Album-AAC-GROUP", Source: "aac", ReleaseGroup: "GROUP"},
		},
		{
			"lossless from keyword",
			"Artist.Album.Lossless-GRP",
			ParsedRelease{RawTitle: "Artist.Album.Lossless-GRP", Source: "lossless", ReleaseGroup: "GRP"},
		},
		{
			"24bit hi-res",
			"Artist.Album.24bit.96kHz-GRP",
			ParsedRelease{RawTitle: "Artist.Album.24bit.96kHz-GRP", Source: "lossless", ReleaseGroup: "GRP"},
		},
		{
			"opus format",
			"Artist.Album.OPUS-GROUP",
			ParsedRelease{RawTitle: "Artist.Album.OPUS-GROUP", Source: "opus", ReleaseGroup: "GROUP"},
		},
		{
			"no format detected",
			"Artist-Album-GROUP",
			ParsedRelease{RawTitle: "Artist-Album-GROUP", ReleaseGroup: "GROUP"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMusicRelease(tt.input)
			assert.Equal(t, tt.want.Source, got.Source, "Source")
			assert.Equal(t, tt.want.Codec, got.Codec, "Codec")
			assert.Equal(t, tt.want.ReleaseGroup, got.ReleaseGroup, "ReleaseGroup")
		})
	}
}

func TestMusicFormatScore(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		priority []string
		want     int
	}{
		{"empty format", "", []string{"flac", "mp3"}, 0},
		{"empty priority", "flac", nil, 0},
		{"first priority", "flac", []string{"flac", "mp3", "aac"}, 100},
		{"second priority", "mp3", []string{"flac", "mp3", "aac"}, 66},
		{"not in priority", "opus", []string{"flac", "mp3"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := musicFormatScore(tt.format, tt.priority)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMusicBitrateScore(t *testing.T) {
	tests := []struct {
		name     string
		bitrate  string
		priority []string
		want     int
	}{
		{"empty bitrate", "", []string{"v0", "320", "v2"}, 0},
		{"empty priority", "v0", nil, 0},
		{"first priority", "v0", []string{"v0", "320", "v2"}, 80},
		{"second priority", "320", []string{"v0", "320", "v2"}, 53},
		{"not in priority", "v1", []string{"v0", "320"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := musicBitrateScore(tt.bitrate, tt.priority)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScoreMusic(t *testing.T) {
	prefs := MusicQualityPrefs{
		FormatPriority:  []string{"flac", "mp3"},
		BitratePriority: []string{"v0", "320"},
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
			"preferred format + bitrate",
			ParsedRelease{Source: "flac", Codec: "v0", Seeders: 100},
			func(t *testing.T, s int) { assert.Greater(t, s, 150) },
		},
		{
			"format only",
			ParsedRelease{Source: "mp3"},
			func(t *testing.T, s int) { assert.Greater(t, s, 0) },
		},
		{
			"bitrate only",
			ParsedRelease{Source: "", Codec: "320"},
			func(t *testing.T, s int) { assert.Greater(t, s, 0) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, ScoreMusic(tt.rel, prefs))
		})
	}
}

func TestBestMusic(t *testing.T) {
	prefs := MusicQualityPrefs{
		FormatPriority: []string{"flac", "mp3"},
	}

	result := BestMusic(nil, prefs)
	assert.Nil(t, result)

	releases := []ParsedRelease{
		{RawTitle: "low", Source: "aac"},
		{RawTitle: "high", Source: "flac"},
	}
	result = BestMusic(releases, prefs)
	assert.NotNil(t, result)
	assert.Equal(t, "high", result.RawTitle)
}

func TestSortMusicTop(t *testing.T) {
	prefs := MusicQualityPrefs{
		FormatPriority: []string{"flac", "mp3"},
	}
	releases := []ParsedRelease{
		{RawTitle: "C", Source: "aac"},
		{RawTitle: "A", Source: "flac"},
		{RawTitle: "B", Source: "mp3"},
	}
	result := SortMusicTop(releases, prefs, 2)
	assert.Len(t, result, 2)

	resultAll := SortMusicTop(releases, prefs, 10)
	assert.Len(t, resultAll, 3)
}

func TestStripAccents(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Café", "Cafe"},
		{"José", "Jose"},
		{"Mötley Crüe", "Motley Crue"},
		{"naïve", "naive"},
		{"François", "Francois"},
		{"Günter", "Gunter"},
		{"normal", "normal"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripAccents(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeMusic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Mötley.Crüe-Dr.Feelgood.FLAC-GROUP", "motley crue dr feelgood flac group"},
		{"Beyoncé-Lemonade", "beyonce lemonade"},
		{"Radiohead-In.Rainbows", "radiohead in rainbows"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeMusic(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCleanArtist(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Artist Name", "Artist Name"},
		{"Artist feat. Guest", "Artist"},
		{"Artist ft. Guest", "Artist"},
		{"Artist featuring Guest", "Artist"},
		{"Artist, Guest", "Artist"},
		{"Artist & Guest", "Artist"},
		{"Artist vs. Guest", "Artist"},
		{"Artist (with Guest)", "Artist"},
		{"Artist Name (UK)", "Artist Name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanArtist(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCleanAlbum(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Album Name", "Album Name"},
		{"Album Name: Subtitle", "Album Name"},
		{"Album Name - EP", "Album Name"},
		{"Album Name - Deluxe Edition", "Album Name - Deluxe Edition"},
		{"Album Name (Bonus Track Version)", "Album Name"},
		{"Album Name, Pt. 1", "Album Name"},
		{"Album Name, Vol. 2", "Album Name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanAlbum(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPartitionMusicReleases(t *testing.T) {
	releases := []ParsedRelease{
		{RawTitle: "Radiohead.OK Computer.FLAC-GROUP"},
		// Fuzzy: release has extra word before artist name
		{RawTitle: "The Radiohead OK-Computer-1997-FLAC-GROUP"},
	}

	exact, fuzzy := PartitionMusicReleases(releases, "Radiohead", "OK Computer")
	assert.Len(t, exact, 1)
	assert.Len(t, fuzzy, 1)

	exact2, fuzzy2 := PartitionMusicReleases(nil, "Artist", "Album")
	assert.Empty(t, exact2)
	assert.Empty(t, fuzzy2)
}
