package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
)

func newCatchupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catchup",
		Short: "Run all pending steps for incomplete weeks",
		Long: `Advance each incomplete week by one step (discover → review → process)
per invocation, then move to the next week.

This means a single run only moves each week one step forward.
For example, if weeks 10, 11, and 12 all need the full pipeline:

  Run 1: discover week 10, discover week 11, discover week 12
  Run 2: review week 10,  review week 11,  review week 12
  Run 3: process week 10, process week 11, process week 12

The individual commands (discover, review, process) target only
one week at a time — use catchup to handle all outstanding weeks.`,
		RunE: func(c *cobra.Command, args []string) error {
			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmdl.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			states, err := database.GetWeekStates(c.Context(), 12)
			if err != nil {
				return fmt.Errorf("loading week states: %w", err)
			}

			if len(states) == 0 {
				log.Info().Msg("No week data found. Running discover for current week.")
				return runDiscover(c, false)
			}

			for _, s := range states {
				if !s.Discovered {
					log.Info().Msgf("Week %d/%d: needs discover, running now...", s.Year, s.Week)
					if err := runDiscover(c, false); err != nil {
						log.Warn().Err(err).Msg("discover failed")
					}
					continue
				}

				if !s.Reviewed {
					log.Info().Msgf("Week %d/%d: needs review, running now...", s.Year, s.Week)
					if err := runReview(c, database); err != nil {
						log.Warn().Err(err).Msg("review failed")
					}
					continue
				}

				if !s.Processed {
					log.Info().Msgf("Week %d/%d: needs process, running now...", s.Year, s.Week)
					if err := runProcess(c); err != nil {
						log.Warn().Err(err).Msg("process failed")
					}
				}
			}

			// Re-check whether any weeks still have remaining steps
			remaining, err := database.GetWeekStates(c.Context(), 12)
			if err == nil {
				hasRemaining := false
				for _, s := range remaining {
					if !s.Discovered || !s.Reviewed || !s.Processed {
						hasRemaining = true
						break
					}
				}
				if hasRemaining {
					log.Info().Msg("Some weeks still have pending steps. Run 'wmdl catchup' again to continue.")
				} else {
					log.Info().Msg("All caught up! Run 'wmdl status' to verify.")
				}
			}

			return nil
		},
	}
	return cmd
}

func runDiscover(cmd *cobra.Command, headless bool) error {
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
		return err
	}
	defer database.Close()

	_, err = runDiscoverForWeek(cmd.Context(), database, cfg, 0, 0, headless)
	return err
}

func runReview(cmd *cobra.Command, database *db.DB) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	_, err = runReviewForWeek(cmd.Context(), database, cfg, 0, 0)
	return err
}

func runProcess(cmd *cobra.Command) error {
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
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	return runProcessForWeek(ctx, database, cfg, 0, 0)
}
