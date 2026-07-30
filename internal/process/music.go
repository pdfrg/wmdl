package process

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/library"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

type MusicSearchResult struct {
	Event db.EventWithAlbumRelease
	Top   []quality.ParsedRelease
	Error error
}

type MusicAlbumResult struct {
	Event      db.EventWithAlbumRelease
	Downloaded bool
	Trial      bool
}

func musicSearchQueries(artist, album string, year int) []string {
	artist = quality.StripAccents(artist)
	album = quality.StripAccents(album)
	cleanArtist := quality.CleanArtist(artist)
	cleanAlbum := quality.CleanAlbum(album)

	raw := []string{
		fmt.Sprintf("%s %s %d", artist, album, year),
		fmt.Sprintf("%s %s", artist, album),
		fmt.Sprintf("%s %d", artist, year),
		fmt.Sprintf("%s %s %d", cleanArtist, cleanAlbum, year),
		fmt.Sprintf("%s %s", cleanArtist, cleanAlbum),
		fmt.Sprintf("%s %d", cleanAlbum, year),
		cleanAlbum,
	}

	seen := make(map[string]bool)
	var queries []string
	for _, q := range raw {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		queries = append(queries, q)
	}
	return queries
}

func (e *Executor) searchMusicRelease(ctx context.Context, artist, album string, year int) ([]quality.ParsedRelease, error) {
	queries := musicSearchQueries(artist, album, year)
	preferredID := e.prowl.PreferredIndexerID(search.CatMusic)
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	if preferredID > 0 {
		name := e.prowl.GetIndexerName(ctx, preferredID)
		e.log.Info().Str("name", name).Int("id", preferredID).Msg("preferred indexer (music)")

		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] preferred: %s", i+1, numTiers, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       "music",
				IndexerID:  preferredID,
				Limit:      50,
				Categories: []int{search.CatMusic},
			})
			if err != nil {
				e.log.Warn().Err(err).Str("query", q).Msg("preferred indexer search failed")
				break
			}
			exact, fuzzy := quality.PartitionMusicReleases(results, artist, album)
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= e.cfg.ShowTopN {
				return exactPool, nil
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	for i, q := range queries {
		e.log.Info().Msgf("[%d/%d] searching all: %s", i+1, numTiers, q)
		results, err := e.prowl.SearchMusic(ctx, q)
		if err != nil {
			e.log.Warn().Err(err).Str("query", q).Msg("prowlarr music search failed")
			continue
		}
		exact, fuzzy := quality.PartitionMusicReleases(results, artist, album)
		exactPool = mergeReleases(exactPool, exact)
		fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
		e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
		if len(exactPool) >= e.cfg.ShowTopN {
			return exactPool, nil
		}
	}

	if len(exactPool) > 0 || len(fuzzyPool) > 0 {
		result := exactPool
		if n := e.cfg.ShowTopN - len(exactPool); n > 0 && len(fuzzyPool) > 0 {
			if n > len(fuzzyPool) {
				n = len(fuzzyPool)
			}
			result = append(result, fuzzyPool[:n]...)
		}
		return result, nil
	}
	return nil, nil
}

func (e *Executor) SearchMusicRelease(ctx context.Context, ae db.EventWithAlbumRelease) (*MusicSearchResult, error) {
	e.log.Info().Str("artist", ae.Release.ArtistName).Str("album", ae.Release.Title).Int("year", ae.Release.Year).Msg("searching music")

	releases, err := e.searchMusicRelease(ctx, ae.Release.ArtistName, ae.Release.Title, ae.Release.Year)
	if err != nil {
		return nil, fmt.Errorf("searching music: %w", err)
	}

	if len(releases) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s - %s", ae.Release.ArtistName, ae.Release.Title))
		return &MusicSearchResult{Event: ae}, nil
	}

	for i := range releases {
		musicParsed := quality.ParseMusicRelease(releases[i].RawTitle)
		releases[i].Source = musicParsed.Source
		releases[i].Codec = musicParsed.Codec
	}

	prefs := quality.MusicQualityPrefs{
		FormatPriority:     e.cfg.Quality.Music.FormatPriority,
		BitratePriority:    e.cfg.Quality.Music.BitratePriority,
		MinSeeders:         e.cfg.MinSeeders,
		PreferredGroups:    e.cfg.PreferredGroups,
		PreferredIndexerID: e.prowl.PreferredIndexerID(search.CatMusic),
	}
	top := quality.SortMusicTop(releases, prefs, e.cfg.ShowTopN)

	return &MusicSearchResult{Event: ae, Top: top}, nil
}

