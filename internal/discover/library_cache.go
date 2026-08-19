package discover

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) cacheLibraryData(ctx context.Context) error {
	if r.radarrRes == nil && r.sonarrRes == nil && r.lidarrRes == nil && r.bookClient == nil {
		return nil
	}
	r.log.Info().Msg("persisting library cache from background fetches")

	var sonarrSeries []library.SonarrSeries

	if r.sonarrRes != nil {
		r.log.Info().Msg("waiting for Sonarr library...")
		series, err := r.sonarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("background Sonarr fetch failed, retrying synchronously...")
			series, err = r.sonarr.GetAllSeries(ctx)
			if err != nil {
				r.log.Warn().Err(err).Msg("failed to fetch Sonarr library")
			}
		}
		if err == nil {
			sonarrSeries = series
			var entries []db.LibraryCache
			for _, s := range series {
				details, err := json.Marshal(s)
				if err != nil {
					r.log.Warn().Err(err).Int("tvdb", s.TVDBID).Msg("marshaling Sonarr series for cache")
					continue
				}
				entries = append(entries, db.LibraryCache{
					Source: "sonarr", ExtID: strconv.Itoa(s.TVDBID),
					ArrID: int64(s.ID), ArrTitle: s.Title, Details: string(details),
				})
			}
			if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
				r.log.Warn().Err(err).Msg("failed to save Sonarr library cache")
			} else {
				r.log.Info().Int("count", len(series)).Msg("cached Sonarr library")
			}
		}
	}

	if r.radarrRes != nil {
		r.log.Info().Msg("waiting for Radarr library...")
		movies, err := r.radarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("background Radarr fetch failed, retrying synchronously...")
			movies, err = r.radarr.GetAllMovies(ctx)
			if err != nil {
				r.log.Warn().Err(err).Msg("failed to fetch Radarr library")
			}
		}
		if err == nil {
			var entries []db.LibraryCache
			for _, m := range movies {
				details, err := json.Marshal(m)
				if err != nil {
					r.log.Warn().Err(err).Int("tmdb", m.TMDBID).Msg("marshaling Radarr movie for cache")
					continue
				}
				entries = append(entries, db.LibraryCache{
					Source: "radarr", ExtID: strconv.Itoa(m.TMDBID),
					ArrID: int64(m.ID), ArrTitle: m.Title, Details: string(details),
				})
			}
			if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
				r.log.Warn().Err(err).Msg("failed to save Radarr library cache")
			} else {
				r.log.Info().Int("count", len(movies)).Msg("cached Radarr library")
			}
		}
	}

	if r.lidarrRes != nil {
		r.log.Info().Msg("waiting for Lidarr library...")
		result, err := r.lidarrRes.wait(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("background Lidarr fetch failed, retrying synchronously...")
			artists, aErr := r.lidarr.GetAllArtists(ctx)
			albums, alErr := r.lidarr.GetAllAlbums(ctx)
			if aErr != nil {
				r.log.Warn().Err(aErr).Msg("failed to fetch Lidarr artists")
			} else if alErr != nil {
				r.log.Warn().Err(alErr).Msg("failed to fetch Lidarr albums")
			} else {
				result = struct {
					artists []library.LidarrArtist
					albums  []library.LidarrAlbum
				}{artists, albums}
				err = nil
			}
		}
		if err == nil {
			{
				var entries []db.LibraryCache
				for _, a := range result.artists {
					details, err := json.Marshal(a)
					if err != nil {
						r.log.Warn().Err(err).Str("artist", a.ArtistName).Msg("marshaling Lidarr artist for cache")
						continue
					}
					entries = append(entries, db.LibraryCache{
						Source: "lidarr", ExtID: a.MusicBrainzID(),
						ArrID: int64(a.ID), ArrTitle: a.ArtistName, Details: string(details),
					})
				}
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save Lidarr artist cache")
				} else {
					r.log.Info().Int("count", len(result.artists)).Msg("cached Lidarr artists")
				}
			}
			{
				var entries []db.LibraryCache
				for _, a := range result.albums {
					details, err := json.Marshal(a)
					if err != nil {
						r.log.Warn().Err(err).Str("album", a.Title).Msg("marshaling Lidarr album for cache")
						continue
					}
					entries = append(entries, db.LibraryCache{
						Source: "lidarr-album", ExtID: a.ForeignAlbumID,
						ArrID: int64(a.ID), ArrTitle: a.Title, Details: string(details),
					})
				}
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save Lidarr album cache")
				} else {
					r.log.Info().Int("count", len(result.albums)).Msg("cached Lidarr albums")
				}
			}
		}
	}

	wantBooks := len(r.mediaTypeFilters) == 0 || r.mediaTypeFilters[model.MediaTypeBook]

	if r.bookClient != nil && wantBooks {
		books, err := r.bookClient.GetAllBooks(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to fetch LazyLibrarian library")
		} else {
			var entries []db.LibraryCache
			for _, b := range books {
				bookID, _ := strconv.Atoi(b.BookID)
				details, err := json.Marshal(b)
				if err != nil {
					r.log.Warn().Err(err).Str("book", b.Title).Msg("marshaling book for cache")
					continue
				}
				if b.Isbn != "" {
					entries = append(entries, db.LibraryCache{
						Source: "book-client", ExtID: b.Isbn,
						ArrID: int64(bookID), ArrTitle: b.Title, Details: string(details),
					})
				}
			}
			if len(entries) > 0 {
				if err := r.db.BulkUpsertLibraryCache(ctx, entries); err != nil {
					r.log.Warn().Err(err).Msg("failed to save LazyLibrarian library cache")
				} else {
					r.log.Info().Int("count", len(books)).Msg("cached LazyLibrarian library")
				}
			}
			r.bookClient.SetAllBooks(books)
		}
	}

	if len(sonarrSeries) > 0 {
		titles, err := r.db.ListTitles(ctx)
		if err != nil {
			r.log.Warn().Err(err).Msg("failed to list titles for anime matching")
			return nil
		}
		for _, t := range titles {
			if t.MediaType != model.MediaTypeAnime || t.TvdbID != 0 || t.MalID == 0 {
				continue
			}
			matchTitle := normalizeAnimeTitle(t.Title)
			for _, s := range sonarrSeries {
				seriesTitle := normalizeAnimeTitle(s.Title)
				if matchTitle == seriesTitle || strings.HasPrefix(seriesTitle, matchTitle) || strings.HasPrefix(matchTitle, seriesTitle) {
					r.log.Info().Str("anime", t.Title).Int("tvdb_id", s.TVDBID).Str("sonarr_title", s.Title).Msg("matched anime to Sonarr series")
					if err := r.db.UpdateTitleTvdbID(ctx, t.ID, s.TVDBID); err != nil {
						r.log.Warn().Err(err).Msg("failed to update anime tvdb_id")
						continue
					}
					t.TvdbID = s.TVDBID
					details, err := json.Marshal(s)
					if err != nil {
						r.log.Warn().Err(err).Int("tvdb", s.TVDBID).Msg("marshaling matched anime for cache")
						break
					}
					if err := r.db.UpsertLibraryCache(ctx, "sonarr", strconv.Itoa(s.TVDBID), int64(s.ID), s.Title, string(details)); err != nil {
						r.log.Warn().Err(err).Msg("failed to upsert sonarr cache for matched anime")
					}
					break
				}
			}
		}
	}

	return nil
}
