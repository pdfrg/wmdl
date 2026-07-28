package process

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmdl/internal/quality"
)

// BatchItem represents a single title the user needs to pick torrents for.
type BatchItem struct {
	ID       string
	Label    string
	Phase3   bool
	Releases []quality.ParsedRelease
	Selected []quality.ParsedRelease
	Skipped  bool
	Trial    bool

	SearchResult *SearchResult
	MusicResult  *MusicSearchResult
	BookResult   *BookSearchResult
}

// BatchPicker presents all items in a single TUI session.
type BatchPicker struct {
	Items  []*BatchItem
	cursor int
	width  int
	height int

	detail      bool
	selCursor   int
	selToggles  map[int]bool
	selColWidth colWidths

	abort bool
}

func NewBatchPicker(items []*BatchItem) *BatchPicker {
	return &BatchPicker{
		Items:      items,
		selToggles: make(map[int]bool),
	}
}

func (bp *BatchPicker) Run() (*BatchPicker, error) {
	p := tea.NewProgram(bp)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := final.(*BatchPicker)
	if m.abort {
		return m, ErrAbort
	}
	return m, nil
}

func (bp *BatchPicker) Init() tea.Cmd {
	return nil
}

func (bp *BatchPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		bp.width = msg.Width
		bp.height = msg.Height

	case tea.KeyPressMsg:
		if bp.detail {
			return bp.updateDetail(msg)
		}
		return bp.updateList(msg)
	}

	return bp, nil
}

func (bp *BatchPicker) allDecided() bool {
	for _, item := range bp.Items {
		if !item.Skipped && len(item.Selected) == 0 {
			return false
		}
	}
	return true
}

func (bp *BatchPicker) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "j", msg.String() == "down":
		if bp.cursor < len(bp.Items)-1 {
			bp.cursor++
		}

	case msg.String() == "k", msg.String() == "up":
		if bp.cursor > 0 {
			bp.cursor--
		}

	case msg.String() == "enter":
		item := bp.Items[bp.cursor]
		if len(item.Releases) > 0 {
			bp.enterDetail()
		}

	case msg.String() == "s", msg.String() == "ctrl+c":
		item := bp.Items[bp.cursor]
		item.Skipped = true
		item.Selected = nil

	case msg.String() == "c":
		if bp.allDecided() {
			return bp, tea.Quit
		}

	case msg.String() == "q":
		bp.abort = true
		return bp, tea.Quit
	}

	return bp, nil
}

func (bp *BatchPicker) enterDetail() {
	bp.detail = true
	item := bp.Items[bp.cursor]
	bp.selCursor = 0
	bp.selToggles = make(map[int]bool)
	for i, r := range item.Releases {
		for _, sel := range item.Selected {
			if r.Guid == sel.Guid {
				bp.selToggles[i] = true
				break
			}
		}
	}
	bp.selColWidth = computeColWidths(item.Releases)
}

func (bp *BatchPicker) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	item := bp.Items[bp.cursor]

	switch {
	case msg.String() == "j", msg.String() == "down":
		if bp.selCursor < len(item.Releases)-1 {
			bp.selCursor++
		}

	case msg.String() == "k", msg.String() == "up":
		if bp.selCursor > 0 {
			bp.selCursor--
		}

	case msg.String() == " ", msg.String() == "space":
		bp.selToggles[bp.selCursor] = !bp.selToggles[bp.selCursor]

	case msg.String() == "enter":
		var toggled []int
		for idx, on := range bp.selToggles {
			if on {
				toggled = append(toggled, idx)
			}
		}
		if len(toggled) == 0 {
			break
		}
		item.Skipped = false
		item.Selected = nil
		for _, idx := range toggled {
			item.Selected = append(item.Selected, item.Releases[idx])
		}
		bp.detail = false

	case msg.String() == "s":
		item.Skipped = true
		item.Selected = nil
		bp.detail = false

	case msg.String() == "esc":
		bp.detail = false

	case msg.String() == "q":
		bp.abort = true
		return bp, tea.Quit
	}

	return bp, nil
}

func (bp *BatchPicker) View() tea.View {
	if bp.detail {
		return bp.detailView()
	}
	return bp.listView()
}

