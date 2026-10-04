package review

import (
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func movieItemPtr(tmdbID int) *itemState {
	return &itemState{event: db.EventWithTitle{
		Event: &model.ReleaseEvent{},
		Title: &model.Title{
			Title:      "Movie",
			MediaType:  model.MediaTypeMovie,
			TmdbID:     tmdbID,
			PosterPath: "/poster.jpg",
		},
	}}
}

func movieItem(tmdbID int) itemState {
	return itemState{event: db.EventWithTitle{
		Event: &model.ReleaseEvent{},
		Title: &model.Title{
			Title:      "Movie",
			MediaType:  model.MediaTypeMovie,
			TmdbID:     tmdbID,
			PosterPath: "/poster.jpg",
		},
	}}
}

// newPosterTUI builds a TUI with three movies, cursor on the first item.
func newPosterTUI(t *testing.T) *TUI {
	t.Helper()
	tui := &TUI{
		items:        []itemState{movieItem(1), movieItem(2), movieItem(3)},
		posterMode:   PosterKitty,
		posterCache:  make(map[string]image.Image),
		posterFailed: make(map[string]bool),
	}
	tui.rebuildFiltered()
	return tui
}

func TestPosterKeyFor(t *testing.T) {
	assert.Equal(t, "tmdb:1", posterKeyFor(movieItemPtr(1)))
	assert.Equal(t, "", posterKeyFor(nil))
	assert.Equal(t, "", posterKeyFor(&itemState{event: db.EventWithTitle{
		Event: &model.ReleaseEvent{},
		Title: &model.Title{Title: "No poster"},
	}}))
	assert.Equal(t, "anime:mal:55", posterKeyFor(&itemState{event: db.EventWithTitle{
		Event: &model.ReleaseEvent{},
		Title: &model.Title{Title: "Anime", MalID: 55, PosterPath: "https://img/jikan.jpg"},
	}}))
	assert.Equal(t, "book:isbn13:9781234567897", posterKeyFor(&itemState{bookEvent: &db.EventWithBook{
		Event: &model.BookReleaseEvent{},
		Book:  &model.Book{ISBN13: "9781234567897"},
	}}))
	assert.Equal(t, "book:url:https://img/cover.jpg", posterKeyFor(&itemState{bookEvent: &db.EventWithBook{
		Event: &model.BookReleaseEvent{},
		Book:  &model.Book{ISBN13: "9781234567897", ImageURL: "https://img/cover.jpg"},
	}}))
}

func TestPosterDebounceSchedulesOnce(t *testing.T) {
	tui := newPosterTUI(t)

	cmd := tui.schedulePosterCmd()
	require.NotNil(t, cmd, "first move should schedule a debounced load")
	assert.Nil(t, tui.posterImg)
	assert.Equal(t, "tmdb:1", tui.posterPending)

	// Repeated scheduling for the same item must not queue more timers.
	assert.Nil(t, tui.schedulePosterCmd())
	assert.Equal(t, "tmdb:1", tui.posterPending)

	// Moving on supersedes the pending load.
	tui.cursor = 1
	assert.NotNil(t, tui.schedulePosterCmd())
	assert.Equal(t, "tmdb:2", tui.posterPending)
}

func TestPosterDebounceMessageOnlyFiresForSettledItem(t *testing.T) {
	tui := newPosterTUI(t)

	// User scrolls twice more; the first timer is now superseded.
	tui.cursor = 1
	tui.schedulePosterCmd()
	tui.cursor = 2
	tui.schedulePosterCmd()

	// The original timer fires for the item the user already scrolled past.
	_, cmd := tui.Update(posterDebounceMsg{key: "tmdb:1"})
	assert.Nil(t, cmd, "stale debounce must not trigger a load")
	assert.Nil(t, tui.posterImg)
	assert.Equal(t, "tmdb:3", tui.posterPending)
}

func TestPosterStaleReadyMsgIsIgnored(t *testing.T) {
	tui := newPosterTUI(t)
	tui.cursor = 1 // settled on item 2

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// A load for a previously scrolled-past item finishes late.
	tui.Update(posterReadyMsg{key: "tmdb:1", img: img})
	assert.Nil(t, tui.posterImg, "stale poster must not be displayed")
	assert.Empty(t, tui.posterKey)

	// The load for the settled item applies.
	_, _ = tui.Update(posterReadyMsg{key: "tmdb:2", img: img})
	assert.Equal(t, img, tui.posterImg)
	assert.Equal(t, "tmdb:2", tui.posterKey)
	assert.Empty(t, tui.posterPending)
}

func TestPosterStaleErrorDoesNotWipeCurrentPoster(t *testing.T) {
	tui := newPosterTUI(t)
	tui.cursor = 1

	current := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, _ = tui.Update(posterReadyMsg{key: "tmdb:2", img: current})

	// Late failure for an old item must not clear the visible poster.
	tui.Update(posterReadyMsg{key: "tmdb:1", err: assertErr{}})
	assert.Equal(t, current, tui.posterImg)
	assert.Equal(t, "tmdb:2", tui.posterKey)
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }

func TestPosterCacheServesWithoutLoad(t *testing.T) {
	tui := newPosterTUI(t)
	cached := image.NewRGBA(image.Rect(0, 0, 2, 2))
	tui.posterCache["tmdb:1"] = cached

	tui.posterPending = ""
	tui.schedulePosterCmd()
	assert.Equal(t, cached, tui.posterImg)
	assert.Equal(t, "tmdb:1", tui.posterKey)
	assert.Empty(t, tui.posterPending, "cached poster must not schedule a load")
}

func TestPosterFailedKeyIsNotRetried(t *testing.T) {
	tui := newPosterTUI(t)
	tui.posterFailed["tmdb:1"] = true

	tui.schedulePosterCmd()
	assert.Nil(t, tui.posterImg)
	assert.Equal(t, "tmdb:1", tui.posterKey)
	assert.Empty(t, tui.posterPending)
}

func TestPosterMissingClearsDisplay(t *testing.T) {
	tui := newPosterTUI(t)
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, _ = tui.Update(posterReadyMsg{key: "tmdb:1", img: img})

	// An item with no poster clears the panel instead of leaving the old one.
	tui.items = []itemState{{event: db.EventWithTitle{
		Event: &model.ReleaseEvent{},
		Title: &model.Title{Title: "No poster"},
	}}}
	tui.rebuildFiltered()

	tui.schedulePosterCmd()
	assert.Nil(t, tui.posterImg)
	assert.Empty(t, tui.posterKey)
}
