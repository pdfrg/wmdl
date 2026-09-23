package process

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCollectionPartReleased(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		part cachedCollectionPart
		want bool
	}{
		{"released past date", cachedCollectionPart{Title: "Evil Dead Rise", ReleaseDate: "2023-04-12"}, true},
		{"released today", cachedCollectionPart{Title: "X", ReleaseDate: "2026-09-23"}, true},
		{"future date skipped (Evil Dead Wrath 2028)", cachedCollectionPart{Title: "Evil Dead Wrath", Year: 2028, ReleaseDate: "2028-04-05"}, false},
		{"future digital skipped", cachedCollectionPart{Title: "PAW Patrol", ReleaseDate: "2026-09-29"}, false},
		{"empty date skipped (untitled Avatar film 2)", cachedCollectionPart{Title: "Untitled Avatar: The Last Airbender Film 2", Year: 0}, false},
		{"year zero skipped", cachedCollectionPart{Title: "Unknown", Year: 0}, false},
		{"future year skipped (stale cache)", cachedCollectionPart{Title: "Wrath", Year: 2028}, false},
		{"past year kept (stale cache)", cachedCollectionPart{Title: "Rise", Year: 2023}, true},
		{"current year kept (stale cache)", cachedCollectionPart{Title: "Burn", Year: 2026}, true},
		{"unparseable date skipped", cachedCollectionPart{Title: "X", ReleaseDate: "not-a-date"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, collectionPartReleased(tt.part, now))
		})
	}
}

func TestParseCachedCollection(t *testing.T) {
	// New format with release dates.
	name, parts := parseCachedCollection(`{"name":"Evil Dead Standalone Collection","movies":[{"tmdb_id":713704,"title":"Evil Dead Rise","year":2023,"release_date":"2023-04-12"},{"tmdb_id":1280608,"title":"Evil Dead Wrath","year":2028,"release_date":"2028-04-05"}]}`)
	assert.Equal(t, "Evil Dead Standalone Collection", name)
	assert.Len(t, parts, 2)
	assert.Equal(t, "2028-04-05", parts[1].ReleaseDate)

	// Old format without release dates still parses (year fallback applies).
	_, oldParts := parseCachedCollection(`{"name":"Avatar Aang: The Last Airbender Collection","movies":[{"tmdb_id":980431,"title":"Avatar Aang: The Last Airbender","year":2026},{"tmdb_id":1127274,"title":"Untitled Avatar: The Last Airbender Film 2","year":0}]}`)
	assert.Len(t, oldParts, 2)
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	assert.True(t, collectionPartReleased(oldParts[0], now))
	assert.False(t, collectionPartReleased(oldParts[1], now))

	// Garbage returns nil parts.
	_, bad := parseCachedCollection(`not json`)
	assert.Nil(t, bad)
}
