package discover

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractAuthorFromTags(t *testing.T) {
	tests := []struct {
		tags string
		want string
	}{
		{"Fiction,Hottest Books of the Season,Literary,John Smith", "John Smith"},
		{"Fiction,Mystery,Agatha Christie", "Agatha Christie"},
		{"Fiction", ""},
		{"", ""},
		{"Fiction,Literary,Too Many Words In This Name So It Won't Match", ""},
	}
	for _, tt := range tests {
		got := extractAuthorFromTags(tt.tags)
		assert.Equal(t, tt.want, got, "extractAuthorFromTags(%q)", tt.tags)
	}
}

func TestBmListPageRegexps(t *testing.T) {
	t.Run("slug", func(t *testing.T) {
		matches := bmSlugRe.FindStringSubmatch(`<div data-slug="test-slug">`)
		assert.Equal(t, []string{`data-slug="test-slug"`, "test-slug"}, matches)
	})
	t.Run("title", func(t *testing.T) {
		matches := bmTitleRe.FindStringSubmatch(`<div class="latest_book_title">The Great Book</div>`)
		assert.Equal(t, "The Great Book", matches[1])
	})
	t.Run("author", func(t *testing.T) {
		matches := bmAuthorRe.FindStringSubmatch(`<div class="latest_book_author">Jane Austen</div>`)
		assert.Equal(t, "Jane Austen", matches[1])
	})
	t.Run("genre", func(t *testing.T) {
		matches := bmGenreRe.FindStringSubmatch(`<div class="book_genre">Literary Fiction</div>`)
		assert.Equal(t, "Literary Fiction", matches[1])
	})
	t.Run("verdict", func(t *testing.T) {
		matches := bmVerdictRe.FindStringSubmatch(`<span class="featured_book_review_index rave">Rave</span>`)
		assert.Len(t, matches, 3)
		assert.Equal(t, "rave", matches[1])
		assert.Equal(t, "Rave", matches[2])
	})
	t.Run("image", func(t *testing.T) {
		matches := bmImgRe.FindStringSubmatch(`<img src="https://example.com/book.jpg" class="latest_book_image">`)
		assert.Equal(t, "https://example.com/book.jpg", matches[1])
	})
}

func TestBmDetailPageRegexps(t *testing.T) {
	t.Run("date", func(t *testing.T) {
		matches := bmDetailDateRe.FindStringSubmatch(`<meta itemprop="datePublished" content="2025-06-15">`)
		assert.Equal(t, "2025-06-15", matches[1])
	})
	t.Run("isbn", func(t *testing.T) {
		matches := bmDetailISBNRe.FindStringSubmatch(`bookshop.org/a/123/9781234567890`)
		assert.Equal(t, "9781234567890", matches[1])
	})
	t.Run("publisher", func(t *testing.T) {
		matches := bmDetailPubRe.FindStringSubmatch(`<span itemprop="publisher"><span itemprop="name">Penguin Books</span></span>`)
		assert.Equal(t, "Penguin Books", matches[1])
	})
	t.Run("description", func(t *testing.T) {
		matches := bmDetailDescRe.FindStringSubmatch(`<div class="book_manual_description">A compelling story.</div>`)
		assert.Equal(t, "A compelling story.", matches[1])
	})
	t.Run("tags", func(t *testing.T) {
		matches := bmDetailTagsRe.FindStringSubmatch(`<meta name="keywords" content="Fiction,Mystery,Stephen King">`)
		assert.Equal(t, "Fiction,Mystery,Stephen King", matches[1])
	})
	t.Run("verdict", func(t *testing.T) {
		matches := bmDetailVerdictRe.FindStringSubmatch(`overall rating of rave based on 5 reviews`)
		assert.Len(t, matches, 3)
		assert.Equal(t, "rave", matches[1])
		assert.Equal(t, "5", matches[2])
	})
}

func TestIsTruncatedTitle(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"The Jackal: The Rise and Fall of Carlos, the Wor\u2026", true},
		{"The Great Book", false},
		{"Short...", true},
		{"Trailing spaces …", true},
	}
	for _, tt := range tests {
		got := isTruncatedTitle(tt.in)
		assert.Equal(t, tt.want, got, "isTruncatedTitle(%q)", tt.in)
	}
}

