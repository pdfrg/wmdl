package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/discover"
)

func newDiscoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Scrape release sources and notify",
		Long:  "Scrape DVD/streaming release sites, enrich with TMDB/RT data, save to database, and send notification.",
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

			if err := database.Migrate(cmd.Context()); err != nil {
				return fmt.Errorf("migrating database: %w", err)
			}

			targetYear, targetWeek, err := resolveWeek(cmd)
			if err != nil {
				return err
			}

			ws, err := database.GetWeekState(cmd.Context(), targetYear, targetWeek)
			if err != nil {
				return fmt.Errorf("checking week state: %w", err)
			}
			if ws != nil && ws.Discovered {
				fmt.Fprintf(os.Stderr, "Week %d-W%02d already discovered.\n", targetYear, targetWeek)
				if !promptYesNo("Continue anyway?") {
					return nil
				}
			}

			runner := discover.NewRunner(cfg, database)
			if targetYear != 0 && targetWeek != 0 {
				runner.SetTargetWeek(targetYear, targetWeek)
			}
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			return runner.Run(ctx)
		},
	}
	addWeekFlag(cmd)
	return cmd
}

func dataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "wmd")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating data dir: %w", err)
	}
	return dir, nil
}
