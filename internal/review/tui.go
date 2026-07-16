package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

// Strip metadata parentheticals before comparing scraped vs TMDB titles,
// so "(season 3)" differences don't trigger false mismatch warnings.
var reviewMetaParen = regexp.MustCompile(`(?i)\s*\((season\s+\d+|complete\s+.*|series\s+\d+|vol\..*)\)`)

type decision int

const (
	decisionNone decision = iota
	decisionApproved
	decisionRejected
)

func decisionForStatus(s model.ReleaseStatus) decision {
	switch s {
	case model.StatusApproved, model.StatusDownloaded:
		return decisionApproved
	case model.StatusRejected:
		return decisionRejected
	default:
		return decisionNone
	}
}

type phase int

const (
	phaseReview phase = iota
	phaseConfirm
)

type itemState struct {
	event      db.EventWithTitle  // for movies/TV
	albumEvent *db.EventWithAlbum // for music (nil for movies/TV)
	bookEvent  *db.EventWithBook  // for books (nil for movies/TV/music)
	decision   decision
}

func (it *itemState) mediaType() model.MediaType {
	if it.bookEvent != nil {
		return model.MediaTypeBook
	}
	if it.albumEvent != nil {
		return model.MediaTypeMusic
	}
	return it.event.Title.MediaType
}

func (it *itemState) displayTitle() string {
	if it.bookEvent != nil {
		return fmt.Sprintf("%s — %s", it.bookEvent.Author.Name, it.bookEvent.Book.Title)
	}
	if it.albumEvent != nil {
		return fmt.Sprintf("%s - %s", it.albumEvent.Artist.Name, it.albumEvent.Album.Title)
	}
	return it.event.Title.Title
}

type libStatus int

const (
	libNone    libStatus = iota
	libFull              // green — all files present
	libPartial           // yellow — some files missing or 0 files
)

type libInfo struct {
	label  string
	status libStatus
}

func (it *itemState) libraryInfo(dbCache map[string]*db.LibraryCache) libInfo {
	if it.bookEvent != nil {
		be := it.bookEvent
		// Check by HC ID (book-client-hc) first, then fall back to ISBN
		var c *db.LibraryCache
		if be.Book.HardcoverID > 0 {
			c = dbCache["book-client-hc:"+strconv.Itoa(be.Book.HardcoverID)]
		}
		if c == nil && be.Book.ISBN13 != "" {
			c = dbCache["book-client:"+be.Book.ISBN13]
		}
		if c != nil {
			return libInfo{
				label:  fmt.Sprintf("✓ LazyLibrarian — %s", c.ArrTitle),
				status: libFull,
			}
		}
		return libInfo{status: libNone}
	}
	if it.albumEvent != nil {
		artistKey := "lidarr:" + it.albumEvent.Artist.MBID
		albumKey := "lidarr-album:" + it.albumEvent.Album.MBID
		artistCache := dbCache[artistKey]
		albumCache := dbCache[albumKey]
		if artistCache != nil && albumCache != nil {
			return libInfo{
				label:  fmt.Sprintf("✓ Lidarr — %s [album in library]", it.albumEvent.Artist.Name),
				status: libFull,
			}
		}
		if artistCache != nil {
			return libInfo{
				label:  fmt.Sprintf("⚠ Lidarr — %s [artist in library, album not found]", it.albumEvent.Artist.Name),
				status: libPartial,
			}
		}
		return libInfo{status: libNone}
	}

	tl := it.event.Title
	if tl.TmdbID > 0 {
		if c := dbCache["radarr:"+strconv.Itoa(tl.TmdbID)]; c != nil {
			return libInfo{
				label:  fmt.Sprintf("✓ Radarr — %s", c.ArrTitle),
				status: libFull,
			}
		}
	}
	if tl.TvdbID > 0 {
		if c := dbCache["sonarr:"+strconv.Itoa(tl.TvdbID)]; c != nil {
			label := fmt.Sprintf("✓ Sonarr — %s", c.ArrTitle)
			status := libFull
			var series struct {
				Seasons []struct {
					SeasonNumber int `json:"seasonNumber"`
					Statistics   *struct {
						EpisodeFileCount  int `json:"episodeFileCount"`
						EpisodeCount      int `json:"episodeCount"`
						TotalEpisodeCount int `json:"totalEpisodeCount"`
					} `json:"statistics,omitempty"`
				} `json:"seasons"`
			}
			if err := json.Unmarshal([]byte(c.Details), &series); err == nil && len(series.Seasons) > 0 {
				var complete, partial []string
				anyMissing := false
				for _, s := range series.Seasons {
					if s.SeasonNumber == 0 {
						continue // skip specials
					}
					if s.Statistics == nil || s.Statistics.TotalEpisodeCount == 0 {
						continue
					}
					total := s.Statistics.TotalEpisodeCount
					aired := s.Statistics.EpisodeCount
					if aired <= 0 {
						aired = total
					}
					if s.Statistics.EpisodeFileCount >= total {
						complete = append(complete, strconv.Itoa(s.SeasonNumber))
					} else {
						partial = append(partial, fmt.Sprintf("S%d(%d/%d)", s.SeasonNumber, s.Statistics.EpisodeFileCount, total))
						if s.Statistics.EpisodeFileCount >= aired {
							// Up-to-date with aired episodes, waiting for future airings
						} else {
							anyMissing = true
						}
					}
				}
				if anyMissing {
					status = libPartial
				}
				if len(complete) > 0 || len(partial) > 0 {
					var parts []string
					if len(complete) > 0 {
						parts = append(parts, "S"+strings.Join(complete, ","))
					}
					parts = append(parts, partial...)
					label += " [" + strings.Join(parts, " ") + "]"
				} else if len(series.Seasons) > 0 {
					// Seasons exist in Sonarr but none have stats yet (e.g. newly added, 0 episodes)
					status = libPartial
				}
			}
			return libInfo{label: label, status: status}
		}
	}
	return libInfo{status: libNone}
}

func (it *itemState) collectionStr(dbCache map[string]*db.LibraryCache) string {
	if it.event.Title == nil || it.event.Title.MediaType != model.MediaTypeMovie || it.event.Title.CollectionID == 0 {
		return ""
	}
	key := "tmdb-collection:" + strconv.Itoa(it.event.Title.CollectionID)
	c := dbCache[key]
	if c == nil || c.Details == "" {
		return ""
	}
	var collData struct {
		Name   string `json:"name"`
		Movies []struct {
			TmdbID int    `json:"tmdb_id"`
			Title  string `json:"title"`
		} `json:"movies"`
	}
	if err := json.Unmarshal([]byte(c.Details), &collData); err != nil {
		return ""
	}
	if len(collData.Movies) == 0 {
		return ""
	}
	owned := 0
	for _, m := range collData.Movies {
		if dbCache["radarr:"+strconv.Itoa(m.TmdbID)] != nil {
			owned++
		}
	}
	return fmt.Sprintf("Collection: %s (%d/%d)", collData.Name, owned, len(collData.Movies))
}

func (it *itemState) bookSeriesStr(dbCache map[string]*db.LibraryCache) string {
	if it.bookEvent == nil || it.bookEvent.Book.SeriesID == "" {
		return ""
	}
	seriesID := it.bookEvent.Book.SeriesID
	seriesName := it.bookEvent.Book.SeriesName
	key := "book-series:" + seriesID
	c := dbCache[key]
	if c == nil || c.Details == "" {
		return ""
	}
	var members []db.BookSeriesMember
	if err := json.Unmarshal([]byte(c.Details), &members); err != nil {
		return ""
	}
	if len(members) == 0 {
		return ""
	}
	owned := 0
	for _, m := range members {
		if dbCache["book-client-hc:"+strconv.Itoa(m.HardcoverID)] != nil {
			owned++
		}
	}
	label := seriesName
	if label == "" {
		label = "Series"
	}
	return fmt.Sprintf("Series: %s (%d/%d)", label, owned, len(members))
}

