package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/process"
)

func newProcessCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "process",
		Short: "Search and download approved releases",
		Long: `For each approved release: search Prowlarr, select a release in the TUI picker,
and send it to the download client.`,
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

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()

			events, err := database.ListApprovedWithTitles(ctx)
			if err != nil {
				return fmt.Errorf("loading approved: %w", err)
			}
			if len(events) == 0 {
				log.Println("No approved releases to process.")
				log.Println("Run 'wmd review' to approve releases first.")
				return nil
			}

			exec := process.NewExecutor(cfg, database)

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
			}

			return nil
		},
	}
}
