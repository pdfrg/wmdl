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

func TestUpsertAlbumRelease_ConflictKeepsEnrichment(t *testing.T) {
	// AOTY stores the album with scores + URL; a later rpcharts-style upsert
	// matching on (artist, title, year) must not wipe that data.
	d := openTestDB(t)
	ctx := context.Background()

	aoty := &model.AlbumRelease{
		ArtistName:      "The The",
		Title:           "Ensoulment",
		Year:            2024,
		MBID:            "mbid-ens",
		AOTYURL:         "https://albumoftheyear.org/album/1",
		AOTYCriticScore: 82,
		AOTYCriticCount: 12,
		AOTYUserScore:   78,
		AOTYUserCount:   1500,
		AOTYMustHear:    true,
		Genres:          "Alternative Rock",
	}
	id, err := d.UpsertAlbumRelease(ctx, aoty)
	require.NoError(t, err)

	rpcharts := &model.AlbumRelease{
		ArtistName:  "The The",
		Title:       "Ensoulment",
		Year:        2024,
		ReleaseDate: "2026-07-30",
		PosterPath:  "https://img.radioparadise.com/covers/l/26434.jpg",
	}
	id2, err := d.UpsertAlbumRelease(ctx, rpcharts)
	require.NoError(t, err)
	assert.Equal(t, id, id2)

	got, err := d.GetAlbumReleaseByAOTYURL(ctx, "https://albumoftheyear.org/album/1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "mbid-ens", got.MBID)
	assert.Equal(t, 82.0, got.AOTYCriticScore)
	assert.Equal(t, 12, got.AOTYCriticCount)
	assert.Equal(t, 78.0, got.AOTYUserScore)
	assert.Equal(t, 1500, got.AOTYUserCount)
	assert.True(t, got.AOTYMustHear)
	assert.Equal(t, "Alternative Rock", got.Genres)
	assert.Equal(t, "2026-07-30", got.ReleaseDate)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26434.jpg", got.PosterPath)
}

func TestUpsertAlbumRelease_ConflictFillsScores(t *testing.T) {
	// rpcharts-first ordering: a later AOTY upsert fills in the gaps.
	d := openTestDB(t)
	ctx := context.Background()

	_, err := d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName:  "Local Natives",
		Title:       "Curio",
		Year:        2026,
		ReleaseDate: "2026-07-28",
		PosterPath:  "https://img.radioparadise.com/covers/l/26501.jpg",
	})
	require.NoError(t, err)

	_, err = d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName:      "Local Natives",
		Title:           "Curio",
		Year:            2026,
		MBID:            "mbid-curio",
		AOTYURL:         "https://albumoftheyear.org/album/2",
		AOTYCriticScore: 88,
		AOTYCriticCount: 20,
		Genres:          "Indie Rock",
	})
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseByAOTYURL(ctx, "https://albumoftheyear.org/album/2")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "mbid-curio", got.MBID)
	assert.Equal(t, 88.0, got.AOTYCriticScore)
	assert.Equal(t, 20, got.AOTYCriticCount)
	assert.Equal(t, "Indie Rock", got.Genres)
	assert.Equal(t, "2026-07-28", got.ReleaseDate)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26501.jpg", got.PosterPath)
}

func TestUpsertAlbumRelease_MBIDPathKeepsEnrichment(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	_, err := d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName:      "Artist",
		Title:           "Album",
		Year:            2025,
		MBID:            "mbid-keep",
		AOTYURL:         "https://albumoftheyear.org/album/3",
		AOTYCriticScore: 91,
		AOTYCriticCount: 30,
		Genres:          "Rock",
	})
	require.NoError(t, err)

	// Same MBID, empty enrichment (e.g. a source whose MB/AOTY lookup failed).
	_, err = d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName: "Artist",
		Title:      "Album",
		Year:       2025,
		MBID:       "mbid-keep",
		PosterPath: "https://img/new.jpg",
	})
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseByMBID(ctx, "mbid-keep")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://albumoftheyear.org/album/3", got.AOTYURL)
	assert.Equal(t, 91.0, got.AOTYCriticScore)
	assert.Equal(t, 30, got.AOTYCriticCount)
	assert.Equal(t, "Rock", got.Genres)
	assert.Equal(t, "https://img/new.jpg", got.PosterPath)
}

func TestUpsertAlbumRelease_AllMusicPreservesAOTY(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	_, err := d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName:      "Artist",
		Title:           "Album",
		Year:            2025,
		AOTYURL:         "https://albumoftheyear.org/album/4",
		AOTYCriticScore: 85,
		AOTYCriticCount: 15,
	})
	require.NoError(t, err)

	_, err = d.UpsertAlbumRelease(ctx, &model.AlbumRelease{
		ArtistName:     "Artist",
		Title:          "Album",
		Year:           2025,
		AllMusicURL:    "https://allmusic.com/album/4",
		AllMusicRating: 8,
	})
	require.NoError(t, err)

	got, err := d.GetAlbumReleaseByAllMusicURL(ctx, "https://allmusic.com/album/4")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://albumoftheyear.org/album/4", got.AOTYURL)
	assert.Equal(t, 85.0, got.AOTYCriticScore)
	assert.Equal(t, 15, got.AOTYCriticCount)
	assert.Equal(t, 8.0, got.AllMusicRating)
}