type posterReadyMsg struct {
	img image.Image
	err error
}

type TUI struct {
	items             []itemState
	cursor            int
	phase             phase
	database          *db.DB
	width             int
	height            int
	approved          []db.EventWithTitle
	approvedBooks     int
	err               error
	posterImg         image.Image
	posterMode        PosterMode
	flashMsg          string
	vpConfirm         viewport.Model
	filter            model.MediaType // "" = all, "movie" or "tv"
	filtered          []int           // indices into items matching current filter
	pendingQuit       bool
	filterUndecided   bool
	year              int
	week              int
	prevAnimeWeek     string                      // most recent earlier week with anime events, for re-review hint
	libraryCache      map[string]*db.LibraryCache // key: "source:ext_id"
	defaultBookFormat model.BookFormat
}

func NewReviewTUIWithEvents(events []db.EventWithTitle, albumEvents []db.EventWithAlbum, bookEvents []db.EventWithBook, database *db.DB, posterMode string, year, week int, prevAnimeWeek string, defaultBookFormat model.BookFormat) (*TUI, error) {
	totalItems := len(events) + len(albumEvents) + len(bookEvents)
	if totalItems == 0 {
		return nil, fmt.Errorf("no events to review")
	}

	items := make([]itemState, totalItems)
	idx := 0

	mediaTypeOrder := map[model.MediaType]int{
		model.MediaTypeMovie: 0,
		model.MediaTypeTV:    1,
		model.MediaTypeAnime: 2,
	}
	slices.SortStableFunc(events, func(a, b db.EventWithTitle) int {
		return mediaTypeOrder[a.Title.MediaType] - mediaTypeOrder[b.Title.MediaType]
	})

	for _, e := range events {
		items[idx] = itemState{event: e, decision: decisionForStatus(e.Event.Status)}
		idx++
	}
	for _, a := range albumEvents {
		ae := a
		items[idx] = itemState{albumEvent: &ae, decision: decisionForStatus(ae.Event.Status)}
		idx++
	}
	for _, b := range bookEvents {
		be := b
		items[idx] = itemState{bookEvent: &be, decision: decisionForStatus(be.Event.Status)}
		idx++
	}

	detectTerminal()

	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(10)

	t := &TUI{
		items:             items,
		database:          database,
		height:            24,
		posterMode:        ParsePosterMode(posterMode),
		vpConfirm:         vp,
		year:              year,
		week:              week,
		prevAnimeWeek:     prevAnimeWeek,
		libraryCache:      buildLibraryCacheMap(database, events, albumEvents, bookEvents),
		defaultBookFormat: defaultBookFormat,
	}

	t.rebuildFiltered()
	return t, nil
}

func buildLibraryCacheMap(database *db.DB, events []db.EventWithTitle, albumEvents []db.EventWithAlbum, bookEvents []db.EventWithBook) map[string]*db.LibraryCache {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var lookups []struct{ Source, ExtID string }
	seen := make(map[string]bool)
	for _, e := range events {
		if e.Title.TmdbID > 0 {
			key := "radarr:" + strconv.Itoa(e.Title.TmdbID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"radarr", strconv.Itoa(e.Title.TmdbID)})
				seen[key] = true
			}
		}
		if e.Title.TvdbID > 0 {
			key := "sonarr:" + strconv.Itoa(e.Title.TvdbID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"sonarr", strconv.Itoa(e.Title.TvdbID)})
				seen[key] = true
			}
		}
	}
	for _, ae := range albumEvents {
		if ae.Artist.MBID != "" {
			key := "lidarr:" + ae.Artist.MBID
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"lidarr", ae.Artist.MBID})
				seen[key] = true
			}
		}
		if ae.Album.MBID != "" {
			key := "lidarr-album:" + ae.Album.MBID
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"lidarr-album", ae.Album.MBID})
				seen[key] = true
			}
		}
	}
	for _, be := range bookEvents {
		if be.Book.ISBN13 != "" {
			key := "book-client:" + be.Book.ISBN13
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client", be.Book.ISBN13})
				seen[key] = true
			}
		}
		if be.Book.ASIN != "" {
			key := "book-client:" + be.Book.ASIN
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client", be.Book.ASIN})
				seen[key] = true
			}
		}
		// Lookup by HC ID for library status check
		if be.Book.HardcoverID > 0 {
			key := "book-client-hc:" + strconv.Itoa(be.Book.HardcoverID)
			if !seen[key] {
				lookups = append(lookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(be.Book.HardcoverID)})
				seen[key] = true
			}
		}
	}
	result, err := database.GetLibraryCacheMap(ctx, lookups)
	if err != nil {
		return nil
	}

	// Phase 2: fetch collection data for events with CollectionID
	// and add Radarr lookups for collection member movies
	var collLookups []struct{ Source, ExtID string }
	for _, e := range events {
		if e.Title.CollectionID > 0 {
			key := "tmdb-collection:" + strconv.Itoa(e.Title.CollectionID)
			if !seen[key] {
				collLookups = append(collLookups, struct{ Source, ExtID string }{"tmdb-collection", strconv.Itoa(e.Title.CollectionID)})
				seen[key] = true
			}
		}
	}
	if len(collLookups) > 0 {
		collResult, err := database.GetLibraryCacheMap(ctx, collLookups)
		if err == nil {
			for k, v := range collResult {
				result[k] = v
			}
			// Parse collection data to add radarr lookups for member movies
			var memberLookups []struct{ Source, ExtID string }
			for _, e := range events {
				if e.Title.CollectionID == 0 {
					continue
				}
				ck := "tmdb-collection:" + strconv.Itoa(e.Title.CollectionID)
				c := collResult[ck]
				if c == nil || c.Details == "" {
					continue
				}
				var collData struct {
					Movies []struct {
						TmdbID int    `json:"tmdb_id"`
						Title  string `json:"title"`
						Year   int    `json:"year"`
					} `json:"movies"`
				}
				if err := json.Unmarshal([]byte(c.Details), &collData); err != nil {
					continue
				}
				for _, m := range collData.Movies {
					rk := "radarr:" + strconv.Itoa(m.TmdbID)
					if !seen[rk] {
						memberLookups = append(memberLookups, struct{ Source, ExtID string }{"radarr", strconv.Itoa(m.TmdbID)})
						seen[rk] = true
					}
				}
			}
			if len(memberLookups) > 0 {
				memberResult, err := database.GetLibraryCacheMap(ctx, memberLookups)
				if err == nil {
					for k, v := range memberResult {
						result[k] = v
					}
				}
			}
		}
	}

	// Phase 3: fetch book series member data and add book-client-hc lookups
	var bookSeriesLookups []struct{ Source, ExtID string }
	for _, be := range bookEvents {
		if be.Book.SeriesID != "" {
			key := "book-series:" + be.Book.SeriesID
			if !seen[key] {
				bookSeriesLookups = append(bookSeriesLookups, struct{ Source, ExtID string }{"book-series", be.Book.SeriesID})
				seen[key] = true
			}
		}
	}
	if len(bookSeriesLookups) > 0 {
		seriesResult, err := database.GetLibraryCacheMap(ctx, bookSeriesLookups)
		if err != nil {
			seriesResult = nil
		} else {
			for k, v := range seriesResult {
				result[k] = v
			}
		}

		var memberLookups []struct{ Source, ExtID string }

		// Phase 1: series with pre-warmed cache entries
		if seriesResult != nil {
			for _, be := range bookEvents {
				if be.Book.SeriesID == "" {
					continue
				}
				sk := "book-series:" + be.Book.SeriesID
				if seriesResult[sk] != nil && seriesResult[sk].Details != "" {
					var members []db.BookSeriesMember
					if err := json.Unmarshal([]byte(seriesResult[sk].Details), &members); err == nil {
						for _, m := range members {
							mk := "book-client-hc:" + strconv.Itoa(m.HardcoverID)
							if !seen[mk] {
								memberLookups = append(memberLookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(m.HardcoverID)})
								seen[mk] = true
							}
						}
					}
				}
			}
		}

		// Phase 2: series without pre-warmed cache entries — query local DB
		for _, be := range bookEvents {
			if be.Book.SeriesID == "" {
				continue
			}
			sk := "book-series:" + be.Book.SeriesID
			if result[sk] != nil {
				continue // already have cache entry
			}
			members, qErr := database.GetBookSeriesMembers(ctx, be.Book.SeriesID)
			if qErr != nil || len(members) == 0 {
				continue
			}
			mJSON, mErr := json.Marshal(members)
			if mErr == nil {
				result[sk] = &db.LibraryCache{
					Source:  "book-series",
					ExtID:   be.Book.SeriesID,
					Details: string(mJSON),
				}
			}
			for _, m := range members {
				mk := "book-client-hc:" + strconv.Itoa(m.HardcoverID)
				if !seen[mk] {
					memberLookups = append(memberLookups, struct{ Source, ExtID string }{"book-client-hc", strconv.Itoa(m.HardcoverID)})
					seen[mk] = true
				}
			}
		}

		if len(memberLookups) > 0 {
			memberResult, err := database.GetLibraryCacheMap(ctx, memberLookups)
			if err == nil {
				for k, v := range memberResult {
					result[k] = v
				}
			}
		}
	}

	return result
}

