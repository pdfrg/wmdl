package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/pdfrg/wmdl/internal/model"
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
	if err := db.Migrate(context.Background()); err != nil {
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

	CREATE TABLE IF NOT EXISTS artists (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		mbid           TEXT NOT NULL DEFAULT '',
		name           TEXT NOT NULL,
		lidarr_id      INTEGER DEFAULT 0,
		country        TEXT NOT NULL DEFAULT '',
		artist_type    TEXT NOT NULL DEFAULT '',
		begin_date     TEXT NOT NULL DEFAULT '',
		end_date       TEXT NOT NULL DEFAULT '',
		begin_area     TEXT NOT NULL DEFAULT '',
		area           TEXT NOT NULL DEFAULT '',
		disambiguation TEXT NOT NULL DEFAULT '',
		tags           TEXT NOT NULL DEFAULT '',
		genres         TEXT NOT NULL DEFAULT '',
		mb_rating      REAL DEFAULT 0,
		created_at     TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE(mbid)
	);

	CREATE TABLE IF NOT EXISTS albums (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		artist_id       INTEGER NOT NULL REFERENCES artists(id),
		title           TEXT NOT NULL,
		year            INTEGER NOT NULL DEFAULT 0,
		mbid            TEXT NOT NULL DEFAULT '',
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
		UNIQUE(artist_id, title, year)
	);

	CREATE TABLE IF NOT EXISTS album_release_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		album_id        INTEGER NOT NULL REFERENCES albums(id),
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
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN country TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN artist_type TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN begin_date TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN end_date TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN begin_area TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN tags TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN genres TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN mb_rating REAL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN area TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE artists ADD COLUMN disambiguation TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE titles ADD COLUMN mal_id INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE book_release_events ADD COLUMN ebook_processed INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE book_release_events ADD COLUMN audiobook_processed INTEGER NOT NULL DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN shelvings_count INTEGER DEFAULT 0`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN hardcover_slug TEXT NOT NULL DEFAULT ''`)

	// Recreate titles table to update media_type CHECK constraint for anime support
	// SQLite doesn't support ALTER TABLE to change constraints, so we recreate.
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
	// If the old titles table exists and the new one was just created, copy data
	if _, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO titles_new (
			id, tmdb_id, tvdb_id, mal_id, title, year, media_type,
			imdb_id, imdb_rating, rt_url, rt_critics_score, rt_audience_score,
			tmdb_rating, metacritic_score, us_rating, original_language, origin_country,
			yt_trailer_views, overview, genres, runtime, poster_path, created_at,
			anime_type, anime_episodes, anime_status, anime_members, anime_rank,
			anime_source, anime_studio, themes, demographics, streaming
		) SELECT
			id, tmdb_id, tvdb_id, mal_id, title, year, media_type,
			imdb_id, imdb_rating, rt_url, rt_critics_score, rt_audience_score,
			tmdb_rating, metacritic_score, us_rating, original_language, origin_country,
			yt_trailer_views, overview, genres, runtime, poster_path, created_at,
			anime_type, anime_episodes, anime_status, anime_members, anime_rank,
			anime_source, anime_studio, themes, demographics, streaming
		FROM titles
	`); err == nil {
		// Only swap if the old table still exists (has content from original schema)
		d.db.ExecContext(ctx, `DROP TABLE IF EXISTS titles`)
		d.db.ExecContext(ctx, `ALTER TABLE titles_new RENAME TO titles`)
	} else {
		// Clean up the new table if the old one didn't exist or migration failed
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
	d.db.ExecContext(ctx, `ALTER TABLE albums ADD COLUMN aoty_url TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE albums ADD COLUMN allmusic_url TEXT NOT NULL DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE albums ADD COLUMN allmusic_rating REAL DEFAULT 0`)

	// Anime-specific columns
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
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN series_id TEXT DEFAULT ''`)
	d.db.ExecContext(ctx, `ALTER TABLE books ADD COLUMN series_name TEXT DEFAULT ''`)

	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_release_events_week ON release_events(iso_year, iso_week)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_albums_artist_id ON albums(artist_id)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_albums_mbid ON albums(mbid)`)
	d.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_album_release_events_album_id ON album_release_events(album_id)`)
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

func (d *DB) UpsertTitle(ctx context.Context, t *model.Title) (int64, error) {
	return d.upsertTitle(ctx, d.db, t)
}

func (d *DB) UpsertTitleTx(ctx context.Context, tx *sql.Tx, t *model.Title) (int64, error) {
	return d.upsertTitle(ctx, tx, t)
}

func (d *DB) upsertTitle(ctx context.Context, q querier, t *model.Title) (int64, error) {
	// For anime, use mal_id as the unique key; for movies/TV, use tmdb_id
	if t.MediaType == model.MediaTypeAnime && t.MalID > 0 {
		existing, err := d.getTitleByMalID(ctx, q, t.MalID)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			// Update existing anime title
			_, err := q.ExecContext(ctx, `
				UPDATE titles SET
					tvdb_id = ?, title = ?, year = ?, media_type = ?,
					imdb_id = ?, imdb_rating = ?, rt_url = ?, rt_critics_score = ?,
					rt_audience_score = ?, tmdb_rating = ?, metacritic_score = ?,
					us_rating = ?, original_language = ?, origin_country = ?,
					yt_trailer_views = ?, overview = ?, genres = ?, runtime = ?,
					poster_path = ?, tmdb_title = ?,
					anime_type = ?, anime_episodes = ?, anime_status = ?,
					anime_members = ?, anime_rank = ?, anime_source = ?,
					anime_studio = ?, themes = ?, demographics = ?, streaming = ?,
					collection_id = ?, collection_name = ?
				WHERE mal_id = ?
			`,
				t.TvdbID, t.Title, t.Year, string(t.MediaType),
				t.ImdbID, t.ImdbRating, t.RTURL, t.RTCriticsScore,
				t.RTAudienceScore, t.TmdbRating, t.MetacriticScore,
				t.USRating, t.OriginalLanguage, t.OriginCountry,
				t.YoutubeViews, t.Overview, t.Genres, t.Runtime,
				t.PosterPath, t.TmdbTitle,
				t.AnimeType, t.AnimeEpisodes, t.AnimeStatus,
				t.AnimeMembers, t.AnimeRank, t.AnimeSource,
				t.AnimeStudio, t.Themes, t.Demographics, t.Streaming,
				t.CollectionID, t.CollectionName, t.MalID,
			)
			if err != nil {
				return 0, fmt.Errorf("updating anime title: %w", err)
			}
			return existing.ID, nil
		}
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO titles (tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id, imdb_rating,
		                    rt_url, rt_critics_score, rt_audience_score, tmdb_rating,
		                    metacritic_score, us_rating, original_language, origin_country,
		                    yt_trailer_views, overview, genres, runtime, poster_path, created_at,
		                    anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		                    anime_source, anime_studio, themes, demographics, streaming,
		                    collection_id, collection_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tmdb_id, mal_id) DO UPDATE SET
			title             = excluded.title,
			tmdb_title        = excluded.tmdb_title,
			year              = excluded.year,
			media_type        = excluded.media_type,
			tvdb_id           = excluded.tvdb_id,
			mal_id            = excluded.mal_id,
			imdb_id           = excluded.imdb_id,
			imdb_rating       = excluded.imdb_rating,
			rt_url            = excluded.rt_url,
			rt_critics_score  = excluded.rt_critics_score,
			rt_audience_score = excluded.rt_audience_score,
			tmdb_rating       = excluded.tmdb_rating,
			metacritic_score  = excluded.metacritic_score,
			us_rating         = excluded.us_rating,
			original_language = excluded.original_language,
			origin_country    = excluded.origin_country,
			yt_trailer_views  = excluded.yt_trailer_views,
			overview          = excluded.overview,
			genres            = excluded.genres,
			runtime           = excluded.runtime,
			poster_path       = excluded.poster_path,
			anime_type        = excluded.anime_type,
			anime_episodes    = excluded.anime_episodes,
			anime_status      = excluded.anime_status,
			anime_members     = excluded.anime_members,
			anime_rank        = excluded.anime_rank,
			anime_source      = excluded.anime_source,
			anime_studio      = excluded.anime_studio,
			themes            = excluded.themes,
			demographics      = excluded.demographics,
			streaming         = excluded.streaming,
			collection_id     = excluded.collection_id,
			collection_name   = excluded.collection_name
	`,
		t.TmdbID, t.TvdbID, t.MalID, t.Title, t.TmdbTitle, t.Year, string(t.MediaType), t.ImdbID, t.ImdbRating,
		t.RTURL, t.RTCriticsScore, t.RTAudienceScore, t.TmdbRating,
		t.MetacriticScore, t.USRating, t.OriginalLanguage, t.OriginCountry,
		t.YoutubeViews, t.Overview, t.Genres, t.Runtime, t.PosterPath, t.CreatedAt,
		t.AnimeType, t.AnimeEpisodes, t.AnimeStatus, t.AnimeMembers, t.AnimeRank,
		t.AnimeSource, t.AnimeStudio, t.Themes, t.Demographics, t.Streaming,
		t.CollectionID, t.CollectionName,
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
	return d.getTitleByField(ctx, "tmdb_id", tmdbID)
}

func (d *DB) GetTitleByMalID(ctx context.Context, malID int) (*model.Title, error) {
	return d.getTitleByField(ctx, "mal_id", malID)
}

func (d *DB) getTitleByMalID(ctx context.Context, q querier, malID int) (*model.Title, error) {
	if malID <= 0 {
		return nil, nil
	}
	var t model.Title
	var mediaType string
	var createdAt string
	err := q.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, rt_url, rt_critics_score, rt_audience_score,
		       tmdb_rating, metacritic_score, yt_trailer_views, us_rating, original_language, origin_country,
		       overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
		FROM titles WHERE mal_id = ?
	`, malID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.TmdbRating, &t.MetacriticScore, &t.YoutubeViews, &t.USRating, &t.OriginalLanguage, &t.OriginCountry,
		&t.Overview, &t.Genres, &t.Runtime, &t.PosterPath, &createdAt,
		&t.AnimeType, &t.AnimeEpisodes, &t.AnimeStatus, &t.AnimeMembers, &t.AnimeRank,
		&t.AnimeSource, &t.AnimeStudio, &t.Themes, &t.Demographics, &t.Streaming,
		&t.CollectionID, &t.CollectionName,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying title by mal_id: %w", err)
	}
	t.MediaType = model.MediaType(mediaType)
	t.CreatedAt = createdAt
	return &t, nil
}

