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
	"github.com/pdfrg/wmdl/internal/process"
	"github.com/pdfrg/wmdl/internal/review"
)

func runDiscoverForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int, headless bool) (discovered bool, err error) {
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

	events, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		return 0, fmt.Errorf("loading events: %w", err)
	}

	hasPending := false
	for _, ev := range events {
		if ev.Event.Status == model.StatusPending {
			hasPending = true
			break
		}
	}

	if !hasPending || target.Reviewed {
		if !hasPending && !target.Reviewed {
			fmt.Fprintf(os.Stderr, "No pending releases for %d-W%02d.\n", year, week)
		} else {
			fmt.Fprintf(os.Stderr, "All releases for %d-W%02d have already been reviewed.\n", year, week)
		}
		for {
			fmt.Fprintf(os.Stderr, "[r] review again  [e] export choices  [q] quit\n")
			fmt.Fprintf(os.Stderr, "Choose: ")
			var choice string
			if _, err := fmt.Scanln(&choice); err != nil {
				return 0, nil
			}
			switch choice {
			case "r":
				tui, err := review.NewReviewTUIWithEvents(events, nil, database, cfg.PosterMode)
				if err != nil {
					return 0, err
				}
				if err := tui.Run(); err != nil {
					return 0, err
				}
				approved = len(tui.ApprovedTitles())
				if approved > 0 {
					fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", approved)
				}
				target.Reviewed = true
				if err := database.UpsertWeekState(ctx, target); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
				}
				return approved, nil
			case "e":
				printReviewExport(events)
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

	tui, err := review.NewReviewTUIWithEvents(pendingEvents, nil, database, cfg.PosterMode)
	if err != nil {
		return 0, err
	}

	if err := tui.Run(); err != nil {
		return 0, err
	}

	approved = len(tui.ApprovedTitles())
	if approved > 0 {
		fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", approved)
	}

	target.Reviewed = true
	if err := database.UpsertWeekState(ctx, target); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
	}

	return approved, nil
}

func runProcessForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int) error {
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

	allEvents, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		return fmt.Errorf("loading events: %w", err)
	}
	if len(allEvents) == 0 {
		log.Info().Msgf("No releases found for week %d-W%02d.", year, week)
		return nil
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

	switch {
	case len(pending) == 0 && len(downloaded) == 0:
		log.Info().Msgf("No processable releases for week %d-W%02d.", year, week)
		return nil

	case len(downloaded) > 0 && len(pending) == 0:
		log.Info().Msgf("All %d releases for %d-W%02d already downloaded.", len(downloaded), year, week)
		if !promptYesNo(ctx, "Continue anyway (re-process all)?") {
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

	log.Info().Msgf("Processing %d release(s) for %d-W%02d", len(events), year, week)

	exec := process.NewExecutor(log.Logger, cfg, database)

	health := exec.HealthCheck(ctx)
	skipLibrary := false

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
					health = exec.HealthCheck(ctx)
					if len(health.Critical) == 0 && len(health.Warnings) == 0 {
						fmt.Fprintln(os.Stderr, "  All services OK")
						break
					}
					if len(health.Critical) > 0 {
						continue
					}
					// Warnings remain, fall through to warnings handler
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
					health = exec.HealthCheck(ctx)
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

	preWarmDone := make(chan struct{}, 2)
	if !skipLibrary {
		go func() { exec.PreWarmRadarr(ctx); preWarmDone <- struct{}{} }()
		go func() { exec.PreWarmSonarr(ctx); preWarmDone <- struct{}{} }()
		fmt.Fprintf(os.Stderr, "  Pre-warming library data in background...\n")
	}

	var picked []process.PickedItem

	if cfg.ProcessMode == "batch" {
		picked = exec.SearchAndPickAll(ctx, events)
	} else {
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if item := exec.SearchAndPickOne(ctx, ev); item != nil {
				picked = append(picked, *item)
			}
		}
	}

	if !skipLibrary {
		fmt.Fprintf(os.Stderr, "  Waiting for library data...\n")
		<-preWarmDone
		<-preWarmDone
		exec.ProcessLibraryDecisions(ctx, picked)
	}

	target.Processed = true
	if err := database.UpsertWeekState(ctx, target); err != nil {
		log.Warn().Err(err).Msg("tracking week state")
	}

	log.Info().Msgf("Processed %d/%d releases", len(picked), len(events))

	if len(exec.Unfound) > 0 {
		log.Warn().Msgf("No results found for %d item(s):", len(exec.Unfound))
		for _, u := range exec.Unfound {
			log.Info().Str("title", u).Msg("unfound")
		}
	}

	// ─── Music album processing ──────────────────────────────────────────
	albumEvents, err := database.ListApprovedAlbumEventsWithAlbums(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("loading approved album events")
	} else if len(albumEvents) > 0 {
		log.Info().Msgf("Processing %d music album(s)...", len(albumEvents))
		for _, ae := range albumEvents {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err := exec.ProcessMusicAlbum(ctx, ae); err != nil {
				log.Warn().Err(err).Str("album", ae.Album.Title).Str("artist", ae.Artist.Name).Msg("error processing album")
			}
		}
	}

	return nil
}