func NewReviewTUI(database *db.DB, posterMode string, defaultBookFormat model.BookFormat) (*TUI, error) {
	listCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := database.ListPendingWithTitles(listCtx)
	if err != nil {
		return nil, fmt.Errorf("loading events: %w", err)
	}

	albumEvents, err := database.ListPendingAlbumEventsWithAlbums(listCtx)
	if err != nil {
		return nil, fmt.Errorf("loading album events: %w", err)
	}

	bookEvents, err := database.ListPendingBookEventsWithBooks(listCtx)
	if err != nil {
		return nil, fmt.Errorf("loading book events: %w", err)
	}

	if len(events) == 0 && len(albumEvents) == 0 && len(bookEvents) == 0 {
		return nil, fmt.Errorf("no pending releases to review")
	}

	return NewReviewTUIWithEvents(events, albumEvents, bookEvents, database, posterMode, 0, 0, "", defaultBookFormat)
}

func (t *TUI) Run() error {
	p := tea.NewProgram(t)
	final, err := p.Run()
	if err != nil {
		return err
	}
	m := final.(*TUI)
	return m.err
}

func (t *TUI) ApprovedTitles() []db.EventWithTitle {
	return t.approved
}

func (t *TUI) ApprovedCount() int {
	return len(t.approved) + t.approvedBooks
}

func (t *TUI) Counts() (approved, rejected, pending int) {
	for _, it := range t.items {
		switch it.decision {
		case decisionApproved:
			approved++
		case decisionRejected:
			rejected++
		default:
			pending++
		}
	}
	return
}

func (t *TUI) Init() tea.Cmd {
	return t.loadCurrentPosterCmd()
}

func (t *TUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width = msg.Width
		t.height = msg.Height
		if t.phase == phaseConfirm {
			t.vpConfirm.SetHeight(max(1, t.height-4))
			t.vpConfirm.SetWidth(t.width)
		}
		return t, t.renderPosterCmd()

	case posterReadyMsg:
		if msg.err == nil {
			t.posterImg = msg.img
			return t, t.renderPosterCmd()
		}
		t.posterImg = nil
		return t, t.clearPosterCmd()

	case tea.KeyPressMsg:
		switch t.phase {
		case phaseReview:
			return t.updateReview(msg)
		case phaseConfirm:
			return t.updateConfirm(msg)
		}
	}

	return t, nil
}

func (t *TUI) loadCurrentPosterCmd() tea.Cmd {
	it := t.currentItem()
	if it == nil {
		return t.clearPosterCmd()
	}
	switch {
	case it.bookEvent != nil:
		if it.bookEvent.Book.ImageURL == "" {
			return t.clearPosterCmd()
		}
	case it.albumEvent != nil:
		if it.albumEvent.Album.PosterPath == "" {
			return t.clearPosterCmd()
		}
	default:
		if it.event.Title.PosterPath == "" {
			return t.clearPosterCmd()
		}
	}
	return t.loadPosterCmd()
}

func (t *TUI) loadPosterCmd() tea.Cmd {
	if t.posterMode == PosterOff {
		return nil
	}
	it := t.currentItem()
	if it == nil {
		return nil
	}

	var imageURL string
	switch {
	case it.bookEvent != nil:
		imageURL = it.bookEvent.Book.ImageURL
	case it.albumEvent != nil:
		imageURL = it.albumEvent.Album.PosterPath
	default:
		tl := it.event.Title
		if tl.PosterPath != "" {
			return func() tea.Msg {
				img, err := getPosterImage(tl)
				if err != nil {
					return posterReadyMsg{err: err}
				}
				return posterReadyMsg{img: img}
			}
		}
		return t.clearPosterCmd()
	}

	if imageURL == "" {
		return t.clearPosterCmd()
	}
	return func() tea.Msg {
		img, err := getAlbumPosterImage(imageURL)
		if err != nil {
			return posterReadyMsg{err: err}
		}
		return posterReadyMsg{img: img}
	}
}

func (t *TUI) renderPosterCmd() tea.Cmd {
	if !posterAvailable || t.posterMode == PosterOff || t.posterImg == nil {
		return nil
	}

	apc := buildPosterAPC(t.posterImg)
	if apc == "" {
		return nil
	}

	clear := buildPosterClear()
	raw := fmt.Sprintf("%s\x1b[s\x1b[3;1H%s\x1b[u", clear, apc)
	return tea.Raw(raw)
}

func (t *TUI) clearPosterCmd() tea.Cmd {
	if !kittySupported {
		return nil
	}
	return tea.Raw(buildPosterClear())
}

func (t *TUI) rebuildFiltered() {
	t.filtered = nil
	for i, it := range t.items {
		if t.filter != "" && it.mediaType() != t.filter {
			continue
		}
		if t.filterUndecided && it.decision != decisionNone {
			continue
		}
		t.filtered = append(t.filtered, i)
	}
	if t.cursor >= len(t.filtered) {
		t.cursor = 0
	}
}

func (t *TUI) currentItem() *itemState {
	if len(t.filtered) == 0 {
		return nil
	}
	return &t.items[t.filtered[t.cursor]]
}

func (t *TUI) hasDecisions() bool {
	for _, it := range t.items {
		if it.decision != decisionNone {
			return true
		}
	}
	return false
}

