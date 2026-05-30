package discover

import (
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

func isoWeekToDate(year, week int) time.Time {
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	dayOffset := (int(jan4.Weekday()) - 1 + 7) % 7
	week1Monday := jan4.AddDate(0, 0, -dayOffset)
	return week1Monday.AddDate(0, 0, 7*(week-1))
}

func tuesdayOfISOWeek(year, week int) time.Time {
	monday := isoWeekToDate(year, week)
	return monday.AddDate(0, 0, 1)
}

type ReleaseProvider interface {
	Name() string
	Scrape() ([]ScrapedItem, error)
}

type WeekSettable interface {
	SetWeekRange(year, week int)
}

type ScrapedItem struct {
	Title           string
	Year            int
	MediaType       model.MediaType
	ReleaseType     model.ReleaseType
	ReleaseDate     string
	ImdbID          string
	ImdbRating      float64
	RTCriticsScore  float64
	RTAudienceScore float64
	YoutubeViews    int64
	Source          string

	// Music-specific fields (empty for movie/TV)
	ArtistName      string
	AlbumType       model.AlbumType
	AOTYCriticScore float64
	AOTYCriticCount int
	AOTYUserScore   float64
	AOTYUserCount   int
	AOTYMustHear    bool
	ImageURL        string
	AOTYURL         string
}
