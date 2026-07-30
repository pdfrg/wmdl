package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/library"
)

func TestFindSeasonByNumber(t *testing.T) {
	seasons := []library.SonarrSeason{
		{SeasonNumber: 1},
		{SeasonNumber: 2, Monitored: true},
		{SeasonNumber: 3},
	}

	t.Run("found", func(t *testing.T) {
		s := findSeasonByNumber(seasons, 2)
		assert.NotNil(t, s)
		assert.Equal(t, 2, s.SeasonNumber)
		assert.True(t, s.Monitored)
	})

	t.Run("not found", func(t *testing.T) {
		s := findSeasonByNumber(seasons, 5)
		assert.Nil(t, s)
	})

	t.Run("empty slice", func(t *testing.T) {
		s := findSeasonByNumber(nil, 1)
		assert.Nil(t, s)
	})
}
