package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/pdfrg/wmd/internal/model"
)

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	d.SetMaxOpenConns(1)
	return &DB{db: d}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) Migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS titles (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		tmdb_id         INTEGER NOT NULL,
		tvdb_id         INTEGER NOT NULL DEFAULT 0,
		title           TEXT NOT NULL,
		year            INTEGER NOT NULL DEFAULT 0,
		media_type      TEXT NOT NULL DEFAULT 'movie'
		                CHECK(media_type IN ('movie','tv')),
		imdb_id         TEXT DEFAULT '',
		rt_url          TEXT DEFAULT '',
		rt_critics_score REAL DEFAULT 0,
		rt_audience_score REAL DEFAULT 0,
		tmdb_rating     REAL DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(tmdb_id)
	);

	CREATE TABLE IF NOT EXISTS release_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		title_id        INTEGER NOT NULL REFERENCES titles(id),
		source          TEXT NOT NULL,
		release_type    TEXT NOT NULL DEFAULT ''
		                CHECK(release_type IN ('physical','streaming','')),
		release_date    TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'pending'
		                CHECK(status IN ('pending','approved','rejected','downloaded')),
		previous_status TEXT DEFAULT '',
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS downloads (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		title_id        INTEGER NOT NULL REFERENCES titles(id),
		release_event_id INTEGER NOT NULL REFERENCES release_events(id),
		quality         TEXT NOT NULL DEFAULT '',
		source_type     TEXT NOT NULL DEFAULT '',
		codec           TEXT NOT NULL DEFAULT '',
		info_hash       TEXT NOT NULL DEFAULT '',
		category        TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'added'
		                CHECK(status IN ('added','downloading','complete','upgraded')),
		client_torrent_id TEXT NOT NULL DEFAULT '',
		radarr_id       INTEGER DEFAULT 0,
		sonarr_id       INTEGER DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_titles_tmdb_id ON titles(tmdb_id);
	CREATE INDEX IF NOT EXISTS idx_release_events_title_id ON release_events(title_id);
	CREATE INDEX IF NOT EXISTS idx_release_events_status ON release_events(status);
	CREATE INDEX IF NOT EXISTS idx_downloads_title_id ON downloads(title_id);

	CREATE TABLE IF NOT EXISTS week_state (
		year        INTEGER NOT NULL,
		week        INTEGER NOT NULL,
		week_date   TEXT NOT NULL DEFAULT '',
		discovered  INTEGER NOT NULL DEFAULT 0,
		reviewed    INTEGER NOT NULL DEFAULT 0,
		processed   INTEGER NOT NULL DEFAULT 0,
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(year, week)
	);
	`
	if _, err := d.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrating schema: %w", err)
	}

	// Migrations for existing databases
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE release_events ADD COLUMN iso_year INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE release_events ADD COLUMN iso_week INTEGER DEFAULT 0`)

	return nil
}

func (d *DB) UpsertTitle(ctx context.Context, t *model.Title) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO titles (tmdb_id, tvdb_id, title, year, media_type, imdb_id, rt_url,
		                    rt_critics_score, rt_audience_score, tmdb_rating, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tmdb_id) DO UPDATE SET
			title             = excluded.title,
			year              = excluded.year,
			media_type        = excluded.media_type,
			tvdb_id           = excluded.tvdb_id,
			imdb_id           = excluded.imdb_id,
			rt_url            = excluded.rt_url,
			rt_critics_score  = excluded.rt_critics_score,
			rt_audience_score = excluded.rt_audience_score,
			tmdb_rating       = excluded.tmdb_rating
	`,
		t.TmdbID, t.TvdbID, t.Title, t.Year, string(t.MediaType), t.ImdbID, t.RTURL,
		t.RTCriticsScore, t.RTAudienceScore, t.TmdbRating, t.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("upserting title: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) GetTitleByTmdbID(ctx context.Context, tmdbID int) (*model.Title, error) {
	var t model.Title
	var mediaType string
	var createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, title, year, media_type, imdb_id,
		       rt_url, rt_critics_score, rt_audience_score, tmdb_rating, created_at
		FROM titles WHERE tmdb_id = ?
	`, tmdbID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.Title, &t.Year, &mediaType,
		&t.ImdbID, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.TmdbRating, &createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying title by tmdb_id: %w", err)
	}
	t.MediaType = model.MediaType(mediaType)
	t.CreatedAt = createdAt
	return &t, nil
}

