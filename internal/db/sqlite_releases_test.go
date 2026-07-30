package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestCreateReleaseEventTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test Movie")

	var eventID int64
	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		id, err := d.CreateReleaseEventTx(ctx, tx, &model.ReleaseEvent{
			TitleID: tmdbID, Source: "test", Status: model.StatusPending})
		if err != nil {
			return err
		}
		eventID = id
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, eventID, int64(0))
}

func TestGetLatestReleaseEventTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eventID := seedEvent(t, d, ctx, tmdbID)

	var got *model.ReleaseEvent
	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = d.GetLatestReleaseEventTx(ctx, tx, tmdbID)
		return err
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, eventID, got.ID)
}

func TestUpdateReleaseEventStatusTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eventID := seedEvent(t, d, ctx, tmdbID)

	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		return d.UpdateReleaseEventStatusTx(ctx, tx, eventID, model.StatusApproved)
	})
	require.NoError(t, err)

	events, err := d.ListReleaseEvents(ctx, model.StatusApproved)
	require.NoError(t, err)
	require.Len(t, events, 1)
}

func TestGetDownloadByTitleIDTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eventID := seedEvent(t, d, ctx, tmdbID)
	dlID := seedDownload(t, d, ctx, tmdbID, eventID)

	var got *model.Download
	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = d.GetDownloadByTitleIDTx(ctx, tx, tmdbID)
		return err
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dlID, got.ID)
}

func TestGetReleaseEventWithTitle(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Event Title Movie")
	eventID := seedEvent(t, d, ctx, tmdbID)

	ewt, err := d.GetReleaseEventWithTitle(ctx, eventID)
	require.NoError(t, err)
	require.NotNil(t, ewt)
	assert.Equal(t, "Event Title Movie", ewt.Title.Title)
	assert.Equal(t, eventID, ewt.Event.ID)

	missing, err := d.GetReleaseEventWithTitle(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestCreateDownloadTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eventID := seedEvent(t, d, ctx, tmdbID)

	dl := &model.Download{
		TitleID:        tmdbID,
		ReleaseEventID: eventID,
		Quality:        "1080p",
		SourceType:     "web-dl",
		Codec:          "x265",
		InfoHash:       "abc123",
		Status:         model.DownloadAdded,
	}

	id, err := d.CreateDownload(ctx, dl)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := d.GetDownloadByTitleID(ctx, tmdbID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "1080p", got.Quality)
}

func TestUpdateDownloadStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eventID := seedEvent(t, d, ctx, tmdbID)
	dlID := seedDownload(t, d, ctx, tmdbID, eventID)

	err := d.UpdateDownloadStatus(ctx, dlID, model.DownloadComplete)
	require.NoError(t, err)

	got, err := d.GetDownloadByTitleID(ctx, tmdbID)
	require.NoError(t, err)
	assert.Equal(t, model.DownloadComplete, got.Status)
}

func TestGetWeekProcessCounts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")
	eID := seedEvent(t, d, ctx, tmdbID)

	d.UpdateReleaseEventStatus(ctx, eID, model.StatusDownloaded)
	d.db.ExecContext(ctx, `UPDATE release_events SET iso_year = 2025, iso_week = 22 WHERE id = ?`, eID)

	dl, app, err := d.GetWeekProcessCounts(ctx, 2025, 22)
	require.NoError(t, err)
	assert.Equal(t, 1, dl)
	assert.Equal(t, 1, app)
}

func TestCountReleaseEventsByWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")

	seedEventWithWeek(t, d, ctx, tmdbID, 2025, 22)
	seedEventWithWeek(t, d, ctx, tmdbID, 2025, 22)
	seedEventWithWeek(t, d, ctx, tmdbID, 2025, 23)

	count22, err := d.CountReleaseEventsByWeek(ctx, 2025, 22)
	require.NoError(t, err)
	assert.Equal(t, 2, count22)

	count23, err := d.CountReleaseEventsByWeek(ctx, 2025, 23)
	require.NoError(t, err)
	assert.Equal(t, 1, count23)
}

