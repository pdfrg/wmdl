package discover

import "testing"

func TestSignificantWords(t *testing.T) {
	tests := []struct {
		title string
		want  []string
	}{
		{"Untold: Chess Mates", []string{"untold", "chess", "mates"}},
		{"This is Birmingham", []string{"birmingham"}},
		{"The Dark Knight", []string{"dark", "knight"}},
		{"Wicked", []string{"wicked"}},
		{"It", nil},
		{"", nil},
		{"a simple test", []string{"simple", "test"}},
		{"This is Your Captain Speaking", []string{"captain", "speaking"}},
		{"hello-world_test", []string{"hello", "world", "test"}},
		{"Star Wars: The Force Awakens", []string{"star", "wars", "force", "awakens"}},
		{"The Art of Sarah", []string{"sarah"}},
		{"Nippon Sangoku: The Three Nations of the Crimson Sun", []string{"nippon", "sangoku", "three", "nations", "crimson"}},
	}
	for _, tt := range tests {
		got := significantWords(tt.title)
		if !stringSliceEqual(got, tt.want) {
			t.Errorf("significantWords(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestLcsLen(t *testing.T) {
	tests := []struct {
		a, b []string
		want int
	}{
		{[]string{"dark", "knight"}, []string{"dark", "knight"}, 2},
		{[]string{"dark", "knight"}, []string{"knight", "dark"}, 1},
		{[]string{"dark", "knight"}, []string{"dark"}, 1},
		{[]string{"dark", "knight"}, []string{"dark", "knight", "rises"}, 2},
		{[]string{"dark", "knight"}, []string{"man", "of", "steel"}, 0},
		{[]string{"a", "b", "c"}, []string{"a", "c", "b"}, 2},
		{nil, []string{"a"}, 0},
		{[]string{"a"}, nil, 0},
	}
	for _, tt := range tests {
		got := lcsLen(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("lcsLen(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestTitleMatchScore(t *testing.T) {
	tests := []struct {
		search, result string
		wantScore      int
		wantSufficient bool
		desc           string
	}{
		// Exact matches
		{"The Dark Knight", "The Dark Knight", 100, true, "exact match"},
		{"Wicked", "Wicked", 100, true, "single word exact"},
		{"Wicked", "Wicked: Part One", 100, true, "single word in multi-word result"},

		// User's problem examples - should all be insufficient
		{"Untold: Chess Mates", "Queen of Chess", 0, false, "user example 1 - lcs=1 < 2"},
		{"This is Birmingham", "This is Your Captain Speaking", 0, false, "user example 2 - only stop word matches"},
		{"Nippon Sangoku: The Three Nations of the Crimson Sun", "The Art of Sarah", 0, false, "user example 3 - no overlap"},

		// Order sensitivity
		{"Dark Knight", "Knight Dark", 0, false, "reversed order lcs=1 < 2"},
		{"Dark Knight", "Dark Knight", 100, true, "same order"},
		{"Dark Knight Rises", "Dark Knight Rises", 100, true, "exact three words"},
		{"Dark Knight Rises", "Dark Knight", 66, true, "two of three words lcs=2 ratio=0.66"},
		{"Dark Knight", "Dark", 0, false, "only 1 of 2 search words"},

		// Partial match with order preserved
		{"Batman Begins", "Batman Begins", 100, true, "exact two words"},
		{"Batman Begins", "Batman: The Animated Series", 0, false, "only 1 of 2 search words lcs=1 < 2"},
		{"Batman Begins", "The Batman", 0, false, "only 'batman' matches but reversed order lcs=1 < 2"},

		// Edge cases
		{"It", "It", 0, false, "short word excluded"},
		{"", "Something", 0, false, "empty search"},
		{"Something", "", 0, false, "empty result"},
		{"The", "The", 0, false, "stop word only"},
	}
	for _, tt := range tests {
		score, sufficient := titleMatchScore(tt.search, tt.result)
		if score != tt.wantScore || sufficient != tt.wantSufficient {
			t.Errorf("%s: titleMatchScore(%q, %q) = (%d, %v), want (%d, %v)",
				tt.desc, tt.search, tt.result, score, sufficient, tt.wantScore, tt.wantSufficient)
		}
	}
}

func TestScoreSearchResult(t *testing.T) {
	tests := []struct {
		r       RTSearchResult
		year    int
		title   string
		wantNeg bool // true = expect -100000 (insufficient)
		desc    string
	}{
		{RTSearchResult{Title: "Queen of Chess", Year: 2025}, 2025, "Untold: Chess Mates", true, "user example 1 - insufficient overlap"},
		{RTSearchResult{Title: "This is Your Captain Speaking", Year: 2025}, 2025, "This is Birmingham", true, "user example 2 - only stop word"},
		{RTSearchResult{Title: "The Art of Sarah", Year: 2025}, 2025, "Nippon Sangoku: The Three Nations of the Crimson Sun", true, "user example 3 - no overlap"},
		{RTSearchResult{Title: "Knight Dark", Year: 2008}, 2008, "Dark Knight", true, "wrong word order"},

		{RTSearchResult{Title: "The Dark Knight", Year: 2008}, 2008, "The Dark Knight", false, "exact match"},
		{RTSearchResult{Title: "The Dark Knight", Year: 2007}, 2008, "The Dark Knight", false, "year ±1"},
		{RTSearchResult{Title: "The Dark Knight", Year: 2000}, 2008, "The Dark Knight", false, "year mismatch but good title"},
		{RTSearchResult{Title: "Wicked", Year: 2024}, 2024, "Wicked", false, "single word exact"},
	}
	for _, tt := range tests {
		got := scoreSearchResult(tt.r, tt.year, tt.title)
		if tt.wantNeg && got != -100000 {
			t.Errorf("%s: scoreSearchResult = %d, want -100000", tt.desc, got)
		}
		if !tt.wantNeg && got <= 0 {
			t.Errorf("%s: scoreSearchResult = %d, want positive", tt.desc, got)
		}
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
