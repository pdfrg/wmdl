//go:build integration

package discover

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPChartsLiveEndpoint(t *testing.T) {
	p := NewRPChartsProvider([]string{"all"})

	items, err := p.Scrape()
	require.NoError(t, err)
	require.NotEmpty(t, items, "live charts.json should yield new_albums")

	for _, item := range items {
		assert.NotEmpty(t, item.Title, "entry title must not be empty")
		assert.NotEmpty(t, item.ArtistName, "entry artist must not be empty")
		assert.Greater(t, item.Year, 0, "entry year must be populated")
		if assert.NotEmpty(t, item.ReleaseDate, "entry release date must be populated") {
			_, err := time.Parse("2006-01-02", item.ReleaseDate)
			assert.NoError(t, err, "release date %q must be YYYY-MM-DD", item.ReleaseDate)
		}
		assert.Equal(t, "rpcharts", item.Source)
		assert.Contains(t, item.Notes, "https://radioparadise.com/music/album/")
	}
}
