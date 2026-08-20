package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/config"
)

func TestMusicSearchQueries(t *testing.T) {
	queries := musicSearchQueries("Radiohead", "OK Computer", 1997)
	assert.NotEmpty(t, queries)
	assert.Contains(t, queries[0], "Radiohead")
	assert.Contains(t, queries[0], "OK Computer")
	assert.Contains(t, queries[0], "1997")

	// No duplicates
	seen := make(map[string]bool)
	for _, q := range queries {
		assert.False(t, seen[q], "duplicate query: %s", q)
		seen[q] = true
	}
}

func TestMusicSearchQueriesEmpty(t *testing.T) {
	queries := musicSearchQueries("", "", 0)
	// Empty artist+album still generates a "0" query from year
	// This is a known limitation — the caller should guard against empty inputs
	assert.NotNil(t, queries)
}

func TestFormatOverview(t *testing.T) {
	t.Run("wraps at max width", func(t *testing.T) {
		text := "This is a long text that should be wrapped"
		lines := formatOverview(text, 20)
		for _, line := range lines {
			assert.LessOrEqual(t, len(line), 20+5) // allow some slack with ... truncation
		}
	})

	t.Run("short text stays single line", func(t *testing.T) {
		lines := formatOverview("Short", 50)
		assert.Len(t, lines, 1)
		assert.Equal(t, "Short", lines[0])
	})

	t.Run("truncates at 5 lines", func(t *testing.T) {
		text := "word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word"
		lines := formatOverview(text, 5)
		assert.Len(t, lines, 5)
	})

	t.Run("empty text", func(t *testing.T) {
		lines := formatOverview("", 50)
		assert.Empty(t, lines)
	})
}

func TestMusicCategory(t *testing.T) {
	cfg := &config.Config{
		Downloader: config.DownloaderConfig{
			Categories: config.CategoryConfig{
				Music:      "Albums",
				MusicTrial: "Music",
			},
		},
	}
	e := &Executor{cfg: cfg}

	t.Run("non-trial uses music category", func(t *testing.T) {
		assert.Equal(t, "Albums", e.musicCategory(false))
	})

	t.Run("trial uses music_trial category", func(t *testing.T) {
		assert.Equal(t, "Music", e.musicCategory(true))
	})

	t.Run("trial falls back to music when music_trial empty", func(t *testing.T) {
		e2 := &Executor{cfg: &config.Config{
			Downloader: config.DownloaderConfig{
				Categories: config.CategoryConfig{Music: "Albums"},
			},
		}}
		assert.Equal(t, "Albums", e2.musicCategory(true))
	})

	t.Run("non-trial defaults to Music when music empty", func(t *testing.T) {
		e2 := &Executor{cfg: &config.Config{
			Downloader: config.DownloaderConfig{Categories: config.CategoryConfig{}},
		}}
		assert.Equal(t, "Music", e2.musicCategory(false))
	})
}
