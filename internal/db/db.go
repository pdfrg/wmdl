package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	d.SetMaxOpenConns(1)
	db := &DB{db: d}
	migrateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(migrateCtx); err != nil {
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return db, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) Transaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("rolling back: %v (original: %w)", rbErr, err)
		}
		return err
	}

	return tx.Commit()
}

func (d *DB) Migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS titles (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		tmdb_id         INTEGER NOT NULL,
		tvdb_id         INTEGER NOT NULL DEFAULT 0,
		mal_id          INTEGER NOT NULL DEFAULT 0,
		title           TEXT NOT NULL,
		year            INTEGER NOT NULL DEFAULT 0,
		media_type      TEXT NOT NULL DEFAULT 'movie'
		                CHECK(media_type IN ('movie','tv','anime')),
		imdb_id         TEXT DEFAULT '',
		imdb_rating      REAL DEFAULT 0,
		imdb_votes       INTEGER DEFAULT 0,
		awards           TEXT DEFAULT '',
		box_office       TEXT DEFAULT '',
		director         TEXT DEFAULT '',
		writer           TEXT DEFAULT '',
		actors           TEXT DEFAULT '',
		rt_url           TEXT DEFAULT '',
		rt_critics_score REAL DEFAULT 0,
		rt_audience_score REAL DEFAULT 0,
		tmdb_rating      REAL DEFAULT 0,
		metacritic_score REAL DEFAULT 0,
		us_rating         TEXT DEFAULT '',
		original_language TEXT DEFAULT '',
		origin_country    TEXT DEFAULT '',
		overview          TEXT DEFAULT '',
		genres            TEXT DEFAULT '',
		runtime           INTEGER DEFAULT 0,
		yt_trailer_views  INTEGER DEFAULT 0,
		poster_path       TEXT DEFAULT '',
		anime_type        TEXT DEFAULT '',
		anime_episodes    INTEGER DEFAULT 0,
		anime_status      TEXT DEFAULT '',
		anime_members     INTEGER DEFAULT 0,
		anime_rank        INTEGER DEFAULT 0,
		anime_source      TEXT DEFAULT '',
		anime_studio      TEXT DEFAULT '',
		themes            TEXT DEFAULT '',
		demographics      TEXT DEFAULT '',
		streaming         TEXT DEFAULT '',
		created_at        TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(tmdb_id, mal_id)
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
	CREATE INDEX IF NOT EXISTS idx_titles_tvdb_id ON titles(tvdb_id);
	CREATE INDEX IF NOT EXISTS idx_release_events_title_id ON release_events(title_id);
	CREATE INDEX IF NOT EXISTS idx_release_events_status ON release_events(status);
	CREATE INDEX IF NOT EXISTS idx_downloads_title_id ON downloads(title_id);
	CREATE INDEX IF NOT EXISTS idx_downloads_release_event_id ON downloads(release_event_id);

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

	CREATE TABLE IF NOT EXISTS album_releases (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		artist_name     TEXT NOT NULL,
		title           TEXT NOT NULL,
		year            INTEGER NOT NULL DEFAULT 0,
		mbid            TEXT NOT NULL DEFAULT '',
		artist_mbid     TEXT NOT NULL DEFAULT '',
		album_type      TEXT NOT NULL DEFAULT '',
		release_date    TEXT NOT NULL DEFAULT '',
		genres          TEXT NOT NULL DEFAULT '',
		overview        TEXT NOT NULL DEFAULT '',
		poster_path     TEXT NOT NULL DEFAULT '',
		aoty_url        TEXT NOT NULL DEFAULT '',
		allmusic_url    TEXT NOT NULL DEFAULT '',
		aoty_critic_score REAL DEFAULT 0,
		aoty_critic_count INTEGER DEFAULT 0,
		aoty_user_score    REAL DEFAULT 0,
		aoty_user_count    INTEGER DEFAULT 0,
		aoty_must_hear     INTEGER DEFAULT 0,
		allmusic_rating    REAL DEFAULT 0,
		mb_rating          REAL DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(artist_name, title, year)
	);

	CREATE TABLE IF NOT EXISTS album_release_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		release_id      INTEGER NOT NULL REFERENCES album_releases(id),
		source          TEXT NOT NULL,
		release_date    TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'pending'
		                CHECK(status IN ('pending','approved','rejected','downloaded')),
		previous_status TEXT DEFAULT '',
		notes           TEXT DEFAULT '',
		created_at      TEXT DEFAULT (datetime('now')),
		iso_year        INTEGER DEFAULT 0,
		iso_week        INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS library_cache (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		source     TEXT NOT NULL,
		ext_id     TEXT NOT NULL,
		arr_id     INTEGER NOT NULL,
		arr_title  TEXT NOT NULL,
		details    TEXT NOT NULL DEFAULT '{}',
		fetched_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(source, ext_id)
	);

	CREATE TABLE IF NOT EXISTS authors (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		hardcover_id INTEGER NOT NULL DEFAULT 0,
		olid         TEXT NOT NULL DEFAULT '',
		name         TEXT NOT NULL,
		bio          TEXT NOT NULL DEFAULT '',
		born_date    TEXT NOT NULL DEFAULT '',
		death_date   TEXT NOT NULL DEFAULT '',
		image_url    TEXT NOT NULL DEFAULT '',
		identifiers  TEXT NOT NULL DEFAULT '{}',
		links        TEXT NOT NULL DEFAULT '{}',
		created_at   TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(olid)
	);

	CREATE TABLE IF NOT EXISTS books (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		author_id    INTEGER NOT NULL REFERENCES authors(id),
		title        TEXT NOT NULL,
		subtitle     TEXT NOT NULL DEFAULT '',
		hardcover_id   INTEGER NOT NULL DEFAULT 0,
		hardcover_slug TEXT NOT NULL DEFAULT '',
		olid           TEXT NOT NULL DEFAULT '',
		isbn10       TEXT NOT NULL DEFAULT '',
		isbn13       TEXT NOT NULL DEFAULT '',
		asin         TEXT NOT NULL DEFAULT '',
		pages        INTEGER DEFAULT 0,
		audio_seconds INTEGER DEFAULT 0,
		description  TEXT NOT NULL DEFAULT '',
		release_date TEXT NOT NULL DEFAULT '',
		release_year INTEGER DEFAULT 0,
		rating         REAL DEFAULT 0,
		ratings_count  INTEGER DEFAULT 0,
		shelvings_count INTEGER DEFAULT 0,
		image_url      TEXT NOT NULL DEFAULT '',
		language       TEXT NOT NULL DEFAULT '',
		publisher      TEXT NOT NULL DEFAULT '',
		tags           TEXT NOT NULL DEFAULT '',
		literary_type  TEXT NOT NULL DEFAULT '',
		created_at     TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(author_id, title, release_year)
	);

	CREATE TABLE IF NOT EXISTS book_release_events (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		book_id           INTEGER NOT NULL REFERENCES books(id),
		source            TEXT NOT NULL,
		release_date      TEXT NOT NULL DEFAULT '',
		format_pref       TEXT NOT NULL DEFAULT 'both'
		                  CHECK(format_pref IN ('ebook','audiobook','both')),
		status            TEXT NOT NULL DEFAULT 'pending'
		                  CHECK(status IN ('pending','approved','rejected','downloaded')),
		previous_status   TEXT DEFAULT '',
		notes             TEXT DEFAULT '',
		created_at        TEXT DEFAULT (datetime('now')),
		iso_year          INTEGER DEFAULT 0,
		iso_week          INTEGER DEFAULT 0,
		ebook_processed   INTEGER NOT NULL DEFAULT 0,
		audiobook_processed INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS book_downloads (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		book_id             INTEGER NOT NULL REFERENCES books(id),
		book_release_event  INTEGER NOT NULL REFERENCES book_release_events(id),
		format              TEXT NOT NULL DEFAULT 'both'
		                    CHECK(format IN ('ebook','audiobook','both')),
		quality             TEXT NOT NULL DEFAULT '',
		source_type         TEXT NOT NULL DEFAULT '',
		codec               TEXT NOT NULL DEFAULT '',
		info_hash           TEXT NOT NULL DEFAULT '',
		category            TEXT NOT NULL DEFAULT '',
		status              TEXT NOT NULL DEFAULT 'added'
		                    CHECK(status IN ('added','downloading','complete','upgraded')),
		client_torrent_id   TEXT NOT NULL DEFAULT '',
		created_at          TEXT NOT NULL DEFAULT (datetime('now'))
	);
	`
	if _, err := d.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrating schema: %w", err)
	}

	// Migrations for existing databases
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN mal_id INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE book_release_events ADD COLUMN ebook_processed INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE book_release_events ADD COLUMN audiobook_processed INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN shelvings_count INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN hardcover_slug TEXT NOT NULL DEFAULT ''`)

	// Recreate titles table to update media_type CHECK constraint for anime support
	d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS titles_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			tmdb_id         INTEGER NOT NULL,
			tvdb_id         INTEGER NOT NULL DEFAULT 0,
			mal_id          INTEGER NOT NULL DEFAULT 0,
			title           TEXT NOT NULL,
			year            INTEGER NOT NULL DEFAULT 0,
			media_type      TEXT NOT NULL DEFAULT 'movie'
			                CHECK(media_type IN ('movie','tv','anime')),
			imdb_id         TEXT DEFAULT '',
			imdb_rating      REAL DEFAULT 0,
			imdb_votes       INTEGER DEFAULT 0,
			awards           TEXT DEFAULT '',
			box_office       TEXT DEFAULT '',
			director         TEXT DEFAULT '',
			writer           TEXT DEFAULT '',
			actors           TEXT DEFAULT '',
			rt_url           TEXT DEFAULT '',
			rt_critics_score REAL DEFAULT 0,
			rt_audience_score REAL DEFAULT 0,
			tmdb_rating      REAL DEFAULT 0,
			metacritic_score REAL DEFAULT 0,
			us_rating         TEXT DEFAULT '',
			original_language TEXT DEFAULT '',
			origin_country    TEXT DEFAULT '',
			overview          TEXT DEFAULT '',
			genres            TEXT DEFAULT '',
			runtime           INTEGER DEFAULT 0,
			yt_trailer_views  INTEGER DEFAULT 0,
			poster_path       TEXT DEFAULT '',
			anime_type        TEXT DEFAULT '',
			anime_episodes    INTEGER DEFAULT 0,
			anime_status      TEXT DEFAULT '',
			anime_members     INTEGER DEFAULT 0,
			anime_rank        INTEGER DEFAULT 0,
			anime_source      TEXT DEFAULT '',
			anime_studio      TEXT DEFAULT '',
			themes            TEXT DEFAULT '',
			demographics      TEXT DEFAULT '',
			streaming         TEXT DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(tmdb_id, mal_id)
		)
	`)
	if _, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO titles_new (
			id, tmdb_id, tvdb_id, mal_id, title, year, media_type,
			imdb_id, imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
			tmdb_rating, metacritic_score, us_rating, original_language, origin_country,
			yt_trailer_views, overview, genres, runtime, poster_path, created_at,
			anime_type, anime_episodes, anime_status, anime_members, anime_rank,
			anime_source, anime_studio, themes, demographics, streaming
		) SELECT
			id, tmdb_id, tvdb_id, mal_id, title, year, media_type,
			imdb_id, imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
			tmdb_rating, metacritic_score, us_rating, original_language, origin_country,
			yt_trailer_views, overview, genres, runtime, poster_path, created_at,
			anime_type, anime_episodes, anime_status, anime_members, anime_rank,
			anime_source, anime_studio, themes, demographics, streaming
		FROM titles
	`); err == nil {
		d.db.ExecContext(ctx, `DROP TABLE IF EXISTS titles`)
		d.db.ExecContext(ctx, `ALTER TABLE titles_new RENAME TO titles`)
	} else {
		d.db.ExecContext(ctx, `DROP TABLE IF EXISTS titles_new`)
	}
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
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN original_language TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN origin_country TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN tmdb_title TEXT DEFAULT ''`)

	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_type TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_episodes INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_status TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_members INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_rank INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_source TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN anime_studio TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN themes TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN demographics TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN streaming TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN collection_id INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN collection_name TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN imdb_votes INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN awards TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN box_office TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN director TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN writer TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN actors TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN series_id TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN series_name TEXT DEFAULT ''`)

	var hasOldTables bool
	if err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='artists'`,
	).Scan(&hasOldTables); err == nil && hasOldTables {
		d.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO album_releases (
				artist_name, title, year, mbid, artist_mbid, album_type,
				release_date, genres, overview, poster_path,
				aoty_url, allmusic_url,
				aoty_critic_score, aoty_critic_count,
				aoty_user_score, aoty_user_count, aoty_must_hear,
				allmusic_rating, mb_rating, created_at
			) SELECT
				a.name, al.title, al.year, al.mbid, a.mbid, al.album_type,
				al.release_date, al.genres, al.overview, al.poster_path,
				al.aoty_url, al.allmusic_url,
				al.aoty_critic_score, al.aoty_critic_count,
				al.aoty_user_score, al.aoty_user_count, al.aoty_must_hear,
				al.allmusic_rating, al.mb_rating, al.created_at
			FROM albums al
			JOIN artists a ON a.id = al.artist_id
		`)
		d.db.ExecContext(ctx, `
			UPDATE album_release_events
			SET release_id = (
				SELECT ar.id FROM album_releases ar
				JOIN albums al ON al.mbid = ar.mbid AND al.title = ar.title
				WHERE al.id = album_release_events.album_id
			)
		`)
		d.db.ExecContext(ctx, `DROP TABLE IF EXISTS albums`)
		d.db.ExecContext(ctx, `DROP TABLE IF EXISTS artists`)
	}

	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_release_events_week ON release_events(iso_year, iso_week)`)
	d.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_album_releases_mbid ON album_releases(mbid) WHERE mbid != ''`)
	d.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_album_releases_aoty_url ON album_releases(aoty_url) WHERE aoty_url != ''`)
	d.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_album_releases_allmusic_url ON album_releases(allmusic_url) WHERE allmusic_url != ''`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_album_release_events_release_id ON album_release_events(release_id)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_album_release_events_status ON album_release_events(status)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_album_release_events_week ON album_release_events(iso_year, iso_week)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_library_cache_source_ext ON library_cache(source, ext_id)`)

	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_authors_olid ON authors(olid)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_books_author_id ON books(author_id)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_books_isbn13 ON books(isbn13)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_books_asin ON books(asin)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_book_release_events_book_id ON book_release_events(book_id)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_book_release_events_status ON book_release_events(status)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_book_release_events_week ON book_release_events(iso_year, iso_week)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_book_downloads_book_id ON book_downloads(book_id)`)

	return nil
}
