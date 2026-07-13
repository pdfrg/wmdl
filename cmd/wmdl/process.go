package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/notifier"
	"github.com/pdfrg/wmdl/internal/process"
	"github.com/pdfrg/wmdl/internal/quality"
)

func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "Search and download approved releases",
		Long: `For each approved release: search Prowlarr, select a release in the TUI picker,
and send it to the download client.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmdl.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			refreshCache, _ := cmd.Flags().GetBool("refresh-cache")
			if refreshCache {
				if err := database.PurgeLibraryCache(cmd.Context()); err != nil {
					return fmt.Errorf("purging library cache: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  Library cache purged, will re-fetch from *arr services\n")
			}

			var typeFilter model.MediaType
			typeFilterStr, _ := cmd.Flags().GetString("type")
			if typeFilterStr != "" {
				switch model.MediaType(typeFilterStr) {
				case model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeMovie, model.MediaTypeTV, model.MediaTypeBook:
					typeFilter = model.MediaType(typeFilterStr)
				default:
					return fmt.Errorf("invalid type %q: must be anime, movie, tv, music, or book", typeFilterStr)
				}
			}

			ctx := cmd.Context()

			backlog, _ := cmd.Flags().GetBool("backlog")
			if backlog {
				return runBacklogBatch(ctx, database, cfg, typeFilter, 0, 0)
			}

			var targetYear, targetWeek int

			if cmd.Flags().Changed("week") {
				targetYear, targetWeek, err = resolveWeek(cmd)
				if err != nil {
					return err
				}
			} else {
				curYear, curWeek, err := resolveWeek(cmd)
				if err != nil {
					return err
				}

				state, err := database.GetWeekState(ctx, curYear, curWeek)
				if err != nil {
					return fmt.Errorf("checking week state: %w", err)
				}

				switch {
				case state != nil && state.Discovered && state.Reviewed:
					targetYear, targetWeek = curYear, curWeek

				case state != nil && state.Discovered && !state.Reviewed:
					return fmt.Errorf("week %d-W%02d has not been reviewed yet — run 'wmdl review' first", curYear, curWeek)

				default:
					states, err := database.GetWeekStates(ctx, 12)
					if err != nil {
						return fmt.Errorf("loading week states: %w", err)
					}
					found := false
					for _, s := range states {
						if s.Reviewed && !s.Processed {
							log.Info().Msgf("Week %d-W%02d not discovered yet, processing %d-W%02d instead", curYear, curWeek, s.Year, s.Week)
							targetYear, targetWeek = s.Year, s.Week
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("week %d-W%02d not discovered yet and no prior weeks ready to process", curYear, curWeek)
					}
				}
			}

			if err := runProcessForWeek(ctx, database, cfg, targetYear, targetWeek, typeFilter, false); err != nil {
				return err
			}

			if cfg.IncludeBacklog && !cmd.Flags().Changed("week") {
				return runBacklogBatch(ctx, database, cfg, typeFilter, targetYear, targetWeek)
			}

			return nil
		},
	}
	addWeekFlag(cmd)
	cmd.Flags().Bool("backlog", false, "Process remaining items from all processed weeks")
	cmd.Flags().String("type", "", "Media type to process (anime, movie, tv, music, book)")
	cmd.Flags().Bool("refresh-cache", false, "Force re-fetch of library cache from *arr services")
	return cmd
}

func promptYesNo(ctx context.Context, prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		ch <- scanner.Text()
	}()
	select {
	case ans := <-ch:
		return strings.ToLower(strings.TrimSpace(ans)) == "y" || strings.ToLower(strings.TrimSpace(ans)) == "yes"
	case <-ctx.Done():
		return false
	}
}

func runBacklogBatch(ctx context.Context, database *db.DB, cfg *config.Config, typeFilter model.MediaType, skipYear, skipWeek int) error {
	states, err := database.GetWeekStates(ctx, 0)
	if err != nil {
		return fmt.Errorf("loading week states: %w", err)
	}

	var backlogWeeks []*model.WeekState
	for _, s := range states {
		if !s.Processed {
			continue
		}
		if s.Year == skipYear && s.Week == skipWeek {
			continue
		}
		dl, app, err := database.GetWeekProcessCounts(ctx, s.Year, s.Week)
		if err != nil {
			log.Warn().Err(err).Msgf("getting process counts for %d-W%02d", s.Year, s.Week)
			continue
		}
		if app > 0 && dl < app {
			s.DownloadedCount = dl
			s.ApprovedCount = app
			backlogWeeks = append(backlogWeeks, s)
		}
	}

	if len(backlogWeeks) == 0 {
		log.Info().Msg("No backlog weeks to process.")
		return nil
	}

	totalItems := 0
	for _, s := range backlogWeeks {
		n := s.ApprovedCount - s.DownloadedCount
		totalItems += n
		log.Info().Msgf("backlog: %d-W%02d (%d items remaining)", s.Year, s.Week, n)
	}
	log.Info().Msgf("backlog: processing %d week(s) with %d total item(s)", len(backlogWeeks), totalItems)

	// ─── Collect events from all backlog weeks ─────────────────
	type backlogWeekData struct {
		state       *model.WeekState
		events      []db.EventWithTitle
		albumEvents []db.EventWithAlbum
		bookEvents  []db.EventWithBook
	}

	var weeks []backlogWeekData
	var allEvents []db.EventWithTitle
	var allAlbumEvents []db.EventWithAlbum
	var allBookEvents []db.EventWithBook
	var allAnimeAiring []db.EventWithTitle

	for _, s := range backlogWeeks {
		autoApproveYoloItems(ctx, database, cfg, s.Year, s.Week)

		events, _ := database.ListEventsByWeekWithTitles(ctx, s.Year, s.Week)
		albumEvents, _ := database.ListAlbumEventsByWeekAndStatus(ctx, s.Year, s.Week, model.StatusApproved)
		bookEvents, _ := database.ListBookEventsByWeekAndStatus(ctx, s.Year, s.Week, model.StatusApproved)

		var pending []db.EventWithTitle
		for _, ev := range events {
			if typeFilter != "" && ev.Title.MediaType != typeFilter {
				continue
			}
			if ev.Event.Status == model.StatusApproved {
				pending = append(pending, ev)
			}
		}

		if typeFilter == model.MediaTypeMusic {
			pending = nil
			bookEvents = nil
		} else if typeFilter == model.MediaTypeBook {
			pending = nil
			albumEvents = nil
		} else if typeFilter != "" {
			albumEvents = nil
			bookEvents = nil
		}

		var searchable []db.EventWithTitle
		var animeAiring []db.EventWithTitle
		for _, ev := range pending {
			if ev.Title.MediaType == model.MediaTypeAnime && ev.Event.Source == "jikan-airing" {
				animeAiring = append(animeAiring, ev)
			} else {
				searchable = append(searchable, ev)
			}
		}

		if len(searchable) == 0 && len(animeAiring) == 0 && len(albumEvents) == 0 && len(bookEvents) == 0 {
			s.Processed = true
			if err := database.UpsertWeekState(ctx, s); err != nil {
				log.Warn().Err(err).Msg("tracking week state")
			}
			continue
		}

		weeks = append(weeks, backlogWeekData{
			state: s, events: searchable, albumEvents: albumEvents, bookEvents: bookEvents,
		})
		allEvents = append(allEvents, searchable...)
		allAlbumEvents = append(allAlbumEvents, albumEvents...)
		allBookEvents = append(allBookEvents, bookEvents...)
		allAnimeAiring = append(allAnimeAiring, animeAiring...)
	}

	if len(weeks) == 0 {
		log.Info().Msg("No backlog items to process.")
		return nil
	}

	exec := process.NewExecutor(log.Logger, cfg, database)
	modes := cfg.UsedModes()
	needsProwlarr := modes["full"] || modes["prowlarr-grab"]
	needsDownloader := modes["full"]
	needsLibrary := modes["full"] || modes["arr"] || modes["auto"] || modes["yolo"]
	hasSearchable := len(allEvents) > 0
	hasAlbums := len(allAlbumEvents) > 0
	hasBooks := len(allBookEvents) > 0
	hasAnimeAiring := len(allAnimeAiring) > 0

	if !needsLibrary {
		allAnimeAiring = nil
		hasAnimeAiring = false
	}

	if !hasSearchable && !hasAnimeAiring && !hasAlbums && !hasBooks {
		log.Info().Msg("No processable items across backlog weeks.")
		return nil
	}

	// ─── Health check (single pass, shared across all weeks) ──
	health := exec.HealthCheck(ctx, needsProwlarr, needsDownloader, needsLibrary)
	skipLibrary := !needsLibrary

	if len(health.Critical) > 0 || len(health.Warnings) > 0 {
		fmt.Fprintln(os.Stderr, "── Service health check ──")
		for _, c := range health.Critical {
			fmt.Fprintf(os.Stderr, "  ✗ %s\n", c)
		}
		for _, w := range health.Warnings {
			fmt.Fprintf(os.Stderr, "  ! %s\n", w)
		}
	}

	if len(health.Critical) > 0 {
		if modes["yolo"] {
			log.Error().Msg("yolo mode: critical services unreachable, aborting")
			return fmt.Errorf("critical services unreachable")
		}
		fmt.Fprintln(os.Stderr, "  Critical services unreachable. Cannot proceed without them.")
		for {
			fmt.Fprintf(os.Stderr, "  [r] retry  [q] quit\n")
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
					health = exec.HealthCheck(ctx, needsProwlarr, needsDownloader, needsLibrary)
					if len(health.Critical) == 0 && len(health.Warnings) == 0 {
						fmt.Fprintln(os.Stderr, "  All services OK")
						break
					}
					if len(health.Critical) > 0 {
						continue
					}
				case "q", "quit":
					return nil
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
	}

	if len(health.Warnings) > 0 && len(health.Critical) == 0 {
		if modes["yolo"] {
			log.Error().Msg("yolo mode: library services unreachable, aborting")
			return fmt.Errorf("library services unreachable")
		}
		for {
			fmt.Fprintf(os.Stderr, "  [r] retry  [p] proceed without library management  [q] quit\n")
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
					health = exec.HealthCheck(ctx, needsProwlarr, needsDownloader, needsLibrary)
					if len(health.Critical) > 0 {
						fmt.Fprintln(os.Stderr, "  Critical services also unreachable now.")
						return nil
					}
					if len(health.Warnings) > 0 {
						continue
					}
					fmt.Fprintln(os.Stderr, "  All services OK")
				case "p", "proceed":
					skipLibrary = true
				case "q", "quit":
					return nil
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
	}

	// ─── Pre-warm caches (shared across all weeks) ────────────
	warmCtx, warmCancel := context.WithCancel(ctx)
	defer warmCancel()

	preWarmDone := make(chan struct{}, 4)
	if !skipLibrary {
		go func() { exec.PreWarmRadarr(warmCtx); preWarmDone <- struct{}{} }()
		go func() { exec.PreWarmSonarr(warmCtx); preWarmDone <- struct{}{} }()
		if hasAlbums {
			go func() { exec.PreWarmLidarr(warmCtx); preWarmDone <- struct{}{} }()
		} else {
			preWarmDone <- struct{}{}
		}
		if hasBooks && exec.BookClientAvailable() {
			go func() { exec.PreWarmBookClient(warmCtx); preWarmDone <- struct{}{} }()
		} else {
			preWarmDone <- struct{}{}
		}
		fmt.Fprintf(os.Stderr, "  Pre-warming library data in background...\n")
	} else {
		preWarmDone <- struct{}{}
		preWarmDone <- struct{}{}
		preWarmDone <- struct{}{}
		preWarmDone <- struct{}{}
	}

	// ─── Batch search phase (all weeks at once) ───────────────
	fmt.Fprintf(os.Stderr, "\n── Batch search phase (all weeks) ──\n")

	var primaryVideoResults []*process.SearchResult
	var musicResults []*process.MusicSearchResult
	var bookResults []*process.BookSearchResult

	if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
		var phase3Candidates []process.Phase3Candidate
		if needsLibrary && (hasSearchable || hasAnimeAiring) {
			var allForPhase3 []db.EventWithTitle
			for _, ev := range allEvents {
				m := cfg.MediaTypeMode(ev.Title.MediaType)
				if m == "full" || m == "arr" || m == "auto" || m == "yolo" {
					allForPhase3 = append(allForPhase3, ev)
				}
			}
			for _, ev := range allAnimeAiring {
				m := cfg.MediaTypeMode(ev.Title.MediaType)
				if m == "full" || m == "arr" || m == "auto" || m == "yolo" {
					allForPhase3 = append(allForPhase3, ev)
				}
			}
			if len(allForPhase3) > 0 {
				phase3Candidates = exec.ComputePhase3Candidates(ctx, allForPhase3)
				if len(phase3Candidates) > 0 {
					fmt.Fprintf(os.Stderr, "  Pre-computed %d Phase 3 candidates (collection gaps + earlier seasons)\n", len(phase3Candidates))
				}
			}
		}

		var searchEvents []db.EventWithTitle
		for _, ev := range allEvents {
			m := cfg.MediaTypeMode(ev.Title.MediaType)
			if m != "arr" && m != "auto" && m != "yolo" {
				searchEvents = append(searchEvents, ev)
			}
		}
		if len(searchEvents) > 0 {
			log.Info().Msgf("Searching %d movie/TV release(s)...", len(searchEvents))
			primaryVideoResults = exec.SearchAll(ctx, searchEvents)
		}
		if len(phase3Candidates) > 0 {
			exec.SearchAllCandidates(ctx, phase3Candidates)
		}
		if hasAlbums {
			musicResults = exec.SearchMusicAll(ctx, allAlbumEvents)
		}
		if hasBooks {
			bm := cfg.MediaTypeMode(model.MediaTypeBook)
			if bm != "arr" && bm != "auto" && bm != "yolo" {
				bookResults = exec.SearchBooksAll(ctx, allBookEvents)
			}
		}
		if hasBooks && exec.BookClientAvailable() && exec.HCClientAvailable() {
			bm := cfg.MediaTypeMode(model.MediaTypeBook)
			if bm == "full" || bm == "prowlarr-grab" {
				exec.ComputeBookPhase3Candidates(ctx, allBookEvents)
				if len(exec.BookPhase3SearchPhase) > 0 {
					log.Info().Int("count", len(exec.BookPhase3SearchPhase)).Msg("book phase 3 candidates to pick")
				}
			}
		}
		fmt.Fprintf(os.Stderr, "  All searches complete. Starting picker phase...\n")
	} else {
		fmt.Fprintf(os.Stderr, "  Interactive mode: processing items one at a time...\n")
	}

	// ─── Primary pickers ──────────────────────────────────────
	var picked []process.PickedItem
	var batchItems []*process.BatchItem

	if hasSearchable {
		if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
			var fullResults, grabResults []*process.SearchResult
			for _, sr := range primaryVideoResults {
				m := cfg.MediaTypeMode(sr.Event.Title.MediaType)
				if m == "prowlarr-grab" {
					grabResults = append(grabResults, sr)
				} else {
					fullResults = append(fullResults, sr)
				}
			}
			for _, sr := range grabResults {
				if len(sr.Top) == 0 {
					continue
				}
				batchItems = append(batchItems, &process.BatchItem{
					ID:           fmt.Sprintf("v-%d", sr.Event.Event.ID),
					Label:        labelForSearchResult(sr),
					Releases:     sr.Top,
					SearchResult: sr,
				})
			}
			for _, sr := range fullResults {
				if len(sr.Top) == 0 {
					continue
				}
				batchItems = append(batchItems, &process.BatchItem{
					ID:           fmt.Sprintf("v-%d", sr.Event.Event.ID),
					Label:        labelForSearchResult(sr),
					Releases:     sr.Top,
					SearchResult: sr,
				})
			}
		} else {
			for _, ev := range allEvents {
				m := cfg.MediaTypeMode(ev.Title.MediaType)
				if m == "prowlarr-grab" {
					exec.SearchAndGrabOne(ctx, ev)
				} else {
					if item := exec.SearchAndPickOne(ctx, ev); item != nil {
						picked = append(picked, *item)
					}
				}
			}
		}
	}

	for _, ev := range allEvents {
		m := cfg.MediaTypeMode(ev.Title.MediaType)
		if m == "arr" || m == "auto" || m == "yolo" {
			season := quality.ParseSeasonNumber(ev.Title.Title)
			picked = append(picked, process.PickedItem{Event: ev, Season: season})
		}
	}

	// ─── Batch picker (single TUI for all weeks) ──────────────
	var albumResults []process.MusicAlbumResult
	var bookDownloadedInfo map[int64]*process.BookDownloadInfo

	if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
		if hasAlbums {
			for _, sr := range musicResults {
				if len(sr.Top) == 0 {
					continue
				}
				batchItems = append(batchItems, &process.BatchItem{
					ID:          fmt.Sprintf("m-%d", sr.Event.Event.ID),
					Label:       fmt.Sprintf("%s - %s", sr.Event.Artist.Name, sr.Event.Album.Title),
					Releases:    sr.Top,
					MusicResult: sr,
				})
			}
		}

		if hasBooks {
			bm := cfg.MediaTypeMode(model.MediaTypeBook)
			if bm != "arr" && bm != "auto" && bm != "yolo" {
				for _, sr := range bookResults {
					if len(sr.Top) == 0 {
						continue
					}
					parsed := make([]quality.ParsedRelease, len(sr.Top))
					for i, br := range sr.Top {
						parsed[i] = br.ParsedRelease
					}
					batchItems = append(batchItems, &process.BatchItem{
						ID:         fmt.Sprintf("b-%d-%s", sr.Event.Event.ID, sr.Format),
						Label:      fmt.Sprintf("%s by %s [%s]", sr.Event.Book.Title, sr.Event.Author.Name, sr.Format),
						Releases:   parsed,
						BookResult: sr,
					})
				}
			}
		}

		if needsLibrary && (hasSearchable || hasAnimeAiring) && len(exec.Phase3SearchPhase) > 0 {
			var remaining []process.Phase3SearchEntry
			for _, entry := range exec.Phase3SearchPhase {
				c := entry.Candidate
				m := cfg.MediaTypeMode(c.MediaType)
				if m == "arr" || m == "auto" || m == "yolo" {
					remaining = append(remaining, entry)
				} else if len(entry.Top) > 0 {
					label := fmt.Sprintf("%s (%d)", c.Title, c.Year)
					if c.Season > 0 {
						label = fmt.Sprintf("Season %d - %s", c.Season, c.Title)
					}
					batchItems = append(batchItems, &process.BatchItem{
						ID:       fmt.Sprintf("p3-%d-%d", c.TmdbID, c.Season),
						Label:    label,
						Phase3:   true,
						Releases: entry.Top,
						SearchResult: &process.SearchResult{
							Event: db.EventWithTitle{
								Title: &model.Title{
									Title: c.Title, Year: c.Year, MediaType: c.MediaType,
									TmdbID: c.TmdbID, TvdbID: c.TvdbID,
								},
							},
							Season: c.Season,
							Top:    entry.Top,
						},
					})
				}
			}
			exec.Phase3SearchPhase = remaining
		}

		if hasBooks && exec.BookClientAvailable() && exec.HCClientAvailable() {
			bm := cfg.MediaTypeMode(model.MediaTypeBook)
			if bm == "full" || bm == "prowlarr-grab" {
				for _, sr := range exec.BookPhase3SearchPhase {
					if len(sr.Top) == 0 {
						continue
					}
					parsed := make([]quality.ParsedRelease, len(sr.Top))
					for i, br := range sr.Top {
						parsed[i] = br.ParsedRelease
					}
					batchItems = append(batchItems, &process.BatchItem{
						ID:         fmt.Sprintf("bp3-%d-%s", sr.Event.Book.HardcoverID, sr.Format),
						Label:      fmt.Sprintf("%s by %s [%s]", sr.Event.Book.Title, sr.Event.Author.Name, sr.Format),
						Releases:   parsed,
						Phase3:     true,
						BookResult: sr,
					})
				}
			}
		}

		if len(batchItems) > 0 {
			fmt.Fprintf(os.Stderr, "  Processing %d item(s) in picker...\n", len(batchItems))
			picker := process.NewBatchPicker(batchItems)
			result, err := picker.Run()
			if err != nil && !errors.Is(err, process.ErrAbort) {
				return err
			}
			if errors.Is(err, process.ErrAbort) {
				log.Info().Msg("pipeline aborted by user")
			}
			batchPicked, batchAlbumResults, batchBookInfo := exec.ProcessBatchResults(ctx, result.Items)
			picked = append(picked, batchPicked...)
			albumResults = batchAlbumResults
			bookDownloadedInfo = batchBookInfo
		}
	} else {
		if hasAlbums {
			albumResults = exec.ProcessMusicAlbumsInteractive(ctx, allAlbumEvents)
		}
	}

	// ─── Library decisions (single pass) ──────────────────────
	if !skipLibrary {
		if hasSearchable || hasAnimeAiring || hasAlbums || (hasBooks && exec.BookClientAvailable()) {
			fmt.Fprintf(os.Stderr, "  Waiting for library data...\n")
			<-preWarmDone
			<-preWarmDone
			<-preWarmDone
			<-preWarmDone
		}

		if len(picked) > 0 {
			exec.ProcessLibraryDecisions(ctx, picked)
		}
		if len(albumResults) > 0 {
			exec.ProcessMusicAlbumDecisions(ctx, albumResults)
		}
		if hasBooks && exec.BookClientAvailable() {
			bm := cfg.MediaTypeMode(model.MediaTypeBook)
			var be []db.EventWithBook
			if bm == "arr" || bm == "auto" || bm == "yolo" || bm == "full" {
				be = allBookEvents
			} else if len(bookDownloadedInfo) > 0 {
				be = allBookEvents
			}
			exec.ProcessBookLibraryDecisions(ctx, be, bookDownloadedInfo)
		}
	}

	// ─── Phase 3 pickers ──────────────────────────────────────
	if !skipLibrary {
		exec.ProcessPhase3Pickers(ctx)
	}

	// ─── Anime airing items ───────────────────────────────────
	for _, ae := range allAnimeAiring {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		m := cfg.MediaTypeMode(ae.Title.MediaType)
		searchNow := m == "arr" || m == "auto" || m == "yolo"
		series, err := exec.AddAiringAnimeToSonarr(ctx, ae, searchNow)
		if err != nil {
			log.Warn().Err(err).Str("title", ae.Title.Title).Msg("error adding airing anime to Sonarr")
			continue
		}
		if series == nil {
			continue
		}
		if err := database.UpdateReleaseEventStatus(ctx, ae.Event.ID, model.StatusDownloaded); err != nil {
			log.Warn().Err(err).Msg("updating anime event status")
		}
		exec.SearchAiringAnimeEarlierSeasons(ctx, ae, series)
	}

	// ─── Book interactive (non-batch mode) ────────────────────
	if hasBooks && cfg.ProcessMode != "batch" && cfg.ProcessMode != "" {
		bm := cfg.MediaTypeMode(model.MediaTypeBook)
		if bm == "full" {
			interactiveInfo := exec.ProcessBooks(ctx, allBookEvents)
			if exec.BookClientAvailable() {
				var be []db.EventWithBook
				if bm == "arr" || bm == "auto" || bm == "yolo" || bm == "full" {
					be = allBookEvents
				} else if len(interactiveInfo) > 0 {
					be = allBookEvents
				}
				exec.ProcessBookLibraryDecisions(ctx, be, interactiveInfo)
			}
		}
	}

	// ─── Mark each week processed ─────────────────────────────
	for _, wd := range weeks {
		wd.state.Processed = true
		if err := database.UpsertWeekState(ctx, wd.state); err != nil {
			log.Warn().Err(err).Msg("tracking week state")
		}
	}

	// ─── Log unfound/skipped ──────────────────────────────────
	if len(exec.Unfound) > 0 {
		log.Warn().Msgf("No results found for %d item(s):", len(exec.Unfound))
		for _, u := range exec.Unfound {
			log.Info().Str("title", u).Msg("unfound")
		}
	}
	if len(exec.Skipped) > 0 {
		log.Warn().Msgf("Skipped %d item(s):", len(exec.Skipped))
		for _, s := range exec.Skipped {
			log.Info().Str("title", s).Msg("skipped")
		}
	}

	// ─── Consolidated notification ────────────────────────────
	if cfg.Notifier.Service != "" {
		notify, nErr := notifier.New(cfg.Notifier)
		if nErr == nil {
			total := len(allEvents) + len(allAlbumEvents) + len(allBookEvents) + len(allAnimeAiring)
			msg := fmt.Sprintf("%d weeks · %d items", len(weeks), total)
			if len(exec.Unfound) > 0 {
				msg += fmt.Sprintf(" · %d unfound", len(exec.Unfound))
			}
			if len(picked) > 0 || len(albumResults) > 0 || len(bookDownloadedInfo) > 0 {
				msg += fmt.Sprintf(" · %d downloaded", len(picked)+len(albumResults)+len(bookDownloadedInfo))
			}
			if err := notify.Send("wmdl: Backlog Complete", msg, 5); err != nil {
				log.Warn().Err(err).Msg("sending backlog notification")
			}
		}
	}

	return nil
}
