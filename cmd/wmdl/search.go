package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/process"
)

type searchFlags struct {
	year    int
	season  string
	grab    bool
	noLib   bool
	auto    bool
	bookFmt string
}

var sFlags searchFlags

func init() {
	f := searchCmd.Flags()
	f.IntVarP(&sFlags.year, "year", "y", 0, "Filter or disambiguate by year")
	f.StringVar(&sFlags.season, "season", "", "Season number, range (1-3, S01-S03), or \"all\" (TV/anime only)")
	f.BoolVar(&sFlags.grab, "grab", false, "Use Prowlarr grab instead of direct download")
	f.BoolVar(&sFlags.noLib, "no-library", false, "Skip adding to library manager")
	f.BoolVar(&sFlags.auto, "auto", false, "Auto-confirm prompts (download and library add)")
	f.StringVar(&sFlags.bookFmt, "format", "", "Book format override (ebook, audiobook, both)")
}

var searchCmd = &cobra.Command{
	Use:   "search <type> <query>",
	Short: "Search Prowlarr, download, and add to library",
	Long: `Ad-hoc Prowlarr search for a specific title, interactively pick a release,
download it, and optionally add it to your library manager (Radarr/Sonarr/Lidarr).

Supported types: movie, tv, anime, music, book

The query is a free-text search string sent to Prowlarr.
For movies, after adding to Radarr, wmdl checks for collection gaps and offers
to search and download missing movies in the same collection.

Season flags for TV/anime:
  --season 4       Single season
  --season 1-3     Range of seasons (searches S01, S02, S03 each)
  --season all     All completed seasons (fetches season list from TMDB)

Examples:
  wmdl search movie "Dune: Part Two" --year 2024
  wmdl search tv "Severance" --season 2
  wmdl search tv "The Bear" --season all
  wmdl search anime "Attack on Titan" --season "1-3"
  wmdl search music "Radiohead - Kid A"
  wmdl search book "Dune - Frank Herbert"
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSearch(cmd.Context(), args[0], args[1])
	},
}

func runSearch(ctx context.Context, typeStr, query string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	mt := model.MediaType(typeStr)
	switch mt {
	case model.MediaTypeMovie, model.MediaTypeTV, model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeBook:
	default:
		return fmt.Errorf("unknown type %q; supported: movie, tv, anime, music, book", typeStr)
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

	s, err := process.NewSearcher(ctx, cfg, database, process.SearcherOptions{
		Auto:      sFlags.auto,
		NoLibrary: sFlags.noLib,
		Grab:      sFlags.grab,
		Year:      sFlags.year,
		Season:    sFlags.season,
		BookFmt:   sFlags.bookFmt,
	})
	if err != nil {
		return err
	}

	switch mt {
	case model.MediaTypeMovie:
		return s.SearchMovie(ctx, query)
	case model.MediaTypeTV:
		return s.SearchTV(ctx, query, false)
	case model.MediaTypeAnime:
		return s.SearchTV(ctx, query, true)
	case model.MediaTypeMusic:
		return s.SearchMusic(ctx, query)
	case model.MediaTypeBook:
		return s.SearchBook(ctx, query)
	}
	return nil
}
