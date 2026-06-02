package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show status of recent weeks",
		Long:  "Display which weeks have been discovered, reviewed, and processed.",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			allStates, err := database.GetWeekStates(ctx, 0)
			if err != nil {
				return fmt.Errorf("loading week states: %w", err)
			}

			if len(allStates) == 0 {
				fmt.Println("No week tracking data yet. Run 'wmdl discover' first.")
				return nil
			}

			// Display: 12 most recent weeks + any older week with incomplete status
			var display []*model.WeekState
			for i, s := range allStates {
				if i < 12 || !s.Discovered || !s.Reviewed || !s.Processed {
					display = append(display, s)
				}
			}

			// Enrich display weeks with download counts
			for _, s := range display {
				dl, app, err := database.GetWeekProcessCounts(ctx, s.Year, s.Week)
				if err == nil {
					s.DownloadedCount = dl
					s.ApprovedCount = app
				}
			}

			fmt.Println("Week      Date        Discover  Review  Process")
			fmt.Println("────────  ──────────  ────────  ──────  ───────")
			for _, s := range display {
				fmt.Fprintf(os.Stdout, "%-8s  %-10s  %-8s  %-6s  %-5s\n",
					weekLabel(s),
					formatDate(s.WeekDate),
					checkMark(s.Discovered),
					checkMark(s.Reviewed),
					processStatus(s),
				)
			}

			// Collect undownloaded items from display weeks (they have counts)
			var remaining []string
			for _, s := range display {
				if s.Processed && s.ApprovedCount > 0 && s.DownloadedCount < s.ApprovedCount {
					remaining = append(remaining, fmt.Sprintf("wmdl process --week %d-W%02d  (%d items remaining)",
						s.Year, s.Week, s.ApprovedCount-s.DownloadedCount))
				}
			}

			// Show summary (based on all weeks, not just display)
			var todo []string
			for _, s := range allStates {
				if !s.Discovered {
					todo = append(todo, fmt.Sprintf("wmdl discover (W%02d %d)", s.Week, s.Year))
				} else if !s.Reviewed {
					todo = append(todo, fmt.Sprintf("wmdl review (W%02d %d)", s.Week, s.Year))
				} else if !s.Processed && s.ApprovedCount == 0 {
					// All items rejected, nothing to process
				} else if !s.Processed {
					todo = append(todo, fmt.Sprintf("wmdl process (W%02d %d)", s.Week, s.Year))
				}
			}

			if len(todo) > 0 {
				fmt.Fprintf(os.Stderr, "\nPending actions:\n")
				for _, t := range todo {
					fmt.Fprintf(os.Stderr, "  - %s\n", t)
				}
				fmt.Fprintf(os.Stderr, "\nRun 'wmdl catchup' to process all pending.\n")
			}

			if len(remaining) > 0 {
				fmt.Fprintf(os.Stderr, "\nWeeks with remaining items:\n")
				for _, r := range remaining {
					fmt.Fprintf(os.Stderr, "  - %s\n", r)
				}
				fmt.Fprintf(os.Stderr, "\nRun the command(s) above to search and download remaining items.\n")
			}

			if len(todo) == 0 && len(remaining) == 0 {
				fmt.Println("\nAll caught up!")
			}

			return nil
		},
	}
}

func processStatus(s *model.WeekState) string {
	if !s.Reviewed {
		return "✗"
	}
	if s.ApprovedCount == 0 {
		return "N/A"
	}
	if s.Processed && s.DownloadedCount >= s.ApprovedCount {
		return "✓"
	}
	if s.Processed {
		return fmt.Sprintf("%d/%d", s.DownloadedCount, s.ApprovedCount)
	}
	return "✗"
}

func weekLabel(s *model.WeekState) string {
	return fmt.Sprintf("%d-W%02d", s.Year, s.Week)
}

func checkMark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func formatDate(d string) string {
	if d == "" || len(d) < 10 {
		return "          "
	}
	return d[:10]
}
