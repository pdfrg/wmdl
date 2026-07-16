package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/model"
)

type addFlags struct {
	tmdbID  int
	tvdbID  int
	malID   int
	mbid    string
	isbn    string
	year    int
	season  int
	status  string
	week    string
	bookFmt string
}

var aFlags addFlags

func init() {
	f := addCmd.Flags()
	f.IntVar(&aFlags.tmdbID, "tmdb", 0, "TMDB ID (movie or TV)")
	f.IntVar(&aFlags.tvdbID, "tvdb", 0, "TVDB ID (TV only)")
	f.IntVar(&aFlags.malID, "mal", 0, "MyAnimeList ID (anime)")
	f.StringVar(&aFlags.mbid, "mbid", "", "MusicBrainz release group ID (music)")
	f.StringVar(&aFlags.isbn, "isbn", "", "ISBN-13 (book)")
	f.IntVarP(&aFlags.year, "year", "y", 0, "Disambiguation year")
	f.IntVar(&aFlags.season, "season", 0, "Season number (TV/anime, default: latest completed)")
	f.StringVar(&aFlags.status, "status", "pending", "Initial status: pending or approved")
	f.StringVar(&aFlags.week, "week", "", "Target week (default: current atomic week)")
	f.StringVar(&aFlags.bookFmt, "format", "", "Book format: ebook, audiobook, both")
}

var addCmd = &cobra.Command{
	Use:   "add <type> [query]",
	Short: "Add a title to the weekly processing pipeline",
	Long: `Add a title to the weekly pipeline, bypassing the discovery step.
The item is stored in the database and picked up by 'wmdl process'.

Specify the item by ID flag or by search query:
  wmdl add movie --tmdb 693134
  wmdl add tv "Severance"
  wmdl add anime "Attack on Titan" --mal 52578
  wmdl add music --mbid "abc-123-..."
  wmdl add book --isbn "9780553897845"

For TV, the latest completed season is used automatically. Use --season
to override. For earlier seasons, use 'wmdl search tv' instead.

Examples:
  wmdl add movie --tmdb 693134
  wmdl add movie "Dune: Part Two" --year 2024
  wmdl add tv "The Bear" --season 4
  wmdl add music --mbid "abc-123-..."
  wmdl add anime "Attack on Titan" --mal 52578
  wmdl add book --isbn "9780553897845"
`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAdd(cmd.Context(), args)
	},
}

func runAdd(ctx context.Context, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	mt := model.MediaType(args[0])
	switch mt {
	case model.MediaTypeMovie, model.MediaTypeTV, model.MediaTypeAnime, model.MediaTypeMusic, model.MediaTypeBook:
	default:
		return fmt.Errorf("unknown type %q; supported: movie, tv, anime, music, book", mt)
	}

	query := ""
	if len(args) > 1 {
		query = args[1]
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

	targetYear, targetWeek, err := parseWeekFlag(aFlags.week)
	if err != nil {
		return fmt.Errorf("parsing --week: %w", err)
	}

	tmdbClient := discover.NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken)

	switch mt {
	case model.MediaTypeMovie:
		return addMovie(ctx, database, tmdbClient, query, targetYear, targetWeek)
	case model.MediaTypeTV:
		return addTV(ctx, database, tmdbClient, query, targetYear, targetWeek)
	case model.MediaTypeAnime:
		return addAnime(ctx, database, query, targetYear, targetWeek)
	case model.MediaTypeMusic:
		return addMusic(ctx, database, query, targetYear, targetWeek)
	case model.MediaTypeBook:
		return addBook(ctx, database, query, targetYear, targetWeek)
	}
	return nil
}

// ─── Movie ─────────────────────────────────────────────

