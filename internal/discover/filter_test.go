package discover

import (
	"testing"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestFilterTitle_EmptyFilter(t *testing.T) {
	f := &config.ContentFilter{}
	tm := &model.Title{OriginalLanguage: "de", Genres: "Horror"}
	if res := FilterTitle(f, tm); !res.Passed {
		t.Errorf("empty filter should pass everything, got blocked: %s", res.Reason)
	}
}

func TestFilterTitle_AllowedLanguages(t *testing.T) {
	f := &config.ContentFilter{AllowedLanguages: []string{"en"}}

	t.Run("allowed", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "en"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("english should be allowed, got: %s", res.Reason)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "de"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("german should be blocked when only english allowed")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "EN"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("EN should match en: %s", res.Reason)
		}
	})
}

func TestFilterTitle_BlockedLanguages(t *testing.T) {
	f := &config.ContentFilter{BlockedLanguages: []string{"de", "fr"}}

	t.Run("allowed", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "en"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("english should pass: %s", res.Reason)
		}
	})

	t.Run("blocked german", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "de"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("german should be blocked")
		}
	})

	t.Run("blocked french", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "fr"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("french should be blocked")
		}
	})
}

func TestFilterTitle_BlockedCountries(t *testing.T) {
	f := &config.ContentFilter{BlockedCountries: []string{"in"}}

	t.Run("blocked single country", func(t *testing.T) {
		tm := &model.Title{OriginCountry: "IN"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("India should be blocked")
		}
	})

	t.Run("blocked in multi-country", func(t *testing.T) {
		tm := &model.Title{OriginCountry: "US,GB,IN"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("India in multi-country should be blocked")
		}
	})

	t.Run("allowed country", func(t *testing.T) {
		tm := &model.Title{OriginCountry: "US"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("US should pass: %s", res.Reason)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		tm := &model.Title{OriginCountry: "In"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("case-insensitive IN should be blocked")
		}
	})
}

func TestFilterTitle_BlockedGenres(t *testing.T) {
	f := &config.ContentFilter{BlockedGenres: []string{"Documentary", "Horror"}}

	t.Run("blocked genre", func(t *testing.T) {
		tm := &model.Title{Genres: "Action, Horror, Thriller"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("Horror should be blocked")
		}
	})

	t.Run("allowed genre", func(t *testing.T) {
		tm := &model.Title{Genres: "Action, Comedy"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("Action/Comedy should pass: %s", res.Reason)
		}
	})

	t.Run("partial genre name no longer matches", func(t *testing.T) {
		tm := &model.Title{Genres: "Action & Adventure, Comedy"}
		f2 := &config.ContentFilter{BlockedGenres: []string{"Action"}}
		res := FilterTitle(f2, tm)
		if !res.Passed {
			t.Error("Action should not match Action & Adventure — comma-delimited exact match only")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		tm := &model.Title{Genres: "action, documentary"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("documentary (lowercase) should be blocked")
		}
	})
}

func TestFilterMusic_BlockedGenres(t *testing.T) {
	f := &config.ContentFilter{BlockedGenres: []string{"Jazz", "Classical"}}
	release := &model.AlbumRelease{Genres: "Rock, Jazz, Blues"}

	if res := FilterMusic(f, release); res.Passed {
		t.Error("Jazz album should be blocked")
	}

	release2 := &model.AlbumRelease{Genres: "Rock, Blues"}
	if res := FilterMusic(f, release2); !res.Passed {
		t.Errorf("Rock/Blues should pass: %s", res.Reason)
	}
}

func TestFilterBook_AllowedLanguages(t *testing.T) {
	f := &config.ContentFilter{AllowedLanguages: []string{"en"}}

	t.Run("english natural name", func(t *testing.T) {
		b := &model.Book{Language: "English"}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("English should be allowed: %s", res.Reason)
		}
	})

	t.Run("english iso", func(t *testing.T) {
		b := &model.Book{Language: "en"}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("'en' should be allowed: %s", res.Reason)
		}
	})

	t.Run("french blocked", func(t *testing.T) {
		b := &model.Book{Language: "French"}
		if res := FilterBook(f, b); res.Passed {
			t.Error("French should be blocked when only english allowed")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		b := &model.Book{Language: "ENGLISH"}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("ENGLISH should match: %s", res.Reason)
		}
	})
}

