package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
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
			database, err := db.Open(filepath.Join(dbPath, "wmd.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			allStates, err := database.GetWeekStates(context.Background(), 0)
			if err != nil {
				return fmt.Errorf("loading week states: %w", err)
			}

			if len(allStates) == 0 {
				fmt.Println("No week tracking data yet. Run 'wmd discover' first.")
				return nil
			}

			// Display: 12 most recent weeks + any older week with incomplete status
			var display []*model.WeekState
			for i, s := range allStates {
				if i < 12 || !s.Discovered || !s.Reviewed || !s.Processed {
					display = append(display, s)
				}
			}

			fmt.Println("Week      Date        Discover  Review  Process")
			fmt.Println("────────  ──────────  ────────  ──────  ───────")
			for _, s := range display {
				fmt.Fprintf(os.Stdout, "W%-2d %-4d  %-10s  %-8s  %-6s  %-5s\n",
					s.Week, s.Year,
					formatDate(s.WeekDate),
					checkMark(s.Discovered),
					checkMark(s.Reviewed),
					checkMark(s.Processed),
				)
			}

			// Show summary (based on all weeks, not just display)
			var todo []string
			for _, s := range allStates {
				if !s.Discovered {
					todo = append(todo, fmt.Sprintf("wmd discover (W%02d %d)", s.Week, s.Year))
				} else if !s.Reviewed {
					todo = append(todo, fmt.Sprintf("wmd review (W%02d %d)", s.Week, s.Year))
				} else if !s.Processed {
					todo = append(todo, fmt.Sprintf("wmd process (W%02d %d)", s.Week, s.Year))
				}
			}

			if len(todo) > 0 {
				fmt.Fprintf(os.Stderr, "\nPending actions:\n")
				for _, t := range todo {
					fmt.Fprintf(os.Stderr, "  - %s\n", t)
				}
				fmt.Fprintf(os.Stderr, "\nRun 'wmd catchup' to process all pending.\n")
			} else {
				fmt.Println("\nAll caught up!")
			}

			return nil
		},
	}
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
