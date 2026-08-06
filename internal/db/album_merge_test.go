package db

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestMergeAlbumRelease(t *testing.T) {
	existing := &model.AlbumRelease{
		ArtistName:      "The The",
		Title:           "Ensoulment",
		Year:            2024,
		MBID:            "mbid-1",
		ArtistMBID:      "artist-mbid-1",
		AlbumType:       model.AlbumTypeLP,
		ReleaseDate:     "2024-09-06",
		Genres:          "Alternative Rock",
		Overview:        "A strong comeback.",
		PosterPath:      "https://img/aoty.jpg",
		AOTYURL:         "https://albumoftheyear.org/album/1",
		AllMusicURL:     "https://allmusic.com/album/1",
		AOTYCriticScore: 82,
		AOTYCriticCount: 12,
		AOTYUserScore:   78,
		AOTYUserCount:   1500,
		AOTYMustHear:    true,
		AllMusicRating:  8,
		MBRating:        3.5,
	}

	// rpcharts-style incoming: carries only artist/title/year, a cover, and
	// the "on RP since" date. Must not wipe any enrichment data.
	incoming := &model.AlbumRelease{
		ArtistName:  "The The",
		Title:       "Ensoulment",
		Year:        2024,
		ReleaseDate: "2026-07-30",
		PosterPath:  "https://img.radioparadise.com/covers/l/26434.jpg",
	}

	merged := mergeAlbumRelease(existing, incoming)

	assert.Equal(t, "The The", merged.ArtistName)
	assert.Equal(t, "Ensoulment", merged.Title)
	assert.Equal(t, 2024, merged.Year)
	assert.Equal(t, "mbid-1", merged.MBID)
	assert.Equal(t, "artist-mbid-1", merged.ArtistMBID)
	assert.Equal(t, model.AlbumTypeLP, merged.AlbumType)
	assert.Equal(t, "Alternative Rock", merged.Genres)
	assert.Equal(t, "A strong comeback.", merged.Overview)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26434.jpg", merged.PosterPath)
	assert.Equal(t, "2026-07-30", merged.ReleaseDate)
	assert.Equal(t, "https://albumoftheyear.org/album/1", merged.AOTYURL)
	assert.Equal(t, "https://allmusic.com/album/1", merged.AllMusicURL)
	assert.Equal(t, 82.0, merged.AOTYCriticScore)
	assert.Equal(t, 12, merged.AOTYCriticCount)
	assert.Equal(t, 78.0, merged.AOTYUserScore)
	assert.Equal(t, 1500, merged.AOTYUserCount)
	assert.True(t, merged.AOTYMustHear)
	assert.Equal(t, 8.0, merged.AllMusicRating)
	assert.Equal(t, 3.5, merged.MBRating)
}

func TestMergeAlbumRelease_FillsGapsFromIncoming(t *testing.T) {
	// rpcharts-first ordering: a later, richer source fills the gaps.
	existing := &model.AlbumRelease{
		ArtistName:  "Local Natives",
		Title:       "Curio",
		Year:        2026,
		ReleaseDate: "2026-07-28",
		PosterPath:  "https://img.radioparadise.com/covers/l/26501.jpg",
	}

	incoming := &model.AlbumRelease{
		ArtistName:      "Local Natives",
		Title:           "Curio",
		Year:            2026,
		MBID:            "mbid-2",
		AOTYURL:         "https://albumoftheyear.org/album/2",
		AOTYCriticScore: 88,
		AOTYCriticCount: 20,
		AOTYMustHear:    true,
		Genres:          "Indie Rock",
	}

	merged := mergeAlbumRelease(existing, incoming)

	assert.Equal(t, "mbid-2", merged.MBID)
	assert.Equal(t, "https://albumoftheyear.org/album/2", merged.AOTYURL)
	assert.Equal(t, 88.0, merged.AOTYCriticScore)
	assert.Equal(t, 20, merged.AOTYCriticCount)
	assert.True(t, merged.AOTYMustHear)
	assert.Equal(t, "Indie Rock", merged.Genres)
	assert.Equal(t, "2026-07-28", merged.ReleaseDate)
	assert.Equal(t, "https://img.radioparadise.com/covers/l/26501.jpg", merged.PosterPath)
}

func TestMergeAlbumRelease_NonZeroIncomingOverrides(t *testing.T) {
	existing := &model.AlbumRelease{
		ArtistName: "Artist",
		Title:      "Album",
		Year:       2025,
		AOTYURL:    "https://albumoftheyear.org/album/3",
	}
	incoming := &model.AlbumRelease{
		ArtistName:      "Artist",
		Title:           "Album",
		Year:            2025,
		AOTYCriticScore: 91,
		AOTYCriticCount: 30,
	}

	merged := mergeAlbumRelease(existing, incoming)
	assert.Equal(t, 91.0, merged.AOTYCriticScore)
	assert.Equal(t, 30, merged.AOTYCriticCount)
	// A later AOTY re-scrape keeps its URL even when the incoming omits it.
	assert.Equal(t, "https://albumoftheyear.org/album/3", merged.AOTYURL)
}

func TestMergeAlbumRelease_NilExisting(t *testing.T) {
	incoming := &model.AlbumRelease{ArtistName: "A", Title: "B", Year: 2025}
	merged := mergeAlbumRelease(nil, incoming)
	assert.Equal(t, incoming, merged)
}
