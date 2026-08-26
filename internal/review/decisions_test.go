package review

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func openReviewTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/wmdl-test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func seedReviewVideoEvent(t *testing.T, d *db.DB, title string, status model.ReleaseStatus) (int64, *model.Title) {
	t.Helper()
	ctx := context.Background()
	tid, err := d.UpsertTitle(ctx, &model.Title{TmdbID: 9000, Title: title, Year: 2025, MediaType: model.MediaTypeTV})
	require.NoError(t, err)
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{TitleID: tid, Source: "test", Status: status})
	require.NoError(t, err)
	return id, &model.Title{ID: tid, TmdbID: 9000, Title: title, Year: 2025, MediaType: model.MediaTypeTV}
}

func seedReviewBookEvent(t *testing.T, d *db.DB, title string, status BookFormatProcessedState) (int64, *model.Book, *model.Author, *model.BookReleaseEvent) {
	t.Helper()
	ctx := context.Background()
	authorID, err := d.UpsertAuthor(ctx, &model.Author{Name: "Author", OLID: "olid-" + title})
	require.NoError(t, err)
	bookID, err := d.UpsertBook(ctx, &model.Book{AuthorID: authorID, Title: title})
	require.NoError(t, err)
	ev := &model.BookReleaseEvent{
		BookID:             bookID,
		Source:             "test",
		Status:             status.Status,
		FormatPref:         status.FormatPref,
		EbookProcessed:     status.EbookProcessed,
		AudiobookProcessed: status.AudiobookProcessed,
	}
	id, err := d.CreateBookReleaseEvent(ctx, ev)
	require.NoError(t, err)
	return id, &model.Book{ID: bookID, AuthorID: authorID, Title: title}, &model.Author{ID: authorID, Name: "Author"}, &model.BookReleaseEvent{ID: id, BookID: bookID, Status: status.Status, FormatPref: status.FormatPref, EbookProcessed: status.EbookProcessed, AudiobookProcessed: status.AudiobookProcessed}
}

type BookFormatProcessedState struct {
	Status             model.ReleaseStatus
	FormatPref         model.BookFormat
	EbookProcessed     bool
	AudiobookProcessed bool
}

func TestDecisionForStatus(t *testing.T) {
	assert.Equal(t, decisionApproved, decisionForStatus(model.StatusApproved))
	assert.Equal(t, decisionDownloaded, decisionForStatus(model.StatusDownloaded))
	assert.Equal(t, decisionRejected, decisionForStatus(model.StatusRejected))
	assert.Equal(t, decisionNone, decisionForStatus(model.StatusPending))
}

func TestSaveDecisionsPreservesDownloaded(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()

	dlID, dlTitle := seedReviewVideoEvent(t, d, "Already Got It", model.StatusDownloaded)
	pendingID, pendingTitle := seedReviewVideoEvent(t, d, "New One", model.StatusApproved)

	events := []db.EventWithTitle{
		{Title: dlTitle, Event: &model.ReleaseEvent{ID: dlID, Status: model.StatusDownloaded}},
		{Title: pendingTitle, Event: &model.ReleaseEvent{ID: pendingID, Status: model.StatusApproved}},
	}
	tui, err := NewReviewTUIWithEvents(events, nil, nil, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	// The downloaded item is decisionDownloaded; the approved one is decisionApproved.
	require.Equal(t, decisionDownloaded, tui.items[0].decision)
	require.Equal(t, decisionApproved, tui.items[1].decision)

	require.NoError(t, tui.saveDecisions())

	gotDL, err := d.GetReleaseEventWithTitle(ctx, dlID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusDownloaded, gotDL.Event.Status, "downloaded item must not be reverted to approved")

	gotNew, err := d.GetReleaseEventWithTitle(ctx, pendingID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusApproved, gotNew.Event.Status, "new approval must still be written")
}

func TestApproveKeyNoopOnDownloadedVideo(t *testing.T) {
	d := openReviewTestDB(t)
	id, title := seedReviewVideoEvent(t, d, "Done Movie", model.StatusDownloaded)
	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Status: model.StatusDownloaded}},
	}, nil, nil, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.approveCurrent()
	assert.Equal(t, decisionDownloaded, tui.items[0].decision)

	// undecide is also a no-op on a downloaded item.
	tui.undecideCurrent()
	assert.Equal(t, decisionDownloaded, tui.items[0].decision)

	// reject toggles downloaded -> rejected -> downloaded.
	tui.rejectCurrent()
	assert.Equal(t, decisionRejected, tui.items[0].decision)
	tui.rejectCurrent()
	assert.Equal(t, decisionDownloaded, tui.items[0].decision)
}

