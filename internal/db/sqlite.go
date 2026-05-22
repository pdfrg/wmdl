package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/pdfrg/wmdl/internal/model"
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
		imdb_rating      REAL DEFAULT 0,
		rt_url           TEXT DEFAULT '',
		rt_critics_score REAL DEFAULT 0,
		rt_audience_score REAL DEFAULT 0,
		tmdb_rating      REAL DEFAULT 0,
		metacritic_score REAL DEFAULT 0,
		us_rating        TEXT DEFAULT '',
		overview         TEXT DEFAULT '',
		genres           TEXT DEFAULT '',
		runtime          INTEGER DEFAULT 0,
		yt_trailer_views INTEGER DEFAULT 0,
		poster_path     TEXT DEFAULT '',
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
		notes           TEXT DEFAULT '',
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
	d.db.ExecContext(ctx, `ALTER TABLE release_events ADD COLUMN notes TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN overview TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN genres TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN runtime INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN imdb_rating REAL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN yt_trailer_views INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN metacritic_score REAL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN us_rating TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN poster_path TEXT DEFAULT ''`)

	return nil
}

func (d *DB) UpsertTitle(ctx context.Context, t *model.Title) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO titles (tmdb_id, tvdb_id, title, year, media_type, imdb_id, imdb_rating,
		                    rt_url, rt_critics_score, rt_audience_score, tmdb_rating,
		                    metacritic_score, us_rating, yt_trailer_views, overview, genres, runtime, poster_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tmdb_id) DO UPDATE SET
			title             = excluded.title,
			year              = excluded.year,
			media_type        = excluded.media_type,
			tvdb_id           = excluded.tvdb_id,
			imdb_id           = excluded.imdb_id,
			imdb_rating       = excluded.imdb_rating,
			rt_url            = excluded.rt_url,
			rt_critics_score  = excluded.rt_critics_score,
			rt_audience_score = excluded.rt_audience_score,
			tmdb_rating       = excluded.tmdb_rating,
			metacritic_score  = excluded.metacritic_score,
			us_rating         = excluded.us_rating,
			yt_trailer_views  = excluded.yt_trailer_views,
			overview          = excluded.overview,
			genres            = excluded.genres,
			runtime           = excluded.runtime,
			poster_path       = excluded.poster_path
	`,
		t.TmdbID, t.TvdbID, t.Title, t.Year, string(t.MediaType), t.ImdbID, t.ImdbRating,
		t.RTURL, t.RTCriticsScore, t.RTAudienceScore, t.TmdbRating,
		t.MetacriticScore, t.USRating, t.YoutubeViews, t.Overview, t.Genres, t.Runtime, t.PosterPath, t.CreatedAt,
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
		       imdb_rating, rt_url, rt_critics_score, rt_audience_score,
		       tmdb_rating, metacritic_score, us_rating, overview, genres, runtime, poster_path, created_at
		FROM titles WHERE tmdb_id = ?
	`, tmdbID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.Title, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.TmdbRating, &t.MetacriticScore, &t.USRating, &t.Overview, &t.Genres, &t.Runtime, &t.PosterPath, &createdAt,
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
		       imdb_rating, rt_url, rt_critics_score, rt_audience_score,
		       tmdb_rating, metacritic_score, us_rating,
		       yt_trailer_views, overview, genres, runtime, poster_path, created_at
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
			&t.ImdbID, &t.ImdbRating, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
			&t.TmdbRating, &t.MetacriticScore, &t.USRating, &t.YoutubeViews, &t.Overview, &t.Genres, &t.Runtime, &t.PosterPath, &createdAt,
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
		INSERT INTO release_events (title_id, source, release_type, release_date, status, previous_status, notes, iso_year, iso_week)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.TitleID, e.Source, string(e.ReleaseType), e.ReleaseDate, string(e.Status), string(e.PreviousStatus), e.Notes, e.ISOYear, e.ISOWeek)
	if err != nil {
		return 0, fmt.Errorf("creating release event: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) ListReleaseEvents(ctx context.Context, status model.ReleaseStatus) ([]*model.ReleaseEvent, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, title_id, source, release_type, release_date, status, previous_status, notes, created_at, iso_year, iso_week
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
			&status, &prevStatus, &e.Notes, &createdAt, &e.ISOYear, &e.ISOWeek,
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
		SELECT id, title_id, source, release_type, release_date, status, previous_status, notes, created_at, iso_year, iso_week
		FROM release_events WHERE title_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, titleID).Scan(
		&e.ID, &e.TitleID, &e.Source, &releaseType, &e.ReleaseDate,
		&status, &prevStatus, &e.Notes, &createdAt, &e.ISOYear, &e.ISOWeek,
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
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at
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
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.Title, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.ImdbRating, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tl.MetacriticScore, &tl.USRating, &tl.YoutubeViews, &tl.Overview, &tl.Genres, &tl.Runtime, &tl.PosterPath, &tlCreated,
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
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at
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
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.Title, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.ImdbRating, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tl.MetacriticScore, &tl.USRating, &tl.YoutubeViews, &tl.Overview, &tl.Genres, &tl.Runtime, &tl.PosterPath, &tlCreated,
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

func (d *DB) UpdateDownloadStatus(ctx context.Context, id int64, status model.DownloadStatus) error {
	_, err := d.db.ExecContext(ctx, `UPDATE downloads SET status = ? WHERE id = ?`, string(status), id)
	return err
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
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = d.db.QueryContext(ctx, `
			SELECT year, week, week_date, discovered, reviewed, processed, updated_at
			FROM week_state ORDER BY year DESC, week DESC LIMIT ?
		`, limit)
	} else {
		rows, err = d.db.QueryContext(ctx, `
			SELECT year, week, week_date, discovered, reviewed, processed, updated_at
			FROM week_state ORDER BY year DESC, week DESC
		`)
	}
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

func (d *DB) GetLatestDiscoveredWeek(ctx context.Context) (*model.WeekState, error) {
	var ws model.WeekState
	var disc, rev, proc int
	var updatedAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT year, week, week_date, discovered, reviewed, processed, updated_at
		FROM week_state
		WHERE discovered = 1
		ORDER BY year DESC, week DESC
		LIMIT 1
	`).Scan(&ws.Year, &ws.Week, &ws.WeekDate, &disc, &rev, &proc, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying latest discovered week: %w", err)
	}
	ws.Discovered = disc > 0
	ws.Reviewed = rev > 0
	ws.Processed = proc > 0
	ws.UpdatedAt = updatedAt
	return &ws, nil
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

func (d *DB) GetWeekState(ctx context.Context, year, week int) (*model.WeekState, error) {
	var ws model.WeekState
	var disc, rev, proc int
	var updatedAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT year, week, week_date, discovered, reviewed, processed, updated_at
		FROM week_state WHERE year = ? AND week = ?
	`, year, week).Scan(&ws.Year, &ws.Week, &ws.WeekDate, &disc, &rev, &proc, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying week state: %w", err)
	}
	ws.Discovered = disc > 0
	ws.Reviewed = rev > 0
	ws.Processed = proc > 0
	ws.UpdatedAt = updatedAt
	return &ws, nil
}

func (d *DB) ListEventsByWeekWithTitles(ctx context.Context, year, week int) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		ORDER BY e.created_at DESC
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("listing events by week with titles: %w", err)
	}
	defer rows.Close()

	return scanEventWithTitleRows(rows)
}