func (t *TUI) saveDecisions() error {
	saveCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.database.Transaction(saveCtx, func(tx *sql.Tx) error {
		for _, it := range t.items {
			if it.decision == decisionNone {
				continue
			}
			status := model.StatusApproved
			if it.decision == decisionRejected {
				status = model.StatusRejected
			}

			switch {
			case it.bookEvent != nil:
				pref := it.bookEvent.Event.FormatPref
				if pref != "" {
					if err := t.database.UpdateBookReleaseEventStatusAndFormatTx(saveCtx, tx, it.bookEvent.Event.ID, status, pref); err != nil {
						return err
					}
				} else {
					if err := t.database.UpdateBookReleaseEventStatusTx(saveCtx, tx, it.bookEvent.Event.ID, status); err != nil {
						return err
					}
				}
			case it.albumEvent != nil:
				if err := t.database.UpdateAlbumReleaseEventStatusTx(saveCtx, tx, it.albumEvent.Event.ID, status); err != nil {
					return err
				}
			default:
				if err := t.database.UpdateReleaseEventStatusTx(saveCtx, tx, it.event.Event.ID, status); err != nil {
					return err
				}
			}

			if it.decision == decisionApproved {
				switch {
				case it.bookEvent != nil:
					t.approvedBooks++
				case it.albumEvent != nil:
					t.approved = append(t.approved, db.EventWithTitle{
						Event: &model.ReleaseEvent{
							ID:      it.albumEvent.Event.ID,
							Status:  model.StatusApproved,
							ISOYear: it.albumEvent.Event.ISOYear,
							ISOWeek: it.albumEvent.Event.ISOWeek,
						},
						Title: &model.Title{
							Title:     it.albumEvent.Artist.Name + " - " + it.albumEvent.Album.Title,
							Year:      it.albumEvent.Album.Year,
							MediaType: model.MediaTypeMusic,
						},
					})
				default:
					t.approved = append(t.approved, it.event)
				}
			}
		}
		return nil
	})
}

func (t *TUI) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t.flashMsg = ""

	// Handle pending quit prompt: y = save+quit, n = discard+quit, any other key = cancel
	if t.pendingQuit {
		t.pendingQuit = false
		switch msg.String() {
		case "y":
			if err := t.saveDecisions(); err != nil {
				t.err = err
			}
			return t, t.quitCmd()
		case "n":
			return t, t.quitCmd()
		default:
			return t, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if t.hasDecisions() {
			t.pendingQuit = true
			t.flashMsg = "Save changes? (y/n)"
			return t, nil
		}
		return t, t.quitCmd()

	case "j", "down":
		if t.cursor < len(t.filtered)-1 {
			t.cursor++
			t.posterImg = nil
			return t, t.loadCurrentPosterCmd()
		}

	case "k", "up":
		if t.cursor > 0 {
			t.cursor--
			t.posterImg = nil
			return t, t.loadCurrentPosterCmd()
		}

	case "g", "home":
		t.cursor = 0
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "G", "end":
		t.cursor = len(t.filtered) - 1
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "pgup":
		t.cursor -= 5
		if t.cursor < 0 {
			t.cursor = 0
		}
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "pgdown":
		t.cursor += 5
		if t.cursor >= len(t.filtered) {
			t.cursor = len(t.filtered) - 1
		}
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "a":
		it := t.currentItem()
		if it == nil {
			return t, nil
		}
		if it.bookEvent != nil {
			ev := it.bookEvent.Event
			if it.decision == decisionNone {
				ev.FormatPref = t.defaultBookFormat
				it.decision = decisionApproved
				return t, nil
			}
			switch ev.FormatPref {
			case model.BookFormatBoth:
				ev.FormatPref = model.BookFormatEbook
			case model.BookFormatEbook:
				ev.FormatPref = model.BookFormatAudiobook
			case model.BookFormatAudiobook:
				ev.FormatPref = ""
				it.decision = decisionNone
				return t, nil
			}
			if ev.FormatPref != "" {
				it.decision = decisionApproved
			}
			return t, nil
		}
		if it.decision == decisionApproved {
			it.decision = decisionNone
		} else {
			it.decision = decisionApproved
		}

	case "r":
		it := t.currentItem()
		if it == nil {
			return t, nil
		}
		if it.decision == decisionRejected {
			it.decision = decisionNone
		} else {
			it.decision = decisionRejected
		}

	case "u":
		it := t.currentItem()
		if it == nil {
			return t, nil
		}
		it.decision = decisionNone

	case "m":
		t.filterUndecided = false
		if t.filter == model.MediaTypeMovie {
			t.filter = ""
		} else {
			t.filter = model.MediaTypeMovie
		}
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "t":
		t.filterUndecided = false
		if t.filter == model.MediaTypeTV {
			t.filter = ""
		} else {
			t.filter = model.MediaTypeTV
		}
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "b":
		t.filterUndecided = false
		if t.filter == model.MediaTypeBook {
			t.filter = ""
		} else {
			t.filter = model.MediaTypeBook
		}
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "l":
		t.filterUndecided = false
		if t.filter == model.MediaTypeMusic {
			t.filter = ""
		} else {
			t.filter = model.MediaTypeMusic
		}
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "e":
		t.filterUndecided = false
		if t.filter == model.MediaTypeAnime {
			t.filter = ""
		} else {
			t.filter = model.MediaTypeAnime
		}
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "n":
		t.filterUndecided = !t.filterUndecided
		t.rebuildFiltered()
		t.posterImg = nil
		return t, t.loadCurrentPosterCmd()

	case "o":
		it := t.currentItem()
		if it == nil {
			return t, nil
		}
		if it.bookEvent != nil {
			ev := it.bookEvent.Event
			// Parse structured notes for source-specific URLs
			u := ""
			var slug, ean string
			if ev.Notes != "" {
				for _, part := range strings.Split(ev.Notes, "|") {
					if strings.HasPrefix(part, "url=") {
						u = strings.TrimPrefix(part, "url=")
					} else if strings.HasPrefix(part, "slug=") {
						slug = strings.TrimPrefix(part, "slug=")
					} else if strings.HasPrefix(part, "ean=") {
						ean = strings.TrimPrefix(part, "ean=")
					}
				}
			}
			// Check if Notes itself is a direct URL (Goodreads compatibility)
			if u == "" && !strings.Contains(ev.Notes, "=") {
				u = ev.Notes
			}
			if u == "" && slug != "" {
				u = "https://bookmarks.reviews/reviews/" + slug + "/"
			}
			if u == "" && ean != "" {
				u = "https://bookshop.org/books?ean=" + ean
			}
			if u == "" && it.bookEvent.Book.HardcoverSlug != "" {
				u = fmt.Sprintf("https://hardcover.app/books/%s", it.bookEvent.Book.HardcoverSlug)
			}
			if u == "" && it.bookEvent.Book.OLID != "" {
				u = fmt.Sprintf("https://openlibrary.org/works/%s", it.bookEvent.Book.OLID)
			}
			if u == "" {
				u = "https://www.goodreads.com/search?q=" + url.QueryEscape(it.bookEvent.Book.Title)
			}
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				t.flashMsg = fmt.Sprintf("Failed to open browser: %v", err)
			}
		} else if it.albumEvent != nil {
			u := it.albumEvent.Album.AOTYURL
			if u == "" {
				u = "https://www.albumoftheyear.org/search/?q=" + url.QueryEscape(it.albumEvent.Album.Title)
			}
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				t.flashMsg = fmt.Sprintf("Failed to open browser: %v", err)
			}
		} else if it.event.Title != nil && it.event.Title.MediaType == model.MediaTypeAnime && it.event.Title.MalID > 0 {
			u := fmt.Sprintf("https://myanimelist.net/anime/%d", it.event.Title.MalID)
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				t.flashMsg = fmt.Sprintf("Failed to open browser: %v", err)
			}
		} else if it.event.Title != nil {
			tl := it.event.Title
			rtURL := tl.RTURL
			if rtURL == "" {
				rtURL = "https://www.rottentomatoes.com/search?search=" + url.QueryEscape(tl.Title)
			}
			if err := exec.Command("xdg-open", rtURL).Start(); err != nil {
				t.flashMsg = fmt.Sprintf("Failed to open browser: %v", err)
			}
		}

	case "enter":
		firstUndecided := -1
		re := 0
		for idx, f := range t.filtered {
			if t.items[f].decision == decisionNone {
				if firstUndecided == -1 {
					firstUndecided = idx
				}
				re++
			}
		}
		if re > 0 {
			if !t.filterUndecided {
				t.filterUndecided = true
				t.rebuildFiltered()
				t.cursor = 0
				t.posterImg = nil
				t.flashMsg = fmt.Sprintf("%d title(s) need a decision — make choices, then press Enter again", re)
				return t, t.loadCurrentPosterCmd()
			}
			t.flashMsg = fmt.Sprintf("%d title(s) still need a decision", re)
			return t, nil
		}
		t.phase = phaseConfirm
		t.vpConfirm.SetContent(t.buildConfirmContent())
		t.vpConfirm.SetHeight(max(1, t.height-4))
		t.vpConfirm.SetWidth(t.width)
		t.vpConfirm.GotoTop()
		return t, t.clearPosterCmd()
	}

	return t, nil
}

