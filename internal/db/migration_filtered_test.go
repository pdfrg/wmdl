package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
)

// downgradeEventTables rewrites the three event tables with the pre-"filtered"
// CHECK constraint, producing the schema shape an existing installation has
// before this migration. Titles/authors/etc. are left untouched.
func downgradeEventTables(t *testing.T, d *DB) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`CREATE TABLE release_events_old (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title_id INTEGER NOT NULL REFERENCES titles(id),
			source TEXT NOT NULL,
			release_type TEXT NOT NULL DEFAULT '' CHECK(release_type IN ('physical','streaming','')),
			release_date TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','downloaded')),
			previous_status TEXT DEFAULT '',
			notes TEXT DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			iso_year INTEGER DEFAULT 0,
			iso_week INTEGER DEFAULT 0
		)`,
		`INSERT INTO release_events_old (id, title_id, source, release_type, release_date, status, previous_status, notes, created_at, iso_year, iso_week)
		 SELECT id, title_id, source, release_type, release_date, status, previous_status, notes, created_at, iso_year, iso_week FROM release_events`,
		`DROP TABLE release_events`,
		`ALTER TABLE release_events_old RENAME TO release_events`,

		`CREATE TABLE album_release_events_old (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			release_id INTEGER NOT NULL REFERENCES album_releases(id),
			source TEXT NOT NULL,
			release_date TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','downloaded')),
			previous_status TEXT DEFAULT '',
			notes TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			iso_year INTEGER DEFAULT 0,
			iso_week INTEGER DEFAULT 0
		)`,
		`INSERT INTO album_release_events_old (id, release_id, source, release_date, status, previous_status, notes, created_at, iso_year, iso_week)
		 SELECT id, release_id, source, release_date, status, previous_status, notes, created_at, iso_year, iso_week FROM album_release_events`,
		`DROP TABLE album_release_events`,
		`ALTER TABLE album_release_events_old RENAME TO album_release_events`,

		`CREATE TABLE book_release_events_old (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			book_id INTEGER NOT NULL REFERENCES books(id),
			source TEXT NOT NULL,
			release_date TEXT NOT NULL DEFAULT '',
			format_pref TEXT NOT NULL DEFAULT 'both' CHECK(format_pref IN ('ebook','audiobook','both')),
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','downloaded')),
			previous_status TEXT DEFAULT '',
			notes TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			iso_year INTEGER DEFAULT 0,
			iso_week INTEGER DEFAULT 0,
			ebook_processed INTEGER NOT NULL DEFAULT 0,
			audiobook_processed INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO book_release_events_old (id, book_id, source, release_date, format_pref, status, previous_status, notes, created_at, iso_year, iso_week, ebook_processed, audiobook_processed)
		 SELECT id, book_id, source, release_date, format_pref, status, previous_status, notes, created_at, iso_year, iso_week, ebook_processed, audiobook_processed FROM book_release_events`,
		`DROP TABLE book_release_events`,
		`ALTER TABLE book_release_events_old RENAME TO book_release_events`,
	}
	for _, stmt := range stmts {
		if _, err := d.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("downgrade event tables: %v\n%s", err, stmt)
		}
	}
}

