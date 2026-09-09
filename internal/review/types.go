package review

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

type decision int

const (
	decisionNone decision = iota
	decisionApproved
	decisionRejected
	// decisionDownloaded marks an item that is already downloaded. It is
	// preserved across re-review (never downgraded to approved by accident),
	// unless the user explicitly rejects it or expands a book's formats.
	decisionDownloaded
)

func decisionForStatus(s model.ReleaseStatus) decision {
	switch s {
	case model.StatusApproved:
		return decisionApproved
	case model.StatusDownloaded:
		return decisionDownloaded
	case model.StatusRejected:
		return decisionRejected
	default:
		return decisionNone
	}
}

type phase int

const (
	phaseReview phase = iota
	phaseConfirm
)

type itemState struct {
	event      db.EventWithTitle         // for movies/TV
	albumEvent *db.EventWithAlbumRelease // for music (nil for movies/TV)
	bookEvent  *db.EventWithBook         // for books (nil for movies/TV/music)
	decision   decision

	// Book format expansion: when a downloaded book had only one format
	// processed, pressing `a` expands FormatPref to "both" and re-approves it
	// so the missing format is searched. origFormatPref lets `r`/`u` undo.
	origFormatPref     model.BookFormat
	bookFormatExpanded bool
}

// isDownloaded reports whether the item's pre-review DB status is downloaded.
// The in-memory status is never mutated, so this stays true even after the user
// changes the decision (used to decide which transitions are allowed).
func (it *itemState) isDownloaded() bool {
	switch {
	case it.bookEvent != nil:
		return it.bookEvent.Event.Status == model.StatusDownloaded
	case it.albumEvent != nil:
		return it.albumEvent.Event.Status == model.StatusDownloaded
	default:
		return it.event.Event.Status == model.StatusDownloaded
	}
}

func (it *itemState) mediaType() model.MediaType {
	if it.bookEvent != nil {
		return model.MediaTypeBook
	}
	if it.albumEvent != nil {
		return model.MediaTypeMusic
	}
	return it.event.Title.MediaType
}

func (it *itemState) displayTitle() string {
	if it.bookEvent != nil {
		return fmt.Sprintf("%s — %s", it.bookEvent.Author.Name, it.bookEvent.Book.Title)
	}
	if it.albumEvent != nil {
		return fmt.Sprintf("%s - %s", it.albumEvent.Release.ArtistName, it.albumEvent.Release.Title)
	}
	return it.event.Title.Title
}

type libStatus int

const (
	libNone    libStatus = iota
	libFull              // green — all files present
	libPartial           // yellow — some files missing or 0 files
)

type libInfo struct {
	label  string
	status libStatus
}

