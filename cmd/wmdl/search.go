package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/discover"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/process"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
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

	s, err := newSearcher(ctx, cfg, database)
	if err != nil {
		return err
	}

	switch mt {
	case model.MediaTypeMovie:
		return s.searchMovie(ctx, query)
	case model.MediaTypeTV:
		return s.searchTV(ctx, query, false)
	case model.MediaTypeAnime:
		return s.searchTV(ctx, query, true)
	case model.MediaTypeMusic:
		return s.searchMusic(ctx, query)
	case model.MediaTypeBook:
		return s.searchBook(ctx, query)
	}
	return nil
}

type searcher struct {
	cfg   *config.Config
	db    *db.DB
	log   zerolog.Logger
	prowl *search.ProwlarrClient
	dl    download.Client
	tmdb  *discover.TMDBClient

	radarr *library.RadarrClient
	sonarr *library.SonarrClient
	lidarr *library.LidarrClient

	auto      bool
	noLibrary bool
	grab      bool
}

func newSearcher(ctx context.Context, cfg *config.Config, database *db.DB) (*searcher, error) {
	s := &searcher{
		cfg:       cfg,
		db:        database,
		log:       log.With().Str("component", "search").Logger(),
		auto:      sFlags.auto,
		noLibrary: sFlags.noLib,
		grab:      sFlags.grab,
	}

	catMap := map[string]int{
		"videos":     cfg.Prowlarr.IndexerIDs.Videos,
		"music":      cfg.Prowlarr.IndexerIDs.Music,
		"anime":      cfg.Prowlarr.IndexerIDs.Anime,
		"ebooks":     cfg.Prowlarr.IndexerIDs.Ebooks,
		"audiobooks": cfg.Prowlarr.IndexerIDs.Audiobooks,
	}
	s.prowl = search.NewProwlarrClient(cfg.Prowlarr.URL, cfg.Prowlarr.APIKey, cfg.Prowlarr.Timeout, catMap)

	if cfg.TMDB.APIKey != "" {
		s.tmdb = discover.NewTMDBClient(cfg.TMDB.APIKey, cfg.TMDB.AccessToken)
	}

	switch cfg.Downloader.Type {
	case "qbittorrent":
		s.dl = download.NewQbittorrentClient(cfg.Downloader.Qbittorrent.URL, cfg.Downloader.Qbittorrent.Username, cfg.Downloader.Qbittorrent.Password)
	case "transmission":
		s.dl = download.NewTransmissionClient(cfg.Downloader.Transmission.URL, cfg.Downloader.Transmission.Username, cfg.Downloader.Transmission.Password)
	case "deluge":
		s.dl = download.NewDelugeClient(cfg.Downloader.Deluge.URL, cfg.Downloader.Deluge.Password)
	}

	if cfg.Library.Radarr.URL != "" && cfg.Library.Radarr.APIKey != "" {
		s.radarr = library.NewRadarrClient(cfg.Library.Radarr.URL, cfg.Library.Radarr.APIKey, cfg.Library.Radarr.Timeout)
	}
	if cfg.Library.Sonarr.URL != "" && cfg.Library.Sonarr.APIKey != "" {
		s.sonarr = library.NewSonarrClient(cfg.Library.Sonarr.URL, cfg.Library.Sonarr.APIKey, cfg.Library.Sonarr.Timeout)
	}
	if cfg.Library.Lidarr.URL != "" && cfg.Library.Lidarr.APIKey != "" {
		s.lidarr = library.NewLidarrClient(cfg.Library.Lidarr.URL, cfg.Library.Lidarr.APIKey, cfg.Library.Lidarr.Timeout)
	}

	return s, nil
}

// ─── Movie search ─────────────────────────────────────────

