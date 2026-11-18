package review

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func seedFilteredVideoEvent(t *testing.T, d *db.DB, title, reason string) (int64, *model.Title) {
	t.Helper()
	ctx := context.Background()
	tid, err := d.UpsertTitle(ctx, &model.Title{TmdbID: 9100, Title: title, Year: 2026, MediaType: model.MediaTypeTV})
	require.NoError(t, err)
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID:      tid,
		Source:       "test",
		Status:       model.StatusFiltered,
		FilterReason: reason,
		ISOYear:      2026,
		ISOWeek:      32,
	})
	require.NoError(t, err)
	return id, &model.Title{ID: tid, TmdbID: 9100, Title: title, Year: 2026, MediaType: model.MediaTypeTV}
}

func TestFilteredItemsHiddenByDefault(t *testing.T) {
	d := openReviewTestDB(t)
	id, title := seedFilteredVideoEvent(t, d, "Hidden Gem", "language ja is blocked")

	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Source: "test", Status: model.StatusFiltered, ISOYear: 2026, ISOWeek: 32}},
	}, nil, nil, d, "none", 2026, 32, "", model.BookFormatBoth)
	require.NoError(t, err)

	assert.Empty(t, tui.filtered, "filtered item must be hidden by default")
	assert.Nil(t, tui.currentItem())

	approved, _, pending, downloaded := tui.Counts()
	assert.Equal(t, 0, approved)
	assert.Equal(t, 0, pending, "filtered item must not count as pending")
	assert.Equal(t, 0, downloaded)
	assert.Equal(t, 1, tui.filteredUndecided())

	// Reason is loaded from the DB and appears once the view is toggled on.
	tui.SetShowFiltered(true)
	require.Len(t, tui.filtered, 1)
	require.NotNil(t, tui.currentItem())

	content := tui.buildReviewContent()
	assert.Contains(t, content, "Filtered:")
	assert.Contains(t, content, "language ja is blocked")
}

func TestApproveFilteredItem(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()
	id, title := seedFilteredVideoEvent(t, d, "Hidden Gem", "language ja is blocked")

	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Source: "test", Status: model.StatusFiltered, ISOYear: 2026, ISOWeek: 32}},
	}, nil, nil, d, "none", 2026, 32, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.SetShowFiltered(true)
	tui.approveCurrent()
	require.Equal(t, decisionApproved, tui.items[0].decision)
	require.NoError(t, tui.saveDecisions())

	got, err := d.GetReleaseEventWithTitle(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusApproved, got.Event.Status)

	// Approving counts as approved, not pending, and no longer blocks review.
	approved, _, pending, _ := tui.Counts()
	assert.Equal(t, 1, approved)
	assert.Equal(t, 0, pending)
}

func TestRejectFilteredItem(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()
	id, title := seedFilteredVideoEvent(t, d, "Hidden Gem", "rating too low")

	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Source: "test", Status: model.StatusFiltered, ISOYear: 2026, ISOWeek: 32}},
	}, nil, nil, d, "none", 2026, 32, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.SetShowFiltered(true)
	tui.rejectCurrent()
	require.NoError(t, tui.saveDecisions())

	got, err := d.GetReleaseEventWithTitle(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusRejected, got.Event.Status)
}

func TestFilteredBookReasonLoaded(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()
	authorID, err := d.UpsertAuthor(ctx, &model.Author{Name: "Author", OLID: "ol-filtered"})
	require.NoError(t, err)
	bookID, err := d.UpsertBook(ctx, &model.Book{AuthorID: authorID, Title: "Low Rated"})
	require.NoError(t, err)
	evID, err := d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{
		BookID: bookID, Source: "test", Status: model.StatusFiltered,
		FilterReason: "rating 3.1 below min 3.5", FormatPref: model.BookFormatBoth,
		ISOYear: 2026, ISOWeek: 32,
	})
	require.NoError(t, err)

	tui, err := NewReviewTUIWithEvents(nil, nil, []db.EventWithBook{{
		Book:   &model.Book{ID: bookID, AuthorID: authorID, Title: "Low Rated"},
		Author: &model.Author{ID: authorID, Name: "Author"},
		Event: &model.BookReleaseEvent{
			ID: evID, BookID: bookID, Source: "test", Status: model.StatusFiltered,
			FormatPref: model.BookFormatBoth, ISOYear: 2026, ISOWeek: 32,
		},
	}}, d, "none", 2026, 32, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.SetShowFiltered(true)
	content := tui.buildReviewContent()
	assert.Contains(t, content, "Filtered:")
	assert.Contains(t, content, "rating 3.1 below min 3.5")
}
