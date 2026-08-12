package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		TMDB: TMDBConfig{APIKey: "tmdb-key"},
		Prowlarr: ProwlarrConfig{
			URL:    "http://prowlarr:9696",
			APIKey: "prowlarr-key",
		},
		Downloader: DownloaderConfig{
			Type: "qbittorrent",
			Qbittorrent: QbittorrentConfig{
				URL: "http://qb:8080",
			},
		},
		MediaTypes: MediaTypesConfig{
			Movies: MovieConfig{Enabled: true, Mode: "full"},
			TV:     TVConfig{Enabled: true, Mode: "full"},
		},
		Library: LibraryConfig{
			Radarr: RadarrConfig{URL: "http://radarr:7878", APIKey: "radarr-key"},
			Sonarr: SonarrConfig{URL: "http://sonarr:8989", APIKey: "sonarr-key"},
		},
		Log: LogConfig{Level: "info"},
	}
}

func TestValidateMode(t *testing.T) {
	modes := []string{"full", "prowlarr-grab", "arr", "auto", "yolo", "batch"}
	for _, m := range modes {
		t.Run("valid_"+m, func(t *testing.T) {
			cfg := validConfig()
			cfg.MediaTypes.Movies.Mode = m
			assert.NoError(t, cfg.Validate())
		})
	}

	t.Run("empty_mode", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = ""
		assert.NoError(t, cfg.Validate())
	})

	t.Run("invalid_movies", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "bogus"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "media_types.movies.mode")
	})

	t.Run("invalid_tv", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.TV.Mode = "bogus"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "media_types.tv.mode")
	})

	t.Run("invalid_music", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Music.Mode = "bogus"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "media_types.music.mode")
	})

	t.Run("invalid_anime", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Anime.Mode = "bogus"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "media_types.anime.mode")
	})

	t.Run("invalid_books", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Books.Mode = "bogus"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "media_types.books.mode")
	})
}

func TestValidateTMDB(t *testing.T) {
	cfg := validConfig()
	cfg.TMDB.APIKey = ""
	err := cfg.Validate()
	assert.ErrorContains(t, err, "tmdb.api_key")
}

func TestValidateProwlarr(t *testing.T) {
	t.Run("required_for_full", func(t *testing.T) {
		cfg := validConfig()
		cfg.Prowlarr.URL = ""
		cfg.Prowlarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "prowlarr.url")
		assert.ErrorContains(t, err, "prowlarr.api_key")
	})

	t.Run("missing_api_key", func(t *testing.T) {
		cfg := validConfig()
		cfg.Prowlarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "prowlarr.api_key")
	})

	t.Run("not_required_for_arr", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "arr"
		cfg.MediaTypes.TV.Mode = "arr"
		cfg.Prowlarr.URL = ""
		cfg.Prowlarr.APIKey = ""
		assert.NoError(t, cfg.Validate())
	})
}

func TestValidateDownloader(t *testing.T) {
	t.Run("type_required_for_full", func(t *testing.T) {
		cfg := validConfig()
		cfg.Downloader.Type = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.type is required for full mode")
	})

	t.Run("qbittorrent_url", func(t *testing.T) {
		cfg := validConfig()
		cfg.Downloader.Qbittorrent.URL = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.qbittorrent.url")
	})

	t.Run("transmission_url", func(t *testing.T) {
		cfg := validConfig()
		cfg.Downloader.Type = "transmission"
		cfg.Downloader.Transmission.URL = ""
		cfg.Downloader.Qbittorrent.URL = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.transmission.url")
	})

	t.Run("deluge_url", func(t *testing.T) {
		cfg := validConfig()
		cfg.Downloader.Type = "deluge"
		cfg.Downloader.Deluge.URL = ""
		cfg.Downloader.Qbittorrent.URL = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.deluge.url")
	})

	t.Run("invalid_type", func(t *testing.T) {
		cfg := validConfig()
		cfg.Downloader.Type = "aria2"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.type")
	})

	t.Run("not_checked_when_not_full", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "arr"
		cfg.MediaTypes.TV.Mode = "arr"
		cfg.Downloader.Type = ""
		cfg.Downloader.Qbittorrent.URL = ""
		assert.NoError(t, cfg.Validate())
	})

	t.Run("valid_type_no_full_mode_still_validates_type_value", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "arr"
		cfg.MediaTypes.TV.Mode = "arr"
		cfg.Downloader.Type = "aria2"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "downloader.type")
	})
}

