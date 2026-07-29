package discover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) processItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	searchTitle := cleanTitleForSearch(item.Title)
	r.log.Info().Str("title", item.Title).Int("year", item.Year).Msg("processing item")

	apiCtx, apiCancel := context.WithTimeout(ctx, 20*time.Second)
	defer apiCancel()

	mediaType := item.MediaType
	var tmdbID int
	var rating float64
	var enrich *TMDBEnrichment
	var imdbID string
	var err error

	preferType := string(item.MediaType)

	if item.TmdbID > 0 {
		enrich, err = r.tmdb.enrichByID(apiCtx, item.TmdbID, preferType, 0, item.Title, item.Year)
		if err == nil {
			tmdbID = enrich.TMDBID
			rating = enrich.Rating
			imdbID = enrich.IMDbID
			r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (by ID)")
		} else {
			r.log.Warn().Err(err).Str("title", item.Title).Msg("TMDB enrich by ID failed")
		}
	}

	if tmdbID == 0 {
		enrich, err = r.tmdb.enrichWithPrefs(apiCtx, searchTitle, item.Year, preferType)
		if err == nil {
			tmdbID = enrich.TMDBID
			rating = enrich.Rating
			imdbID = enrich.IMDbID
			r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched")
		} else {
			r.log.Warn().Err(err).Str("title", item.Title).Msg("TMDB enrich with prefs failed")
		}
	}

	if tmdbID == 0 && (err != nil || (enrich != nil && !strings.EqualFold(enrich.Title, searchTitle))) {
		origTitle := html.UnescapeString(item.Title)
		if origTitle != searchTitle {
			r.log.Info().Str("original", origTitle).Msg("retrying TMDB with original title")
			enrich2, err2 := r.tmdb.enrichWithPrefs(apiCtx, origTitle, item.Year, preferType)
			if err2 == nil {
				enrich = enrich2
				tmdbID = enrich.TMDBID
				rating = enrich.Rating
				imdbID = enrich.IMDbID
				r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (retry)")
			} else {
				r.log.Warn().Err(err2).Str("title", item.Title).Msg("TMDB retry with original title failed")
			}
		}

		if tmdbID == 0 && (enrich == nil || !strings.EqualFold(enrich.Title, searchTitle)) {
			if paren := extractParenthetical(item.Title); paren != "" && !strings.EqualFold(paren, searchTitle) && !strings.EqualFold(paren, origTitle) {
				r.log.Info().Str("parenthetical", paren).Msg("retrying TMDB with parenthetical content")
				enrich3, err3 := r.tmdb.enrichWithPrefs(apiCtx, paren, item.Year, preferType)
				if err3 == nil {
					enrich = enrich3
					tmdbID = enrich.TMDBID
					rating = enrich.Rating
					imdbID = enrich.IMDbID
					r.log.Info().Int("tmdb_id", tmdbID).Float64("rating", rating).Str("media", string(enrich.MediaType)).Msg("TMDB enriched (parenthetical)")
				} else {
					r.log.Warn().Err(err3).Str("title", item.Title).Msg("TMDB retry with parenthetical failed")
				}
			}
		}
	}

	if enrich == nil {
		r.log.Warn().Err(err).Msg("TMDB lookup failed")
		imdbID = item.ImdbID
	}

	if enrich != nil && enrich.Year > 0 {
		item.Year = enrich.Year
	}

	var imdbRating float64
	var metacriticScore float64
	var imdbVotes int64
	var awards, boxOffice, director, writer, actors string
	if imdbID != "" {
		data, err := r.omdb.FetchRatings(apiCtx, imdbID)
		if err == nil && data != nil {
			imdbRating = data.ImdbRating
			metacriticScore = data.MetacriticScore
			imdbVotes = data.ImdbVotes
			awards = data.Awards
			boxOffice = data.BoxOffice
			director = data.Director
			writer = data.Writer
			actors = data.Actors
			r.log.Info().
				Float64("imdb_rating", imdbRating).
				Float64("metacritic", metacriticScore).
				Int64("imdb_votes", imdbVotes).
				Msg("OMDB data")
		} else if err != nil {
			r.log.Warn().Err(err).Msg("OMDB failed")
		}
	}
	if imdbRating == 0 && item.ImdbRating > 0 {
		imdbRating = item.ImdbRating
		r.log.Info().Float64("imdb_rating", imdbRating).Msg("using scraped IMDb rating")
	}

	var usRating string
	if tmdbID > 0 {
		if mediaType == model.MediaTypeTV {
			usRating = r.tmdb.GetTVRating(apiCtx, tmdbID)
		} else {
			usRating = r.tmdb.GetUSCertification(apiCtx, tmdbID, string(mediaType))
		}
		if usRating != "" {
			r.log.Info().Str("rating", usRating).Msg("US content rating")
		}
	}

	rtTitle := searchTitle
	if enrich != nil && enrich.Title != "" {
		rtTitle = enrich.Title
	}
	rtURL := r.rt.FindURL(apiCtx, rtTitle, item.Year, string(mediaType))

	if strings.Contains(rtURL, "search?search=") {
		if r.allocCtx != nil {
			r.log.Info().Msg("trying RT search via chromedp")
			if searchURL := SearchRTSite(ctx, r.allocCtx, rtTitle, item.Year, string(mediaType)); searchURL != "" {
				rtURL = searchURL
				r.log.Info().Str("url", rtURL).Msg("RT URL found via search")
			}
		}
	}
	apiCancel()

	rtCritics, rtAudience, rtAudienceReal, rtRealVotes := 0.0, 0.0, 0.0, 0
	if rtURL != "" {
		r.log.Info().Str("url", rtURL).Msg("RT URL found")
		ratings := ScrapeRTRatings(ctx, rtURL)
		rtCritics = ratings.CriticsScore
		rtAudience = ratings.AudienceScore
		rtAudienceReal = ratings.AudienceRealScore
		rtRealVotes = ratings.RealVotes
		if rtCritics > 0 || rtAudience > 0 {
			r.log.Info().Str("url", rtURL).Float64("critics", rtCritics).Float64("audience", rtAudience).Msg("RT scores")
		} else {
			r.log.Warn().Str("url", rtURL).Msg("RT scrape: no scores found")
		}
		if rtAudienceReal > 0 {
			r.log.Info().Str("url", rtURL).Float64("real", rtAudienceReal).Int("votes", rtRealVotes).Msg("RT real audience score")
		}
	} else {
		r.log.Info().Msg("RT URL not found")
	}
	if rtCritics == 0 && item.RTCriticsScore > 0 {
		rtCritics = item.RTCriticsScore
		r.log.Warn().Float64("critics_score", rtCritics).Msg("using provider RT critics score (may be stale)")
	}

	overview := ""
	genres := ""
	runtime := 0
	tvdbID := 0
	posterPath := ""
	originalLanguage := ""
	originCountry := ""
	tmdbTitle := ""
	collectionID := 0
	collectionName := ""
	if enrich != nil {
		tvdbID = enrich.TVDBID
		tmdbTitle = enrich.Title
		overview = enrich.Overview
		genres = enrich.Genres
		runtime = enrich.Runtime
		posterPath = enrich.PosterPath
		originalLanguage = enrich.OriginalLanguage
		originCountry = enrich.OriginCountry
		collectionID = enrich.CollectionID
		collectionName = enrich.CollectionName
	}

	title := &model.Title{
		TmdbID:              tmdbID,
		TvdbID:              tvdbID,
		Title:               item.Title,
		TmdbTitle:           tmdbTitle,
		Year:                item.Year,
		MediaType:           mediaType,
		ImdbID:              imdbID,
		ImdbRating:          imdbRating,
		ImdbVotes:           imdbVotes,
		Awards:              awards,
		BoxOffice:           boxOffice,
		Director:            director,
		Writer:              writer,
		Actors:              actors,
		MetacriticScore:     metacriticScore,
		RTURL:               rtURL,
		RTCriticsScore:      rtCritics,
		RTAudienceScore:     rtAudience,
		RTAudienceRealScore: rtAudienceReal,
		RTRealVotes:         rtRealVotes,
		TmdbRating:          rating,
		USRating:            usRating,
		YoutubeViews:        item.YoutubeViews,
		Overview:            overview,
		Genres:              genres,
		Runtime:             runtime,
		PosterPath:          posterPath,
		OriginalLanguage:    originalLanguage,
		OriginCountry:       originCountry,
		CollectionID:        collectionID,
		CollectionName:      collectionName,
	}

	cf := &r.cfg.MediaTypes.Movies.Filter
	if mediaType == model.MediaTypeTV {
		cf = &r.cfg.MediaTypes.TV.Filter
	}
	if result := FilterTitle(cf, title); !result.Passed {
		r.log.Info().Str("title", item.Title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
	}

	var titleID int64
	var existingEvent *model.ReleaseEvent

	err = r.db.Transaction(ctx, func(tx *sql.Tx) error {
		id, err := r.db.UpsertTitleTx(ctx, tx, title)
		if err != nil {
			return fmt.Errorf("saving title: %w", err)
		}
		titleID = id

		existing, err := r.db.GetLatestReleaseEventTx(ctx, tx, titleID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		var evtNotes string
		var evtPrev model.ReleaseStatus

		if existing != nil {
			existingEvent = existing
			evtPrev = existing.Status

			switch existing.Status {
			case model.StatusPending, model.StatusApproved:
				return nil
			case model.StatusDownloaded:
				dl, dlErr := r.db.GetDownloadByTitleIDTx(ctx, tx, titleID)
				if dlErr == nil && dl != nil && dl.SourceType != "" {
					if isUpgrade(dl.SourceType, item.ReleaseType) {
						evtNotes = fmt.Sprintf("upgrade: %s → %s", dl.SourceType, item.ReleaseType)
					}
				}
				if evtNotes == "" {
					return nil
				}
			case model.StatusRejected:
				// Re-queue only when the release type changes
				// (e.g. streaming → physical upgrade).
				if existing.ReleaseType == item.ReleaseType {
					return nil
				}
				evtNotes = fmt.Sprintf("re-queue: %s → %s",
					existing.ReleaseType, item.ReleaseType)
			}
		}

		evt := &model.ReleaseEvent{
			TitleID:        titleID,
			Source:         item.Source,
			ReleaseType:    item.ReleaseType,
			ReleaseDate:    item.ReleaseDate,
			Status:         model.StatusPending,
			PreviousStatus: evtPrev,
			Notes:          evtNotes,
			ISOYear:        progYear,
			ISOWeek:        progWeek,
		}

		if _, err := r.db.CreateReleaseEventTx(ctx, tx, evt); err != nil {
			return fmt.Errorf("saving release event: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	if existingEvent != nil {
		return nil
	}

	if enrich != nil && enrich.CollectionID > 0 && enrich.MediaType == "movie" {
		collIDStr := strconv.Itoa(enrich.CollectionID)
		existingCache, _ := r.db.GetLibraryCache(ctx, "tmdb-collection", collIDStr)
		if existingCache == nil {
			coll, err := r.tmdb.GetCollection(ctx, enrich.CollectionID)
			if err == nil && coll != nil && len(coll.Parts) > 0 {
				type collPart struct {
					TmdbID int    `json:"tmdb_id"`
					Title  string `json:"title"`
					Year   int    `json:"year"`
				}
				parts := make([]collPart, 0, len(coll.Parts))
				for _, p := range coll.Parts {
					year := 0
					if len(p.ReleaseDate) >= 4 {
						if n, _ := fmt.Sscanf(p.ReleaseDate[:4], "%d", &year); n != 1 {
							year = 0
						}
					}
					parts = append(parts, collPart{TmdbID: p.ID, Title: p.Title, Year: year})
				}
				collJSON, _ := json.Marshal(map[string]any{
					"name":   coll.Name,
					"movies": parts,
				})
				_ = r.db.UpsertLibraryCache(ctx, "tmdb-collection", collIDStr, int64(coll.ID), coll.Name, string(collJSON))
			}
		}
	}

	return nil
}

func isUpgrade(prevSource string, newReleaseType model.ReleaseType) bool {
	if prevSource == "" {
		return false
	}
	if newReleaseType != model.ReleasePhysical {
		return false
	}
	prev := string(prevSource)
	prev = strings.ToLower(prev)
	switch prev {
	case "webrip", "web-dl", "hdtv":
		return true
	case "bluray", "remux":
		return false
	}
	return false
}

func cleanTitleForSearch(title string) string {
	cleaned := html.UnescapeString(title)
	cleaned = metaParen.ReplaceAllString(cleaned, "")
	if parts := strings.SplitN(cleaned, ":", 2); len(parts) == 2 && len(parts[0]) >= 4 {
		suffix := strings.ToLower(strings.TrimSpace(parts[1]))
		if strings.Contains(suffix, "season") || strings.Contains(suffix, "complete") {
			cleaned = parts[0]
		}
	}
	cleaned = trailingFmt.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

func extractParenthetical(title string) string {
	re := regexp.MustCompile(`\(([^)]+)\)`)
	m := re.FindStringSubmatch(title)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}