func (t *TUI) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return t, t.quitCmd()

	case "j", "down":
		t.vpConfirm.ScrollDown(1)

	case "k", "up":
		t.vpConfirm.ScrollUp(1)

	case "y":
		if err := t.saveDecisions(); err != nil {
			t.err = err
		}
		return t, t.quitCmd()

	case "n":
		t.phase = phaseReview
		return t, t.loadCurrentPosterCmd()
	}

	return t, nil
}

func (t *TUI) quitCmd() tea.Cmd {
	cmds := []tea.Cmd{tea.Quit}
	if clear := t.clearPosterCmd(); clear != nil {
		cmds = append([]tea.Cmd{clear}, cmds...)
	}
	return tea.Sequence(cmds...)
}

func (t *TUI) View() tea.View {
	var content string
	var footer string

	switch t.phase {
	case phaseReview:
		content = t.buildReviewContent()
		footer = keyStyle.Render("j") + helpStyle.Render("/") + keyStyle.Render("k") + helpStyle.Render("   ") +
			keyStyle.Render("a") + helpStyle.Render(" 🟢  ") +
			keyStyle.Render("r") + helpStyle.Render(" 🔴  ") +
			keyStyle.Render("n") + helpStyle.Render(" ❔  ") +
			keyStyle.Render("m") + helpStyle.Render(" 🎬️  ") +
			keyStyle.Render("t") + helpStyle.Render(" 📺️  ") +
			keyStyle.Render("e") + helpStyle.Render(" 🌸  ") +
			keyStyle.Render("b") + helpStyle.Render(" 📚️  ") +
			keyStyle.Render("l") + helpStyle.Render(" 💿️  ") +
			keyStyle.Render("enter") + helpStyle.Render(" confirm  ") +
			keyStyle.Render("o") + helpStyle.Render(" open  ") +
			keyStyle.Render("q") + helpStyle.Render(" quit")
	case phaseConfirm:
		footer = keyStyle.Render("j") + helpStyle.Render("/") + keyStyle.Render("k") + helpStyle.Render(" scroll  ") +
			keyStyle.Render("y") + helpStyle.Render(" continue  ") +
			keyStyle.Render("n") + helpStyle.Render(" return")
	}

	var b strings.Builder

	filterLabel := ""
	switch t.filter {
	case model.MediaTypeMovie:
		filterLabel = "  [movies]"
	case model.MediaTypeTV:
		filterLabel = "  [tv]"
	case model.MediaTypeBook:
		filterLabel = "  [books]"
	case model.MediaTypeMusic:
		filterLabel = "  [albums]"
	case model.MediaTypeAnime:
		filterLabel = "  [anime]"
	}
	if t.filterUndecided {
		filterLabel = "  [undecided]"
	}

	pending := 0
	for _, it := range t.items {
		if it.decision == decisionNone {
			pending++
		}
	}

	if t.phase == phaseConfirm {
		b.WriteString(headerStyle.Render(fmt.Sprintf("wmdl review — %d pending%s — confirm decisions",
			pending, filterLabel)))
		b.WriteString("\n\n")
		b.WriteString(t.vpConfirm.View())
	} else {
		b.WriteString(headerStyle.Render(fmt.Sprintf("wmdl review — %d pending%s       [%d/%d]",
			pending, filterLabel, t.cursor+1, len(t.filtered))))
		b.WriteString("\n\n")
		b.WriteString(content)

		if t.flashMsg != "" {
			b.WriteString("\n\n")
			if t.shouldPadForPoster() {
				b.WriteString(strings.Repeat(" ", posterCols+1))
			}
			b.WriteString(warnStyle.Render(t.flashMsg))
		}
	}

	curLines := strings.Count(b.String(), "\n") + 1
	if t.height > 0 {
		pad := t.height - curLines - 2
		for i := 0; i < pad; i++ {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(footer)

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (t *TUI) rightWidth() int {
	if t.width <= 0 {
		return 70
	}
	right := t.width - posterCols - 2
	if right < 20 {
		right = 20
	}
	return right
}

func (t *TUI) availableContentLines() int {
	reserved := 4 // header + blank + blank-before-footer + footer
	if t.flashMsg != "" {
		reserved += 2 // blank + flash message
	}
	avail := t.height - reserved
	if avail < 5 {
		avail = 5
	}
	return avail
}

func (t *TUI) shouldPadForPoster() bool {
	switch t.posterMode {
	case PosterOff:
		return false
	case PosterText:
		return true
	default:
		return kittySupported
	}
}

func (t *TUI) buildReviewContent() string {
	rw := t.rightWidth()

	it := t.currentItem()
	if it == nil {
		if t.filter == model.MediaTypeAnime && t.prevAnimeWeek != "" {
			return fmt.Sprintf("No new anime items for W%d.\nPreviously-reviewed anime items are in %s.\nRun: wmdl review --week %s",
				t.week, t.prevAnimeWeek, t.prevAnimeWeek)
		}
		return "no items match the current filter"
	}

	var b strings.Builder

	if it.albumEvent != nil {
		if t.shouldPadForPoster() {
			posterBlock := t.buildPosterBlock()
			posterLines := strings.Split(posterBlock, "\n")
			musicContent := t.buildMusicContent(it.albumEvent, rw, t.availableContentLines())
			musicLines := strings.Split(musicContent, "\n")

			maxLines := len(posterLines)
			if len(musicLines) > maxLines {
				maxLines = len(musicLines)
			}

			for i := 0; i < maxLines; i++ {
				leftPart := ""
				if i < len(posterLines) {
					leftPart = posterLines[i] + " "
				} else {
					leftPart = strings.Repeat(" ", posterCols+1)
				}

				rightPart := ""
				if i < len(musicLines) {
					rightPart = musicLines[i]
				}

				b.WriteString(leftPart)
				b.WriteString(" ")
				b.WriteString(rightPart)
				if i < maxLines-1 {
					b.WriteString("\n")
				}
			}
		} else {
			b.WriteString(t.buildMusicContent(it.albumEvent, rw, t.availableContentLines()))
		}
		return b.String()
	}

	if it.bookEvent != nil {
		if t.shouldPadForPoster() {
			posterBlock := t.buildPosterBlock()
			posterLines := strings.Split(posterBlock, "\n")
			bookContent := t.buildBookContent(it.bookEvent, rw, t.availableContentLines())
			bookLines := strings.Split(bookContent, "\n")

			maxLines := len(posterLines)
			if len(bookLines) > maxLines {
				maxLines = len(bookLines)
			}

			for i := 0; i < maxLines; i++ {
				leftPart := ""
				if i < len(posterLines) {
					leftPart = posterLines[i] + " "
				} else {
					leftPart = strings.Repeat(" ", posterCols+1)
				}

				rightPart := ""
				if i < len(bookLines) {
					rightPart = bookLines[i]
				}

				b.WriteString(leftPart)
				b.WriteString(" ")
				b.WriteString(rightPart)
				if i < maxLines-1 {
					b.WriteString("\n")
				}
			}
		} else {
			b.WriteString(t.buildBookContent(it.bookEvent, rw, t.availableContentLines()))
		}
		return b.String()
	}

	tl := it.event.Title
	ev := it.event.Event

	if t.shouldPadForPoster() {
		posterBlock := t.buildPosterBlock()
		posterLines := strings.Split(posterBlock, "\n")
		rightContent := t.buildRightContent(tl, ev, rw, t.availableContentLines())
		rightLines := strings.Split(rightContent, "\n")

		maxLines := len(posterLines)
		if len(rightLines) > maxLines {
			maxLines = len(rightLines)
		}

		for i := 0; i < maxLines; i++ {
			leftPart := ""
			if i < len(posterLines) {
				leftPart = posterLines[i] + " "
			} else {
				leftPart = strings.Repeat(" ", posterCols+1)
			}

			rightPart := ""
			if i < len(rightLines) {
				rightPart = rightLines[i]
			}

			b.WriteString(leftPart)
			b.WriteString(" ")
			b.WriteString(rightPart)
			if i < maxLines-1 {
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(t.buildRightContent(tl, ev, rw, t.availableContentLines()))
	}

	return b.String()
}

func (t *TUI) buildMusicContent(ae *db.EventWithAlbum, rw int, maxLines int) string {
	var b strings.Builder

	al := ae.Album
	ar := ae.Artist
	ev := ae.Event

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decoration = " "
			decorationStyle = approvedStyle
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	avail := rw - 1
	if avail < 10 {
		avail = 10
	}

	title := ar.Name + " - " + al.Title
	if al.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, al.Year)
	}

	// Line 1: decoration + title
	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title))

	// Line 2: empty
	b.WriteString("\n\n")

	isAllMusic := ev.Source == "allmusic"

	// Line 3: [album] type · source · release_date
	var tagParts []string
	tagParts = append(tagParts, fmt.Sprintf("[album] %s", string(al.AlbumType)))
	if isAllMusic {
		tagParts = append(tagParts, "AllMusic Editor's Choice")
	} else if ev.Source != "" {
		tagParts = append(tagParts, ev.Source)
	}
	if isAllMusic && al.ReleaseDate != "" {
		// Format "2026-05-01" as "May 2026"
		if t, err := time.Parse("2006-01-02", al.ReleaseDate); err == nil {
			tagParts = append(tagParts, t.Format("January 2006"))
		} else {
			tagParts = append(tagParts, al.ReleaseDate)
		}
	} else if al.ReleaseDate != "" {
		tagParts = append(tagParts, al.ReleaseDate)
	}
	b.WriteString(tagStyle.Render(strings.Join(tagParts, " · ")))

	// Line 4: MB info or warning
	if al.MBID != "" {
		b.WriteString("\n")
		infoParts := []string{string(al.AlbumType)}
		if isAllMusic && al.ReleaseDate != "" {
			if t, err := time.Parse("2006-01-02", al.ReleaseDate); err == nil {
				infoParts = append(infoParts, t.Format("January 2006"))
			} else {
				infoParts = append(infoParts, al.ReleaseDate)
			}
		} else {
			infoParts = append(infoParts, al.ReleaseDate)
		}
		b.WriteString(rtStyle.Render(strings.Join(infoParts, " · ")))
	}
	if al.MBID == "" {
		b.WriteString("\n")
		b.WriteString(rejectedStyle.Render("⚠ No MusicBrainz match — may not add to Lidarr"))
	}

	// Line 5: AllMusic Editor's Choice badge
	if isAllMusic {
		b.WriteString("\n")
		b.WriteString(approvedStyle.Render("🏅 AllMusic Editor's Choice"))
	}

	// Line 6: empty before scores
	b.WriteString("\n")

	// Line 7: Scores (AOTY + AllMusic)
	var scoreParts []string
	if al.AOTYCriticScore > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AOTY critic: %.0f (%d reviews)", al.AOTYCriticScore, al.AOTYCriticCount))
	}
	if al.AOTYUserScore > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AOTY user: %.0f (%d ratings)", al.AOTYUserScore, al.AOTYUserCount))
	}
	if al.AllMusicRating > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AllMusic: %.0f/10", al.AllMusicRating))
	}
	if len(scoreParts) > 0 {
		b.WriteString(ratingsLine.Render(strings.Join(scoreParts, " · ")))
	}

	// Line 8: empty before must-hear
	if len(scoreParts) > 0 || al.AOTYMustHear {
		b.WriteString("\n")
	}

	// Line 9: Must Hear (if true)
	if al.AOTYMustHear {
		b.WriteString(approvedStyle.Render("★ Must Hear (Editor's Pick)"))
	}

	// Genres (from AOTY or AllMusic scrape)
	if al.Genres != "" {
		b.WriteString("\n")
		b.WriteString(rtStyle.Width(rw).Render("genres: " + al.Genres))
	}

	// Overview/blurb from AllMusic (or AOTY)
	if al.Overview != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2 // \n\n separator
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(al.Overview)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	// Only show artist data for MB-matched items
	if ar.MBID != "" && !strings.HasPrefix(ar.MBID, "_nm_") {
		// Line 9: empty line before artist info
		b.WriteString("\n\n")

		// Line 10: country flag · begin-area, area · begin_date[-end_date] (age)
		var locParts []string
		if ar.Country != "" {
			locParts = append(locParts, countryFlag(ar.Country))
		}
		var fromParts []string
		if ar.BeginArea != "" {
			fromParts = append(fromParts, ar.BeginArea)
		}
		if ar.Area != "" && ar.Area != ar.BeginArea {
			fromParts = append(fromParts, ar.Area)
		}
		if len(fromParts) > 0 {
			locParts = append(locParts, strings.Join(fromParts, ", "))
		}
		if ar.BeginDate != "" {
			dateStr := ar.BeginDate
			if ar.EndDate != "" {
				dateStr += " – " + ar.EndDate
			}
			age := calcAge(ar.BeginDate, ar.EndDate)
			if age >= 0 {
				dateStr += fmt.Sprintf(" (%d)", age)
			}
			locParts = append(locParts, dateStr)
		}
		if len(locParts) > 0 {
			b.WriteString(strings.Join(locParts, " · "))
		}

		// Line 11: disambiguation
		if ar.Disambiguation != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render(ar.Disambiguation))
		}

		// MB artist rating (always shown, — when none)
		b.WriteString("\n")
		b.WriteString(ratingsLine.Render(fmt.Sprintf("MB artist rating: %s", fmtRating(ar.MBRating))))
	}

	// Library status
	it = t.currentItem()
	if it != nil && it.albumEvent != nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}
	}

	return b.String()
}

