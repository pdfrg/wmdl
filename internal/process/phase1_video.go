package process

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

type SearchResult struct {
	Event    db.EventWithTitle
	Season   int
	Stripped string
	Top      []quality.ParsedRelease
	Error    error
}

func (e *Executor) ProcessApproved(ctx context.Context, evt db.EventWithTitle) error {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		return sr.Error
	}
	return e.PresentResult(ctx, sr)
}

func (e *Executor) SearchEvent(ctx context.Context, evt db.EventWithTitle) *SearchResult {
	title := evt.Title

	season := quality.ParseSeasonNumber(title.Title)
	stripped := quality.StripSeason(title.Title)

	e.log.Info().Str("title", title.Title).Int("year", title.Year).Int("season", season).Str("stripped", stripped).Msg("processing title")

	releases, err := e.searchRelease(ctx, title, stripped, season)
	if err != nil {
		return &SearchResult{Event: evt, Error: fmt.Errorf("searching releases: %w", err)}
	}
	if len(releases) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s (%d)", title.Title, title.Year))
		return &SearchResult{Event: evt, Season: season, Stripped: stripped}
	}

	cat := search.CatMovie
	switch title.MediaType {
	case model.MediaTypeTV:
		cat = search.CatTV
	case model.MediaTypeAnime:
		cat = search.CatAnime
	}
	prefs := buildQualityPrefs(e.cfg, title.MediaType, e.prowl.PreferredIndexerIDs(cat))
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

	return &SearchResult{
		Event:    evt,
		Season:   season,
		Stripped: stripped,
		Top:      top,
	}
}

func (e *Executor) SearchAll(ctx context.Context, events []db.EventWithTitle) []*SearchResult {
	e.log.Info().Msgf("Searching %d items...", len(events))
	results := make([]*SearchResult, 0, len(events))
	for i, ev := range events {
		e.log.Info().Str("title", ev.Title.Title).Msgf("[%d/%d] searching", i+1, len(events))
		sr := e.SearchEvent(ctx, ev)
		if sr.Error != nil {
			e.log.Warn().Err(sr.Error).Str("title", ev.Title.Title).Msg("error searching")
			sr.Error = nil
		} else if len(sr.Top) == 0 {
			e.log.Info().Str("title", ev.Title.Title).Msg("no results")
		} else {
			e.log.Info().Int("count", len(sr.Top)).Str("title", ev.Title.Title).Msg("results found")
		}
		results = append(results, sr)
	}
	return results
}

func (e *Executor) presentPicker(ctx context.Context, sr *SearchResult) ([]quality.ParsedRelease, error) {
	if len(sr.Top) == 0 {
		return nil, nil
	}
	sel := NewSelector(sr.Event.Title.Title, sr.Top)
	chosen, err := sel.Run()
	if err != nil {
		return nil, err
	}
	return chosen, nil
}

func (e *Executor) addToClient(ctx context.Context, evt db.EventWithTitle, chosen []quality.ParsedRelease) {
	if e.dl == nil {
		return
	}
	title := evt.Title
	if title == nil {
		e.log.Warn().Msg("addToClient called with nil title")
		return
	}
	category := e.cfg.Downloader.Categories.Movies
	switch title.MediaType {
	case model.MediaTypeTV:
		category = e.cfg.Downloader.Categories.TV
	case model.MediaTypeAnime:
		category = e.cfg.Downloader.Categories.Anime
	}

	var releaseEventID int64
	if evt.Event != nil {
		releaseEventID = evt.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Int("score", release.Score).Msg("selected")
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		var torrentID string
		if uri != "" {
			tid, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category))
			if err != nil {
				e.log.Warn().Err(err).Msg("direct add failed")
			} else {
				torrentID = tid
				e.log.Info().Str("client", e.cfg.Downloader.Type).Str("category", category).Msg("added to download client")
			}
		}

		dl := &model.Download{
			TitleID:         title.ID,
			ReleaseEventID:  releaseEventID,
			Quality:         fmt.Sprintf("%dp", release.Resolution),
			SourceType:      release.Source,
			Codec:           release.Codec,
			InfoHash:        release.InfoHash,
			Category:        category,
			Status:          model.DownloadAdded,
			ClientTorrentID: torrentID,
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("creating download record")
		}
	}
}

