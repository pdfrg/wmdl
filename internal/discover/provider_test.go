package discover

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
)

func TestISOWeekToDate(t *testing.T) {
	tests := []struct {
		year, week int
		want       string
	}{
		{2025, 1, "2024-12-30"},
		{2025, 22, "2025-05-26"},
		{2025, 53, "2025-12-29"},
		{2026, 1, "2025-12-29"},
		{2023, 1, "2023-01-02"},
		{2024, 1, "2024-01-01"},
	}
	for _, tt := range tests {
		got := isoWeekToDate(tt.year, tt.week)
		want, _ := time.Parse("2006-01-02", tt.want)
		assert.True(t, got.Equal(want),
			"isoWeekToDate(%d, %d) = %s, want %s", tt.year, tt.week, got.Format("2006-01-02"), tt.want)
	}
}

func TestTuesdayOfISOWeek(t *testing.T) {
	tests := []struct {
		year, week int
		want       string
	}{
		{2025, 22, "2025-05-27"},
		{2025, 1, "2024-12-31"},
	}
	for _, tt := range tests {
		got := tuesdayOfISOWeek(tt.year, tt.week)
		want, _ := time.Parse("2006-01-02", tt.want)
		assert.True(t, got.Equal(want),
			"tuesdayOfISOWeek(%d, %d) = %s, want %s", tt.year, tt.week, got.Format("2006-01-02"), tt.want)
	}
}

func TestWmdlWeekRange(t *testing.T) {
	wedStr := "2025-05-21"
	tueStr := "2025-05-27"
	wed, _ := time.Parse("2006-01-02", wedStr)
	tue, _ := time.Parse("2006-01-02", tueStr)

	gotWed, gotTue := wmdlWeekRange(2025, 22)
	assert.True(t, gotWed.Equal(wed), "start: got %s, want %s", gotWed.Format("2006-01-02"), wedStr)
	assert.True(t, gotTue.Equal(tue), "end: got %s, want %s", gotTue.Format("2006-01-02"), tueStr)
}

func TestMostRecentTuesday(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"2025-05-28", "2025-05-27"},
		{"2025-05-27", "2025-05-27"},
		{"2025-05-26", "2025-05-20"},
		{"2025-05-25", "2025-05-20"},
	}
	for _, tt := range tests {
		input, _ := time.Parse("2006-01-02", tt.input)
		want, _ := time.Parse("2006-01-02", tt.want)
		got := mostRecentTuesday(input)
		assert.True(t, got.Equal(want),
			"mostRecentTuesday(%s) = %s, want %s", tt.input, got.Format("2006-01-02"), tt.want)
	}
}

func TestExtractYear(t *testing.T) {
	tests := []struct {
		imgAlt, imgSrc, title string
		want                  int
	}{
		{"Dreams 2025", "Dreams-2025.jpg", "Dreams", 2025},
		{"", "GOAT-2026.jpg", "GOAT", 2026},
		{"", "", "The Movie (2024)", 2024},
		{"No Year", "image.jpg", "No Year", 0},
		{"2023", "", "Title", 2023},
	}
	for _, tt := range tests {
		got := extractYear(tt.imgAlt, tt.imgSrc, tt.title)
		assert.Equal(t, tt.want, got, "extractYear(%q, %q, %q)", tt.imgAlt, tt.imgSrc, tt.title)
	}
}

func TestCleanTitle(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"The Movie  DVD", "The Movie"},
		{"The Movie Blu-ray", "The Movie"},
		{"The Movie 4K", "The Movie"},
		{"The Movie", "The Movie"},
		{"  Spaced  ", "Spaced"},
	}
	for _, tt := range tests {
		got := cleanTitle(tt.input)
		assert.Equal(t, tt.want, got, "cleanTitle(%q)", tt.input)
	}
}

func TestParseJikanTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"2025-05-27T00:00:00+00:00", "2025-05-27", true},
		{"2025-05-27T12:00:00Z", "2025-05-27", true},
		{"2025-05-27", "2025-05-27", true},
		{"", "", false},
		{"invalid", "", false},
	}
	for _, tt := range tests {
		got, err := parseJikanTime(tt.input)
		if !tt.ok {
			assert.Error(t, err)
			return
		}
		assert.NoError(t, err)
		assert.Equal(t, tt.want, got.Format("2006-01-02"))
	}
}

func TestFuzzyDateInt(t *testing.T) {
	d := time.Date(2025, 5, 27, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 20250527, fuzzyDateInt(d))
}

func TestHtmlEntityUnescape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"&amp;", "&"},
		{"&lt;tag&gt;", "<tag>"},
		{"&quot;hello&quot;", "\"hello\""},
		{"&nbsp;", " "},
		{"&amp;lt;", "<"},
		{"normal text", "normal text"},
	}
	for _, tt := range tests {
		got := htmlEntityUnescape(tt.input)
		assert.Equal(t, tt.want, got, "htmlEntityUnescape(%q)", tt.input)
	}
}

func TestExtractImageURL(t *testing.T) {
	html := `<html><body>
		<div id="s1" class="image" style="background-image: url('//example.com/img.jpg')"></div>
		<div id="s2" class="image"><img src="//example.com/img2.jpg"></div>
		<div id="s3" class="image" data-src="//example.com/img3.jpg"></div>
		<div id="s4" class="image" data-lazy="//example.com/img4.jpg"></div>
		<div id="s5" class="image" data-original="//example.com/img5.jpg"></div>
		<div id="s6" class="image" style="background-image: url('http://example.com/img6.jpg')"></div>
		<div id="s7" class="image"></div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	assert.NoError(t, err)

	tests := []struct {
		id   string
		want string
	}{
		{"s1", "https://example.com/img.jpg"},
		{"s2", "https://example.com/img2.jpg"},
		{"s3", "https://example.com/img3.jpg"},
		{"s4", "https://example.com/img4.jpg"},
		{"s5", "https://example.com/img5.jpg"},
		{"s6", "http://example.com/img6.jpg"},
		{"s7", ""},
	}

	for _, tt := range tests {
		sel := doc.Find("#" + tt.id)
		got := extractImageURL(sel)
		assert.Equal(t, tt.want, got, "id=%s", tt.id)
	}

	// Also test /200x0/ → /500x0/ upgrade
	html2 := `<div class="image" style="background-image: url('//example.com/200x0/img.jpg')"></div>`
	doc2, err := goquery.NewDocumentFromReader(strings.NewReader(html2))
	assert.NoError(t, err)
	got := extractImageURL(doc2.Find("div.image"))
	assert.Equal(t, "https://example.com/500x0/img.jpg", got)
}

func TestParseAOTYDate(t *testing.T) {
	tests := []struct {
		input  string
		suffix string
		valid  bool
	}{
		{"January 15", "-01-15", true},
		{"May 27", "-05-27", true},
		{"Dec 25", "-12-25", true},
		{"January 2", "-01-02", true},
		{"InvalidDay", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got := parseAOTYDate(tt.input)
		if !tt.valid {
			assert.Empty(t, got, "parseAOTYDate(%q) should be empty", tt.input)
			continue
		}
		assert.Contains(t, got, tt.suffix, "parseAOTYDate(%q) = %q", tt.input, got)
		_, err := time.Parse("2006-01-02", got)
		assert.NoError(t, err, "parseAOTYDate(%q) returned non-ISO date: %s", tt.input, got)
	}
}