func (d *DB) getTitleByField(ctx context.Context, field string, value int) (*model.Title, error) {
	var t model.Title
	var mediaType string
	var createdAt string
	err := d.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, rt_url, rt_critics_score, rt_audience_score,
		       tmdb_rating, metacritic_score, yt_trailer_views, us_rating, original_language, origin_country,
		       overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
		FROM titles WHERE %s = ?
	`, field), value).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.TmdbRating, &t.MetacriticScore, &t.YoutubeViews, &t.USRating, &t.OriginalLanguage, &t.OriginCountry,
		&t.Overview, &t.Genres, &t.Runtime, &t.PosterPath, &createdAt,
		&t.AnimeType, &t.AnimeEpisodes, &t.AnimeStatus, &t.AnimeMembers, &t.AnimeRank,
		&t.AnimeSource, &t.AnimeStudio, &t.Themes, &t.Demographics, &t.Streaming,
		&t.CollectionID, &t.CollectionName,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying title by %s: %w", field, err)
	}
	t.MediaType = model.MediaType(mediaType)
	t.CreatedAt = createdAt
	return &t, nil
}

func (d *DB) ListTitles(ctx context.Context) ([]*model.Title, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, rt_url, rt_critics_score, rt_audience_score,
		       tmdb_rating, metacritic_score, us_rating, original_language, origin_country,
		       yt_trailer_views, overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
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
			&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
			&t.ImdbID, &t.ImdbRating, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
			&t.TmdbRating, &t.MetacriticScore, &t.USRating, &t.OriginalLanguage, &t.OriginCountry,
			&t.YoutubeViews, &t.Overview, &t.Genres, &t.Runtime, &t.PosterPath, &createdAt,
			&t.AnimeType, &t.AnimeEpisodes, &t.AnimeStatus, &t.AnimeMembers, &t.AnimeRank,
			&t.AnimeSource, &t.AnimeStudio, &t.Themes, &t.Demographics, &t.Streaming,
			&t.CollectionID, &t.CollectionName,
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
	return d.createReleaseEvent(ctx, d.db, e)
}

func (d *DB) CreateReleaseEventTx(ctx context.Context, tx *sql.Tx, e *model.ReleaseEvent) (int64, error) {
	return d.createReleaseEvent(ctx, tx, e)
}

func (d *DB) createReleaseEvent(ctx context.Context, q querier, e *model.ReleaseEvent) (int64, error) {
	res, err := q.ExecContext(ctx, `
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
	return d.getLatestReleaseEvent(ctx, d.db, titleID)
}

func (d *DB) GetLatestReleaseEventTx(ctx context.Context, tx *sql.Tx, titleID int64) (*model.ReleaseEvent, error) {
	return d.getLatestReleaseEvent(ctx, tx, titleID)
}

func (d *DB) getLatestReleaseEvent(ctx context.Context, q querier, titleID int64) (*model.ReleaseEvent, error) {
	var e model.ReleaseEvent
	var releaseType, status, prevStatus, createdAt string
	err := q.QueryRowContext(ctx, `
		SELECT id, title_id, source, release_type, release_date, status, previous_status, notes, created_at, iso_year, iso_week
		FROM release_events WHERE title_id = ?
		ORDER BY id DESC LIMIT 1
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
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.original_language, t.origin_country,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at,
		       t.anime_type, t.anime_episodes, t.anime_status, t.anime_members, t.anime_rank,
		       t.anime_source, t.anime_studio, t.themes, t.demographics, t.streaming, t.collection_id, t.collection_name
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.status = 'pending'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending events with titles: %w", err)
	}
	defer rows.Close()

	return scanEventWithTitleRows(rows)
}

func (d *DB) ListApprovedWithTitles(ctx context.Context) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.original_language, t.origin_country,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at,
		       t.anime_type, t.anime_episodes, t.anime_status, t.anime_members, t.anime_rank,
		       t.anime_source, t.anime_studio, t.themes, t.demographics, t.streaming, t.collection_id, t.collection_name
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.status = 'approved'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing approved events with titles: %w", err)
	}
	defer rows.Close()

	return scanEventWithTitleRows(rows)
}

func (d *DB) UpdateReleaseEventStatus(ctx context.Context, id int64, status model.ReleaseStatus) error {
	return d.updateReleaseEventStatus(ctx, d.db, id, status)
}

func (d *DB) UpdateReleaseEventStatusTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus) error {
	return d.updateReleaseEventStatus(ctx, tx, id, status)
}

func (d *DB) updateReleaseEventStatus(ctx context.Context, q querier, id int64, status model.ReleaseStatus) error {
	_, err := q.ExecContext(ctx, `UPDATE release_events SET previous_status = status, status = ? WHERE id = ?`, string(status), id)
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

func (d *DB) GetPreviousAnimeWeek(ctx context.Context, excludeYear, excludeWeek int) (int, int, error) {
	var year, week int
	err := d.db.QueryRowContext(ctx, `
		SELECT e.iso_year, e.iso_week
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE t.media_type = 'anime'
		AND (e.iso_year < ? OR (e.iso_year = ? AND e.iso_week < ?))
		ORDER BY e.iso_year DESC, e.iso_week DESC
		LIMIT 1
	`, excludeYear, excludeYear, excludeWeek).Scan(&year, &week)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return year, week, nil
}

func (d *DB) GetDownloadByTitleID(ctx context.Context, titleID int64) (*model.Download, error) {
	return d.getDownloadByTitleID(ctx, d.db, titleID)
}

func (d *DB) GetDownloadByTitleIDTx(ctx context.Context, tx *sql.Tx, titleID int64) (*model.Download, error) {
	return d.getDownloadByTitleID(ctx, tx, titleID)
}

func (d *DB) getDownloadByTitleID(ctx context.Context, q querier, titleID int64) (*model.Download, error) {
	var dl model.Download
	var status, createdAt string
	err := q.QueryRowContext(ctx, `
		SELECT id, title_id, release_event_id, quality, source_type, codec,
		       info_hash, category, status, client_torrent_id, radarr_id, sonarr_id, created_at
		FROM downloads WHERE title_id = ?
		ORDER BY id DESC LIMIT 1
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
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.original_language, t.origin_country,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at,
		       t.anime_type, t.anime_episodes, t.anime_status, t.anime_members, t.anime_rank,
		       t.anime_source, t.anime_studio, t.themes, t.demographics, t.streaming, t.collection_id, t.collection_name
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
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.original_language, t.origin_country,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at,
		       t.anime_type, t.anime_episodes, t.anime_status, t.anime_members, t.anime_rank,
		       t.anime_source, t.anime_studio, t.themes, t.demographics, t.streaming, t.collection_id, t.collection_name
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

func (d *DB) GetReleaseEventWithTitle(ctx context.Context, id int64) (*EventWithTitle, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.rt_url, t.rt_critics_score, t.rt_audience_score,
		       t.tmdb_rating, t.metacritic_score, t.us_rating,
		       t.original_language, t.origin_country,
		       t.yt_trailer_views, t.overview, t.genres, t.runtime, t.poster_path, t.created_at,
		       t.anime_type, t.anime_episodes, t.anime_status, t.anime_members, t.anime_rank,
		       t.anime_source, t.anime_studio, t.themes, t.demographics, t.streaming, t.collection_id, t.collection_name
		FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.id = ?
	`, id)
	var ev model.ReleaseEvent
	var tl model.Title
	var evRelType, evStatus, evPrevStatus, evCreated string
	var tlMediaType, tlCreated string

	err := row.Scan(
		&ev.ID, &ev.TitleID, &ev.Source, &evRelType, &ev.ReleaseDate,
		&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
		&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.MalID, &tl.Title, &tl.TmdbTitle, &tl.Year, &tlMediaType,
		&tl.ImdbID, &tl.ImdbRating, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
		&tl.TmdbRating, &tl.MetacriticScore, &tl.USRating,
		&tl.OriginalLanguage, &tl.OriginCountry,
		&tl.YoutubeViews, &tl.Overview, &tl.Genres, &tl.Runtime, &tl.PosterPath, &tlCreated,
		&tl.AnimeType, &tl.AnimeEpisodes, &tl.AnimeStatus, &tl.AnimeMembers, &tl.AnimeRank,
		&tl.AnimeSource, &tl.AnimeStudio, &tl.Themes, &tl.Demographics, &tl.Streaming,
		&tl.CollectionID, &tl.CollectionName,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying release event %d: %w", id, err)
	}

	ev.ReleaseType = model.ReleaseType(evRelType)
	ev.Status = model.ReleaseStatus(evStatus)
	ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
	ev.CreatedAt = evCreated

	tl.MediaType = model.MediaType(tlMediaType)
	tl.CreatedAt = tlCreated

	return &EventWithTitle{Event: &ev, Title: &tl}, nil
}

func (d *DB) GetWeekProcessCounts(ctx context.Context, year, week int) (downloaded, approved int, err error) {
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM release_events
		WHERE iso_year = ? AND iso_week = ? AND status = 'downloaded'
	`, year, week).Scan(&downloaded)
	if err != nil {
		return 0, 0, fmt.Errorf("counting downloaded events: %w", err)
	}
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM release_events
		WHERE iso_year = ? AND iso_week = ? AND status IN ('approved', 'downloaded')
	`, year, week).Scan(&approved)
	if err != nil {
		return 0, 0, fmt.Errorf("counting approved events: %w", err)
	}

	var albumDL, albumApproved, bookDL, bookApproved int
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM album_release_events
		WHERE iso_year = ? AND iso_week = ? AND status = 'downloaded'
	`, year, week).Scan(&albumDL)
	if err != nil {
		return 0, 0, fmt.Errorf("counting downloaded album events: %w", err)
	}
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM album_release_events
		WHERE iso_year = ? AND iso_week = ? AND status IN ('approved', 'downloaded')
	`, year, week).Scan(&albumApproved)
	if err != nil {
		return 0, 0, fmt.Errorf("counting approved album events: %w", err)
	}

	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM book_release_events
		WHERE iso_year = ? AND iso_week = ? AND status = 'downloaded'
	`, year, week).Scan(&bookDL)
	if err != nil {
		return 0, 0, fmt.Errorf("counting downloaded book events: %w", err)
	}
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM book_release_events
		WHERE iso_year = ? AND iso_week = ? AND status IN ('approved', 'downloaded')
	`, year, week).Scan(&bookApproved)
	if err != nil {
		return 0, 0, fmt.Errorf("counting approved book events: %w", err)
	}

	downloaded += albumDL + bookDL
	approved += albumApproved + bookApproved
	return downloaded, approved, nil
}

func (d *DB) CountReleaseEventsByWeek(ctx context.Context, year, week int) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM release_events
		WHERE iso_year = ? AND iso_week = ?
	`, year, week).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting release events: %w", err)
	}
	return count, nil
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