func addMovie(ctx context.Context, database *db.DB, tmdb *discover.TMDBClient, query string, year, week int) error {
	var tmdbID int
	var details *discover.TMDBDetails
	var title string
	var movieYear int

	if aFlags.tmdbID > 0 {
		tmdbID = aFlags.tmdbID
		var err error
		details, err = tmdb.GetMovieDetails(ctx, tmdbID)
		if err != nil {
			return fmt.Errorf("fetching TMDB movie %d: %w", tmdbID, err)
		}
		title = details.DisplayTitle()
		movieYear = details.DisplayYear()
	} else if query != "" {
		enrich, err := tmdb.Enrich(ctx, query, aFlags.year)
		if err != nil {
			return fmt.Errorf("searching TMDB: %w", err)
		}
		if enrich == nil {
			return fmt.Errorf("no TMDB results for %q", query)
		}
		tmdbID = enrich.TMDBID
		title = enrich.Title
		movieYear = enrich.Year
		oc := []string{enrich.OriginCountry}
		if enrich.OriginCountry == "" {
			oc = []string{"US"}
		}
		details = &discover.TMDBDetails{
			Title:               enrich.Title,
			Overview:            enrich.Overview,
			Runtime:             enrich.Runtime,
			IMDbID:              enrich.IMDbID,
			OriginalLanguage:    enrich.OriginalLanguage,
			OriginCountry:       oc,
			BelongsToCollection: enrichCollection(enrich),
		}
		// Re-fetch to get full details if needed
		if details.Overview == "" {
			fullDetails, err := tmdb.GetMovieDetails(ctx, tmdbID)
			if err == nil {
				details = fullDetails
				title = details.DisplayTitle()
				movieYear = details.DisplayYear()
			}
		}
	} else {
		return fmt.Errorf("use --tmdb <id> or provide a movie title")
	}

	originCountry := "US"
	if len(details.OriginCountry) > 0 {
		originCountry = details.OriginCountry[0]
	}
	enrich := &discover.TMDBEnrichment{
		TMDBID:           tmdbID,
		Title:            title,
		Year:             movieYear,
		IMDbID:           details.IMDbID,
		Overview:         details.Overview,
		Genres:           joinGenreNames(details.Genres),
		Runtime:          details.Runtime,
		OriginalLanguage: firstOr(details.OriginalLanguage, "en"),
		OriginCountry:    originCountry,
		CollectionID:     collectionID(details.BelongsToCollection),
		CollectionName:   collectionName(details.BelongsToCollection),
	}
	if details.VoteAverage > 0 {
		enrich.Rating = details.VoteAverage
	}

	return upsertAndCreateMovieEvent(ctx, database, enrich, aFlags.status, year, week)
}

// ─── TV ───────────────────────────────────────────────

func addTV(ctx context.Context, database *db.DB, tmdb *discover.TMDBClient, query string, year, week int) error {
	var tmdbID int
	var details *discover.TMDBDetails
	var tvName string
	var tvYear int

	if aFlags.tmdbID > 0 {
		tmdbID = aFlags.tmdbID
		var err error
		details, err = tmdb.GetTVDetails(ctx, tmdbID)
		if err != nil {
			return fmt.Errorf("fetching TMDB TV %d: %w", tmdbID, err)
		}
		tvName = details.DisplayTitle()
		tvYear = details.DisplayYear()
	} else if query != "" {
		enrich, err := tmdb.Enrich(ctx, query, aFlags.year)
		if err != nil {
			return fmt.Errorf("searching TMDB: %w", err)
		}
		if enrich == nil {
			return fmt.Errorf("no TMDB results for %q", query)
		}
		tmdbID = enrich.TMDBID
		tvName = enrich.Title
		tvYear = enrich.Year
		fullDetails, err := tmdb.GetTVDetails(ctx, tmdbID)
		if err != nil {
			return fmt.Errorf("fetching TV details: %w", err)
		}
		details = fullDetails
		if tvName != "" {
			tvName = details.DisplayTitle()
		}
		if tvYear == 0 {
			tvYear = details.DisplayYear()
		}
	} else {
		return fmt.Errorf("use --tmdb <id> or provide a TV title")
	}

	// Determine season: flag > latest completed > 1
	season := aFlags.season
	if season == 0 {
		season = latestCompletedSeason(details.Seasons)
	}
	if season < 1 {
		season = 1
	}

	titleStr := fmt.Sprintf("%s Season %d", tvName, season)
	fmt.Fprintf(os.Stderr, "  Using season %d for %s\n", season, tvName)

	if aFlags.tvdbID == 0 {
		fmt.Fprintf(os.Stderr, "  Note: no --tvdb provided; Sonarr will look up by title during 'wmdl process'.\n")
	}

	originCountry := "US"
	if len(details.OriginCountry) > 0 {
		originCountry = details.OriginCountry[0]
	}
	enrich := &discover.TMDBEnrichment{
		TMDBID:           tmdbID,
		TVDBID:           aFlags.tvdbID,
		Title:            titleStr,
		Year:             tvYear,
		Overview:         details.Overview,
		Genres:           joinGenreNames(details.Genres),
		Runtime:          details.Runtime,
		OriginalLanguage: firstOr(details.OriginalLanguage, "en"),
		OriginCountry:    originCountry,
	}

	return upsertAndCreateTVEvent(ctx, database, enrich, aFlags.status, year, week)
}

