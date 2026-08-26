package review

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"net/url"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

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

func NewReviewTUIWithEvents(events []db.EventWithTitle, albumEvents []db.EventWithAlbumRelease, bookEvents []db.EventWithBook, database *db.DB, posterMode string, year, week int, prevAnimeWeek string, defaultBookFormat model.BookFormat) (*TUI, error) {
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
		items[idx] = itemState{bookEvent: &be, decision: decisionForStatus(be.Event.Status), origFormatPref: be.Event.FormatPref}
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

func NewReviewTUI(database *db.DB, posterMode string, defaultBookFormat model.BookFormat) (*TUI, error) {
	listCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := database.ListPendingWithTitles(listCtx)
	if err != nil {
		return nil, fmt.Errorf("loading events: %w", err)
	}

	albumEvents, err := database.ListPendingAlbumReleaseEvents(listCtx)
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

func (t *TUI) Counts() (approved, rejected, pending, downloaded int) {
	for _, it := range t.items {
		switch it.decision {
		case decisionApproved:
			approved++
		case decisionRejected:
			rejected++
		case decisionDownloaded:
			downloaded++
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
		if it.albumEvent.Release.PosterPath == "" {
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
		imageURL = it.albumEvent.Release.PosterPath
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
		if it.decision != decisionNone && it.decision != decisionDownloaded {
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
			if it.decision == decisionNone || it.decision == decisionDownloaded {
				continue
			}
			status := model.StatusApproved
			if it.decision == decisionRejected {
				status = model.StatusRejected
			}
			// Never downgrade an already-downloaded item to approved unless the
			// user explicitly expanded a downloaded book's formats to "both".
			if it.isDownloaded() && status == model.StatusApproved && !it.bookFormatExpanded {
				continue
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
							Title:     it.albumEvent.Release.ArtistName + " - " + it.albumEvent.Release.Title,
							Year:      it.albumEvent.Release.Year,
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
		return t.approveCurrent()

	case "r":
		return t.rejectCurrent()

	case "u":
		return t.undecideCurrent()

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
			u := it.albumEvent.Release.AOTYURL
			if u == "" {
				u = albumNotesURL(it.albumEvent.Event)
			}
			if u == "" {
				u = "https://www.albumoftheyear.org/search/?q=" + url.QueryEscape(it.albumEvent.Release.Title)
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

// approveCurrent handles the `a` key. Downloaded items are not re-approved by
// accident — the one exception is expanding a downloaded book that has a
// missing format to "both", so the missing format gets fetched.
func (t *TUI) approveCurrent() (tea.Model, tea.Cmd) {
	it := t.currentItem()
	if it == nil {
		return t, nil
	}
	if it.isDownloaded() {
		if it.bookEvent != nil && !it.bookFormatExpanded {
			ev := it.bookEvent.Event
			if !ev.EbookProcessed || !ev.AudiobookProcessed {
				ev.FormatPref = model.BookFormatBoth
				it.decision = decisionApproved
				it.bookFormatExpanded = true
			}
		}
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
	return t, nil
}

// rejectCurrent handles the `r` key. A downloaded item can be explicitly
// rejected; pressing `r` again restores the downloaded state.
func (t *TUI) rejectCurrent() (tea.Model, tea.Cmd) {
	it := t.currentItem()
	if it == nil {
		return t, nil
	}
	if it.isDownloaded() {
		if it.decision == decisionRejected {
			if it.bookFormatExpanded {
				it.bookEvent.Event.FormatPref = it.origFormatPref
				it.bookFormatExpanded = false
			}
			it.decision = decisionDownloaded
		} else {
			it.decision = decisionRejected
		}
		return t, nil
	}
	if it.decision == decisionRejected {
		it.decision = decisionNone
	} else {
		it.decision = decisionRejected
	}
	return t, nil
}

// undecideCurrent handles the `u` key. For a downloaded item this only undoes
// a book format expansion; otherwise the decision is cleared.
func (t *TUI) undecideCurrent() (tea.Model, tea.Cmd) {
	it := t.currentItem()
	if it == nil {
		return t, nil
	}
	if it.isDownloaded() {
		if it.bookFormatExpanded {
			it.bookEvent.Event.FormatPref = it.origFormatPref
			it.bookFormatExpanded = false
			it.decision = decisionDownloaded
		}
		return t, nil
	}
	it.decision = decisionNone
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
			helpStyle.Render("🔵 downloaded  ") +
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
