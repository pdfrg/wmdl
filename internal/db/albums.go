package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
)

type EventWithAlbumRelease struct {
	Event   *model.AlbumReleaseEvent
	Release *model.AlbumRelease
}

// mergeAlbumRelease fills gaps in `incoming` from `existing` so that a
// less-complete source (e.g. rpcharts, which carries no AOTY/AllMusic scores
// or URLs) never wipes data already stored by another source. Rule: the
// incoming value wins only when it carries a real value (non-empty string or
// non-zero number); otherwise the existing value is kept. Both albums are
// assumed to identify the same release.
func mergeAlbumRelease(existing, incoming *model.AlbumRelease) *model.AlbumRelease {
	if existing == nil {
		return incoming
	}
	merged := *existing

	merged.ArtistName = firstNonEmpty(incoming.ArtistName, existing.ArtistName)
	merged.Title = firstNonEmpty(incoming.Title, existing.Title)
	if incoming.Year != 0 {
		merged.Year = incoming.Year
	}
	merged.MBID = firstNonEmpty(incoming.MBID, existing.MBID)
	merged.ArtistMBID = firstNonEmpty(incoming.ArtistMBID, existing.ArtistMBID)
	merged.AlbumType = model.AlbumType(firstNonEmpty(string(incoming.AlbumType), string(existing.AlbumType)))
	merged.ReleaseDate = firstNonEmpty(incoming.ReleaseDate, existing.ReleaseDate)
	merged.Genres = firstNonEmpty(incoming.Genres, existing.Genres)
	merged.Overview = firstNonEmpty(incoming.Overview, existing.Overview)
	merged.PosterPath = firstNonEmpty(incoming.PosterPath, existing.PosterPath)
	merged.AOTYURL = firstNonEmpty(incoming.AOTYURL, existing.AOTYURL)
	merged.AllMusicURL = firstNonEmpty(incoming.AllMusicURL, existing.AllMusicURL)

	if incoming.AOTYCriticScore != 0 {
		merged.AOTYCriticScore = incoming.AOTYCriticScore
	}
	if incoming.AOTYCriticCount != 0 {
		merged.AOTYCriticCount = incoming.AOTYCriticCount
	}
	if incoming.AOTYUserScore != 0 {
		merged.AOTYUserScore = incoming.AOTYUserScore
	}
	if incoming.AOTYUserCount != 0 {
		merged.AOTYUserCount = incoming.AOTYUserCount
	}
	if incoming.AOTYMustHear {
		merged.AOTYMustHear = true
	}
	if incoming.AllMusicRating != 0 {
		merged.AllMusicRating = incoming.AllMusicRating
	}
	if incoming.MBRating != 0 {
		merged.MBRating = incoming.MBRating
	}

	return &merged
}

func firstNonEmpty(newVal, oldVal string) string {
	if newVal != "" {
		return newVal
	}
	return oldVal
}