// ─── Anime ─────────────────────────────────────────────

func addAnime(ctx context.Context, database *db.DB, title string, year, week int) error {
	if aFlags.malID == 0 {
		return fmt.Errorf("use --mal <id> (anime requires MAL ID)")
	}

	t := &model.Title{
		MalID:     aFlags.malID,
		Title:     title,
		Year:      aFlags.year,
		MediaType: model.MediaTypeAnime,
	}
	titleID, err := database.UpsertTitle(ctx, t)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	evt := &model.ReleaseEvent{
		TitleID: titleID,
		Source:  "manual-add",
		Status:  model.ReleaseStatus(aFlags.status),
		ISOYear: year,
		ISOWeek: week,
	}
	if _, err := database.CreateReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("creating release event: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Added anime %q (MAL %d) to %d-W%02d as %s\n", title, aFlags.malID, year, week, aFlags.status)
	return nil
}

// ─── Music ─────────────────────────────────────────────

func addMusic(ctx context.Context, database *db.DB, query string, year, week int) error {
	if aFlags.mbid == "" {
		return fmt.Errorf("use --mbid <musicbrainz-release-group-id>")
	}

	artistName := "Unknown Artist"
	albumTitle := query
	if parts := strings.SplitN(query, " - ", 2); len(parts) == 2 {
		artistName = strings.TrimSpace(parts[0])
		albumTitle = strings.TrimSpace(parts[1])
	}

	release := &model.AlbumRelease{
		ArtistName: artistName,
		MBID:       aFlags.mbid,
		Title:      albumTitle,
		Year:       aFlags.year,
	}
	releaseID, err := database.UpsertAlbumRelease(ctx, release)
	if err != nil {
		return fmt.Errorf("saving album release: %w", err)
	}

	evt := &model.AlbumReleaseEvent{
		ReleaseID: releaseID,
		Source:    "manual-add",
		Status:    model.ReleaseStatus(aFlags.status),
		ISOYear:   year,
		ISOWeek:   week,
	}
	if _, err := database.CreateAlbumReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("creating album release event: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Added album %q by %s (MBID: %s) to %d-W%02d as %s\n",
		albumTitle, artistName, aFlags.mbid, year, week, aFlags.status)
	return nil
}

// ─── Book ──────────────────────────────────────────────

func addBook(ctx context.Context, database *db.DB, query string, year, week int) error {
	if aFlags.isbn == "" {
		return fmt.Errorf("use --isbn <isbn13>")
	}

	bookFmt := aFlags.bookFmt
	if bookFmt == "" {
		bookFmt = "both"
	}
	bf := model.BookFormat(bookFmt)
	switch bf {
	case model.BookFormatEbook, model.BookFormatAudiobook, model.BookFormatBoth:
	default:
		return fmt.Errorf("invalid format %q; use ebook, audiobook, or both", bookFmt)
	}

	book := &model.Book{
		ISBN13:      aFlags.isbn,
		Title:       query,
		ReleaseYear: aFlags.year,
	}
	author := &model.Author{
		Name: "Unknown Author",
	}
	authorID, err := database.UpsertAuthor(ctx, author)
	if err != nil {
		return fmt.Errorf("saving author: %w", err)
	}
	book.AuthorID = authorID

	bookID, err := database.UpsertBook(ctx, book)
	if err != nil {
		return fmt.Errorf("saving book: %w", err)
	}

	evt := &model.BookReleaseEvent{
		BookID:     bookID,
		Source:     "manual-add",
		FormatPref: bf,
		Status:     model.ReleaseStatus(aFlags.status),
		ISOYear:    year,
		ISOWeek:    week,
	}
	if _, err := database.CreateBookReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("creating book release event: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Added book %q (ISBN: %s) to %d-W%02d as %s\n",
		query, aFlags.isbn, year, week, aFlags.status)
	return nil
}

