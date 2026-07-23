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
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

func (e *Executor) ComputePhase3Candidates(ctx context.Context, events []db.EventWithTitle) []Phase3Candidate {
	var candidates []Phase3Candidate

	var radarrCollections []library.RadarrCollection
	if e.cfg.CheckCollections && e.radarr != nil {
		var err error
		radarrCollections, err = e.radarr.GetCollections(ctx)
		if err != nil {
			e.log.Warn().Err(err).Msg("failed to fetch Radarr collections, skipping Phase 3 movies")
		}
	}
	var movieByTMDB map[int]library.RadarrMovie
	if e.radarr != nil {
		allRadarrMovies, err := e.radarr.GetAllMovies(ctx)
		if err != nil {
			e.log.Warn().Err(err).Msg("failed to fetch all Radarr movies for Phase 3")
		} else {
			movieByTMDB = make(map[int]library.RadarrMovie, len(allRadarrMovies))
			for _, m := range allRadarrMovies {
				movieByTMDB[m.TMDBID] = m
			}
		}
	}

	for _, ev := range events {
		if ev.Title.MediaType == model.MediaTypeMovie {
			if !e.cfg.CheckCollections || e.radarr == nil {
				continue
			}
			tmdbID := ev.Title.TmdbID
			if tmdbID == 0 {
				e.log.Warn().Str("title", ev.Title.Title).Msg("no TMDB ID, skipping collection gap check")
				continue
			}
			existing, err := e.radarr.Exists(ctx, tmdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("radarr check for phase 3")
				continue
			}
			if existing == nil || existing.Collection == nil || existing.Collection.TMDBID == 0 {
				if ev.Title.CollectionID > 0 {
					if c, cErr := e.db.GetLibraryCache(ctx, "tmdb-collection", strconv.Itoa(ev.Title.CollectionID)); cErr == nil && c != nil {
						var data struct {
							Name   string `json:"name"`
							Movies []struct {
								TmdbID int    `json:"tmdb_id"`
								Title  string `json:"title"`
								Year   int    `json:"year"`
							} `json:"movies"`
						}
						if json.Unmarshal([]byte(c.Details), &data) == nil {
							for _, m := range data.Movies {
								if m.TmdbID == tmdbID {
									continue
								}
								if ex, ok := movieByTMDB[m.TmdbID]; ok && ex.HasFile {
									continue
								}
								candidates = append(candidates, Phase3Candidate{
									Title:     m.Title,
									Year:      m.Year,
									MediaType: model.MediaTypeMovie,
									TmdbID:    m.TmdbID,
								})
							}
						}
					}
				}
				continue
			}

			for _, col := range radarrCollections {
				if col.TMDBID != existing.Collection.TMDBID {
					continue
				}
				for _, m := range col.Movies {
					if m.TMDBID == tmdbID {
						continue
					}
					if m.Status != "" && m.Status != "released" {
						continue
					}
					if existing, ok := movieByTMDB[m.TMDBID]; ok && existing.HasFile {
						continue
					}
					candidates = append(candidates, Phase3Candidate{
						Title:     m.Title,
						Year:      m.Year,
						MediaType: model.MediaTypeMovie,
						TmdbID:    m.TMDBID,
					})
				}
			}
		}

		if ev.Title.MediaType == model.MediaTypeTV || ev.Title.MediaType == model.MediaTypeAnime {
			season := quality.ParseSeasonNumber(ev.Title.Title)
			if season <= 1 {
				continue
			}

			tvdbID := ev.Title.TvdbID
			if tvdbID == 0 && ev.Title.MediaType == model.MediaTypeAnime {
				lookup, err := e.sonarr.LookupByTitle(ctx, ev.Title.Title)
				if err != nil || lookup == nil {
					e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("anime title lookup failed, skipping Phase 3 season check")
					continue
				}
				tvdbID = lookup.TVDBID
			}
			if tvdbID == 0 {
				e.log.Warn().Str("title", ev.Title.Title).Msg("no TVDB ID, skipping Phase 3 season check")
				continue
			}

			existing, err := e.sonarr.Exists(ctx, tvdbID)
			if err != nil {
				e.log.Warn().Err(err).Str("title", ev.Title.Title).Msg("sonarr check for phase 3")
				continue
			}

			var missingSeasons []int
			if existing != nil {
				series, err := e.sonarr.GetSeries(ctx, existing.ID)
				if err != nil {
					e.log.Warn().Err(err).Msg("fetching sonarr series for phase 3")
					continue
				}
				for _, s := range series.Seasons {
					if s.SeasonNumber == 0 || s.SeasonNumber >= season {
						continue
					}
					if s.Statistics != nil && s.Statistics.EpisodeFileCount > 0 {
						continue
					}
					if s.Statistics != nil && s.Statistics.TotalEpisodeCount == 0 {
						continue
					}
					missingSeasons = append(missingSeasons, s.SeasonNumber)
				}
			} else {
				for s := 1; s < season; s++ {
					missingSeasons = append(missingSeasons, s)
				}
			}

			for _, ms := range missingSeasons {
				candidates = append(candidates, Phase3Candidate{
					Title:     ev.Title.Title,
					Year:      ev.Title.Year,
					MediaType: ev.Title.MediaType,
					TvdbID:    tvdbID,
					Season:    ms,
				})
			}
		}
	}

	return candidates
}