func (d *DB) CountAlbumReleaseEventsByWeek(ctx context.Context, year, week int) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM album_release_events
		WHERE iso_year = ? AND iso_week = ?
	`, year, week).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting album release events: %w", err)
	}
	return count, nil
}

func (d *DB) UpsertAlbumRelease(ctx context.Context, r *model.AlbumRelease) (int64, error) {
	if r.MBID != "" {
		existing, err := d.GetAlbumReleaseByMBID(ctx, r.MBID)
		if err != nil {
			return 0, fmt.Errorf("checking existing release by mbid: %w", err)
		}
		if existing != nil {
			merged := mergeAlbumRelease(existing, r)
			_, err := d.db.ExecContext(ctx, `
				UPDATE album_releases SET
					artist_name = ?, title = ?, year = ?, artist_mbid = ?,
					album_type = ?, release_date = ?, genres = ?, overview = ?, poster_path = ?,
					aoty_url = ?, allmusic_url = ?,
					aoty_critic_score = ?, aoty_critic_count = ?,
					aoty_user_score = ?, aoty_user_count = ?,
					aoty_must_hear = ?, allmusic_rating = ?, mb_rating = ?
				WHERE id = ?
			`, merged.ArtistName, merged.Title, merged.Year, merged.ArtistMBID,
				string(merged.AlbumType), merged.ReleaseDate, merged.Genres, merged.Overview, merged.PosterPath,
				merged.AOTYURL, merged.AllMusicURL,
				merged.AOTYCriticScore, merged.AOTYCriticCount,
				merged.AOTYUserScore, merged.AOTYUserCount,
				boolToInt(merged.AOTYMustHear), merged.AllMusicRating, merged.MBRating,
				existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing release by mbid: %w", err)
			}
			return existing.ID, nil
		}
	}

	if r.AOTYURL != "" {
		existing, err := d.GetAlbumReleaseByAOTYURL(ctx, r.AOTYURL)
		if err != nil {
			return 0, fmt.Errorf("checking existing release by aoty_url: %w", err)
		}
		if existing != nil {
			merged := mergeAlbumRelease(existing, r)
			_, err := d.db.ExecContext(ctx, `
				UPDATE album_releases SET
					artist_name = ?, title = ?, year = ?, mbid = ?, artist_mbid = ?,
					album_type = ?, release_date = ?, genres = ?, overview = ?, poster_path = ?,
					allmusic_url = ?,
					aoty_critic_score = ?, aoty_critic_count = ?,
					aoty_user_score = ?, aoty_user_count = ?,
					aoty_must_hear = ?, allmusic_rating = ?, mb_rating = ?
				WHERE id = ?
			`, merged.ArtistName, merged.Title, merged.Year, merged.MBID, merged.ArtistMBID,
				string(merged.AlbumType), merged.ReleaseDate, merged.Genres, merged.Overview, merged.PosterPath,
				merged.AllMusicURL,
				merged.AOTYCriticScore, merged.AOTYCriticCount,
				merged.AOTYUserScore, merged.AOTYUserCount,
				boolToInt(merged.AOTYMustHear), merged.AllMusicRating, merged.MBRating,
				existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing release by aoty_url: %w", err)
			}
			return existing.ID, nil
		}
	}

	if r.AllMusicURL != "" {
		existing, err := d.GetAlbumReleaseByAllMusicURL(ctx, r.AllMusicURL)
		if err != nil {
			return 0, fmt.Errorf("checking existing release by allmusic_url: %w", err)
		}
		if existing != nil {
			merged := mergeAlbumRelease(existing, r)
			_, err := d.db.ExecContext(ctx, `
				UPDATE album_releases SET
					artist_name = ?, title = ?, year = ?, mbid = ?, artist_mbid = ?,
					album_type = ?, release_date = ?, genres = ?, overview = ?, poster_path = ?,
					aoty_url = ?,
					aoty_critic_score = ?, aoty_critic_count = ?,
					aoty_user_score = ?, aoty_user_count = ?,
					aoty_must_hear = ?, allmusic_rating = ?, mb_rating = ?
				WHERE id = ?
			`, merged.ArtistName, merged.Title, merged.Year, merged.MBID, merged.ArtistMBID,
				string(merged.AlbumType), merged.ReleaseDate, merged.Genres, merged.Overview, merged.PosterPath,
				merged.AOTYURL,
				merged.AOTYCriticScore, merged.AOTYCriticCount,
				merged.AOTYUserScore, merged.AOTYUserCount,
				boolToInt(merged.AOTYMustHear), merged.AllMusicRating, merged.MBRating,
				existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing release by allmusic_url: %w", err)
			}
			return existing.ID, nil
		}
	}

	res, err := d.db.ExecContext(ctx, `
		INSERT INTO album_releases (artist_name, title, year, mbid, artist_mbid, album_type,
		                            release_date, genres, overview, poster_path,
		                            aoty_url, allmusic_url,
		                            aoty_critic_score, aoty_critic_count,
		                            aoty_user_score, aoty_user_count,
		                            aoty_must_hear, allmusic_rating, mb_rating, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(artist_name, title, year) DO UPDATE SET
			mbid              = CASE WHEN excluded.mbid != '' THEN excluded.mbid ELSE album_releases.mbid END,
			artist_mbid       = CASE WHEN excluded.artist_mbid != '' THEN excluded.artist_mbid ELSE album_releases.artist_mbid END,
			album_type        = CASE WHEN excluded.album_type != '' THEN excluded.album_type ELSE album_releases.album_type END,
			release_date      = CASE WHEN excluded.release_date != '' THEN excluded.release_date ELSE album_releases.release_date END,
			genres            = CASE WHEN excluded.genres != '' THEN excluded.genres ELSE album_releases.genres END,
			overview          = CASE WHEN excluded.overview != '' THEN excluded.overview ELSE album_releases.overview END,
			poster_path       = CASE WHEN excluded.poster_path != '' THEN excluded.poster_path ELSE album_releases.poster_path END,
			aoty_url          = CASE WHEN excluded.aoty_url != '' THEN excluded.aoty_url ELSE album_releases.aoty_url END,
			allmusic_url      = CASE WHEN excluded.allmusic_url != '' THEN excluded.allmusic_url ELSE album_releases.allmusic_url END,
			aoty_critic_score = CASE WHEN excluded.aoty_critic_score != 0 THEN excluded.aoty_critic_score ELSE album_releases.aoty_critic_score END,
			aoty_critic_count = CASE WHEN excluded.aoty_critic_count != 0 THEN excluded.aoty_critic_count ELSE album_releases.aoty_critic_count END,
			aoty_user_score   = CASE WHEN excluded.aoty_user_score != 0 THEN excluded.aoty_user_score ELSE album_releases.aoty_user_score END,
			aoty_user_count   = CASE WHEN excluded.aoty_user_count != 0 THEN excluded.aoty_user_count ELSE album_releases.aoty_user_count END,
			aoty_must_hear    = CASE WHEN excluded.aoty_must_hear != 0 THEN 1 ELSE album_releases.aoty_must_hear END,
			allmusic_rating   = CASE WHEN excluded.allmusic_rating != 0 THEN excluded.allmusic_rating ELSE album_releases.allmusic_rating END,
			mb_rating         = CASE WHEN excluded.mb_rating != 0 THEN excluded.mb_rating ELSE album_releases.mb_rating END
	`, r.ArtistName, r.Title, r.Year, r.MBID, r.ArtistMBID, string(r.AlbumType),
		r.ReleaseDate, r.Genres, r.Overview, r.PosterPath,
		r.AOTYURL, r.AllMusicURL,
		r.AOTYCriticScore, r.AOTYCriticCount,
		r.AOTYUserScore, r.AOTYUserCount,
		boolToInt(r.AOTYMustHear), r.AllMusicRating, r.MBRating)
	if err != nil {
		return 0, fmt.Errorf("upserting album release: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) GetAlbumReleaseByMBID(ctx context.Context, mbid string) (*model.AlbumRelease, error) {
	if mbid == "" {
		return nil, nil
	}
	var r model.AlbumRelease
	var albumType, createdAt string
	var mustHear int
	err := d.db.QueryRowContext(ctx, `
		SELECT id, artist_name, title, year, mbid, artist_mbid, album_type,
		       release_date, genres, overview, poster_path,
		       aoty_url, allmusic_url,
		       aoty_critic_score, aoty_critic_count,
		       aoty_user_score, aoty_user_count,
		       aoty_must_hear, allmusic_rating, mb_rating, created_at
		FROM album_releases WHERE mbid = ?
	`, mbid).Scan(
		&r.ID, &r.ArtistName, &r.Title, &r.Year, &r.MBID, &r.ArtistMBID, &albumType,
		&r.ReleaseDate, &r.Genres, &r.Overview, &r.PosterPath,
		&r.AOTYURL, &r.AllMusicURL,
		&r.AOTYCriticScore, &r.AOTYCriticCount,
		&r.AOTYUserScore, &r.AOTYUserCount,
		&mustHear, &r.AllMusicRating, &r.MBRating, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying album release by mbid: %w", err)
	}
	r.AlbumType = model.AlbumType(albumType)
	r.AOTYMustHear = mustHear > 0
	r.CreatedAt = createdAt
	return &r, nil
}

func (d *DB) GetAlbumReleaseByAOTYURL(ctx context.Context, url string) (*model.AlbumRelease, error) {
	if url == "" {
		return nil, nil
	}
	var r model.AlbumRelease
	var albumType, createdAt string
	var mustHear int
	err := d.db.QueryRowContext(ctx, `
		SELECT id, artist_name, title, year, mbid, artist_mbid, album_type,
		       release_date, genres, overview, poster_path,
		       aoty_url, allmusic_url,
		       aoty_critic_score, aoty_critic_count,
		       aoty_user_score, aoty_user_count,
		       aoty_must_hear, allmusic_rating, mb_rating, created_at
		FROM album_releases WHERE aoty_url = ?
	`, url).Scan(
		&r.ID, &r.ArtistName, &r.Title, &r.Year, &r.MBID, &r.ArtistMBID, &albumType,
		&r.ReleaseDate, &r.Genres, &r.Overview, &r.PosterPath,
		&r.AOTYURL, &r.AllMusicURL,
		&r.AOTYCriticScore, &r.AOTYCriticCount,
		&r.AOTYUserScore, &r.AOTYUserCount,
		&mustHear, &r.AllMusicRating, &r.MBRating, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying album release by aoty_url: %w", err)
	}
	r.AlbumType = model.AlbumType(albumType)
	r.AOTYMustHear = mustHear > 0
	r.CreatedAt = createdAt
	return &r, nil
}

func (d *DB) GetAlbumReleaseByAllMusicURL(ctx context.Context, url string) (*model.AlbumRelease, error) {
	if url == "" {
		return nil, nil
	}
	var r model.AlbumRelease
	var albumType, createdAt string
	var mustHear int
	err := d.db.QueryRowContext(ctx, `
		SELECT id, artist_name, title, year, mbid, artist_mbid, album_type,
		       release_date, genres, overview, poster_path,
		       aoty_url, allmusic_url,
		       aoty_critic_score, aoty_critic_count,
		       aoty_user_score, aoty_user_count,
		       aoty_must_hear, allmusic_rating, mb_rating, created_at
		FROM album_releases WHERE allmusic_url = ?
	`, url).Scan(
		&r.ID, &r.ArtistName, &r.Title, &r.Year, &r.MBID, &r.ArtistMBID, &albumType,
		&r.ReleaseDate, &r.Genres, &r.Overview, &r.PosterPath,
		&r.AOTYURL, &r.AllMusicURL,
		&r.AOTYCriticScore, &r.AOTYCriticCount,
		&r.AOTYUserScore, &r.AOTYUserCount,
		&mustHear, &r.AllMusicRating, &r.MBRating, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying album release by allmusic_url: %w", err)
	}
	r.AlbumType = model.AlbumType(albumType)
	r.AOTYMustHear = mustHear > 0
	r.CreatedAt = createdAt
	return &r, nil
}

func (d *DB) CreateAlbumReleaseEvent(ctx context.Context, e *model.AlbumReleaseEvent) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO album_release_events (release_id, source, release_date, status, previous_status, notes, iso_year, iso_week)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, e.ReleaseID, e.Source, e.ReleaseDate, string(e.Status), string(e.PreviousStatus), e.Notes, e.ISOYear, e.ISOWeek)
	if err != nil {
		return 0, fmt.Errorf("creating album release event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) GetLatestAlbumReleaseEvent(ctx context.Context, releaseID int64) (*model.AlbumReleaseEvent, error) {
	var ev model.AlbumReleaseEvent
	var status, prevStatus, createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, release_id, source, release_date, status, previous_status, notes, created_at, iso_year, iso_week
		FROM album_release_events WHERE release_id = ?
		ORDER BY id DESC LIMIT 1
	`, releaseID).Scan(
		&ev.ID, &ev.ReleaseID, &ev.Source, &ev.ReleaseDate,
		&status, &prevStatus, &ev.Notes, &createdAt, &ev.ISOYear, &ev.ISOWeek)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying latest album release event: %w", err)
	}
	ev.Status = model.ReleaseStatus(status)
	ev.PreviousStatus = model.ReleaseStatus(prevStatus)
	ev.CreatedAt = createdAt
	return &ev, nil
}