// ─── Book API ─────────────────────────────────────────────────────────────

type EventWithBook struct {
	Event  *model.BookReleaseEvent
	Book   *model.Book
	Author *model.Author
}

func (d *DB) upsertAuthor(ctx context.Context, q querier, a *model.Author) (int64, error) {
	// When we have a real OLID, prefer it for dedup, but also check by
	// name first. Different sources (Hardcover vs Open Library) may map
	// the same author to different Open Library IDs (e.g. duplicate OL
	// records for the same person). Checking by name catches these
	// cross-source duplicates.
	if a.OLID != "" && !strings.HasPrefix(a.OLID, "_nm_") {
		existing, err := d.getAuthorByName(ctx, q, a.Name)
		if err != nil {
			return 0, fmt.Errorf("checking existing author by name: %w", err)
		}
		if existing != nil {
			// Found by name — update the existing row with the new
			// OLID and data. This merges the same person across
			// different OL records (e.g. HC's OL3173592A vs OL's
			// own OL1412763A for Maggie O'Farrell).
			_, err := q.ExecContext(ctx, `
				UPDATE authors SET
					hardcover_id = ?, olid = ?, bio = ?, born_date = ?, death_date = ?,
					image_url = ?, identifiers = ?, links = ?
				WHERE id = ?
			`, a.HardcoverID, a.OLID, a.Bio, a.BornDate, a.DeathDate, a.ImageURL, a.Identifiers, a.Links, existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing author by name: %w", err)
			}
			return existing.ID, nil
		}

		res, err := q.ExecContext(ctx, `
			INSERT INTO authors (hardcover_id, olid, name, bio, born_date, death_date, image_url, identifiers, links, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
			ON CONFLICT(olid) DO UPDATE SET
				hardcover_id = excluded.hardcover_id,
				name         = excluded.name,
				bio          = excluded.bio,
				born_date    = excluded.born_date,
				death_date   = excluded.death_date,
				image_url    = excluded.image_url,
				identifiers  = excluded.identifiers,
				links        = excluded.links
		`, a.HardcoverID, a.OLID, a.Name, a.Bio, a.BornDate, a.DeathDate, a.ImageURL, a.Identifiers, a.Links)
		if err != nil {
			return 0, fmt.Errorf("upserting author by olid: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("getting last insert id: %w", err)
		}
		return id, nil
	}

	// No real OLID — check by name to avoid creating duplicate synthetic
	// entries. If found by name and the existing entry has a synthetic OLID,
	// update it with any new data. If the existing entry already has a real
	// OLID, it's a different author with the same name — create a new entry.
	existing, err := d.getAuthorByName(ctx, q, a.Name)
	if err != nil {
		return 0, fmt.Errorf("checking existing author by name: %w", err)
	}
	if existing != nil {
		// Only update if the existing entry has a synthetic OLID, meaning
		// it was created without real enrichment. Otherwise keep both.
		if strings.HasPrefix(existing.OLID, "_nm_") {
			olidVal := a.OLID
			if olidVal == "" {
				olidVal = existing.OLID // keep existing synthetic OLID
			}
			_, err := q.ExecContext(ctx, `
				UPDATE authors SET
					hardcover_id = ?, olid = ?, bio = ?, born_date = ?, death_date = ?,
					image_url = ?, identifiers = ?, links = ?
				WHERE id = ?
			`, a.HardcoverID, olidVal, a.Bio, a.BornDate, a.DeathDate, a.ImageURL, a.Identifiers, a.Links, existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing author: %w", err)
			}
			return existing.ID, nil
		}
		// Existing entry has a real OLID — likely a different author with
		// the same name. Fall through to create a new entry.
	}

	// Generate a deterministic synthetic OLID from the name hash.
	olid := a.OLID
	if olid == "" {
		h := sha256.Sum256([]byte(a.Name))
		olid = fmt.Sprintf("_nm_%x", h[:8])
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO authors (hardcover_id, olid, name, bio, born_date, death_date, image_url, identifiers, links, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(olid) DO UPDATE SET
			hardcover_id = excluded.hardcover_id,
			name         = excluded.name,
			bio          = excluded.bio,
			born_date    = excluded.born_date,
			death_date   = excluded.death_date,
			image_url    = excluded.image_url,
			identifiers  = excluded.identifiers,
			links        = excluded.links
	`, a.HardcoverID, olid, a.Name, a.Bio, a.BornDate, a.DeathDate, a.ImageURL, a.Identifiers, a.Links)
	if err != nil {
		return 0, fmt.Errorf("upserting author: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) UpsertAuthor(ctx context.Context, a *model.Author) (int64, error) {
	return d.upsertAuthor(ctx, d.db, a)
}

func (d *DB) UpsertAuthorTx(ctx context.Context, tx *sql.Tx, a *model.Author) (int64, error) {
	return d.upsertAuthor(ctx, tx, a)
}

func (d *DB) getAuthorByOLID(ctx context.Context, q querier, olid string) (*model.Author, error) {
	var a model.Author
	err := q.QueryRowContext(ctx, `
		SELECT id, hardcover_id, olid, name, bio, born_date, death_date, image_url, identifiers, links, created_at
		FROM authors WHERE olid = ?
	`, olid).Scan(&a.ID, &a.HardcoverID, &a.OLID, &a.Name, &a.Bio, &a.BornDate, &a.DeathDate, &a.ImageURL, &a.Identifiers, &a.Links, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying author by olid: %w", err)
	}
	return &a, nil
}

func (d *DB) GetAuthorByOLID(ctx context.Context, olid string) (*model.Author, error) {
	return d.getAuthorByOLID(ctx, d.db, olid)
}

func (d *DB) getAuthorByName(ctx context.Context, q querier, name string) (*model.Author, error) {
	var a model.Author
	err := q.QueryRowContext(ctx, `
		SELECT id, hardcover_id, olid, name, bio, born_date, death_date, image_url, identifiers, links, created_at
		FROM authors WHERE name = ?
	`, name).Scan(&a.ID, &a.HardcoverID, &a.OLID, &a.Name, &a.Bio, &a.BornDate, &a.DeathDate, &a.ImageURL, &a.Identifiers, &a.Links, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying author by name: %w", err)
	}
	return &a, nil
}

func (d *DB) GetAuthorByName(ctx context.Context, name string) (*model.Author, error) {
	return d.getAuthorByName(ctx, d.db, name)
}

func (d *DB) upsertBook(ctx context.Context, q querier, b *model.Book) (int64, error) {
	// When ISBN-13 is known, prefer it for dedup over the
	// (author_id, title, release_year) conflict key to handle
	// slight title differences across sources.
	if b.ISBN13 != "" {
		existing, err := d.getBookByISBN(ctx, q, b.ISBN13)
		if err != nil {
			return 0, fmt.Errorf("checking existing book by isbn: %w", err)
		}
		if existing != nil {
			// Preserve the existing author_id — ISBN identifies the
			// work, not the author. Different sources may enrich with
			// different authors for the same ISBN (e.g. duplicate
			// OpenLibrary records), so we trust the first binding.
			_, err := q.ExecContext(ctx, `
				UPDATE books SET
					title = ?, subtitle = ?, hardcover_id = ?,
					hardcover_slug = ?, olid = ?, isbn10 = ?, asin = ?,
					pages = ?, audio_seconds = ?, description = ?,
					release_date = ?, release_year = ?,
					rating = ?, ratings_count = ?, shelvings_count = ?, image_url = ?,
					language = ?, publisher = ?, tags = ?, literary_type = ?,
					series_id = ?, series_name = ?
				WHERE id = ?
			`, b.Title, b.Subtitle, b.HardcoverID,
				b.HardcoverSlug, b.OLID, b.ISBN10, b.ASIN,
				b.Pages, b.AudioSeconds, b.Description,
				b.ReleaseDate, b.ReleaseYear,
				b.Rating, b.RatingsCount, b.ShelvingsCount, b.ImageURL,
				b.Language, b.Publisher, b.Tags, b.LiteraryType,
				b.SeriesID, b.SeriesName, existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing book by isbn: %w", err)
			}
			return existing.ID, nil
		}
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO books (author_id, title, subtitle, hardcover_id, hardcover_slug, olid, isbn10, isbn13, asin,
		                   pages, audio_seconds, description, release_date, release_year,
		                   rating, ratings_count, shelvings_count, image_url, language, publisher, tags, literary_type,
		                   series_id, series_name, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, datetime('now'))
		ON CONFLICT(author_id, title, release_year) DO UPDATE SET
			subtitle       = excluded.subtitle,
			hardcover_id   = excluded.hardcover_id,
			hardcover_slug = excluded.hardcover_slug,
			olid           = excluded.olid,
			isbn10         = excluded.isbn10,
			isbn13         = excluded.isbn13,
			asin           = excluded.asin,
			pages          = excluded.pages,
			audio_seconds  = excluded.audio_seconds,
			description    = excluded.description,
			release_date   = excluded.release_date,
			rating         = excluded.rating,
			ratings_count  = excluded.ratings_count,
			shelvings_count = excluded.shelvings_count,
			image_url      = excluded.image_url,
			language       = excluded.language,
			publisher      = excluded.publisher,
			tags           = excluded.tags,
			literary_type  = excluded.literary_type,
			series_id      = excluded.series_id,
			series_name    = excluded.series_name
	`, b.AuthorID, b.Title, b.Subtitle, b.HardcoverID, b.HardcoverSlug, b.OLID, b.ISBN10, b.ISBN13, b.ASIN,
		b.Pages, b.AudioSeconds, b.Description, b.ReleaseDate, b.ReleaseYear,
		b.Rating, b.RatingsCount, b.ShelvingsCount, b.ImageURL, b.Language, b.Publisher, b.Tags, b.LiteraryType,
		b.SeriesID, b.SeriesName)
	if err != nil {
		return 0, fmt.Errorf("upserting book: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) UpsertBook(ctx context.Context, b *model.Book) (int64, error) {
	return d.upsertBook(ctx, d.db, b)
}

func (d *DB) UpsertBookTx(ctx context.Context, tx *sql.Tx, b *model.Book) (int64, error) {
	return d.upsertBook(ctx, tx, b)
}

func (d *DB) getBookByISBN(ctx context.Context, q querier, isbn13 string) (*model.Book, error) {
	if isbn13 == "" {
		return nil, nil
	}
	var b model.Book
	err := q.QueryRowContext(ctx, `
		SELECT id, author_id, title, subtitle, hardcover_id, hardcover_slug, olid, isbn10, isbn13, asin,
		       pages, audio_seconds, description, release_date, release_year,
		       rating, ratings_count, shelvings_count, image_url, language, publisher, tags, literary_type,
		       series_id, series_name, created_at
		FROM books WHERE isbn13 = ?
	`, isbn13).Scan(
		&b.ID, &b.AuthorID, &b.Title, &b.Subtitle, &b.HardcoverID, &b.HardcoverSlug, &b.OLID,
		&b.ISBN10, &b.ISBN13, &b.ASIN,
		&b.Pages, &b.AudioSeconds, &b.Description, &b.ReleaseDate, &b.ReleaseYear,
		&b.Rating, &b.RatingsCount, &b.ShelvingsCount, &b.ImageURL, &b.Language, &b.Publisher, &b.Tags, &b.LiteraryType,
		&b.SeriesID, &b.SeriesName, &b.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying book by isbn: %w", err)
	}
	return &b, nil
}

func (d *DB) GetBookByISBN(ctx context.Context, isbn13 string) (*model.Book, error) {
	return d.getBookByISBN(ctx, d.db, isbn13)
}

func (d *DB) CreateBookReleaseEvent(ctx context.Context, e *model.BookReleaseEvent) (int64, error) {
	return d.createBookReleaseEvent(ctx, d.db, e)
}

func (d *DB) CreateBookReleaseEventTx(ctx context.Context, tx *sql.Tx, e *model.BookReleaseEvent) (int64, error) {
	return d.createBookReleaseEvent(ctx, tx, e)
}

func (d *DB) createBookReleaseEvent(ctx context.Context, q querier, e *model.BookReleaseEvent) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO book_release_events (book_id, source, release_date, format_pref, status, previous_status, notes, iso_year, iso_week, ebook_processed, audiobook_processed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.BookID, e.Source, e.ReleaseDate, string(e.FormatPref), string(e.Status), string(e.PreviousStatus), e.Notes, e.ISOYear, e.ISOWeek, boolToInt(e.EbookProcessed), boolToInt(e.AudiobookProcessed))
	if err != nil {
		return 0, fmt.Errorf("creating book release event: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) GetLatestBookReleaseEvent(ctx context.Context, bookID int64) (*model.BookReleaseEvent, error) {
	return d.getLatestBookReleaseEvent(ctx, d.db, bookID)
}

func (d *DB) GetLatestBookReleaseEventTx(ctx context.Context, tx *sql.Tx, bookID int64) (*model.BookReleaseEvent, error) {
	return d.getLatestBookReleaseEvent(ctx, tx, bookID)
}

func (d *DB) getLatestBookReleaseEvent(ctx context.Context, q querier, bookID int64) (*model.BookReleaseEvent, error) {
	var e model.BookReleaseEvent
	var status, prevStatus, formatPref, createdAt string
	var ebookProc, audiobookProc int
	err := q.QueryRowContext(ctx, `
		SELECT id, book_id, source, release_date, format_pref, status, previous_status, notes, created_at, iso_year, iso_week, ebook_processed, audiobook_processed
		FROM book_release_events WHERE book_id = ?
		ORDER BY id DESC LIMIT 1
	`, bookID).Scan(
		&e.ID, &e.BookID, &e.Source, &e.ReleaseDate, &formatPref,
		&status, &prevStatus, &e.Notes, &createdAt, &e.ISOYear, &e.ISOWeek,
		&ebookProc, &audiobookProc)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying latest book release event: %w", err)
	}
	e.FormatPref = model.BookFormat(formatPref)
	e.Status = model.ReleaseStatus(status)
	e.PreviousStatus = model.ReleaseStatus(prevStatus)
	e.CreatedAt = createdAt
	e.EbookProcessed = ebookProc != 0
	e.AudiobookProcessed = audiobookProc != 0
	return &e, nil
}

func (d *DB) UpdateBookReleaseEventStatus(ctx context.Context, id int64, status model.ReleaseStatus) error {
	_, err := d.db.ExecContext(ctx, `UPDATE book_release_events SET previous_status = status, status = ? WHERE id = ?`, string(status), id)
	return err
}

func (d *DB) UpdateBookReleaseEventStatusTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus) error {
	_, err := tx.ExecContext(ctx, `UPDATE book_release_events SET previous_status = status, status = ? WHERE id = ?`, string(status), id)
	return err
}

func (d *DB) UpdateBookReleaseEventStatusAndFormatTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus, formatPref model.BookFormat) error {
	_, err := tx.ExecContext(ctx, `UPDATE book_release_events SET previous_status = status, status = ?, format_pref = ? WHERE id = ?`, string(status), string(formatPref), id)
	return err
}

func (d *DB) MarkBookFormatProcessed(ctx context.Context, id int64, format model.BookFormat) error {
	var col string
	switch format {
	case model.BookFormatEbook:
		col = "ebook_processed"
	case model.BookFormatAudiobook:
		col = "audiobook_processed"
	default:
		return fmt.Errorf("unknown book format: %s", format)
	}
	_, err := d.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE book_release_events SET %s = 1 WHERE id = ?`, col), id)
	return err
}

func (d *DB) RequeueBookReleaseEvent(ctx context.Context, id int64, source, notes string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE book_release_events SET status = ?, previous_status = status, notes = ?, source = ? WHERE id = ?`,
		string(model.StatusPending), notes, source, id)
	return err
}

func (d *DB) UpdateBookReleaseEventSourceAndNotes(ctx context.Context, id int64, source, notes string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE book_release_events SET source = ?, notes = ? WHERE id = ?`,
		source, notes, id)
	return err
}

func (d *DB) UpdateBookReleaseEventSourceAndNotesTx(ctx context.Context, tx *sql.Tx, id int64, source, notes string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE book_release_events SET source = ?, notes = ? WHERE id = ?`,
		source, notes, id)
	return err
}

