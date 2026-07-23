package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
)

type EventWithBook struct {
	Event  *model.BookReleaseEvent
	Book   *model.Book
	Author *model.Author
}

func (d *DB) upsertAuthor(ctx context.Context, q querier, a *model.Author) (int64, error) {
	if a.OLID != "" && !strings.HasPrefix(a.OLID, "_nm_") {
		existing, err := d.getAuthorByName(ctx, q, a.Name)
		if err != nil {
			return 0, fmt.Errorf("checking existing author by name: %w", err)
		}
		if existing != nil {
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

	existing, err := d.getAuthorByName(ctx, q, a.Name)
	if err != nil {
		return 0, fmt.Errorf("checking existing author by name: %w", err)
	}
	if existing != nil {
		if strings.HasPrefix(existing.OLID, "_nm_") {
			olidVal := a.OLID
			if olidVal == "" {
				olidVal = existing.OLID
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
	}

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
	if b.ISBN13 != "" {
		existing, err := d.getBookByISBN(ctx, q, b.ISBN13)
		if err != nil {
			return 0, fmt.Errorf("checking existing book by isbn: %w", err)
		}
		if existing != nil {
			if err := d.updateBookFields(ctx, q, b, existing.ID); err != nil {
				return 0, fmt.Errorf("updating existing book by isbn: %w", err)
			}
			return existing.ID, nil
		}
	}

	if b.HardcoverID > 0 {
		existing, err := d.getBookByHardcoverID(ctx, q, b.HardcoverID)
		if err != nil {
			return 0, fmt.Errorf("checking existing book by hardcover_id: %w", err)
		}
		if existing != nil {
			if err := d.updateBookFields(ctx, q, b, existing.ID); err != nil {
				return 0, fmt.Errorf("updating existing book by hardcover_id: %w", err)
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

func (d *DB) getBookByHardcoverID(ctx context.Context, q querier, hcID int) (*model.Book, error) {
	if hcID <= 0 {
		return nil, nil
	}
	var b model.Book
	err := q.QueryRowContext(ctx, `
		SELECT id, author_id, title, subtitle, hardcover_id, hardcover_slug, olid, isbn10, isbn13, asin,
		       pages, audio_seconds, description, release_date, release_year,
		       rating, ratings_count, shelvings_count, image_url, language, publisher, tags, literary_type,
		       series_id, series_name, created_at
		FROM books WHERE hardcover_id = ?
	`, hcID).Scan(
		&b.ID, &b.AuthorID, &b.Title, &b.Subtitle, &b.HardcoverID, &b.HardcoverSlug, &b.OLID,
		&b.ISBN10, &b.ISBN13, &b.ASIN,
		&b.Pages, &b.AudioSeconds, &b.Description, &b.ReleaseDate, &b.ReleaseYear,
		&b.Rating, &b.RatingsCount, &b.ShelvingsCount, &b.ImageURL, &b.Language, &b.Publisher, &b.Tags, &b.LiteraryType,
		&b.SeriesID, &b.SeriesName, &b.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("querying book by hardcover_id: %w", err)
	}
	return &b, nil
}

func (d *DB) updateBookFields(ctx context.Context, q querier, b *model.Book, existingID int64) error {
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
		b.SeriesID, b.SeriesName, existingID)
	if err != nil {
		return fmt.Errorf("updating book fields: %w", err)
	}
	return nil
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
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
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
	switch format {
	case model.BookFormatEbook:
		_, err := d.db.ExecContext(ctx, `UPDATE book_release_events SET ebook_processed = 1 WHERE id = ?`, id)
		return err
	case model.BookFormatAudiobook:
		_, err := d.db.ExecContext(ctx, `UPDATE book_release_events SET audiobook_processed = 1 WHERE id = ?`, id)
		return err
	default:
		return fmt.Errorf("unknown book format: %s", format)
	}
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
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
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

type BookISBNEntry struct {
	ISBN    string
	BookID  int64
	EventID int64
}

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

func (d *DB) FindPendingBookEventsByHardcoverIDTx(ctx context.Context, tx *sql.Tx) (map[int][]BookISBNEntry, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT b.hardcover_id, b.id, e.id
		FROM books b
		JOIN book_release_events e ON e.book_id = b.id
		WHERE b.hardcover_id > 0 AND e.status = 'pending'
		ORDER BY b.hardcover_id, b.id, e.id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying pending events by hardcover_id: %w", err)
	}
	defer rows.Close()

	raw := make(map[int][]BookISBNEntry)
	for rows.Next() {
		var hcID int
		var bookID, eventID int64
		if err := rows.Scan(&hcID, &bookID, &eventID); err != nil {
			return nil, fmt.Errorf("scanning pending event row: %w", err)
		}
		raw[hcID] = append(raw[hcID], BookISBNEntry{
			BookID:  bookID,
			EventID: eventID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make(map[int][]BookISBNEntry)
	for hcID, entries := range raw {
		seen := make(map[int64]bool)
		for _, e := range entries {
			seen[e.BookID] = true
		}
		if len(seen) >= 2 {
			results[hcID] = entries
		}
	}
	return results, nil
}

func (d *DB) DeleteBookReleaseEvent(ctx context.Context, id int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM book_release_events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book release event %d: %w", id, err)
	}
	return nil
}

func (d *DB) DeleteBookReleaseEventTx(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM book_release_events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book release event %d: %w", id, err)
	}
	return nil
}

func (d *DB) DeleteBook(ctx context.Context, id int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book %d: %w", id, err)
	}
	return nil
}

func (d *DB) DeleteBookTx(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting book %d: %w", id, err)
	}
	return nil
}

type BookSeriesMember struct {
	HardcoverID int
	Title       string
}

func (d *DB) GetBookSeriesMembers(ctx context.Context, seriesID string) ([]BookSeriesMember, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT hardcover_id, title FROM books
		WHERE series_id = ? AND hardcover_id > 0
		ORDER BY title
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("querying book series members: %w", err)
	}
	defer rows.Close()
	var members []BookSeriesMember
	for rows.Next() {
		var m BookSeriesMember
		if err := rows.Scan(&m.HardcoverID, &m.Title); err != nil {
			return nil, fmt.Errorf("scanning book series member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return members, nil
}

func (d *DB) FindPendingBookEventsByISBNTx(ctx context.Context, tx *sql.Tx) (map[string][]BookISBNEntry, error) {
	rows, err := tx.QueryContext(ctx, `
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