func (it *itemState) libraryInfo(dbCache map[string]*db.LibraryCache) libInfo {
	if it.bookEvent != nil {
		be := it.bookEvent
		var c *db.LibraryCache
		if be.Book.HardcoverID > 0 {
			c = dbCache["book-client-hc:"+strconv.Itoa(be.Book.HardcoverID)]
		}
		if c == nil && be.Book.ISBN13 != "" {
			c = dbCache["book-client:"+be.Book.ISBN13]
		}
		if c != nil {
			return libInfo{
				label:  fmt.Sprintf("✓ LazyLibrarian — %s", c.ArrTitle),
				status: libFull,
			}
		}
		return libInfo{status: libNone}
	}
	if it.albumEvent != nil {
		artistKey := "lidarr:" + it.albumEvent.Release.ArtistMBID
		albumKey := "lidarr-album:" + it.albumEvent.Release.MBID
		artistCache := dbCache[artistKey]
		albumCache := dbCache[albumKey]
		if artistCache != nil && albumCache != nil {
			label := fmt.Sprintf("✓ Lidarr — %s [album in library]", it.albumEvent.Release.ArtistName)
			status := libFull
			// Only claim the album is present when Lidarr reports actual
			// track files; unmonitored/listed-only albums have none.
			if st := parseLidarrAlbumStats(albumCache); st == nil || st.TrackFileCount == 0 {
				label = fmt.Sprintf("⚠ Lidarr — %s [in Lidarr, not downloaded]", it.albumEvent.Release.ArtistName)
				status = libPartial
			}
			return libInfo{label: label, status: status}
		}
		if artistCache != nil {
			return libInfo{
				label:  fmt.Sprintf("⚠ Lidarr — %s [artist in library, album not found]", it.albumEvent.Release.ArtistName),
				status: libPartial,
			}
		}
		return libInfo{status: libNone}
	}

	tl := it.event.Title
	if tl == nil {
		return libInfo{status: libNone}
	}
	if tl.TmdbID > 0 {
		if c := dbCache["radarr:"+strconv.Itoa(tl.TmdbID)]; c != nil {
			return libInfo{
				label:  fmt.Sprintf("✓ Radarr — %s", c.ArrTitle),
				status: libFull,
			}
		}
	}
	if tl.TvdbID > 0 {
		if c := dbCache["sonarr:"+strconv.Itoa(tl.TvdbID)]; c != nil {
			label := fmt.Sprintf("✓ Sonarr — %s", c.ArrTitle)
			status := libFull
			var series struct {
				Seasons []struct {
					SeasonNumber int `json:"seasonNumber"`
					Statistics   *struct {
						EpisodeFileCount  int    `json:"episodeFileCount"`
						EpisodeCount      int    `json:"episodeCount"`
						TotalEpisodeCount int    `json:"totalEpisodeCount"`
						NextAiring        string `json:"nextAiring,omitempty"`
					} `json:"statistics,omitempty"`
				} `json:"seasons"`
			}
			if err := json.Unmarshal([]byte(c.Details), &series); err == nil && len(series.Seasons) > 0 {
				var complete, partial []string
				anyMissing := false
				for _, s := range series.Seasons {
					if s.SeasonNumber == 0 {
						continue
					}
					if s.Statistics == nil || s.Statistics.TotalEpisodeCount == 0 {
						continue
					}
					total := s.Statistics.TotalEpisodeCount
					aired := s.Statistics.EpisodeCount
					if aired < 0 {
						aired = 0
					}
					if aired > total {
						aired = total
					}
					unaired := total - aired
					files := s.Statistics.EpisodeFileCount
					if files >= total {
						complete = append(complete, strconv.Itoa(s.SeasonNumber))
					} else {
						if unaired > 0 {
							partial = append(partial, fmt.Sprintf("S%d(%d/%d aired, %d unaired)", s.SeasonNumber, files, aired, unaired))
						} else {
							partial = append(partial, fmt.Sprintf("S%d(%d/%d)", s.SeasonNumber, files, total))
						}
						switch {
						case aired <= 0:
							// Nothing aired yet: green only when the premiere is
							// confirmed in the future. Stale caches predate
							// nextAiring capture and stay conservative (yellow).
							if !seasonPremierePending(s.Statistics.NextAiring) {
								anyMissing = true
							}
						case files < aired:
							anyMissing = true
						}
					}
				}
				if anyMissing {
					status = libPartial
				}
				if len(complete) > 0 || len(partial) > 0 {
					var parts []string
					if len(complete) > 0 {
						parts = append(parts, "S"+strings.Join(complete, ","))
					}
					parts = append(parts, partial...)
					label += " [" + strings.Join(parts, " ") + "]"
				} else if len(series.Seasons) > 0 {
					status = libPartial
				}
			}
			return libInfo{label: label, status: status}
		}
	}
	return libInfo{status: libNone}
}

// seasonPremierePending reports whether a season with no aired episodes yet has
// its premiere in the future. An empty or unparseable nextAiring (e.g. caches
// written before nextAiring capture) returns false so callers stay conservative
// and treat the season as missing, as before.
func seasonPremierePending(nextAiring string) bool {
	if nextAiring == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, nextAiring)
	if err != nil {
		return false
	}
	return t.After(time.Now())
}

// parseLidarrAlbumStats extracts download statistics from a cached Lidarr
// album. Returns nil when the cache entry predates statistics capture, so
// callers can treat it as "in Lidarr, download status unknown".
func parseLidarrAlbumStats(c *db.LibraryCache) *lidarrAlbumStats {
	if c == nil || c.Details == "" {
		return nil
	}
	var raw struct {
		Statistics *struct {
			TrackFileCount int `json:"trackFileCount"`
		} `json:"statistics"`
	}
	if err := json.Unmarshal([]byte(c.Details), &raw); err != nil || raw.Statistics == nil {
		return nil
	}
	return &lidarrAlbumStats{TrackFileCount: raw.Statistics.TrackFileCount}
}