func (d *DB) ListPendingBookEventsWithBooks(ctx context.Context) ([]EventWithBook, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.book_id, e.source, e.release_date, e.format_pref,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       e.ebook_processed, e.audiobook_processed,
	b.id, b.author_id, b.title, b.subtitle, b.hardcover_id, b.hardcover_slug, b.olid,
		       b.isbn10, b.isbn13, b.asin,
		       b.pages, b.audio_seconds, b.description, b.release_date, b.release_year,
		       b.rating, b.ratings_count, b.shelvings_count, b.image_url, b.language, b.publisher, b.tags, b.literary_type, b.series_id, b.series_name, b.created_at,
		       a.id, a.hardcover_id, a.olid, a.name, a.bio, a.born_date, a.death_date, a.image_url, a.identifiers, a.links, a.created_at
		FROM book_release_events e
		JOIN books b ON b.id = e.book_id
		JOIN authors a ON a.id = b.author_id
		WHERE e.status = 'pending'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending book events: %w", err)
	}
	defer rows.Close()
	return scanEventWithBookRows(rows)
}

func (d *DB) ListBookEventsByWeek(ctx context.Context, year, week int) ([]EventWithBook, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.book_id, e.source, e.release_date, e.format_pref,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       e.ebook_processed, e.audiobook_processed,
		       b.id, b.author_id, b.title, b.subtitle, b.hardcover_id, b.hardcover_slug, b.olid,
		       b.isbn10, b.isbn13, b.asin,
		       b.pages, b.audio_seconds, b.description, b.release_date, b.release_year,
		       b.rating, b.ratings_count, b.shelvings_count, b.image_url, b.language, b.publisher, b.tags, b.literary_type, b.series_id, b.series_name, b.created_at,
		       a.id, a.hardcover_id, a.olid, a.name, a.bio, a.born_date, a.death_date, a.image_url, a.identifiers, a.links, a.created_at
		FROM book_release_events e
		JOIN books b ON b.id = e.book_id
		JOIN authors a ON a.id = b.author_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		ORDER BY e.created_at DESC
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("listing book events by week: %w", err)
	}
	defer rows.Close()
	return scanEventWithBookRows(rows)
}

