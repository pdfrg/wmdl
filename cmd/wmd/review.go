package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
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

				// Track week state as reviewed
				for _, ev := range approved {
					if t, err := time.Parse("2006-01-02", ev.Event.ReleaseDate); err == nil {
						y, w := t.ISOWeek()
						ws := &model.WeekState{
							Year:     y,
							Week:     w,
							WeekDate: ev.Event.ReleaseDate,
							Reviewed: true,
						}
						if err := database.UpsertWeekState(context.Background(), ws); err != nil {
							fmt.Fprintf(os.Stderr, "Warning: tracking week state: %v\n", err)
						}
						break
					}
				}
			}
			return nil
		},
	}
}