func (bp *BatchPicker) listView() tea.View {
	var b strings.Builder

	b.WriteString(selHeaderStyle.Render("Select releases"))
	b.WriteString("\n\n")

	decided := 0
	for _, item := range bp.Items {
		if item.Skipped || len(item.Selected) > 0 {
			decided++
		}
	}

	maxY := bp.height - 6
	if maxY < 1 {
		maxY = 1
	}

	start := 0
	if bp.cursor >= maxY {
		start = bp.cursor - maxY + 1
	}
	end := start + maxY
	if end > len(bp.Items) {
		end = len(bp.Items)
	}

	// Track whether we've printed the phase3 header yet
	phase3HeaderPrinted := false

	for i, item := range bp.Items[start:end] {
		idx := start + i

		// Print phase3 section header if transitioning
		if item.Phase3 && !phase3HeaderPrinted {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(selHelpStyle.Render("  ── Optional: previous seasons and missing collection/series items: ──"))
			b.WriteString("\n\n")
			phase3HeaderPrinted = true
		}

		prefix := "  "
		if idx == bp.cursor {
			prefix = "▸ "
		}

		status := ""
		if item.Skipped {
			status = statusSkippedStyle.Render("[skipped]")
		} else if len(item.Selected) > 0 {
			status = statusSelectedStyle.Render(fmt.Sprintf("[selected %d]", len(item.Selected)))
		} else {
			status = statusPendingStyle.Render("[pending]")
		}

		releaseCount := ""
		if len(item.Releases) > 0 {
			releaseCount = selHelpStyle.Render(fmt.Sprintf(" ▶ %d releases", len(item.Releases)))
		}

		line := fmt.Sprintf("%s%s  %s%s", prefix, status, item.Label, releaseCount)

		if idx == bp.cursor {
			b.WriteString(selSelectedStyle.Render(line))
		} else {
			b.WriteString(selItemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	helpText := "[↑/↓] navigate  [enter] pick release  [s] skip"
	if bp.allDecided() {
		helpText += "  [c] continue"
	} else {
		helpText += "  [q] quit pipeline"
	}
	footer := fmt.Sprintf("\n%s",
		selHelpStyle.Render(helpText),
	)
	if decided > 0 {
		footer += selHelpStyle.Render(fmt.Sprintf("  %d/%d decided", decided, len(bp.Items)))
	}
	b.WriteString(footer)

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (bp *BatchPicker) detailView() tea.View {
	item := bp.Items[bp.cursor]
	var b strings.Builder

	b.WriteString(selHeaderStyle.Render(fmt.Sprintf("Select release for: %s", item.Label)))
	b.WriteString("\n\n")

	maxY := bp.height - 5
	if maxY < 1 {
		maxY = 1
	}
	start := 0
	if bp.selCursor >= maxY {
		start = bp.selCursor - maxY + 1
	}
	end := start + maxY
	if end > len(item.Releases) {
		end = len(item.Releases)
	}

	cw := bp.selColWidth

	for i, r := range item.Releases[start:end] {
		idx := start + i

		toggleMark := " "
		if bp.selToggles[idx] {
			toggleMark = "x"
		}

		prefix := "  "
		if idx == bp.selCursor {
			prefix = "▸ "
		}

		resStr := ""
		if r.Resolution > 0 {
			resStr = fmt.Sprintf("%dp", r.Resolution)
		}
		hdrStr := ""
		if r.HDR {
			hdrStr = "HDR"
		}

		attrs := fmt.Sprintf("%-*s ┃ %-*s ┃ %-*s ┃ %-*s ┃ %-*s ┃ %-*s",
			cw.resolution, resStr,
			cw.hdr, hdrStr,
			cw.source, r.Source,
			cw.codec, r.Codec,
			cw.releaseGroup, r.ReleaseGroup,
			cw.indexerName, r.IndexerName,
		)

		line := fmt.Sprintf("%s[%s] %-4s %s\n     %s  S: %*d  %-*s",
			prefix,
			toggleMark,
			fmt.Sprintf("[%d]", idx+1),
			r.RawTitle,
			attrs,
			cw.seeders, r.Seeders,
			cw.size, fmtSize(r.SizeBytes),
		)

		if idx == bp.selCursor {
			b.WriteString(selSelectedStyle.Render(line))
		} else {
			b.WriteString(selItemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	toggledCount := 0
	for _, on := range bp.selToggles {
		if on {
			toggledCount++
		}
	}

	footerExtra := ""
	if toggledCount > 0 {
		footerExtra = fmt.Sprintf("  %d selected", toggledCount)
	}

	footer := fmt.Sprintf("\n%s%s",
		selHelpStyle.Render("[↑/↓] navigate  [space] toggle  [enter] confirm  [s] skip  [esc] back  [q] abort"),
		selHelpStyle.Render(fmt.Sprintf("  [%d/%d]%s", bp.selCursor+1, len(item.Releases), footerExtra)),
	)
	b.WriteString(footer)

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func computeColWidths(releases []quality.ParsedRelease) colWidths {
	var cw colWidths
	for _, r := range releases {
		if w := len(fmt.Sprintf("%dp", r.Resolution)); w > cw.resolution {
			cw.resolution = w
		}
		hdrStr := ""
		if r.HDR {
			hdrStr = "HDR"
		}
		if len(hdrStr) > cw.hdr {
			cw.hdr = len(hdrStr)
		}
		if len(r.Source) > cw.source {
			cw.source = len(r.Source)
		}
		if len(r.Codec) > cw.codec {
			cw.codec = len(r.Codec)
		}
		if w := len(fmt.Sprintf("%d", r.Seeders)); w > cw.seeders {
			cw.seeders = w
		}
		if w := len(fmtSize(r.SizeBytes)); w > cw.size {
			cw.size = w
		}
		if len(r.ReleaseGroup) > cw.releaseGroup {
			cw.releaseGroup = len(r.ReleaseGroup)
		}
		if len(r.IndexerName) > cw.indexerName {
			cw.indexerName = len(r.IndexerName)
		}
	}
	return cw
}

var (
	statusPendingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	statusSkippedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("59"))
)
