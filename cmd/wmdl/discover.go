package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
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
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmdl.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			targetYear, targetWeek, err := resolveWeek(cmd)
			if err != nil {
				return err
			}

			headless, _ := cmd.Flags().GetBool("headless")

			typeFilterStr, _ := cmd.Flags().GetString("type")
			var typeFilter model.MediaType
			if typeFilterStr != "" {
				switch model.MediaType(typeFilterStr) {
				case model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeMovie, model.MediaTypeTV:
					typeFilter = model.MediaType(typeFilterStr)
				default:
					return fmt.Errorf("invalid type %q: must be anime, movie, tv, or music", typeFilterStr)
				}
			}

			discovered, err := runDiscoverForWeek(cmd.Context(), database, cfg, targetYear, targetWeek, headless, typeFilter)
			if err != nil {
				return err
			}

			if discovered && !headless && term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprintln(os.Stderr)
				if promptYesNo(cmd.Context(), "Review this week now?") {
					_, err := runReviewForWeek(cmd.Context(), database, cfg, targetYear, targetWeek)
					return err
				}
			}
			return nil
		},
	}
	addWeekFlag(cmd)
	cmd.Flags().Bool("headless", false, "Run without opening a browser window (for cron/systemd)")
	cmd.Flags().String("type", "", "Media type to discover (anime, movie, tv, music)")
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
	dir := filepath.Join(base, "wmdl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating data dir: %w", err)
	}
	return dir, nil
}
