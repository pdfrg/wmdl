package review

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmdl/internal/model"
)

// Strip metadata parentheticals before comparing scraped vs TMDB titles,
// so "(season 3)" differences don't trigger false mismatch warnings.
var reviewMetaParen = regexp.MustCompile(`(?i)\s*\((season\s+\d+|complete\s+.*|series\s+\d+|vol\..*)\)`)

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
	headerStyle            = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	emptyStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	approvedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	rejectedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	downloadedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	tagStyle               = lipgloss.NewStyle()
	titleStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	ratingsLine            = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	overviewStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	helpStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	keyStyle               = lipgloss.NewStyle()
	rtStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warnStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true).Padding(0, 2)
	sectionStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")).Padding(0, 2)
	confirmApprovedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Padding(0, 2)
	confirmRejectedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 2)
	confirmDownloadedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Padding(0, 2)
	confirmEmptyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 4)
	confirmPromptStyle     = lipgloss.NewStyle().Bold(true).Padding(0, 2)
)