func TestFilterBook_BlockedGenres(t *testing.T) {
	f := &config.ContentFilter{BlockedGenres: []string{"Self-Help", "Religion"}}

	t.Run("blocked genre", func(t *testing.T) {
		b := &model.Book{Tags: "Fiction, Self-Help, Business"}
		if res := FilterBook(f, b); res.Passed {
			t.Error("Self-Help should be blocked")
		}
	})

	t.Run("allowed genre", func(t *testing.T) {
		b := &model.Book{Tags: "Fiction, Mystery, Thriller"}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("Fiction should pass: %s", res.Reason)
		}
	})

	t.Run("empty tags is not blocked by blocked_genres", func(t *testing.T) {
		b := &model.Book{Tags: ""}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("empty tags should pass blocked_genres check: %s", res.Reason)
		}
	})
}

func TestFilterBook_BlockedLanguages(t *testing.T) {
	f := &config.ContentFilter{BlockedLanguages: []string{"de"}}

	t.Run("blocked german natural name", func(t *testing.T) {
		b := &model.Book{Language: "German"}
		if res := FilterBook(f, b); res.Passed {
			t.Error("German should be blocked")
		}
	})

	t.Run("allowed english", func(t *testing.T) {
		b := &model.Book{Language: "English"}
		if res := FilterBook(f, b); !res.Passed {
			t.Errorf("English should pass: %s", res.Reason)
		}
	})
}

func TestHasOverrideGenre(t *testing.T) {
	t.Run("no override set", func(t *testing.T) {
		f := &config.ContentFilter{}
		if HasOverrideGenre(f, "Action") {
			t.Error("empty OverrideGenres should return false")
		}
	})

	t.Run("override matches", func(t *testing.T) {
		f := &config.ContentFilter{OverrideGenres: []string{"Action"}}
		if !HasOverrideGenre(f, "Action, Thriller") {
			t.Error("Action should match override")
		}
	})

	t.Run("override does not match", func(t *testing.T) {
		f := &config.ContentFilter{OverrideGenres: []string{"Action"}}
		if HasOverrideGenre(f, "Comedy, Drama") {
			t.Error("Comedy/Drama should not match Action override")
		}
	})

	t.Run("substring no longer matches", func(t *testing.T) {
		f := &config.ContentFilter{OverrideGenres: []string{"Action"}}
		if HasOverrideGenre(f, "Action & Adventure, Thriller") {
			t.Error("Action should not match Action & Adventure — comma-delimited exact match only")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		f := &config.ContentFilter{OverrideGenres: []string{"action"}}
		if !HasOverrideGenre(f, "Action") {
			t.Error("case insensitive match should work")
		}
	})

	t.Run("empty genres string", func(t *testing.T) {
		f := &config.ContentFilter{OverrideGenres: []string{"Action"}}
		if HasOverrideGenre(f, "") {
			t.Error("empty genres should not match")
		}
	})
}

func TestFilterTitle_CombinedAllowedAndBlocked(t *testing.T) {
	// AllowedLanguages takes precedence — only check blocked if allowed passes
	f := &config.ContentFilter{
		AllowedLanguages: []string{"en"},
		BlockedGenres:    []string{"Horror"},
	}

	t.Run("english non-horror passes", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "en", Genres: "Action, Thriller"}
		if res := FilterTitle(f, tm); !res.Passed {
			t.Errorf("english Action/Thriller should pass: %s", res.Reason)
		}
	})

	t.Run("english horror blocked", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "en", Genres: "Horror, Thriller"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("english Horror should be blocked")
		}
	})

	t.Run("non-english blocked regardless", func(t *testing.T) {
		tm := &model.Title{OriginalLanguage: "de", Genres: "Action"}
		if res := FilterTitle(f, tm); res.Passed {
			t.Error("german should be blocked even if genre is ok")
		}
	})
}
