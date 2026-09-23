package process

import (
	"encoding/json"
	"time"
)

// cachedCollectionPart is one movie in a cached tmdb-collection entry.
// ReleaseDate is present on caches written after the collection-gap
// unreleased filter was added; older caches carry Year only.
type cachedCollectionPart struct {
	TmdbID      int    `json:"tmdb_id"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	ReleaseDate string `json:"release_date"`
}

// parseCachedCollection decodes a tmdb-collection library_cache details blob.
func parseCachedCollection(details string) (name string, parts []cachedCollectionPart) {
	var data struct {
		Name   string                 `json:"name"`
		Movies []cachedCollectionPart `json:"movies"`
	}
	if json.Unmarshal([]byte(details), &data) != nil {
		return "", nil
	}
	return data.Name, data.Movies
}

// collectionPartReleased reports whether a cached collection part looks
// released yet. This mirrors the Radarr collection status filter
// (status == "released") for the cache-fallback path, which has no status
// field: entries with no date (e.g. "Untitled Avatar: The Last Airbender
// Film 2", year 0) or a future date (e.g. "Evil Dead Wrath", 2028) are not
// released. Caches that predate release_date storage fall back to the year.
func collectionPartReleased(p cachedCollectionPart, now time.Time) bool {
	if p.ReleaseDate != "" {
		if d, err := time.Parse("2006-01-02", p.ReleaseDate); err == nil {
			y, m, dd := d.Date()
			ny, nm, nd := now.Date()
			releaseDay := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
			today := time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC)
			return !releaseDay.After(today)
		}
		return false
	}
	if p.Year <= 0 || p.Year > now.Year() {
		return false
	}
	return true
}
