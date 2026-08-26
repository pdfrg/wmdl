package discover

import (
	"context"
	"testing"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/notifier"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func notificationTestConfig() *config.Config {
	return &config.Config{
		MediaTypes: config.MediaTypesConfig{
			Movies: config.MovieConfig{
				Enabled:  true,
				Scrapers: []string{"dvdsreleasedates", "tmdb-discover", "flixpatrol"},
			},
			TV: config.TVConfig{
				Enabled:  true,
				Scrapers: []string{"dvdsreleasedates", "tmdb-discover", "flixpatrol"},
			},
			Anime: config.AnimeConfig{
				Enabled:       true,
				PhaseBEnabled: true,
			},
			Music: config.MusicConfig{
				Enabled:  true,
				Scrapers: []string{"albumoftheyear", "allmusic", "rpcharts"},
			},
			Books: config.BookConfig{
				Enabled:  true,
				Scrapers: []string{"goodreads_blog", "bookshop", "bookmarks"},
			},
		},
	}
}

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/wmdl-test.db")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMarkScrapeFailures(t *testing.T) {
	r := &Runner{
		cfg: notificationTestConfig(),
		failedScrapers: map[string]string{
			"bookshop":   "scrape failed",
			"flixpatrol": "browser unavailable: exec: \"brave\": executable file not found in $PATH",
			"allmusic":   "browser unavailable",
			"anilist":    "scrape failed",
			"tenrai":     "scrape failed",
			"jikan":      "scrape failed",
		},
	}
	sourceCounts := map[string]int{
		"bookshop": 0, "goodreads blog": 0,
		"flixpatrol movie": 0, "flixpatrol tv": 0,
		"allmusic":            0,
		"anilist (completed)": 0, "anilist (airing)": 0,
		"tenrai (completed)": 0, "tenrai (airing)": 0,
		"jikan (completed)": 0,
	}
	sourceNotes := make(map[string]string)

	r.markScrapeFailures(sourceCounts, sourceNotes)

	assert.Equal(t, "scrape failed", sourceNotes["bookshop"])
	assert.Equal(t, "browser unavailable: exec: \"brave\": executable file not found in $PATH", sourceNotes["flixpatrol movie"])
	assert.Equal(t, "browser unavailable: exec: \"brave\": executable file not found in $PATH", sourceNotes["flixpatrol tv"])
	assert.Equal(t, "browser unavailable", sourceNotes["allmusic"])
	assert.Equal(t, "scrape failed", sourceNotes["anilist (completed)"])
	assert.Equal(t, "scrape failed", sourceNotes["anilist (airing)"])
	assert.Equal(t, "scrape failed", sourceNotes["tenrai (completed)"])
	assert.Equal(t, "scrape failed", sourceNotes["tenrai (airing)"])
	// jikan is deprecated: its failure is expected and not surfaced.
	assert.NotContains(t, sourceNotes, "jikan (completed)")
	// Healthy scrapers stay unannotated.
	assert.NotContains(t, sourceNotes, "goodreads blog")
}

func TestSendNotificationAllFailAlert(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	mockNotifier := &notifier.MockNotifier{}
	mockNotifier.On("Send", "wmdl: Scrape Failures", mock.Anything, 8).Return(nil).Once()

	r := &Runner{
		log:            zerolog.Nop(),
		cfg:            notificationTestConfig(),
		db:             d,
		notify:         mockNotifier,
		failedScrapers: map[string]string{"bookshop": "scrape failed"},
	}

	r.sendNotification(ctx, true, true, true, true, true, nil, nil, nil, nil, 2026, 33, 0)

	msg := mockNotifier.Calls[0].Arguments.String(1)
	assert.Contains(t, msg, "bookshop: scrape failed")
	mockNotifier.AssertExpectations(t)
}

func TestSendNotificationNormalPathAnnotation(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Seed one pending movie event from tmdb-discover for week 2026-W33.
	titleID, err := d.UpsertTitle(ctx, &model.Title{
		TmdbID: 100, Title: "Test Movie", Year: 2026, MediaType: model.MediaTypeMovie,
	})
	require.NoError(t, err)
	_, err = d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "tmdb-discover", Status: model.StatusPending,
		ISOYear: 2026, ISOWeek: 33,
	})
	require.NoError(t, err)

	mockNotifier := &notifier.MockNotifier{}
	mockNotifier.On("Send", "wmdl: New Releases", mock.Anything, 5).Return(nil).Once()

	r := &Runner{
		log:            zerolog.Nop(),
		cfg:            notificationTestConfig(),
		db:             d,
		notify:         mockNotifier,
		failedScrapers: map[string]string{"bookshop": "browser unavailable"},
	}

	r.sendNotification(ctx, true, true, true, true, true, nil, nil, nil, nil, 2026, 33, 1)

	msg := mockNotifier.Calls[0].Arguments.String(1)
	assert.Contains(t, msg, "bookshop: 0 (browser unavailable)")
	assert.Contains(t, msg, "tmdb movie: 1")
	mockNotifier.AssertExpectations(t)
}

