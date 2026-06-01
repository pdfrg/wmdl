package quality

import "testing"

func TestParseSeasonNumber(t *testing.T) {
	tests := []struct {
		title string
		want  int
	}{
		{"The Show (season 1)", 1},
		{"Furies (season 2)", 2},
		{"INVINCIBLE (season 4)", 4},
		{"Call the Midwife: Season Fifteen", 15},
		{"Fallout: Complete Second Season", 2},
		{"Show Name S02E03", 2},
		{"Show S1 Complete 1080p", 1},
		{"Show Name - 2nd Season", 2},
		{"Show Name - 3rd Season", 3},
		{"Regular Show (season 1)", 1},
		{"New Show", 1},
		{"Movie Title 2025", 1},
		{"Show: Season One", 1},
		{"Show: Complete First Season", 1},
		{"Show: Complete Third Season", 3},
		{"Series: Season Twenty", 20},
		{"S04E01 Episode Name", 4},
		{"Show Name (2025)", 1},
		{"Show Name (Season 12)", 12},
		// "Final Season" doesn't parse as a season number → defaults to 1
		{"Show Name Final Season", 1},
		// "season N episode M" format
		{"Show Season 02 Episode 05 1080p", 2},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := ParseSeasonNumber(tt.title)
			if got != tt.want {
				t.Errorf("ParseSeasonNumber(%q) = %d, want %d", tt.title, got, tt.want)
			}
		})
	}
}

func TestStripSeason(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Eva Lasting (season 4)", "Eva Lasting"},
		{"Furies (season 2)", "Furies"},
		{"INVINCIBLE (season 4)", "INVINCIBLE"},
		{"Call the Midwife: Season Fifteen", "Call the Midwife"},
		{"Fallout: Complete Second Season", "Fallout"},
		{"Regular Show (season 1)", "Regular Show"},
		{"Show: Season One", "Show"},
		{"Show: Complete First Season", "Show"},
		{"Show: Complete Third Season", "Show"},
		{"Series: Season Twenty", "Series"},
		{"The Show (season 1)", "The Show"},
		{"Imperfect Women (season one)", "Imperfect Women"},
		{"No Season Here", "No Season Here"},
		{"Movie Title 2025", "Movie Title 2025"},
		{"New Show", "New Show"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := StripSeason(tt.title)
			if got != tt.want {
				t.Errorf("StripSeason(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}
