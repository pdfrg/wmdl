package discover

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) processMusicItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	apiCtx, apiCancel := context.WithTimeout(ctx, 30*time.Second)
	defer apiCancel()

	if item.Source != "allmusic" {
		if HasOverrideGenre(&r.cfg.MediaTypes.Music.Filter.ContentFilter, item.Genres) {
			r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).
				Msg("override genre matched, skipping score filter")
		} else if !passesMusicFilter(r.cfg.MediaTypes.Music.Filter, item) {
			r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("does not pass filter, skipping")
			return nil
		}
	}
	r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("musicbrainz: searching release group")

	if item.AOTYURL != "" {
		existingRelease, err := r.db.GetAlbumReleaseByAOTYURL(ctx, item.AOTYURL)
		if err == nil && existingRelease != nil {
			existingEvent, err := r.db.GetLatestAlbumReleaseEvent(ctx, existingRelease.ID)
			if err == nil && existingEvent != nil {
				r.log.Info().Str("album", item.Title).Msg("already have this album, skipping")
				return nil
			}
		}
	}

	mbAlbumID := ""
	mbArtistID := ""
	mbArtistName := item.ArtistName
	var mbGenres []string
	mbRating := 0.0

	artists := strings.Split(item.ArtistName, " / ")
	for i, a := range artists {
		artists[i] = strings.TrimSpace(a)
	}

	var rgResult *MBReleaseGroupResult
	var err error
	for _, a := range artists {
		rgResult, err = r.mb.SearchReleaseGroup(apiCtx, item.Title, a)
		if err == nil && rgResult != nil {
			break
		}
	}

	if err != nil {
		r.log.Warn().Err(err).Str("album", item.Title).Msg("MusicBrainz search failed, storing without MB data")
	} else if rgResult == nil {
		blindResult, blindErr := r.mb.SearchReleaseGroupByAlbum(apiCtx, item.Title)
		if blindErr == nil && blindResult != nil {
			match := false
			for _, a := range artists {
				if artistNamesMatch(a, blindResult.ArtistName) {
					match = true
					break
				}
			}
			if match {
				mbAlbumID = blindResult.MBID
				mbArtistID = blindResult.ArtistMBID
				r.log.Info().Str("album", item.Title).Msg("found via blind MB search")
			} else {
				r.log.Warn().Str("scraped_artist", item.ArtistName).
					Str("mb_artist", blindResult.ArtistName).
					Str("album", item.Title).
					Msg("artist mismatch with blind MB search, storing without MB data")
			}
		}
		if mbAlbumID == "" {
			r.log.Info().Str("album", item.Title).Msg("not found in MusicBrainz, storing without MB data")
		}
	} else {
		match := false
		for _, a := range artists {
			if artistNamesMatch(a, rgResult.ArtistName) {
				match = true
				break
			}
		}
		if !match {
			r.log.Warn().Str("scraped_artist", item.ArtistName).
				Str("mb_artist", rgResult.ArtistName).
				Str("album", item.Title).
				Msg("artist mismatch in MB search result, storing without MB data")
		} else {
			mbAlbumID = rgResult.MBID
			mbArtistID = rgResult.ArtistMBID
			r.log.Info().Str("album", item.Title).Msg("found in MusicBrainz")
		}

		if mbAlbumID != "" {
			r.log.Info().Str("mbid", mbAlbumID).Msg("musicbrainz: fetching release group detail")
			detail, err := r.mb.GetReleaseGroupDetail(apiCtx, mbAlbumID)
			if err == nil && detail != nil {
				mbRating = detail.Rating
				mbGenres = detail.Genres
			}
		}
	}

	genresStr := item.Genres
	if genresStr == "" {
		genresStr = strings.Join(mbGenres, ", ")
	}

	release := &model.AlbumRelease{
		ArtistName:      mbArtistName,
		Title:           item.Title,
		Year:            item.Year,
		MBID:            mbAlbumID,
		ArtistMBID:      mbArtistID,
		AlbumType:       item.AlbumType,
		ReleaseDate:     item.ReleaseDate,
		Genres:          genresStr,
		PosterPath:      item.ImageURL,
		AOTYURL:         item.AOTYURL,
		AOTYCriticScore: item.AOTYCriticScore,
		AOTYCriticCount: item.AOTYCriticCount,
		AOTYUserScore:   item.AOTYUserScore,
		AOTYUserCount:   item.AOTYUserCount,
		AOTYMustHear:    item.AOTYMustHear,
		AllMusicRating:  item.AllMusicRating,
		AllMusicURL:     item.AllMusicURL,
		MBRating:        mbRating,
		Overview:        item.Overview,
	}

	if result := FilterMusic(&r.cfg.MediaTypes.Music.Filter.ContentFilter, release); !result.Passed {
		r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Str("reason", result.Reason).
			Msg("content filter: skipping")
		return nil
	}

	releaseID, err := r.db.UpsertAlbumRelease(ctx, release)
	if err != nil {
		return fmt.Errorf("saving album release: %w", err)
	}

	existing, err := r.db.GetLatestAlbumReleaseEvent(ctx, releaseID)
	if err != nil {
		return fmt.Errorf("checking existing events: %w", err)
	}

	if existing != nil {
		switch existing.Status {
		case model.StatusPending:
			return nil
		case model.StatusApproved, model.StatusDownloaded:
			return nil
		case model.StatusRejected:
			if item.Source == "allmusic" {
				notes := existing.Notes
				if notes != "" {
					notes += "; "
				}
				notes += "AllMusic Editor's Choice"
				if err := r.db.RequeueAlbumReleaseEvent(ctx, existing.ID, "allmusic", notes); err != nil {
					return fmt.Errorf("re-queuing album release event: %w", err)
				}
				r.log.Info().Str("artist", item.ArtistName).Str("album", item.Title).Msg("re-queued from rejected by AllMusic Editor's Choice")
				return nil
			}
			return nil
		}
	}

	evt := &model.AlbumReleaseEvent{
		ReleaseID:   releaseID,
		Source:      item.Source,
		ReleaseDate: item.ReleaseDate,
		Status:      model.StatusPending,
		ISOYear:     progYear,
		ISOWeek:     progWeek,
	}
	if _, err := r.db.CreateAlbumReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("saving album release event: %w", err)
	}

	return nil
}

func artistNamesMatch(a, b string) bool {
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u02bc", "'").Replace(s)
		t := norm.NFKD.String(s)
		var out strings.Builder
		for _, r := range t {
			if unicode.Is(unicode.Mn, r) {
				continue
			}
			out.WriteRune(r)
		}
		return out.String()
	}
	return normalize(a) == normalize(b)
}