func (e *Executor) handleUpgrade(ctx context.Context, evt db.EventWithTitle) {
	title := evt.Title
	if evt.Event.Notes != "" && strings.Contains(evt.Event.Notes, "upgrade:") {
		oldDL, err := e.db.GetDownloadByTitleID(ctx, title.ID)
		if err == nil && oldDL != nil {
			if err := e.db.UpdateDownloadStatus(ctx, oldDL.ID, model.DownloadUpgraded); err != nil {
				e.log.Warn().Err(err).Msg("marking old download as upgraded")
			} else {
				e.log.Info().Msg("marked previous download as upgraded")
			}
		}
	}
}

func (e *Executor) markDownloaded(ctx context.Context, evt db.EventWithTitle) {
	if err := e.db.UpdateReleaseEventStatus(ctx, evt.Event.ID, model.StatusDownloaded); err != nil {
		e.log.Warn().Err(err).Msg("updating event status")
	}
}

func (e *Executor) grabViaProwlarr(ctx context.Context, evt db.EventWithTitle, chosen []quality.ParsedRelease) {
	title := evt.Title
	if title == nil {
		e.log.Warn().Msg("grabViaProwlarr called with nil title")
		return
	}

	var releaseEventID int64
	if evt.Event != nil {
		releaseEventID = evt.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Int("score", release.Score).Int("indexer", release.IndexerID).Msg("grabbing via Prowlarr")

		if err := e.prowl.Grab(ctx, release.IndexerID, release.Guid); err != nil {
			e.log.Warn().Err(err).Str("release", release.RawTitle).Msg("prowlarr grab failed")
			continue
		}

		dl := &model.Download{
			TitleID:        title.ID,
			ReleaseEventID: releaseEventID,
			Quality:        fmt.Sprintf("%dp", release.Resolution),
			SourceType:     release.Source,
			Codec:          release.Codec,
			InfoHash:       release.InfoHash,
			Status:         model.DownloadAdded,
		}
		if _, err := e.db.CreateDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("creating download record")
		}
	}
}

func (e *Executor) ProcessGrabResults(ctx context.Context, results []*SearchResult) {
	for _, sr := range results {
		if len(sr.Top) == 0 {
			continue
		}
		chosen, err := e.presentPicker(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("title", sr.Event.Title.Title).Msg("picker error")
			continue
		}
		if len(chosen) == 0 {
			e.log.Info().Str("title", sr.Event.Title.Title).Msg("skipped")
			label := sr.Event.Title.Title
			if sr.Event.Title.Year > 0 {
				label = fmt.Sprintf("%s (%d)", label, sr.Event.Title.Year)
			}
			e.Skipped = append(e.Skipped, label)
			continue
		}
		e.grabViaProwlarr(ctx, sr.Event, chosen)
		e.handleUpgrade(ctx, sr.Event)
		e.markDownloaded(ctx, sr.Event)
	}
}

func (e *Executor) SearchAndGrabOne(ctx context.Context, evt db.EventWithTitle) *PickedItem {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		e.log.Warn().Err(sr.Error).Str("title", evt.Title.Title).Msg("error searching")
		return nil
	}
	if len(sr.Top) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("no search results found, skipping")
		return nil
	}
	chosen, err := e.presentPicker(ctx, sr)
	if errors.Is(err, ErrAbort) {
		e.log.Info().Msg("pipeline aborted by user")
		return nil
	}
	if err != nil {
		e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("picker error")
		return nil
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		return nil
	}
	e.grabViaProwlarr(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)
	e.markDownloaded(ctx, evt)
	return nil
}

func (e *Executor) PresentResult(ctx context.Context, sr *SearchResult) error {
	if sr.Error != nil {
		return sr.Error
	}
	evt := sr.Event

	chosen, err := e.presentPicker(ctx, sr)
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		return nil
	}

	e.addToClient(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)

	if err := e.addToLibrary(ctx, evt, sr.Season); err != nil {
		e.log.Warn().Err(err).Msg("library add failed")
	}

	e.markDownloaded(ctx, evt)
	return nil
}

func (e *Executor) SearchAndPickAll(ctx context.Context, events []db.EventWithTitle) []PickedItem {
	return e.PickResults(ctx, e.SearchAll(ctx, events))
}

