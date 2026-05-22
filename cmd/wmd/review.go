package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
	"github.com/pdfrg/wmd/internal/review"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review pending releases in TUI",
		Long:  "Open interactive TUI to approve/reject pending releases.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmd.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			ctx := context.Background()

			// Resolve target week
			var target *model.WeekState
			if cmd.Flags().Changed("week") {
				y, w, err := resolveWeek(cmd)
				if err != nil {
					return err
				}
				target, err = database.GetWeekState(ctx, y, w)
				if err != nil {
					return fmt.Errorf("finding week state: %w", err)
				}
				if target == nil {
					return fmt.Errorf("week %d-W%02d not discovered yet", y, w)
				}
			} else {
				target, err = database.GetLatestDiscoveredWeek(ctx)
				if err != nil {
					return fmt.Errorf("finding target week: %w", err)
				}
				if target == nil {
					fmt.Fprintln(os.Stderr, "No weeks discovered yet. Run 'wmd discover' first.")
					return nil
				}
			}

			// Load all events for the week to check state
			events, err := database.ListEventsByWeekWithTitles(ctx, target.Year, target.Week)
			if err != nil {
				return fmt.Errorf("loading events: %w", err)
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
					fmt.Fprintf(os.Stderr, "No pending releases for %d-W%02d.\n", target.Year, target.Week)
				} else {
					fmt.Fprintf(os.Stderr, "All releases for %d-W%02d have already been reviewed.\n", target.Year, target.Week)
				}
				for {
					fmt.Fprintf(os.Stderr, "[r] review again  [e] export choices  [q] quit\n")
					fmt.Fprintf(os.Stderr, "Choose: ")
					var choice string
					if _, err := fmt.Scanln(&choice); err != nil {
						return nil
					}
					switch choice {
					case "r":
						tui, err := review.NewReviewTUIWithEvents(events, database, cfg.PosterMode)
						if err != nil {
							return err
						}
						if err := tui.Run(); err != nil {
							return err
						}
						approved := tui.ApprovedTitles()
						if len(approved) > 0 {
							fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", len(approved))
							fmt.Fprintf(os.Stderr, "Run 'wmd process' to search and download.\n")
						}
						target.Reviewed = true
						if err := database.UpsertWeekState(ctx, target); err != nil {
							fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
						}
						return nil
					case "e":
						printReviewExport(events)
						return nil
					case "q":
						return nil
					default:
						fmt.Fprintf(os.Stderr, "Invalid choice.\n")
					}
				}
			}

			// First review — only show pending events
			var pendingEvents []db.EventWithTitle
			for _, ev := range events {
				if ev.Event.Status == model.StatusPending {
					pendingEvents = append(pendingEvents, ev)
				}
			}
			tui, err := review.NewReviewTUIWithEvents(pendingEvents, database, cfg.PosterMode)
			if err != nil {
				return err
			}

			if err := tui.Run(); err != nil {
				return err
			}

			approved := tui.ApprovedTitles()
			if len(approved) > 0 {
				fmt.Fprintf(os.Stderr, "\nApproved %d titles for processing.\n", len(approved))
				fmt.Fprintf(os.Stderr, "Run 'wmd process' to search and download.\n")
			}

			target.Reviewed = true
			if err := database.UpsertWeekState(ctx, target); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
			}
			return nil
		},
	}
	addWeekFlag(cmd)
	return cmd
}

func printReviewExport(events []db.EventWithTitle) {
	var approved, rejected []string
	for _, ev := range events {
		title := ev.Title.Title
		if ev.Title.Year > 0 {
			title = fmt.Sprintf("%s (%d)", title, ev.Title.Year)
		}
		switch ev.Event.Status {
		case model.StatusApproved:
			approved = append(approved, title)
		case model.StatusRejected:
			rejected = append(rejected, title)
		}
	}
	fmt.Println("── Items to download ──")
	if len(approved) > 0 {
		for _, t := range approved {
			fmt.Printf("  ✓ %s\n", t)
		}
	} else {
		fmt.Println("  (none)")
	}
	fmt.Println()
	fmt.Println("── Items rejected ──")
	if len(rejected) > 0 {
		for _, t := range rejected {
			fmt.Printf("  ✗ %s\n", t)
		}
	} else {
		fmt.Println("  (none)")
	}
}
