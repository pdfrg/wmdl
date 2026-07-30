package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
)

func TestBookSearchQueries(t *testing.T) {
	evt := db.EventWithBook{
		Book: &model.Book{
			Title:  "The Great Book: A Novel",
			ISBN13: "9781234567890",
			ASIN:   "B00TEST",
			ISBN10: "1234567890",
		},
		Author: &model.Author{Name: "John Smith"},
	}

	queries := bookSearchQueries(evt)
	assert.NotEmpty(t, queries)
	// ISBNs first
	assert.Equal(t, "9781234567890", queries[0])
	assert.Equal(t, "B00TEST", queries[1])
	assert.Equal(t, "1234567890", queries[2])
	// Author + main title (without subtitle)
	assert.Contains(t, queries[3], "John Smith")
	assert.Contains(t, queries[3], "The Great Book")
	// Main title only
	assert.Equal(t, "The Great Book", queries[4])
}

func TestBookSearchQueriesNoSubtitle(t *testing.T) {
	evt := db.EventWithBook{
		Book:   &model.Book{Title: "Simple Title"},
		Author: &model.Author{Name: "Author"},
	}
	queries := bookSearchQueries(evt)
	assert.Len(t, queries, 2) // author+title, title only (no ISBNs)
	assert.Contains(t, queries[1], "Simple Title")
}

func TestBookSearchQueriesNoAuthor(t *testing.T) {
	evt := db.EventWithBook{
		Book:   &model.Book{Title: "Book", ISBN13: "9780000000001"},
		Author: &model.Author{Name: ""}, // empty name, no author+title query generated
	}
	queries := bookSearchQueries(evt)
	assert.Len(t, queries, 2) // ISBN + title
	assert.Equal(t, "9780000000001", queries[0])
	assert.Equal(t, "Book", queries[1])
}

func TestFilterEbookResults(t *testing.T) {
	ebook := quality.ParsedBookRelease{
		ParsedRelease: quality.ParsedRelease{RawTitle: "Book EPUB"},
		IsEbook:       true,
		EbookFormat:   "EPUB",
	}
	audiobook := quality.ParsedBookRelease{
		ParsedRelease:   quality.ParsedRelease{RawTitle: "Book MP3"},
		IsAudiobook:     true,
		AudiobookFormat: "MP3",
	}
	unknown := quality.ParsedBookRelease{
		ParsedRelease: quality.ParsedRelease{RawTitle: "Book Unknown"},
	}

	t.Run("passes ebooks through", func(t *testing.T) {
		result := filterEbookResults([]quality.ParsedBookRelease{ebook, audiobook, unknown})
		assert.Len(t, result, 2)
		assert.Equal(t, "Book EPUB", result[0].RawTitle)
		// Unknown marked as ebook
		assert.True(t, result[1].IsEbook)
		assert.Equal(t, "unknown", result[1].EbookFormat)
	})

	t.Run("empty input", func(t *testing.T) {
		assert.Empty(t, filterEbookResults(nil))
	})
}

func TestFilterAudiobookResults(t *testing.T) {
	ebook := quality.ParsedBookRelease{
		ParsedRelease: quality.ParsedRelease{RawTitle: "Book EPUB"},
		IsEbook:       true,
		EbookFormat:   "EPUB",
	}
	audiobook := quality.ParsedBookRelease{
		ParsedRelease:   quality.ParsedRelease{RawTitle: "Book MP3"},
		IsAudiobook:     true,
		AudiobookFormat: "MP3",
	}
	unknown := quality.ParsedBookRelease{
		ParsedRelease: quality.ParsedRelease{RawTitle: "Book Unknown"},
	}

	t.Run("passes audiobooks through", func(t *testing.T) {
		result := filterAudiobookResults([]quality.ParsedBookRelease{audiobook, ebook, unknown})
		assert.Len(t, result, 2)
		assert.Equal(t, "Book MP3", result[0].RawTitle)
		assert.True(t, result[1].IsAudiobook)
		assert.Equal(t, "unknown", result[1].AudiobookFormat)
	})

	t.Run("empty input", func(t *testing.T) {
		assert.Empty(t, filterAudiobookResults(nil))
	})
}
