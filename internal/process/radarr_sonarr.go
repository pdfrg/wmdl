package process

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
)

func (e *Executor) addToLibrary(ctx context.Context, evt db.EventWithTitle, season int) error {
	if evt.Title.MediaType == model.MediaTypeMovie {
		return e.addToRadarr(ctx, evt, false, false, e.cfg.Library.Radarr.Monitor)
	}
	tvdbID := evt.Title.TvdbID
	if tvdbID == 0 && evt.Title.MediaType == model.MediaTypeAnime {
		lookup, err := e.sonarr.LookupByTitle(ctx, evt.Title.Title)
		if err != nil {
			return fmt.Errorf("anime title lookup in Sonarr: %w", err)
		}
		if lookup == nil {
			e.log.Warn().Str("title", evt.Title.Title).Msg("anime not found in Sonarr by title lookup")
			return nil
		}
		tvdbID = lookup.TVDBID
	}
	return e.addToSonarr(ctx, evt, tvdbID, season, false, false, false)
}

func (e *Executor) addToRadarr(ctx context.Context, evt db.EventWithTitle, confirmed bool, searchNow bool, monitored bool) error {
	tmdbID := evt.Title.TmdbID
	if tmdbID == 0 {
		return fmt.Errorf("cannot add to Radarr: movie %q has no TMDB ID", evt.Title.Title)
	}

	if !confirmed {
		existing, err := e.radarr.Exists(ctx, tmdbID)
		if err != nil {
			return err
		}
		if existing != nil {
			e.log.Info().Str("title", evt.Title.Title).Msg("already in Radarr")
			return nil
		}

		lookup, err := e.radarr.Lookup(ctx, tmdbID)
		if err != nil {
			return err
		}
		if lookup == nil {
			return fmt.Errorf("movie not found on TMDB (tmdb_id=%d)", tmdbID)
		}

		prompt := fmt.Sprintf("  Add to Radarr? %s (%d)", lookup.Title, lookup.Year)
		if searchNow {
			prompt += " [will trigger search]"
		}
		e.log.Info().Msg(prompt)
		if lookup.Overview != "" {
			for _, line := range formatOverview(lookup.Overview, 72) {
				e.log.Info().Msg(line)
			}
		}
		e.log.Info().Str("profile", e.cfg.Library.Radarr.QualityProfile).Str("root", e.cfg.Library.Radarr.RootFolder).Msg("Radarr config")
		if !PromptYesNo(ctx, "  Add to Radarr?") {
			return nil
		}
	}

	title := evt.Title.Title
	year := evt.Title.Year
	profileID, err := resolveRadarrProfileID(ctx, e.radarr, e.cfg.Library.Radarr.QualityProfile)
	if err != nil {
		return fmt.Errorf("resolving Radarr quality profile: %w", err)
	}

	added, err := e.radarr.Add(ctx, tmdbID, title, year, library.AddMovieOptions{
		Monitored:           monitored,
		MinimumAvailability: "released",
		QualityProfileID:    profileID,
		RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
		SearchNow:           searchNow,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Radarr")

	if e.cfg.CheckCollections {
		if added.Collection != nil && added.Collection.TMDBID > 0 {
			e.log.Info().Str("title", title).Str("collection", added.Collection.Name).Int("tmdb", added.Collection.TMDBID).Msg("checking collection gaps")
			phase3FromCollection := e.checkCollectionGaps(ctx, tmdbID, added.Collection.TMDBID, profileID, searchNow)
			e.phase3Movies = append(e.phase3Movies, phase3FromCollection...)
			if !searchNow && len(phase3FromCollection) > 0 {
				e.log.Info().Int("count", len(phase3FromCollection)).Msg("pre-searching newly added collection movies")
				e.SearchAllCandidates(ctx, phase3MoviesToCandidates(phase3FromCollection))
			}
		} else {
			e.log.Info().Str("title", title).Msg("movie not part of a TMDB collection, skipping")
		}
	}

	return nil
}

func (e *Executor) AddAiringAnimeToSonarr(ctx context.Context, evt db.EventWithTitle, searchNow bool) (*library.SonarrSeries, error) {
	if e.sonarr == nil {
		e.log.Warn().Msg("Sonarr not configured, skipping anime addition")
		return nil, nil
	}

	season := quality.ParseSeasonNumber(evt.Title.Title)
	stripped := quality.StripSeason(evt.Title.Title)
	searchTitle := sanitizeSearchQuery(stripped)

	lookup, err := e.sonarr.LookupByTitle(ctx, searchTitle)
	if err != nil {
		return nil, fmt.Errorf("looking up anime in Sonarr: %w", err)
	}
	if lookup == nil {
		e.log.Warn().Str("title", searchTitle).Msg("anime not found on Sonarr via title lookup")
		return nil, nil
	}

	existing, err := e.sonarr.Exists(ctx, lookup.TVDBID)
	if err == nil && existing != nil {
		e.log.Info().Str("title", searchTitle).Int("id", existing.ID).Msg("already in Sonarr")
		return existing, nil
	}

	prompt := fmt.Sprintf("  Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
	if searchNow {
		prompt += " [will trigger search]"
	}
	e.log.Info().Msg(prompt)
	if lookup.Overview != "" {
		for _, line := range formatOverview(lookup.Overview, 72) {
			e.log.Info().Msg(line)
		}
	}
	e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
	if searchNow {
		e.log.Info().Str("title", searchTitle).Msg("auto-adding airing anime to Sonarr")
	} else if !PromptYesNo(ctx, "  Add to Sonarr?") {
		e.log.Info().Str("title", searchTitle).Msg("skipped adding airing anime to Sonarr")
		return nil, nil
	}

	profileID, err := resolveSonarrProfileID(ctx, e.sonarr, e.cfg.Library.Sonarr.QualityProfile)
	if err != nil {
		return nil, fmt.Errorf("resolving Sonarr quality profile: %w", err)
	}
	langProfiles, lErr := e.sonarr.GetLanguageProfiles(ctx)
	if lErr != nil {
		return nil, lErr
	}
	langProfileID := 1
	if len(langProfiles) > 0 {
		langProfileID = langProfiles[0].ID
	}

	var seasons []library.SonarrSeason
	for _, s := range lookup.Seasons {
		if s.SeasonNumber > 0 {
			seasons = append(seasons, library.SonarrSeason{
				SeasonNumber: s.SeasonNumber,
				Monitored:    s.SeasonNumber == season,
			})
		}
	}

	opts := library.AddSeriesOptions{
		Monitored:         true,
		SeasonFolder:      e.cfg.Library.Sonarr.SeasonFolders,
		QualityProfileID:  profileID,
		LanguageProfileID: langProfileID,
		RootFolderPath:    e.cfg.Library.Sonarr.RootFolder,
		Seasons:           seasons,
		SearchForMissing:  searchNow,
	}

	series, err := e.sonarr.Add(ctx, lookup.TVDBID, lookup.Title, lookup.Year, opts)
	if err != nil {
		return nil, fmt.Errorf("adding anime to Sonarr: %w", err)
	}

	e.log.Info().Str("title", series.Title).Int("id", series.ID).Msg("added airing anime to Sonarr")
	return series, nil
}

func (e *Executor) SearchAiringAnimeEarlierSeasons(ctx context.Context, evt db.EventWithTitle, series *library.SonarrSeries) {
	season := quality.ParseSeasonNumber(evt.Title.Title)
	if season <= 1 {
		return
	}

	displayTitle := series.Title
	if displayTitle == "" {
		displayTitle = evt.Title.Title
	}

	missing := e.checkExistingSonarrSeasons(ctx, series, season)
	mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
	tvdbID := series.TVDBID
	if tvdbID == 0 {
		tvdbID = evt.Title.TvdbID
	}
	for _, ms := range missing {
		seasonNum := ms.SeasonNumber
		markProcessed := func() {
			if tvdbID > 0 {
				if e.phase3BatchProcessedSeasons[tvdbID] == nil {
					e.phase3BatchProcessedSeasons[tvdbID] = make(map[int]bool)
				}
				e.phase3BatchProcessedSeasons[tvdbID][seasonNum] = true
			}
		}

		searchSeason := false
		if mode == config.ProcessModeAuto || mode == config.ProcessModeYolo {
			searchSeason = true
			e.log.Info().Str("title", displayTitle).Int("season", ms.SeasonNumber).Msg("auto-searching earlier season")
		} else {
			searchSeason = PromptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", displayTitle, ms.SeasonNumber))
		}
		if !searchSeason {
			markProcessed()
			continue
		}

		var entry *Phase3SearchEntry
		for i := range e.Phase3SearchPhase {
			c := &e.Phase3SearchPhase[i].Candidate
			if c.Title == evt.Title.Title && c.Season == ms.SeasonNumber {
				entry = &e.Phase3SearchPhase[i]
				break
			}
		}
		if entry == nil || len(entry.Top) == 0 {
			e.log.Info().Str("title", displayTitle).Int("season", ms.SeasonNumber).Msg("no pre-searched results for earlier season, falling back")
			e.searchPhase3Season(ctx, struct {
				SeriesID     int
				SeasonNumber int
				SeriesTitle  string
				MediaType    model.MediaType
			}{
				SeriesID:     series.ID,
				SeasonNumber: ms.SeasonNumber,
				SeriesTitle:  displayTitle,
				MediaType:    evt.Title.MediaType,
			})
			markProcessed()
			continue
		}

		sr := &SearchResult{
			Event:  db.EventWithTitle{Title: &model.Title{Title: displayTitle, MediaType: evt.Title.MediaType}},
			Season: ms.SeasonNumber,
			Top:    entry.Top,
		}
		chosen, err := e.presentPicker(ctx, sr)
		markProcessed()
		if err != nil || len(chosen) == 0 {
			continue
		}
		synthEvent := db.EventWithTitle{Title: &model.Title{Title: displayTitle, MediaType: evt.Title.MediaType}}
		e.addToClient(ctx, synthEvent, chosen)
	}
}

func (e *Executor) addToSonarr(ctx context.Context, evt db.EventWithTitle, tvdbID, season int, confirmed bool, searchNow bool, monitorTarget bool) error {
	if tvdbID == 0 {
		return nil
	}

	if !confirmed {
		existing, err := e.sonarr.Exists(ctx, tvdbID)
		if err != nil {
			return err
		}
		if existing != nil {
			series, err := e.sonarr.GetSeries(ctx, existing.ID)
			if err != nil {
				return err
			}
			targetSeason := findSeasonByNumber(series.Seasons, season)
			if targetSeason != nil && targetSeason.Statistics != nil &&
				targetSeason.Statistics.EpisodeFileCount >= targetSeason.Statistics.EpisodeCount &&
				targetSeason.Statistics.EpisodeCount > 0 {
				e.log.Info().Str("title", evt.Title.Title).Int("season", season).Msg("season already complete in Sonarr")
				return nil
			}
			e.log.Info().Str("title", evt.Title.Title).Int("season", season).Msg("already in Sonarr, season incomplete")
			return nil
		}

		lookup, err := e.sonarr.Lookup(ctx, tvdbID)
		if err != nil {
			return err
		}
		if lookup == nil {
			return fmt.Errorf("series not found on TVDB (tvdb_id=%d)", tvdbID)
		}

		prompt := fmt.Sprintf("  Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
		if searchNow {
			prompt += " [will trigger search]"
		}
		e.log.Info().Msg(prompt)
		if lookup.Overview != "" {
			for _, line := range formatOverview(lookup.Overview, 72) {
				e.log.Info().Msg(line)
			}
		}
		e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
		if !PromptYesNo(ctx, "  Add to Sonarr?") {
			return nil
		}
	}

	title := evt.Title.Title
	year := evt.Title.Year
	profileID, err := resolveSonarrProfileID(ctx, e.sonarr, e.cfg.Library.Sonarr.QualityProfile)
	if err != nil {
		return fmt.Errorf("resolving Sonarr quality profile: %w", err)
	}

	langProfiles, lErr := e.sonarr.GetLanguageProfiles(ctx)
	if lErr != nil {
		return lErr
	}
	langProfileID := 1
	if len(langProfiles) > 0 {
		langProfileID = langProfiles[0].ID
	}

	lookup, err := e.sonarr.Lookup(ctx, tvdbID)
	if err != nil {
		return err
	}
	if lookup == nil {
		return fmt.Errorf("series not found on TVDB (tvdb_id=%d)", tvdbID)
	}

	var seasons []library.SonarrSeason
	for _, s := range lookup.Seasons {
		if s.SeasonNumber > 0 {
			monitored := false
			if monitorTarget && s.SeasonNumber == season {
				monitored = true
			}
			seasons = append(seasons, library.SonarrSeason{
				SeasonNumber: s.SeasonNumber,
				Monitored:    monitored,
			})
		}
	}

	added, err := e.sonarr.Add(ctx, tvdbID, title, year, library.AddSeriesOptions{
		Monitored:         true,
		SeasonFolder:      e.cfg.Library.Sonarr.SeasonFolders,
		QualityProfileID:  profileID,
		LanguageProfileID: langProfileID,
		RootFolderPath:    e.cfg.Library.Sonarr.RootFolder,
		Seasons:           seasons,
		SearchForMissing:  searchNow,
	})
	if err != nil {
		return err
	}
	e.log.Info().Str("title", added.Title).Int("id", added.ID).Msg("added to Sonarr")

	if season > 1 && added.ID > 0 {
		mode := e.cfg.MediaTypeMode(evt.Title.MediaType)
		for s := 1; s < season; s++ {
			if e.phase3BatchProcessedSeasons[tvdbID][s] {
				e.log.Info().Str("title", title).Int("season", s).Msg("already processed via batch picker, skipping")
				continue
			}
			enqueue := false
			if mode == config.ProcessModeAuto || mode == config.ProcessModeYolo {
				enqueue = true
				e.log.Info().Str("title", title).Int("season", s).Msg("auto-queueing earlier season search")
			} else {
				enqueue = PromptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", title, s))
			}
			if enqueue {
				e.phase3Seasons = append(e.phase3Seasons, struct {
					SeriesID     int
					SeasonNumber int
					SeriesTitle  string
					MediaType    model.MediaType
					TvdbID       int
				}{
					SeriesID:     added.ID,
					SeasonNumber: s,
					SeriesTitle:  title,
					MediaType:    evt.Title.MediaType,
					TvdbID:       tvdbID,
				})
			}
		}
	}

	return nil
}

func findSeasonByNumber(seasons []library.SonarrSeason, num int) *library.SonarrSeason {
	for i := range seasons {
		if seasons[i].SeasonNumber == num {
			return &seasons[i]
		}
	}
	return nil
}

func (e *Executor) checkExistingSonarrSeasons(ctx context.Context, series *library.SonarrSeries, currentSeason int) []library.SonarrSeason {
	var missing []library.SonarrSeason
	for _, s := range series.Seasons {
		if s.SeasonNumber == 0 || s.SeasonNumber >= currentSeason {
			continue
		}
		if s.Statistics != nil && s.Statistics.EpisodeFileCount > 0 {
			continue
		}
		if s.Statistics != nil && s.Statistics.TotalEpisodeCount == 0 {
			continue
		}
		missing = append(missing, s)
	}
	if len(missing) > 0 {
		e.log.Info().Str("series", series.Title).Msgf("Found %d earlier season(s) missing in Sonarr", len(missing))
	} else {
		e.log.Info().Str("series", series.Title).Msg("all earlier seasons already have files in Sonarr")
	}
	return missing
}

func (e *Executor) checkCollectionGaps(ctx context.Context, tmdbID int, colTMDBID int, profileID int, searchNow bool) []Phase3Movie {
	allMovies, err := e.radarr.GetAllMovies(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Radarr error fetching movie list: %v\n", err)
		fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip collections  [q] quit pipeline\n")
		fmt.Fprintf(os.Stderr, "  Choose: ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "r", "retry":
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID, searchNow)
			case "q", "quit":
				return nil
			}
		case <-ctx.Done():
			return nil
		}
		return nil
	}

	movieByTMDB := make(map[int]library.RadarrMovie, len(allMovies))
	for _, m := range allMovies {
		movieByTMDB[m.TMDBID] = m
	}

	collections, err := e.radarr.GetCollections(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Radarr collections error: %v\n", err)
		fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip collections  [q] quit pipeline\n")
		fmt.Fprintf(os.Stderr, "  Choose: ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "r", "retry":
				return e.checkCollectionGaps(ctx, tmdbID, colTMDBID, profileID, searchNow)
			case "q", "quit":
				return nil
			}
		case <-ctx.Done():
			return nil
		}
		return nil
	}

	var phase3 []Phase3Movie

	for _, col := range collections {
		if col.TMDBID != colTMDBID {
			continue
		}
		var missing []library.RadarrMovie
		seen := make(map[int]bool)
		for _, m := range col.Movies {
			seen[m.TMDBID] = true
			if m.TMDBID == tmdbID {
				continue
			}
			if m.Status != "" && m.Status != "released" {
				e.log.Debug().Str("title", m.Title).Str("status", m.Status).Int("tmdb", m.TMDBID).Msg("collection movie not released, skipping")
				continue
			}
			if existing, ok := movieByTMDB[m.TMDBID]; ok && existing.HasFile {
				continue
			}
			missing = append(missing, m)
		}

		if c, cErr := e.db.GetLibraryCache(ctx, "tmdb-collection", strconv.Itoa(colTMDBID)); cErr == nil && c != nil {
			var data struct {
				Name   string `json:"name"`
				Movies []struct {
					TmdbID int    `json:"tmdb_id"`
					Title  string `json:"title"`
					Year   int    `json:"year"`
				} `json:"movies"`
			}
			if json.Unmarshal([]byte(c.Details), &data) == nil {
				for _, cm := range data.Movies {
					if seen[cm.TmdbID] {
						continue
					}
					if cm.TmdbID == tmdbID {
						continue
					}
					if ex, ok := movieByTMDB[cm.TmdbID]; ok && ex.HasFile {
						continue
					}
					missing = append(missing, library.RadarrMovie{
						TMDBID: cm.TmdbID,
						Title:  cm.Title,
						Year:   cm.Year,
					})
				}
			}
		}
		if len(missing) == 0 {
			e.log.Info().Str("collection", col.Name).Msg("no missing movies in collection")
			continue
		}
		e.log.Info().Str("collection", col.Name).Msgf("collection has %d missing movie(s)", len(missing))
		for _, m := range missing {
			addIt := false
			mode := e.cfg.MediaTypeMode(model.MediaTypeMovie)
			if mode == config.ProcessModeAuto || mode == config.ProcessModeYolo {
				addIt = true
				e.log.Info().Str("title", m.Title).Str("collection", col.Name).Msg("auto-adding collection movie")
			} else {
				addIt = PromptYesNo(ctx, fmt.Sprintf("    Collection %q: Add %s?", col.Name, m.Title))
			}
			if addIt {
				if _, err := e.radarr.Add(ctx, m.TMDBID, m.Title, m.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           searchNow,
				}); err != nil {
					e.log.Warn().Err(err).Str("title", m.Title).Msg("error adding movie to collection")
				} else {
					e.log.Info().Str("title", m.Title).Msg("added movie to collection")
					phase3 = append(phase3, Phase3Movie{
						Title:     m.Title,
						Year:      m.Year,
						TMDBID:    m.TMDBID,
						ProfileID: profileID,
						RootPath:  e.cfg.Library.Radarr.RootFolder,
					})
				}
			}
		}
	}
	return phase3
}
