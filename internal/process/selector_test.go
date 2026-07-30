package process

import (
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/quality"
)

func TestNewSelector(t *testing.T) {
	releases := []quality.ParsedRelease{
		{RawTitle: "Release A", Resolution: 1080, Source: "BluRay", Seeders: 10, SizeBytes: 5 << 30, IndexerName: "Indexer1"},
		{RawTitle: "Release B", Resolution: 2160, HDR: true, Source: "WEB-DL", Seeders: 5, SizeBytes: 2 << 30, IndexerName: "Indexer2"},
		{RawTitle: "Release C", Resolution: 720, Source: "HDTV", Seeders: 0, SizeBytes: 1 << 20, ReleaseGroup: "GRP", IndexerName: "Indexer3"},
	}

	s := NewSelector("Test Movie", releases)
	assert.Equal(t, "Test Movie", s.title)
	assert.Len(t, s.releases, 3)
	assert.Equal(t, 0, s.cursor)
	assert.NotNil(t, s.toggles)
}

func TestSelectorInit(t *testing.T) {
	s := NewSelector("Test", nil)
	assert.Nil(t, s.Init())
}

func TestSelectorUpdateNavigation(t *testing.T) {
	releases := make([]quality.ParsedRelease, 5)
	for i := range releases {
		releases[i] = quality.ParsedRelease{RawTitle: "Release"}
	}
	s := NewSelector("Test", releases)

	// Initial cursor
	assert.Equal(t, 0, s.cursor)

	// Move down
	s.Update(tea.KeyPressMsg{Text: "j"})
	assert.Equal(t, 1, s.cursor)

	// Move down with "down"
	s.Update(tea.KeyPressMsg{Text: "down"})
	assert.Equal(t, 2, s.cursor)

	// Move up
	s.Update(tea.KeyPressMsg{Text: "k"})
	assert.Equal(t, 1, s.cursor)

	// Move up with "up"
	s.Update(tea.KeyPressMsg{Text: "up"})
	assert.Equal(t, 0, s.cursor)

	// Can't go below 0
	s.Update(tea.KeyPressMsg{Text: "up"})
	assert.Equal(t, 0, s.cursor)
}

func TestSelectorUpdateBoundary(t *testing.T) {
	releases := make([]quality.ParsedRelease, 2)
	for i := range releases {
		releases[i] = quality.ParsedRelease{RawTitle: "Release"}
	}
	s := NewSelector("Test", releases)

	// Move past end
	s.cursor = 1
	s.Update(tea.KeyPressMsg{Text: "j"})
	assert.Equal(t, 1, s.cursor) // stays at end
}

func TestSelectorUpdateToggle(t *testing.T) {
	releases := make([]quality.ParsedRelease, 3)
	for i := range releases {
		releases[i] = quality.ParsedRelease{RawTitle: "Release"}
	}
	s := NewSelector("Test", releases)

	// Toggle item 0
	s.Update(tea.KeyPressMsg{Code: ' '})
	assert.True(t, s.toggles[0])

	// Toggle again (deselect)
	s.Update(tea.KeyPressMsg{Code: ' '})
	assert.False(t, s.toggles[0])
}

func TestSelectorUpdateEnter(t *testing.T) {
	releases := make([]quality.ParsedRelease, 3)
	for i := range releases {
		releases[i] = quality.ParsedRelease{RawTitle: "Release"}
	}
	s := NewSelector("Test", releases)

	// Enter with nothing toggled should not quit
	_, cmd := s.Update(tea.KeyPressMsg{Text: "enter"})
	assert.False(t, s.quit)
	assert.Nil(t, cmd)

	// Toggle and enter
	s.toggles[1] = true
	m2, cmd2 := s.Update(tea.KeyPressMsg{Text: "enter"})
	assert.Len(t, s.selected, 1)
	assert.Equal(t, "Release", s.selected[0].RawTitle)
	assert.NotNil(t, cmd2) // tea.Quit() sent
	_ = m2
}

func TestSelectorUpdateSkip(t *testing.T) {
	s := NewSelector("Test", nil)
	_, cmd := s.Update(tea.KeyPressMsg{Text: "s"})
	assert.True(t, s.quit)
	assert.NotNil(t, cmd)
}

func TestSelectorUpdateAbort(t *testing.T) {
	s := NewSelector("Test", nil)
	_, cmd := s.Update(tea.KeyPressMsg{Text: "q"})
	assert.True(t, s.abort)
	assert.NotNil(t, cmd)
}

func TestSelectorWindowSize(t *testing.T) {
	s := NewSelector("Test", nil)
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	assert.Equal(t, 100, s.width)
	assert.Equal(t, 40, s.height)
}

func TestFmtSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1 << 20, "1.0 MB"},
		{5 << 30, "5.0 GB"},
		{3 << 40, "3072.0 GB"}, // beyond GB — fmtSize only goes up to GB
	}
	for _, tt := range tests {
		got := fmtSize(tt.bytes)
		assert.Equal(t, tt.want, got, "fmtSize(%d)", tt.bytes)
	}
}
