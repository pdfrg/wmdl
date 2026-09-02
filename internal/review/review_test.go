package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestParsePosterMode(t *testing.T) {
	tests := []struct {
		input string
		want  PosterMode
	}{
		{"kitty", PosterKitty},
		{"KITTY", PosterKitty},
		{"text", PosterText},
		{"off", PosterOff},
		{"auto", PosterAuto},
		{"", PosterAuto},
		{"unknown", PosterAuto},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, ParsePosterMode(tt.input))
		})
	}
}

func TestCenterText(t *testing.T) {
	tests := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{"shorter_odd_pad", "a", 5, "  a  "},
		{"shorter_even_pad", "ab", 6, "  ab  "},
		{"exact", "hello", 5, "hello"},
		{"longer_truncated", "hello", 3, "hel"},
		{"empty", "", 4, "    "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, centerText(tt.s, tt.w))
		})
	}
}

func TestRenderTextPlaceholder(t *testing.T) {
	t.Run("default_size", func(t *testing.T) {
		result := renderTextPlaceholder(30, 10)
		lines := strings.Split(result, "\n")
		assert.Equal(t, 10, len(lines))
		assert.True(t, strings.HasPrefix(lines[0], "┌"))
		assert.True(t, strings.HasSuffix(lines[0], "┐"))
		assert.True(t, strings.HasPrefix(lines[9], "└"))
		// Should contain centered POSTER / NOT / AVAILABLE
		assert.Contains(t, result, "POSTER")
		assert.Contains(t, result, "NOT")
		assert.Contains(t, result, "AVAILABLE")
	})

	t.Run("minimum_size", func(t *testing.T) {
		result := renderTextPlaceholder(3, 2)
		lines := strings.Split(result, "\n")
		assert.Equal(t, 4, len(lines), "should enforce minimum cols=10, rows=4")
	})

	t.Run("small_cols", func(t *testing.T) {
		result := renderTextPlaceholder(6, 6)
		lines := strings.Split(result, "\n")
		assert.True(t, len(lines[0]) > 5)
	})
}

func TestCountryFlag(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"us", "\U0001F1FA\U0001F1F8"},
		{"US", "\U0001F1FA\U0001F1F8"},
		{"gb", "\U0001F1EC\U0001F1E7"},
		{"jp", "\U0001F1EF\U0001F1F5"},
		{"", ""},
		{"usa", ""},
		{"u", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			assert.Equal(t, tt.want, countryFlag(tt.code))
		})
	}
}

func TestLanguageName(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"en", "English"},
		{"EN", "English"},
		{"fr", "French"},
		{"ja", "Japanese"},
		{"xx", "XX"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			assert.Equal(t, tt.want, languageName(tt.code))
		})
	}

	t.Run("all_known_codes_return_something", func(t *testing.T) {
		for code := range languageNames {
			name := languageName(code)
			assert.NotEmpty(t, name, "code %q should have a name", code)
			assert.NotEqual(t, strings.ToUpper(code), name, "code %q should not be fallback", code)
		}
	})
}

func TestAlbumNotesURL(t *testing.T) {
	tests := []struct {
		name  string
		notes string
		want  string
	}{
		{"rpcharts https url", "https://radioparadise.com/music/album/26434", "https://radioparadise.com/music/album/26434"},
		{"http url", "http://example.com/album/1", "http://example.com/album/1"},
		{"url with surrounding space", "  https://radioparadise.com/music/album/26434  ", "https://radioparadise.com/music/album/26434"},
		{"url with stations segment", "https://radioparadise.com/music/album/26434|stations: Main Mix, RockIt!", "https://radioparadise.com/music/album/26434"},
		{"stations only", "stations: Main Mix", ""},
		{"plain text", "AllMusic Editor's Choice", ""},
		{"key=value notes", "url=https://x.example|ean=123", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &model.AlbumReleaseEvent{Notes: tt.notes}
			assert.Equal(t, tt.want, albumNotesURL(ev))
		})
	}

	t.Run("nil_event_returns_empty", func(t *testing.T) {
		assert.Empty(t, albumNotesURL(nil))
	})
}

func TestAlbumStations(t *testing.T) {
	tests := []struct {
		name  string
		notes string
		want  string
	}{
		{"url and stations", "https://radioparadise.com/music/album/26434|stations: Main Mix, RockIt!", "Main Mix, RockIt!"},
		{"stations only", "stations: Serenity", "Serenity"},
		{"no stations segment", "https://radioparadise.com/music/album/26434", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &model.AlbumReleaseEvent{Notes: tt.notes}
			assert.Equal(t, tt.want, albumStations(ev))
		})
	}

	t.Run("nil_event_returns_empty", func(t *testing.T) {
		assert.Empty(t, albumStations(nil))
	})
}

