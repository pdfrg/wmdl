package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pdfrg/wmdl/internal/model"
)

func (d *DB) UpsertTitle(ctx context.Context, t *model.Title) (int64, error) {
	return d.upsertTitle(ctx, d.db, t)
}

func (d *DB) UpsertTitleTx(ctx context.Context, tx *sql.Tx, t *model.Title) (int64, error) {
	return d.upsertTitle(ctx, tx, t)
}

func (d *DB) upsertTitle(ctx context.Context, q querier, t *model.Title) (int64, error) {
	if t.MediaType == model.MediaTypeAnime && t.MalID > 0 {
		existing, err := d.getTitleByMalID(ctx, q, t.MalID)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			_, err := q.ExecContext(ctx, `
				UPDATE titles SET
					tvdb_id = ?, title = ?, year = ?, media_type = ?,
					imdb_id = ?,
					imdb_rating = COALESCE(NULLIF(?, 0), ?),
					imdb_votes = ?,
					awards = ?, box_office = ?, director = ?, writer = ?, actors = ?,
					rt_url = ?,
					rt_critics_score = COALESCE(NULLIF(?, 0), ?),
					rt_audience_score = COALESCE(NULLIF(?, 0), ?),
					rt_audience_real_score = ?,
					rt_real_votes = ?,
					tmdb_rating = ?,
					metacritic_score = COALESCE(NULLIF(?, 0), ?),
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
				t.ImdbID,
				t.ImdbRating, existing.ImdbRating,
				t.ImdbVotes,
				t.Awards, t.BoxOffice, t.Director, t.Writer, t.Actors,
				t.RTURL,
				t.RTCriticsScore, existing.RTCriticsScore,
				t.RTAudienceScore, existing.RTAudienceScore,
				t.RTAudienceRealScore,
				t.RTRealVotes,
				t.TmdbRating,
				t.MetacriticScore, existing.MetacriticScore,
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
		                    imdb_votes, awards, box_office, director, writer, actors,
		                    rt_url, rt_critics_score, rt_audience_score, tmdb_rating,
		                    metacritic_score, us_rating, original_language, origin_country,
		                    yt_trailer_views, overview, genres, runtime, poster_path, created_at,
		                    anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		                    anime_source, anime_studio, themes, demographics, streaming,
		                    collection_id, collection_name,
		                    rt_audience_real_score, rt_real_votes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?)
		ON CONFLICT(tmdb_id, mal_id) DO UPDATE SET
			title                = excluded.title,
			tmdb_title           = excluded.tmdb_title,
			year                 = excluded.year,
			media_type           = excluded.media_type,
			tvdb_id              = excluded.tvdb_id,
			mal_id               = excluded.mal_id,
			imdb_id              = excluded.imdb_id,
			imdb_rating          = CASE WHEN excluded.imdb_rating > 0 THEN excluded.imdb_rating ELSE titles.imdb_rating END,
			imdb_votes           = excluded.imdb_votes,
			awards               = excluded.awards,
			box_office           = excluded.box_office,
			director             = excluded.director,
			writer               = excluded.writer,
			actors               = excluded.actors,
			rt_url               = excluded.rt_url,
			rt_critics_score     = CASE WHEN excluded.rt_critics_score > 0 THEN excluded.rt_critics_score ELSE titles.rt_critics_score END,
			rt_audience_score    = CASE WHEN excluded.rt_audience_score > 0 THEN excluded.rt_audience_score ELSE titles.rt_audience_score END,
			rt_audience_real_score = CASE WHEN excluded.rt_audience_real_score > 0 THEN excluded.rt_audience_real_score ELSE titles.rt_audience_real_score END,
			rt_real_votes        = CASE WHEN excluded.rt_real_votes > 0 THEN excluded.rt_real_votes ELSE titles.rt_real_votes END,
			tmdb_rating          = excluded.tmdb_rating,
			metacritic_score     = CASE WHEN excluded.metacritic_score > 0 THEN excluded.metacritic_score ELSE titles.metacritic_score END,
			us_rating            = excluded.us_rating,
			original_language    = excluded.original_language,
			origin_country       = excluded.origin_country,
			yt_trailer_views     = excluded.yt_trailer_views,
			overview             = excluded.overview,
			genres               = excluded.genres,
			runtime              = excluded.runtime,
			poster_path          = excluded.poster_path,
			anime_type           = excluded.anime_type,
			anime_episodes       = excluded.anime_episodes,
			anime_status         = excluded.anime_status,
			anime_members        = excluded.anime_members,
			anime_rank           = excluded.anime_rank,
			anime_source         = excluded.anime_source,
			anime_studio         = excluded.anime_studio,
			themes               = excluded.themes,
			demographics         = excluded.demographics,
			streaming            = excluded.streaming,
			collection_id        = excluded.collection_id,
			collection_name      = excluded.collection_name
	`,
		t.TmdbID, t.TvdbID, t.MalID, t.Title, t.TmdbTitle, t.Year, string(t.MediaType), t.ImdbID, t.ImdbRating,
		t.ImdbVotes, t.Awards, t.BoxOffice, t.Director, t.Writer, t.Actors,
		t.RTURL, t.RTCriticsScore, t.RTAudienceScore, t.TmdbRating,
		t.MetacriticScore, t.USRating, t.OriginalLanguage, t.OriginCountry,
		t.YoutubeViews, t.Overview, t.Genres, t.Runtime, t.PosterPath, t.CreatedAt,
		t.AnimeType, t.AnimeEpisodes, t.AnimeStatus, t.AnimeMembers, t.AnimeRank,
		t.AnimeSource, t.AnimeStudio, t.Themes, t.Demographics, t.Streaming,
		t.CollectionID, t.CollectionName,
		t.RTAudienceRealScore, t.RTRealVotes,
	)
	if err != nil {
		return 0, fmt.Errorf("upserting title: %w", err)
	}
	lastID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert id: %w", err)
	}

	// SQLite LastInsertId is unreliable for UPSERT UPDATE — it may return
	// a stale value from a previous INSERT. Always re-query to get the
	// real id for this (tmdb_id, mal_id) pair.
	if lastID > 0 && t.TmdbID > 0 {
		var match int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM titles WHERE id = ? AND tmdb_id = ? AND mal_id = ?`,
			lastID, t.TmdbID, t.MalID).Scan(&match); err == nil && match > 0 {
			return lastID, nil
		}
	}
	var id int64
	err = q.QueryRowContext(ctx,
		`SELECT id FROM titles WHERE tmdb_id = ? AND mal_id = ?`,
		t.TmdbID, t.MalID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("re-querying title id after upsert: %w", err)
	}
	return id, nil
}

func (d *DB) GetTitleByTmdbID(ctx context.Context, tmdbID int) (*model.Title, error) {
	var t model.Title
	var mediaType string
	var createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
		       rt_audience_real_score, rt_real_votes,
		       tmdb_rating, metacritic_score, yt_trailer_views, us_rating, original_language, origin_country,
		       overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
		FROM titles WHERE tmdb_id = ?
	`, tmdbID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.ImdbVotes, &t.Awards, &t.BoxOffice, &t.Director, &t.Writer, &t.Actors, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.RTAudienceRealScore, &t.RTRealVotes,
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
		return nil, fmt.Errorf("querying title by tmdb_id: %w", err)
	}
	t.MediaType = model.MediaType(mediaType)
	t.CreatedAt = createdAt
	return &t, nil
}

