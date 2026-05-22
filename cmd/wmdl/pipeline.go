package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/process"
	"github.com/pdfrg/wmdl/internal/review"
)

func runDiscoverForWeek(ctx context.Context, database *db.DB, cfg *config.Config, year, week int, headless bool) error {
	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}

	ws, err := database.GetWeekState(ctx, year, week)
	if err != nil {
		return fmt.Errorf("checking week state: %w", err)
	}

	if ws != nil && ws.Discovered {
		fmt.Fprintf(os.Stderr, "Week %d-W%02d already discovered.\n", year, week)
		if !promptYesNo("Continue anyway?") {
			return nil
		}
	}

	runner := discover.NewRunner(cfg, database, headless)
	if year != 0 && week != 0 {
		runner.SetTargetWeek(year, week)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return runner.Run(ctx)
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
				tui, err := review.NewReviewTUIWithEvents(events, database, cfg.PosterMode)
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

	tui, err := review.NewReviewTUIWithEvents(pendingEvents, database, cfg.PosterMode)
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
			log.Println("No weeks discovered yet.")
			log.Println("Run 'wmdl discover' first.")
			return nil
		}
	}

	year, week = target.Year, target.Week

	allEvents, err := database.ListEventsByWeekWithTitles(ctx, year, week)
	if err != nil {
		return fmt.Errorf("loading events: %w", err)
	}
	if len(allEvents) == 0 {
		log.Printf("No releases found for week %d-W%02d.", year, week)
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
		log.Printf("No processable releases for week %d-W%02d.", year, week)
		return nil

	case len(downloaded) > 0 && len(pending) == 0:
		log.Printf("All %d releases for %d-W%02d already downloaded.", len(downloaded), year, week)
		if !promptYesNo("Continue anyway (re-process all)?") {
			return nil
		}
		events = downloaded

	case len(downloaded) > 0 && len(pending) > 0:
		log.Printf("%d/%d releases already downloaded for %d-W%02d.", len(downloaded), len(allEvents), year, week)
		for {
			log.Print("[a] re-process all  [u] only not-yet-downloaded  [q] quit")
			fmt.Fprintf(os.Stderr, "Choose: ")
			var choice string
			if _, err := fmt.Scanln(&choice); err != nil {
				return nil
			}
			switch choice {
			case "a":
				events = allEvents
			case "u":
				events = pending
			case "q":
				return nil
			default:
				log.Print("Invalid choice.")
				continue
			}
			break
		}

	default:
		events = allEvents
	}

	log.Printf("Processing %d release(s) for %d-W%02d", len(events), year, week)

	exec := process.NewExecutor(cfg, database)

	processed := 0
	if cfg.ProcessMode == "batch" {
		results := exec.SearchAll(ctx, events)
		for _, sr := range results {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if len(sr.Top) == 0 {
				continue
			}
			if err := exec.PresentResult(ctx, sr); err != nil {
				log.Printf("Error presenting %q: %v", sr.Event.Title.Title, err)
				continue
			}
			processed++
		}
	} else {
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err := exec.ProcessApproved(ctx, ev); err != nil {
				log.Printf("Error processing %q: %v", ev.Title.Title, err)
				continue
			}
			processed++
		}
	}

	target.Processed = true
	if err := database.UpsertWeekState(ctx, target); err != nil {
		log.Printf("Warning: tracking week state: %v", err)
	}

	log.Printf("Processed %d/%d releases", processed, len(events))

	if len(exec.Unfound) > 0 {
		log.Printf("No results found for %d item(s):", len(exec.Unfound))
		for _, u := range exec.Unfound {
			log.Printf("  - %s", u)
		}
		if promptSaveManualSearch(exec.Unfound) {
			fname := fmt.Sprintf("wmd-manual-search-%d-W%02d.md", year, week)
			if err := writeManualSearchFile(fname, exec.Unfound); err != nil {
				log.Printf("Warning: writing manual search file: %v", err)
			} else {
				log.Printf("Wrote %s", fname)
			}
		}
	}

	return nil
}