func (d *DB) GetTitleByID(ctx context.Context, id int64) (*model.Title, error) {
	var t model.Title
	var mediaType string
	var createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, title, year, media_type, imdb_id,
		       rt_url, rt_critics_score, rt_audience_score, tmdb_rating, created_at
		FROM titles WHERE id = ?
	`, id).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.Title, &t.Year, &mediaType,
		&t.ImdbID, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.TmdbRating, &createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying title by id: %w", err)
	}
	t.MediaType = model.MediaType(mediaType)
	t.CreatedAt = createdAt
	return &t, nil
}

func (d *DB) ListTitles(ctx context.Context) ([]*model.Title, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, title, year, media_type, imdb_id,
		       rt_url, rt_critics_score, rt_audience_score, tmdb_rating, created_at
		FROM titles ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing titles: %w", err)
	}
	defer rows.Close()

	var titles []*model.Title
	for rows.Next() {
		var t model.Title
		var mediaType string
		var createdAt string
		if err := rows.Scan(
			&t.ID, &t.TmdbID, &t.TvdbID, &t.Title, &t.Year, &mediaType,
			&t.ImdbID, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
			&t.TmdbRating, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning title row: %w", err)
		}
		t.MediaType = model.MediaType(mediaType)
		t.CreatedAt = createdAt
		titles = append(titles, &t)
	}
	return titles, rows.Err()
}

func (d *DB) CreateReleaseEvent(ctx context.Context, e *model.ReleaseEvent) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO release_events (title_id, source, release_type, release_date, status, previous_status)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.TitleID, e.Source, string(e.ReleaseType), e.ReleaseDate, string(e.Status), string(e.PreviousStatus))
	if err != nil {
		return 0, fmt.Errorf("creating release event: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) ListReleaseEvents(ctx context.Context, status model.ReleaseStatus) ([]*model.ReleaseEvent, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, title_id, source, release_type, release_date, status, previous_status, created_at
		FROM release_events WHERE status = ?
		ORDER BY created_at DESC
	`, string(status))
	if err != nil {
		return nil, fmt.Errorf("listing release events: %w", err)
	}
	defer rows.Close()

	var events []*model.ReleaseEvent
	for rows.Next() {
		var e model.ReleaseEvent
		var releaseType, status, prevStatus, createdAt string
		if err := rows.Scan(
			&e.ID, &e.TitleID, &e.Source, &releaseType, &e.ReleaseDate,
			&status, &prevStatus, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning release event row: %w", err)
		}
		e.ReleaseType = model.ReleaseType(releaseType)
		e.Status = model.ReleaseStatus(status)
		e.PreviousStatus = model.ReleaseStatus(prevStatus)
		e.CreatedAt = createdAt
		events = append(events, &e)
	}
	return events, rows.Err()
}

func (d *DB) GetLatestReleaseEvent(ctx context.Context, titleID int64) (*model.ReleaseEvent, error) {
	var e model.ReleaseEvent
	var releaseType, status, prevStatus, createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, title_id, source, release_type, release_date, status, previous_status, created_at
		FROM release_events WHERE title_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, titleID).Scan(
		&e.ID, &e.TitleID, &e.Source, &releaseType, &e.ReleaseDate,
		&status, &prevStatus, &createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying latest release event: %w", err)
	}
	e.ReleaseType = model.ReleaseType(releaseType)
	e.Status = model.ReleaseStatus(status)
	e.PreviousStatus = model.ReleaseStatus(prevStatus)
	e.CreatedAt = createdAt
	return &e, nil
}

type EventWithTitle struct {
	Event *model.ReleaseEvent
	Title *model.Title
}

func (d *DB) ListPendingWithTitles(ctx context.Context) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.created_at,
		       t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.rt_url, t.rt_critics_score, t.rt_audience_score, t.tmdb_rating, t.created_at
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.status = 'pending'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending events with titles: %w", err)
	}
	defer rows.Close()

	var results []EventWithTitle
	for rows.Next() {
		var ev model.ReleaseEvent
		var tl model.Title
		var evRelType, evStatus, evPrevStatus, evCreated string
		var tlMediaType, tlCreated string

		err := rows.Scan(
			&ev.ID, &ev.TitleID, &ev.Source, &evRelType, &ev.ReleaseDate,
			&evStatus, &evPrevStatus, &evCreated,
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.Title, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tlCreated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning event with title: %w", err)
		}

		ev.ReleaseType = model.ReleaseType(evRelType)
		ev.Status = model.ReleaseStatus(evStatus)
		ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
		ev.CreatedAt = evCreated

		tl.MediaType = model.MediaType(tlMediaType)
		tl.CreatedAt = tlCreated

		results = append(results, EventWithTitle{Event: &ev, Title: &tl})
	}
	return results, rows.Err()
}