func TestRejectDownloadedWritesRejected(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()
	id, title := seedReviewVideoEvent(t, d, "Change My Mind", model.StatusDownloaded)
	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Status: model.StatusDownloaded}},
	}, nil, nil, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.rejectCurrent()
	require.NoError(t, tui.saveDecisions())

	got, err := d.GetReleaseEventWithTitle(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusRejected, got.Event.Status)
}

func TestBookFormatExpansion(t *testing.T) {
	d := openReviewTestDB(t)
	ctx := context.Background()

	state := BookFormatProcessedState{Status: model.StatusDownloaded, FormatPref: model.BookFormatEbook, EbookProcessed: true}
	id, book, author, ev := seedReviewBookEvent(t, d, "Half Book", state)

	tui, err := NewReviewTUIWithEvents(nil, nil, []db.EventWithBook{
		{Book: book, Author: author, Event: ev},
	}, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)
	require.Equal(t, decisionDownloaded, tui.items[0].decision)

	// `a` expands a half-downloaded book to both and re-approves it.
	tui.approveCurrent()
	assert.Equal(t, decisionApproved, tui.items[0].decision)
	assert.Equal(t, model.BookFormatBoth, tui.items[0].bookEvent.Event.FormatPref)
	assert.True(t, tui.items[0].bookFormatExpanded)

	// `a` again is a no-op (no format cycling).
	tui.approveCurrent()
	assert.Equal(t, decisionApproved, tui.items[0].decision)
	assert.Equal(t, model.BookFormatBoth, tui.items[0].bookEvent.Event.FormatPref)

	// saveDecisions writes approved + both.
	require.NoError(t, tui.saveDecisions())
	got, err := d.GetBookReleaseEventWithBook(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.StatusApproved, got.Event.Status)
	assert.Equal(t, model.BookFormatBoth, got.Event.FormatPref)

	// Undo: `u` reverts the expansion back to downloaded + original pref.
	tui.undecideCurrent()
	assert.Equal(t, decisionDownloaded, tui.items[0].decision)
	assert.Equal(t, model.BookFormatEbook, tui.items[0].bookEvent.Event.FormatPref)
}

func TestBookFullyDownloadedNoop(t *testing.T) {
	d := openReviewTestDB(t)
	state := BookFormatProcessedState{Status: model.StatusDownloaded, FormatPref: model.BookFormatBoth, EbookProcessed: true, AudiobookProcessed: true}
	_, book, author, ev := seedReviewBookEvent(t, d, "Full Book", state)

	tui, err := NewReviewTUIWithEvents(nil, nil, []db.EventWithBook{
		{Book: book, Author: author, Event: ev},
	}, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	tui.approveCurrent()
	assert.Equal(t, decisionDownloaded, tui.items[0].decision)
	assert.Equal(t, model.BookFormatBoth, tui.items[0].bookEvent.Event.FormatPref)
}

func TestConfirmContentShowsDownloaded(t *testing.T) {
	d := openReviewTestDB(t)
	id, title := seedReviewVideoEvent(t, d, "Already Have It", model.StatusDownloaded)
	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Status: model.StatusDownloaded}},
	}, nil, nil, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	content := tui.buildConfirmContent()
	assert.Contains(t, content, "Already downloaded")
	assert.Contains(t, content, "Already Have It")
	assert.Contains(t, content, "◉")
}

func TestHasDecisionsIgnoresDownloaded(t *testing.T) {
	d := openReviewTestDB(t)
	id, title := seedReviewVideoEvent(t, d, "Done", model.StatusDownloaded)
	tui, err := NewReviewTUIWithEvents([]db.EventWithTitle{
		{Title: title, Event: &model.ReleaseEvent{ID: id, Status: model.StatusDownloaded}},
	}, nil, nil, d, "none", 2026, 35, "", model.BookFormatBoth)
	require.NoError(t, err)

	assert.False(t, tui.hasDecisions(), "a downloaded item alone is not a pending decision")

	approved, _, pending, downloaded := tui.Counts()
	assert.Equal(t, 0, approved)
	assert.Equal(t, 0, pending)
	assert.Equal(t, 1, downloaded)
}
