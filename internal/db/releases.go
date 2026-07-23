package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
)

type EventWithTitle struct {
	Event *model.ReleaseEvent
	Title *model.Title
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
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
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

func (d *DB) ListPendingWithTitles(ctx context.Context) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.imdb_votes, t.awards, t.box_office, t.director, t.writer, t.actors, t.rt_url, t.rt_critics_score, t.rt_audience_score,
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
		       t.imdb_rating, t.imdb_votes, t.awards, t.box_office, t.director, t.writer, t.actors, t.rt_url, t.rt_critics_score, t.rt_audience_score,
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
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}
	return id, nil
}

func (d *DB) UpdateDownloadStatus(ctx context.Context, id int64, status model.DownloadStatus) error {
	_, err := d.db.ExecContext(ctx, `UPDATE downloads SET status = ? WHERE id = ?`, string(status), id)
	return err
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

func (d *DB) ListEventsByWeekWithTitles(ctx context.Context, year, week int) ([]EventWithTitle, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.id, e.title_id, e.source, e.release_type, e.release_date,
		       e.status, e.previous_status, e.notes, e.created_at, e.iso_year, e.iso_week,
		t.id, t.tmdb_id, t.tvdb_id, t.mal_id, t.title, t.tmdb_title, t.year, t.media_type, t.imdb_id,
		       t.imdb_rating, t.imdb_votes, t.awards, t.box_office, t.director, t.writer, t.actors, t.rt_url, t.rt_critics_score, t.rt_audience_score,
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
		       t.imdb_rating, t.imdb_votes, t.awards, t.box_office, t.director, t.writer, t.actors, t.rt_url, t.rt_critics_score, t.rt_audience_score,
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
		       t.imdb_rating, t.imdb_votes, t.awards, t.box_office, t.director, t.writer, t.actors, t.rt_url, t.rt_critics_score, t.rt_audience_score,
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
		&tl.ImdbID, &tl.ImdbRating, &tl.ImdbVotes, &tl.Awards, &tl.BoxOffice, &tl.Director, &tl.Writer, &tl.Actors, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
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

func (d *DB) PendingEventCountsBySource(ctx context.Context, year, week int) (map[string]int, error) {
	counts := make(map[string]int)

	rows, err := d.db.QueryContext(ctx, `
		SELECT e.source, t.media_type, COUNT(*) FROM release_events e
		JOIN titles t ON t.id = e.title_id
		WHERE e.iso_year = ? AND e.iso_week = ? AND e.status = 'pending'
		GROUP BY e.source, t.media_type
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("counting pending release events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var mediaType string
		var n int
		if err := rows.Scan(&source, &mediaType, &n); err != nil {
			return nil, fmt.Errorf("scan pending release event: %w", err)
		}
		for _, s := range strings.Split(source, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			label := pendingSourceLabel(s, model.MediaType(mediaType))
			counts[label] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows2, err := d.db.QueryContext(ctx, `
		SELECT source, COUNT(*) FROM album_release_events
		WHERE iso_year = ? AND iso_week = ? AND status = 'pending'
		GROUP BY source
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("counting pending album events: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var source string
		var n int
		if err := rows2.Scan(&source, &n); err != nil {
			return nil, fmt.Errorf("scan pending album event: %w", err)
		}
		for _, s := range strings.Split(source, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			label := pendingSourceLabel(s, model.MediaTypeMusic)
			counts[label] += n
		}
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	rows3, err := d.db.QueryContext(ctx, `
		SELECT source, COUNT(*) FROM book_release_events
		WHERE iso_year = ? AND iso_week = ? AND status = 'pending'
		GROUP BY source
	`, year, week)
	if err != nil {
		return nil, fmt.Errorf("counting pending book events: %w", err)
	}
	defer rows3.Close()
	for rows3.Next() {
		var source string
		var n int
		if err := rows3.Scan(&source, &n); err != nil {
			return nil, fmt.Errorf("scan pending book event: %w", err)
		}
		for _, s := range strings.Split(source, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			label := pendingSourceLabel(s, model.MediaTypeBook)
			counts[label] += n
		}
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	return counts, nil
}

func pendingSourceLabel(source string, mt model.MediaType) string {
	switch source {
	case "tmdb-discover":
		return "tmdb " + string(mt)
	case "flixpatrol":
		return "flixpatrol " + string(mt)
	case "goodreads_blog":
		return "goodreads blog"
	case "anilist":
		return "anilist (completed)"
	case "anilist-airing":
		return "anilist (airing)"
	case "tenrai":
		return "tenrai (completed)"
	case "tenrai-airing":
		return "tenrai (airing)"
	case "jikan":
		return "jikan (completed)"
	case "jikan-airing":
		return "jikan (airing)"
	default:
		return source
	}
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
			&tl.ImdbID, &tl.ImdbRating, &tl.ImdbVotes, &tl.Awards, &tl.BoxOffice, &tl.Director, &tl.Writer, &tl.Actors, &tl.RTURL, &tl.RTCriticsScore, &tl.RTAudienceScore,
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
