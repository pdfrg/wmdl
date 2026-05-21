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
	"github.com/pdfrg/wmd/internal/discover"
	"github.com/pdfrg/wmd/internal/process"
	"github.com/pdfrg/wmd/internal/review"
)

func newCatchupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "catchup",
		Short: "Run all pending steps for incomplete weeks",
		Long: `Check week state and run discover, review, or process for any weeks 
that have not been fully handled.`,
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

	if err := database.Migrate(cmd.Context()); err != nil {
		return err
	}

	runner := discover.NewRunner(cfg, database)
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	return runner.Run(ctx)
}

func runReview(database *db.DB) error {
	tui, err := review.NewReviewTUI(database)
	if err != nil {
		return err
	}
	return tui.Run()
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

	events, err := database.ListApprovedWithTitles(ctx)
	if err != nil {
		return err
	}
	if len(events) == 0 {
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
		}
	}
	return nil
}