func (t *TUI) buildBookContent(be *db.EventWithBook, rw int, maxLines int) string {
	var b strings.Builder

	book := be.Book
	author := be.Author
	ev := be.Event

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decorationStyle = approvedStyle
			switch ev.FormatPref {
			case model.BookFormatEbook:
				decoration = " "
			case model.BookFormatAudiobook:
				decoration = " "
			default:
				decoration = " "
			}
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	avail := rw - 1
	if avail < 10 {
		avail = 10
	}

	title := author.Name + " — " + book.Title
	if book.ReleaseYear > 0 {
		title = fmt.Sprintf("%s (%d)", title, book.ReleaseYear)
	}

	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title))

	b.WriteString("\n\n")

	var tagParts []string
	tagParts = append(tagParts, "[book]")
	tagParts = append(tagParts, string(ev.FormatPref))
	if ev.Source != "" {
		sources := strings.Split(ev.Source, ",")
		tagParts = append(tagParts, strings.Join(sources, " + "))
	}
	b.WriteString(tagStyle.Render(strings.Join(tagParts, " · ")))

	if book.Rating > 0 {
		b.WriteString("\n")
		var scoreParts []string
		scoreParts = append(scoreParts, fmt.Sprintf("Rating: %.1f", book.Rating))
		if book.RatingsCount > 0 {
			scoreParts = append(scoreParts, fmt.Sprintf("%d ratings", book.RatingsCount))
		}
		if book.ShelvingsCount > 0 {
			scoreParts = append(scoreParts, fmtShelvings(book.ShelvingsCount)+" shelvings")
		}
		b.WriteString(ratingsLine.Render(strings.Join(scoreParts, " · ")))
	}

	// Book Marks critic verdict
	if strings.Contains(ev.Source, "bookmarks") {
		verdict, total := parseBmVerdict(ev.Notes)
		if verdict != "" {
			b.WriteString("\n")
			display := "Book Marks: " + verdict
			if total > 0 {
				display += fmt.Sprintf(" — %d reviews", total)
			}
			b.WriteString(ratingsLine.Render(display))
		}
	}

	// Curation badge for goodreads_blog posts
	if strings.Contains(ev.Source, "goodreads_blog") {
		var badge string
		if strings.Contains(ev.Notes, ":weekly|") {
			badge = "Readers' Pick"
		} else if strings.Contains(ev.Notes, ":editors|") {
			badge = "Editors' Pick"
		}
		if badge != "" {
			b.WriteString("\n")
			b.WriteString(ratingsLine.Render(badge))
		}
	}

	var detailParts []string
	if book.ReleaseDate != "" {
		detailParts = append(detailParts, book.ReleaseDate)
	}
	if book.Publisher != "" {
		detailParts = append(detailParts, book.Publisher)
	}
	if book.Language != "" {
		detailParts = append(detailParts, book.Language)
	}
	if book.Pages > 0 {
		detailParts = append(detailParts, fmt.Sprintf("%d pages", book.Pages))
	}
	if book.AudioSeconds > 0 {
		h := book.AudioSeconds / 3600
		m := (book.AudioSeconds % 3600) / 60
		if h > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%dh %dm", h, m))
		} else {
			detailParts = append(detailParts, fmt.Sprintf("%dm", m))
		}
	}
	if len(detailParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(detailParts, " · ")))
	}

	var idParts []string
	if book.ISBN13 != "" {
		idParts = append(idParts, "ISBN: "+book.ISBN13)
	}
	if book.ASIN != "" {
		idParts = append(idParts, "ASIN: "+book.ASIN)
	}
	if len(idParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(idParts, " · ")))
	}

	var metaParts []string
	if book.LiteraryType != "" {
		metaParts = append(metaParts, book.LiteraryType)
	}
	if book.Tags != "" {
		metaParts = append(metaParts, book.Tags)
	}
	if len(metaParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(metaParts, " · ")))
	}

	it = t.currentItem()
	if it != nil && it.bookEvent != nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}

		if seriesStr := it.bookSeriesStr(t.libraryCache); seriesStr != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("  " + seriesStr))
		}
	}

	if book.Description != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2 // \n\n separator
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(book.Description)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	return b.String()
}