func TestValidateArrMode(t *testing.T) {
	t.Run("radarr_required_for_movies_arr", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "arr"
		cfg.MediaTypes.TV.Mode = "full"
		cfg.Library.Radarr.URL = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.radarr.url")
	})

	t.Run("radarr_key_for_movies_arr", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Mode = "arr"
		cfg.MediaTypes.TV.Mode = "full"
		cfg.Library.Radarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.radarr.api_key")
	})

	t.Run("sonarr_required_for_tv_arr", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.TV.Mode = "arr"
		cfg.MediaTypes.Movies.Mode = "full"
		cfg.Library.Sonarr.URL = ""
		cfg.Library.Sonarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.sonarr.url")
	})

	t.Run("sonarr_required_for_anime_yolo", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Enabled = false
		cfg.MediaTypes.TV.Enabled = false
		cfg.MediaTypes.Anime.Enabled = true
		cfg.MediaTypes.Anime.Mode = "yolo"
		cfg.Library.Sonarr.URL = ""
		cfg.Library.Sonarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.sonarr.url")
	})

	t.Run("lidarr_required_for_music_auto", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Enabled = false
		cfg.MediaTypes.TV.Enabled = false
		cfg.MediaTypes.Music.Enabled = true
		cfg.MediaTypes.Music.Mode = "auto"
		cfg.Library.Lidarr.URL = ""
		cfg.Library.Lidarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.lidarr.url")
	})

	t.Run("lazylibrarian_required_for_books_arr", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Movies.Enabled = false
		cfg.MediaTypes.TV.Enabled = false
		cfg.MediaTypes.Books.Enabled = true
		cfg.MediaTypes.Books.Mode = "arr"
		cfg.MediaTypes.Books.DefaultFormat = "ebook"
		cfg.Library.LazyLibrarian.URL = ""
		cfg.Library.LazyLibrarian.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.lazylibrarian.url")
	})
}

func TestValidatePartialConfig(t *testing.T) {
	t.Run("radarr_url_no_key", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.Radarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.radarr.api_key")
	})

	t.Run("sonarr_url_no_key", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.Sonarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.sonarr.api_key")
	})

	t.Run("lidarr_url_no_key", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.Lidarr.URL = "http://lidarr:8686"
		cfg.Library.Lidarr.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.lidarr.api_key")
	})

	t.Run("lazylibrarian_url_no_key", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.LazyLibrarian.URL = "http://ll:5299"
		cfg.Library.LazyLibrarian.APIKey = ""
		err := cfg.Validate()
		assert.ErrorContains(t, err, "library.lazylibrarian.api_key")
	})
}

func TestValidateLookbacks(t *testing.T) {
	tests := []struct {
		name  string
		tweak func(cfg *Config)
		err   string
	}{
		{"music_negative", func(cfg *Config) { cfg.MediaTypes.Music.LookbackWeeks = -1 }, "music.lookback_weeks"},
		{"movies_streaming_negative", func(cfg *Config) { cfg.MediaTypes.Movies.StreamingLookbackWeeks = -1 }, "movies.streaming_lookback_weeks"},
		{"tv_streaming_negative", func(cfg *Config) { cfg.MediaTypes.TV.StreamingLookbackWeeks = -1 }, "tv.streaming_lookback_weeks"},
		{"physical_negative", func(cfg *Config) { cfg.MediaTypes.PhysicalLookbackWeeks = -1 }, "physical_lookback_weeks"},
		{"anime_negative", func(cfg *Config) { cfg.MediaTypes.Anime.LookbackWeeks = -1 }, "anime.lookback_weeks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.tweak(&cfg)
			err := cfg.Validate()
			assert.ErrorContains(t, err, tt.err)
		})
	}

	t.Run("books_negative", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Books.Enabled = true
		cfg.MediaTypes.Books.LookbackWeeks = -1
		cfg.MediaTypes.Books.DefaultFormat = "ebook"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "books.lookback_weeks")
	})
}

