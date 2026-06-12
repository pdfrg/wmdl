package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/notifier"
	"github.com/pdfrg/wmdl/internal/process"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/review"
)

func runDiscoverForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int, headless bool, typeFilter model.MediaType) (discovered bool, err error) {
	ws, err := database.GetWeekState(ctx, year, week)
	if err != nil {
		return false, fmt.Errorf("checking week state: %w", err)
	}

	if ws != nil && ws.Discovered {
		fmt.Fprintf(os.Stderr, "Week %d-W%02d already discovered.\n", year, week)
		if !promptYesNo(ctx, "Continue anyway?") {
			return false, nil
		}
	}

	runner := discover.NewRunner(log.Logger, cfg, database, headless)
	if typeFilter != "" {
		runner.SetMediaTypeFilter(typeFilter)
	}
	if year != 0 && week != 0 {
		runner.SetTargetWeek(year, week)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := runner.Run(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func runReviewForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int) (approved int, err error) {
	var target *model.WeekState

	if year != 0 && week != 0 {
		target, err = database.GetWeekState(ctx, year, week)
		if err != nil {
			return 0, fmt.Errorf("finding week state: %w", err)
		}
		if target == nil {
			return 0, fmt.Errorf("week %d-W%02d not discovered yet", year, week)
		}
	} else {
		target, err = database.GetLatestDiscoveredWeek(ctx)
		if err != nil {
			return 0, fmt.Errorf("finding target week: %w", err)
		}
		if target == nil {
			fmt.Fprintln(os.Stderr, "No weeks discovered yet. Run 'wmdl discover' first.")
			return 0, nil
		}
	}

	year, week = target.Year, target.Week

	// Auto-approve yolo-mode items before review (they skip the TUI entirely)
	autoApproveYoloItems(ctx, database, cfg, year, week)

	events, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		return 0, fmt.Errorf("loading events: %w", err)
	}

	albumEvents, err := database.ListAlbumEventsByWeek(ctx, year, week)
	if err != nil {
		return 0, fmt.Errorf("loading album events: %w", err)
	}

	bookEvents, err := database.ListBookEventsByWeek(ctx, year, week)
	if err != nil {
		return 0, fmt.Errorf("loading book events: %w", err)
	}

	prevAnimeWeek := ""
	prevYear, prevWeek, err := database.GetPreviousAnimeWeek(ctx, year, week)
	if err == nil && prevYear > 0 {
		prevAnimeWeek = fmt.Sprintf("%d-W%02d", prevYear, prevWeek)
	}

	// Count total approved (including yolo auto-approvals) and check for pending
	approved = 0
	hasPending := false
	for _, ev := range events {
		switch ev.Event.Status {
		case model.StatusApproved:
			approved++
		case model.StatusPending:
			hasPending = true
		}
	}
	if !hasPending {
		for _, ev := range albumEvents {
			switch ev.Event.Status {
			case model.StatusApproved:
				approved++
			case model.StatusPending:
				hasPending = true
			}
		}
	}
	if !hasPending {
		for _, ev := range bookEvents {
			switch ev.Event.Status {
			case model.StatusApproved:
				approved++
			case model.StatusPending:
				hasPending = true
			}
		}
	}

	if !hasPending {
		if !target.Reviewed {
			if approved > 0 {
				fmt.Fprintf(os.Stderr, "All %d items auto-approved (yolo mode) for %d-W%02d.\n", approved, year, week)
				target.Reviewed = true
				if err := database.UpsertWeekState(ctx, target); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
				}
				return approved, nil
			}
			fmt.Fprintf(os.Stderr, "No pending releases for %d-W%02d.\n", year, week)
			return 0, nil
		}
		// target.Reviewed is true — all items were decided in a prior
		// session (manually or by yolo). Fall through to re-review menu.
	}

	if target.Reviewed {
		fmt.Fprintf(os.Stderr, "All releases for %d-W%02d have already been reviewed.\n", year, week)
		for {
			fmt.Fprintf(os.Stderr, "[r] review again  [e] export choices  [q] quit\n")
			fmt.Fprintf(os.Stderr, "Choose: ")
			var choice string
			if _, err := fmt.Scanln(&choice); err != nil {
				return 0, nil
			}
			switch choice {
			case "r":
				tui, err := review.NewReviewTUIWithEvents(events, albumEvents, bookEvents, database, cfg.PosterMode, year, week, prevAnimeWeek, model.BookFormat(cfg.MediaTypes.Books.DefaultFormat))
				if err != nil {
					return 0, err
				}
				if err := tui.Run(); err != nil {
					return 0, err
				}
				approved = tui.ApprovedCount()
				_, _, pending := tui.Counts()
				if pending > 0 {
					fmt.Fprintf(os.Stderr, "\n%d items still need decisions.\n", pending)
				} else if approved > 0 {
					fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", approved)
				} else {
					fmt.Fprintf(os.Stderr, "\nAll items rejected.\n")
				}
				target.Reviewed = (pending == 0)
				if err := database.UpsertWeekState(ctx, target); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
				}
				if pending > 0 {
					return 0, nil
				}
				return approved, nil
			case "e":
				printReviewExport(events, albumEvents, bookEvents)
				return 0, nil
			case "q":
				return 0, nil
			default:
				fmt.Fprintf(os.Stderr, "Invalid choice.\n")
			}
		}
	}

	var pendingEvents []db.EventWithTitle
	for _, ev := range events {
		if ev.Event.Status == model.StatusPending {
			pendingEvents = append(pendingEvents, ev)
		}
	}

	var pendingAlbumEvents []db.EventWithAlbum
	for _, ev := range albumEvents {
		if ev.Event.Status == model.StatusPending {
			pendingAlbumEvents = append(pendingAlbumEvents, ev)
		}
	}

	var pendingBookEvents []db.EventWithBook
	for _, ev := range bookEvents {
		if ev.Event.Status == model.StatusPending {
			pendingBookEvents = append(pendingBookEvents, ev)
		}
	}

	autoApproved := approved // yolo auto-approvals counted before TUI
	tui, err := review.NewReviewTUIWithEvents(pendingEvents, pendingAlbumEvents, pendingBookEvents, database, cfg.PosterMode, year, week, prevAnimeWeek, model.BookFormat(cfg.MediaTypes.Books.DefaultFormat))
	if err != nil {
		return 0, err
	}
	if err := tui.Run(); err != nil {
		return 0, err
	}

	approved = tui.ApprovedCount() + autoApproved
	_, _, pending := tui.Counts()
	if pending > 0 {
		fmt.Fprintf(os.Stderr, "\n%d items still need decisions.\n", pending)
	} else if approved > 0 {
		fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", approved)
	} else {
		fmt.Fprintf(os.Stderr, "\nAll items rejected — nothing to process.\n")
	}
	target.Reviewed = (pending == 0)
	if err := database.UpsertWeekState(ctx, target); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
	}
	if pending > 0 {
		return 0, nil
	}
	return approved, nil
}

func runProcessForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int, typeFilter model.MediaType) error {
	var target *model.WeekState
	var err error

	if year != 0 && week != 0 {
		target, err = database.GetWeekState(ctx, year, week)
		if err != nil {
			return fmt.Errorf("finding week state: %w", err)
		}
		if target == nil {
			return fmt.Errorf("week %d-W%02d not discovered yet", year, week)
		}
	} else {
		target, err = database.GetLatestDiscoveredWeek(ctx)
		if err != nil {
			return fmt.Errorf("finding target week: %w", err)
		}
		if target == nil {
			log.Info().Msg("No weeks discovered yet.")
			log.Info().Msg("Run 'wmdl discover' first.")
			return nil
		}
	}

	year, week = target.Year, target.Week

	// Auto-approve yolo-mode items so they're processable without review
	autoApproveYoloItems(ctx, database, cfg, year, week)

	if !target.Reviewed {
		// After auto-approval, check if any non-yolo items remain pending
		pending, _ := database.ListEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
		pendingAlbums, _ := database.ListAlbumEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
		pendingBooks, _ := database.ListBookEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
		if len(pending) == 0 && len(pendingAlbums) == 0 && len(pendingBooks) == 0 {
			target.Reviewed = true
			if err := database.UpsertWeekState(ctx, target); err != nil {
				log.Warn().Err(err).Msg("tracking week state")
			}
		} else {
			return fmt.Errorf("week %d-W%02d has not been fully reviewed — run 'wmdl review' first", year, week)
		}
	}

	allEvents, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		return fmt.Errorf("loading events: %w", err)
	}
	if len(allEvents) == 0 {
		log.Info().Msgf("No video/anime releases found for week %d-W%02d.", year, week)
	}

	albumEventsForProcess, _ := database.ListAlbumEventsByWeekAndStatus(ctx, year, week, model.StatusApproved)
	bookEventsForProcess, _ := database.ListBookEventsByWeekAndStatus(ctx, year, week, model.StatusApproved)

	if typeFilter != "" {
		switch typeFilter {
		case model.MediaTypeMusic:
			allEvents = nil
			bookEventsForProcess = nil
		case model.MediaTypeBook:
			allEvents = nil
			albumEventsForProcess = nil
		default:
			var filtered []db.EventWithTitle
			for _, ev := range allEvents {
				if ev.Title.MediaType == typeFilter {
					filtered = append(filtered, ev)
				}
			}
			allEvents = filtered
			albumEventsForProcess = nil
			bookEventsForProcess = nil
		}
	}

	var pending, downloaded []db.EventWithTitle
	for _, ev := range allEvents {
		switch ev.Event.Status {
		case model.StatusApproved:
			pending = append(pending, ev)
		case model.StatusDownloaded:
			downloaded = append(downloaded, ev)
		}
	}

	var events []db.EventWithTitle

	// Determine which services are needed based on active processing modes
	modes := cfg.UsedModes()

	switch {
	case len(pending) == 0 && len(downloaded) == 0 && len(albumEventsForProcess) == 0 && len(bookEventsForProcess) == 0:
		log.Info().Msgf("No processable releases for week %d-W%02d — all rejected.", year, week)
		target.Processed = true
		if err := database.UpsertWeekState(ctx, target); err != nil {
			log.Warn().Err(err).Msg("tracking week state")
		}
		return nil

	case len(downloaded) > 0 && len(pending) == 0:
		log.Info().Msgf("All %d releases for %d-W%02d already downloaded.", len(downloaded), year, week)
		if modes["yolo"] {
			log.Info().Msg("yolo mode: re-processing all")
		} else if !promptYesNo(ctx, "Continue anyway (re-process all)?") {
			return nil
		}
		events = downloaded

	case len(downloaded) > 0 && len(pending) > 0:
		log.Info().Msgf("%d/%d releases already downloaded for %d-W%02d.", len(downloaded), len(pending)+len(downloaded), year, week)
		for {
			log.Info().Msg("[a] re-process all  [u] only not-yet-downloaded  [q] quit")
			fmt.Fprintf(os.Stderr, "Choose: ")
			var choice string
			if _, err := fmt.Scanln(&choice); err != nil {
				return nil
			}
			switch choice {
			case "a":
				events = append(pending, downloaded...)
			case "u":
				events = pending
			case "q":
				return nil
			default:
				log.Info().Msg("Invalid choice.")
				continue
			}
			break
		}

	default:
		events = pending
	}

	exec := process.NewExecutor(log.Logger, cfg, database)

	needsProwlarr := modes["full"] || modes["prowlarr-grab"]
	needsDownloader := modes["full"]
	needsLibrary := modes["full"] || modes["arr"] || modes["auto"] || modes["yolo"]

	// Split anime Phase B (airing) items — skip search, add directly to Sonarr
	var animeAiring []db.EventWithTitle
	var searchable []db.EventWithTitle
	for _, ev := range events {
		if ev.Title.MediaType == model.MediaTypeAnime && ev.Event.Source == "jikan-airing" {
			animeAiring = append(animeAiring, ev)
		} else {
			searchable = append(searchable, ev)
		}
	}
	events = searchable

	// For anime airing items in prowlarr-grab mode, skip them entirely
	// (no Sonarr to add to)
	if !needsLibrary {
		animeAiring = nil
	}

	hasSearchable := len(events) > 0
	hasAlbums := len(albumEventsForProcess) > 0
	hasBooks := len(bookEventsForProcess) > 0
	hasAnimeAiring := len(animeAiring) > 0

	if !hasSearchable && !hasAnimeAiring && !hasAlbums && !hasBooks {
		log.Info().Msgf("No processable releases for week %d-W%02d — all rejected or already downloaded.", year, week)
		target.Processed = true
		if err := database.UpsertWeekState(ctx, target); err != nil {
			log.Warn().Err(err).Msg("tracking week state")
		}
		return nil
	}

	// ─── Health check (mode-aware) ───────────────────────────────
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

	// ─── Pre-warm caches (only if library services are needed) ──
	warmCtx, warmCancel := context.WithCancel(ctx)
	defer warmCancel()

	preWarmDone := make(chan struct{}, 3)
	if !skipLibrary {
		go func() { exec.PreWarmRadarr(warmCtx); preWarmDone <- struct{}{} }()
		go func() { exec.PreWarmSonarr(warmCtx); preWarmDone <- struct{}{} }()
		if len(albumEventsForProcess) > 0 {
			go func() { exec.PreWarmLidarr(warmCtx); preWarmDone <- struct{}{} }()
		} else {
			preWarmDone <- struct{}{} // skip Lidarr
		}
		fmt.Fprintf(os.Stderr, "  Pre-warming library data in background...\n")
	} else {
		preWarmDone <- struct{}{}
		preWarmDone <- struct{}{}
		preWarmDone <- struct{}{}
	}

	// ─── BATCH SEARCH PHASE (all background, no user interaction) ─────
	fmt.Fprintf(os.Stderr, "\n── Batch search phase ──\n")

	var primaryVideoResults []*process.SearchResult
	var musicResults []*process.MusicSearchResult
	var bookResults []*process.BookSearchResult

	if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
		// Compute Phase 3 candidates (collection gaps, earlier seasons) —
		// only for items using modes that manage a library
		var phase3Candidates []process.Phase3Candidate
		if needsLibrary && (hasSearchable || hasAnimeAiring) {
			allForPhase3 := make([]db.EventWithTitle, 0, len(events)+len(animeAiring))
			for _, ev := range events {
				mode := cfg.MediaTypeMode(ev.Title.MediaType)
				if mode == "full" || mode == "arr" || mode == "auto" || mode == "yolo" {
					allForPhase3 = append(allForPhase3, ev)
				}
			}
			for _, ev := range animeAiring {
				mode := cfg.MediaTypeMode(ev.Title.MediaType)
				if mode == "full" || mode == "arr" || mode == "auto" || mode == "yolo" {
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

		// Search all primary video events (skip arr/auto/yolo items — no Prowlarr needed)
		var searchEvents []db.EventWithTitle
		for _, ev := range events {
			mode := cfg.MediaTypeMode(ev.Title.MediaType)
			if mode != "arr" && mode != "auto" && mode != "yolo" {
				searchEvents = append(searchEvents, ev)
			}
		}
		if len(searchEvents) > 0 {
			log.Info().Msgf("Searching %d movie/TV release(s)...", len(searchEvents))
			primaryVideoResults = exec.SearchAll(ctx, searchEvents)
		}

		// Search all Phase 3 candidates
		if len(phase3Candidates) > 0 {
			exec.SearchAllCandidates(ctx, phase3Candidates)
		}

		// Search all music
		if hasAlbums {
			musicResults = exec.SearchMusicAll(ctx, albumEventsForProcess)
		}

		// Search all books (skip for arr/auto/yolo — no library integration yet)
		if hasBooks {
			bookMode := cfg.MediaTypeMode(model.MediaTypeBook)
			if bookMode == "arr" || bookMode == "auto" || bookMode == "yolo" {
				log.Info().Msgf("books in %s mode not yet supported — skipping", bookMode)
			} else {
				bookResults = exec.SearchBooksAll(ctx, bookEventsForProcess)
			}
		}

		// Optional notification: searches complete
		if cfg.Notifier.SearchCompleteNotify {
			notify, err := notifier.New(cfg.Notifier)
			if err == nil {
				msg := fmt.Sprintf("%d-W%02d · %d primary + %d music + %d books",
					year, week, len(events), len(albumEventsForProcess), len(bookEventsForProcess))
				if len(phase3Candidates) > 0 {
					msg += fmt.Sprintf(" + %d collection/season", len(phase3Candidates))
				}
				if err := notify.Send("wmdl: Searches Complete", msg, 5); err != nil {
					log.Warn().Err(err).Msg("sending search complete notification")
				}
			}
		}

		fmt.Fprintf(os.Stderr, "  All searches complete. Starting picker phase...\n")
	} else {
		// Interactive mode — no batch pre-search
		// Processing happens in the picker sections below
		fmt.Fprintf(os.Stderr, "  Interactive mode: processing items one at a time...\n")
	}

	// ─── PRIMARY PICKERS (movies/TV/anime, mode-aware) ──────────
	var picked []process.PickedItem
	if hasSearchable {
		if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
			// Split results by processing mode
			var fullResults, grabResults []*process.SearchResult
			for _, sr := range primaryVideoResults {
				mode := cfg.MediaTypeMode(sr.Event.Title.MediaType)
				if mode == "prowlarr-grab" {
					grabResults = append(grabResults, sr)
				} else {
					fullResults = append(fullResults, sr)
				}
			}
			if len(grabResults) > 0 {
				exec.ProcessGrabResults(ctx, grabResults)
			}
			if len(fullResults) > 0 {
				picked = exec.PickResults(ctx, fullResults)
			}
		} else {
			// Interactive mode — process one at a time
			for _, ev := range events {
				mode := cfg.MediaTypeMode(ev.Title.MediaType)
				if mode == "prowlarr-grab" {
					exec.SearchAndGrabOne(ctx, ev)
				} else {
					if item := exec.SearchAndPickOne(ctx, ev); item != nil {
						picked = append(picked, *item)
					}
				}
			}
		}
	}

	// ─── ARR/AUTO/YOLO MODE ITEMS (no Prowlarr search, add directly to *arr) ──
	// These weren't searched above, so construct synthetic PickedItems
	for _, ev := range events {
		mode := cfg.MediaTypeMode(ev.Title.MediaType)
		if mode == "arr" || mode == "auto" || mode == "yolo" {
			season := quality.ParseSeasonNumber(ev.Title.Title)
			picked = append(picked, process.PickedItem{Event: ev, Season: season})
		}
	}

	// ─── ALL PICKERS (continuous user attention) ─────────────────
	var albumResults []process.MusicAlbumResult

	if hasAlbums {
		if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
			albumResults = exec.PickMusicResults(ctx, musicResults)
		} else {
			albumResults = exec.ProcessMusicAlbumsInteractive(ctx, albumEventsForProcess)
		}
	}

	if hasBooks && (cfg.ProcessMode == "batch" || cfg.ProcessMode == "") {
		exec.PickBookResults(ctx, bookResults)
	}

	// ─── ALL LIBRARY DECISIONS ──────────────────────────────────
	if !skipLibrary {
		if hasSearchable || hasAnimeAiring || hasAlbums {
			fmt.Fprintf(os.Stderr, "  Waiting for library data...\n")
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
	}

	// ─── PHASE 3 PICKERS (collection movies + earlier seasons) ──
	if !skipLibrary {
		exec.ProcessPhase3Pickers(ctx)
	}

	// ─── Anime Phase B processing (airing items) ─────────────────
	for _, ae := range animeAiring {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		mode := cfg.MediaTypeMode(ae.Title.MediaType)
		searchNow := mode == "arr" || mode == "auto" || mode == "yolo"
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

	// ─── Book upload trigger (no user attention needed) ─────────
	if hasBooks {
		bookMode := cfg.MediaTypeMode(model.MediaTypeBook)
		if bookMode == "arr" || bookMode == "auto" || bookMode == "yolo" {
			// Already logged "skipping" in search phase
		} else if cfg.ProcessMode == "batch" || cfg.ProcessMode == "" {
			// batch mode — no media server scan needed
		} else {
			exec.ProcessBooks(ctx, bookEventsForProcess)
		}
	}

	if hasSearchable || hasAnimeAiring || hasAlbums || hasBooks {
		target.Processed = true
		if err := database.UpsertWeekState(ctx, target); err != nil {
			log.Warn().Err(err).Msg("tracking week state")
		}
	}

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

	// ─── Yolo notification: send summary if notifier is configured ──
	if modes["yolo"] && cfg.Notifier.Service != "" {
		notify, nErr := notifier.New(cfg.Notifier)
		if nErr == nil {
			msg := fmt.Sprintf("%d-W%02d processed", year, week)
			if len(picked) > 0 {
				msg += fmt.Sprintf(" · %d movies/TV added to *arr", len(picked))
			}
			if len(albumResults) > 0 {
				msg += fmt.Sprintf(" · %d albums added", len(albumResults))
			}
			if hasBooks && len(bookResults) > 0 {
				msg += fmt.Sprintf(" · %d books", len(bookResults))
			}
			if err := notify.Send("wmdl: Yolo Complete", msg, 5); err != nil {
				log.Warn().Err(err).Msg("sending yolo complete notification")
			}
		}
	}

	return nil
}

// autoApproveYoloItems sets all pending events with yolo mode to approved,
// bypassing the review TUI. Safe to call multiple times (idempotent).
func autoApproveYoloItems(ctx context.Context, database *db.DB, cfg *config.Config, year, week int) {
	events, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		log.Warn().Err(err).Msg("loading events for yolo auto-approve")
		return
	}
	for _, ev := range events {
		if ev.Event.Status == model.StatusPending && cfg.MediaTypeMode(ev.Title.MediaType) == "yolo" {
			if err := database.UpdateReleaseEventStatus(ctx, ev.Event.ID, model.StatusApproved); err != nil {
				log.Warn().Err(err).Str("title", ev.Title.Title).Msg("yolo auto-approve failed")
			} else {
				log.Info().Str("title", ev.Title.Title).Msg("auto-approved (yolo mode)")
			}
		}
	}

	albumEvents, err := database.ListAlbumEventsByWeek(ctx, year, week)
	if err != nil {
		log.Warn().Err(err).Msg("loading album events for yolo auto-approve")
		return
	}
	for _, ae := range albumEvents {
		if ae.Event.Status == model.StatusPending && cfg.MediaTypeMode(model.MediaTypeMusic) == "yolo" {
			if err := database.UpdateAlbumReleaseEventStatus(ctx, ae.Event.ID, model.StatusApproved); err != nil {
				log.Warn().Err(err).Str("album", ae.Album.Title).Msg("yolo auto-approve failed")
			} else {
				log.Info().Str("album", ae.Album.Title).Str("artist", ae.Artist.Name).Msg("auto-approved (yolo mode)")
			}
		}
	}

	bookEvents, err := database.ListBookEventsByWeek(ctx, year, week)
	if err != nil {
		log.Warn().Err(err).Msg("loading book events for yolo auto-approve")
		return
	}
	for _, be := range bookEvents {
		if be.Event.Status == model.StatusPending && cfg.MediaTypeMode(model.MediaTypeBook) == "yolo" {
			if err := database.UpdateBookReleaseEventStatus(ctx, be.Event.ID, model.StatusApproved); err != nil {
				log.Warn().Err(err).Str("book", be.Book.Title).Msg("yolo auto-approve failed")
			} else {
				log.Info().Str("book", be.Book.Title).Str("author", be.Author.Name).Msg("auto-approved (yolo mode)")
			}
		}
	}
}
