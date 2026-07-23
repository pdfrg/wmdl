package discover

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) processAnimeItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	r.log.Info().Str("title", item.Title).Int("year", item.Year).Msg("processing anime item")

	title := &model.Title{
		MalID:         item.MalID,
		Title:         item.Title,
		Year:          item.Year,
		MediaType:     model.MediaTypeAnime,
		USRating:      item.USRating,
		Overview:      item.Overview,
		Genres:        item.Genres,
		PosterPath:    item.ImageURL,
		TmdbRating:    item.ImdbRating,
		AnimeType:     item.AnimeType,
		AnimeEpisodes: item.AnimeEpisodes,
		AnimeStatus:   item.AnimeStatus,
		AnimeMembers:  item.AnimeMembers,
		AnimeRank:     item.AnimeRank,
		AnimeSource:   item.AnimeSource,
		AnimeStudio:   item.AnimeStudio,
		Themes:        item.Themes,
		Demographics:  item.Demographics,
		Streaming:     item.Streaming,
	}

	if result := FilterTitle(&r.cfg.MediaTypes.Anime.Filter, title); !result.Passed {
		r.log.Info().Str("title", item.Title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
	}

	var titleID int64
	var existingEvent *model.ReleaseEvent

	err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
		id, err := r.db.UpsertTitleTx(ctx, tx, title)
		if err != nil {
			return fmt.Errorf("saving anime title: %w", err)
		}
		titleID = id

		existing, err := r.db.GetLatestReleaseEventTx(ctx, tx, titleID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		if existing != nil {
			existingEvent = existing
			return nil
		}

		evt := &model.ReleaseEvent{
			TitleID:     titleID,
			Source:      item.Source,
			ReleaseType: model.ReleaseStreaming,
			Status:      model.StatusPending,
			Notes:       item.Notes,
			ISOYear:     progYear,
			ISOWeek:     progWeek,
		}

		if _, err := r.db.CreateReleaseEventTx(ctx, tx, evt); err != nil {
			return fmt.Errorf("saving anime release event: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	if existingEvent != nil {
		return nil
	}

	return nil
}

func normalizeAnimeTitle(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.NewReplacer("-", " ", ":", " ", ",", " ", ".", " ", "_", " ").Replace(v)
	v = metaParen.ReplaceAllString(v, "")
	v = trailingFmt.ReplaceAllString(v, "")
	return strings.TrimSpace(strings.Join(strings.Fields(v), " "))
}
