package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBookRelease(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ParsedBookRelease
	}{
		{
			"epub ebook",
			"Author.Name-Book.Title.epub-GROUP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Author.Name-Book.Title.epub-GROUP", ReleaseGroup: "GROUP"},
				EbookFormat:   "epub",
				IsEbook:       true,
			},
		},
		{
			"mobi ebook",
			"Book Title mobi-GROUP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book Title mobi-GROUP", ReleaseGroup: "GROUP"},
				EbookFormat:   "mobi",
				IsEbook:       true,
			},
		},
		{
			"pdf ebook",
			"Book.Title.pdf-GRP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book.Title.pdf-GRP", ReleaseGroup: "GRP"},
				EbookFormat:   "pdf",
				IsEbook:       true,
			},
		},
		{
			"audiobook m4b",
			"Book.Title.m4b-GROUP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book.Title.m4b-GROUP", ReleaseGroup: "GROUP"},
				AudiobookFormat: "m4b",
				IsAudiobook:     true,
			},
		},
		{
			"audiobook mp3",
			"Book.Title.mp3-GRP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book.Title.mp3-GRP", ReleaseGroup: "GRP"},
				AudiobookFormat: "mp3",
				IsAudiobook:     true,
			},
		},
		{
			"audiobook flac with narrator",
			"Book.Title.FLAC.Read.by.Narrator-GRP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book.Title.FLAC.Read.by.Narrator-GRP", ReleaseGroup: "GRP"},
				AudiobookFormat: "flac",
				IsAudiobook:     true,
			},
		},
		{
			"audiobook from keyword",
			"Book Title Audiobook-GROUP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book Title Audiobook-GROUP", ReleaseGroup: "GROUP"},
				AudiobookFormat: "mp3",
				IsAudiobook:     true,
			},
		},
		{
			"bitrate detection",
			"Book.Title.VBR.mp3-GRP",
			ParsedBookRelease{
				ParsedRelease: ParsedRelease{RawTitle: "Book.Title.VBR.mp3-GRP", ReleaseGroup: "GRP", Codec: "vbr"},
				AudiobookFormat: "mp3",
				IsAudiobook:     true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBookRelease(tt.input)
			assert.Equal(t, tt.want.EbookFormat, got.EbookFormat, "EbookFormat")
			assert.Equal(t, tt.want.AudiobookFormat, got.AudiobookFormat, "AudiobookFormat")
			assert.Equal(t, tt.want.IsEbook, got.IsEbook, "IsEbook")
			assert.Equal(t, tt.want.IsAudiobook, got.IsAudiobook, "IsAudiobook")
			assert.Equal(t, tt.want.ReleaseGroup, got.ReleaseGroup, "ReleaseGroup")
			assert.Equal(t, tt.want.Codec, got.Codec, "Codec")
		})
	}
}

func TestBookFormatScore(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		priority []string
		want     int
	}{
		{"empty format", "", []string{"epub", "mobi"}, 0},
		{"empty priority", "epub", nil, 0},
		{"first priority", "epub", []string{"epub", "mobi", "pdf"}, 100},
		{"second priority", "mobi", []string{"epub", "mobi", "pdf"}, 66},
		{"last priority", "pdf", []string{"epub", "mobi", "pdf"}, 33},
		{"not in priority", "azw3", []string{"epub", "mobi"}, 0},
		{"case insensitive", "EPUB", []string{"epub"}, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bookFormatScore(tt.format, tt.priority)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScoreBook(t *testing.T) {
	prefs := BookQualityPrefs{
		EbookFormatPriority:     []string{"epub", "mobi", "pdf"},
		AudiobookFormatPriority: []string{"m4b", "mp3", "flac"},
		PreferredGroups:         []string{"GRP"},
		MinSeeders:              0,
	}

	tests := []struct {
		name string
		rel  ParsedBookRelease
		check func(t *testing.T, score int)
	}{
		{
			"empty release",
			ParsedBookRelease{},
			func(t *testing.T, s int) { assert.Equal(t, 0, s) },
		},
		{
			"preferred ebook format",
			ParsedBookRelease{IsEbook: true, EbookFormat: "epub"},
			func(t *testing.T, s int) { assert.Greater(t, s, 0) },
		},
		{
			"preferred audiobook format",
			ParsedBookRelease{IsAudiobook: true, AudiobookFormat: "m4b"},
			func(t *testing.T, s int) { assert.Greater(t, s, 0) },
		},
		{
			"group bonus",
			ParsedBookRelease{IsEbook: true, EbookFormat: "mobi", ParsedRelease: ParsedRelease{ReleaseGroup: "GRP"}},
			func(t *testing.T, s int) { assert.Greater(t, s, 50) },
		},
		{
			"seeder score",
			ParsedBookRelease{IsEbook: true, EbookFormat: "pdf", ParsedRelease: ParsedRelease{Seeders: 50}},
			func(t *testing.T, s int) { assert.Greater(t, s, 30) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, ScoreBook(tt.rel, prefs))
		})
	}
}

func TestCountPresentWords(t *testing.T) {
	tests := []struct {
		release []string
		search  []string
		want    int
	}{
		{[]string{"the", "quick", "brown", "fox"}, []string{"quick", "fox"}, 2},
		{[]string{"the", "quick", "brown", "fox"}, []string{"cat", "dog"}, 0},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 3},
		{[]string{}, []string{"a"}, 0},
		{[]string{"a", "b"}, []string{}, 0},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := countPresentWords(tt.release, tt.search)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllWordsPresent(t *testing.T) {
	tests := []struct {
		release []string
		search  []string
		want    bool
	}{
		{[]string{"the", "quick", "brown", "fox"}, []string{"quick", "fox"}, true},
		{[]string{"the", "quick", "brown", "fox"}, []string{"slow", "fox"}, false},
		{[]string{}, []string{}, true},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := allWordsPresent(tt.release, tt.search)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSortBookTop(t *testing.T) {
	prefs := BookQualityPrefs{
		EbookFormatPriority: []string{"epub", "mobi"},
	}

	releases := []ParsedBookRelease{
		{IsEbook: true, EbookFormat: "mobi"},
		{IsEbook: true, EbookFormat: "epub"},
		{IsEbook: true, EbookFormat: "pdf"},
	}

	result := SortBookTop(releases, prefs, 2)
	assert.Len(t, result, 2)

	resultAll := SortBookTop(releases, prefs, 10)
	assert.Len(t, resultAll, 3)
}