func (e *Executor) SearchMusicAll(ctx context.Context, events []db.EventWithAlbumRelease) []*MusicSearchResult {
	e.log.Info().Msgf("Searching %d music album(s)...", len(events))
	results := make([]*MusicSearchResult, 0, len(events))
	for i, ae := range events {
		e.log.Info().Str("album", ae.Release.Title).Str("artist", ae.Release.ArtistName).Msgf("[%d/%d] searching", i+1, len(events))
		sr, err := e.SearchMusicRelease(ctx, ae)
		if err != nil {
			e.log.Warn().Err(err).Str("album", ae.Release.Title).Str("artist", ae.Release.ArtistName).Msg("error searching music")
			continue
		}
		results = append(results, sr)
	}
	return results
}

func (e *Executor) PickMusicResults(ctx context.Context, results []*MusicSearchResult) []MusicAlbumResult {
	var albumResults []MusicAlbumResult
	for _, sr := range results {
		result := e.PickMusicAlbum(ctx, sr)
		if result != nil {
			albumResults = append(albumResults, *result)
		}
	}
	return albumResults
}

func (e *Executor) ProcessMusicAlbumsInteractive(ctx context.Context, events []db.EventWithAlbumRelease) []MusicAlbumResult {
	e.log.Info().Msgf("Processing %d music album(s) interactively...", len(events))
	var albumResults []MusicAlbumResult
	for _, ae := range events {
		select {
		case <-ctx.Done():
			return albumResults
		default:
		}
		e.log.Info().Str("artist", ae.Release.ArtistName).Str("album", ae.Release.Title).Msgf("processing album")
		sr, err := e.SearchMusicRelease(ctx, ae)
		if err != nil {
			e.log.Warn().Err(err).Str("album", ae.Release.Title).Str("artist", ae.Release.ArtistName).Msg("error searching music")
			continue
		}
		result := e.PickMusicAlbum(ctx, sr)
		if result != nil {
			albumResults = append(albumResults, *result)
		}
	}
	return albumResults
}

func (e *Executor) ProcessMusicAlbum(ctx context.Context, ae db.EventWithAlbumRelease) (*MusicAlbumResult, error) {
	sr, err := e.SearchMusicRelease(ctx, ae)
	if err != nil {
		return nil, err
	}
	return e.PickMusicAlbum(ctx, sr), nil
}

func (e *Executor) PickMusicAlbum(ctx context.Context, sr *MusicSearchResult) *MusicAlbumResult {
	ae := sr.Event
	if len(sr.Top) == 0 {
		e.log.Info().Str("artist", ae.Release.ArtistName).Str("album", ae.Release.Title).Msg("no music results found")
		return &MusicAlbumResult{Event: ae}
	}

	selLabel := ae.Release.ArtistName + " - " + ae.Release.Title
	sel := NewSelector(selLabel, sr.Top)
	chosen, err := sel.Run()
	if err != nil {
		return &MusicAlbumResult{Event: ae}
	}
	if len(chosen) == 0 {
		label := ae.Release.ArtistName + " - " + ae.Release.Title
		e.Skipped = append(e.Skipped, label)
		e.handleSkipLibraryMusic(ctx, ae)
		return &MusicAlbumResult{Event: ae}
	}

	trial := false
	if e.lidarr != nil {
		mode := e.cfg.MediaTypeMode(model.MediaTypeMusic)
		if mode == config.ProcessModeFull {
			artistMBID := ae.Release.ArtistMBID
			if artistMBID != "" && e.LidarrArtistExists(ctx, artistMBID) {
				trial = !PromptYesNo(ctx, fmt.Sprintf("  %s is tracked in Lidarr. Add this release to Lidarr?", ae.Release.ArtistName))
			} else {
				trial = !PromptYesNo(ctx, fmt.Sprintf("  Add %s to Lidarr?", ae.Release.ArtistName))
			}
		}
	}

	category := e.musicCategory(trial)
	var releaseEventID int64
	if ae.Event != nil {
		releaseEventID = ae.Event.ID
	}

	for _, release := range chosen {
		e.log.Info().Str("release", release.RawTitle).Str("artist", ae.Release.ArtistName).Str("album", ae.Release.Title).Int("score", release.Score).Msg("selected music release")
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		if uri != "" {
			tid, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category))
			if err != nil {
				e.log.Warn().Err(err).Msg("music add failed")
			} else {
				e.log.Info().Str("client", e.cfg.Downloader.Type).Str("category", category).Str("torrent_id", tid).Msg("added music to download client")
				_ = tid
			}
		}

		_ = e.db.UpdateAlbumReleaseEventStatus(ctx, releaseEventID, model.StatusDownloaded)
	}

	return &MusicAlbumResult{Event: ae, Downloaded: true, Trial: trial}
}