// ─── DB helpers ───────────────────────────────────────

func upsertAndCreateMovieEvent(ctx context.Context, database *db.DB, enrich *discover.TMDBEnrichment, status string, year, week int) error {
	t := &model.Title{
		TmdbID:           enrich.TMDBID,
		Title:            enrich.Title,
		TmdbTitle:        enrich.Title,
		Year:             enrich.Year,
		MediaType:        model.MediaTypeMovie,
		ImdbID:           enrich.IMDbID,
		TmdbRating:       enrich.Rating,
		Overview:         enrich.Overview,
		Genres:           enrich.Genres,
		Runtime:          enrich.Runtime,
		PosterPath:       enrich.PosterPath,
		OriginalLanguage: enrich.OriginalLanguage,
		OriginCountry:    enrich.OriginCountry,
		CollectionID:     enrich.CollectionID,
		CollectionName:   enrich.CollectionName,
	}
	titleID, err := database.UpsertTitle(ctx, t)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	evt := &model.ReleaseEvent{
		TitleID: titleID,
		Source:  "manual-add",
		Status:  model.ReleaseStatus(status),
		ISOYear: year,
		ISOWeek: week,
	}
	if _, err := database.CreateReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("creating release event: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Added %s (%d) to %d-W%02d as %s\n", enrich.Title, enrich.Year, year, week, status)
	return nil
}

func upsertAndCreateTVEvent(ctx context.Context, database *db.DB, enrich *discover.TMDBEnrichment, status string, year, week int) error {
	t := &model.Title{
		TmdbID:           enrich.TMDBID,
		TvdbID:           enrich.TVDBID,
		Title:            enrich.Title,
		TmdbTitle:        enrich.Title,
		Year:             enrich.Year,
		MediaType:        model.MediaTypeTV,
		Overview:         enrich.Overview,
		Genres:           enrich.Genres,
		Runtime:          enrich.Runtime,
		OriginalLanguage: enrich.OriginalLanguage,
		OriginCountry:    enrich.OriginCountry,
	}
	titleID, err := database.UpsertTitle(ctx, t)
	if err != nil {
		return fmt.Errorf("saving title: %w", err)
	}

	evt := &model.ReleaseEvent{
		TitleID: titleID,
		Source:  "manual-add",
		Status:  model.ReleaseStatus(status),
		ISOYear: year,
		ISOWeek: week,
	}
	if _, err := database.CreateReleaseEvent(ctx, evt); err != nil {
		return fmt.Errorf("creating release event: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Added %s (%d) to %d-W%02d as %s\n", enrich.Title, enrich.Year, year, week, status)
	return nil
}

// ─── Misc helpers ─────────────────────────────────────

func joinGenreNames(genres []struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}) string {
	names := make([]string, len(genres))
	for i, g := range genres {
		names[i] = g.Name
	}
	return strings.Join(names, ", ")
}

func firstOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func collectionID(coll *struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}) int {
	if coll == nil {
		return 0
	}
	return coll.ID
}

func collectionName(coll *struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}) string {
	if coll == nil {
		return ""
	}
	return coll.Name
}

func enrichCollection(enrich *discover.TMDBEnrichment) *struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
} {
	if enrich.CollectionID == 0 {
		return nil
	}
	return &struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{ID: enrich.CollectionID, Name: enrich.CollectionName}
}

func latestCompletedSeason(seasons []discover.TMDBTVSeason) int {
	latest := 0
	for _, s := range seasons {
		if s.SeasonNumber > 0 && s.AirDatePassed() && s.SeasonNumber > latest {
			latest = s.SeasonNumber
		}
	}
	return latest
}
