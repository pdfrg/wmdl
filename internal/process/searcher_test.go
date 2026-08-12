package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/config"
)

func TestQualityPrefs(t *testing.T) {
	cfg := &config.Config{
		Quality: config.QualityConfig{
			Movies: config.MediaQualityConfig{
				Resolution:     "2160p",
				PreferHDR:      true,
				SourcePriority: []string{"BluRay"},
				CodecPriority:  []string{"x265"},
			},
			TV: config.MediaQualityConfig{
				Resolution: "1080p",
			},
			Anime: config.MediaQualityConfig{
				Resolution: "720p",
			},
		},
		MinSeeders:      5,
		PreferredGroups: []string{"GRP"},
	}

	t.Run("movies", func(t *testing.T) {
		prefs := qualityPrefs(cfg, "movie", []int{1, 2})
		assert.Equal(t, 2160, prefs.TargetResolution)
		assert.True(t, prefs.PreferHDR)
		assert.Equal(t, 5, prefs.MinSeeders)
		assert.Equal(t, []int{1, 2}, prefs.PreferredIndexerIDs)
	})

	t.Run("tv", func(t *testing.T) {
		prefs := qualityPrefs(cfg, "tv", []int{2})
		assert.Equal(t, 1080, prefs.TargetResolution)
	})

	t.Run("anime", func(t *testing.T) {
		prefs := qualityPrefs(cfg, "anime", []int{3})
		assert.Equal(t, 720, prefs.TargetResolution)
	})

	t.Run("unknown defaults to movies", func(t *testing.T) {
		prefs := qualityPrefs(cfg, "unknown", nil)
		assert.Equal(t, 2160, prefs.TargetResolution)
	})
}
