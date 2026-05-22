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
	selected []quality.ParsedRelease
	toggles  map[int]bool
	quit     bool
}

func NewSelector(title string, releases []quality.ParsedRelease) *Selector {
	return &Selector{
		title:    title,
		releases: releases,
		toggles:  make(map[int]bool),
	}
}

func (s *Selector) Run() ([]quality.ParsedRelease, error) {
	p := tea.NewProgram(s)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	m := final.(*Selector)
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
		switch msg.String() {
		case "s", "ctrl+c":
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

		case " ":
			s.toggles[s.cursor] = !s.toggles[s.cursor]

		case "enter":
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

		var info []string
		if r.Resolution > 0 {
			info = append(info, fmt.Sprintf("%dp", r.Resolution))
		}
		if r.HDR {
			info = append(info, "HDR")
		}
		info = append(info, r.Source)
		info = append(info, r.Codec)

		line := fmt.Sprintf("%s[%s] %-4s %s\n     %s  S: %d  %s",
			prefix,
			toggleMark,
			fmt.Sprintf("[%d]", idx+1),
			r.RawTitle,
			strings.Join(info, " ┃ "),
			r.Seeders,
			fmtSize(r.SizeBytes),
		)

		if r.ReleaseGroup != "" {
			line += fmt.Sprintf("  ┃  %s", r.ReleaseGroup)
		}
		if r.IndexerName != "" {
			line += fmt.Sprintf("  ┃  %s", r.IndexerName)
		}

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
		selHelpStyle.Render("[↑/↓] navigate  [space] toggle  [enter] confirm  [s] skip"),
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
