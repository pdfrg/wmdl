package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/process"
)

func TestParseWeekFlagEmpty(t *testing.T) {
	// Empty string returns the last completed Tuesday's ISO week
	y, w, err := parseWeekFlag("")
	assert.NoError(t, err)
	assert.Greater(t, y, 2020)
	assert.GreaterOrEqual(t, w, 1)
	assert.LessOrEqual(t, w, 53)
}

func TestParseWeekFlagWeekOnly(t *testing.T) {
	y, w, err := parseWeekFlag("W22")
	assert.NoError(t, err)
	assert.Equal(t, 22, w)
	cy, _ := time.Now().ISOWeek()
	assert.Equal(t, cy, y)

	// lowercase
	y2, w2, err := parseWeekFlag("w5")
	assert.NoError(t, err)
	cy2, _ := time.Now().ISOWeek()
	assert.Equal(t, cy2, y2)
	assert.Equal(t, 5, w2)
}

func TestParseWeekFlagYearWeek(t *testing.T) {
	t.Run("with dash", func(t *testing.T) {
		y, w, err := parseWeekFlag("2025-W22")
		assert.NoError(t, err)
		assert.Equal(t, 2025, y)
		assert.Equal(t, 22, w)
	})

	t.Run("without dash", func(t *testing.T) {
		y, w, err := parseWeekFlag("2025W05")
		assert.NoError(t, err)
		assert.Equal(t, 2025, y)
		assert.Equal(t, 5, w)
	})

	t.Run("out of range", func(t *testing.T) {
		_, _, err := parseWeekFlag("2025-W99")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "out of range")
	})
}

func TestParseWeekFlagDate(t *testing.T) {
	t.Run("full date", func(t *testing.T) {
		y, w, err := parseWeekFlag("05-27-2025")
		assert.NoError(t, err)
		assert.Equal(t, 2025, y)
		assert.Equal(t, 22, w) // May 27 2025 is Tuesday of ISO week 22
	})

	t.Run("month-day only", func(t *testing.T) {
		y, w, err := parseWeekFlag("01-01")
		assert.NoError(t, err)
		cy, _ := time.Now().ISOWeek()
		assert.Equal(t, cy, y)
		assert.GreaterOrEqual(t, w, 1)
	})

	t.Run("invalid date", func(t *testing.T) {
		_, _, err := parseWeekFlag("02-30-2025")
		assert.Error(t, err)
	})

	t.Run("invalid month-day", func(t *testing.T) {
		_, _, err := parseWeekFlag("13-01")
		assert.Error(t, err)
	})
}

func TestParseWeekFlagRelative(t *testing.T) {
	y, w, err := parseWeekFlag("-1")
	assert.NoError(t, err)
	// Should be approximately last week
	ly, lw := time.Now().AddDate(0, 0, -7).ISOWeek()
	assert.Equal(t, ly, y)
	assert.Equal(t, lw, w)
}

func TestParseWeekFlagErrors(t *testing.T) {
	t.Run("ambiguous bare number", func(t *testing.T) {
		_, _, err := parseWeekFlag("22")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ambiguous")
	})

	t.Run("unrecognized format", func(t *testing.T) {
		_, _, err := parseWeekFlag("foobar")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unrecognized")
	})

	t.Run("empty after trim", func(t *testing.T) {
		yy, _, err := parseWeekFlag("  ")
		assert.NoError(t, err)
		assert.Greater(t, yy, 2020)
	})
}

func TestParseLookbackFlag(t *testing.T) {
	t.Run("single type single value", func(t *testing.T) {
		ov, err := parseLookbackFlag("movie:8")
		assert.NoError(t, err)
		assert.Equal(t, discover.LookbackRange{Min: 8, Max: 8}, ov["movie"])
	})

	t.Run("single type range", func(t *testing.T) {
		ov, err := parseLookbackFlag("tv:2-8")
		assert.NoError(t, err)
		assert.Equal(t, discover.LookbackRange{Min: 2, Max: 8}, ov["tv"])
	})

	t.Run("multiple types", func(t *testing.T) {
		ov, err := parseLookbackFlag("physical:8,anime:1-4")
		assert.NoError(t, err)
		assert.Equal(t, discover.LookbackRange{Min: 8, Max: 8}, ov["physical"])
		assert.Equal(t, discover.LookbackRange{Min: 1, Max: 4}, ov["anime"])
	})

	t.Run("all valid types", func(t *testing.T) {
		ov, err := parseLookbackFlag("movie:1,tv:2,physical:3,anime:4,music:5,book:6")
		assert.NoError(t, err)
		assert.Len(t, ov, 6)
	})

	t.Run("invalid type", func(t *testing.T) {
		_, err := parseLookbackFlag("invalid:2")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid lookback type")
	})

	t.Run("no segments", func(t *testing.T) {
		_, err := parseLookbackFlag("")
		assert.Error(t, err)
	})

	t.Run("malformed segment", func(t *testing.T) {
		_, err := parseLookbackFlag("movie")
		assert.Error(t, err)
	})

	t.Run("min > max", func(t *testing.T) {
		_, err := parseLookbackFlag("movie:8-2")
		assert.Error(t, err)
	})

	t.Run("negative value", func(t *testing.T) {
		_, err := parseLookbackFlag("movie:-1")
		assert.Error(t, err)
	})
}