func TestValidateBooks(t *testing.T) {
	t.Run("default_format_invalid", func(t *testing.T) {
		cfg := validConfig()
		cfg.MediaTypes.Books.Enabled = true
		cfg.MediaTypes.Books.DefaultFormat = "pdf"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "default_format")
	})

	t.Run("default_format_valid", func(t *testing.T) {
		tests := []string{"ebook", "audiobook", "both"}
		for _, f := range tests {
			cfg := validConfig()
			cfg.MediaTypes.Books.Enabled = true
			cfg.MediaTypes.Books.DefaultFormat = f
			assert.NoError(t, cfg.Validate())
		}
	})
}

func TestValidateLogLevel(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		cfg := validConfig()
		cfg.Log.Level = "trace"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "log.level")
	})

	t.Run("valid", func(t *testing.T) {
		for _, lvl := range []string{"debug", "info", "warn", "error", ""} {
			cfg := validConfig()
			cfg.Log.Level = lvl
			assert.NoError(t, cfg.Validate())
		}
	})
}

func TestValidateBookBackend(t *testing.T) {
	t.Run("lazylibrarian", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.BookBackend = "lazylibrarian"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("none", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.BookBackend = "none"
		assert.NoError(t, cfg.Validate())
	})

	t.Run("empty", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.BookBackend = ""
		assert.NoError(t, cfg.Validate())
	})

	t.Run("unknown", func(t *testing.T) {
		cfg := validConfig()
		cfg.Library.BookBackend = "calibre"
		err := cfg.Validate()
		assert.ErrorContains(t, err, "book_backend")
	})
}

func TestValidateSuccess(t *testing.T) {
	t.Run("minimal_valid", func(t *testing.T) {
		cfg := validConfig()
		assert.NoError(t, cfg.Validate())
	})

	t.Run("all_media_types_disabled", func(t *testing.T) {
		cfg := Config{
			TMDB: TMDBConfig{APIKey: "key"},
			MediaTypes: MediaTypesConfig{
				Movies: MovieConfig{Enabled: false},
				TV:     TVConfig{Enabled: false},
			},
			Log: LogConfig{Level: "info"},
		}
		assert.NoError(t, cfg.Validate())
	})
}

func TestUsedModes(t *testing.T) {
	t.Run("all_disabled", func(t *testing.T) {
		cfg := Config{}
		assert.Empty(t, cfg.UsedModes())
	})

	t.Run("movies_full", func(t *testing.T) {
		cfg := Config{}
		cfg.MediaTypes.Movies.Enabled = true
		cfg.MediaTypes.Movies.Mode = "full"
		assert.Equal(t, map[string]bool{"full": true}, cfg.UsedModes())
	})

	t.Run("multiple_modes", func(t *testing.T) {
		cfg := Config{}
		cfg.MediaTypes.Movies.Enabled = true
		cfg.MediaTypes.Movies.Mode = "full"
		cfg.MediaTypes.TV.Enabled = true
		cfg.MediaTypes.TV.Mode = "arr"
		cfg.MediaTypes.Music.Enabled = true
		cfg.MediaTypes.Music.Mode = "auto"
		modes := cfg.UsedModes()
		assert.True(t, modes["full"])
		assert.True(t, modes["arr"])
		assert.True(t, modes["auto"])
	})

	t.Run("duplicate_mode_deduped", func(t *testing.T) {
		cfg := Config{}
		cfg.MediaTypes.Movies.Enabled = true
		cfg.MediaTypes.Movies.Mode = "full"
		cfg.MediaTypes.TV.Enabled = true
		cfg.MediaTypes.TV.Mode = "full"
		modes := cfg.UsedModes()
		assert.Len(t, modes, 1)
		assert.True(t, modes["full"])
	})
}

