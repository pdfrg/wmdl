package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePosterMode(t *testing.T) {
	tests := []struct {
		input string
		want  PosterMode
	}{
		{"kitty", PosterKitty},
		{"KITTY", PosterKitty},
		{"text", PosterText},
		{"off", PosterOff},
		{"auto", PosterAuto},
		{"", PosterAuto},
		{"unknown", PosterAuto},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, ParsePosterMode(tt.input))
		})
	}
}

func TestCenterText(t *testing.T) {
	tests := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{"shorter_odd_pad", "a", 5, "  a  "},
		{"shorter_even_pad", "ab", 6, "  ab  "},
		{"exact", "hello", 5, "hello"},
		{"longer_truncated", "hello", 3, "hel"},
		{"empty", "", 4, "    "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, centerText(tt.s, tt.w))
		})
	}
}

func TestRenderTextPlaceholder(t *testing.T) {
	t.Run("default_size", func(t *testing.T) {
		result := renderTextPlaceholder(30, 10)
		lines := strings.Split(result, "\n")
		assert.Equal(t, 10, len(lines))
		assert.True(t, strings.HasPrefix(lines[0], "┌"))
		assert.True(t, strings.HasSuffix(lines[0], "┐"))
		assert.True(t, strings.HasPrefix(lines[9], "└"))
		// Should contain centered POSTER / NOT / AVAILABLE
		assert.Contains(t, result, "POSTER")
		assert.Contains(t, result, "NOT")
		assert.Contains(t, result, "AVAILABLE")
	})

	t.Run("minimum_size", func(t *testing.T) {
		result := renderTextPlaceholder(3, 2)
		lines := strings.Split(result, "\n")
		assert.Equal(t, 4, len(lines), "should enforce minimum cols=10, rows=4")
	})

	t.Run("small_cols", func(t *testing.T) {
		result := renderTextPlaceholder(6, 6)
		lines := strings.Split(result, "\n")
		assert.True(t, len(lines[0]) > 5)
	})
}

func TestCountryFlag(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"us", "\U0001F1FA\U0001F1F8"},
		{"US", "\U0001F1FA\U0001F1F8"},
		{"gb", "\U0001F1EC\U0001F1E7"},
		{"jp", "\U0001F1EF\U0001F1F5"},
		{"", ""},
		{"usa", ""},
		{"u", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			assert.Equal(t, tt.want, countryFlag(tt.code))
		})
	}
}

func TestLanguageName(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"en", "English"},
		{"EN", "English"},
		{"fr", "French"},
		{"ja", "Japanese"},
		{"xx", "XX"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			assert.Equal(t, tt.want, languageName(tt.code))
		})
	}

	t.Run("all_known_codes_return_something", func(t *testing.T) {
		for code := range languageNames {
			name := languageName(code)
			assert.NotEmpty(t, name, "code %q should have a name", code)
			assert.NotEqual(t, strings.ToUpper(code), name, "code %q should not be fallback", code)
		}
	})
}