type lidarrAlbumStats struct {
	TrackFileCount int
}

func (it *itemState) collectionStr(dbCache map[string]*db.LibraryCache) string {
	if it.event.Title == nil || it.event.Title.MediaType != model.MediaTypeMovie || it.event.Title.CollectionID == 0 {
		return ""
	}
	key := "tmdb-collection:" + strconv.Itoa(it.event.Title.CollectionID)
	c := dbCache[key]
	if c == nil || c.Details == "" {
		return ""
	}
	var collData struct {
		Name   string `json:"name"`
		Movies []struct {
			TmdbID int    `json:"tmdb_id"`
			Title  string `json:"title"`
		} `json:"movies"`
	}
	if err := json.Unmarshal([]byte(c.Details), &collData); err != nil {
		return ""
	}
	if len(collData.Movies) == 0 {
		return ""
	}
	owned := 0
	for _, m := range collData.Movies {
		if dbCache["radarr:"+strconv.Itoa(m.TmdbID)] != nil {
			owned++
		}
	}
	return fmt.Sprintf("Collection: %s (%d/%d)", collData.Name, owned, len(collData.Movies))
}

func (it *itemState) bookSeriesStr(dbCache map[string]*db.LibraryCache) string {
	if it.bookEvent == nil || it.bookEvent.Book.SeriesID == "" {
		return ""
	}
	seriesID := it.bookEvent.Book.SeriesID
	seriesName := it.bookEvent.Book.SeriesName
	key := "book-series:" + seriesID
	c := dbCache[key]
	if c == nil || c.Details == "" {
		return ""
	}
	var members []db.BookSeriesMember
	if err := json.Unmarshal([]byte(c.Details), &members); err != nil {
		return ""
	}
	if len(members) == 0 {
		return ""
	}
	owned := 0
	for _, m := range members {
		if dbCache["book-client-hc:"+strconv.Itoa(m.HardcoverID)] != nil {
			owned++
		}
	}
	label := seriesName
	if label == "" {
		label = "Series"
	}
	return fmt.Sprintf("Series: %s (%d/%d)", label, owned, len(members))
}

type posterReadyMsg struct {
	img image.Image
	err error
}

