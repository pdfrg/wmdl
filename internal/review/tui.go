package review

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/viewport"
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

type TUI struct {
	items    []itemState
	cursor   int
	phase    phase
	database *db.DB
	width    int
	height   int
	approved []db.EventWithTitle
	err      error
	vp       viewport.Model
}

func NewReviewTUI(database *db.DB) (*TUI, error) {
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

	vp := viewport.New()
	vp.SetHeight(20)
	vp.SetWidth(80)

	return &TUI{
		items:    items,
		database: database,
		height:   24,
		vp:       vp,
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
	return nil
}

func (t *TUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width = msg.Width
		t.height = msg.Height
		t.vp.SetHeight(max(1, t.height-4))
		t.vp.SetWidth(t.width)

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

func (t *TUI) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return t, tea.Quit

	case "j", "down":
		if t.cursor < len(t.items)-1 {
			t.cursor++
			t.syncViewport()
		}

	case "k", "up":
		if t.cursor > 0 {
			t.cursor--
			t.syncViewport()
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
			exec.Command("xdg-open", rtURL).Start()
		}

	case "enter":
		for _, it := range t.items {
			if it.decision != decisionNone {
				t.phase = phaseConfirm
				t.cursor = 0
				t.vp.GotoTop()
				break
			}
		}
	}

	return t, nil
}

func (t *TUI) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return t, tea.Quit

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
				return t, tea.Quit
			}
			if it.decision == decisionApproved {
				t.approved = append(t.approved, it.event)
			}
		}
		return t, tea.Quit

	case "n":
		t.phase = phaseReview
		t.cursor = 0
		t.vp.GotoTop()
	}

	return t, nil
}

func (t *TUI) syncViewport() {
	y := 0
	for i := 0; i < t.cursor && i < len(t.items); i++ {
		y += t.linesForItem(&t.items[i].event)
	}
	itemH := t.linesForItem(&t.items[t.cursor].event)
	vpY := t.vp.YOffset()
	vpH := t.vp.Height()

	if y < vpY {
		t.vp.SetYOffset(y)
	} else if y+itemH > vpY+vpH {
		t.vp.SetYOffset(y + itemH - vpH)
	}
}

func (t *TUI) linesForItem(ewt *db.EventWithTitle) int {
	n := 1 // title line
	if hasFirstMeta(ewt.Title) {
		n++
	}
	// ratings line always shown
	n++
	if ewt.Title.Overview != "" {
		n += overviewLines(ewt.Title.Overview)
	}
	return n + 1 // blank separator between items
}

func hasFirstMeta(tl *model.Title) bool {
	return tl.USRating != "" || tl.Genres != "" || tl.Runtime > 0
}

func overviewLines(s string) int {
	if s == "" {
		return 0
	}
	return max(1, int(math.Ceil(float64(len(s))/70)))
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

func (t *TUI) renderItem(it *itemState, cursor bool) string {
	tl := it.event.Title
	ev := it.event.Event

	var b strings.Builder

	prefix := "  "
	decoration := ""
	decorationStyle := tagStyle

	if cursor {
		prefix = "▸ "
	}
	switch it.decision {
	case decisionApproved:
		decoration = "✓ "
		decorationStyle = approvedStyle
	case decisionRejected:
		decoration = "✗ "
		decorationStyle = rejectedStyle
	}

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

	// Title line: [movie|tv] Title (Year)     streaming|physical
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

	// Tag/style prefix and release type use decorationStyle, title in yellow
	tag := fmt.Sprintf("%s%s[%s] ", prefix, decoration, mediaType)
	titleText := fmt.Sprintf("%s%s", title, extra)
	releaseTag := fmt.Sprintf("  %s", releaseType)
	b.WriteString(decorationStyle.Render(tag))
	b.WriteString(titleStyle.Render(titleText))
	b.WriteString(decorationStyle.Render(releaseTag))

	// Line 2: (US rating) · genre(s) · runtime (only if any present)
	if hasFirstMeta(tl) {
		var parts []string
		if tl.USRating != "" {
			parts = append(parts, "("+tl.USRating+")")
		}
		if tl.Genres != "" {
			parts = append(parts, tl.Genres)
		}
		if tl.Runtime > 0 {
			parts = append(parts, fmtRuntime(tl.Runtime))
		}
		b.WriteString("\n")
		b.WriteString(infoStyle.Render("  " + strings.Join(parts, " · ")))
	}

	// Line 3: TMDB: score · IMDb: score · MC: score · YT: views · 🍅 critics · 🍿 audience
	ratings := fmt.Sprintf("TMDB: %s · IMDb: %s · MC: %s · YT: %s · 🍅 %s · 🍿 %s",
		fmtRating(tl.TmdbRating),
		fmtRating(tl.ImdbRating),
		fmtRating(tl.MetacriticScore),
		fmtViews(tl.YoutubeViews),
		fmtPct(tl.RTCriticsScore),
		fmtPct(tl.RTAudienceScore),
	)
	b.WriteString("\n")
	b.WriteString(ratingsLine.Render("  " + ratings))

	// Overview
	if tl.Overview != "" {
		b.WriteString("\n")
		b.WriteString(overviewStyle.Render("  " + tl.Overview))
	}

	b.WriteString("\n\n")

	return b.String()
}

func (t *TUI) buildReviewContent() string {
	var b strings.Builder
	for i := range t.items {
		b.WriteString(t.renderItem(&t.items[i], i == t.cursor))
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

func (t *TUI) View() tea.View {
	var content string
	var help string

	switch t.phase {
	case phaseReview:
		content = t.buildReviewContent()
		help = "[a] approve  [r] reject  [u] undo  [enter] confirm  [o] open RT  [j/k] navigate  [q] quit"
	case phaseConfirm:
		content = t.buildConfirmContent()
		help = "[y] continue  [n] return"
	}

	t.vp.SetContent(content)

	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("wmd review — %d pending", len(t.items))))
	b.WriteString("\n\n")
	b.WriteString(t.vp.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(help))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

var (
	headerStyle          = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	approvedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 1)
	rejectedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 1)
	tagStyle             = lipgloss.NewStyle().Padding(0, 1)
	titleStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Padding(0, 1)
	infoStyle            = lipgloss.NewStyle().Padding(0, 2)
	ratingsLine          = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Padding(0, 2)
	overviewStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 2).Width(70)
	helpStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sectionStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")).Padding(0, 2)
	confirmApprovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 2)
	confirmRejectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 2)
	confirmEmptyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 4)
	confirmPromptStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 2)
)