func (d *DB) ListBookEventsByWeekAndStatus(ctx context.Context, year, week int, statuses ...model.ReleaseStatus) ([]EventWithBook, error) {
	if len(statuses) == 0 {
		return d.ListBookEventsByWeek(ctx, year, week)
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+2)
	args = append(args, year, week)
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, string(s))
	}
	query := fmt.Sprintf(`
		SELECT e.id, e.book_id, e.source, e.release_date, e.format_pref,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       e.ebook_processed, e.audiobook_processed,
		       b.id, b.author_id, b.title, b.subtitle, b.hardcover_id, b.hardcover_slug, b.olid,
		       b.isbn10, b.isbn13, b.asin,
		       b.pages, b.audio_seconds, b.description, b.release_date, b.release_year,
		       b.rating, b.ratings_count, b.shelvings_count, b.image_url, b.language, b.publisher, b.tags, b.literary_type, b.series_id, b.series_name, b.created_at,
		       a.id, a.hardcover_id, a.olid, a.name, a.bio, a.born_date, a.death_date, a.image_url, a.identifiers, a.links, a.created_at
		FROM book_release_events e
		JOIN books b ON b.id = e.book_id
		JOIN authors a ON a.id = b.author_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		AND e.status IN (%s)
		ORDER BY e.created_at DESC
	`, strings.Join(placeholders, ","))
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing book events by week and status: %w", err)
	}
	defer rows.Close()
	return scanEventWithBookRows(rows)
}

func (d *DB) GetBookReleaseEventWithBook(ctx context.Context, id int64) (*EventWithBook, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT e.id, e.book_id, e.source, e.release_date, e.format_pref,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       e.ebook_processed, e.audiobook_processed,
		       b.id, b.author_id, b.title, b.subtitle, b.hardcover_id, b.hardcover_slug, b.olid,
		       b.isbn10, b.isbn13, b.asin,
		       b.pages, b.audio_seconds, b.description, b.release_date, b.release_year,
		       b.rating, b.ratings_count, b.shelvings_count, b.image_url, b.language, b.publisher, b.tags, b.literary_type, b.series_id, b.series_name, b.created_at,
		       a.id, a.hardcover_id, a.olid, a.name, a.bio, a.born_date, a.death_date, a.image_url, a.identifiers, a.links, a.created_at
		FROM book_release_events e
		JOIN books b ON b.id = e.book_id
		JOIN authors a ON a.id = b.author_id
		WHERE e.id = ?
	`, id)
	var ev model.BookReleaseEvent
	var b model.Book
	var a model.Author
	var evStatus, evPrevStatus, evFormatPref, evCreated string
	var bCreated, aCreated string
	var ebookProc, audiobookProc int

	err := row.Scan(
		&ev.ID, &ev.BookID, &ev.Source, &ev.ReleaseDate, &evFormatPref,
		&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
		&ebookProc, &audiobookProc,
		&b.ID, &b.AuthorID, &b.Title, &b.Subtitle, &b.HardcoverID, &b.HardcoverSlug, &b.OLID,
		&b.ISBN10, &b.ISBN13, &b.ASIN,
		&b.Pages, &b.AudioSeconds, &b.Description, &b.ReleaseDate, &b.ReleaseYear,
		&b.Rating, &b.RatingsCount, &b.ShelvingsCount, &b.ImageURL, &b.Language, &b.Publisher, &b.Tags, &b.LiteraryType, &bCreated,
		&b.SeriesID, &b.SeriesName,
		&a.ID, &a.HardcoverID, &a.OLID, &a.Name, &a.Bio, &a.BornDate, &a.DeathDate, &a.ImageURL, &a.Identifiers, &a.Links, &aCreated,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying book release event %d: %w", id, err)
	}

	ev.FormatPref = model.BookFormat(evFormatPref)
	ev.Status = model.ReleaseStatus(evStatus)
	ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
	ev.CreatedAt = evCreated
	ev.EbookProcessed = ebookProc != 0
	ev.AudiobookProcessed = audiobookProc != 0

	b.CreatedAt = bCreated
	a.CreatedAt = aCreated

	return &EventWithBook{Event: &ev, Book: &b, Author: &a}, nil
}

func (d *DB) CountBookReleaseEventsByWeek(ctx context.Context, year, week int) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM book_release_events
		WHERE iso_year = ? AND iso_week = ?
	`, year, week).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting book release events: %w", err)
	}
	return count, nil
}

func (d *DB) CreateBookDownload(ctx context.Context, dl *model.BookDownload) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO book_downloads (book_id, book_release_event, format, quality, source_type, codec, info_hash, category, status, client_torrent_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, dl.BookID, dl.BookReleaseEvent, string(dl.Format), dl.Quality, dl.SourceType,
		dl.Codec, dl.InfoHash, dl.Category, string(dl.Status), dl.ClientTorrentID)
	if err != nil {
		return 0, fmt.Errorf("creating book download: %w", err)
	}
	return res.LastInsertId()
}

