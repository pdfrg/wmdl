package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestUpsertAlbumRelease(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	r := &model.AlbumRelease{
		ArtistName: "Radiohead",
		Title:      "OK Computer",
		Year:       1997,
		MBID:       "mbid-okc",
		AlbumType:  model.AlbumTypeLP,
	}

	id, err := d.UpsertAlbumRelease(ctx, r)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := d.GetAlbumReleaseByMBID(ctx, "mbid-okc")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "OK Computer", got.Title)
	assert.Equal(t, model.AlbumTypeLP, got.AlbumType)
}

func TestUpsertAlbumReleaseByAOTYURL(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	r := &model.AlbumRelease{
		ArtistName: "Artist",
		Title:      "Album",
		Year:       2025,
		AOTYURL:    "https://albumoftheyear.org/album/123",
	}

	id, err := d.UpsertAlbumRelease(ctx, r)
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseByAOTYURL(ctx, "https://albumoftheyear.org/album/123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)

	notFound, err := d.GetAlbumReleaseByAOTYURL(ctx, "https://example.com/none")
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestUpsertAlbumReleaseByAllMusicURL(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	r := &model.AlbumRelease{
		ArtistName:  "Artist",
		Title:       "Album",
		Year:        2025,
		AllMusicURL: "https://allmusic.com/album/test",
	}

	id, err := d.UpsertAlbumRelease(ctx, r)
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseByAllMusicURL(ctx, "https://allmusic.com/album/test")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
}

func TestCreateAlbumReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	releaseID := seedAlbumRelease(t, d, ctx)

	e := &model.AlbumReleaseEvent{
		ReleaseID: releaseID,
		Source:    "albumoftheyear",
		Status:    model.StatusPending,
		ISOYear:   2025,
		ISOWeek:   22,
	}

	id, err := d.CreateAlbumReleaseEvent(ctx, e)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))
}

func TestGetLatestAlbumReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	e1 := &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "aoty", Status: model.StatusPending}
	d.CreateAlbumReleaseEvent(ctx, e1)
	e2 := &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "allmusic", Status: model.StatusApproved}
	e2ID, err := d.CreateAlbumReleaseEvent(ctx, e2)
	require.NoError(t, err)

	latest, err := d.GetLatestAlbumReleaseEvent(ctx, releaseID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, e2ID, latest.ID)

	noEvents, err := d.GetLatestAlbumReleaseEvent(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, noEvents)
}

func TestUpdateAlbumReleaseEventStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	e := &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "aoty", Status: model.StatusPending}
	eID, err := d.CreateAlbumReleaseEvent(ctx, e)
	require.NoError(t, err)

	err = d.UpdateAlbumReleaseEventStatus(ctx, eID, model.StatusApproved)
	require.NoError(t, err)

	latest, err := d.GetLatestAlbumReleaseEvent(ctx, releaseID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusApproved, latest.Status)
}

func TestListPendingAlbumReleaseEvents(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "a", Status: model.StatusPending})
	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "b", Status: model.StatusApproved})

	events, err := d.ListPendingAlbumReleaseEvents(ctx)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "a", events[0].Event.Source)
}

func TestListAlbumReleaseEventsByWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "a", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 22})

	events, err := d.ListAlbumReleaseEventsByWeek(ctx, 2025, 22)
	require.NoError(t, err)
	require.Len(t, events, 1)

	events2, err := d.ListAlbumReleaseEventsByWeek(ctx, 2025, 23)
	require.NoError(t, err)
	assert.Empty(t, events2)
}

func TestListAlbumReleaseEventsByWeekAndStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "a", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 22})
	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "b", Status: model.StatusApproved, ISOYear: 2025, ISOWeek: 22})

	events, err := d.ListAlbumReleaseEventsByWeekAndStatus(ctx, 2025, 22, model.StatusPending)
	require.NoError(t, err)
	require.Len(t, events, 1)

	events2, err := d.ListAlbumReleaseEventsByWeekAndStatus(ctx, 2025, 22)
	require.NoError(t, err)
	require.Len(t, events2, 2)
}

func TestGetAlbumReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	e := &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "aoty", Status: model.StatusPending}
	eID, err := d.CreateAlbumReleaseEvent(ctx, e)
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseEvent(ctx, eID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, releaseID, got.Event.ReleaseID)

	missing, err := d.GetAlbumReleaseEvent(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestCountAlbumReleaseEventsByWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	releaseID := seedAlbumRelease(t, d, ctx)

	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "a", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 22})
	d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: releaseID, Source: "b", Status: model.StatusApproved, ISOYear: 2025, ISOWeek: 22})

	count, err := d.CountAlbumReleaseEventsByWeek(ctx, 2025, 22)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func seedAlbumRelease(t *testing.T, d *DB, ctx context.Context) int64 {
	t.Helper()
	id, err := d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName: "Test Artist",
		Title:      "Test Album",
		Year:       2025,
	})
	require.NoError(t, err)
	return id
}