func TestMediaTypeMode(t *testing.T) {
	cfg := Config{}
	cfg.MediaTypes.Movies.Mode = "arr"
	cfg.MediaTypes.TV.Mode = "auto"
	cfg.MediaTypes.Anime.Mode = "yolo"
	cfg.MediaTypes.Music.Mode = "full"
	cfg.MediaTypes.Books.Mode = "batch"

	assert.Equal(t, "arr", cfg.MediaTypeMode(model.MediaTypeMovie))
	assert.Equal(t, "auto", cfg.MediaTypeMode(model.MediaTypeTV))
	assert.Equal(t, "yolo", cfg.MediaTypeMode(model.MediaTypeAnime))
	assert.Equal(t, "full", cfg.MediaTypeMode(model.MediaTypeMusic))
	assert.Equal(t, "batch", cfg.MediaTypeMode(model.MediaTypeBook))
	assert.Equal(t, "full", cfg.MediaTypeMode(model.MediaType("unknown")))
}

func TestLoad(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		cfgDir := filepath.Join(dir, "wmdl")
		require.NoError(t, os.MkdirAll(cfgDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`
tmdb:
  api_key: test-key
prowlarr:
  url: "http://prowlarr:9696"
  api_key: prowlarr-key
`), 0644))
		t.Setenv("XDG_CONFIG_HOME", dir)

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, "test-key", cfg.TMDB.APIKey)
		assert.Equal(t, "http://prowlarr:9696", cfg.Prowlarr.URL)
		assert.Equal(t, "prowlarr-key", cfg.Prowlarr.APIKey)
	})

	t.Run("defaults_applied", func(t *testing.T) {
		dir := t.TempDir()
		cfgDir := filepath.Join(dir, "wmdl")
		require.NoError(t, os.MkdirAll(cfgDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`
tmdb:
  api_key: test-key
`), 0644))
		t.Setenv("XDG_CONFIG_HOME", dir)

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 9222, cfg.Browser.DebugPort)
		assert.Equal(t, "info", cfg.Log.Level)
	})

	t.Run("missing_file", func(t *testing.T) {
		dir := t.TempDir()
		cfgDir := filepath.Join(dir, "wmdl")
		require.NoError(t, os.MkdirAll(cfgDir, 0755))
		t.Setenv("XDG_CONFIG_HOME", dir)
		t.Setenv("HOME", dir)

		_, err := Load()
		assert.ErrorContains(t, err, "reading config")
	})
}

func TestLoadIndexerIDs(t *testing.T) {
	writeConfig := func(t *testing.T, body string) {
		t.Helper()
		dir := t.TempDir()
		cfgDir := filepath.Join(dir, "wmdl")
		require.NoError(t, os.MkdirAll(cfgDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(body), 0644))
		t.Setenv("XDG_CONFIG_HOME", dir)
	}

	t.Run("scalar ints decode to single-element lists", func(t *testing.T) {
		writeConfig(t, `
tmdb:
  api_key: test-key
prowlarr:
  url: "http://prowlarr:9696"
  api_key: prowlarr-key
  indexer_id:
    videos: 5
    music: 0
`)
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, []int{5}, cfg.Prowlarr.IndexerIDs.Videos)
		assert.Empty(t, cfg.Prowlarr.IndexerIDs.Music)
	})

	t.Run("lists decode in config order", func(t *testing.T) {
		writeConfig(t, `
tmdb:
  api_key: test-key
prowlarr:
  url: "http://prowlarr:9696"
  api_key: prowlarr-key
  indexer_id:
    videos: [5, 9, 12]
    anime:
      - 3
      - 8
    ebooks: 7
`)
		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, []int{5, 9, 12}, cfg.Prowlarr.IndexerIDs.Videos)
		assert.Equal(t, []int{3, 8}, cfg.Prowlarr.IndexerIDs.Anime)
		assert.Equal(t, []int{7}, cfg.Prowlarr.IndexerIDs.Ebooks)
	})

	t.Run("omitted categories default to empty", func(t *testing.T) {
		writeConfig(t, `
tmdb:
  api_key: test-key
`)
		cfg, err := Load()
		require.NoError(t, err)
		assert.Empty(t, cfg.Prowlarr.IndexerIDs.Videos)
		assert.Empty(t, cfg.Prowlarr.IndexerIDs.Audiobooks)
	})
}
