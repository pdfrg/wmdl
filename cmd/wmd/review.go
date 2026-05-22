package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/review"
)

func newReviewCmd() *cobra.Command {
	return &cobra.Command{
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

			target, err := database.GetLatestDiscoveredWeek(context.Background())
			if err != nil {
				return fmt.Errorf("finding target week: %w", err)
			}
			if target == nil {
				fmt.Fprintln(os.Stderr, "No weeks discovered yet. Run 'wmd discover' first.")
				return nil
			}
			if target.Reviewed {
				fmt.Fprintf(os.Stderr, "Week %d-W%02d already reviewed.\n", target.Year, target.Week)
				if !promptYesNo("Continue with review anyway?") {
					return nil
				}
			}

			tui, err := review.NewReviewTUI(database, cfg.PosterMode)
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
			if err := database.UpsertWeekState(context.Background(), target); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
			}
			return nil
		},
	}
}
