package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestUpsertAuthor(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	a := &model.Author{
		Name:   "Stephen King",
		OLID:   "OL21567A",
		Bio:    "American author",
		BornDate: "1947-09-21",
	}

	id, err := d.UpsertAuthor(ctx, a)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := d.GetAuthorByOLID(ctx, "OL21567A")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Stephen King", got.Name)

	got2, err := d.GetAuthorByName(ctx, "Stephen King")
	require.NoError(t, err)
	require.NotNil(t, got2)
	assert.Equal(t, id, got2.ID)
}

func TestUpsertAuthorWithoutOLID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	a := &model.Author{Name: "Unknown Author"}
	id, err := d.UpsertAuthor(ctx, a)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := d.GetAuthorByName(ctx, "Unknown Author")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Contains(t, got.OLID, "_nm_")
}

func TestUpsertAuthorDuplicateByName(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	a1 := &model.Author{Name: "Same Name", OLID: "OL1"}
	id1, err := d.UpsertAuthor(ctx, a1)
	require.NoError(t, err)

	a2 := &model.Author{Name: "Same Name", OLID: "OL2"}
	id2, err := d.UpsertAuthor(ctx, a2)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "should update existing author by name")
}

func TestGetAuthorByOLIDNotFound(t *testing.T) {
	d := openTestDB(t)
	got, err := d.GetAuthorByOLID(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetAuthorByNameNotFound(t *testing.T) {
	d := openTestDB(t)
	got, err := d.GetAuthorByName(context.Background(), "No One")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestUpsertBook(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	authorID := seedAuthor(t, d, ctx, "George Orwell")

	b := &model.Book{
		AuthorID:    authorID,
		Title:       "1984",
		ISBN13:      "9780451524935",
		Pages:       328,
		ReleaseYear: 1949,
		Language:    "en",
		Publisher:   "Secker & Warburg",
	}

	id, err := d.UpsertBook(ctx, b)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := d.GetBookByISBN(ctx, "9780451524935")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "1984", got.Title)
	assert.Equal(t, authorID, got.AuthorID)
}

func TestUpsertBookByHardcoverID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	authorID := seedAuthor(t, d, ctx, "J.R.R. Tolkien")
	book := &model.Book{
		AuthorID:    authorID,
		Title:       "The Hobbit",
		HardcoverID: 42,
		ISBN13:      "9780547928227",
	}
	id, err := d.UpsertBook(ctx, book)
	require.NoError(t, err)

	got, err := d.getBookByHardcoverID(ctx, d.db, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)

	notFound, err := d.getBookByHardcoverID(ctx, d.db, 999)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestUpsertBookUpdatesExisting(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	authorID := seedAuthor(t, d, ctx, "Author")
	b1 := &model.Book{AuthorID: authorID, Title: "Old Title", ISBN13: "9780000000001"}
	id1, err := d.UpsertBook(ctx, b1)
	require.NoError(t, err)

	b2 := &model.Book{AuthorID: authorID, Title: "Updated Title", ISBN13: "9780000000001"}
	id2, err := d.UpsertBook(ctx, b2)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)

	got, err := d.GetBookByISBN(ctx, "9780000000001")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Updated Title", got.Title)
}

func TestGetBookByISBNNotFound(t *testing.T) {
	d := openTestDB(t)
	got, err := d.GetBookByISBN(context.Background(), "9780000000000")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestCreateBookReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test Book")

	e := &model.BookReleaseEvent{
		BookID:      bookID,
		Source:      "goodreads",
		ReleaseDate: "2025-06-01",
		FormatPref:  model.BookFormatEbook,
		Status:      model.StatusPending,
		ISOYear:     2025,
		ISOWeek:     22,
	}

	id, err := d.CreateBookReleaseEvent(ctx, e)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestGetLatestBookReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test Book")

	e1 := &model.BookReleaseEvent{BookID: bookID, Source: "goodreads", Status: model.StatusPending, FormatPref: model.BookFormatBoth}
	_, err := d.CreateBookReleaseEvent(ctx, e1)
	require.NoError(t, err)

	e2 := &model.BookReleaseEvent{BookID: bookID, Source: "bookshop", Status: model.StatusApproved, FormatPref: model.BookFormatBoth}
	id2, err := d.CreateBookReleaseEvent(ctx, e2)
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, id2, latest.ID)
	assert.Equal(t, "bookshop", latest.Source)

	noEvents, err := d.GetLatestBookReleaseEvent(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, noEvents)
}

func TestUpdateBookReleaseEventStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test")
	eID := seedBookEvent(t, d, ctx, bookID)

	err := d.UpdateBookReleaseEventStatus(ctx, eID, model.StatusApproved)
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusApproved, latest.Status)
}

func TestMarkBookFormatProcessed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test")
	eID := seedBookEvent(t, d, ctx, bookID)

	err := d.MarkBookFormatProcessed(ctx, eID, model.BookFormatEbook)
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.True(t, latest.EbookProcessed)
	assert.False(t, latest.AudiobookProcessed)

	err = d.MarkBookFormatProcessed(ctx, eID, model.BookFormatAudiobook)
	require.NoError(t, err)

	latest, err = d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	assert.True(t, latest.AudiobookProcessed)
}

func TestRequeueBookReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test")
	eID := seedBookEvent(t, d, ctx, bookID)

	d.UpdateBookReleaseEventStatus(ctx, eID, model.StatusDownloaded)
	err := d.RequeueBookReleaseEvent(ctx, eID, "retry", "re-queue")
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusPending, latest.Status)
	assert.Equal(t, "re-queue", latest.Notes)
}

func TestUpdateBookReleaseEventSourceAndNotes(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Test")
	eID := seedBookEvent(t, d, ctx, bookID)

	err := d.UpdateBookReleaseEventSourceAndNotes(ctx, eID, "bookshop,goodreads", "merged")
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	assert.Equal(t, "bookshop,goodreads", latest.Source)
	assert.Equal(t, "merged", latest.Notes)
}

func TestListPendingBookEventsWithBooks(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Pending Author")
	bookID := seedBook(t, d, ctx, authorID, "Pending Book")
	seedBookEvent(t, d, ctx, bookID)

	// Second event that should not appear (not pending)
	bookID2 := seedBook(t, d, ctx, authorID, "Approved Book")
	e2 := &model.BookReleaseEvent{BookID: bookID2, Source: "goodreads", Status: model.StatusApproved, FormatPref: model.BookFormatBoth}
	d.CreateBookReleaseEvent(ctx, e2)

	events, err := d.ListPendingBookEventsWithBooks(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Pending Book", events[0].Book.Title)
	assert.Equal(t, "Pending Author", events[0].Author.Name)
}

func TestListBookEventsByWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Week Book")
	e := &model.BookReleaseEvent{BookID: bookID, Source: "goodreads", Status: model.StatusPending, FormatPref: model.BookFormatBoth, ISOYear: 2025, ISOWeek: 22}
	d.CreateBookReleaseEvent(ctx, e)

	events, err := d.ListBookEventsByWeek(ctx, 2025, 22)
	require.NoError(t, err)
	require.Len(t, events, 1)

	events2, err := d.ListBookEventsByWeek(ctx, 2025, 23)
	require.NoError(t, err)
	assert.Empty(t, events2)
}

func TestListBookEventsByWeekAndStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Status Book")

	d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{BookID: bookID, Source: "a", Status: model.StatusPending, FormatPref: model.BookFormatBoth, ISOYear: 2025, ISOWeek: 22})
	d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{BookID: bookID, Source: "b", Status: model.StatusApproved, FormatPref: model.BookFormatBoth, ISOYear: 2025, ISOWeek: 22})

	events, err := d.ListBookEventsByWeekAndStatus(ctx, 2025, 22, model.StatusPending)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "a", events[0].Event.Source)

	events2, err := d.ListBookEventsByWeekAndStatus(ctx, 2025, 22)
	require.NoError(t, err)
	require.Len(t, events2, 2)
}

