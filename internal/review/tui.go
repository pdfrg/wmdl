package review

import (
	"context"
	"fmt"
	"image"
	"os/exec"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
)

type decision int

const (
	decisionNone decision = iota
	decisionApproved
	decisionRejected
)

type phase int

const (
	phaseReview phase = iota
	phaseConfirm
)

type itemState struct {
	event    db.EventWithTitle
	decision decision
}

type posterReadyMsg struct {
	img image.Image
	err error
}

type TUI struct {
	items      []itemState
	cursor     int
	phase      phase
	database   *db.DB
	width      int
	height     int
	approved   []db.EventWithTitle
	err        error
	posterImg  image.Image
	posterMode PosterMode
	flashMsg   string
}

func NewReviewTUI(database *db.DB, posterMode string) (*TUI, error) {
	ctx := context.Background()
	events, err := database.ListPendingWithTitles(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading events: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no pending releases to review")
	}

	items := make([]itemState, len(events))
	for i, e := range events {
		items[i] = itemState{event: e}
	}

	detectTerminal()

	return &TUI{
		items:      items,
		database:   database,
		height:     24,
		posterMode: ParsePosterMode(posterMode),
	}, nil
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

func (t *TUI) Init() tea.Cmd {
	return t.loadPosterCmd()
}

func (t *TUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width = msg.Width
		t.height = msg.Height

	case posterReadyMsg:
		if msg.err == nil {
			t.posterImg = msg.img
			return t, t.renderPosterCmd()
		} else {
			t.posterImg = nil
		}

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

func (t *TUI) loadPosterCmd() tea.Cmd {
	if t.posterMode == PosterOff {
		return nil
	}
	it := t.items[t.cursor]
	tl := it.event.Title

	return func() tea.Msg {
		img, err := getPosterImage(tl)
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

func (t *TUI) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t.flashMsg = ""

	switch msg.String() {
	case "q", "ctrl+c":
		return t, t.quitCmd()

	case "j", "down":
		if t.cursor < len(t.items)-1 {
			t.cursor++
			t.posterImg = nil
			return t, tea.Batch(t.loadPosterCmd(), t.renderPosterCmd())
		}

	case "k", "up":
		if t.cursor > 0 {
			t.cursor--
			t.posterImg = nil
			return t, tea.Batch(t.loadPosterCmd(), t.renderPosterCmd())
		}

	case "a":
		if t.items[t.cursor].decision == decisionApproved {
			t.items[t.cursor].decision = decisionNone
		} else {
			t.items[t.cursor].decision = decisionApproved
		}

	case "r":
		if t.items[t.cursor].decision == decisionRejected {
			t.items[t.cursor].decision = decisionNone
		} else {
			t.items[t.cursor].decision = decisionRejected
		}

	case "u":
		t.items[t.cursor].decision = decisionNone

	case "o":
		rtURL := t.items[t.cursor].event.Title.RTURL
		if rtURL != "" {
			_ = exec.Command("xdg-open", rtURL).Start()
		}

	case "enter":
		remaining := 0
		for _, it := range t.items {
			if it.decision == decisionNone {
				remaining++
			}
		}
		if remaining > 0 {
			t.flashMsg = fmt.Sprintf("%d title(s) still need a decision — [a] approve or [r] reject each", remaining)
			return t, nil
		}
		t.phase = phaseConfirm
		return t, t.clearPosterCmd()
	}

	return t, nil
}

func (t *TUI) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return t, t.quitCmd()

	case "y":
		for _, it := range t.items {
			if it.decision == decisionNone {
				continue
			}
			status := model.StatusApproved
			if it.decision == decisionRejected {
				status = model.StatusRejected
			}
			ctx := context.Background()
			if err := t.database.UpdateReleaseEventStatus(ctx, it.event.Event.ID, status); err != nil {
				t.err = err
				return t, t.quitCmd()
			}
			if it.decision == decisionApproved {
				t.approved = append(t.approved, it.event)
			}
		}
		return t, t.quitCmd()

	case "n":
		t.phase = phaseReview
		return t, t.renderPosterCmd()
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
		footer = keyStyle.Render("j") + helpStyle.Render("/") + keyStyle.Render("k") + helpStyle.Render(" navigate  ") +
			keyStyle.Render("a") + helpStyle.Render(" approve  ") +
			keyStyle.Render("r") + helpStyle.Render(" reject  ") +
			keyStyle.Render("u") + helpStyle.Render(" undo  ") +
			keyStyle.Render("enter") + helpStyle.Render(" confirm  ") +
			keyStyle.Render("o") + helpStyle.Render(" open RT  ") +
			keyStyle.Render("q") + helpStyle.Render(" quit")
	case phaseConfirm:
		content = t.buildConfirmContent()
		footer = keyStyle.Render("y") + helpStyle.Render(" continue  ") +
			keyStyle.Render("n") + helpStyle.Render(" return")
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("wmd review — %d pending       [%d/%d]",
		len(t.items), t.cursor+1, len(t.items))))
	b.WriteString("\n\n")
	b.WriteString(content)

	if t.flashMsg != "" {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(t.flashMsg))
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

	it := &t.items[t.cursor]
	tl := it.event.Title
	ev := it.event.Event

	var b strings.Builder

	if t.shouldPadForPoster() {
		posterBlock := t.buildPosterBlock()
		posterLines := strings.Split(posterBlock, "\n")
		rightContent := t.buildRightContent(tl, ev, rw)
		rightLines := strings.Split(rightContent, "\n")

		maxLines := len(posterLines)
		if len(rightLines) > maxLines {
			maxLines = len(rightLines)
		}

		for i := 0; i < maxLines; i++ {
			leftPart := ""
			if i < len(posterLines) {
				leftPart = posterLines[i]
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
		b.WriteString(t.buildRightContent(tl, ev, rw))
	}

	return b.String()
}

func (t *TUI) buildPosterBlock() string {
	if !posterAvailable || t.posterMode == PosterText {
		return renderTextPlaceholder(posterCols, 10)
	}
	if t.posterImg == nil {
		return renderTextPlaceholder(posterCols, 10)
	}
	return strings.Repeat(" ", posterCols)
}

func (t *TUI) buildRightContent(tl *model.Title, ev *model.ReleaseEvent, rw int) string {
	var b strings.Builder

	title := tl.Title
	if tl.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, tl.Year)
	}

	mediaType := "movie"
	if tl.MediaType == model.MediaTypeTV {
		mediaType = "tv"
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
	} else if ev.PreviousStatus == model.StatusDownloaded {
		notes = append(notes, "previously downloaded")
	}

	extra := ""
	if len(notes) > 0 {
		extra = " (" + strings.Join(notes, ", ") + ")"
	}

	decoration := " "
	decorationStyle := emptyStyle
	switch t.items[t.cursor].decision {
	case decisionApproved:
		decoration = " "
		decorationStyle = approvedStyle
	case decisionRejected:
		decoration = " "
		decorationStyle = rejectedStyle
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

	// Meta line
	if hasFirstMeta(tl) {
		var parts []string
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

	// Scores line
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

	// Overview
	if tl.Overview != "" {
		b.WriteString("\n\n")
		b.WriteString(overviewStyle.Width(rw).Render(tl.Overview))
	}

	// RT URL status
	b.WriteString("\n\n")
	if tl.RTURL != "" {
		b.WriteString(rtStyle.Render("RT: " + tl.RTURL))
	} else {
		b.WriteString(rtStyle.Render("RT page unavailable"))
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
			title := it.event.Title.Title
			if it.event.Title.Year > 0 {
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
			title := it.event.Title.Title
			if it.event.Title.Year > 0 {
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
	b.WriteString(confirmPromptStyle.Render("Are you sure?"))

	return b.String()
}

func hasFirstMeta(tl *model.Title) bool {
	return tl.USRating != "" || tl.Genres != "" || tl.Runtime > 0
}

func fmtRating(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v)
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
