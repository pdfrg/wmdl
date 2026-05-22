package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
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

			targetYear, targetWeek, err := resolveWeek(cmd)
			if err != nil {
				return err
			}

			if err := runDiscoverForWeek(cmd.Context(), database, cfg, targetYear, targetWeek); err != nil {
				return err
			}

			if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprintln(os.Stderr)
				if promptYesNo("Review this week now?") {
					_, err := runReviewForWeek(cmd.Context(), database, cfg, targetYear, targetWeek)
					return err
				}
			}
			return nil
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