func (d *DB) ListApprovedWithTitles(ctx context.Context) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.created_at,
		       t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.rt_url, t.rt_critics_score, t.rt_audience_score, t.tmdb_rating, t.created_at
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.status = 'approved'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing approved events with titles: %w", err)
	}
	defer rows.Close()

	var results []EventWithTitle
	for rows.Next() {
		var ev model.ReleaseEvent
		var tl model.Title
		var evRelType, evStatus, evPrevStatus, evCreated string
		var tlMediaType, tlCreated string

		err := rows.Scan(
			&ev.ID, &ev.TitleID, &ev.Source, &evRelType, &ev.ReleaseDate,
			&evStatus, &evPrevStatus, &evCreated,
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.Title, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tlCreated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning approved event with title: %w", err)
		}

		ev.ReleaseType = model.ReleaseType(evRelType)
		ev.Status = model.ReleaseStatus(evStatus)
		ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
		ev.CreatedAt = evCreated

		tl.MediaType = model.MediaType(tlMediaType)
		tl.CreatedAt = tlCreated

		results = append(results, EventWithTitle{Event: &ev, Title: &tl})
	}
	return results, rows.Err()
}

func (d *DB) UpdateReleaseEventStatus(ctx context.Context, id int64, status model.ReleaseStatus) error {
	_, err := d.db.ExecContext(ctx, `UPDATE release_events SET status = ? WHERE id = ?`, string(status), id)
	return err
}

func (d *DB) CreateDownload(ctx context.Context, dl *model.Download) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO downloads (title_id, release_event_id, quality, source_type, codec,
		                       info_hash, category, status, client_torrent_id, radarr_id, sonarr_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, dl.TitleID, dl.ReleaseEventID, dl.Quality, dl.SourceType, dl.Codec,
		dl.InfoHash, dl.Category, string(dl.Status), dl.ClientTorrentID, dl.RadarrID, dl.SonarrID)
	if err != nil {
		return 0, fmt.Errorf("creating download: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) UpsertWeekState(ctx context.Context, ws *model.WeekState) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO week_state (year, week, week_date, discovered, reviewed, processed, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(year, week) DO UPDATE SET
			discovered = MAX(week_state.discovered, excluded.discovered),
			reviewed   = MAX(week_state.reviewed, excluded.reviewed),
			processed  = MAX(week_state.processed, excluded.processed),
			updated_at = datetime('now')
	`, ws.Year, ws.Week, ws.WeekDate,
		boolToInt(ws.Discovered), boolToInt(ws.Reviewed), boolToInt(ws.Processed))
	return err
}

func (d *DB) GetWeekStates(ctx context.Context, limit int) ([]*model.WeekState, error) {
	if limit <= 0 {
		limit = 12
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT year, week, week_date, discovered, reviewed, processed, updated_at
		FROM week_state ORDER BY year DESC, week DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying week states: %w", err)
	}
	defer rows.Close()

	var states []*model.WeekState
	for rows.Next() {
		var ws model.WeekState
		var disc, rev, proc int
		if err := rows.Scan(&ws.Year, &ws.Week, &ws.WeekDate, &disc, &rev, &proc, &ws.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning week state: %w", err)
		}
		ws.Discovered = disc > 0
		ws.Reviewed = rev > 0
		ws.Processed = proc > 0
		states = append(states, &ws)
	}
	return states, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (d *DB) GetDownloadByTitleID(ctx context.Context, titleID int64) (*model.Download, error) {
	var dl model.Download
	var status, createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, title_id, release_event_id, quality, source_type, codec,
		       info_hash, category, status, client_torrent_id, radarr_id, sonarr_id, created_at
		FROM downloads WHERE title_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, titleID).Scan(
		&dl.ID, &dl.TitleID, &dl.ReleaseEventID, &dl.Quality, &dl.SourceType,
		&dl.Codec, &dl.InfoHash, &dl.Category, &status, &dl.ClientTorrentID,
		&dl.RadarrID, &dl.SonarrID, &createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying download by title_id: %w", err)
	}
	dl.Status = model.DownloadStatus(status)
	dl.CreatedAt = createdAt
	return &dl, nil
}