func calcAge(beginDate, endDate string) int {
	layout := "2006-01-02"
	start, err := time.Parse(layout, beginDate)
	if err != nil {
		return -1
	}
	var end time.Time
	if endDate != "" {
		end, err = time.Parse(layout, endDate)
		if err != nil {
			return -1
		}
	} else {
		end = time.Now()
	}
	if end.Before(start) {
		return -1
	}
	age := end.Year() - start.Year()
	if end.YearDay() < start.YearDay() {
		age--
	}
	return age
}

func (t *TUI) buildPosterBlock() string {
	ph := t.posterHeight()
	if !posterAvailable || t.posterMode == PosterText {
		return renderTextPlaceholder(posterCols, ph)
	}
	if t.posterImg == nil {
		return renderTextPlaceholder(posterCols, ph)
	}
	return strings.Repeat(" ", posterCols)
}

func (t *TUI) posterHeight() int {
	if fontW <= 0 || fontH <= 0 {
		return 16
	}
	// Estimate poster rows: 2:3 aspect ratio for movies and book covers,
	// 1:1 for albums (square cover art).
	aspect := 1.5
	if it := t.currentItem(); it != nil {
		if it.albumEvent != nil {
			aspect = 1.0
		}
	}
	h := int(float64(posterCols*fontW) * aspect / float64(fontH))
	if h < 10 {
		h = 10
	}
	return h
}

