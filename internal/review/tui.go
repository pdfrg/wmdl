package review

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmd/internal/db"
	"github.com/pdfrg/wmd/internal/model"
)

type TUI struct {
	items    []db.EventWithTitle
	cursor   int
	database *db.DB
	width    int
	height   int
	approved []db.EventWithTitle
	err      error
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

	return &TUI{
		items:    events,
		database: database,
		height:   24,
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

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return t, tea.Quit

		case "j", "down":
			if t.cursor < len(t.items)-1 {
				t.cursor++
			}

		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
			}

		case "a":
			if t.cursor >= len(t.items) {
				return t, nil
			}
			it := t.items[t.cursor]
			ctx := context.Background()
			if err := t.database.UpdateReleaseEventStatus(ctx, it.Event.ID, model.StatusApproved); err != nil {
				t.err = err
				return t, tea.Quit
			}
			t.approved = append(t.approved, it)
			t.items = append(t.items[:t.cursor], t.items[t.cursor+1:]...)
			if t.cursor >= len(t.items) && t.cursor > 0 {
				t.cursor--
			}
			if len(t.items) == 0 {
				return t, tea.Quit
			}

		case "r":
			if t.cursor >= len(t.items) {
				return t, nil
			}
			it := t.items[t.cursor]
			ctx := context.Background()
			if err := t.database.UpdateReleaseEventStatus(ctx, it.Event.ID, model.StatusRejected); err != nil {
				t.err = err
				return t, tea.Quit
			}
			t.items = append(t.items[:t.cursor], t.items[t.cursor+1:]...)
			if t.cursor >= len(t.items) && t.cursor > 0 {
				t.cursor--
			}
			if len(t.items) == 0 {
				return t, tea.Quit
			}
		}
	}

	return t, nil
}

func (t *TUI) View() tea.View {
	if len(t.items) == 0 {
		return tea.NewView("No more releases to review.\n")
	}

	var b strings.Builder

	b.WriteString(headerStyle.Render(fmt.Sprintf("wmd review — %d pending", len(t.items))))
	b.WriteString("\n\n")

	for i, it := range t.items {
		prefix := "  "
		if i == t.cursor {
			prefix = "▸ "
		}

		title := it.Title.Title
		if it.Title.Year > 0 {
			title = fmt.Sprintf("%s (%d)", title, it.Title.Year)
		}

		mediaType := "movie"
		if it.Title.MediaType == model.MediaTypeTV {
			mediaType = "tv"
		}

		line := fmt.Sprintf("%s[%s] %s",
			prefix, mediaType, title,
		)

		// Second line: ratings and metadata
		var metaParts []string
		if r := styleRating(it.Title.TmdbRating); r != "" {
			metaParts = append(metaParts, "TMDB: "+r)
		}
		if it.Title.ImdbRating > 0 {
			metaParts = append(metaParts, fmt.Sprintf("IMDb: %.1f", it.Title.ImdbRating))
		}
		if it.Title.Genres != "" {
			metaParts = append(metaParts, it.Title.Genres)
		}
		if it.Title.Runtime > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%dh %dm", it.Title.Runtime/60, it.Title.Runtime%60))
		}
		if it.Title.ImdbID != "" {
			metaParts = append(metaParts, "IMDb: "+it.Title.ImdbID)
		}
		if len(metaParts) > 0 {
			line += "\n" + infoStyle.Render("  "+strings.Join(metaParts, "  ·  "))
		}

		// Third line: overview
		if it.Title.Overview != "" {
			overview := it.Title.Overview
			if len(overview) > 120 {
				overview = overview[:117] + "..."
			}
			line += "\n" + overviewStyle.Render("  "+overview)
		}

		// Fourth line: release type + notes
		releaseType := "physical"
		if it.Event.ReleaseType == model.ReleaseStreaming {
			releaseType = "streaming"
		}
		extra := "▸ " + releaseType

		var notes []string
		if it.Event.Notes != "" {
			notes = append(notes, it.Event.Notes)
		} else if it.Event.PreviousStatus == model.StatusRejected {
			notes = append(notes, "previously rejected")
		} else if it.Event.PreviousStatus == model.StatusDownloaded {
			notes = append(notes, "previously downloaded")
		}
		if len(notes) > 0 {
			extra += "  (" + strings.Join(notes, ", ") + ")"
		}
		line += "\n" + noteStyle.Render("  "+extra)

		if i == t.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(itemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[a] approve  [r] reject  [j/k] navigate  [q] quit"))

	return tea.NewView(b.String())
}

var (
	headerStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Padding(0, 1)
	itemStyle     = lipgloss.NewStyle().Padding(0, 1)
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Padding(0, 2)
	overviewStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Padding(0, 2).Width(70)
	noteStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 2)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func styleRating(rating float64) string {
	if rating == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f ★", rating)
}
