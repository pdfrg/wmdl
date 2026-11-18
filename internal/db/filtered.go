package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
)

// rebuildEventTablesForFiltered adds the 'filtered' value to each event
// table's status CHECK constraint. SQLite cannot alter a CHECK constraint, so
// the tables are rebuilt (create new → copy → drop → rename). Each rebuild is
// guarded by inspecting sqlite_master, so this is a no-op on up-to-date
// databases.
func (d *DB) rebuildEventTablesForFiltered(ctx context.Context) {
	d.rebuildEventTableForFiltered(ctx, "release_events",
		`CREATE TABLE release_events_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			title_id        INTEGER NOT NULL REFERENCES titles(id),
			source          TEXT NOT NULL,
			release_type    TEXT NOT NULL DEFAULT ''
			                CHECK(release_type IN ('physical','streaming','')),
			release_date    TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'pending'
			                CHECK(status IN ('pending','approved','rejected','downloaded','filtered')),
			previous_status TEXT DEFAULT '',
			notes           TEXT DEFAULT '',
			filter_reason   TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			iso_year        INTEGER DEFAULT 0,
			iso_week        INTEGER DEFAULT 0
		)`,
		`INSERT INTO release_events_new
			(id, title_id, source, release_type, release_date, status, previous_status,
			 notes, created_at, iso_year, iso_week)
		 SELECT id, title_id, source, release_type, release_date, status, previous_status,
			 notes, created_at, iso_year, iso_week
		 FROM release_events`)

	d.rebuildEventTableForFiltered(ctx, "album_release_events",
		`CREATE TABLE album_release_events_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id      INTEGER NOT NULL REFERENCES album_releases(id),
			source          TEXT NOT NULL,
			release_date    TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'pending'
			                CHECK(status IN ('pending','approved','rejected','downloaded','filtered')),
			previous_status TEXT DEFAULT '',
			notes           TEXT DEFAULT '',
			filter_reason   TEXT NOT NULL DEFAULT '',
			created_at      TEXT DEFAULT (datetime('now')),
			iso_year        INTEGER DEFAULT 0,
			iso_week        INTEGER DEFAULT 0
		)`,
		`INSERT INTO album_release_events_new
			(id, release_id, source, release_date, status, previous_status, notes,
			 created_at, iso_year, iso_week)
		 SELECT id, release_id, source, release_date, status, previous_status, notes,
			 created_at, iso_year, iso_week
		 FROM album_release_events`)

	d.rebuildEventTableForFiltered(ctx, "book_release_events",
		`CREATE TABLE book_release_events_new (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			book_id             INTEGER NOT NULL REFERENCES books(id),
			source              TEXT NOT NULL,
			release_date        TEXT NOT NULL DEFAULT '',
			format_pref         TEXT NOT NULL DEFAULT 'both'
			                    CHECK(format_pref IN ('ebook','audiobook','both')),
			status              TEXT NOT NULL DEFAULT 'pending'
			                    CHECK(status IN ('pending','approved','rejected','downloaded','filtered')),
			previous_status     TEXT DEFAULT '',
			notes               TEXT DEFAULT '',
			filter_reason       TEXT NOT NULL DEFAULT '',
			created_at          TEXT DEFAULT (datetime('now')),
			iso_year            INTEGER DEFAULT 0,
			iso_week            INTEGER DEFAULT 0,
			ebook_processed     INTEGER NOT NULL DEFAULT 0,
			audiobook_processed INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO book_release_events_new
			(id, book_id, source, release_date, format_pref, status, previous_status,
			 notes, created_at, iso_year, iso_week, ebook_processed, audiobook_processed)
		 SELECT id, book_id, source, release_date, format_pref, status, previous_status,
			 notes, created_at, iso_year, iso_week, ebook_processed, audiobook_processed
		 FROM book_release_events`)
}

func (d *DB) rebuildEventTableForFiltered(ctx context.Context, name, createSQL, copySQL string) {
	var sqlText string
	if err := d.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&sqlText); err != nil {
		return
	}
	if containsFilteredStatus(sqlText) {
		return
	}

	newTable := name + "_new"
	d.migrateExec(ctx, `DROP TABLE IF EXISTS `+newTable)
	d.migrateExec(ctx, createSQL)

	if _, err := d.db.ExecContext(ctx, copySQL); err != nil {
		d.migrateExec(ctx, `DROP TABLE IF EXISTS `+newTable)
		return
	}

	d.migrateExec(ctx, `DROP TABLE `+name)
	d.migrateExec(ctx, `ALTER TABLE `+newTable+` RENAME TO `+name)
}

func containsFilteredStatus(tableSQL string) bool {
	return strings.Contains(tableSQL, "'filtered'") || strings.Contains(tableSQL, `"filtered"`)
}

// FilteredReasons holds the filter_reason column for filtered events, keyed by
// event ID, for each media family. Review attaches these to the in-memory
// items so the reason is available without widening every event SELECT.
type FilteredReasons struct {
	Video map[int64]string
	Album map[int64]string
	Book  map[int64]string
}

// FilteredReasonsForWeek loads filter reasons for filtered events in a week.
// Passing year=0 loads reasons for every filtered event (used when review is
// not scoped to a week).
func (d *DB) FilteredReasonsForWeek(ctx context.Context, year, week int) (*FilteredReasons, error) {
	out := &FilteredReasons{
		Video: make(map[int64]string),
		Album: make(map[int64]string),
		Book:  make(map[int64]string),
	}

	load := func(table, dst string) error {
		query := fmt.Sprintf(`SELECT id, filter_reason FROM %s WHERE status = 'filtered'`, table)
		args := []any{}
		if year != 0 {
			query += ` AND iso_year = ? AND iso_week = ?`
			args = append(args, year, week)
		}
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("loading filtered reasons for %s: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var reason string
			if err := rows.Scan(&id, &reason); err != nil {
				return fmt.Errorf("scanning filtered reason for %s: %w", table, err)
			}
			switch dst {
			case "video":
				out.Video[id] = reason
			case "album":
				out.Album[id] = reason
			case "book":
				out.Book[id] = reason
			}
		}
		return rows.Err()
	}

	if err := load("release_events", "video"); err != nil {
		return nil, err
	}
	if err := load("album_release_events", "album"); err != nil {
		return nil, err
	}
	if err := load("book_release_events", "book"); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateReleaseEventStatusAndFilterReasonTx updates an event's status and
// filter reason atomically. Used when a filtered item starts passing filters
// (status → pending, reason cleared).
func (d *DB) UpdateReleaseEventStatusAndFilterReasonTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus, reason string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE release_events SET previous_status = status, status = ?, filter_reason = ? WHERE id = ?`,
		string(status), reason, id)
	return err
}

func (d *DB) UpdateAlbumReleaseEventStatusAndFilterReason(ctx context.Context, id int64, status model.ReleaseStatus, reason string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE album_release_events SET previous_status = status, status = ?, filter_reason = ? WHERE id = ?`,
		string(status), reason, id)
	return err
}

func (d *DB) UpdateBookReleaseEventStatusAndFilterReasonTx(ctx context.Context, tx *sql.Tx, id int64, status model.ReleaseStatus, reason string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE book_release_events SET previous_status = status, status = ?, filter_reason = ? WHERE id = ?`,
		string(status), reason, id)
	return err
}
