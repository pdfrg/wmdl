package process

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmdl/internal/quality"
)

type colWidths struct {
	resolution    int
	hdr           int
	source        int
	codec         int
	seeders       int
	size          int
	releaseGroup  int
	indexerName   int
}

type Selector struct {
	title     string
	releases  []quality.ParsedRelease
	cursor    int
	width     int
	height    int
	selected  []quality.ParsedRelease
	toggles   map[int]bool
	quit      bool
	abort     bool
	colWidths colWidths
}

func NewSelector(title string, releases []quality.ParsedRelease) *Selector {
	s := &Selector{
		title:    title,
		releases: releases,
		toggles:  make(map[int]bool),
	}
	for _, r := range releases {
		if w := len(fmt.Sprintf("%dp", r.Resolution)); w > s.colWidths.resolution {
			s.colWidths.resolution = w
		}
		hdrStr := ""
		if r.HDR {
			hdrStr = "HDR"
		}
		if len(hdrStr) > s.colWidths.hdr {
			s.colWidths.hdr = len(hdrStr)
		}
		if len(r.Source) > s.colWidths.source {
			s.colWidths.source = len(r.Source)
		}
		if len(r.Codec) > s.colWidths.codec {
			s.colWidths.codec = len(r.Codec)
		}
		if w := len(fmt.Sprintf("%d", r.Seeders)); w > s.colWidths.seeders {
			s.colWidths.seeders = w
		}
		if w := len(fmtSize(r.SizeBytes)); w > s.colWidths.size {
			s.colWidths.size = w
		}
		if len(r.ReleaseGroup) > s.colWidths.releaseGroup {
			s.colWidths.releaseGroup = len(r.ReleaseGroup)
		}
		if len(r.IndexerName) > s.colWidths.indexerName {
			s.colWidths.indexerName = len(r.IndexerName)
		}
	}
	return s
}

func (s *Selector) Run() ([]quality.ParsedRelease, error) {
	p := tea.NewProgram(s)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := final.(*Selector)
	if m.abort {
		return nil, ErrAbort
	}
	return m.selected, nil
}

func (s *Selector) Init() tea.Cmd {
	return nil
}

func (s *Selector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height

	case tea.KeyPressMsg:
		switch {
		case msg.String() == "s", msg.String() == "ctrl+c":
			s.quit = true
			return s, tea.Quit

		case msg.String() == "q":
			s.abort = true
			return s, tea.Quit

		case msg.String() == "j", msg.String() == "down":
			if s.cursor < len(s.releases)-1 {
				s.cursor++
			}

		case msg.String() == "k", msg.String() == "up":
			if s.cursor > 0 {
				s.cursor--
			}

		case msg.String() == " ", msg.String() == "space":
			s.toggles[s.cursor] = !s.toggles[s.cursor]

		case msg.String() == "enter":
			var toggled []int
			for idx, on := range s.toggles {
				if on {
					toggled = append(toggled, idx)
				}
			}
			if len(toggled) == 0 {
				// nothing toggled, stay
				break
			}
			for _, idx := range toggled {
				s.selected = append(s.selected, s.releases[idx])
			}
			return s, tea.Quit
		}
	}

	return s, nil
}

func (s *Selector) View() tea.View {
	var b strings.Builder

	b.WriteString(selHeaderStyle.Render(fmt.Sprintf("Select release for: %s", s.title)))
	b.WriteString("\n\n")

	maxY := s.height - 5
	if maxY < 1 {
		maxY = 1
	}
	start := 0
	if s.cursor >= maxY {
		start = s.cursor - maxY + 1
	}
	end := start + maxY
	if end > len(s.releases) {
		end = len(s.releases)
	}

	for i, r := range s.releases[start:end] {
		idx := start + i

		toggleMark := " "
		if s.toggles[idx] {
			toggleMark = "x"
		}

		prefix := "  "
		if idx == s.cursor {
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
			s.colWidths.resolution, resStr,
			s.colWidths.hdr, hdrStr,
			s.colWidths.source, r.Source,
			s.colWidths.codec, r.Codec,
			s.colWidths.releaseGroup, r.ReleaseGroup,
			s.colWidths.indexerName, r.IndexerName,
		)

		line := fmt.Sprintf("%s[%s] %-4s %s\n     %s  S: %*d  %-*s",
			prefix,
			toggleMark,
			fmt.Sprintf("[%d]", idx+1),
			r.RawTitle,
			attrs,
			s.colWidths.seeders, r.Seeders,
			s.colWidths.size, fmtSize(r.SizeBytes),
		)

		if idx == s.cursor {
			b.WriteString(selSelectedStyle.Render(line))
		} else {
			b.WriteString(selItemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	toggledCount := 0
	for _, on := range s.toggles {
		if on {
			toggledCount++
		}
	}

	footerExtra := ""
	if toggledCount > 0 {
		footerExtra = fmt.Sprintf("  %d selected", toggledCount)
	}

	footer := fmt.Sprintf("\n%s%s",
		selHelpStyle.Render("[↑/↓] navigate  [space] toggle  [enter] confirm  [s] skip  [q] abort"),
		selHelpStyle.Render(fmt.Sprintf("  [%d/%d]%s", s.cursor+1, len(s.releases), footerExtra)),
	)
	b.WriteString(footer)

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

var (
	selHeaderStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Padding(0, 1)
	selItemStyle     = lipgloss.NewStyle().Padding(0, 1)
	selHelpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func fmtSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