func (e *Executor) ProcessMusicAlbumDecisions(ctx context.Context, results []MusicAlbumResult) {
	if len(results) == 0 || e.lidarr == nil {
		return
	}

	mode := e.cfg.MediaTypeMode(model.MediaTypeMusic)
	autoConfirm := mode == config.ProcessModeAuto || mode == config.ProcessModeYolo

	fmt.Fprintln(os.Stderr, "\n── Lidarr decisions ──")

	type retryAction int
	const (
		retryActionSkip retryAction = iota
		retryActionRetry
		retryActionQuit
	)

	promptRetry := func(label string, err error) retryAction {
		fmt.Fprintf(os.Stderr, "  %s error: %v\n", label, err)
		for {
			fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip this item  [q] quit pipeline\n")
			fmt.Fprintf(os.Stderr, "  Choose: ")
			ch := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				ch <- scanner.Text()
			}()
			select {
			case ans := <-ch:
				switch strings.ToLower(strings.TrimSpace(ans)) {
				case "r", "retry":
					return retryActionRetry
				case "s", "skip":
					return retryActionSkip
				case "q", "quit":
					return retryActionQuit
				}
			case <-ctx.Done():
				return retryActionQuit
			}
		}
	}

	type albumDecision struct {
		evt        db.EventWithAlbumRelease
		artistMbid string
	}

	var decisions []albumDecision

	for _, r := range results {
		if !r.Downloaded {
			continue
		}
		if r.Trial {
			e.log.Info().Str("artist", r.Event.Release.ArtistName).Str("album", r.Event.Release.Title).Msg("trial album, skipping Lidarr add")
			continue
		}
		ae := r.Event
		artistMbid := ae.Release.ArtistMBID
		if artistMbid == "" {
			continue
		}

		existing, err := e.lidarr.GetArtist(ctx, artistMbid)
		if err != nil {
			e.log.Warn().Err(err).Str("artist", ae.Release.ArtistName).Msg("lidarr check error")
			continue
		}
		if existing != nil {
			e.log.Info().Str("artist", ae.Release.ArtistName).Int("lidarr_id", existing.ID).Msg("artist already in Lidarr")
			continue
		}

		e.log.Info().Msgf("Add to Lidarr? %s — %s (%d)", ae.Release.ArtistName, ae.Release.Title, ae.Release.Year)
		if autoConfirm || PromptYesNo(ctx, "  Add artist to Lidarr?") {
			decisions = append(decisions, albumDecision{
				evt:        ae,
				artistMbid: artistMbid,
			})
		}
	}

	if len(decisions) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\n── Executing Lidarr adds ──")

