package process

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolutionSearchKeyword(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2160p", "2160p"},
		{"4k", "2160p"},
		{"uhd", "2160p"},
		{"1080p", "1080p"},
		{"720p", "720p"},
		{"480p", "1080p"},
		{"", "1080p"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := resolutionSearchKeyword(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFallbackResolution(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2160p", "1080p"},
		{"1080p", "720p"},
		{"720p", "480p"},
		{"480p", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := fallbackResolution(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeSearchQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"The Movie 2025 1080p", "The Movie 2025 1080p"},
		{"Show/Name S01", "Show Name S01"},
		{"It's Fine", "Its Fine"},
		{`"Quoted"`, "Quoted"},
		{"What? No", "What No"},
		{"Time: Now", "Time  Now"},
		{"Half½", "Half"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeSearchQuery(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMovieSearchQueries(t *testing.T) {
	queries := movieSearchQueries("The Matrix", 1999, "2160p")
	assert.Len(t, queries, 4)
	assert.Contains(t, queries[0], "The Matrix 1999 2160p")
	assert.Contains(t, queries[1], "The Matrix 1999 4k")
	assert.Contains(t, queries[2], "The Matrix 1999 1080p")
	assert.Contains(t, queries[3], "The Matrix 1999")
	// All should be sanitized (no special chars)
	for _, q := range queries {
		assert.NotContains(t, q, "/")
		assert.NotContains(t, q, "'")
	}
}

func TestTVSearchQueries(t *testing.T) {
	t.Run("season included", func(t *testing.T) {
		queries := tvSearchQueries("Show", 2, "1080p", "720p", false)
		assert.Len(t, queries, 6)
		assert.Contains(t, queries[0], "S02")
		assert.Contains(t, queries[0], "complete")
		assert.Contains(t, queries[1], "season 2")
		assert.Contains(t, queries[5], "720p")
	})

	t.Run("season skipped for final season", func(t *testing.T) {
		queries := tvSearchQueries("Show Final Season", 1, "1080p", "", true)
		assert.Len(t, queries, 1)
		assert.NotContains(t, queries[0], "S01")
		assert.NotContains(t, queries[0], "season 1")
	})

	t.Run("no fallback", func(t *testing.T) {
		queries := tvSearchQueries("Show", 1, "1080p", "", false)
		assert.Len(t, queries, 5)
	})

	t.Run("special chars sanitized", func(t *testing.T) {
		queries := tvSearchQueries("Show/Name", 1, "1080p", "", false)
		for _, q := range queries {
			assert.NotContains(t, q, "/")
		}
	})
}
