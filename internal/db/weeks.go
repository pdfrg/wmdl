package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pdfrg/wmdl/internal/model"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
