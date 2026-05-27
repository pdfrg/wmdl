package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/process"
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

			ctx := cmd.Context()

			// Resolve target week
			var target *model.WeekState
			if cmd.Flags().Changed("week") {
				y, w, err := resolveWeek(cmd)
				if err != nil {
					return err
				}
				target, err = database.GetWeekState(ctx, y, w)
				if err != nil {
					return fmt.Errorf("finding week state: %w", err)
				}
				if target == nil {
					return fmt.Errorf("week %d-W%02d not discovered yet", y, w)
				}
			} else {
				target, err = database.GetLatestDiscoveredWeek(ctx)
				if err != nil {
					return fmt.Errorf("finding target week: %w", err)
				}
				if target == nil {
					log.Info().Msg("No weeks discovered yet.")
					log.Info().Msg("Run 'wmdl discover' first.")
					return nil
				}
			}

			allEvents, err := database.ListEventsByWeekWithTitles(ctx, target.Year, target.Week)
			if err != nil {
				return fmt.Errorf("loading events: %w", err)
			}
			if len(allEvents) == 0 {
				log.Info().Msgf("No releases found for week %d-W%02d.", target.Year, target.Week)
				return nil
			}

			// Partition into pending and downloaded
			var pending, downloaded []db.EventWithTitle
			for _, ev := range allEvents {
				switch ev.Event.Status {
				case model.StatusApproved:
					pending = append(pending, ev)
				case model.StatusDownloaded:
					downloaded = append(downloaded, ev)
				}
			}

			var events []db.EventWithTitle

			switch {
			case len(pending) == 0 && len(downloaded) == 0:
				log.Info().Msgf("No processable releases for week %d-W%02d.", target.Year, target.Week)
				return nil

			case len(downloaded) > 0 && len(pending) == 0:
				log.Info().Msgf("All %d releases for %d-W%02d already downloaded.", len(downloaded), target.Year, target.Week)
				if !promptYesNo(ctx, "Continue anyway (re-process all)?") {
					return nil
				}
				events = downloaded

			case len(downloaded) > 0 && len(pending) > 0:
				log.Info().Msgf("%d/%d releases already downloaded for %d-W%02d.", len(downloaded), len(pending)+len(downloaded), target.Year, target.Week)
				for {
					log.Info().Msg("[a] re-process all  [u] only not-yet-downloaded  [q] quit")
					fmt.Fprintf(os.Stderr, "Choose: ")
					var choice string
					if _, err := fmt.Scanln(&choice); err != nil {
						return nil
					}
					switch choice {
					case "a":
						events = append(pending, downloaded...)
					case "u":
						events = pending
					case "q":
						return nil
					default:
						log.Info().Msg("Invalid choice.")
						continue
					}
					break
				}

			default:
				events = pending
			}

			log.Info().Msgf("Processing %d release(s) for %d-W%02d", len(events), target.Year, target.Week)

			exec := process.NewExecutor(log.Logger, cfg, database)

			var picked []process.PickedItem

			if cfg.ProcessMode == "batch" {
				picked = exec.SearchAndPickAll(ctx, events)
			} else {
				for _, ev := range events {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}

					if item := exec.SearchAndPickOne(ctx, ev); item != nil {
						picked = append(picked, *item)
					}
				}
			}

			exec.ProcessLibraryDecisions(ctx, picked)

			// Mark the week as processed
			target.Processed = true
			if err := database.UpsertWeekState(ctx, target); err != nil {
				log.Warn().Err(err).Msg("tracking week state")
			}

			log.Info().Msgf("Processed %d/%d releases", len(picked), len(events))

			if len(exec.Unfound) > 0 {
				log.Warn().Msgf("No results found for %d item(s):", len(exec.Unfound))
				for _, u := range exec.Unfound {
					log.Info().Str("title", u).Msg("unfound")
				}
			}

			return nil
		},
	}
	addWeekFlag(cmd)
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
