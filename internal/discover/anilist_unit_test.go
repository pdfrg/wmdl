package discover

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestAniListToScrapedItem(t *testing.T) {
	p := &AniListProvider{}
	m := anilistMedia{
		ID:    100,
		IDMal: 200,
		Title: struct {
			Romaji  string `json:"romaji"`
			English string `json:"english"`
		}{Romaji: "Test Anime Romaji", English: "Test Anime"},
		Format:       "TV",
		Status:       "FINISHED",
		Episodes:     12,
		AverageScore: 85,
		MeanScore:    80,
		Popularity:   50000,
		Genres:       []string{"Action", "Sci-Fi"},
		Source:       "ORIGINAL",
		StartDate:    anilistFuzzyDate{Year: 2025, Month: 1, Day: 10},
		EndDate:      anilistFuzzyDate{Year: 2025, Month: 3, Day: 28},
		CoverImage: struct {
			Large string `json:"large"`
		}{Large: "https://example.com/cover.jpg"},
		Studios: anilistStudioConnection{
			Nodes: []struct {
				Name string `json:"name"`
			}{{Name: "Studio A"}},
		},
		Description: "<p>A great anime.</p>",
	}

	item := p.toScrapedItem(m, "anilist")
	assert.Equal(t, "Test Anime", item.Title)
	assert.Equal(t, 2025, item.Year)
	assert.Equal(t, model.MediaTypeAnime, item.MediaType)
	assert.Equal(t, model.ReleaseStreaming, item.ReleaseType)
	assert.Equal(t, "anilist", item.Source)
	assert.Equal(t, 200, item.MalID)
	assert.Equal(t, "https://example.com/cover.jpg", item.ImageURL)
	assert.Equal(t, "A great anime.", item.Overview)
	assert.Equal(t, 8.5, item.ImdbRating)
	assert.Equal(t, "TV", item.AnimeType)
	assert.Equal(t, 12, item.AnimeEpisodes)
	assert.Equal(t, "FINISHED", item.AnimeStatus)
	assert.Equal(t, 50000, item.AnimeMembers)
	assert.Equal(t, "ORIGINAL", item.AnimeSource)
	assert.Equal(t, "Studio A", item.AnimeStudio)
	assert.Equal(t, "Action, Sci-Fi", item.Genres)
	// Phase A items should not have Notes
	assert.Empty(t, item.Notes)
}

func TestAniListToScrapedItemAiring(t *testing.T) {
	p := &AniListProvider{}
	m := anilistMedia{
		ID:    101,
		IDMal: 201,
		Title: struct {
			Romaji  string `json:"romaji"`
			English string `json:"english"`
		}{Romaji: "Airing Anime", English: ""},
		Format:       "TV",
		Status:       "RELEASING",
		Episodes:     24,
		AverageScore: 75,
		Popularity:   20000,
		StartDate:    anilistFuzzyDate{Year: 2025, Month: 4, Day: 1},
		EndDate:      anilistFuzzyDate{Year: 2025, Month: 6, Day: 30},
		CoverImage: struct {
			Large string `json:"large"`
		}{Large: ""},
	}

	item := p.toScrapedItem(m, "anilist-airing")
	assert.Equal(t, "Airing Anime", item.Title)
	assert.Equal(t, "anilist-airing", item.Source)
	assert.Contains(t, item.Notes, "airing|end=2025-06-30|eps=24")
}

func TestAniListToScrapedItemRomajiFallback(t *testing.T) {
	p := &AniListProvider{}
	m := anilistMedia{
		ID:    102,
		IDMal: 202,
		Title: struct {
			Romaji  string `json:"romaji"`
			English string `json:"english"`
		}{Romaji: "Romaji Title", English: ""},
		AverageScore: 80,
		StartDate:    anilistFuzzyDate{Year: 2025, Month: 1, Day: 1},
		CoverImage: struct {
			Large string `json:"large"`
		}{},
	}

	item := p.toScrapedItem(m, "anilist")
	assert.Equal(t, "Romaji Title", item.Title)
}

func TestAniListFuzzyDate(t *testing.T) {
	d := anilistFuzzyDate{Year: 2025, Month: 5, Day: 27}
	assert.False(t, d.IsZero())
	assert.Equal(t, "2025-05-27", d.ToTime().Format("2006-01-02"))

	zero := anilistFuzzyDate{}
	assert.True(t, zero.IsZero())
	assert.True(t, zero.ToTime().IsZero())
}