func (e *Executor) SearchAndPickOne(ctx context.Context, evt db.EventWithTitle) *PickedItem {
	sr := e.SearchEvent(ctx, evt)
	if sr.Error != nil {
		e.log.Warn().Err(sr.Error).Str("title", evt.Title.Title).Msg("error searching")
		return nil
	}
	if len(sr.Top) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("no search results found, skipping")
		return nil
	}
	chosen, err := e.presentPicker(ctx, sr)
	if errors.Is(err, ErrAbort) {
		e.log.Info().Msg("pipeline aborted by user")
		return nil
	}
	if err != nil {
		e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("picker error")
		return nil
	}
	if len(chosen) == 0 {
		e.log.Info().Str("title", evt.Title.Title).Msg("skipped")
		label := evt.Title.Title
		if evt.Title.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, evt.Title.Year)
		}
		e.Skipped = append(e.Skipped, label)
		e.handleSkipLibrary(ctx, evt, sr.Season)
		return nil
	}
	e.addToClient(ctx, evt, chosen)
	e.handleUpgrade(ctx, evt)
	e.markDownloaded(ctx, evt)
	return &PickedItem{Event: evt, Season: sr.Season, Chosen: chosen}
}

func (e *Executor) PickResults(ctx context.Context, results []*SearchResult) []PickedItem {
	var picked []PickedItem
	for _, sr := range results {
		if len(sr.Top) == 0 {
			continue
		}
		chosen, err := e.presentPicker(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("title", sr.Event.Title.Title).Msg("picker error")
			continue
		}
		if len(chosen) == 0 {
			e.log.Info().Str("title", sr.Event.Title.Title).Msg("skipped")
			label := sr.Event.Title.Title
			if sr.Event.Title.Year > 0 {
				label = fmt.Sprintf("%s (%d)", label, sr.Event.Title.Year)
			}
			e.Skipped = append(e.Skipped, label)
			e.handleSkipLibrary(ctx, sr.Event, sr.Season)
			continue
		}
		e.addToClient(ctx, sr.Event, chosen)
		e.handleUpgrade(ctx, sr.Event)
		e.markDownloaded(ctx, sr.Event)
		picked = append(picked, PickedItem{Event: sr.Event, Season: sr.Season, Chosen: chosen})
	}
	return picked
}

func (e *Executor) ProcessBatchResults(ctx context.Context, items []*BatchItem) (picked []PickedItem, albumResults []MusicAlbumResult, bookInfo map[int64]*BookDownloadInfo) {
	for _, item := range items {
		if item.Skipped {
			e.Skipped = append(e.Skipped, item.Label)
			if item.Phase3 && item.SearchResult != nil {
				sr := item.SearchResult
				if sr.Event.Title.MediaType == model.MediaTypeMovie && sr.Event.Title.TmdbID > 0 {
					e.phase3BatchProcessedMovies[sr.Event.Title.TmdbID] = true
				} else if (sr.Event.Title.MediaType == model.MediaTypeTV || sr.Event.Title.MediaType == model.MediaTypeAnime) && sr.Season > 0 && sr.Event.Title.TvdbID > 0 {
					if e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID] == nil {
						e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID] = make(map[int]bool)
					}
					e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID][sr.Season] = true
				}
			}
			continue
		}
		if len(item.Selected) == 0 {
			continue
		}

		switch {
		case item.Phase3 && item.BookResult != nil:
			e.processPhase3BookBatchItem(ctx, item)

		case item.Phase3:
			e.processPhase3BatchItem(ctx, item)

		case item.MusicResult != nil:
			result := e.processMusicBatchItem(ctx, item)
			if result != nil {
				albumResults = append(albumResults, *result)
			}

		case item.BookResult != nil:
			e.processBookBatchItem(ctx, item, &bookInfo)

		case item.SearchResult != nil:
			sr := item.SearchResult
			mode := e.cfg.MediaTypeMode(sr.Event.Title.MediaType)
			if mode == config.ProcessModeProwlarrGrab {
				e.grabViaProwlarr(ctx, sr.Event, item.Selected)
			} else {
				e.addToClient(ctx, sr.Event, item.Selected)
				picked = append(picked, PickedItem{Event: sr.Event, Season: sr.Season, Chosen: item.Selected})
			}
			e.handleUpgrade(ctx, sr.Event)
			e.markDownloaded(ctx, sr.Event)
		}
	}
	return
}