func TestWeekLabel(t *testing.T) {
	s := &model.WeekState{Year: 2025, Week: 22}
	assert.Equal(t, "2025-W22", weekLabel(s))
}

func TestCheckMark(t *testing.T) {
	assert.Equal(t, "✓", checkMark(true))
	assert.Equal(t, "✗", checkMark(false))
}

func TestProcessStatus(t *testing.T) {
	t.Run("not reviewed", func(t *testing.T) {
		s := &model.WeekState{Reviewed: false}
		assert.Equal(t, "✗", processStatus(s))
	})

	t.Run("no approved items", func(t *testing.T) {
		s := &model.WeekState{Reviewed: true, ApprovedCount: 0}
		assert.Equal(t, "N/A", processStatus(s))
	})

	t.Run("all processed", func(t *testing.T) {
		s := &model.WeekState{Reviewed: true, ApprovedCount: 5, Processed: true, DownloadedCount: 5}
		assert.Equal(t, "✓", processStatus(s))
	})

	t.Run("partially processed", func(t *testing.T) {
		s := &model.WeekState{Reviewed: true, ApprovedCount: 5, Processed: true, DownloadedCount: 3}
		assert.Equal(t, "3/5", processStatus(s))
	})

	t.Run("not processed yet", func(t *testing.T) {
		s := &model.WeekState{Reviewed: true, ApprovedCount: 5, Processed: false}
		assert.Equal(t, "✗", processStatus(s))
	})
}

func TestFormatDate(t *testing.T) {
	assert.Equal(t, "2025-05-27", formatDate("2025-05-27T12:00:00Z"))
	assert.Equal(t, "2025-05-27", formatDate("2025-05-27"))
	assert.Equal(t, "          ", formatDate(""))
	assert.Equal(t, "          ", formatDate("short"))
}

func TestLabelForSearchResult(t *testing.T) {
	t.Run("with year", func(t *testing.T) {
		sr := &process.SearchResult{}
		sr.Event.Title = &model.Title{Title: "The Matrix", Year: 1999}
		assert.Equal(t, "The Matrix (1999)", labelForSearchResult(sr))
	})

	t.Run("without year", func(t *testing.T) {
		sr := &process.SearchResult{}
		sr.Event.Title = &model.Title{Title: "No Year Show"}
		assert.Equal(t, "No Year Show", labelForSearchResult(sr))
	})
}

func TestJoinGenreNames(t *testing.T) {
	genres := []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{
		{ID: 1, Name: "Action"},
		{ID: 2, Name: "Comedy"},
	}
	assert.Equal(t, "Action, Comedy", joinGenreNames(genres))
	assert.Equal(t, "", joinGenreNames(nil))
}

func TestFirstOr(t *testing.T) {
	assert.Equal(t, "hello", firstOr("hello", "fallback"))
	assert.Equal(t, "fallback", firstOr("", "fallback"))
	assert.Equal(t, "", firstOr("", ""))
}

func TestCollectionID(t *testing.T) {
	coll := &struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{ID: 42, Name: "Collection"}
	assert.Equal(t, 42, collectionID(coll))
	assert.Equal(t, 0, collectionID(nil))
}

func TestCollectionName(t *testing.T) {
	coll := &struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{ID: 42, Name: "Collection"}
	assert.Equal(t, "Collection", collectionName(coll))
	assert.Equal(t, "", collectionName(nil))
}

func TestLatestCompletedSeason(t *testing.T) {
	seasons := []discover.TMDBTVSeason{
		{SeasonNumber: 0, AirDate: "2025-01-01"},
		{SeasonNumber: 1, AirDate: "2025-01-01"},
		{SeasonNumber: 2, AirDate: "2030-01-01"}, // future, not passed
		{SeasonNumber: 3, AirDate: "2025-06-01"},
	}
	latest := latestCompletedSeason(seasons)
	assert.Equal(t, 3, latest)
}

func TestContains(t *testing.T) {
	formats := []model.BookFormat{model.BookFormatEbook, model.BookFormatAudiobook}
	assert.True(t, contains(formats, model.BookFormatEbook))
	assert.True(t, contains(formats, model.BookFormatAudiobook))
	assert.False(t, contains(formats, model.BookFormatBoth))
	assert.False(t, contains(nil, model.BookFormatEbook))
}
