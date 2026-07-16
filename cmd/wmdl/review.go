package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review pending releases in TUI",
		Long:  "Open interactive TUI to approve/reject pending releases.",
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

			var targetYear, targetWeek int
			if cmd.Flags().Changed("week") {
				targetYear, targetWeek, err = resolveWeek(cmd)
				if err != nil {
					return err
				}
			} else {
				curYear, curWeek, err := resolveWeek(cmd)
				if err != nil {
					return err
				}

				state, err := database.GetWeekState(cmd.Context(), curYear, curWeek)
				if err != nil {
					return fmt.Errorf("checking week state: %w", err)
				}

				if state != nil && state.Discovered {
					targetYear, targetWeek = curYear, curWeek
				} else {
					latest, err := database.GetLatestDiscoveredWeek(cmd.Context())
					if err != nil {
						return fmt.Errorf("finding week state: %w", err)
					}
					if latest != nil {
						log.Info().Msgf("Week %d-W%02d not discovered yet, reviewing %d-W%02d instead", curYear, curWeek, latest.Year, latest.Week)
						targetYear, targetWeek = latest.Year, latest.Week
					}
				}
			}

			approved, err := runReviewForWeek(cmd.Context(), database, cfg, targetYear, targetWeek)
			if err != nil {
				return err
			}

			if approved > 0 && term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprintln(os.Stderr)
				if promptYesNo(cmd.Context(), "Process this week now?") {
					return runProcessForWeek(cmd.Context(), database, cfg, targetYear, targetWeek, "")
				}
			}

			return nil
		},
	}
	addWeekFlag(cmd)
	return cmd
}

func printReviewExport(events []db.EventWithTitle, albumEvents []db.EventWithAlbumRelease, bookEvents []db.EventWithBook) {
	var approved, rejected []string
	for _, ev := range events {
		title := ev.Title.Title
		if ev.Title.Year > 0 {
			title = fmt.Sprintf("%s (%d)", title, ev.Title.Year)
		}
		switch ev.Event.Status {
		case model.StatusApproved:
			approved = append(approved, title)
		case model.StatusRejected:
			rejected = append(rejected, title)
		}
	}
	for _, ae := range albumEvents {
		title := fmt.Sprintf("%s - %s (%d)", ae.Release.ArtistName, ae.Release.Title, ae.Release.Year)
		switch ae.Event.Status {
		case model.StatusApproved:
			approved = append(approved, title)
		case model.StatusRejected:
			rejected = append(rejected, title)
		}
	}
	for _, be := range bookEvents {
		title := fmt.Sprintf("%s — %s", be.Author.Name, be.Book.Title)
		if be.Book.ReleaseYear > 0 {
			title = fmt.Sprintf("%s (%d)", title, be.Book.ReleaseYear)
		}
		switch be.Event.Status {
		case model.StatusApproved:
			approved = append(approved, title)
		case model.StatusRejected:
			rejected = append(rejected, title)
		}
	}
	fmt.Println("── Items to download ──")
	if len(approved) > 0 {
		for _, t := range approved {
			fmt.Printf("  ✓ %s\n", t)
		}
	} else {
		fmt.Println("  (none)")
	}
	fmt.Println()
	fmt.Println("── Items rejected ──")
	if len(rejected) > 0 {
		for _, t := range rejected {
			fmt.Printf("  ✗ %s\n", t)
		}
	} else {
		fmt.Println("  (none)")
	}
}
