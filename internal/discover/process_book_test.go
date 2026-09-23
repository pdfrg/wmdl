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

func TestNormalizeAuthorKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"R.F. Kuang", "r f kuang"},
		{"R. F. Kuang", "r f kuang"},
		{"r.f. kuang", "r f kuang"},
		{"J.R.R. Tolkien", "j r r tolkien"},
		{"J. R. R. Tolkien", "j r r tolkien"},
		{"Ursula K. Le Guin", "ursula k le guin"},
		{"O'Brien", "obrien"},
		{"O’Brien", "obrien"},
		{"Café Author", "cafe author"},
		{"", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, normalizeAuthorKey(tt.in), "normalizeAuthorKey(%q)", tt.in)
	}
}

func TestSameBookAuthor(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"initial spacing", "R.F. Kuang", "R. F. Kuang", true},
		{"initial spacing lower", "r.f. kuang", "r. f. kuang", true},
		{"multi initial", "J.R.R. Tolkien", "J. R. R. Tolkien", true},
		{"case insensitive", "Emily Wilson", "emily wilson", true},
		{"different authors", "Emily Wilson", "Jon Ronson", false},
		{"apostrophe", "O'Brien", "OBrien", true},
		{"empty", "", "", false},
		{"empty vs name", "", "Emily Wilson", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sameBookAuthor(tt.a, tt.b))
			assert.Equal(t, tt.want, sameBookAuthor(tt.b, tt.a), "symmetry")
		})
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

func TestGroupBookEventsByTitleInitialSpacing(t *testing.T) {
	// Regression: Taipei Story dedup failed because "R.F. Kuang" and
	// "R. F. Kuang" did not compare equal as authors.
	entries := []db.BookTitleEntry{
		{BookID: 305, EventID: 314, Author: "R.F. Kuang", Title: "Taipei Story"},
		{BookID: 349, EventID: 358, Author: "R. F. Kuang", Title: "Taipei Story"},
	}
	groups := groupBookEventsByTitle(entries)
	assert.Len(t, groups, 1)
	assert.Len(t, groups[0], 2)
}

func TestNormalizeAuthorNameContributors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"cramped initials expanded", "R.F. Kuang", "R. F. Kuang"},
		{"spaced initials unchanged", "R. F. Kuang", "R. F. Kuang"},
		{"multi initials", "J.R.R. Tolkien", "J. R. R. Tolkien"},
		{"ampersand folded", "Naomi Klein & Astra Taylor", "Naomi Klein, Astra Taylor"},
		{"and folded", "Cristina Rivera Garza and Christina MacSweeney", "Cristina Rivera Garza, Christina MacSweeney"},
		{"translator role stripped", "Cristina Rivera Garza (Translator)", "Cristina Rivera Garza"},
		{"narrator role stripped", "Elin Hilderbrand [Narrator]", "Elin Hilderbrand"},
		{"plain name unchanged", "Emily St. John Mandel", "Emily St. John Mandel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeAuthorName(tt.in))
		})
	}
}

func TestSamePrimaryAuthor(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"narrator suffix", "Elin Hilderbrand", "Elin Hilderbrand, Shelby Cunningham", true},
		{"translator suffix", "Cristina Rivera Garza", "Cristina Rivera Garza, Christina MacSweeney", true},
		{"ampersand co-author", "Naomi Klein", "Naomi Klein & Astra Taylor", true},
		{"different authors", "Emily Wilson", "Jon Ronson", false},
		{"wrong author rejected", "Suzy Eynon", "Cristina Rivera Garza, Christina MacSweeney", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, samePrimaryAuthor(tt.a, tt.b))
			assert.Equal(t, tt.want, samePrimaryAuthor(tt.b, tt.a), "symmetry")
		})
	}
}

func TestFindSiblingBookID(t *testing.T) {
	cands := []db.SiblingBookCandidate{
		{BookID: 305, AuthorName: "R.F. Kuang", Title: "Taipei Story", ReleaseYear: 2026, ISBN13: "9780063473751"},
		{BookID: 322, AuthorName: "Suzy Eynon", Title: "Terrestrial", ReleaseYear: 0, ISBN13: "9781968523077",
			EvtNotes: "url=https://bookshop.org/p/books/terrestrial-cristina-rivera-garza/80744946ce0299f6?ean=9780593980088"},
		{BookID: 341, AuthorName: "Elin Hilderbrand", Title: "The Thoroughbreds", ReleaseYear: 2026, ISBN13: "9781529445282"},
	}
	tests := []struct {
		name     string
		title    string
		year     int
		author   string
		isbn13   string
		notesEAN string
		want     int64
	}{
		{"initial-spacing author variant", "Taipei Story", 2026, "R. F. Kuang", "9780063473744", "", 305},
		{"wrong-author linked by EAN in notes", "Terrestrial", 0, "Cristina Rivera Garza, Christina MacSweeney", "9780593980088", "9780593980088", 322},
		{"narrator suffix author", "The Thoroughbreds", 0, "Elin Hilderbrand, Shelby Cunningham", "9780316567916", "9780316567916", 341},
		{"different title no match", "Dune", 2026, "R. F. Kuang", "", "", 0},
		{"year mismatch no match", "Taipei Story", 2025, "R. F. Kuang", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSiblingBookID(cands, tt.title, tt.year, tt.author, tt.isbn13, "", tt.notesEAN)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeBookItemsIdempotent(t *testing.T) {
	a := &ScrapedItem{Source: "bookshop", Notes: "url=https://bookshop.org/p/books/x?ean=9780000000001"}
	b := &ScrapedItem{Source: "bookshop", Notes: "url=https://bookshop.org/p/books/x?ean=9780000000001"}
	mergeBookItems(a, b)
	// Identical re-merge is a no-op.
	assert.Equal(t, "url=https://bookshop.org/p/books/x?ean=9780000000001", a.Notes)
	assert.Equal(t, "bookshop", a.Source)

	// Merging a genuinely new source appends exactly once.
	c := &ScrapedItem{Source: "bookmarks", Notes: "slug=x|isbn=9780000000001"}
	mergeBookItems(a, c)
	assert.Contains(t, a.Source, "bookmarks")
	merged := a.Notes
	mergeBookItems(a, c)
	assert.Equal(t, merged, a.Notes)
}