func TestSendNotificationNoPendingNoFailures(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	mockNotifier := &notifier.MockNotifier{}

	r := &Runner{
		log:    zerolog.Nop(),
		cfg:    notificationTestConfig(),
		db:     d,
		notify: mockNotifier,
	}

	r.sendNotification(ctx, true, true, true, true, true, nil, nil, nil, nil, 2026, 33, 0)

	assert.Len(t, mockNotifier.Calls, 0)
}

func TestBookshopStorageWeek(t *testing.T) {
	cases := []struct {
		name        string
		releaseDate string
		lookback    int
		wantYear    int
		wantWeek    int
	}{
		{"aug 2026 release", "2026-08-25", 1, 2026, 36},
		{"mid aug 2026 release", "2026-08-11", 1, 2026, 34},
		{"year boundary", "2025-12-30", 1, 2026, 2},
		{"two week timeshift", "2026-08-25", 2, 2026, 37},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := notificationTestConfig()
			cfg.MediaTypes.Books.LookbackWeeks = tc.lookback
			r := &Runner{cfg: cfg}

			releaseT, err := time.Parse("2006-01-02", tc.releaseDate)
			require.NoError(t, err)
			y, w := releaseT.ISOWeek()

			gotYear, gotWeek := r.bookshopStorageWeek(y, w)
			assert.Equal(t, tc.wantYear, gotYear)
			assert.Equal(t, tc.wantWeek, gotWeek)
		})
	}
}

func TestBookshopPrePopSummary(t *testing.T) {
	r := &Runner{log: zerolog.Nop(), cfg: notificationTestConfig()}

	items := []ScrapedItem{
		{Source: "bookshop", ReleaseDate: "August 25, 2026"},
		{Source: "bookshop", ReleaseDate: "August 25, 2026"},
		{Source: "goodreads_blog,bookshop", ReleaseDate: "August 25, 2026"},
		{Source: "goodreads_blog", ReleaseDate: "August 19, 2026"}, // non-bookshop: ignored
		{Source: "bookshop", ReleaseDate: ""},                      // undated: ignored
	}

	got := r.bookshopPrePopSummary(items)
	assert.Contains(t, got, "3 scraped today")
	assert.Contains(t, got, "2026-W36")

	assert.Empty(t, r.bookshopPrePopSummary(nil))
	assert.Empty(t, r.bookshopPrePopSummary([]ScrapedItem{{Source: "goodreads_blog", ReleaseDate: "August 25, 2026"}}))
}

func TestSendNotificationBookshopPrePopWithPending(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	titleID, err := d.UpsertTitle(ctx, &model.Title{
		TmdbID: 200, Title: "Pending Movie", Year: 2026, MediaType: model.MediaTypeMovie,
	})
	require.NoError(t, err)
	_, err = d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "tmdb-discover", Status: model.StatusPending,
		ISOYear: 2026, ISOWeek: 35,
	})
	require.NoError(t, err)

	mockNotifier := &notifier.MockNotifier{}
	mockNotifier.On("Send", "wmdl: New Releases", mock.Anything, 5).Return(nil).Once()

	r := &Runner{
		log:    zerolog.Nop(),
		cfg:    notificationTestConfig(),
		db:     d,
		notify: mockNotifier,
	}

	books := []ScrapedItem{
		{Source: "bookshop", Title: "Book A", ArtistName: "Author A", ReleaseDate: "August 25, 2026"},
		{Source: "bookshop", Title: "Book B", ArtistName: "Author B", ReleaseDate: "August 25, 2026"},
	}
	r.sendNotification(ctx, true, true, true, true, true, nil, nil, nil, books, 2026, 35, 3)

	msg := mockNotifier.Calls[0].Arguments.String(1)
	assert.Contains(t, msg, "tmdb movie: 1")
	assert.Contains(t, msg, "bookshop:")
	assert.Contains(t, msg, "2 scraped today, stored under 2026-W36")
	mockNotifier.AssertExpectations(t)
}

func TestSendNotificationPrePopOnlyStillNotifies(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	mockNotifier := &notifier.MockNotifier{}
	mockNotifier.On("Send", "wmdl: Bookshop Pre-Population", mock.Anything, 5).Return(nil).Once()

	r := &Runner{
		log:    zerolog.Nop(),
		cfg:    notificationTestConfig(),
		db:     d,
		notify: mockNotifier,
	}

	books := []ScrapedItem{
		{Source: "bookshop", Title: "Book A", ArtistName: "Author A", ReleaseDate: "August 25, 2026"},
	}
	r.sendNotification(ctx, true, true, true, true, true, nil, nil, nil, books, 2026, 35, 0)

	msg := mockNotifier.Calls[0].Arguments.String(1)
	assert.Contains(t, msg, "No releases pending for 2026-W35")
	assert.Contains(t, msg, "1 scraped today, stored under 2026-W36")
	mockNotifier.AssertExpectations(t)
}