func TestTitleFromSlug(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"the-jackal-the-rise-and-fall-of-carlos-the-worlds-first-super-terrorist",
			"The Jackal The Rise And Fall Of Carlos The Worlds First Super Terrorist"},
		{"the-great-book", "The Great Book"},
		{"", ""},
	}
	for _, tt := range tests {
		got := titleFromSlug(tt.slug)
		assert.Equal(t, tt.want, got, "titleFromSlug(%q)", tt.slug)
	}
}

func TestBmRecoversTruncatedTitleFromSlug(t *testing.T) {
	html := `<div data-slug="the-jackal-the-rise-and-fall-of-carlos-the-worlds-first-super-terrorist" class="latest_book">
		<div class="latest_book_title super_clarendon">The Jackal: The Rise and Fall of Carlos, the Wor&hellip;</div>
		<div class="latest_book_author">Joby Warrick</div>
	</div>`
	slugs := bmSlugRe.FindAllStringSubmatch(html, -1)
	titles := bmTitleRe.FindAllStringSubmatch(html, -1)
	authors := bmAuthorRe.FindAllStringSubmatch(html, -1)
	require.Len(t, slugs, 1)
	require.Len(t, titles, 1)

	slug := strings.TrimSpace(slugs[0][1])
	title := htmlUnescape(strings.TrimSpace(titles[0][1]))
	if isTruncatedTitle(title) {
		if recovered := titleFromSlug(slug); recovered != "" {
			title = recovered
		}
	}
	assert.False(t, isTruncatedTitle(title), "title should no longer be truncated")
	assert.Equal(t, "The Jackal The Rise And Fall Of Carlos The Worlds First Super Terrorist", title)
	_ = authors
}

func TestBmFullParseLogic(t *testing.T) {
	// Apply the same regex logic as fetchBookDetail to a full HTML snippet
	html := `<html><head>
		<meta itemprop="datePublished" content="2025-06-15">
		<meta name="keywords" content="Fiction,Mystery,Stephen King">
	</head><body>
		<a href="https://bookshop.org/a/123/9781234567890">Buy</a>
		<span itemprop="publisher"><span itemprop="name">Penguin Books</span></span>
		<div class="book_manual_description">A thrilling mystery novel.</div>
		<meta name="description" content="overall rating of positive based on 3 reviews">
	</body></html>`

	var (
		releaseDate  string
		isbn         string
		publisher    string
		desc         string
		tags         string
		verdict      string
		totalReviews int
	)

	if m := bmDetailDateRe.FindStringSubmatch(html); len(m) > 1 {
		releaseDate = m[1]
	}
	if m := bmDetailISBNRe.FindStringSubmatch(html); len(m) > 1 {
		isbn = m[1]
	}
	if m := bmDetailPubRe.FindStringSubmatch(html); len(m) > 1 {
		publisher = htmlUnescape(strings.TrimSpace(m[1]))
	}
	if m := bmDetailDescRe.FindStringSubmatch(html); len(m) > 1 {
		desc = htmlUnescape(strings.TrimSpace(m[1]))
	}
	if m := bmDetailTagsRe.FindStringSubmatch(html); len(m) > 1 {
		tags = m[1]
		if author := extractAuthorFromTags(tags); author != "" {
			tags = strings.ReplaceAll(tags, ","+author, "")
			tags = strings.ReplaceAll(tags, author+",", "")
			tags = strings.ReplaceAll(tags, author, "")
		}
		tags = strings.Trim(tags, ", ")
	}
	if m := bmDetailVerdictRe.FindStringSubmatch(html); len(m) > 2 {
		verdict = m[1]
		if n, err := fmt.Sscanf(m[2], "%d", &totalReviews); err != nil || n != 1 {
			totalReviews = 0
		}
	}

	assert.Equal(t, "2025-06-15", releaseDate)
	assert.Equal(t, "9781234567890", isbn)
	assert.Equal(t, "Penguin Books", publisher)
	assert.Equal(t, "A thrilling mystery novel.", desc)
	assert.Equal(t, "Fiction,Mystery", tags)
	assert.Equal(t, "positive", verdict)
	assert.Equal(t, 3, totalReviews)
}