func scanEventWithBookRows(rows *sql.Rows) ([]EventWithBook, error) {
	var results []EventWithBook
	for rows.Next() {
		var ev model.BookReleaseEvent
		var b model.Book
		var a model.Author
		var evStatus, evPrevStatus, evFormatPref, evCreated string
		var bCreated string
		var aCreated string
		var ebookProc, audiobookProc int

		err := rows.Scan(
			&ev.ID, &ev.BookID, &ev.Source, &ev.ReleaseDate, &evFormatPref,
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&ebookProc, &audiobookProc,
			&b.ID, &b.AuthorID, &b.Title, &b.Subtitle, &b.HardcoverID, &b.HardcoverSlug, &b.OLID,
			&b.ISBN10, &b.ISBN13, &b.ASIN,
			&b.Pages, &b.AudioSeconds, &b.Description, &b.ReleaseDate, &b.ReleaseYear,
			&b.Rating, &b.RatingsCount, &b.ShelvingsCount, &b.ImageURL, &b.Language, &b.Publisher, &b.Tags, &b.LiteraryType, &bCreated,
			&b.SeriesID, &b.SeriesName,
			&a.ID, &a.HardcoverID, &a.OLID, &a.Name, &a.Bio, &a.BornDate, &a.DeathDate, &a.ImageURL, &a.Identifiers, &a.Links, &aCreated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning event with book: %w", err)
		}

		ev.FormatPref = model.BookFormat(evFormatPref)
		ev.Status = model.ReleaseStatus(evStatus)
		ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
		ev.CreatedAt = evCreated
		ev.EbookProcessed = ebookProc != 0
		ev.AudiobookProcessed = audiobookProc != 0

		b.CreatedAt = bCreated
		a.CreatedAt = aCreated

		results = append(results, EventWithBook{Event: &ev, Book: &b, Author: &a})
	}
	return results, rows.Err()
}

// ─── Music API ────────────────────────────────────────────────────────────

type EventWithAlbum struct {
	Event  *model.AlbumReleaseEvent
	Album  *model.Album
	Artist *model.Artist
}

func (d *DB) UpsertArtist(ctx context.Context, a *model.Artist) (int64, error) {
	// When no MusicBrainz ID is known, check for an existing artist by name
	// before creating a synthetic MBID. This prevents duplicates when the
	// same artist is later discovered with a real MBID.
	if a.MBID == "" {
		existing, err := d.GetArtistByName(ctx, a.Name)
		if err != nil {
			return 0, fmt.Errorf("checking existing artist by name: %w", err)
		}
		if existing != nil {
			_, err := d.db.ExecContext(ctx, `
				UPDATE artists SET
					lidarr_id = ?, country = ?, artist_type = ?,
					begin_date = ?, end_date = ?, begin_area = ?, area = ?,
					disambiguation = ?, tags = ?, genres = ?, mb_rating = ?
				WHERE id = ?
			`, a.LidarrID, a.Country, a.ArtistType,
				a.BeginDate, a.EndDate, a.BeginArea, a.Area,
				a.Disambiguation, a.Tags, a.Genres, a.MBRating, existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing artist: %w", err)
			}
			return existing.ID, nil
		}
	}

	mbid := a.MBID
	if mbid == "" {
		mbid = noMatchMBID(a.Name)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO artists (mbid, name, lidarr_id, country, artist_type, begin_date, end_date, begin_area, area, disambiguation, tags, genres, mb_rating, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(mbid) DO UPDATE SET
			name           = excluded.name,
			lidarr_id      = excluded.lidarr_id,
			country        = excluded.country,
			artist_type    = excluded.artist_type,
			begin_date     = excluded.begin_date,
			end_date       = excluded.end_date,
			begin_area     = excluded.begin_area,
			area           = excluded.area,
			disambiguation = excluded.disambiguation,
			tags           = excluded.tags,
			genres         = excluded.genres,
			mb_rating      = excluded.mb_rating
	`, mbid, a.Name, a.LidarrID, a.Country, a.ArtistType, a.BeginDate, a.EndDate, a.BeginArea, a.Area, a.Disambiguation, a.Tags, a.Genres, a.MBRating)
	if err != nil {
		return 0, fmt.Errorf("upserting artist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func noMatchMBID(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("_nm_%x", h[:8])
}

func (d *DB) GetArtistByMBID(ctx context.Context, mbid string) (*model.Artist, error) {
	var a model.Artist
	err := d.db.QueryRowContext(ctx, `
		SELECT id, mbid, name, lidarr_id, country, artist_type, begin_date, end_date, begin_area, area, disambiguation, tags, genres, mb_rating, created_at
		FROM artists WHERE mbid = ?
	`, mbid).Scan(&a.ID, &a.MBID, &a.Name, &a.LidarrID,
		&a.Country, &a.ArtistType, &a.BeginDate, &a.EndDate, &a.BeginArea, &a.Area, &a.Disambiguation,
		&a.Tags, &a.Genres, &a.MBRating, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying artist by mbid: %w", err)
	}
	return &a, nil
}

func (d *DB) GetArtistByName(ctx context.Context, name string) (*model.Artist, error) {
	var a model.Artist
	err := d.db.QueryRowContext(ctx, `
		SELECT id, mbid, name, lidarr_id, country, artist_type, begin_date, end_date, begin_area, area, disambiguation, tags, genres, mb_rating, created_at
		FROM artists WHERE name = ?
	`, name).Scan(&a.ID, &a.MBID, &a.Name, &a.LidarrID,
		&a.Country, &a.ArtistType, &a.BeginDate, &a.EndDate, &a.BeginArea, &a.Area, &a.Disambiguation,
		&a.Tags, &a.Genres, &a.MBRating, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying artist by name: %w", err)
	}
	return &a, nil
}

func (d *DB) UpsertAlbum(ctx context.Context, a *model.Album) (int64, error) {
	// When MBID is known, prefer it for dedup. Different scrapers may
	// associate the same release group with different artists (e.g. a
	// listing page error). MBID is the canonical identifier, so if an
	// album with the same MBID already lives under a different artist,
	// update that row rather than creating a second album.
	if a.MBID != "" {
		existing, err := d.GetAlbumByMBID(ctx, a.MBID)
		if err != nil {
			return 0, fmt.Errorf("checking existing album by mbid: %w", err)
		}
		if existing != nil {
			_, err := d.db.ExecContext(ctx, `
				UPDATE albums SET
					artist_id = ?, title = ?, year = ?, album_type = ?,
					release_date = ?, genres = ?, overview = ?, poster_path = ?,
					aoty_url = ?, allmusic_url = ?,
					aoty_critic_score = ?, aoty_critic_count = ?,
					aoty_user_score = ?, aoty_user_count = ?,
					aoty_must_hear = ?, allmusic_rating = ?, mb_rating = ?
				WHERE id = ?
			`, a.ArtistID, a.Title, a.Year, string(a.AlbumType),
				a.ReleaseDate, a.Genres, a.Overview, a.PosterPath,
				a.AOTYURL, a.AllMusicURL,
				a.AOTYCriticScore, a.AOTYCriticCount,
				a.AOTYUserScore, a.AOTYUserCount,
				boolToInt(a.AOTYMustHear), a.AllMusicRating, a.MBRating,
				existing.ID)
			if err != nil {
				return 0, fmt.Errorf("updating existing album by mbid: %w", err)
			}
			return existing.ID, nil
		}
	}

	res, err := d.db.ExecContext(ctx, `
		INSERT INTO albums (artist_id, title, year, mbid, album_type,
		                    release_date, genres, overview, poster_path,
		                    aoty_url, allmusic_url,
		                    aoty_critic_score, aoty_critic_count,
		                    aoty_user_score, aoty_user_count,
		                    aoty_must_hear, allmusic_rating, mb_rating, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(artist_id, title, year) DO UPDATE SET
			mbid              = excluded.mbid,
			album_type        = excluded.album_type,
			release_date      = excluded.release_date,
			genres            = excluded.genres,
			overview          = excluded.overview,
			poster_path       = excluded.poster_path,
			aoty_url          = excluded.aoty_url,
			allmusic_url      = excluded.allmusic_url,
			aoty_critic_score = excluded.aoty_critic_score,
			aoty_critic_count = excluded.aoty_critic_count,
			aoty_user_score   = excluded.aoty_user_score,
			aoty_user_count   = excluded.aoty_user_count,
			aoty_must_hear    = excluded.aoty_must_hear,
			allmusic_rating   = excluded.allmusic_rating,
			mb_rating         = excluded.mb_rating
	`, a.ArtistID, a.Title, a.Year, a.MBID, string(a.AlbumType),
		a.ReleaseDate, a.Genres, a.Overview, a.PosterPath,
		a.AOTYURL, a.AllMusicURL,
		a.AOTYCriticScore, a.AOTYCriticCount,
		a.AOTYUserScore, a.AOTYUserCount,
		boolToInt(a.AOTYMustHear), a.AllMusicRating, a.MBRating)
	if err != nil {
		return 0, fmt.Errorf("upserting album: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) GetAlbumByMBID(ctx context.Context, mbid string) (*model.Album, error) {
	var a model.Album
	var albumType, createdAt string
	var mustHear int
	err := d.db.QueryRowContext(ctx, `
		SELECT id, artist_id, title, year, mbid, album_type,
		       release_date, genres, overview, poster_path,
		       aoty_url, allmusic_url,
		       aoty_critic_score, aoty_critic_count,
		       aoty_user_score, aoty_user_count,
		       aoty_must_hear, allmusic_rating, mb_rating, created_at
		FROM albums WHERE mbid = ?
	`, mbid).Scan(
		&a.ID, &a.ArtistID, &a.Title, &a.Year, &a.MBID, &albumType,
		&a.ReleaseDate, &a.Genres, &a.Overview, &a.PosterPath,
		&a.AOTYURL, &a.AllMusicURL,
		&a.AOTYCriticScore, &a.AOTYCriticCount,
		&a.AOTYUserScore, &a.AOTYUserCount,
		&mustHear, &a.AllMusicRating, &a.MBRating, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying album by mbid: %w", err)
	}
	a.AlbumType = model.AlbumType(albumType)
	a.AOTYMustHear = mustHear > 0
	a.CreatedAt = createdAt
	return &a, nil
}

func (d *DB) CreateAlbumReleaseEvent(ctx context.Context, e *model.AlbumReleaseEvent) (int64, error) {
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO album_release_events (album_id, source, release_date, status, previous_status, notes, iso_year, iso_week)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, e.AlbumID, e.Source, e.ReleaseDate, string(e.Status), string(e.PreviousStatus), e.Notes, e.ISOYear, e.ISOWeek)
	if err != nil {
		return 0, fmt.Errorf("creating album release event: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) GetLatestAlbumReleaseEvent(ctx context.Context, albumID int64) (*model.AlbumReleaseEvent, error) {
	var e model.AlbumReleaseEvent
	var status, prevStatus, createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, album_id, source, release_date, status, previous_status, notes, created_at, iso_year, iso_week
		FROM album_release_events WHERE album_id = ?
		ORDER BY id DESC LIMIT 1
	`, albumID).Scan(
		&e.ID, &e.AlbumID, &e.Source, &e.ReleaseDate,
		&status, &prevStatus, &e.Notes, &createdAt, &e.ISOYear, &e.ISOWeek)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying latest album release event: %w", err)
	}
	e.Status = model.ReleaseStatus(status)
	e.PreviousStatus = model.ReleaseStatus(prevStatus)
	e.CreatedAt = createdAt
	return &e, nil
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

func (d *DB) ListPendingAlbumEventsWithAlbums(ctx context.Context) ([]EventWithAlbum, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.album_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       a.id, a.artist_id, a.title, a.year, a.mbid, a.album_type,
		       a.release_date, a.genres, a.overview, a.poster_path, a.aoty_url, a.allmusic_url,
		       a.aoty_critic_score, a.aoty_critic_count, a.aoty_user_score, a.aoty_user_count,
		       a.aoty_must_hear, a.allmusic_rating, a.mb_rating, a.created_at,
		       ar.id, ar.mbid, ar.name, ar.lidarr_id,
		       ar.country, ar.artist_type, ar.begin_date, ar.end_date, ar.begin_area, ar.area, ar.disambiguation, ar.tags, ar.genres, ar.mb_rating, ar.created_at
		FROM album_release_events e
		JOIN albums a ON a.id = e.album_id
		JOIN artists ar ON ar.id = a.artist_id
		WHERE e.status = 'pending'
		ORDER BY e.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending album events: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumRows(rows)
}

func (d *DB) ListAlbumEventsByWeek(ctx context.Context, year, week int) ([]EventWithAlbum, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.album_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       a.id, a.artist_id, a.title, a.year, a.mbid, a.album_type,
		       a.release_date, a.genres, a.overview, a.poster_path, a.aoty_url, a.allmusic_url,
		       a.aoty_critic_score, a.aoty_critic_count, a.aoty_user_score, a.aoty_user_count,
		       a.aoty_must_hear, a.allmusic_rating, a.mb_rating, a.created_at,
		       ar.id, ar.mbid, ar.name, ar.lidarr_id,
		       ar.country, ar.artist_type, ar.begin_date, ar.end_date, ar.begin_area, ar.area, ar.disambiguation, ar.tags, ar.genres, ar.mb_rating, ar.created_at
		FROM album_release_events e
		JOIN albums a ON a.id = e.album_id
		JOIN artists ar ON ar.id = a.artist_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		ORDER BY e.created_at DESC
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("listing album events by week: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumRows(rows)
}

func (d *DB) ListAlbumEventsByWeekAndStatus(ctx context.Context, year, week int, statuses ...model.ReleaseStatus) ([]EventWithAlbum, error) {
	if len(statuses) == 0 {
		return d.ListAlbumEventsByWeek(ctx, year, week)
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+2)
	args = append(args, year, week)
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, string(s))
	}
	query := fmt.Sprintf(`
		SELECT e.id, e.album_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       a.id, a.artist_id, a.title, a.year, a.mbid, a.album_type,
		       a.release_date, a.genres, a.overview, a.poster_path, a.aoty_url, a.allmusic_url,
		       a.aoty_critic_score, a.aoty_critic_count, a.aoty_user_score, a.aoty_user_count,
		       a.aoty_must_hear, a.allmusic_rating, a.mb_rating, a.created_at,
		       ar.id, ar.mbid, ar.name, ar.lidarr_id,
		       ar.country, ar.artist_type, ar.begin_date, ar.end_date, ar.begin_area, ar.area, ar.disambiguation, ar.tags, ar.genres, ar.mb_rating, ar.created_at
		FROM album_release_events e
		JOIN albums a ON a.id = e.album_id
		JOIN artists ar ON ar.id = a.artist_id
		WHERE e.iso_year = ? AND e.iso_week = ?
		AND e.status IN (%s)
		ORDER BY e.created_at DESC
	`, strings.Join(placeholders, ","))
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing album events by week and status: %w", err)
	}
	defer rows.Close()
	return scanEventWithAlbumRows(rows)
}