func TestMusicDateLabel(t *testing.T) {
	tests := []struct {
		name   string
		source string
		date   string
		want   string
	}{
		{"rpcharts labeled", "rpcharts", "2026-07-31", "first seen by rpcharts: 2026-07-31"},
		{"rpcharts empty date", "rpcharts", "", ""},
		{"allmusic month", "allmusic", "2026-06-01", "June 2026"},
		{"allmusic unparseable date", "allmusic", "not-a-date", "not-a-date"},
		{"plain date", "albumoftheyear", "2026-06-05", "2026-06-05"},
		{"unknown source", "tmdb", "2026-06-05", "2026-06-05"},
		{"empty source", "", "2026-06-05", "2026-06-05"},
		{"empty date", "albumoftheyear", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, musicDateLabel(tt.source, tt.date))
		})
	}
}

func TestLibraryInfo_AlbumDownloadStatus(t *testing.T) {
	album := &db.EventWithAlbumRelease{
		Event: &model.AlbumReleaseEvent{},
		Release: &model.AlbumRelease{
			ArtistName: "The All-American Rejects",
			Title:      "Sandbox",
			MBID:       "4c379675-b7c0-41ae-a613-e5519dee7de4",
			ArtistMBID: "09885b8e-f235-4b80-a02a-055539493173",
		},
	}
	it := &itemState{albumEvent: album}
	artistKey := "lidarr:" + album.Release.ArtistMBID
	albumKey := "lidarr-album:" + album.Release.MBID
	artistCache := &db.LibraryCache{Source: "lidarr", ExtID: album.Release.ArtistMBID, ArrTitle: "The All-American Rejects"}

	withAlbum := func(details string) map[string]*db.LibraryCache {
		return map[string]*db.LibraryCache{
			artistKey: artistCache,
			albumKey:  {Source: "lidarr-album", ExtID: album.Release.MBID, Details: details},
		}
	}

	t.Run("downloaded", func(t *testing.T) {
		info := it.libraryInfo(withAlbum(`{"statistics":{"trackFileCount":12,"trackCount":12}}`))
		assert.Equal(t, libFull, info.status)
		assert.Contains(t, info.label, "album in library")
	})
	t.Run("listed but not downloaded", func(t *testing.T) {
		info := it.libraryInfo(withAlbum(`{"statistics":{"trackFileCount":0,"trackCount":12}}`))
		assert.Equal(t, libPartial, info.status)
		assert.Contains(t, info.label, "not downloaded")
	})
	t.Run("stale cache without statistics", func(t *testing.T) {
		info := it.libraryInfo(withAlbum(`{"title":"Sandbox","monitored":false}`))
		assert.Equal(t, libPartial, info.status)
		assert.Contains(t, info.label, "not downloaded")
	})
	t.Run("artist only", func(t *testing.T) {
		info := it.libraryInfo(map[string]*db.LibraryCache{artistKey: artistCache})
		assert.Equal(t, libPartial, info.status)
		assert.Contains(t, info.label, "album not found")
	})
	t.Run("not in library", func(t *testing.T) {
		info := it.libraryInfo(map[string]*db.LibraryCache{})
		assert.Equal(t, libNone, info.status)
	})
}

func TestParseLidarrAlbumStats(t *testing.T) {
	assert.Nil(t, parseLidarrAlbumStats(nil))

	downloaded := parseLidarrAlbumStats(&db.LibraryCache{Details: `{"statistics":{"trackFileCount":12,"trackCount":12,"percentOfTracks":100}}`})
	assert.NotNil(t, downloaded)
	assert.Equal(t, 12, downloaded.TrackFileCount)

	empty := parseLidarrAlbumStats(&db.LibraryCache{Details: `{"statistics":{"trackFileCount":0,"trackCount":12}}`})
	assert.NotNil(t, empty)
	assert.Equal(t, 0, empty.TrackFileCount)

	assert.Nil(t, parseLidarrAlbumStats(&db.LibraryCache{Details: `{"title":"Sandbox"}`}))
	assert.Nil(t, parseLidarrAlbumStats(&db.LibraryCache{Details: "not-json"}))
}
