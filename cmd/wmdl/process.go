package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "Search and download approved releases",
		Long: `For each approved release: search Prowlarr, select a release in the TUI picker,
and send it to the download client.`,
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

			refreshCache, _ := cmd.Flags().GetBool("refresh-cache")
			if refreshCache {
				if err := database.PurgeLibraryCache(cmd.Context()); err != nil {
					return fmt.Errorf("purging library cache: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  Library cache purged, will re-fetch from *arr services\n")
			}

			var typeFilter model.MediaType
			typeFilterStr, _ := cmd.Flags().GetString("type")
			if typeFilterStr != "" {
				switch model.MediaType(typeFilterStr) {
				case model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeMovie, model.MediaTypeTV, model.MediaTypeBook:
					typeFilter = model.MediaType(typeFilterStr)
				default:
					return fmt.Errorf("invalid type %q: must be anime, movie, tv, music, or book", typeFilterStr)
				}
			}

			targetYear, targetWeek, err := resolveWeek(cmd)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			return runProcessForWeek(ctx, database, cfg, targetYear, targetWeek, typeFilter)
		},
	}
	addWeekFlag(cmd)
	cmd.Flags().String("type", "", "Media type to process (anime, movie, tv, music, book)")
	cmd.Flags().Bool("refresh-cache", false, "Force re-fetch of library cache from *arr services")
	return cmd
}

func promptYesNo(ctx context.Context, prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		ch <- scanner.Text()
	}()
	select {
	case ans := <-ch:
		return strings.ToLower(strings.TrimSpace(ans)) == "y" || strings.ToLower(strings.TrimSpace(ans)) == "yes"
	case <-ctx.Done():
		return false
	}
}
