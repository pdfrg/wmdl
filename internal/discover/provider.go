package discover

import "github.com/pdfrg/wmd/internal/model"

type ReleaseProvider interface {
	Name() string
	Scrape() ([]ScrapedItem, error)
}

type ScrapedItem struct {
	Title       string
	Year        int
	MediaType   model.MediaType
	ReleaseType model.ReleaseType
	ReleaseDate string
	ImdbID      string
	ImdbRating  float64
}