func (d *DB) ListEventsByWeekAndStatus(ctx context.Context, year, week int, statuses ...model.ReleaseStatus) ([]EventWithTitle, error) {
	if len(statuses) == 0 {
		return d.ListEventsByWeekWithTitles(ctx, year, week)
	}

	placeholders := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+2)
	args = append(args, year, week)
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, string(s))
	}

	query := fmt.Sprintf(`
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		AND e.status IN (%s)
		ORDER BY e.created_at DESC
	`, strings.Join(placeholders, ","))

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events by week and status: %w", err)
	}
	defer rows.Close()

	return scanEventWithTitleRows(rows)
}

func scanEventWithTitleRows(rows *sql.Rows) ([]EventWithTitle, error) {
	var results []EventWithTitle
	for rows.Next() {
		var ev model.ReleaseEvent
		var tl model.Title
		var evRelType, evStatus, evPrevStatus, evCreated string
		var tlMediaType, tlCreated string

		err := rows.Scan(
			&ev.ID, &ev.TitleID, &ev.Source, &evRelType, &ev.ReleaseDate,
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.Title, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.ImdbRating, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tl.MetacriticScore, &tl.USRating, &tl.YoutubeViews, &tl.Overview, &tl.Genres, &tl.Runtime, &tl.PosterPath, &tlCreated,
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