func (d *DB) GetAlbumReleaseEventWithAlbum(ctx context.Context, id int64) (*EventWithAlbum, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT e.id, e.album_id, e.source, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		       a.id, a.artist_id, a.title, a.year, a.mbid, a.album_type,
		       a.release_date, a.genres, a.overview, a.poster_path, a.aoty_url, a.allmusic_url,
		       a.aoty_critic_score, a.aoty_critic_count, a.aoty_user_score, a.aoty_user_count,
		       a.aoty_must_hear, a.allmusic_rating, a.mb_rating, a.created_at,
		       ar.id, ar.mbid, ar.name, ar.lidarr_id,
		       ar.country, ar.artist_type, ar.begin_date, ar.end_date, ar.begin_area, ar.area, ar.disambiguation, ar.tags, ar.genres, ar.mb_rating, ar.created_at
		FROM album_release_events e
		JOIN albums a ON a.id = e.album_id
		JOIN artists ar ON ar.id = a.artist_id
		WHERE e.id = ?
	`, id)
	var ev model.AlbumReleaseEvent
	var al model.Album
	var ar model.Artist
	var evStatus, evPrevStatus, evCreated string
	var alAlbumType, alCreated string
	var alMustHear int
	var arCreated string

	err := row.Scan(
		&ev.ID, &ev.AlbumID, &ev.Source, &ev.ReleaseDate,
		&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
		&al.ID, &al.ArtistID, &al.Title, &al.Year, &al.MBID, &alAlbumType,
		&al.ReleaseDate, &al.Genres, &al.Overview, &al.PosterPath, &al.AOTYURL, &al.AllMusicURL,
		&al.AOTYCriticScore, &al.AOTYCriticCount, &al.AOTYUserScore, &al.AOTYUserCount,
		&alMustHear, &al.AllMusicRating, &al.MBRating, &alCreated,
		&ar.ID, &ar.MBID, &ar.Name, &ar.LidarrID,
		&ar.Country, &ar.ArtistType, &ar.BeginDate, &ar.EndDate, &ar.BeginArea, &ar.Area, &ar.Disambiguation, &ar.Tags, &ar.Genres, &ar.MBRating, &arCreated,
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

	al.AlbumType = model.AlbumType(alAlbumType)
	al.AOTYMustHear = alMustHear > 0
	al.CreatedAt = alCreated

	ar.CreatedAt = arCreated

	return &EventWithAlbum{Event: &ev, Album: &al, Artist: &ar}, nil
}

func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("querying setting %s: %w", key, err)
	}
	return value, nil
}

func scanEventWithAlbumRows(rows *sql.Rows) ([]EventWithAlbum, error) {
	var results []EventWithAlbum
	for rows.Next() {
		var ev model.AlbumReleaseEvent
		var al model.Album
		var ar model.Artist
		var evStatus, evPrevStatus, evCreated string
		var alAlbumType, alCreated string
		var alMustHear int
		var arCreated string

		err := rows.Scan(
			&ev.ID, &ev.AlbumID, &ev.Source, &ev.ReleaseDate,
			&evStatus, &evPrevStatus, &ev.Notes, &evCreated, &ev.ISOYear, &ev.ISOWeek,
			&al.ID, &al.ArtistID, &al.Title, &al.Year, &al.MBID, &alAlbumType,
			&al.ReleaseDate, &al.Genres, &al.Overview, &al.PosterPath, &al.AOTYURL, &al.AllMusicURL,
			&al.AOTYCriticScore, &al.AOTYCriticCount, &al.AOTYUserScore, &al.AOTYUserCount,
			&alMustHear, &al.AllMusicRating, &al.MBRating, &alCreated,
			&ar.ID, &ar.MBID, &ar.Name, &ar.LidarrID,
			&ar.Country, &ar.ArtistType, &ar.BeginDate, &ar.EndDate, &ar.BeginArea, &ar.Area, &ar.Disambiguation, &ar.Tags, &ar.Genres, &ar.MBRating, &arCreated,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning event with album: %w", err)
		}

		ev.Status = model.ReleaseStatus(evStatus)
		ev.PreviousStatus = model.ReleaseStatus(evPrevStatus)
		ev.CreatedAt = evCreated

		al.AlbumType = model.AlbumType(alAlbumType)
		al.AOTYMustHear = alMustHear > 0
		al.CreatedAt = alCreated

		ar.CreatedAt = arCreated

		results = append(results, EventWithAlbum{Event: &ev, Album: &al, Artist: &ar})
	}
	return results, rows.Err()
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
			&tl.ID, &tl.TmdbID, &tl.TvdbID, &tl.MalID, &tl.Title, &tl.TmdbTitle, &tl.Year, &tlMediaType,
			&tl.ImdbID, &tl.ImdbRating, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
			&tl.TmdbRating, &tl.MetacriticScore, &tl.USRating,
			&tl.OriginalLanguage, &tl.OriginCountry,
			&tl.YoutubeViews, &tl.Overview, &tl.Genres, &tl.Runtime, &tl.PosterPath, &tlCreated,
			&tl.AnimeType, &tl.AnimeEpisodes, &tl.AnimeStatus, &tl.AnimeMembers, &tl.AnimeRank,
			&tl.AnimeSource, &tl.AnimeStudio, &tl.Themes, &tl.Demographics, &tl.Streaming,
			&tl.CollectionID, &tl.CollectionName,
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

// ─── Library Cache ─────────────────────────────────────────────────────────

type LibraryCache struct {
	ID        int64
	Source    string
	ExtID     string
	ArrID     int64
	ArrTitle  string
	Details   string
	FetchedAt string
}

func (d *DB) UpsertLibraryCache(ctx context.Context, source, extID string, arrID int64, arrTitle, details string) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO library_cache (source, ext_id, arr_id, arr_title, details, fetched_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(source, ext_id) DO UPDATE SET
			arr_id     = excluded.arr_id,
			arr_title  = excluded.arr_title,
			details    = excluded.details,
			fetched_at = datetime('now')
	`, source, extID, arrID, arrTitle, details)
	if err != nil {
		return fmt.Errorf("upserting library cache: %w", err)
	}
	return nil
}

