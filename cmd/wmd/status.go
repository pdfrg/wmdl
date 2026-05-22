package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmd/internal/db"
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

			states, err := database.GetWeekStates(context.Background(), 12)
			if err != nil {
				return fmt.Errorf("loading week states: %w", err)
			}

			if len(states) == 0 {
				fmt.Println("No week tracking data yet. Run 'wmd discover' first.")
				return nil
			}

			fmt.Println("Week      Date        Discover  Review  Process")
			fmt.Println("────────  ──────────  ────────  ──────  ───────")
			for _, s := range states {
		fmt.Fprintf(os.Stdout, "W%-2d %-4d  %-10s  %-8s  %-6s  %-5s\n",
			s.Week, s.Year,
			formatDate(s.WeekDate),
			checkMark(s.Discovered),
			checkMark(s.Reviewed),
			checkMark(s.Processed),
		)
			}

			// Show summary
			var todo []string
			for _, s := range states {
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
