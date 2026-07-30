package discover

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestDetectMediaType(t *testing.T) {
	d := &DVDReleaseDates{}

	tests := []struct {
		name  string
		html  string
		title string
		want  model.MediaType
	}{
		{
			name: "season in title",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">Show Season 1</a>
				<table><tr><td class="imdblink right">R</td></tr></table>
			</td></tr></table>`,
			title: "Show Season 1",
			want:  model.MediaTypeTV,
		},
		{
			name: "complete series",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">Show Complete Series</a>
				<table><tr><td class="imdblink right">R</td></tr></table>
			</td></tr></table>`,
			title: "Show Complete Series",
			want:  model.MediaTypeTV,
		},
		{
			name: "movie with R rating",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">The Movie</a>
				<table><tr><td class="imdblink right">R</td></tr></table>
			</td></tr></table>`,
			title: "The Movie",
			want:  model.MediaTypeMovie,
		},
		{
			name: "tv-ma rating badge",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">A Show</a>
				<table><tr><td class="imdblink right">TV-MA</td></tr></table>
			</td></tr></table>`,
			title: "A Show",
			want:  model.MediaTypeTV,
		},
		{
			name: "pg-13 movie",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">Action Flick</a>
				<table><tr><td class="imdblink right">PG-13</td></tr></table>
			</td></tr></table>`,
			title: "Action Flick",
			want:  model.MediaTypeMovie,
		},
		{
			name: "no ratings badges",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">Unknown</a>
				<table><tr><td class="imdblink right"></td></tr></table>
			</td></tr></table>`,
			title: "Unknown",
			want:  model.MediaType(""),
		},
		{
			name: "mini-series title",
			html: `<table><tr><td class="dvdcell">
				<a style="color:#000">A Mini-Series</a>
				<table><tr><td class="imdblink right"></td></tr></table>
			</td></tr></table>`,
			title: "A Mini-Series",
			want:  model.MediaTypeTV,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			assert.NoError(t, err)
			cell := doc.Find("td.dvdcell")
			got := d.detectMediaType(tt.title, cell)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDVDParseDVDCell(t *testing.T) {
	d := &DVDReleaseDates{}

	html := `<table><tr><td class="dvdcell">
		<a style="color:#000">No Href Movie</a>
		<img class="movieimg" src="Dreams-2025.jpg" alt="Dreams 2025">
		<table><tr><td class="imdblink"><a href="https://www.imdb.com/title/tt1234567/">8.5</a></td></tr></table>
	</td></tr></table>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	assert.NoError(t, err)

	cell := doc.Find("td.dvdcell")
	item := d.parseDVDCell(cell, "2025-05-27", t.Context())
	assert.NotNil(t, item)
	assert.Equal(t, "No Href Movie", item.Title)
	assert.Equal(t, 2025, item.Year)
	assert.Equal(t, model.MediaTypeMovie, item.MediaType)
	assert.Equal(t, model.ReleasePhysical, item.ReleaseType)
	assert.Equal(t, "2025-05-27", item.ReleaseDate)
	assert.Equal(t, "tt1234567", item.ImdbID)
	assert.Equal(t, 8.5, item.ImdbRating)
	assert.Equal(t, "dvdsreleasedates", item.Source)
}

func TestDVDParseDVDCellTV(t *testing.T) {
	d := &DVDReleaseDates{}

	html := `<table><tr><td class="dvdcell">
		<a style="color:#000">TV Show Season 2</a>
		<img class="movieimg" src="TV-2025.jpg" alt="TV 2025">
	</td></tr></table>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	assert.NoError(t, err)

	cell := doc.Find("td.dvdcell")
	item := d.parseDVDCell(cell, "2025-05-27", t.Context())
	assert.NotNil(t, item)
	assert.Equal(t, "TV Show Season 2", item.Title)
	assert.Equal(t, model.MediaTypeTV, item.MediaType)
}

func TestDVDHistoricalURL(t *testing.T) {
	d := &DVDReleaseDates{}
	d.targetYear = 2025
	d.targetWeek = 22
	url := d.historicalURL()
	assert.Equal(t, "https://www.dvdsreleasedates.com/releases/2025/5/new-dvd-releases-may-2025", url)
}
