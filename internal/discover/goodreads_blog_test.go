package discover

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsRelevantPost(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"New Books Recommended by Readers This Week", true},
		{"Editors Share Their Picks: January 2025", true},
		{"Some Other Post", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isRelevantPost(tt.title)
		assert.Equal(t, tt.want, got, "isRelevantPost(%q)", tt.title)
	}
}

func TestParseNewsDate(t *testing.T) {
	now := parseOrPanic("2025-06-01")

	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"2h", "2025-05-31", true},
		{"3d", "2025-05-29", true},
		{"Jun 1", "2025-06-01", true},
		{"May 27", "2025-05-27", true},
		{"", "", false},
		{"invalid", "", false},
	}
	for _, tt := range tests {
		got, ok := parseNewsDate(tt.input, now)
		if !tt.ok {
			assert.False(t, ok)
			continue
		}
		assert.True(t, ok)
		assert.Equal(t, tt.want, got.Format("2006-01-02"))
	}
}

func TestParseMonthFromTitle(t *testing.T) {
	tests := []struct {
		title string
		want  int
	}{
		{"Editors Share Their Picks: January 2025", 1},
		{"Editors Share Their Favorite Books: February", 2},
		{"Editors Share Their December Picks", 12},
		{"Random Post", 0},
	}
	for _, tt := range tests {
		got := parseMonthFromTitle(tt.title)
		assert.Equal(t, tt.want, got, "parseMonthFromTitle(%q)", tt.title)
	}
}

func TestComputeTargetMonths(t *testing.T) {
	jan := parseOrPanic("2025-01-01")
	jan15 := parseOrPanic("2025-01-15")
	feb1 := parseOrPanic("2025-02-01")

	assert.Equal(t, []int{1}, computeTargetMonths(jan, jan15))
	assert.Equal(t, []int{1, 2}, computeTargetMonths(jan, feb1))
}

func TestMonthsCovered(t *testing.T) {
	assert.True(t, monthsCovered(map[int]bool{1: true}, []int{1}))
	assert.True(t, monthsCovered(map[int]bool{1: true, 2: true}, []int{1, 2}))
	assert.False(t, monthsCovered(map[int]bool{1: true}, []int{1, 2}))
	assert.False(t, monthsCovered(map[int]bool{}, []int{1}))
}

func TestMonthInSlice(t *testing.T) {
	assert.True(t, monthInSlice(3, []int{1, 2, 3}))
	assert.False(t, monthInSlice(4, []int{1, 2, 3}))
}

func TestNormalizeURL(t *testing.T) {
	assert.Equal(t, "https://www.goodreads.com/abc", normalizeURL("/abc"))
	assert.Equal(t, "https://example.com/abc", normalizeURL("https://example.com/abc"))
	assert.Equal(t, "", normalizeURL(""))
}

func TestParseBlogPostBooks(t *testing.T) {
	html := `<html><body>
		<div class="js-dataTooltip overflow">
			<img class="oneAcrossImage" src="https://example.com/img1.jpg">
		</div>
		<div class="js-dataTooltip overflow">
			<img class="oneAcrossImage" src="https://example.com/img2.jpg">
		</div>
		<div class="bookInfoFullRow">
			<div class="bookTitle"><i><a href="/book/show/1">Book Title One</a></i></div>
			<div class="bookTitle"><a>Author Name</a></div>
			<div class="bookDescription">A great book.</div>
		</div>
		<div class="bookInfoFullRow">
			<div class="bookTitle"><i><a href="/book/show/2">Book Title Two</a></i></div>
			<div class="bookTitle"><a>Another Author</a></div>
			<div class="bookDescription">Another great book.</div>
		</div>
	</body></html>`

	books, err := parseBlogPostBooks(html)
	assert.NoError(t, err)
	assert.Len(t, books, 2)

	assert.Equal(t, "Book Title One", books[0].Title)
	assert.Equal(t, "Author Name", books[0].Author)
	assert.Equal(t, "https://example.com/img1.jpg", books[0].ImageURL)
	assert.Equal(t, "A great book.", books[0].Description)
	assert.Equal(t, "https://www.goodreads.com/book/show/1", books[0].BookURL)

	assert.Equal(t, "Book Title Two", books[1].Title)
	assert.Equal(t, "Another Author", books[1].Author)
}

func TestParseBlogPostBooksEmpty(t *testing.T) {
	books, err := parseBlogPostBooks("<html><body></body></html>")
	assert.NoError(t, err)
	assert.Empty(t, books)
}

func parseOrPanic(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