func (s *searcher) searchMovie(ctx context.Context, query string) error {
	fmt.Fprintf(os.Stderr, "  Searching Prowlarr for movie: %s\n", query)
	releases, err := s.prowl.SearchMovies(ctx, query)
	if err != nil {
		return fmt.Errorf("prowlarr search: %w", err)
	}
	if len(releases) == 0 {
		fmt.Fprintf(os.Stderr, "  No results found.\n")
		return nil
	}

	exact, _ := quality.PartitionReleases(releases, query, sFlags.year, 0, "movie")
	prefs := qualityPrefs(s.cfg, "movie", s.prowl.PreferredIndexerID(search.CatMovie))
	top := quality.SortAndTop(exact, prefs, s.cfg.ShowTopN)
	if len(top) == 0 {
		fmt.Fprintf(os.Stderr, "  No matching releases found.\n")
		return nil
	}

	sel := process.NewSelector(query, top)
	chosen, err := sel.Run()
	if err != nil {
		return nil
	}
	if len(chosen) == 0 {
		fmt.Fprintf(os.Stderr, "  Skipped.\n")
		return nil
	}

	category := s.cfg.Downloader.Categories.Movies
	if category == "" {
		category = "Movies"
	}
	if err := s.downloadReleases(ctx, chosen, category); err != nil {
		return err
	}

	if s.noLibrary || s.radarr == nil {
		fmt.Fprintf(os.Stderr, "  Downloaded. Skipping Radarr add (--no-library or not configured).\n")
		return nil
	}

	tmdbID, title, year, err := s.enrichMovie(ctx, query)
	if err != nil {
		return fmt.Errorf("enriching movie: %w", err)
	}
	if tmdbID == 0 {
		fmt.Fprintf(os.Stderr, "  Could not find movie on TMDB. Skipping Radarr add.\n")
		return nil
	}
	if err := s.addToRadarr(ctx, tmdbID, title, year); err != nil {
		return fmt.Errorf("adding to Radarr: %w", err)
	}
	if s.cfg.CheckCollections {
		if err := s.checkCollectionGaps(ctx, tmdbID); err != nil {
			s.log.Warn().Err(err).Msg("checking collection gaps")
		}
	}
	return nil
}

// ─── TV/Anime search ──────────────────────────────────────

func (s *searcher) searchTV(ctx context.Context, query string, isAnime bool) error {
	seasons, allSeasons, err := quality.ParseSeasonRange(sFlags.season)
	if err != nil {
		return fmt.Errorf("parsing --season: %w", err)
	}

	if allSeasons {
		if s.tmdb == nil {
			return fmt.Errorf("--season all requires TMDB API key configured")
		}
		seasons, err = s.fetchCompletedSeasons(ctx, query)
		if err != nil {
			return fmt.Errorf("fetching seasons: %w", err)
		}
		if len(seasons) == 0 {
			fmt.Fprintf(os.Stderr, "  No completed seasons found on TMDB.\n")
			return nil
		}
		fmt.Fprintf(os.Stderr, "  Found %d completed season(s): %v\n", len(seasons), seasons)
	}

	var tvdbID int
	var seriesName string
	var seriesYear int

	for i, season := range seasons {
		seasonLabel := fmt.Sprintf("%s S%02d", query, season)
		fmt.Fprintf(os.Stderr, "\n  [%d/%d] Searching %s\n", i+1, len(seasons), seasonLabel)

		var prowlReleases []quality.ParsedRelease
		if isAnime {
			prowlReleases, err = s.prowl.SearchAnime(ctx, seasonLabel)
		} else {
			prowlReleases, err = s.prowl.SearchTV(ctx, seasonLabel)
		}
		if err != nil {
			s.log.Warn().Err(err).Str("season", seasonLabel).Msg("search failed")
			continue
		}
		if len(prowlReleases) == 0 {
			fmt.Fprintf(os.Stderr, "  No results.\n")
			continue
		}

		cat := search.CatTV
		if isAnime {
			cat = search.CatAnime
		}
		prefs := qualityPrefs(s.cfg, "tv", s.prowl.PreferredIndexerID(cat))
		exact, _ := quality.PartitionReleases(prowlReleases, seasonLabel, sFlags.year, season, "tv")
		top := quality.SortAndTop(exact, prefs, s.cfg.ShowTopN)
		if len(top) == 0 {
			fmt.Fprintf(os.Stderr, "  No matching releases.\n")
			continue
		}

		sel := process.NewSelector(seasonLabel, top)
		chosen, err := sel.Run()
		if err != nil {
			return nil
		}
		if len(chosen) == 0 {
			fmt.Fprintf(os.Stderr, "  Skipped %s.\n", seasonLabel)
			continue
		}

		category := s.cfg.Downloader.Categories.TV
		if category == "" {
			category = "TV"
		}
		if isAnime {
			if cat := s.cfg.Downloader.Categories.Anime; cat != "" {
				category = cat
			}
		}
		if err := s.downloadReleases(ctx, chosen, category); err != nil {
			return err
		}

		if tvdbID == 0 && !s.noLibrary && s.sonarr != nil {
			tid, name, yr, eErr := s.enrichTV(ctx, query)
			if eErr != nil {
				s.log.Warn().Err(eErr).Msg("enriching TV series")
			} else {
				tvdbID = tid
				seriesName = name
				seriesYear = yr
			}
		}
	}

	if tvdbID > 0 {
		sonarrSeasons := make([]library.SonarrSeason, len(seasons))
		for i, sNum := range seasons {
			sonarrSeasons[i] = library.SonarrSeason{SeasonNumber: sNum, Monitored: true}
		}
		if err := s.addToSonarr(ctx, tvdbID, seriesName, seriesYear, sonarrSeasons); err != nil {
			s.log.Warn().Err(err).Msg("adding to Sonarr")
		}
	} else if !s.noLibrary && s.sonarr != nil {
		fmt.Fprintf(os.Stderr, "  Could not identify series on TMDB. Skipping Sonarr add.\n")
	}

	return nil
}

