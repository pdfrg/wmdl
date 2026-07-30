package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetAndGetSetting(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	err := d.SetSetting(ctx, "theme", "dark")
	require.NoError(t, err)

	val, err := d.GetSetting(ctx, "theme")
	require.NoError(t, err)
	assert.Equal(t, "dark", val)
}

func TestGetSettingNotFound(t *testing.T) {
	d := openTestDB(t)
	val, err := d.GetSetting(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, val)
}

func TestSetSettingOverwrites(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.SetSetting(ctx, "key", "old")
	d.SetSetting(ctx, "key", "new")

	val, err := d.GetSetting(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, "new", val)
}

func TestUpsertLibraryCache(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	err := d.UpsertLibraryCache(ctx, "radarr", "ext-1", 42, "Movie", `{"quality": "HD"}`)
	require.NoError(t, err)

	c, err := d.GetLibraryCache(ctx, "radarr", "ext-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, "radarr", c.Source)
	assert.Equal(t, "ext-1", c.ExtID)
	assert.Equal(t, int64(42), c.ArrID)
	assert.Equal(t, "Movie", c.ArrTitle)
}

func TestGetLibraryCacheNotFound(t *testing.T) {
	d := openTestDB(t)
	c, err := d.GetLibraryCache(context.Background(), "radarr", "no-such")
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestUpsertLibraryCacheOverwrites(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertLibraryCache(ctx, "sonarr", "ext-1", 1, "Old", "")
	d.UpsertLibraryCache(ctx, "sonarr", "ext-1", 2, "New", "{}")

	c, err := d.GetLibraryCache(ctx, "sonarr", "ext-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, int64(2), c.ArrID)
	assert.Equal(t, "New", c.ArrTitle)
}

func TestBulkUpsertLibraryCache(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	entries := []LibraryCache{
		{Source: "radarr", ExtID: "e1", ArrID: 1, ArrTitle: "M1"},
		{Source: "sonarr", ExtID: "e2", ArrID: 2, ArrTitle: "S1"},
	}

	err := d.BulkUpsertLibraryCache(ctx, entries)
	require.NoError(t, err)

	c1, err := d.GetLibraryCache(ctx, "radarr", "e1")
	require.NoError(t, err)
	require.NotNil(t, c1)
	assert.Equal(t, "M1", c1.ArrTitle)
}

func TestBulkUpsertEmpty(t *testing.T) {
	d := openTestDB(t)
	err := d.BulkUpsertLibraryCache(context.Background(), nil)
	require.NoError(t, err)
}

func TestGetLibraryCacheMap(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertLibraryCache(ctx, "radarr", "e1", 1, "Movie", "")

	lookups := []struct{ Source, ExtID string }{
		{Source: "radarr", ExtID: "e1"},
		{Source: "radarr", ExtID: "e2"},
	}

	result, err := d.GetLibraryCacheMap(ctx, lookups)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Contains(t, result, "radarr:e1")

	empty, err := d.GetLibraryCacheMap(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, empty)
}

func TestPurgeLibraryCache(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertLibraryCache(ctx, "radarr", "e1", 1, "M", "")
	d.UpsertLibraryCache(ctx, "sonarr", "e2", 2, "S", "")

	err := d.PurgeLibraryCache(ctx)
	require.NoError(t, err)

	all, err := d.GetAllLibraryCache(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestGetAllLibraryCache(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertLibraryCache(ctx, "radarr", "a", 1, "A", "")
	d.UpsertLibraryCache(ctx, "sonarr", "b", 2, "B", "")

	all, err := d.GetAllLibraryCache(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestGetLibraryCacheFetchedAt(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertLibraryCache(ctx, "radarr", "e1", 1, "M", "")

	tm, err := d.GetLibraryCacheFetchedAt(ctx, "radarr")
	require.NoError(t, err)
	assert.NotEmpty(t, tm)

	empty, err := d.GetLibraryCacheFetchedAt(ctx, "no-source")
	require.NoError(t, err)
	assert.Empty(t, empty)
}
