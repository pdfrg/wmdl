package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestBuildQualityPrefs(t *testing.T) {
	cfg := &config.Config{
		Quality: config.QualityConfig{
			Movies: config.MediaQualityConfig{
				Resolution:     "2160p",
				PreferHDR:      true,
				SourcePriority: []string{"BluRay", "WEB-DL"},
				CodecPriority:  []string{"x265", "x264"},
			},
			TV: config.MediaQualityConfig{
				Resolution: "1080p",
			},
		},
		MinSeeders:      10,
		PreferredGroups: []string{"GROUP"},
	}

	t.Run("movies", func(t *testing.T) {
		prefs := buildQualityPrefs(cfg, model.MediaTypeMovie, []int{5, 9})
		assert.Equal(t, 2160, prefs.TargetResolution)
		assert.True(t, prefs.PreferHDR)
		assert.Equal(t, []string{"BluRay", "WEB-DL"}, prefs.SourcePriority)
		assert.Equal(t, 10, prefs.MinSeeders)
		assert.Equal(t, []int{5, 9}, prefs.PreferredIndexerIDs)
	})

	t.Run("tv", func(t *testing.T) {
		prefs := buildQualityPrefs(cfg, model.MediaTypeTV, nil)
		assert.Equal(t, 1080, prefs.TargetResolution)
		assert.False(t, prefs.PreferHDR)
	})

	t.Run("anime falls to movie config (else branch)", func(t *testing.T) {
		prefs := buildQualityPrefs(cfg, model.MediaTypeAnime, nil)
		assert.Equal(t, 2160, prefs.TargetResolution)
	})
}

func TestBuildBookQualityPrefs(t *testing.T) {
	cfg := &config.Config{
		Quality: config.QualityConfig{
			Books: config.BookQualityConfig{
				Ebooks:     config.BookFormatQualityConfig{FormatPriority: []string{"EPUB", "MOBI"}},
				Audiobooks: config.BookFormatQualityConfig{FormatPriority: []string{"M4B", "MP3"}},
			},
		},
		MinSeeders:      5,
		PreferredGroups: []string{"BOOKGRP"},
	}

	prefs := buildBookQualityPrefs(cfg, []int{3, 7})
	assert.Equal(t, []string{"EPUB", "MOBI"}, prefs.EbookFormatPriority)
	assert.Equal(t, []string{"M4B", "MP3"}, prefs.AudiobookFormatPriority)
	assert.Equal(t, 5, prefs.MinSeeders)
	assert.Equal(t, []string{"BOOKGRP"}, prefs.PreferredGroups)
	assert.Equal(t, []int{3, 7}, prefs.PreferredIndexerIDs)
}