func buildLibraryCacheMap(database *db.DB, events []db.EventWithTitle, albumEvents []db.EventWithAlbumRelease, bookEvents []db.EventWithBook) map[string]*db.LibraryCache {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var lookups []struct{ Source, ExtID string }
	seen := make(map[string]bool)
	for _, e := range events {
		if e.Title.TmdbID > 0 {
			key := "radarr:" + strconv.Itoa(e.Title.TmdbID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"radarr", strconv.Itoa(e.Title.TmdbID)})
				seen[key] = true
			}
		}
		if e.Title.TvdbID > 0 {
			key := "sonarr:" + strconv.Itoa(e.Title.TvdbID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"sonarr", strconv.Itoa(e.Title.TvdbID)})
				seen[key] = true
			}
		}
	}
	for _, ae := range albumEvents {
		if ae.Release.ArtistMBID != "" {
			key := "lidarr:" + ae.Release.ArtistMBID
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"lidarr", ae.Release.ArtistMBID})
				seen[key] = true
			}
		}
		if ae.Release.MBID != "" {
			key := "lidarr-album:" + ae.Release.MBID
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"lidarr-album", ae.Release.MBID})
				seen[key] = true
			}
		}
	}
	for _, be := range bookEvents {
		if be.Book.ISBN13 != "" {
			key := "book-client:" + be.Book.ISBN13
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client", be.Book.ISBN13})
				seen[key] = true
			}
		}
		if be.Book.ASIN != "" {
			key := "book-client:" + be.Book.ASIN
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client", be.Book.ASIN})
				seen[key] = true
			}
		}
		if be.Book.HardcoverID > 0 {
			key := "book-client-hc:" + strconv.Itoa(be.Book.HardcoverID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(be.Book.HardcoverID)})
				seen[key] = true
			}
		}
	}
	result, err := database.GetLibraryCacheMap(ctx, lookups)
	if err != nil {
		return nil
	}

	var collLookups []struct{ Source, ExtID string }
	for _, e := range events {
		if e.Title.CollectionID > 0 {
			key := "tmdb-collection:" + strconv.Itoa(e.Title.CollectionID)
			if !seen[key] {
				collLookups = append(collLookups, struct{ Source, ExtID string }{"tmdb-collection", strconv.Itoa(e.Title.CollectionID)})
				seen[key] = true
			}
		}
	}
	if len(collLookups) > 0 {
		collResult, err := database.GetLibraryCacheMap(ctx, collLookups)
		if err == nil {
			for k, v := range collResult {
				result[k] = v
			}
			var memberLookups []struct{ Source, ExtID string }
			for _, e := range events {
				if e.Title.CollectionID == 0 {
					continue
				}
				ck := "tmdb-collection:" + strconv.Itoa(e.Title.CollectionID)
				c := collResult[ck]
				if c == nil || c.Details == "" {
					continue
				}
				var collData struct {
					Movies []struct {
						TmdbID int    `json:"tmdb_id"`
						Title  string `json:"title"`
						Year   int    `json:"year"`
					} `json:"movies"`
				}
				if err := json.Unmarshal([]byte(c.Details), &collData); err != nil {
					continue
				}
				for _, m := range collData.Movies {
					rk := "radarr:" + strconv.Itoa(m.TmdbID)
					if !seen[rk] {
						memberLookups = append(memberLookups, struct{ Source, ExtID string }{"radarr", strconv.Itoa(m.TmdbID)})
						seen[rk] = true
					}
				}
			}
			if len(memberLookups) > 0 {
				memberResult, err := database.GetLibraryCacheMap(ctx, memberLookups)
				if err == nil {
					for k, v := range memberResult {
						result[k] = v
					}
				}
			}
		}
	}

	var bookSeriesLookups []struct{ Source, ExtID string }
	for _, be := range bookEvents {
		if be.Book.SeriesID != "" {
			key := "book-series:" + be.Book.SeriesID
			if !seen[key] {
				bookSeriesLookups = append(bookSeriesLookups, struct{ Source, ExtID string }{"book-series", be.Book.SeriesID})
				seen[key] = true
			}
		}
	}
	if len(bookSeriesLookups) > 0 {
		seriesResult, err := database.GetLibraryCacheMap(ctx, bookSeriesLookups)
		if err != nil {
			seriesResult = nil
		} else {
			for k, v := range seriesResult {
				result[k] = v
			}
		}

		var memberLookups []struct{ Source, ExtID string }

		if seriesResult != nil {
			for _, be := range bookEvents {
				if be.Book.SeriesID == "" {
					continue
				}
				sk := "book-series:" + be.Book.SeriesID
				if seriesResult[sk] != nil && seriesResult[sk].Details != "" {
					var members []db.BookSeriesMember
					if err := json.Unmarshal([]byte(seriesResult[sk].Details), &members); err == nil {
						for _, m := range members {
							mk := "book-client-hc:" + strconv.Itoa(m.HardcoverID)
							if !seen[mk] {
								memberLookups = append(memberLookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(m.HardcoverID)})
								seen[mk] = true
							}
						}
					}
				}
			}
		}

		for _, be := range bookEvents {
			if be.Book.SeriesID == "" {
				continue
			}
			sk := "book-series:" + be.Book.SeriesID
			if result[sk] != nil {
				continue
			}
			members, qErr := database.GetBookSeriesMembers(ctx, be.Book.SeriesID)
			if qErr != nil || len(members) == 0 {
				continue
			}
			mJSON, mErr := json.Marshal(members)
			if mErr == nil {
				result[sk] = &db.LibraryCache{
					Source:  "book-series",
					ExtID:   be.Book.SeriesID,
					Details: string(mJSON),
				}
			}
			for _, m := range members {
				mk := "book-client-hc:" + strconv.Itoa(m.HardcoverID)
				if !seen[mk] {
					memberLookups = append(memberLookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(m.HardcoverID)})
					seen[mk] = true
				}
			}
		}

		if len(memberLookups) > 0 {
			memberResult, err := database.GetLibraryCacheMap(ctx, memberLookups)
			if err == nil {
				for k, v := range memberResult {
					result[k] = v
				}
			}
		}
	}

	return result
}