func (d *DB) UpdateAlbumReleaseEventStatus(ctx context.Context, id int64, status model.ReleaseStatus) error {
	return d.updateAlbumReleaseEventStatus(ctx, d.db, id, status)
}

func (d *DB) RequeueAlbumReleaseEvent(ctx context.Context, id int64, source, notes string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE album_release_events SET status = ?, previous_status = status, notes = ?, source = ? WHERE id = ?`,
		string(model.StatusPending), notes, source, id)
	return err
}

func (d *DB) UpdateAlbumReleaseEventStatusTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus) error {
	return d.updateAlbumReleaseEventStatus(ctx, tx, id, status)
}

func (d *DB) updateAlbumReleaseEventStatus(ctx context.Context, q querier, id int64, status model.ReleaseStatus) error {
	_, err := q.ExecContext(ctx, `UPDATE album_release_events SET previous_status = status, status = ? WHERE id = ?`, string(status), id)
	return err
}

func (d *DB) ListPendingAlbumReleaseEvents(ctx context.Context) ([]EventWithAlbumRelease, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.release_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       r.id, r.artist_name, r.title, r.year, r.mbid, r.artist_mbid, r.album_type,
		       r.release_date, r.genres, r.overview, r.poster_path, r.aoty_url, r.allmusic_url,
		       r.aoty_critic_score, r.aoty_critic_count, r.aoty_user_score, r.aoty_user_count,
		       r.aoty_must_hear, r.allmusic_rating, r.mb_rating, r.created_at
		FROM album_release_events e
		JOIN album_releases r ON r.id = e.release_id
		WHERE e.status = 'pending'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending album release events: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumReleaseRows(rows)
}

func (d *DB) ListAlbumReleaseEventsByWeek(ctx context.Context, year, week int) ([]EventWithAlbumRelease, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.release_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       r.id, r.artist_name, r.title, r.year, r.mbid, r.artist_mbid, r.album_type,
		       r.release_date, r.genres, r.overview, r.poster_path, r.aoty_url, r.allmusic_url,
		       r.aoty_critic_score, r.aoty_critic_count, r.aoty_user_score, r.aoty_user_count,
		       r.aoty_must_hear, r.allmusic_rating, r.mb_rating, r.created_at
		FROM album_release_events e
		JOIN album_releases r ON r.id = e.release_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		ORDER BY e.created_at DESC
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("listing album release events by week: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumReleaseRows(rows)
}

func (d *DB) ListAlbumReleaseEventsByWeekAndStatus(ctx context.Context, year, week int, statuses ...model.ReleaseStatus) ([]EventWithAlbumRelease, error) {
	if len(statuses) == 0 {
		return d.ListAlbumReleaseEventsByWeek(ctx, year, week)
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+2)
	args = append(args, year, week)
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, string(s))
	}
	query := fmt.Sprintf(`
		SELECT e.id, e.release_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       r.id, r.artist_name, r.title, r.year, r.mbid, r.artist_mbid, r.album_type,
		       r.release_date, r.genres, r.overview, r.poster_path, r.aoty_url, r.allmusic_url,
		       r.aoty_critic_score, r.aoty_critic_count, r.aoty_user_score, r.aoty_user_count,
		       r.aoty_must_hear, r.allmusic_rating, r.mb_rating, r.created_at
		FROM album_release_events e
		JOIN album_releases r ON r.id = e.release_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		AND e.status IN (%s)
		ORDER BY e.created_at DESC
	`, strings.Join(placeholders, ","))
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing album release events by week and status: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumReleaseRows(rows)
}

func (d *DB) GetAlbumReleaseEvent(ctx context.Context, id int64) (*EventWithAlbumRelease, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT e.id, e.release_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       r.id, r.artist_name, r.title, r.year, r.mbid, r.artist_mbid, r.album_type,
		       r.release_date, r.genres, r.overview, r.poster_path, r.aoty_url, r.allmusic_url,
		       r.aoty_critic_score, r.aoty_critic_count, r.aoty_user_score, r.aoty_user_count,
		       r.aoty_must_hear, r.allmusic_rating, r.mb_rating, r.created_at
		FROM album_release_events e
		JOIN album_releases r ON r.id = e.release_id
		WHERE e.id = ?
	`, id)
	var ev model.AlbumReleaseEvent
	var rl model.AlbumRelease
	var evStatus, evPrevStatus, evCreated string
	var rlAlbumType, rlCreated string
	var rlMustHear int

	err := row.Scan(
		&ev.ID, &ev.ReleaseID, &ev.Source, &ev.ReleaseDate,
		&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
		&rl.ID, &rl.ArtistName, &rl.Title, &rl.Year, &rl.MBID, &rl.ArtistMBID, &rlAlbumType,
		&rl.ReleaseDate, &rl.Genres, &rl.Overview, &rl.PosterPath, &rl.AOTYURL, &rl.AllMusicURL,
		&rl.AOTYCriticScore, &rl.AOTYCriticCount, &rl.AOTYUserScore, &rl.AOTYUserCount,
		&rlMustHear, &rl.AllMusicRating, &rl.MBRating, &rlCreated,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying album release event %d: %w", id, err)
	}

	ev.Status = model.ReleaseStatus(evStatus)
	ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
	ev.CreatedAt = evCreated

	rl.AlbumType = model.AlbumType(rlAlbumType)
	rl.AOTYMustHear = rlMustHear > 0
	rl.CreatedAt = rlCreated

	return &EventWithAlbumRelease{Event: &ev, Release: &rl}, nil
}

