package db

import (
	"context"
	"database/sql"
	"fmt"
)

type LibraryCache struct {
	ID        int64
	Source    string
	ExtID     string
	ArrID     int64
	ArrTitle  string
	Details   string
	FetchedAt string
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
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