func (t *TUI) buildRightContent(tl *model.Title, ev *model.ReleaseEvent, rw int, maxLines int) string {
	var b strings.Builder

	title := tl.Title
	if tl.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, tl.Year)
	}
	tmdbNote := ""
	if tl.TmdbTitle != "" {
		// Strip metadata patterns before comparing, so "(season 3)" or
		// "(complete series)" don't trigger false mismatch warnings.
		scrapedClean := strings.TrimSpace(reviewMetaParen.ReplaceAllString(tl.Title, ""))
		tmdbClean := strings.TrimSpace(reviewMetaParen.ReplaceAllString(tl.TmdbTitle, ""))
		if !strings.EqualFold(scrapedClean, tmdbClean) {
			tmdbNote = fmt.Sprintf("TMDB: %s", tl.TmdbTitle)
		}
	}

	mediaType := "movie"
	switch tl.MediaType {
	case model.MediaTypeTV:
		mediaType = "tv"
	case model.MediaTypeAnime:
		mediaType = "anime"
	}

	releaseType := "physical"
	if ev.ReleaseType == model.ReleaseStreaming {
		releaseType = "streaming"
	}

	var notes []string
	if ev.Notes != "" {
		notes = append(notes, ev.Notes)
	} else if ev.PreviousStatus == model.StatusRejected {
		notes = append(notes, "previously rejected")
	} else if ev.PreviousStatus == model.StatusApproved {
		notes = append(notes, "previously approved")
	} else if ev.PreviousStatus == model.StatusDownloaded {
		notes = append(notes, "previously downloaded")
	}

	extra := ""
	if len(notes) > 0 {
		extra = " (" + strings.Join(notes, ", ") + ")"
	}

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decoration = " "
			decorationStyle = approvedStyle
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	// Line 1: indent + decoration + wrapped title
	avail := rw - 1
	if avail < 10 {
		avail = 10
	}
	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title + extra))

	// Line 2: [movie] streaming
	b.WriteString("\n\n")
	b.WriteString(tagStyle.Render(fmt.Sprintf("[%s] %s", mediaType, releaseType)))

	// TMDB match note (shown when TMDB title differs from scraped title)
	if tmdbNote != "" {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(tmdbNote))
	}

	// Meta line
	if hasFirstMeta(tl) {
		var parts []string
		if tl.OriginCountry != "" {
			for _, c := range strings.Split(tl.OriginCountry, ",") {
				if flag := countryFlag(c); flag != "" {
					parts = append(parts, flag)
				}
			}
		}
		if lang := languageName(tl.OriginalLanguage); lang != "" {
			parts = append(parts, lang)
		}
		if tl.USRating != "" {
			parts = append(parts, "["+tl.USRating+"]")
		}
		if tl.Genres != "" {
			parts = append(parts, tl.Genres)
		}
		if tl.Runtime > 0 {
			parts = append(parts, fmtRuntime(tl.Runtime))
		}
		b.WriteString("\n")
		b.WriteString(strings.Join(parts, " · "))
	}

	// Anime-specific detail lines
	if tl.MediaType == model.MediaTypeAnime {
		var animeParts []string
		if tl.AnimeType != "" {
			animeParts = append(animeParts, tl.AnimeType)
		}
		if tl.AnimeStatus != "" {
			animeParts = append(animeParts, tl.AnimeStatus)
		}
		if tl.AnimeSource != "" {
			animeParts = append(animeParts, tl.AnimeSource)
		}
		if tl.AnimeStudio != "" {
			animeParts = append(animeParts, tl.AnimeStudio)
		}
		if tl.AnimeEpisodes > 0 {
			animeParts = append(animeParts, fmt.Sprintf("%d eps", tl.AnimeEpisodes))
		}
		if len(animeParts) > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Join(animeParts, " · "))
		}

		if tl.Themes != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Themes: " + tl.Themes))
		}
		if tl.Demographics != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Demographics: " + tl.Demographics))
		}
		if tl.Streaming != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Streaming: " + tl.Streaming))
		}
	}

	// Phase B (currently-airing) notice
	if strings.HasPrefix(ev.Source, "jikan-airing") || strings.HasPrefix(ev.Source, "anilist-airing") || strings.HasPrefix(ev.Source, "tenrai-airing") {
		b.WriteString("\n\n")
		b.WriteString(warnStyle.Render("⏳ Currently Airing"))
		if strings.Contains(ev.Notes, "end=") {
			endDate := extractEndDate(ev.Notes)
			if endDate != "" {
				b.WriteString(rtStyle.Render(fmt.Sprintf(" — Final episode %s", endDate)))
			}
		}
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("⚠  No season packs available"))
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("Approve to add to Sonarr for episode management,"))
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("or look for this item again after the season ends."))
		b.WriteString("\n")
	}

	// Scores line
	if tl.MediaType == model.MediaTypeAnime {
		var ratings []string
		if tl.TmdbRating > 0 {
			ratings = append(ratings, fmt.Sprintf("MAL: %s", fmtRating(tl.TmdbRating)))
		}
		if tl.AnimeMembers > 0 {
			ratings = append(ratings, fmt.Sprintf("Members: %s", fmtMembers(tl.AnimeMembers)))
		}
		if tl.AnimeRank > 0 {
			ratings = append(ratings, fmt.Sprintf("Rank: #%d", tl.AnimeRank))
		}
		if len(ratings) > 0 {
			b.WriteString("\n\n")
			b.WriteString(ratingsLine.Render(strings.Join(ratings, " · ")))
		}
	} else {
		ratings := fmt.Sprintf("TMDB: %s · IMDb: %s · MC: %s · YT: %s · 🍅 %s · 🍿 %s",
			fmtRating(tl.TmdbRating),
			fmtRating(tl.ImdbRating),
			fmtRating(tl.MetacriticScore),
			fmtViews(tl.YoutubeViews),
			fmtPct(tl.RTCriticsScore),
			fmtPct(tl.RTAudienceScore),
		)
		b.WriteString("\n\n")
		b.WriteString(ratingsLine.Render(ratings))
	}

	// Library status
	it = t.currentItem()
	if it != nil && it.albumEvent == nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}
		if collStr := it.collectionStr(t.libraryCache); collStr != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("  " + collStr))
		}
	}

	// Overview
	if tl.Overview != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2 // \n\n separator
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(tl.Overview)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	// RT URL status (skip for anime)
	if tl.MediaType != model.MediaTypeAnime {
		b.WriteString("\n\n")
		if tl.RTURL != "" {
			b.WriteString(rtStyle.Render("RT: " + tl.RTURL))
		} else {
			b.WriteString(rtStyle.Render("RT page unavailable"))
		}
	}

	return b.String()
}

func (t *TUI) buildConfirmContent() string {
	var b strings.Builder

	b.WriteString(sectionStyle.Render("── Items to download ──"))
	b.WriteString("\n")
	hasApproved := false
	for _, it := range t.items {
		if it.decision == decisionApproved {
			title := it.displayTitle()
			if it.albumEvent == nil && it.bookEvent == nil && it.event.Title.Year > 0 {
				title = fmt.Sprintf("%s (%d)", title, it.event.Title.Year)
			}
			b.WriteString(confirmApprovedStyle.Render("  ✓ " + title))
			b.WriteString("\n")
			hasApproved = true
		}
	}
	if !hasApproved {
		b.WriteString(confirmEmptyStyle.Render("  (none)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("── Items rejected ──"))
	b.WriteString("\n")
	hasRejected := false
	for _, it := range t.items {
		if it.decision == decisionRejected {
			title := it.displayTitle()
			if it.albumEvent == nil && it.bookEvent == nil && it.event.Title.Year > 0 {
				title = fmt.Sprintf("%s (%d)", title, it.event.Title.Year)
			}
			b.WriteString(confirmRejectedStyle.Render("  ✗ " + title))
			b.WriteString("\n")
			hasRejected = true
		}
	}
	if !hasRejected {
		b.WriteString(confirmEmptyStyle.Render("  (none)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	undecided := 0
	for _, it := range t.items {
		if it.decision == decisionNone {
			undecided++
		}
	}
	if undecided > 0 {
		b.WriteString(confirmEmptyStyle.Render(fmt.Sprintf("  %d item(s) still undecided — will be skipped", undecided)))
		b.WriteString("\n")
		b.WriteString("\n")
	}

	b.WriteString(confirmPromptStyle.Render("Are you sure?"))

	return b.String()
}

func hasFirstMeta(tl *model.Title) bool {
	return tl.USRating != "" || tl.Genres != "" || tl.Runtime > 0 ||
		tl.OriginalLanguage != "" || tl.OriginCountry != ""
}

func fmtRating(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v)
}

func fmtShelvings(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return strconv.Itoa(n)
}

func fmtPct(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", v)
}

func fmtRuntime(m int) string {
	if m <= 0 {
		return "—"
	}
	h := m / 60
	mins := m % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func extractEndDate(notes string) string {
	if !strings.Contains(notes, "end=") {
		return ""
	}
	// Notes format: "airing|end=2026-06-22|eps=13"
	for _, part := range strings.Split(notes, "|") {
		if strings.HasPrefix(part, "end=") {
			return strings.TrimPrefix(part, "end=")
		}
	}
	return ""
}

func fmtMembers(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	default:
		return strconv.Itoa(n)
	}
}

func parseBmVerdict(notes string) (verdict string, total int) {
	if notes == "" {
		return "", 0
	}
	// Handle multi-source namespaced format: "goodreads:...||bookmarks:slug=X|url=...|verdict=Rave|total=5"
	bmSection := notes
	if strings.Contains(notes, "||") {
		for _, part := range strings.Split(notes, "||") {
			if strings.HasPrefix(part, "bookmarks:") {
				bmSection = strings.TrimPrefix(part, "bookmarks:")
				break
			}
		}
	}
	if bmSection == "" {
		return "", 0
	}
	for _, part := range strings.Split(bmSection, "|") {
		if strings.HasPrefix(part, "verdict=") {
			verdict = strings.TrimPrefix(part, "verdict=")
		}
		if strings.HasPrefix(part, "total=") {
			n, err := fmt.Sscanf(strings.TrimPrefix(part, "total="), "%d", &total)
			if err != nil || n != 1 {
				total = 0
			}
		}
	}
	return
}

func fmtViews(n int64) string {
	if n == 0 {
		return "—"
	}
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

var (
	headerStyle          = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	emptyStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	approvedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	rejectedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	tagStyle             = lipgloss.NewStyle()
	titleStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	ratingsLine          = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	overviewStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	helpStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	keyStyle             = lipgloss.NewStyle()
	rtStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warnStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true).Padding(0, 2)
	sectionStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")).Padding(0, 2)
	confirmApprovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 2)
	confirmRejectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 2)
	confirmEmptyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 4)
	confirmPromptStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 2)
)