decisionsLoop:
	for _, d := range decisions {
		ae := d.evt
	addRetry:
		e.log.Info().Str("artist", ae.Release.ArtistName).Msg("adding artist to Lidarr")

		qualProfileID, err := e.lidarr.ResolveQualityProfileID(ctx, e.cfg.Library.Lidarr.QualityProfile)
		if err != nil {
			e.log.Warn().Err(err).Msg("resolving quality profile")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		metaProfileID, err := e.lidarr.ResolveMetadataProfileID(ctx, e.cfg.Library.Lidarr.MetadataProfile)
		if err != nil {
			e.log.Warn().Err(err).Msg("resolving metadata profile")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		rootFolder := e.cfg.Library.Lidarr.RootFolder
		if rootFolder == "" {
			e.log.Warn().Msg("lidarr root_folder not configured, skipping")
			continue
		}

		monitor := e.cfg.Library.Lidarr.Monitor
		if monitor == "" {
			monitor = "all"
		}

		added, err := e.lidarr.AddArtist(ctx, d.artistMbid, ae.Release.ArtistName, library.AddArtistOptions{
			Monitored:         true,
			MonitorNewAlbums:  e.cfg.Library.Lidarr.MonitorNewAlbums,
			QualityProfileID:  qualProfileID,
			MetadataProfileID: metaProfileID,
			RootFolderPath:    rootFolder,
			Monitor:           monitor,
			SearchNow:         false,
		})
		if err != nil {
			e.log.Warn().Err(err).Str("artist", ae.Release.ArtistName).Msg("failed adding to Lidarr")
			switch promptRetry("Lidarr add", err) {
			case retryActionRetry:
				goto addRetry
			case retryActionQuit:
				break decisionsLoop
			}
			continue
		}

		_ = e.db.SetSetting(ctx, fmt.Sprintf("lidarr_artist_%s", d.artistMbid), fmt.Sprintf("%d", added.ID))
		e.log.Info().Int("lidarr_id", added.ID).Str("artist", ae.Release.ArtistName).Msg("added artist to Lidarr")
	}
}

func formatOverview(text string, maxWidth int) []string {
	var lines []string
	for _, w := range strings.Fields(text) {
		if len(lines) == 0 || len(lines[len(lines)-1])+len(w)+1 > maxWidth {
			lines = append(lines, w)
		} else {
			lines[len(lines)-1] += " " + w
		}
	}
	if len(lines) > 5 {
		lines = lines[:5]
		lines[4] += " ..."
	}
	return lines
}

// handleSkipLibraryMusic is called when a music album is skipped during
// interactive mode but the user may still want to add the artist to Lidarr.
func (e *Executor) handleSkipLibraryMusic(ctx context.Context, ae db.EventWithAlbumRelease) {
	if e.lidarr == nil {
		return
	}
	mode := e.cfg.MediaTypeMode(model.MediaTypeMusic)
	if mode != config.ProcessModeFull {
		return
	}
	artistMbid := ae.Release.ArtistMBID
	if artistMbid == "" {
		return
	}
	existing, err := e.lidarr.GetArtist(ctx, artistMbid)
	if err != nil {
		e.log.Warn().Err(err).Str("artist", ae.Release.ArtistName).Msg("lidarr check error during skip")
		return
	}
	if existing != nil {
		e.log.Info().Str("artist", ae.Release.ArtistName).Int("lidarr_id", existing.ID).Msg("artist already in Lidarr")
		return
	}
	if !PromptYesNo(ctx, fmt.Sprintf("  Add %s to Lidarr anyway?", ae.Release.ArtistName)) {
		return
	}
	qualProfileID, err := e.lidarr.ResolveQualityProfileID(ctx, e.cfg.Library.Lidarr.QualityProfile)
	if err != nil {
		e.log.Warn().Err(err).Msg("resolving quality profile for Lidarr")
		return
	}
	metaProfileID, err := e.lidarr.ResolveMetadataProfileID(ctx, e.cfg.Library.Lidarr.MetadataProfile)
	if err != nil {
		e.log.Warn().Err(err).Msg("resolving metadata profile for Lidarr")
		return
	}
	rootFolder := e.cfg.Library.Lidarr.RootFolder
	if rootFolder == "" {
		e.log.Warn().Msg("lidarr root_folder not configured, skipping")
		return
	}
	monitor := e.cfg.Library.Lidarr.Monitor
	if monitor == "" {
		monitor = "all"
	}
	added, err := e.lidarr.AddArtist(ctx, artistMbid, ae.Release.ArtistName, library.AddArtistOptions{
		Monitored:         true,
		MonitorNewAlbums:  e.cfg.Library.Lidarr.MonitorNewAlbums,
		QualityProfileID:  qualProfileID,
		MetadataProfileID: metaProfileID,
		RootFolderPath:    rootFolder,
		Monitor:           monitor,
		SearchNow:         false,
	})
	if err != nil {
		e.log.Warn().Err(err).Str("artist", ae.Release.ArtistName).Msg("failed adding to Lidarr after skip")
		return
	}
	_ = e.db.SetSetting(ctx, fmt.Sprintf("lidarr_artist_%s", artistMbid), fmt.Sprintf("%d", added.ID))
	_ = e.db.UpdateAlbumReleaseEventStatus(ctx, ae.Event.ID, model.StatusDownloaded)
	e.log.Info().Int("lidarr_id", added.ID).Str("artist", ae.Release.ArtistName).Msg("added artist to Lidarr after skip (monitored, RSS will pick up)")
}

func (e *Executor) musicCategory(trial bool) string {
	if trial {
		cat := e.cfg.Downloader.Categories.MusicTrial
		if cat != "" {
			return cat
		}
	}
	cat := e.cfg.Downloader.Categories.Music
	if cat == "" {
		cat = "Music"
	}
	return cat
}

func (e *Executor) processMusicBatchItem(ctx context.Context, item *BatchItem) *MusicAlbumResult {
	sr := item.MusicResult
	if sr == nil {
		return nil
	}
	ae := sr.Event

	category := e.musicCategory(item.Trial)
	var releaseEventID int64
	if ae.Event != nil {
		releaseEventID = ae.Event.ID
	}

	for _, release := range item.Selected {
		e.log.Info().Str("release", release.RawTitle).Str("artist", ae.Release.ArtistName).Str("album", ae.Release.Title).Int("score", release.Score).Msg("selected music release")
		uri := release.DownloadURL
		if uri == "" {
			uri = release.MagnetURL
		}
		if uri != "" {
			tid, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category))
			if err != nil {
				e.log.Warn().Err(err).Msg("music add failed")
			} else {
				e.log.Info().Str("client", e.cfg.Downloader.Type).Str("category", category).Str("torrent_id", tid).Msg("added music to download client")
			}
		}

		_ = e.db.UpdateAlbumReleaseEventStatus(ctx, releaseEventID, model.StatusDownloaded)
	}

	return &MusicAlbumResult{Event: ae, Downloaded: true, Trial: item.Trial}
}
