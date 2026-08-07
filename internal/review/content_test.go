package review

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestBuildMusicContent(t *testing.T) {
	lineIndex := func(lines []string, substr string) int {
		for i, l := range lines {
			if strings.Contains(l, substr) {
				return i
			}
		}
		return -1
	}

	assertNoMergedLine := func(t *testing.T, lines []string, a, b string) {
		t.Helper()
		for i, l := range lines {
			if strings.Contains(l, a) && strings.Contains(l, b) {
				t.Fatalf("line %d merges %q and %q: %q", i, a, b, l)
			}
		}
	}

	build := func(t *testing.T, release *model.AlbumRelease, ev *model.AlbumReleaseEvent) string {
		t.Helper()
		d, err := db.Open(t.TempDir() + "/wmdl-test.db")
		require.NoError(t, err)
		t.Cleanup(func() { d.Close() })
		require.NoError(t, d.Migrate(context.Background()))

		albumEvents := []db.EventWithAlbumRelease{{Release: release, Event: ev}}
		tui, err := NewReviewTUIWithEvents(nil, albumEvents, nil, d, "off", 2026, 32, "", model.BookFormatBoth)
		require.NoError(t, err)

		return tui.buildMusicContent(&albumEvents[0], 70, 30)
	}

	t.Run("rpcharts_label_appears_once", func(t *testing.T) {
		release := &model.AlbumRelease{
			ArtistName:  "The Artist",
			Title:       "The Album",
			Year:        2026,
			MBID:        "release-group-id",
			ArtistMBID:  "artist-id",
			MBRating:    8.5,
			ReleaseDate: "2026-07-26",
		}
		ev := &model.AlbumReleaseEvent{
			Source:      "rpcharts",
			Status:      model.StatusPending,
			Notes:       "https://radioparadise.com/music/album/26434",
			ReleaseDate: "2026-07-26",
		}
		content := build(t, release, ev)

		assert.Equal(t, 1, strings.Count(content, "first seen by rpcharts: 2026-07-26"))
	})

	t.Run("url_below_rating_verified_artist", func(t *testing.T) {
		release := &model.AlbumRelease{
			ArtistName:  "The Artist",
			Title:       "The Album",
			MBID:        "release-group-id",
			ArtistMBID:  "artist-id",
			MBRating:    8.5,
			ReleaseDate: "2026-07-26",
		}
		ev := &model.AlbumReleaseEvent{
			Source:      "rpcharts",
			Status:      model.StatusPending,
			Notes:       "https://radioparadise.com/music/album/26434",
			ReleaseDate: "2026-07-26",
		}
		lines := strings.Split(build(t, release, ev), "\n")

		ratingIdx := lineIndex(lines, "MB artist rating: 8.5")
		urlIdx := lineIndex(lines, "https://radioparadise.com/music/album/26434")
		assert.GreaterOrEqual(t, ratingIdx, 0, "rating line present")
		assert.GreaterOrEqual(t, urlIdx, 0, "url line present")
		assert.Less(t, ratingIdx, urlIdx, "rating must appear above the url")
		assertNoMergedLine(t, lines, "MB artist rating", "http")
	})

	t.Run("url_and_rating_separate_lines_unverified_artist", func(t *testing.T) {
		release := &model.AlbumRelease{
			ArtistName:  "The Artist",
			Title:       "The Album",
			MBID:        "release-group-id",
			ArtistMBID:  "",
			MBRating:    8.5,
			ReleaseDate: "2026-07-26",
		}
		ev := &model.AlbumReleaseEvent{
			Source:      "rpcharts",
			Status:      model.StatusPending,
			Notes:       "https://radioparadise.com/music/album/26434",
			ReleaseDate: "2026-07-26",
		}
		lines := strings.Split(build(t, release, ev), "\n")

		assert.GreaterOrEqual(t, lineIndex(lines, "MB artist rating: 8.5"), 0, "rating line present")
		assert.GreaterOrEqual(t, lineIndex(lines, "https://radioparadise.com/music/album/26434"), 0, "url line present")
		assertNoMergedLine(t, lines, "MB artist rating", "http")
	})

	t.Run("aoty_date_appears_once", func(t *testing.T) {
		release := &model.AlbumRelease{
			ArtistName:  "The Artist",
			Title:       "The Album",
			AlbumType:   model.AlbumTypeLP,
			MBID:        "release-group-id",
			ArtistMBID:  "artist-id",
			ReleaseDate: "2026-06-05",
		}
		ev := &model.AlbumReleaseEvent{
			Source:      "albumoftheyear",
			Status:      model.StatusPending,
			ReleaseDate: "2026-06-05",
		}
		content := build(t, release, ev)

		assert.Equal(t, 1, strings.Count(content, "2026-06-05"))
		assert.Equal(t, 0, strings.Count(content, "first seen"))
	})
}