// ─── Music search ─────────────────────────────────────────

func (s *searcher) searchMusic(ctx context.Context, query string) error {
	fmt.Fprintf(os.Stderr, "  Searching Prowlarr for music: %s\n", query)
	releases, err := s.prowl.SearchMusic(ctx, query)
	if err != nil {
		return fmt.Errorf("prowlarr search: %w", err)
	}
	if len(releases) == 0 {
		fmt.Fprintf(os.Stderr, "  No results found.\n")
		return nil
	}

	for i := range releases {
		mp := quality.ParseMusicRelease(releases[i].RawTitle)
		releases[i].Source = mp.Source
		releases[i].Codec = mp.Codec
	}

	prefs := quality.MusicQualityPrefs{
		FormatPriority:  s.cfg.Quality.Music.FormatPriority,
		BitratePriority: s.cfg.Quality.Music.BitratePriority,
		MinSeeders:      s.cfg.MinSeeders,
		PreferredGroups: s.cfg.PreferredGroups,
	}
	top := quality.SortMusicTop(releases, prefs, s.cfg.ShowTopN)
	if len(top) == 0 {
		fmt.Fprintf(os.Stderr, "  No matching releases found.\n")
		return nil
	}

	sel := process.NewSelector(query, top)
	chosen, err := sel.Run()
	if err != nil {
		return nil
	}
	if len(chosen) == 0 {
		fmt.Fprintf(os.Stderr, "  Skipped.\n")
		return nil
	}

	category := s.cfg.Downloader.Categories.Music
	if category == "" {
		category = "Music"
	}
	if err := s.downloadReleases(ctx, chosen, category); err != nil {
		return err
	}

	if s.noLibrary || s.lidarr == nil {
		fmt.Fprintf(os.Stderr, "  Downloaded. Skipping Lidarr add.\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "  Lidarr add requires MusicBrainz ID. Use 'wmdl add music' to add to Lidarr.\n")
	return nil
}

// ─── Book search ──────────────────────────────────────────

func (s *searcher) searchBook(ctx context.Context, query string) error {
	fmt.Fprintf(os.Stderr, "  Searching Prowlarr for books: %s\n", query)
	releases, err := s.prowl.Search(ctx, search.SearchParams{
		Query:      query,
		Type:       "search",
		Limit:      50,
		Categories: []int{search.CatBookEbook, search.CatAudioAudiobook},
	})
	if err != nil {
		return fmt.Errorf("prowlarr search: %w", err)
	}
	if len(releases) == 0 {
		fmt.Fprintf(os.Stderr, "  No results found.\n")
		return nil
	}

	prefs := quality.BookQualityPrefs{
		EbookFormatPriority:     s.cfg.Quality.Books.Ebooks.FormatPriority,
		AudiobookFormatPriority: s.cfg.Quality.Books.Audiobooks.FormatPriority,
		MinSeeders:              s.cfg.MinSeeders,
		PreferredGroups:         s.cfg.PreferredGroups,
	}

	bookReleases := make([]quality.ParsedBookRelease, 0, len(releases))
	for _, r := range releases {
		br := quality.ParseBookRelease(r.RawTitle)
		br.ParsedRelease = r
		bookReleases = append(bookReleases, br)
	}
	top := quality.SortBookTop(bookReleases, prefs, s.cfg.ShowTopN)
	if len(top) == 0 {
		fmt.Fprintf(os.Stderr, "  No matching book releases found.\n")
		return nil
	}

	plainTop := make([]quality.ParsedRelease, len(top))
	for i, br := range top {
		plainTop[i] = br.ParsedRelease
	}
	sel := process.NewSelector(query, plainTop)
	chosen, err := sel.Run()
	if err != nil {
		return nil
	}
	if len(chosen) == 0 {
		fmt.Fprintf(os.Stderr, "  Skipped.\n")
		return nil
	}

	category := s.cfg.Downloader.Categories.Ebooks
	if category == "" {
		category = "Ebooks"
	}
	if err := s.downloadReleases(ctx, chosen, category); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "  Downloaded. Book library manager integration is not yet available.\n")
	return nil
}

// ─── Shared helpers ───────────────────────────────────────

func (s *searcher) downloadReleases(ctx context.Context, chosen []quality.ParsedRelease, category string) error {
	for _, release := range chosen {
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		if uri == "" {
			s.log.Warn().Str("title", release.RawTitle).Msg("no download URL or magnet URI")
			continue
		}
		if s.grab {
			if err := s.prowl.Grab(ctx, release.IndexerID, release.Guid); err != nil {
				s.log.Warn().Err(err).Str("title", release.RawTitle).Msg("prowlarr grab failed")
			} else {
				fmt.Fprintf(os.Stderr, "  ✓ Grabbed via Prowlarr: %s\n", release.RawTitle)
			}
			continue
		}
		tid, err := s.dl.AddTorrent(ctx, uri, download.WithCategory(category))
		if err != nil {
			s.log.Warn().Err(err).Str("title", release.RawTitle).Msg("download failed")
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ Added to %s: %s (torrent ID: %s)\n", s.cfg.Downloader.Type, release.RawTitle, tid)
	}
	return nil
}

func (s *searcher) enrichMovie(ctx context.Context, query string) (tmdbID int, title string, year int, err error) {
	if s.tmdb == nil {
		return 0, "", 0, fmt.Errorf("TMDB not configured")
	}
	enrich, err := s.tmdb.Enrich(ctx, query, sFlags.year)
	if err != nil {
		return 0, "", 0, err
	}
	if enrich == nil {
		return 0, "", 0, nil
	}
	return enrich.TMDBID, enrich.Title, enrich.Year, nil
}

func (s *searcher) enrichTV(ctx context.Context, query string) (tvdbID int, title string, year int, err error) {
	if s.tmdb == nil {
		return 0, "", 0, fmt.Errorf("TMDB not configured")
	}
	enrich, err := s.tmdb.Enrich(ctx, query, sFlags.year)
	if err != nil {
		return 0, "", 0, err
	}
	if enrich == nil {
		return 0, "", 0, nil
	}
	return enrich.TVDBID, enrich.Title, enrich.Year, nil
}

func (s *searcher) addToRadarr(ctx context.Context, tmdbID int, title string, year int) error {
	existing, err := s.radarr.Exists(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("checking Radarr: %w", err)
	}
	if existing != nil {
		fmt.Fprintf(os.Stderr, "  Already in Radarr: %s (%d)\n", existing.Title, existing.Year)
		return nil
	}

	lookup, err := s.radarr.Lookup(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("looking up in Radarr: %w", err)
	}
	if lookup != nil {
		fmt.Fprintf(os.Stderr, "  Already known to Radarr: %s (%d)\n", lookup.Title, lookup.Year)
		return nil
	}

	if !s.auto && !promptYesNo(ctx, fmt.Sprintf("  Add %s (%d) to Radarr?", title, year)) {
		fmt.Fprintf(os.Stderr, "  Skipped Radarr add.\n")
		return nil
	}

	profileID, err := resolveRadarrProfileID(ctx, s.radarr, s.cfg.Library.Radarr.QualityProfile)
	if err != nil {
		return fmt.Errorf("resolving quality profile: %w", err)
	}

	rootFolder := s.cfg.Library.Radarr.RootFolder
	if rootFolder == "" {
		folders, fErr := s.radarr.GetRootFolders(ctx)
		if fErr != nil {
			return fmt.Errorf("getting root folders: %w", fErr)
		}
		if len(folders) > 0 {
			rootFolder = folders[0].Path
		}
	}

	_, err = s.radarr.Add(ctx, tmdbID, title, year, library.AddMovieOptions{
		Monitored:           false,
		MinimumAvailability: "announced",
		QualityProfileID:    profileID,
		RootFolderPath:      rootFolder,
		SearchNow:           false,
	})
	if err != nil {
		return fmt.Errorf("adding to Radarr: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Added to Radarr: %s (%d)\n", title, year)
	return nil
}

func (s *searcher) addToSonarr(ctx context.Context, tvdbID int, title string, year int, seasons []library.SonarrSeason) error {
	if tvdbID == 0 {
		return nil
	}

	existing, err := s.sonarr.Exists(ctx, tvdbID)
	if err != nil {
		return fmt.Errorf("checking Sonarr: %w", err)
	}
	if existing != nil {
		fmt.Fprintf(os.Stderr, "  Already in Sonarr: %s (%d)\n", existing.Title, existing.Year)
		return nil
	}

	if !s.auto && !promptYesNo(ctx, fmt.Sprintf("  Add %s (%d) to Sonarr?", title, year)) {
		fmt.Fprintf(os.Stderr, "  Skipped Sonarr add.\n")
		return nil
	}

	profileID, err := resolveSonarrProfileID(ctx, s.sonarr, s.cfg.Library.Sonarr.QualityProfile)
	if err != nil {
		return fmt.Errorf("resolving quality profile: %w", err)
	}

	rootFolder := s.cfg.Library.Sonarr.RootFolder
	if rootFolder == "" {
		folders, fErr := s.sonarr.GetRootFolders(ctx)
		if fErr != nil {
			return fmt.Errorf("getting root folders: %w", fErr)
		}
		if len(folders) > 0 {
			rootFolder = folders[0].Path
		}
	}

	_, err = s.sonarr.Add(ctx, tvdbID, title, year, library.AddSeriesOptions{
		Monitored:        true,
		SeasonFolder:     true,
		QualityProfileID: profileID,
		RootFolderPath:   rootFolder,
		Seasons:          seasons,
		SearchForMissing: false,
	})
	if err != nil {
		return fmt.Errorf("adding to Sonarr: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Added to Sonarr: %s (%d)\n", title, year)
	return nil
}

func (s *searcher) fetchCompletedSeasons(ctx context.Context, query string) ([]int, error) {
	res, err := s.tmdb.SearchTV(ctx, query, sFlags.year)
	if err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return nil, fmt.Errorf("no TV series found on TMDB for %q", query)
	}

	tmdbID := res.Results[0].ID
	details, err := s.tmdb.GetTVDetails(ctx, tmdbID)
	if err != nil {
		return nil, err
	}

	var seasons []int
	for _, season := range details.Seasons {
		if season.SeasonNumber > 0 && season.AirDatePassed() {
			seasons = append(seasons, season.SeasonNumber)
		}
	}
	return seasons, nil
}

// ─── Phase 3: Collection gap check ────────────────────────

func (s *searcher) checkCollectionGaps(ctx context.Context, tmdbID int) error {
	details, err := s.tmdb.GetMovieDetails(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("getting TMDB details: %w", err)
	}
	if details.BelongsToCollection == nil {
		return nil
	}

	coll := details.BelongsToCollection
	fmt.Fprintf(os.Stderr, "\n  This movie is part of \"%s\" collection.\n", coll.Name)

	collections, err := s.radarr.GetCollections(ctx)
	if err != nil {
		return fmt.Errorf("fetching Radarr collections: %w", err)
	}

	var matching *library.RadarrCollection
	for i := range collections {
		if collections[i].TMDBID == coll.ID {
			matching = &collections[i]
			break
		}
	}
	if matching == nil {
		return nil
	}

	var missing []string
	var missingIDs []int
	movieTitles := make(map[int]string)
	for _, m := range matching.Movies {
		movieTitles[m.TMDBID] = m.Title
		if m.TMDBID == tmdbID {
			continue
		}
		if m.Status != "" && m.Status != "released" {
			continue
		}
		existing, _ := s.radarr.Exists(ctx, m.TMDBID)
		if existing != nil && existing.HasFile {
			continue
		}
		missing = append(missing, m.Title)
		missingIDs = append(missingIDs, m.TMDBID)
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "  %d missing movie(s): %s\n", len(missing), strings.Join(missing, ", "))
	if !s.auto && !promptYesNo(ctx, "  Search and add missing movies?") {
		return nil
	}

	for i := range missingIDs {
		mTitle := missing[i]
		fmt.Fprintf(os.Stderr, "\n  [%d/%d] %s\n", i+1, len(missing), mTitle)

		enrich, err := s.tmdb.Enrich(ctx, mTitle, 0)
		if err != nil || enrich == nil {
			s.log.Warn().Err(err).Str("title", mTitle).Msg("TMDB lookup failed")
			continue
		}

		releases, err := s.prowl.SearchMovies(ctx, mTitle)
		if err != nil {
			s.log.Warn().Err(err).Str("title", mTitle).Msg("Prowlarr search failed")
			continue
		}
		if len(releases) == 0 {
			fmt.Fprintf(os.Stderr, "  No results.\n")
			continue
		}

		exact, _ := quality.PartitionReleases(releases, mTitle, enrich.Year, 0, "movie")
		prefs := qualityPrefs(s.cfg, "movie", s.prowl.PreferredIndexerID(search.CatMovie))
		top := quality.SortAndTop(exact, prefs, s.cfg.ShowTopN)
		if len(top) == 0 {
			fmt.Fprintf(os.Stderr, "  No matching releases.\n")
			continue
		}

		sel := process.NewSelector(mTitle, top)
		chosen, selErr := sel.Run()
		if selErr != nil {
			return nil
		}
		if len(chosen) == 0 {
			fmt.Fprintf(os.Stderr, "  Skipped %s.\n", mTitle)
			continue
		}

		category := s.cfg.Downloader.Categories.Movies
		if category == "" {
			category = "Movies"
		}
		_ = s.downloadReleases(ctx, chosen, category)

		if err := s.addToRadarr(ctx, enrich.TMDBID, enrich.Title, enrich.Year); err != nil {
			s.log.Warn().Err(err).Str("title", mTitle).Msg("adding collection movie to Radarr")
		}
	}
	return nil
}

// ─── Quality helpers ──────────────────────────────────────

func qualityPrefs(cfg *config.Config, mt string, preferredID int) quality.QualityPrefs {
	var qc config.MediaQualityConfig
	switch mt {
	case "tv":
		qc = cfg.Quality.TV
	case "anime":
		qc = cfg.Quality.Anime
	default:
		qc = cfg.Quality.Movies
	}

	res := 1080
	switch qc.Resolution {
	case "2160p", "4k", "uhd":
		res = 2160
	case "1080p":
		res = 1080
	case "720p":
		res = 720
	}

	return quality.QualityPrefs{
		TargetResolution:   res,
		PreferHDR:          qc.PreferHDR,
		SourcePriority:     qc.SourcePriority,
		CodecPriority:      qc.CodecPriority,
		PreferredGroups:    cfg.PreferredGroups,
		MinSeeders:         cfg.MinSeeders,
		PreferredIndexerID: preferredID,
	}
}

func resolveRadarrProfileID(ctx context.Context, r *library.RadarrClient, name string) (int, error) {
	profiles, err := r.GetQualityProfiles(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range profiles {
		if p.Name == name {
			return p.ID, nil
		}
	}
	if len(profiles) > 0 {
		return profiles[0].ID, nil
	}
	return 1, nil
}

func resolveSonarrProfileID(ctx context.Context, s *library.SonarrClient, name string) (int, error) {
	profiles, err := s.GetQualityProfiles(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range profiles {
		if p.Name == name {
			return p.ID, nil
		}
	}
	if len(profiles) > 0 {
		return profiles[0].ID, nil
	}
	return 1, nil
}