func (d *DB) GetTitleByMalID(ctx context.Context, malID int) (*model.Title, error) {
	var t model.Title
	var mediaType string
	var createdAt string
	err := d.db.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
		       rt_audience_real_score, rt_real_votes,
		       tmdb_rating, metacritic_score, yt_trailer_views, us_rating, original_language, origin_country,
		       overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
		FROM titles WHERE mal_id = ?
	`, malID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.ImdbVotes, &t.Awards, &t.BoxOffice, &t.Director, &t.Writer, &t.Actors, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.RTAudienceRealScore, &t.RTRealVotes,
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

func (d *DB) getTitleByMalID(ctx context.Context, q querier, malID int) (*model.Title, error) {
	if malID <= 0 {
		return nil, nil
	}
	var t model.Title
	var mediaType string
	var createdAt string
	err := q.QueryRowContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
		       rt_audience_real_score, rt_real_votes,
		       tmdb_rating, metacritic_score, yt_trailer_views, us_rating, original_language, origin_country,
		       overview, genres, runtime, poster_path, created_at,
		       anime_type, anime_episodes, anime_status, anime_members, anime_rank,
		       anime_source, anime_studio, themes, demographics, streaming,
		       collection_id, collection_name
		FROM titles WHERE mal_id = ?
	`, malID).Scan(
		&t.ID, &t.TmdbID, &t.TvdbID, &t.MalID, &t.Title, &t.TmdbTitle, &t.Year, &mediaType,
		&t.ImdbID, &t.ImdbRating, &t.ImdbVotes, &t.Awards, &t.BoxOffice, &t.Director, &t.Writer, &t.Actors, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
		&t.RTAudienceRealScore, &t.RTRealVotes,
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

func (d *DB) ListTitles(ctx context.Context) ([]*model.Title, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, tmdb_id, tvdb_id, mal_id, title, tmdb_title, year, media_type, imdb_id,
		       imdb_rating, imdb_votes, awards, box_office, director, writer, actors, rt_url, rt_critics_score, rt_audience_score,
		       rt_audience_real_score, rt_real_votes,
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
			&t.ImdbID, &t.ImdbRating, &t.ImdbVotes, &t.Awards, &t.BoxOffice, &t.Director, &t.Writer, &t.Actors, &t.RTURL, &t.RTCriticsScore, &t.RTAudienceScore,
			&t.RTAudienceRealScore, &t.RTRealVotes,
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

func (d *DB) UpdateTitleTvdbID(ctx context.Context, titleID int64, tvdbID int) error {
	_, err := d.db.ExecContext(ctx, `UPDATE titles SET tvdb_id = ? WHERE id = ?`, tvdbID, titleID)
	if err != nil {
		return fmt.Errorf("updating title tvdb_id: %w", err)
	}
	return nil
}
