package discover

import (
	"github.com/pdfrg/wmdl/internal/db"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanBookDescription(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold and italic tags stripped",
			in:   "<b>1940s Hong Kong</b>\nWhen Japanese soldiers invade, she escapes.\n<i>Italic aside</i>",
			want: "1940s Hong Kong\nWhen Japanese soldiers invade, she escapes.\nItalic aside",
		},
		{
			name: "paragraphs become blank-line separated",
			in:   "<p>First paragraph.</p><p>Second paragraph.</p><p>Third.</p>",
			want: "First paragraph.\n\nSecond paragraph.\n\nThird.",
		},
		{
			name: "paragraphs with newlines preserved",
			in:   "<p>First paragraph.</p>\n<p>Second paragraph.</p>",
			want: "First paragraph.\n\nSecond paragraph.",
		},
		{
			name: "br tags become newlines",
			in:   "Line one<br>Line two<br/>Line three<br />",
			want: "Line one\nLine two\nLine three",
		},
		{
			name: "html entities decoded",
			in:   "Tom &amp; Jerry &amp; the &nbsp;case",
			want: "Tom & Jerry & the case",
		},
		{
			name: "curly quote entities decoded",
			in:   "&#8220;Quoted&#8221; text",
			want: "\u201cQuoted\u201d text",
		},
		{
			name: "realistic multi-paragraph blurb",
			in:   "<p><b>1960s San Francisco</b>\nMarigold has a knack for secrets.</p>\n\n<p>Her mother vanishes before her eyes.</p>",
			want: "1960s San Francisco\nMarigold has a knack for secrets.\n\nHer mother vanishes before her eyes.",
		},
		{
			name: "plain text unchanged",
			in:   "A simple description with no tags.",
			want: "A simple description with no tags.",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "whitespace trimmed",
			in:   "  \n\t Leading and trailing whitespace.  \n",
			want: "Leading and trailing whitespace.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanBookDescription(tt.in))
		})
	}
}

func TestNormalizeAuthorName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean name unchanged",
			in:   "Steve Hawk",
			want: "Steve Hawk",
		},
		{
			name: "runs of spaces collapsed",
			in:   "Steve                                         Hawk",
			want: "Steve Hawk",
		},
		{
			name: "double space collapsed",
			in:   "Emily  Jane",
			want: "Emily Jane",
		},
		{
			name: "tabs and newlines collapsed",
			in:   "Jon\t\tRonson\nSmith",
			want: "Jon Ronson Smith",
		},
		{
			name: "leading and trailing whitespace trimmed",
			in:   "  Steve Hawk  \n\t",
			want: "Steve Hawk",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "only whitespace",
			in:   "   \t\n  ",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeAuthorName(tt.in))
		})
	}
}

func TestNormalizeBookKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Crossing the Wine-Dark Sea", "crossing the wine dark sea"},
		{"Crossing The Wine Dark Sea Journeys Through Ancient Literature",
			"crossing the wine dark sea journeys through ancient literature"},
		{"Title: A Subtitle", "title"},
		{"Title; Another Subtitle", "title"},
		{"Some Book (2026)", "some book"},
		{"Café Society", "cafe society"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, normalizeBookKey(tt.in), "normalizeBookKey(%q)", tt.in)
	}
}

func TestSameBookTitle(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"exact", "Dune", "Dune", true},
		{"case and hyphen", "Crossing the Wine-Dark Sea", "Crossing The Wine Dark Sea", true},
		{"colon subtitle stripped", "Crossing the Wine-Dark Sea: Journeys", "Crossing the Wine-Dark Sea", true},
		{
			"concatenated subtitle without colon",
			"Crossing The Wine Dark Sea Journeys Through Ancient Literature",
			"Crossing the Wine-Dark Sea (2026)",
			true,
		},
		{"different books", "Dune", "Dune Messiah", false},
		{"short prefix rejected", "It", "It Comes", false},
		{"unrelated", "The Castle", "Crossing the Wine-Dark Sea", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sameBookTitle(tt.a, tt.b))
			assert.Equal(t, tt.want, sameBookTitle(tt.b, tt.a), "symmetry")
		})
	}
}

func TestDedupeBookItems(t *testing.T) {
	items := []ScrapedItem{
		{Title: "Crossing the Wine-Dark Sea (2026)", ArtistName: "Emily Wilson", Source: "bookshop", Notes: "url=x"},
		{Title: "Crossing The Wine Dark Sea Journeys Through Ancient Literature", ArtistName: "Emily Wilson", Source: "bookmarks", Notes: "slug=y"},
		{Title: "The Castle Adventures In A World Of Unraveling Men", ArtistName: "Jon Ronson", Source: "bookmarks"},
	}
	got := dedupeBookItems(items)
	assert.Len(t, got, 2)
	for _, item := range got {
		if item.ArtistName == "Emily Wilson" {
			assert.Contains(t, item.Source, "bookshop")
			assert.Contains(t, item.Source, "bookmarks")
		}
	}
}

func TestGroupBookEventsByTitle(t *testing.T) {
	entries := []db.BookTitleEntry{
		{BookID: 1, EventID: 11, Author: "Emily Wilson", Title: "Crossing the Wine-Dark Sea"},
		{BookID: 2, EventID: 22, Author: "Emily Wilson", Title: "Crossing The Wine Dark Sea Journeys Through Ancient Literature"},
		{BookID: 3, EventID: 33, Author: "Jon Ronson", Title: "The Castle Adventures In A World Of Unraveling Men"},
	}
	groups := groupBookEventsByTitle(entries)
	assert.Len(t, groups, 1)
	assert.Len(t, groups[0], 2)
}
