package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/model"
)

func newDiscoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Scrape release sources and notify",
		Long: `Scrape DVD/streaming release sites, enrich with TMDB/RT data, save to database, and send notification.

Lookback config keys (set in config.yaml):
  streaming_lookback_weeks     movie/TV streaming (default 8)
  physical_lookback_weeks      DVD/BluRay (default 0)
  lookback_weeks               anime (default 0)
  lookback_weeks               music (default 1)
  lookback_weeks               books (default 1)

These control how many weeks before the target WMDL week each scraper
looks for releases. They are read fresh from config each run.

--lookback is a one-shot override that iterates the specified range
of lookback weeks and files everything under the current target week.
After this run, normal config values resume.
Notes:
  • bookshop always scrapes the current real week, unaffected by --lookback.
  • allmusic participates in --lookback music; its month-boundary logic applies.
  • allmusic (and some other scrapers) require a real browser session and will
    not function in --headless mode due to Cloudflare challenges.`,

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

			headless, _ := cmd.Flags().GetBool("headless")

			typeFilterStr, _ := cmd.Flags().GetString("type")
			var typeFilter model.MediaType
			if typeFilterStr != "" {
				switch model.MediaType(typeFilterStr) {
				case model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeMovie, model.MediaTypeTV, model.MediaTypeBook:
					typeFilter = model.MediaType(typeFilterStr)
				default:
					return fmt.Errorf("invalid type %q: must be anime, movie, tv, music, or book", typeFilterStr)
				}
			}

			lookbackStr, _ := cmd.Flags().GetString("lookback")
			var lookbackOverrides discover.LookbackOverrides
			if lookbackStr != "" {
				if typeFilterStr != "" {
					return fmt.Errorf("--lookback cannot be combined with --type")
				}
				lookbackOverrides, err = parseLookbackFlag(lookbackStr)
				if err != nil {
					return err
				}
			}

			discovered, err := runDiscoverForWeek(cmd.Context(), database, cfg, targetYear, targetWeek, headless, typeFilter, lookbackOverrides)
			if err != nil {
				return err
			}

			if discovered && !headless && term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprintln(os.Stderr)
				if promptYesNo(cmd.Context(), "Review this week now?") {
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
				}
			}
			return nil
		},
	}
	addWeekFlag(cmd)
	cmd.Flags().Bool("headless", false, "Run without opening a browser window (for cron/systemd)")
	cmd.Flags().String("type", "", "Media type to discover (anime, movie, tv, music, book)")
	cmd.Flags().String("lookback", "", `Backfill past weeks for one or more media types.
  Format: type:range[,type:range...]
    type:  movie, tv, physical, anime, music, book
    range: single value (movie:8) or min-max (movie:2-8)
  Overrides config lookback/timeshift values for this run.
  All media types are discovered (not filtered to these types).
  Cannot be combined with --type.
  Examples:
    --lookback movie:2-8,tv:2-8
    --lookback physical:4,anime:0-2
    --lookback music:0-4,book:0-4`)
	return cmd
}

func parseLookbackFlag(s string) (discover.LookbackOverrides, error) {
	ov := make(discover.LookbackOverrides)
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		parts := strings.SplitN(seg, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid lookback segment %q: expected type:range", seg)
		}
		typ := parts[0]
		switch typ {
		case "movie", "tv", "physical", "anime", "music", "book":
		default:
			return nil, fmt.Errorf("invalid lookback type %q: must be movie, tv, physical, anime, music, or book", typ)
		}
		raw := parts[1]
		var lo, hi int
		if strings.Contains(raw, "-") {
			bounds := strings.SplitN(raw, "-", 2)
			if len(bounds) != 2 {
				return nil, fmt.Errorf("invalid lookback range %q: expected N-M", raw)
			}
			var err error
			lo, err = strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid lookback range %q: %w", raw, err)
			}
			hi, err = strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid lookback range %q: %w", raw, err)
			}
			if lo > hi {
				return nil, fmt.Errorf("invalid lookback range %q: min > max", raw)
			}
		} else {
			var err error
			lo, err = strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return nil, fmt.Errorf("invalid lookback value %q: %w", raw, err)
			}
			hi = lo
		}
		if lo < 0 {
			return nil, fmt.Errorf("lookback value must be >= 0, got %d", lo)
		}
		ov[typ] = discover.LookbackRange{Min: lo, Max: hi}
	}
	if len(ov) == 0 {
		return nil, fmt.Errorf("--lookback requires at least one type:range pair (e.g. movie:2-8)")
	}
	return ov, nil
}

func dataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "wmdl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating data dir: %w", err)
	}
	return dir, nil
}
