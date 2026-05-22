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
)

func newCatchupCmd() *cobra.Command {
	return &cobra.Command{
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

			states, err := database.GetWeekStates(context.Background(), 12)
			if err != nil {
				return fmt.Errorf("loading week states: %w", err)
			}

			if len(states) == 0 {
				log.Println("No week data found. Running discover for current week.")
				return runDiscover(cmd)
			}

			for _, s := range states {
				if !s.Discovered {
					log.Printf("Week %d/%d: needs discover, running now...", s.Year, s.Week)
					if err := runDiscover(cmd); err != nil {
						log.Printf("  discover failed: %v", err)
					}
					continue
				}

				if !s.Reviewed {
					log.Printf("Week %d/%d: needs review, running now...", s.Year, s.Week)
					if err := runReview(database); err != nil {
						log.Printf("  review failed: %v", err)
					}
					continue
				}

				if !s.Processed {
					log.Printf("Week %d/%d: needs process, running now...", s.Year, s.Week)
					if err := runProcess(cmd); err != nil {
						log.Printf("  process failed: %v", err)
					}
				}
			}

			// Re-check whether any weeks still have remaining steps
			remaining, err := database.GetWeekStates(context.Background(), 12)
			if err == nil {
				hasRemaining := false
				for _, s := range remaining {
					if !s.Discovered || !s.Reviewed || !s.Processed {
						hasRemaining = true
						break
					}
				}
				if hasRemaining {
					log.Println("Some weeks still have pending steps. Run 'wmd catchup' again to continue.")
				}
			}

			return nil
		},
	}
}

func runDiscover(cmd *cobra.Command) error {
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
		return err
	}
	defer database.Close()

	return runDiscoverForWeek(cmd.Context(), database, cfg, 0, 0)
}

func runReview(database *db.DB) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	_, err = runReviewForWeek(context.Background(), database, cfg, 0, 0)
	return err
}

func runProcess(cmd *cobra.Command) error {
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
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	return runProcessForWeek(ctx, database, cfg, 0, 0)
}
