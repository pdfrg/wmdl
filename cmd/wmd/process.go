package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/config"
	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
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

			ctx := cmd.Context()

			target, err := database.GetLatestDiscoveredWeek(ctx)
			if err != nil {
				return fmt.Errorf("finding target week: %w", err)
			}
			if target == nil {
				log.Println("No weeks discovered yet.")
				log.Println("Run 'wmd discover' first.")
				return nil
			}
			if target.Processed {
				if !promptYesNo(fmt.Sprintf("Week %d-W%02d already processed. Continue anyway?", target.Year, target.Week)) {
					return nil
				}
			}

			allEvents, err := database.ListApprovedWithTitles(ctx)
			if err != nil {
				return fmt.Errorf("loading approved: %w", err)
			}
			if len(allEvents) == 0 {
				log.Println("No approved releases to process.")
				log.Println("Run 'wmd review' to approve releases first.")
				return nil
			}

			// Filter events to target week — prefer stored iso_year/iso_week
			var events []db.EventWithTitle
			for _, ev := range allEvents {
				if eventMatchesWeek(ev.Event, target.Year, target.Week) {
					events = append(events, ev)
				}
			}

			if len(events) == 0 {
				log.Printf("No approved releases for week %d-W%02d", target.Year, target.Week)
				return nil
			}

			log.Printf("Processing %d release(s) for %d-W%02d", len(events), target.Year, target.Week)

			exec := process.NewExecutor(cfg, database)

			processed := 0
			if cfg.ProcessMode == "batch" {
				results := exec.SearchAll(ctx, events)
				for _, sr := range results {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}

					if len(sr.Top) == 0 {
						continue
					}
					if err := exec.PresentResult(ctx, sr); err != nil {
						log.Printf("Error presenting %q: %v", sr.Event.Title.Title, err)
						continue
					}
					processed++
				}
			} else {
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
					processed++
				}
			}

			// Mark the week as processed (user chose to run, so mark it done)
			target.Processed = true
			if err := database.UpsertWeekState(ctx, target); err != nil {
				log.Printf("Warning: tracking week state: %v", err)
			}

			// Print summary
			log.Printf("Processed %d/%d releases", processed, len(events))

			if len(exec.Unfound) > 0 {
				log.Printf("No results found for %d item(s):", len(exec.Unfound))
				for _, u := range exec.Unfound {
					log.Printf("  - %s", u)
				}
				if promptSaveManualSearch(exec.Unfound) {
					fname := fmt.Sprintf("wmd-manual-search-%d-W%02d.md", target.Year, target.Week)
					if err := writeManualSearchFile(fname, exec.Unfound); err != nil {
						log.Printf("Warning: writing manual search file: %v", err)
					} else {
						log.Printf("Wrote %s", fname)
					}
				}
			}

			return nil
		},
	}
}

func promptYesNo(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
}

func eventMatchesWeek(ev *model.ReleaseEvent, year, week int) bool {
	if ev.ISOYear > 0 && ev.ISOWeek > 0 {
		return ev.ISOYear == year && ev.ISOWeek == week
	}
	t, err := time.Parse("2006-01-02", ev.ReleaseDate)
	if err != nil {
		return false
	}
	y, w := t.ISOWeek()
	return y == year && w == week
}

func promptSaveManualSearch(items []string) bool {
	fmt.Printf("Write unfound items to a file for manual searching? [y/N] ")
	var ans string
	if _, err := fmt.Scanln(&ans); err != nil {
		return false
	}
	return ans == "y" || ans == "Y" || ans == "yes"
}

func writeManualSearchFile(fname string, items []string) error {
	f, err := os.Create(fname)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, _ = fmt.Fprintf(f, "# Manual Search — Week %s\n\n", fname)
	_, _ = fmt.Fprintf(f, "The following items had no matching torrent releases found automatically.\n")
	_, _ = fmt.Fprintf(f, "Consider searching for them manually on your preferred indexers.\n\n")
	for _, item := range items {
		_, _ = fmt.Fprintf(f, "- %s\n", item)
	}
	_, _ = fmt.Fprintf(f, "\n---\nGenerated by wmd\n")
	return nil
}
