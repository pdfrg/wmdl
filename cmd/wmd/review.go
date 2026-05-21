package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/review"
)

func newReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Review pending releases in TUI",
		Long:  "Open interactive TUI to approve/reject pending releases.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmd.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			tui, err := review.NewReviewTUI(database)
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
			return nil
		},
	}
}