func (e *Executor) SearchAllCandidates(ctx context.Context, candidates []Phase3Candidate) {
	e.log.Info().Msgf("Searching %d Phase 3 candidates...", len(candidates))
	for i, c := range candidates {
		e.log.Info().Str("title", c.Title).Msgf("[%d/%d] searching phase 3", i+1, len(candidates))

		title := &model.Title{
			Title:     c.Title,
			Year:      c.Year,
			MediaType: c.MediaType,
			TmdbID:    c.TmdbID,
			TvdbID:    c.TvdbID,
		}
		stripped := quality.StripSeason(c.Title)
		season := c.Season
		if season == 0 {
			season = quality.ParseSeasonNumber(c.Title)
		}

		releases, err := e.searchRelease(ctx, title, stripped, season)
		if err != nil {
			e.log.Warn().Err(err).Str("title", c.Title).Msg("error searching phase 3 candidate")
			entry := Phase3SearchEntry{Candidate: c}
			e.Phase3SearchPhase = append(e.Phase3SearchPhase, entry)
			continue
		}

		cat := search.CatMovie
		switch c.MediaType {
		case model.MediaTypeTV:
			cat = search.CatTV
		case model.MediaTypeAnime:
			cat = search.CatAnime
		}
		prefs := buildQualityPrefs(e.cfg, c.MediaType, e.prowl.PreferredIndexerID(cat))
		top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)

		entry := Phase3SearchEntry{Candidate: c, Top: top}
		e.Phase3SearchPhase = append(e.Phase3SearchPhase, entry)

		if len(top) > 0 {
			e.log.Info().Int("count", len(top)).Str("title", c.Title).Msg("phase 3 results found")
		} else {
			e.log.Info().Str("title", c.Title).Msg("no phase 3 results")
		}
	}
}