func TestMigrateAddsFilteredStatus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// Build a current DB, add one pending event per family, then downgrade the
	// event tables so the next Open exercises the rebuild migration.
	d, err := Open(path)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	titleID, err := d.UpsertTitle(ctx, &model.Title{TmdbID: 1, Title: "Old Movie", MediaType: model.MediaTypeMovie})
	if err != nil {
		t.Fatalf("UpsertTitle: %v", err)
	}
	if _, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{TitleID: titleID, Source: "old", Status: model.StatusPending, ISOYear: 2026, ISOWeek: 1}); err != nil {
		t.Fatalf("seed release event: %v", err)
	}
	albumID, err := d.UpsertAlbumRelease(ctx, &model.AlbumRelease{ArtistName: "Artist", Title: "Album"})
	if err != nil {
		t.Fatalf("UpsertAlbumRelease: %v", err)
	}
	if _, err := d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{ReleaseID: albumID, Source: "old", Status: model.StatusPending, ISOYear: 2026, ISOWeek: 1}); err != nil {
		t.Fatalf("seed album event: %v", err)
	}
	authorID, err := d.UpsertAuthor(ctx, &model.Author{Name: "Author", OLID: "ol-1"})
	if err != nil {
		t.Fatalf("UpsertAuthor: %v", err)
	}
	bookID, err := d.UpsertBook(ctx, &model.Book{AuthorID: authorID, Title: "Book"})
	if err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	if _, err := d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{BookID: bookID, Source: "old", Status: model.StatusPending, FormatPref: model.BookFormatBoth, ISOYear: 2026, ISOWeek: 1}); err != nil {
		t.Fatalf("seed book event: %v", err)
	}

	downgradeEventTables(t, d)
	if err := d.Close(); err != nil {
		t.Fatalf("close downgraded DB: %v", err)
	}

	// Re-open triggers Migrate, which must rebuild all three event tables.
	d, err = Open(path)
	if err != nil {
		t.Fatalf("Open after downgrade: %v", err)
	}
	defer d.Close()

	// Rows survive the table rebuild.
	if n, err := d.CountReleaseEventsByWeek(ctx, 2026, 1); err != nil || n != 1 {
		t.Fatalf("release events after migration = %d, err = %v", n, err)
	}

	// The new status is accepted and the reason is stored.
	id, err := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID:      titleID,
		Source:       "test",
		Status:       model.StatusFiltered,
		FilterReason: "language ja is blocked",
		ISOYear:      2026,
		ISOWeek:      1,
	})
	if err != nil {
		t.Fatalf("CreateReleaseEvent(filtered): %v", err)
	}

	reasons, err := d.FilteredReasonsForWeek(ctx, 2026, 1)
	if err != nil {
		t.Fatalf("FilteredReasonsForWeek: %v", err)
	}
	if got := reasons.Video[id]; got != "language ja is blocked" {
		t.Fatalf("filter reason = %q, want %q", got, "language ja is blocked")
	}

	// Album and book tables accept filtered too.
	if _, err := d.CreateAlbumReleaseEvent(ctx, &model.AlbumReleaseEvent{
		ReleaseID: albumID, Source: "test", Status: model.StatusFiltered,
		FilterReason: "critic score too low", ISOYear: 2026, ISOWeek: 1,
	}); err != nil {
		t.Fatalf("CreateAlbumReleaseEvent(filtered): %v", err)
	}
	if _, err := d.CreateBookReleaseEvent(ctx, &model.BookReleaseEvent{
		BookID: bookID, Source: "test", Status: model.StatusFiltered,
		FilterReason: "rating too low", FormatPref: model.BookFormatBoth,
		ISOYear: 2026, ISOWeek: 1,
	}); err != nil {
		t.Fatalf("CreateBookReleaseEvent(filtered): %v", err)
	}

	// Filtered rows are returned by reason loading for all families.
	reasons, err = d.FilteredReasonsForWeek(ctx, 2026, 1)
	if err != nil {
		t.Fatalf("FilteredReasonsForWeek: %v", err)
	}
	if len(reasons.Album) != 1 || len(reasons.Book) != 1 {
		t.Fatalf("expected one filtered album and book reason, got %d/%d", len(reasons.Album), len(reasons.Book))
	}
}

// Ensure the rebuild guard recognizes an already-migrated table (open twice).
func TestMigrateFilteredIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	downgradeEventTables(t, d)
	if err := d.Close(); err != nil {
		t.Fatalf("close downgraded DB: %v", err)
	}

	for i := 0; i < 2; i++ {
		d, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		if err := d.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
}