func (d *DB) BulkUpsertLibraryCache(ctx context.Context, entries []LibraryCache) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO library_cache (source, ext_id, arr_id, arr_title, details, fetched_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(source, ext_id) DO UPDATE SET
			arr_id     = excluded.arr_id,
			arr_title  = excluded.arr_title,
			details    = excluded.details,
			fetched_at = datetime('now')
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, e.Source, e.ExtID, e.ArrID, e.ArrTitle, e.Details); err != nil {
			return fmt.Errorf("inserting cache entry %s/%s: %w", e.Source, e.ExtID, err)
		}
	}
	return tx.Commit()
}

func (d *DB) GetLibraryCache(ctx context.Context, source, extID string) (*LibraryCache, error) {
	var c LibraryCache
	err := d.db.QueryRowContext(ctx, `
		SELECT id, source, ext_id, arr_id, arr_title, details, fetched_at
		FROM library_cache WHERE source = ? AND ext_id = ?
	`, source, extID).Scan(&c.ID, &c.Source, &c.ExtID, &c.ArrID, &c.ArrTitle, &c.Details, &c.FetchedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying library cache: %w", err)
	}
	return &c, nil
}

func (d *DB) GetLibraryCacheMap(ctx context.Context, lookups []struct{ Source, ExtID string }) (map[string]*LibraryCache, error) {
	if len(lookups) == 0 {
		return nil, nil
	}

	result := make(map[string]*LibraryCache, len(lookups))
	seen := make(map[string]bool)

	for _, l := range lookups {
		key := l.Source + ":" + l.ExtID
		if seen[key] {
			continue
		}
		seen[key] = true
		c, err := d.GetLibraryCache(ctx, l.Source, l.ExtID)
		if err != nil {
			return nil, err
		}
		if c != nil {
			result[key] = c
		}
	}
	return result, nil
}

func (d *DB) UpdateTitleTvdbID(ctx context.Context, titleID int64, tvdbID int) error {
	_, err := d.db.ExecContext(ctx, `UPDATE titles SET tvdb_id = ? WHERE id = ?`, tvdbID, titleID)
	if err != nil {
		return fmt.Errorf("updating title tvdb_id: %w", err)
	}
	return nil
}

func (d *DB) PurgeLibraryCache(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM library_cache`)
	if err != nil {
		return fmt.Errorf("purging library cache: %w", err)
	}
	return nil
}

func (d *DB) GetAllLibraryCache(ctx context.Context) ([]LibraryCache, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, source, ext_id, arr_id, arr_title, details, fetched_at
		FROM library_cache ORDER BY source, ext_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying all library cache: %w", err)
	}
	defer rows.Close()

	var caches []LibraryCache
	for rows.Next() {
		var c LibraryCache
		if err := rows.Scan(&c.ID, &c.Source, &c.ExtID, &c.ArrID, &c.ArrTitle, &c.Details, &c.FetchedAt); err != nil {
			return nil, fmt.Errorf("scanning library cache: %w", err)
		}
		caches = append(caches, c)
	}
	return caches, rows.Err()
}

func (d *DB) GetLibraryCacheFetchedAt(ctx context.Context, source string) (string, error) {
	var fetchedAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT MAX(fetched_at) FROM library_cache WHERE source = ?
	`, source).Scan(&fetchedAt)
	if err != nil {
		return "", nil
	}
	return fetchedAt, nil
}

// BookISBNEntry pairs an event with its book's ISBN13 for dedup scanning.
type BookISBNEntry struct {
	ISBN    string
	BookID  int64
	EventID int64
}

// FindPendingBookEventsByISBN returns all pending book release events grouped
// by ISBN13, for use in post-scrape deduplication. Only groups with more than
// one unique book_id are returned.
func (d *DB) FindPendingBookEventsByISBN(ctx context.Context) (map[string][]BookISBNEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT b.isbn13, b.id, e.id
		FROM books b
		JOIN book_release_events e ON e.book_id = b.id
		WHERE b.isbn13 != '' AND e.status = 'pending'
		ORDER BY b.isbn13, b.id, e.id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying pending events by isbn: %w", err)
	}
	defer rows.Close()

	raw := make(map[string][]BookISBNEntry)
	for rows.Next() {
		var isbn string
		var bookID, eventID int64
		if err := rows.Scan(&isbn, &bookID, &eventID); err != nil {
			return nil, fmt.Errorf("scanning pending event row: %w", err)
		}
		raw[isbn] = append(raw[isbn], BookISBNEntry{
			ISBN:    isbn,
			BookID:  bookID,
			EventID: eventID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter to only ISBNs with multiple distinct book_ids
	results := make(map[string][]BookISBNEntry)
	for isbn, entries := range raw {
		seen := make(map[int64]bool)
		for _, e := range entries {
			seen[e.BookID] = true
		}
		if len(seen) >= 2 {
			results[isbn] = entries
		}
	}
	return results, nil
}

// DeleteBookReleaseEvent deletes a book_release_event by ID.
func (d *DB) DeleteBookReleaseEvent(ctx context.Context, id int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM book_release_events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book release event %d: %w", id, err)
	}
	return nil
}

// DeleteBook deletes a book by ID. Only safe to call after its release events
// have been re-pointed or deleted.
func (d *DB) DeleteBook(ctx context.Context, id int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book %d: %w", id, err)
	}
	return nil
}
