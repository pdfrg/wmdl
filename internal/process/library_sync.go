package process

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
)

func (e *Executor) PreWarmRadarr(ctx context.Context) {
	if e.SkipRadarr() {
		return
	}
	if e.tryLoadRadarrCache(ctx) {
		return
	}
	movies, err := e.radarr.GetAllMovies(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Radarr")
		return
	}
	e.updateRadarrCache(ctx, movies)
}

func (e *Executor) tryLoadRadarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "radarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var movies []library.RadarrMovie
	for _, c := range caches {
		if c.Source != "radarr" {
			continue
		}
		var m library.RadarrMovie
		if err := json.Unmarshal([]byte(c.Details), &m); err != nil {
			continue
		}
		movies = append(movies, m)
	}
	if len(movies) == 0 {
		return false
	}
	e.radarr.SetAllMovies(movies)
	return true
}

func (e *Executor) updateRadarrCache(ctx context.Context, movies []library.RadarrMovie) {
	var entries []db.LibraryCache
	for _, m := range movies {
		details, err := json.Marshal(m)
		if err != nil {
			e.log.Warn().Err(err).Int("tmdb", m.TMDBID).Msg("marshaling Radarr movie for cache")
			continue
		}
		entries = append(entries, db.LibraryCache{
			Source: "radarr", ExtID: strconv.Itoa(m.TMDBID),
			ArrID: int64(m.ID), ArrTitle: m.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Radarr cache")
	}
}

func (e *Executor) PreWarmSonarr(ctx context.Context) {
	if e.SkipSonarr() {
		return
	}
	if e.tryLoadSonarrCache(ctx) {
		return
	}
	series, err := e.sonarr.GetAllSeries(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Sonarr")
		return
	}
	e.updateSonarrCache(ctx, series)
}

func (e *Executor) tryLoadSonarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "sonarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var series []library.SonarrSeries
	for _, c := range caches {
		if c.Source != "sonarr" {
			continue
		}
		var s library.SonarrSeries
		if err := json.Unmarshal([]byte(c.Details), &s); err != nil {
			continue
		}
		series = append(series, s)
	}
	if len(series) == 0 {
		return false
	}
	e.sonarr.SetAllSeries(series)
	return true
}

func (e *Executor) updateSonarrCache(ctx context.Context, series []library.SonarrSeries) {
	var entries []db.LibraryCache
	for _, s := range series {
		details, err := json.Marshal(s)
		if err != nil {
			e.log.Warn().Err(err).Int("tvdb", s.TVDBID).Msg("marshaling Sonarr series for cache")
			continue
		}
		entries = append(entries, db.LibraryCache{
			Source: "sonarr", ExtID: strconv.Itoa(s.TVDBID),
			ArrID: int64(s.ID), ArrTitle: s.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Sonarr cache")
	}
}

func (e *Executor) PreWarmLidarr(ctx context.Context) {
	if e.lidarr == nil || e.SkipLidarr() {
		return
	}
	if e.tryLoadLidarrCache(ctx) {
		return
	}
	artists, err := e.lidarr.GetAllArtists(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Lidarr artists")
		return
	}
	albums, err := e.lidarr.GetAllAlbums(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm Lidarr albums")
		return
	}
	e.updateLidarrCache(ctx, artists, albums)
}

func (e *Executor) tryLoadLidarrCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "lidarr")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var artists []library.LidarrArtist
	var albums []library.LidarrAlbum
	for _, c := range caches {
		switch c.Source {
		case "lidarr":
			var a library.LidarrArtist
			if err := json.Unmarshal([]byte(c.Details), &a); err != nil {
				continue
			}
			artists = append(artists, a)
		case "lidarr-album":
			var a library.LidarrAlbum
			if err := json.Unmarshal([]byte(c.Details), &a); err != nil {
				continue
			}
			albums = append(albums, a)
		}
	}
	if len(artists) == 0 {
		return false
	}
	e.lidarr.SetAllArtists(artists)
	e.lidarr.SetAllAlbums(albums)
	return true
}

func (e *Executor) updateLidarrCache(ctx context.Context, artists []library.LidarrArtist, albums []library.LidarrAlbum) {
	var entries []db.LibraryCache
	for _, a := range artists {
		details, err := json.Marshal(a)
		if err != nil {
			e.log.Warn().Err(err).Str("artist", a.ArtistName).Msg("marshaling Lidarr artist for cache")
			continue
		}
		entries = append(entries, db.LibraryCache{
			Source: "lidarr", ExtID: a.MBID,
			ArrID: int64(a.ID), ArrTitle: a.ArtistName, Details: string(details),
		})
	}
	for _, a := range albums {
		details, err := json.Marshal(a)
		if err != nil {
			e.log.Warn().Err(err).Str("album", a.Title).Msg("marshaling Lidarr album for cache")
			continue
		}
		entries = append(entries, db.LibraryCache{
			Source: "lidarr-album", ExtID: a.ForeignAlbumID,
			ArrID: int64(a.ID), ArrTitle: a.Title, Details: string(details),
		})
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving Lidarr cache")
	}
}

func (e *Executor) PreWarmBookClient(ctx context.Context) {
	if e.bookClient == nil || e.SkipBook() {
		return
	}
	if e.tryLoadBookClientCache(ctx) {
		return
	}
	books, err := e.bookClient.GetAllBooks(ctx)
	if err != nil {
		e.log.Warn().Err(err).Msg("pre-warm LazyLibrarian")
		return
	}
	e.updateBookClientCache(ctx, books)
}

func (e *Executor) tryLoadBookClientCache(ctx context.Context) bool {
	fetchedAt, err := e.db.GetLibraryCacheFetchedAt(ctx, "book-client")
	if err != nil || fetchedAt == "" {
		return false
	}
	fetched, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return false
	}
	ttl := time.Duration(e.cfg.CacheTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	if time.Since(fetched) > ttl {
		return false
	}
	caches, err := e.db.GetAllLibraryCache(ctx)
	if err != nil {
		return false
	}
	var books []model.BookStatus
	for _, c := range caches {
		if c.Source != "book-client" {
			continue
		}
		var b model.BookStatus
		if err := json.Unmarshal([]byte(c.Details), &b); err != nil {
			continue
		}
		found := false
		for _, existing := range books {
			if existing.BookID == b.BookID {
				found = true
				break
			}
		}
		if !found {
			books = append(books, b)
		}
	}
	if len(books) == 0 {
		return false
	}
	e.bookClient.SetAllBooks(books)
	return true
}

func (e *Executor) updateBookClientCache(ctx context.Context, books []model.BookStatus) {
	var entries []db.LibraryCache
	for _, b := range books {
		bookID, _ := strconv.Atoi(b.BookID)
		details, err := json.Marshal(b)
		if err != nil {
			e.log.Warn().Err(err).Str("book", b.Title).Msg("marshaling book for cache")
			continue
		}
		if b.Isbn != "" {
			entries = append(entries, db.LibraryCache{
				Source: "book-client", ExtID: b.Isbn,
				ArrID: int64(bookID), ArrTitle: b.Title, Details: string(details),
			})
		}
		if b.BookID != "" {
			entries = append(entries, db.LibraryCache{
				Source: "book-client-hc", ExtID: b.BookID,
				ArrID: int64(bookID), ArrTitle: b.Title, Details: string(details),
			})
		}
	}
	if err := e.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
		e.log.Warn().Err(err).Msg("saving LazyLibrarian cache")
	}
	e.bookClient.SetAllBooks(books)
}
