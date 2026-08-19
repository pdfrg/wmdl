package discover

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseFlixRow_AnimeGenre(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    bool
		wantTv  bool
		wantMov bool
	}{
		{
			name:   "anime genre detected",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Animation</span></div></div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Anime</span></div></div></div></a></td></tr>`,
			want:   true,
			wantTv: true,
		},
		{
			name:   "no anime genre",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Drama</span></div></div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
		{
			name:   "animation but not anime",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Animation</span></div></div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
		{
			name:    "movie with anime genre",
			html:    `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Movie </div><div class="flex flex-wrap gap-x-1"><div>Movie</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Anime</span></div></div></div></a></td></tr>`,
			want:    true,
			wantMov: true,
		},
		{
			name:   "no genre tags at all",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := parseFlixRow(tt.html)
			if item.hasAnimeGenre != tt.want {
				t.Errorf("hasAnimeGenre = %v, want %v", item.hasAnimeGenre, tt.want)
			}
			if tt.wantTv && item.MediaType != "tv" {
				t.Errorf("MediaType = %q, want %q", item.MediaType, "tv")
			}
			if tt.wantMov && item.MediaType != "movie" {
				t.Errorf("MediaType = %q, want %q", item.MediaType, "movie")
			}
		})
	}
}

func TestFlixPatrolScrapePage1Failure(t *testing.T) {
	f := NewFlixPatrolProvider("http://127.0.0.1:9222", nil)
	f.SetWeekRange(2026, 33)
	fetchErr := errors.New("page 1 cloudflare challenge")
	f.fetchPageFn = func(_ *FlixPatrolProvider, page int, _, _ time.Time) ([]flixItem, error) {
		return nil, fetchErr
	}

	_, err := f.Scrape()
	if err == nil {
		t.Fatal("expected error when page 1 fails")
	}
	if !errors.Is(err, fetchErr) && !strings.Contains(err.Error(), "page 1") {
		t.Fatalf("expected page 1 failure wrapped, got: %v", err)
	}
}

func TestFlixPatrolScrapeLaterPageFailureIsTolerated(t *testing.T) {
	f := NewFlixPatrolProvider("http://127.0.0.1:9222", nil)
	f.SetWeekRange(2026, 33)

	streamTue := truncateToDay(tuesdayOfISOWeek(2026, 33))
	streamStart := truncateToDay(streamTue.AddDate(0, 0, -6))
	fetchErr := errors.New("page 2 transient failure")
	f.fetchPageFn = func(_ *FlixPatrolProvider, page int, _, _ time.Time) ([]flixItem, error) {
		switch page {
		case 1:
			return []flixItem{{
				Title:      "Test",
				MediaType:  "movie",
				Date:       streamStart.Format("Jan 2"),
				IMDbRating: 7.5,
			}}, nil
		case 2:
			return nil, fetchErr
		default:
			return []flixItem{{Title: "Old", Date: streamStart.AddDate(0, 0, -8).Format("Jan 2")}}, nil
		}
	}

	items, err := f.Scrape()
	if err != nil {
		t.Fatalf("later-page failure should not abort scrape: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected partial items from page 1 despite later-page failure")
	}
}