func TestGetBookReleaseEventWithBook(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Event Author")
	bookID := seedBook(t, d, ctx, authorID, "Event Book")
	eID := seedBookEvent(t, d, ctx, bookID)

	ewb, err := d.GetBookReleaseEventWithBook(ctx, eID)
	require.NoError(t, err)
	require.NotNil(t, ewb)
	assert.Equal(t, "Event Book", ewb.Book.Title)
	assert.Equal(t, "Event Author", ewb.Author.Name)

	missing, err := d.GetBookReleaseEventWithBook(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestCountBookReleaseEventsByWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Count Book")

	d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{BookID: bookID, Source: "a", Status: model.StatusPending, FormatPref: model.BookFormatBoth, ISOYear: 2025, ISOWeek: 22})
	d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{BookID: bookID, Source: "b", Status: model.StatusApproved, FormatPref: model.BookFormatBoth, ISOYear: 2025, ISOWeek: 22})

	count, err := d.CountBookReleaseEventsByWeek(ctx, 2025, 22)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count2, err := d.CountBookReleaseEventsByWeek(ctx, 2025, 23)
	require.NoError(t, err)
	assert.Equal(t, 0, count2)
}

func TestCreateBookDownload(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "DL Book")
	eID := seedBookEvent(t, d, ctx, bookID)

	dl := &model.BookDownload{
		BookID:           bookID,
		BookReleaseEvent: eID,
		Format:           model.BookFormatEbook,
		Quality:          "epub",
		InfoHash:         "abc123",
		Status:           model.DownloadAdded,
	}

	id, err := d.CreateBookDownload(ctx, dl)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestDeleteBookReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Delete Book")
	eID := seedBookEvent(t, d, ctx, bookID)

	err := d.DeleteBookReleaseEvent(ctx, eID)
	require.NoError(t, err)

	latest, err := d.GetLatestBookReleaseEvent(ctx, bookID)
	require.NoError(t, err)
	assert.Nil(t, latest)
}

func TestDeleteBook(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Author")
	bookID := seedBook(t, d, ctx, authorID, "Delete Book")

	err := d.DeleteBook(ctx, bookID)
	require.NoError(t, err)

	got, err := d.GetBookByISBN(ctx, "9780000000001")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetBookSeriesMembers(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Series Author")

	b1 := &model.Book{AuthorID: authorID, Title: "Book One", HardcoverID: 1, SeriesID: "series-1"}
	b2 := &model.Book{AuthorID: authorID, Title: "Book Two", HardcoverID: 2, SeriesID: "series-1"}
	_, err := d.UpsertBook(ctx, b1)
	require.NoError(t, err)
	_, err = d.UpsertBook(ctx, b2)
	require.NoError(t, err)

	members, err := d.GetBookSeriesMembers(ctx, "series-1")
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, "Book One", members[0].Title)
	assert.Equal(t, "Book Two", members[1].Title)
}

func TestBookReleaseEventTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	authorID := seedAuthor(t, d, ctx, "Tx Author")
	bookID := seedBook(t, d, ctx, authorID, "Tx Book")

	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		e := &model.BookReleaseEvent{BookID: bookID, Source: "goodreads", Status: model.StatusPending, FormatPref: model.BookFormatBoth}
		eID, err := d.CreateBookReleaseEventTx(ctx, tx, e)
		require.NoError(t, err)

		err = d.UpdateBookReleaseEventSourceAndNotesTx(ctx, tx, eID, "source", "notes")
		require.NoError(t, err)

		err = d.DeleteBookReleaseEventTx(ctx, tx, eID)
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)
}

// Helpers for book tests

func seedAuthor(t *testing.T, d *DB, ctx context.Context, name string) int64 {
	t.Helper()
	id, err := d.UpsertAuthor(ctx, &model.Author{Name: name})
	require.NoError(t, err)
	return id
}

var nextBookISBN int64 = 9780000000001

func seedBook(t *testing.T, d *DB, ctx context.Context, authorID int64, title string) int64 {
	t.Helper()
	nextBookISBN++
	id, err := d.UpsertBook(ctx, &model.Book{AuthorID: authorID, Title: title, ISBN13: fmt.Sprintf("%d", nextBookISBN)})
	require.NoError(t, err)
	return id
}

func seedBookEvent(t *testing.T, d *DB, ctx context.Context, bookID int64) int64 {
	t.Helper()
	id, err := d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{
		BookID: bookID, Source: "goodreads", Status: model.StatusPending, FormatPref: model.BookFormatBoth,
	})
	require.NoError(t, err)
	return id
}
