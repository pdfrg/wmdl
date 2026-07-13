package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
)

func newAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run full pipeline: discover, review, and process",
		Long: `Run discover, review, and process in sequence for a single week,
skipping the intermediate chain prompts.`,
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

			ctx := cmd.Context()

			if _, err := runDiscoverForWeek(ctx, database, cfg, targetYear, targetWeek, false, "", nil); err != nil {
				return err
			}
			if _, err := runReviewForWeek(ctx, database, cfg, targetYear, targetWeek); err != nil {
				return err
			}
			if err := runProcessForWeek(ctx, database, cfg, targetYear, targetWeek, ""); err != nil {
				return err
			}

			return nil
		},
	}
	addWeekFlag(cmd)
	return cmd
}
