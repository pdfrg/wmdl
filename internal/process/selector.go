package process

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmd/internal/quality"
)

type Selector struct {
	title    string
	releases []quality.ParsedRelease
	cursor   int
	width    int
	height   int
	chosen   *quality.ParsedRelease
	quit     bool
}

func NewSelector(title string, releases []quality.ParsedRelease) *Selector {
	return &Selector{
		title:    title,
		releases: releases,
	}
}

func (s *Selector) Run() (*quality.ParsedRelease, error) {
	p := tea.NewProgram(s)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := final.(*Selector)
	return m.chosen, nil
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
		switch msg.String() {
		case "q", "ctrl+c":
			s.quit = true
			return s, tea.Quit

		case "j", "down":
			if s.cursor < len(s.releases)-1 {
				s.cursor++
			}

		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}

		case "enter", " ":
			if s.cursor < len(s.releases) {
				s.chosen = &s.releases[s.cursor]
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

		prefix := "  "
		if idx == s.cursor {
			prefix = "▸ "
		}

		var info []string
		if r.Resolution > 0 {
			info = append(info, fmt.Sprintf("%dp", r.Resolution))
		}
		if r.HDR {
			info = append(info, "HDR")
		}
		info = append(info, r.Source)
		info = append(info, r.Codec)

		line := fmt.Sprintf("%s%-4s %s\n     %s  S: %d  %s",
			prefix,
			fmt.Sprintf("[%d]", idx+1),
			r.RawTitle,
			strings.Join(info, " ┃ "),
			r.Seeders,
			fmtSize(r.SizeBytes),
		)

		if r.ReleaseGroup != "" {
			line += fmt.Sprintf("  ┃  %s", r.ReleaseGroup)
		}

		if idx == s.cursor {
			b.WriteString(selSelectedStyle.Render(line))
		} else {
			b.WriteString(selItemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	footer := fmt.Sprintf("\n%s [%d/%d]",
		selHelpStyle.Render("[↑/↓] navigate  [enter] select  [q] skip"),
		s.cursor+1, len(s.releases),
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