func (e *Executor) processPhase3BatchItem(ctx context.Context, item *BatchItem) {
	sr := item.SearchResult
	if sr == nil {
		return
	}

	if sr.Event.Title.MediaType == model.MediaTypeMovie && sr.Event.Title.TmdbID > 0 && !e.SkipRadarr() {
		existing, err := e.radarr.Exists(ctx, sr.Event.Title.TmdbID)
		if err == nil && existing == nil {
			profileID, err := resolveRadarrProfileID(ctx, e.radarr, e.cfg.Library.Radarr.QualityProfile)
			if err != nil {
				e.log.Warn().Err(err).Msg("failed to resolve Radarr quality profile")
				return
			}
			if _, addErr := e.radarr.Add(ctx, sr.Event.Title.TmdbID, sr.Event.Title.Title, sr.Event.Title.Year, library.AddMovieOptions{
				Monitored:           e.cfg.Library.Radarr.Monitor,
				MinimumAvailability: "released",
				QualityProfileID:    profileID,
				RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
				SearchNow:           false,
			}); addErr != nil {
				e.log.Warn().Err(addErr).Str("title", sr.Event.Title.Title).Msg("adding collection movie to Radarr")
			}
		}
	}

	category := e.cfg.Downloader.Categories.Movies
	if sr.Event.Title.MediaType == model.MediaTypeTV || sr.Event.Title.MediaType == model.MediaTypeAnime {
		category = e.cfg.Downloader.Categories.TV
	}
	for _, release := range item.Selected {
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		if uri != "" {
			if _, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category)); err != nil {
				e.log.Warn().Err(err).Str("title", sr.Event.Title.Title).Msg("phase 3 add failed")
			}
		}
	}
	e.log.Info().Str("title", sr.Event.Title.Title).Msg("phase 3 item downloaded")

	if sr.Event.Title.MediaType == model.MediaTypeMovie && sr.Event.Title.TmdbID > 0 {
		e.phase3BatchProcessedMovies[sr.Event.Title.TmdbID] = true
	} else if (sr.Event.Title.MediaType == model.MediaTypeTV || sr.Event.Title.MediaType == model.MediaTypeAnime) && item.SearchResult.Season > 0 && sr.Event.Title.TvdbID > 0 {
		if e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID] == nil {
			e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID] = make(map[int]bool)
		}
		e.phase3BatchProcessedSeasons[sr.Event.Title.TvdbID][item.SearchResult.Season] = true
	}
}

func (e *Executor) handleSkipLibrary(ctx context.Context, evt db.EventWithTitle, season int) {
	mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
	if mode != config.ProcessModeFull {
		return
	}
	switch evt.Title.MediaType {
	case model.MediaTypeTV, model.MediaTypeAnime:
		if e.SkipSonarr() {
			return
		}
		if tvdbID := evt.Title.TvdbID; tvdbID > 0 {
			existing, err := e.sonarr.Exists(ctx, tvdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("checking Sonarr, proceeding without existence info")
			}
			if existing == nil {
				if lookup, err := e.sonarr.Lookup(ctx, tvdbID); err == nil && lookup != nil {
					e.log.Info().Msgf("  Sonarr: %s (%d)", lookup.Title, lookup.Year)
					if lookup.Overview != "" {
						for _, line := range formatOverview(lookup.Overview, 72) {
							e.log.Info().Msg(line)
						}
					}
				}
				if PromptYesNo(ctx, fmt.Sprintf("  Add %s to Sonarr anyway?", evt.Title.Title)) {
					beforeSeasons := len(e.phase3Seasons)
					if err := e.addToSonarr(ctx, evt, tvdbID, season, true, false, true); err != nil {
						e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("adding to Sonarr after skip")
					} else {
						e.phase3Seasons = e.phase3Seasons[:beforeSeasons]
						e.markDownloaded(ctx, evt)
					}
				} else {
					e.skipRejectedTvdbIDs[tvdbID] = struct{}{}
				}
			}
		}
	case model.MediaTypeMovie:
		if e.SkipRadarr() {
			return
		}
		if tmdbID := evt.Title.TmdbID; tmdbID > 0 {
			existing, err := e.radarr.Exists(ctx, tmdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("checking Radarr, proceeding without existence info")
			}
			if existing == nil {
				if lookup, err := e.radarr.Lookup(ctx, tmdbID); err == nil && lookup != nil {
					e.log.Info().Msgf("  Radarr: %s (%d)", lookup.Title, lookup.Year)
					if lookup.Overview != "" {
						for _, line := range formatOverview(lookup.Overview, 72) {
							e.log.Info().Msg(line)
						}
					}
				}
				if PromptYesNo(ctx, fmt.Sprintf("  Add %s to Radarr anyway?", evt.Title.Title)) {
					if err := e.addToRadarr(ctx, evt, true, false, true); err != nil {
						e.log.Warn().Err(err).Str("title", evt.Title.Title).Msg("adding to Radarr after skip")
					} else {
						e.markDownloaded(ctx, evt)
					}
				}
			}
		}
	}
}
