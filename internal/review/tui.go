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
	n := 3 // title, release type, blank separator
	if hasMeta(ewt.Title) {
		n++
	}
	if ewt.Title.Overview != "" {
		n += overviewLines(ewt.Title.Overview)
	}
	return n
}

func hasMeta(tl *model.Title) bool {
	return tl.TmdbRating > 0 || tl.ImdbRating > 0 ||
		tl.RTCriticsScore > 0 || tl.RTAudienceScore > 0 ||
		tl.YoutubeViews > 0 || tl.Genres != "" ||
		tl.Runtime > 0 || tl.ImdbID != ""
}

func overviewLines(s string) int {
	if s == "" {
		return 0
	}
	// overviewStyle has Width(70) which lipgloss wraps at 70 columns
	return max(1, int(math.Ceil(float64(len(s))/70)))
}

func (t *TUI) renderItem(it *itemState, cursor bool) string {
	var b strings.Builder

	prefix := "  "
	decoration := ""
	titleStyle := itemStyle

	if cursor {
		prefix = "▸ "
	}
	switch it.decision {
	case decisionApproved:
		decoration = "✓ "
		titleStyle = approvedStyle
	case decisionRejected:
		decoration = "✗ "
		titleStyle = rejectedStyle
	}

	title := it.event.Title.Title
	if it.event.Title.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, it.event.Title.Year)
	}

	mediaType := "movie"
	if it.event.Title.MediaType == model.MediaTypeTV {
		mediaType = "tv"
	}

	b.WriteString(titleStyle.Render(fmt.Sprintf("%s%s[%s] %s",
		prefix, decoration, mediaType, title,
	)))

	// Meta line
	var metaParts []string
	if r := styleRating(it.event.Title.TmdbRating); r != "" {
		metaParts = append(metaParts, "TMDB: "+r)
	}
	if it.event.Title.ImdbRating > 0 {
		metaParts = append(metaParts, fmt.Sprintf("IMDb: %.1f", it.event.Title.ImdbRating))
	}
	if it.event.Title.RTCriticsScore > 0 || it.event.Title.RTAudienceScore > 0 {
		rt := fmt.Sprintf("RT: %.0f/%.0f", it.event.Title.RTCriticsScore, it.event.Title.RTAudienceScore)
		metaParts = append(metaParts, rt)
	}
	if it.event.Title.YoutubeViews > 0 {
		metaParts = append(metaParts, fmt.Sprintf("▶ %s", formatViews(it.event.Title.YoutubeViews)))
	}
	if it.event.Title.Genres != "" {
		metaParts = append(metaParts, it.event.Title.Genres)
	}
	if it.event.Title.Runtime > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%dh %dm", it.event.Title.Runtime/60, it.event.Title.Runtime%60))
	}
	if it.event.Title.ImdbID != "" {
		metaParts = append(metaParts, "IMDb: "+it.event.Title.ImdbID)
	}
	if len(metaParts) > 0 {
		b.WriteString("\n")
		b.WriteString(infoStyle.Render("  " + strings.Join(metaParts, "  ·  ")))
	}

	// Overview
	if it.event.Title.Overview != "" {
		b.WriteString("\n")
		b.WriteString(overviewStyle.Render("  " + it.event.Title.Overview))
	}

	// Release type + notes
	releaseType := "physical"
	if it.event.Event.ReleaseType == model.ReleaseStreaming {
		releaseType = "streaming"
	}
	extra := "▸ " + releaseType

	var notes []string
	if it.event.Event.Notes != "" {
		notes = append(notes, it.event.Event.Notes)
	} else if it.event.Event.PreviousStatus == model.StatusRejected {
		notes = append(notes, "previously rejected")
	} else if it.event.Event.PreviousStatus == model.StatusDownloaded {
		notes = append(notes, "previously downloaded")
	}
	if len(notes) > 0 {
		extra += "  (" + strings.Join(notes, ", ") + ")"
	}
	b.WriteString("\n")
	b.WriteString(noteStyle.Render("  " + extra))
	b.WriteString("\n")

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
	headerStyle         = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	approvedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 1)
	rejectedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 1)
	itemStyle           = lipgloss.NewStyle().Padding(0, 1)
	infoStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Padding(0, 2)
	overviewStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 2).Width(70)
	noteStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 2)
	helpStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sectionStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")).Padding(0, 2)
	confirmApprovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 2)
	confirmRejectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 2)
	confirmEmptyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 4)
	confirmPromptStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 2)
)

func styleRating(rating float64) string {
	if rating == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f ★", rating)
}

func formatViews(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