func TestPendingEventCountsBySource(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")

	seedEventWithWeekAndSource(t, d, ctx, tmdbID, 2025, 22, "flixpatrol", model.StatusPending)
	seedEventWithWeekAndSource(t, d, ctx, tmdbID, 2025, 22, "flixpatrol", model.StatusPending)
	seedEventWithWeekAndSource(t, d, ctx, tmdbID, 2025, 22, "dvdsreleasedates", model.StatusPending)
	seedEventWithWeekAndSource(t, d, ctx, tmdbID, 2025, 22, "flixpatrol", model.StatusApproved)

	counts, err := d.PendingEventCountsBySource(ctx, 2025, 22)
	require.NoError(t, err)
	assert.Equal(t, 2, counts["flixpatrol movie"])
	assert.Equal(t, 1, counts["dvdsreleasedates"])
}

func TestGetTitleByMalID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{
		TmdbID:    100,
		MalID:     42,
		Title:     "Anime Title",
		Year:      2025,
		MediaType: model.MediaTypeAnime,
	}
	id, err := d.UpsertTitle(ctx, t1)
	require.NoError(t, err)

	got, err := d.GetTitleByMalID(ctx, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "Anime Title", got.Title)

	notFound, err := d.GetTitleByMalID(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestUpdateTitleTvdbID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	tmdbID := seedTitle(t, d, ctx, "Test")

	err := d.UpdateTitleTvdbID(ctx, tmdbID, 12345)
	require.NoError(t, err)

	got, err := d.GetTitleByTmdbID(ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 12345, got.TvdbID)
}

func TestUpsertTitleTx(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var id int64
	err := d.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = d.UpsertTitleTx(ctx, tx, &model.Title{
			TmdbID:    200,
			Title:     "Tx Movie",
			Year:      2025,
			MediaType: model.MediaTypeMovie,
		})
		return err
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestGetPreviousAnimeWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{TmdbID: 1, Title: "Anime A", Year: 2025, MediaType: model.MediaTypeAnime, MalID: 1}
	tid1, err := d.UpsertTitle(ctx, t1)
	require.NoError(t, err)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{TitleID: tid1, Source: "test", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 23})

	t2 := &model.Title{TmdbID: 2, Title: "Anime B", Year: 2025, MediaType: model.MediaTypeAnime, MalID: 2}
	tid2, err := d.UpsertTitle(ctx, t2)
	require.NoError(t, err)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{TitleID: tid2, Source: "test", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 22})

	y, w, err := d.GetPreviousAnimeWeek(ctx, 2025, 24)
	require.NoError(t, err)
	assert.Equal(t, 2025, y)
	assert.Equal(t, 23, w)

	// No anime events before 2024-W01
	noYear, noWeek, err := d.GetPreviousAnimeWeek(ctx, 2024, 1)
	require.NoError(t, err)
	assert.Equal(t, 0, noYear)
	assert.Equal(t, 0, noWeek)
}

// Helpers

func seedTitle(t *testing.T, d *DB, ctx context.Context, title string) int64 {
	t.Helper()
	nextID := int(0)
	d.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tmdb_id), 99) + 1 FROM titles`).Scan(&nextID)
	id, err := d.UpsertTitle(ctx, &model.Title{
		TmdbID:    nextID,
		Title:     title,
		Year:      2025,
		MediaType: model.MediaTypeMovie,
	})
	require.NoError(t, err)
	return id
}

func seedEvent(t *testing.T, d *DB, ctx context.Context, titleID int64) int64 {
	t.Helper()
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusPending,
	})
	require.NoError(t, err)
	return id
}

func seedEventWithWeek(t *testing.T, d *DB, ctx context.Context, titleID int64, year, week int) int64 {
	t.Helper()
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusPending, ISOYear: year, ISOWeek: week,
	})
	require.NoError(t, err)
	return id
}

func seedEventWithWeekAndSource(t *testing.T, d *DB, ctx context.Context, titleID int64, year, week int, source string, status model.ReleaseStatus) int64 {
	t.Helper()
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: source, Status: status, ISOYear: year, ISOWeek: week,
	})
	require.NoError(t, err)
	return id
}

func seedDownload(t *testing.T, d *DB, ctx context.Context, titleID, eventID int64) int64 {
	t.Helper()
	id, err := d.CreateDownload(ctx, &model.Download{
		TitleID: titleID, ReleaseEventID: eventID, Quality: "1080p", Status: model.DownloadAdded,
	})
	require.NoError(t, err)
	return id
}
