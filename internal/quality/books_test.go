package quality

import "testing"

func TestSplitSubtitle(t *testing.T) {
	tests := []struct {
		full string
		want string
	}{
		{"Earth 7: A Novel", "Earth 7"},
		{"1873: The Rotschilds and a bunch of crazy stuff that happened", "1873"},
		{"A Discovery of Witches", "A Discovery of Witches"},
		{"It", "It"},
		{"Title: Subtitle: Nested", "Title"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.full, func(t *testing.T) {
			got := SplitSubtitle(tt.full)
			if got != tt.want {
				t.Errorf("SplitSubtitle(%q) = %q, want %q", tt.full, got, tt.want)
			}
		})
	}
}

func TestWordsFromSearch(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Earth 7: A Novel", []string{"earth", "7", "novel"}},
		{"A Discovery of Witches", []string{"discovery", "witches"}},
		{"The In It", []string{}},
		{"", []string{}},
		{"1873", []string{"1873"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := wordsFromSearch(tt.input)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("wordsFromSearch(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterBookRelease(t *testing.T) {
	tests := []struct {
		name      string
		rawTitle  string
		author    string
		fullTitle string
		want      bool
	}{
		// User's bad example: unrelated "earth" word hit
		{
			name:      "reject unrelated earth word hit",
			rawTitle:  "The Pillars of the Earth",
			author:    "John Smith",
			fullTitle: "Earth 7: A Novel",
			want:      false,
		},
		{
			name:      "reject capitalism war on earth",
			rawTitle:  "Capitalism's war on the earth",
			author:    "John Smith",
			fullTitle: "Earth 7: A Novel",
			want:      false,
		},
		{
			name:      "reject house of earth and blood",
			rawTitle:  "house of earth and blood",
			author:    "John Smith",
			fullTitle: "Earth 7: A Novel",
			want:      false,
		},
		{
			name:      "reject the wandering earth",
			rawTitle:  "the wandering earth",
			author:    "John Smith",
			fullTitle: "Earth 7: A Novel",
			want:      false,
		},

		// User's good example: subtitle omitted in torrent
		{
			name:      "accept subtitle omitted 1873",
			rawTitle:  "1873 - Liquat Ahamed",
			author:    "Liaquat Ahamed",
			fullTitle: "1873: The Rotschilds and a bunch of crazy stuff that happened",
			want:      true,
		},

		// Normal title-led with author
		{
			name:      "accept author then title",
			rawTitle:  "Deborah Harkness - A Discovery of Witches",
			author:    "Deborah Harkness",
			fullTitle: "A Discovery of Witches",
			want:      true,
		},
		{
			name:      "accept title then author",
			rawTitle:  "A Discovery of Witches by Deborah Harkness",
			author:    "Deborah Harkness",
			fullTitle: "A Discovery of Witches",
			want:      true,
		},
		{
			name:      "accept title with format",
			rawTitle:  "A Discovery of Witches.epub",
			author:    "Deborah Harkness",
			fullTitle: "A Discovery of Witches",
			want:      true,
		},

		// Author-led: author name with title
		{
			name:      "accept author with variant title order",
			rawTitle:  "Harkness - Discovery of Witches A",
			author:    "Deborah Harkness",
			fullTitle: "A Discovery of Witches",
			want:      true,
		},

		// Short title edge case
		{
			name:      "accept it by stephen king",
			rawTitle:  "It - Stephen King",
			author:    "Stephen King",
			fullTitle: "It",
			want:      true,
		},
		{
			name:      "reject no matching author",
			rawTitle:  "The Wandering Earth",
			author:    "Cixin Liu",
			fullTitle: "The Wandering Earth",
			want:      true, // title-led: both main words present + author "liu" present
		},
		{
			name:      "reject missing title word and author",
			rawTitle:  "Something Completely Different",
			author:    "Cixin Liu",
			fullTitle: "The Wandering Earth",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterBookRelease(tt.rawTitle, tt.author, tt.fullTitle)
			if got != tt.want {
				t.Errorf("FilterBookRelease(%q, %q, %q) = %v, want %v",
					tt.rawTitle, tt.author, tt.fullTitle, got, tt.want)
			}
		})
	}
}

func TestWordsMatchBook(t *testing.T) {
	tests := []struct {
		name        string
		rawTitle    string
		authorWords []string
		titleWords  []string
		want        bool
	}{
		{
			name:        "exact match author + title in order",
			rawTitle:    "Deborah Harkness - A Discovery of Witches",
			authorWords: []string{"deborah", "harkness"},
			titleWords:  []string{"a", "discovery", "of", "witches"},
			want:        true,
		},
		{
			name:        "title words out of order",
			rawTitle:    "Harkness - Witches of Discovery A",
			authorWords: []string{"deborah", "harkness"},
			titleWords:  []string{"a", "discovery", "of", "witches"},
			want:        false,
		},
		{
			name:        "missing one title word",
			rawTitle:    "Deborah Harkness - Discovery of Witches",
			authorWords: []string{"deborah", "harkness"},
			titleWords:  []string{"a", "discovery", "of", "witches"},
			want:        true, // stop words "a"+"of" filtered → ["discovery","witches"] both present in order
		},
		{
			name:        "missing author word",
			rawTitle:    "Harkness - A Discovery of Witches",
			authorWords: []string{"deborah", "harkness"},
			titleWords:  []string{"a", "discovery", "of", "witches"},
			want:        false,
		},
		{
			name:        "word boundary miss",
			rawTitle:    "Earthquake - John Smith",
			authorWords: []string{"john", "smith"},
			titleWords:  []string{"earth"},
			want:        false, // "earth" is not a word token in "earthquake"
		},
		{
			name:        "short numeric word in title",
			rawTitle:    "Earth 7 - John Smith",
			authorWords: []string{"john", "smith"},
			titleWords:  []string{"earth", "7", "a", "novel"},
			want:        false, // "novel" (non-stop-word) required by exact match but missing in release
		},
		{
			name:        "exact match with all title words present",
			rawTitle:    "Earth 7: A Novel - John Smith",
			authorWords: []string{"john", "smith"},
			titleWords:  []string{"earth", "7", "a", "novel"},
			want:        true,
		},
		{
			name:        "numeric word missing in torrent",
			rawTitle:    "Earth - John Smith",
			authorWords: []string{"john", "smith"},
			titleWords:  []string{"earth", "7", "a", "novel"},
			want:        false, // "7" is not in release
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wordsMatchBook(tt.rawTitle, tt.authorWords, tt.titleWords)
			if got != tt.want {
				t.Errorf("wordsMatchBook(%q, %v, %v) = %v, want %v",
					tt.rawTitle, tt.authorWords, tt.titleWords, got, tt.want)
			}
		})
	}
}

func TestPartitionBookReleasesRaw(t *testing.T) {
	releases := []ParsedRelease{
		{RawTitle: "The Pillars of the Earth"},
		{RawTitle: "Capitalism's war on the earth"},
		{RawTitle: "House of Earth and Blood"},
		{RawTitle: "The Wandering Earth"},
		{RawTitle: "Earth 7 - John Smith"},
		{RawTitle: "Earth 7: A Novel - John Smith"},
	}

	// Search for "Earth 7: A Novel" by "John Smith"
	exact, fuzzy := PartitionBookReleasesRaw(releases, "John Smith", "Earth 7: A Novel")

	if len(exact) != 1 {
		t.Errorf("expected 1 exact match, got %d", len(exact))
		for _, r := range exact {
			t.Logf("  exact: %s", r.RawTitle)
		}
	}
	if len(fuzzy) != 1 {
		t.Errorf("expected 1 fuzzy match, got %d", len(fuzzy))
		for _, r := range fuzzy {
			t.Logf("  fuzzy: %s", r.RawTitle)
		}
	}
	// "Earth 7: A Novel - John Smith" has all 3 non-stop words ("earth","7","novel") → exact
	// "Earth 7 - John Smith" is missing "novel" → fuzzy
	// Everything else rejected by FilterBookRelease (missing "7" + no author match)
}

func TestPartitionBookReleasesRawSubtitleOmitted(t *testing.T) {
	releases := []ParsedRelease{
		{RawTitle: "1873 - Liquat Ahamed"},
		{RawTitle: "1873: The Rotschilds by Liaquat Ahamed"},
		{RawTitle: "Some other book"},
	}

	exact, fuzzy := PartitionBookReleasesRaw(releases, "Liaquat Ahamed", "1873: The Rotschilds and a bunch of crazy stuff that happened")

	// Both pass FilterBookRelease (main title "1873" + author word present)
	// Both are fuzzy because the full title has many non-stop words
	// ("rotschilds","bunch","crazy","stuff","happened") that aren't in either release
	if len(exact) != 0 {
		t.Errorf("expected 0 exact matches, got %d", len(exact))
		for _, r := range exact {
			t.Logf("  exact: %s", r.RawTitle)
		}
	}
	if len(fuzzy) != 2 {
		t.Errorf("expected 2 fuzzy matches, got %d", len(fuzzy))
		for _, r := range fuzzy {
			t.Logf("  fuzzy: %s", r.RawTitle)
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