func scanEventWithAlbumReleaseRows(rows *sql.Rows) ([]EventWithAlbumRelease, error) {
	var results []EventWithAlbumRelease
	for rows.Next() {
		var ev model.AlbumReleaseEvent
		var rl model.AlbumRelease
		var evStatus, evPrevStatus, evCreated string
		var rlAlbumType, rlCreated string
		var rlMustHear int

		err := rows.Scan(
			&ev.ID, &ev.ReleaseID, &ev.Source, &ev.ReleaseDate,
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&rl.ID, &rl.ArtistName, &rl.Title, &rl.Year, &rl.MBID, &rl.ArtistMBID, &rlAlbumType,
			&rl.ReleaseDate, &rl.Genres, &rl.Overview, &rl.PosterPath, &rl.AOTYURL, &rl.AllMusicURL,
			&rl.AOTYCriticScore, &rl.AOTYCriticCount, &rl.AOTYUserScore, &rl.AOTYUserCount,
			&rlMustHear, &rl.AllMusicRating, &rl.MBRating, &rlCreated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning event with album release: %w", err)
		}

		ev.Status = model.ReleaseStatus(evStatus)
		ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
		ev.CreatedAt = evCreated

		rl.AlbumType = model.AlbumType(rlAlbumType)
		rl.AOTYMustHear = rlMustHear > 0
		rl.CreatedAt = rlCreated

		results = append(results, EventWithAlbumRelease{Event: &ev, Release: &rl})
	}
	return results, rows.Err()
}