func (e *Executor) ProcessPhase3Pickers(ctx context.Context) {
	if len(e.Phase3SearchPhase) == 0 && len(e.phase3Movies) == 0 && len(e.phase3Seasons) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Phase 3: Collection movies & earlier seasons ──")

	profileID := 1
	if e.radarr != nil {
		id, err := resolveRadarrProfileID(ctx, e.radarr, e.cfg.Library.Radarr.QualityProfile)
		if err != nil {
			e.log.Warn().Err(err).Msg("failed to resolve Radarr quality profile, using ID 1")
		} else {
			profileID = id
		}
	}

	for _, entry := range e.Phase3SearchPhase {
		c := entry.Candidate
		mode := e.cfg.MediaTypeMode(c.MediaType)

		if c.TvdbID > 0 {
			if _, rejected := e.skipRejectedTvdbIDs[c.TvdbID]; rejected {
				existing, err := e.sonarr.Exists(ctx, c.TvdbID)
				if err != nil {
					e.log.Warn().Err(err).Str("title", c.Title).Msg("checking Sonarr for rejected item, skipping")
					continue
				}
				if existing == nil {
					e.log.Info().Str("title", c.Title).Int("tvdb", c.TvdbID).Msg("skipping phase 3: user rejected library add")
					continue
				}
			}
		}

		if c.MediaType == model.MediaTypeMovie && c.TmdbID > 0 {
			if e.phase3BatchProcessedMovies[c.TmdbID] {
				continue
			}
		} else if c.Season > 0 && c.TvdbID > 0 {
			if e.phase3BatchProcessedSeasons[c.TvdbID] != nil && e.phase3BatchProcessedSeasons[c.TvdbID][c.Season] {
				continue
			}
		}

		if mode == config.ProcessModeArr || mode == config.ProcessModeAuto || mode == config.ProcessModeYolo {
			if c.MediaType == model.MediaTypeMovie && c.TmdbID > 0 {
				existing, err := e.radarr.Exists(ctx, c.TmdbID)
				if err == nil && existing == nil {
					if _, addErr := e.radarr.Add(ctx, c.TmdbID, c.Title, c.Year, library.AddMovieOptions{
						Monitored:           e.cfg.Library.Radarr.Monitor,
						MinimumAvailability: "released",
						QualityProfileID:    profileID,
						RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
						SearchNow:           true,
					}); addErr != nil {
						e.log.Warn().Err(addErr).Str("title", c.Title).Msg("adding collection movie to Radarr")
					} else {
						e.log.Info().Str("title", c.Title).Msg("added collection movie to Radarr [will trigger search]")
					}
				}
			} else if c.Season > 0 && c.TvdbID > 0 {
				existing, err := e.sonarr.Exists(ctx, c.TvdbID)
				if err == nil && existing != nil {
					if err := e.sonarr.TriggerSeasonSearch(ctx, existing.ID, c.Season); err != nil {
						e.log.Warn().Err(err).Str("title", c.Title).Int("season", c.Season).Msg("triggering season search")
					} else {
						e.log.Info().Str("title", c.Title).Int("season", c.Season).Msg("triggered Sonarr season search")
					}
				}
			}
			continue
		}

		if len(entry.Top) == 0 {
			continue
		}

		sr := &SearchResult{
			Event: db.EventWithTitle{
				Title: &model.Title{
					Title:     c.Title,
					Year:      c.Year,
					MediaType: c.MediaType,
					TmdbID:    c.TmdbID,
					TvdbID:    c.TvdbID,
				},
			},
			Season: c.Season,
			Top:    entry.Top,
		}
		if c.Season > 0 {
			sr.Season = c.Season
		}

		chosen, err := e.presentPicker(ctx, sr)
		if err != nil || len(chosen) == 0 {
			continue
		}

		if c.MediaType == model.MediaTypeMovie && c.TmdbID > 0 {
			existing, err := e.radarr.Exists(ctx, c.TmdbID)
			if err == nil && existing == nil {
				if _, addErr := e.radarr.Add(ctx, c.TmdbID, c.Title, c.Year, library.AddMovieOptions{
					Monitored:           e.cfg.Library.Radarr.Monitor,
					MinimumAvailability: "released",
					QualityProfileID:    profileID,
					RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
					SearchNow:           false,
				}); addErr != nil {
					e.log.Warn().Err(addErr).Str("title", c.Title).Msg("adding collection movie to Radarr")
				}
			}
		}

		category := e.cfg.Downloader.Categories.Movies
		if c.MediaType == model.MediaTypeTV || c.MediaType == model.MediaTypeAnime {
			category = e.cfg.Downloader.Categories.TV
		}

		for _, release := range chosen {
			uri := release.DownloadURL
			if uri == "" {
				uri = release.MagnetURL
			}
			if uri != "" {
				if _, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category)); err != nil {
					e.log.Warn().Err(err).Msg("phase 3 add failed")
				}
			}
		}
		e.log.Info().Str("title", c.Title).Msg("phase 3 item downloaded")
	}

	if len(e.phase3Movies) > 0 {
		mode := e.cfg.MediaTypeMode(model.MediaTypeMovie)

		if mode == config.ProcessModeArr || mode == config.ProcessModeAuto || mode == config.ProcessModeYolo {
			fmt.Fprintln(os.Stderr, "\n── Adding collection movies to Radarr ──")
			for _, p3m := range e.phase3Movies {
				existing, err := e.radarr.Exists(ctx, p3m.TMDBID)
				if err == nil && existing == nil {
					if _, addErr := e.radarr.Add(ctx, p3m.TMDBID, p3m.Title, p3m.Year, library.AddMovieOptions{
						Monitored:           e.cfg.Library.Radarr.Monitor,
						MinimumAvailability: "released",
						QualityProfileID:    p3m.ProfileID,
						RootFolderPath:      e.cfg.Library.Radarr.RootFolder,
						SearchNow:           true,
					}); addErr != nil {
						e.log.Warn().Err(addErr).Str("title", p3m.Title).Msg("adding collection movie to Radarr")
					} else {
						e.log.Info().Str("title", p3m.Title).Msg("added collection movie to Radarr [will trigger search]")
					}
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "\n── Additional collection movies (from library adds) ──")
			for _, p3m := range e.phase3Movies {
				if e.hasPhase3Movie(p3m.TMDBID) || e.phase3BatchProcessedMovies[p3m.TMDBID] {
					continue
				}

				title := p3m.Title
				stripped := quality.StripSeason(title)
				season := quality.ParseSeasonNumber(title)

				releases, err := e.searchRelease(ctx, &model.Title{
					Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie,
				}, stripped, season)
				if err != nil {
					e.log.Warn().Err(err).Str("title", title).Msg("error searching collection movie")
					continue
				}

				prefs := buildQualityPrefs(e.cfg, model.MediaTypeMovie, e.prowl.PreferredIndexerID(search.CatMovie))
				top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
				if len(top) == 0 {
					continue
				}

				sr := &SearchResult{
					Event: db.EventWithTitle{Title: &model.Title{
						Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie, TmdbID: p3m.TMDBID,
					}},
					Season: season, Top: top,
				}

				chosen, err := e.presentPicker(ctx, sr)
				if err != nil || len(chosen) == 0 {
					continue
				}
				synthEvent := db.EventWithTitle{Title: &model.Title{
					Title: title, Year: p3m.Year, MediaType: model.MediaTypeMovie, TmdbID: p3m.TMDBID,
				}}
				e.addToClient(ctx, synthEvent, chosen)
			}
		}
		e.phase3Movies = nil
	}

	if len(e.phase3Seasons) > 0 {
		seasonMode := e.cfg.MediaTypeMode(e.phase3Seasons[0].MediaType)

		if seasonMode == config.ProcessModeArr || seasonMode == config.ProcessModeAuto || seasonMode == config.ProcessModeYolo {
			fmt.Fprintln(os.Stderr, "\n── Triggering Sonarr season searches ──")
			for _, p3s := range e.phase3Seasons {
				existing, err := e.sonarr.Exists(ctx, p3s.SeriesID)
				if err == nil && existing != nil {
					if err := e.sonarr.TriggerSeasonSearch(ctx, existing.ID, p3s.SeasonNumber); err != nil {
						e.log.Warn().Err(err).Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("triggering season search")
					} else {
						e.log.Info().Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("triggered Sonarr season search")
					}
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "\n── Additional earlier seasons (from library adds) ──")
			for _, p3s := range e.phase3Seasons {
				if e.hasPhase3Season(p3s.SeriesTitle, p3s.SeasonNumber) || e.phase3BatchProcessedSeasons[p3s.TvdbID][p3s.SeasonNumber] {
					continue
				}

				stripped := quality.StripSeason(p3s.SeriesTitle)
				releases, err := e.searchRelease(ctx, &model.Title{
					Title: p3s.SeriesTitle, MediaType: p3s.MediaType,
				}, stripped, p3s.SeasonNumber)
				if err != nil {
					e.log.Warn().Err(err).Str("title", p3s.SeriesTitle).Int("season", p3s.SeasonNumber).Msg("error searching season")
					continue
				}

				cat := search.CatMovie
				switch p3s.MediaType {
				case model.MediaTypeTV:
					cat = search.CatTV
				case model.MediaTypeAnime:
					cat = search.CatAnime
				}
				prefs := buildQualityPrefs(e.cfg, p3s.MediaType, e.prowl.PreferredIndexerID(cat))
				top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
				if len(top) == 0 {
					continue
				}

				sr := &SearchResult{
					Event:  db.EventWithTitle{Title: &model.Title{Title: p3s.SeriesTitle, MediaType: p3s.MediaType}},
					Season: p3s.SeasonNumber, Top: top,
				}

				chosen, err := e.presentPicker(ctx, sr)
				if err != nil || len(chosen) == 0 {
					continue
				}
				synthEvent := db.EventWithTitle{Title: &model.Title{
					Title: p3s.SeriesTitle, MediaType: p3s.MediaType,
				}}
				e.addToClient(ctx, synthEvent, chosen)
			}
		}
		e.phase3Seasons = nil
	}

	e.Phase3SearchPhase = nil
}

func phase3MoviesToCandidates(movies []Phase3Movie) []Phase3Candidate {
	candidates := make([]Phase3Candidate, len(movies))
	for i, m := range movies {
		candidates[i] = Phase3Candidate{
			Title:     m.Title,
			Year:      m.Year,
			MediaType: model.MediaTypeMovie,
			TmdbID:    m.TMDBID,
		}
	}
	return candidates
}

func (e *Executor) hasPhase3Movie(tmdbID int) bool {
	for i := range e.Phase3SearchPhase {
		if e.Phase3SearchPhase[i].Candidate.TmdbID == tmdbID {
			return true
		}
	}
	return false
}

func (e *Executor) hasPhase3Season(title string, season int) bool {
	for i := range e.Phase3SearchPhase {
		c := &e.Phase3SearchPhase[i].Candidate
		if c.Title == title && c.Season == season {
			return true
		}
	}
	return false
}

func (e *Executor) ProcessLibraryDecisions(ctx context.Context, picked []PickedItem) {
	if len(picked) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Library decisions ──")

	fmt.Fprintf(os.Stderr, "  Loading Radarr library...")
	if movies, err := e.radarr.GetAllMovies(ctx); err != nil {
		fmt.Fprintf(os.Stderr, " error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, " %d movies loaded\n", len(movies))
	}

	fmt.Fprintf(os.Stderr, "  Loading Sonarr library...")
	if series, err := e.sonarr.GetAllSeries(ctx); err != nil {
		fmt.Fprintf(os.Stderr, " error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, " %d series loaded\n", len(series))
	}

	type addAction struct {
		evt       db.EventWithTitle
		season    int
		isTV      bool
		tmdbID    int
		tvdbID    int
		searchNow bool
	}
	var addActions []addAction

	type retryAction int
	const (
		retryActionSkip retryAction = iota
		retryActionRetry
		retryActionQuit
	)

	promptRetry := func(label string, err error) retryAction {
		fmt.Fprintf(os.Stderr, "  %s error: %v\n", label, err)
		for {
			fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip this item  [q] quit pipeline\n")
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
					return retryActionRetry
				case "s", "skip":
					return retryActionSkip
				case "q", "quit":
					return retryActionQuit
				}
			case <-ctx.Done():
				return retryActionQuit
			}
		}
	}

processPicked:
	for _, item := range picked {
		mode := e.cfg.MediaTypeMode(item.Event.Title.MediaType)
		searchNow := mode == config.ProcessModeArr || mode == config.ProcessModeAuto || mode == config.ProcessModeYolo
		autoConfirm := mode == config.ProcessModeAuto || mode == config.ProcessModeYolo
		if item.Event.Title.MediaType == model.MediaTypeMovie {
			tmdbID := item.Event.Title.TmdbID
			if tmdbID == 0 {
				e.log.Warn().Str("title", item.Event.Title.Title).Msg("no TMDB ID, trying title lookup in Radarr")
				titleLookup, lookupErr := e.radarr.LookupByTitle(ctx, item.Event.Title.Title)
				if lookupErr != nil || titleLookup == nil {
					e.log.Warn().Err(lookupErr).Str("title", item.Event.Title.Title).Msg("Radarr title lookup failed, skipping library decisions")
					goto nextPicked
				}
				tmdbID = titleLookup.TMDBID
				e.log.Info().Str("title", item.Event.Title.Title).Int("tmdb_id", tmdbID).Msg("found TMDB ID via Radarr title lookup")
			}
			for {
				existing, err := e.radarr.Exists(ctx, tmdbID)
				if err != nil {
					switch promptRetry("Radarr", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if existing != nil {
					e.log.Info().Str("title", item.Event.Title.Title).Msg("already in Radarr")
					if e.cfg.CheckCollections && existing.Collection != nil && existing.Collection.TMDBID > 0 {
						profileID, err := resolveRadarrProfileID(ctx, e.radarr, e.cfg.Library.Radarr.QualityProfile)
						if err != nil {
							e.log.Warn().Err(err).Msg("failed to resolve Radarr quality profile, skipping collection gap check")
							goto nextPicked
						}
						phase3FromCollection := e.checkCollectionGaps(ctx, existing.TMDBID, existing.Collection.TMDBID, profileID, searchNow)
						e.phase3Movies = append(e.phase3Movies, phase3FromCollection...)
						if !searchNow && len(phase3FromCollection) > 0 {
							e.log.Info().Int("count", len(phase3FromCollection)).Msg("pre-searching newly added collection movies")
							e.SearchAllCandidates(ctx, phase3MoviesToCandidates(phase3FromCollection))
						}
					}
					goto nextPicked
				}
				break
			}
			var lookup *library.RadarrMovie
			for {
				var err error
				lookup, err = e.radarr.Lookup(ctx, tmdbID)
				if err != nil {
					switch promptRetry("Radarr lookup", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if lookup == nil {
					e.log.Warn().Str("title", item.Event.Title.Title).Int("tmdb", tmdbID).Msg("movie not found on TMDB")
					goto nextPicked
				}
				break
			}
			e.log.Info().Msgf("Add to Radarr? %s (%d)", lookup.Title, lookup.Year)
			if lookup.Overview != "" {
				for _, line := range formatOverview(lookup.Overview, 72) {
					e.log.Info().Msg(line)
				}
			}
			e.log.Info().Str("profile", e.cfg.Library.Radarr.QualityProfile).Str("root", e.cfg.Library.Radarr.RootFolder).Msg("Radarr config")
			if autoConfirm || PromptYesNo(ctx, "  Add to Radarr?") {
				addActions = append(addActions, addAction{
					evt:       item.Event,
					isTV:      false,
					tmdbID:    tmdbID,
					searchNow: searchNow,
				})
			}
		} else {
			tvdbID := item.Event.Title.TvdbID
			if tvdbID == 0 {
				if item.Event.Title.MediaType == model.MediaTypeAnime {
					searchTitle := sanitizeSearchQuery(quality.StripSeason(item.Event.Title.Title))
					titleLookup, err := e.sonarr.LookupByTitle(ctx, searchTitle)
					if err != nil {
						e.log.Warn().Err(err).Str("title", item.Event.Title.Title).Msg("anime title lookup in Sonarr failed")
						goto nextPicked
					}
					if titleLookup == nil {
						e.log.Warn().Str("title", item.Event.Title.Title).Msg("anime not found in Sonarr by title lookup")
						goto nextPicked
					}
					tvdbID = titleLookup.TVDBID
				} else {
					e.log.Warn().Str("title", item.Event.Title.Title).Msg("no TVDB ID, skipping Sonarr library decisions")
					goto nextPicked
				}
			}
			for {
				existing, err := e.sonarr.Exists(ctx, tvdbID)
				if err != nil {
					switch promptRetry("Sonarr", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if existing != nil {
					e.log.Info().Str("title", existing.Title).Int("season", item.Season).Msg("already in Sonarr")
					if item.Season > 1 {
						missing := e.checkExistingSonarrSeasons(ctx, existing, item.Season)
						for _, missingS := range missing {
							if e.phase3BatchProcessedSeasons[tvdbID][missingS.SeasonNumber] {
								e.log.Info().Str("series", existing.Title).Int("season", missingS.SeasonNumber).Msg("already processed via batch picker, skipping")
								continue
							}
							enqueue := false
							if searchNow {
								enqueue = true
								e.log.Info().Str("series", existing.Title).Int("season", missingS.SeasonNumber).Msg("auto-queueing earlier season search")
							} else {
								enqueue = PromptYesNo(ctx, fmt.Sprintf("    %s: Search for Season %d?", existing.Title, missingS.SeasonNumber))
							}
							if enqueue {
								e.phase3Seasons = append(e.phase3Seasons, struct {
									SeriesID     int
									SeasonNumber int
									SeriesTitle  string
									MediaType    model.MediaType
									TvdbID       int
								}{
									SeriesID:     existing.ID,
									SeasonNumber: missingS.SeasonNumber,
									SeriesTitle:  existing.Title,
									MediaType:    item.Event.Title.MediaType,
									TvdbID:       tvdbID,
								})
							}
						}
					}
					goto nextPicked
				}
				break
			}
			var lookup *library.SonarrSeries
			for {
				var err error
				lookup, err = e.sonarr.Lookup(ctx, tvdbID)
				if err != nil {
					switch promptRetry("Sonarr lookup", err) {
					case retryActionRetry:
						continue
					case retryActionSkip:
						goto nextPicked
					case retryActionQuit:
						break processPicked
					}
				}
				if lookup == nil {
					e.log.Warn().Str("title", item.Event.Title.Title).Int("tvdb", tvdbID).Msg("series not found on TVDB")
					goto nextPicked
				}
				break
			}
			e.log.Info().Msgf("Add to Sonarr? %s (%d)", lookup.Title, lookup.Year)
			if lookup.Overview != "" {
				for _, line := range formatOverview(lookup.Overview, 72) {
					e.log.Info().Msg(line)
				}
			}
			e.log.Info().Str("profile", e.cfg.Library.Sonarr.QualityProfile).Str("root", e.cfg.Library.Sonarr.RootFolder).Msg("Sonarr config")
			if autoConfirm || PromptYesNo(ctx, "  Add to Sonarr?") {
				addActions = append(addActions, addAction{
					evt:       item.Event,
					season:    item.Season,
					isTV:      true,
					tvdbID:    tvdbID,
					searchNow: searchNow,
				})
			}
		}
	nextPicked:
	}

	if len(addActions) > 0 {
		fmt.Fprintln(os.Stderr, "\n── Executing library adds ──")

		for _, a := range addActions {
			var title string
			if a.isTV {
				title = a.evt.Title.Title
			} else {
				title = a.evt.Title.Title
			}

			var addErr error
		actionRetry:
			if a.isTV {
				addErr = e.addToSonarr(ctx, a.evt, a.tvdbID, a.season, true, a.searchNow, false)
			} else {
				addErr = e.addToRadarr(ctx, a.evt, true, a.searchNow, e.cfg.Library.Radarr.Monitor)
			}
			if addErr != nil {
				fmt.Fprintf(os.Stderr, "  Error adding %q to library: %v\n", title, addErr)
				fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip  [q] quit pipeline\n")
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
						goto actionRetry
					case "s", "skip":
						e.log.Warn().Msgf("skipped adding %q to library", title)
					case "q", "quit":
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (e *Executor) searchPhase3Season(ctx context.Context, s struct {
	SeriesID     int
	SeasonNumber int
	SeriesTitle  string
	MediaType    model.MediaType
}) {
	stripped := quality.StripSeason(s.SeriesTitle)

	releases, err := e.searchRelease(ctx, &model.Title{
		Title:     s.SeriesTitle,
		Year:      0,
		MediaType: s.MediaType,
	}, stripped, s.SeasonNumber)
	if err != nil {
		e.log.Warn().Err(err).Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("error searching earlier season")
		return
	}

	cat := search.CatMovie
	switch s.MediaType {
	case model.MediaTypeTV:
		cat = search.CatTV
	case model.MediaTypeAnime:
		cat = search.CatAnime
	}
	prefs := buildQualityPrefs(e.cfg, s.MediaType, e.prowl.PreferredIndexerID(cat))
	top := quality.SortAndTop(releases, prefs, e.cfg.ShowTopN)
	if len(top) == 0 {
		e.log.Info().Str("title", s.SeriesTitle).Int("season", s.SeasonNumber).Msg("no results for earlier season")
		return
	}

	sr := &SearchResult{
		Event:  db.EventWithTitle{Title: &model.Title{Title: s.SeriesTitle, MediaType: s.MediaType}},
		Season: s.SeasonNumber,
		Top:    top,
	}

	chosen, err := e.presentPicker(ctx, sr)
	if err != nil || len(chosen) == 0 {
		return
	}

	synthEvent := db.EventWithTitle{
		Title: &model.Title{
			Title:     s.SeriesTitle,
			MediaType: s.MediaType,
		},
	}
	e.addToClient(ctx, synthEvent, chosen)
}
