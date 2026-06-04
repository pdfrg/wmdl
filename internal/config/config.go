package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Log              LogConfig        `mapstructure:"log"`
	Hardcover        HardcoverConfig  `mapstructure:"hardcover"`
	Notifier         NotifierConfig   `mapstructure:"notifier"`
	Browser          BrowserConfig    `mapstructure:"browser"`
	Prowlarr         ProwlarrConfig   `mapstructure:"prowlarr"`
	TMDB             TMDBConfig       `mapstructure:"tmdb"`
	Downloader       DownloaderConfig `mapstructure:"downloader"`
	Library          LibraryConfig    `mapstructure:"library"`
	Quality          QualityConfig    `mapstructure:"quality"`
	MediaTypes       MediaTypesConfig `mapstructure:"media_types"`
	ShowTopN         int              `mapstructure:"show_top_n"`
	MinSeeders       int              `mapstructure:"min_seeders"`
	PreferredGroups  []string         `mapstructure:"preferred_release_groups"`
	PosterMode       string           `mapstructure:"poster_mode"`
	ProcessMode      string           `mapstructure:"process_mode"`
	CheckCollections bool             `mapstructure:"check_collections"`
	CacheTTLHours    int              `mapstructure:"cache_ttl_hours"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

type NotifierConfig struct {
	Service        string `mapstructure:"service"`
	URL            string `mapstructure:"url"`
	Token          string `mapstructure:"token"`
	CustomTemplate string `mapstructure:"custom_template"`
}

type BrowserConfig struct {
	DebugPort int    `mapstructure:"debug_port"`
	Profile   string `mapstructure:"profile"`
	Binary    string `mapstructure:"binary"`
}

type ProwlarrConfig struct {
	URL        string             `mapstructure:"url"`
	APIKey     string             `mapstructure:"api_key"`
	Timeout    int                `mapstructure:"timeout"`
	IndexerIDs IndexerPerCategory `mapstructure:"indexer_id"`
}

type IndexerPerCategory struct {
	Videos int `mapstructure:"videos"`
	Music  int `mapstructure:"music"`
	Anime  int `mapstructure:"anime"`
	Books  int `mapstructure:"books"`
}

type TMDBConfig struct {
	APIKey      string `mapstructure:"api_key"`
	AccessToken string `mapstructure:"access_token"`
}

type HardcoverConfig struct {
	APIKey string `mapstructure:"api_key"`
}

type DownloaderConfig struct {
	Type         string             `mapstructure:"type"`
	Qbittorrent  QbittorrentConfig  `mapstructure:"qbittorrent"`
	Transmission TransmissionConfig `mapstructure:"transmission"`
	Deluge       DelugeConfig       `mapstructure:"deluge"`
	Categories   CategoryConfig     `mapstructure:"categories"`
}

type QbittorrentConfig struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

type TransmissionConfig struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

type DelugeConfig struct {
	URL      string `mapstructure:"url"`
	Password string `mapstructure:"password"`
}

type CategoryConfig struct {
	Movies     string `mapstructure:"movies"`
	TV         string `mapstructure:"tv"`
	Music      string `mapstructure:"music"`
	Anime      string `mapstructure:"anime"`
	Ebooks     string `mapstructure:"ebooks"`
	Audiobooks string `mapstructure:"audiobooks"`
}

type LibraryConfig struct {
	Radarr         RadarrConfig         `mapstructure:"radarr"`
	Sonarr         SonarrConfig         `mapstructure:"sonarr"`
	Lidarr         LidarrConfig         `mapstructure:"lidarr"`
	Audiobookshelf AudiobookshelfConfig `mapstructure:"audiobookshelf"`
}

type AudiobookshelfConfig struct {
	URL       string `mapstructure:"url"`
	APIKey    string `mapstructure:"api_key"`
	LibraryID string `mapstructure:"library_id"`
	Timeout   int    `mapstructure:"timeout"`
}

type LidarrConfig struct {
	URL              string `mapstructure:"url"`
	APIKey           string `mapstructure:"api_key"`
	RootFolder       string `mapstructure:"root_folder"`
	QualityProfile   string `mapstructure:"quality_profile"`
	MetadataProfile  string `mapstructure:"metadata_profile"`
	Monitor          string `mapstructure:"monitor"`
	MonitorNewAlbums bool   `mapstructure:"monitor_new_albums"`
	Timeout          int    `mapstructure:"timeout"`
}

type RadarrConfig struct {
	URL            string `mapstructure:"url"`
	APIKey         string `mapstructure:"api_key"`
	RootFolder     string `mapstructure:"root_folder"`
	QualityProfile string `mapstructure:"quality_profile"`
	Monitor        bool   `mapstructure:"monitor"`
	Timeout        int    `mapstructure:"timeout"`
}

type SonarrConfig struct {
	URL                string `mapstructure:"url"`
	APIKey             string `mapstructure:"api_key"`
	RootFolder         string `mapstructure:"root_folder"`
	QualityProfile     string `mapstructure:"quality_profile"`
	MonitorNewEpisodes bool   `mapstructure:"monitor_new_episodes"`
	SeasonFolders      bool   `mapstructure:"season_folders"`
	Timeout            int    `mapstructure:"timeout"`
}

type QualityConfig struct {
	Movies MediaQualityConfig `mapstructure:"movies"`
	TV     MediaQualityConfig `mapstructure:"tv"`
	Music  MusicQualityConfig `mapstructure:"music"`
	Anime  MediaQualityConfig `mapstructure:"anime"`
	Books  BookQualityConfig  `mapstructure:"books"`
}

type MediaQualityConfig struct {
	Resolution     string   `mapstructure:"resolution"`
	PreferHDR      bool     `mapstructure:"prefer_hdr"`
	SourcePriority []string `mapstructure:"source_priority"`
	CodecPriority  []string `mapstructure:"codec_priority"`
}

type MusicQualityConfig struct {
	FormatPriority  []string `mapstructure:"format_priority"`
	BitratePriority []string `mapstructure:"bitrate_priority"`
}

type BookQualityConfig struct {
	Ebooks     BookFormatQualityConfig `mapstructure:"ebooks"`
	Audiobooks BookFormatQualityConfig `mapstructure:"audiobooks"`
}

type BookFormatQualityConfig struct {
	FormatPriority []string `mapstructure:"format_priority"`
}

type MediaTypesConfig struct {
	Movies bool        `mapstructure:"movies"`
	TV     bool        `mapstructure:"tv"`
	Music  MusicConfig `mapstructure:"music"`
	Anime  AnimeConfig `mapstructure:"anime"`
	Books  BookConfig  `mapstructure:"books"`
}

type MusicConfig struct {
	Enabled               bool              `mapstructure:"enabled"`
	InitialTimeshiftWeeks int               `mapstructure:"initial_timeshift_weeks"`
	Filter                MusicFilterConfig `mapstructure:"filter"`
}

type MusicFilterConfig struct {
	MinCriticScore   int  `mapstructure:"min_critic_score"`
	MinCriticReviews int  `mapstructure:"min_critic_reviews"`
	MinUserScore     int  `mapstructure:"min_user_score"`
	MinUserRatings   int  `mapstructure:"min_user_ratings"`
	IncludeMustHear  bool `mapstructure:"include_must_hear"`
}

type AnimeConfig struct {
	Enabled          bool    `mapstructure:"enabled"`
	MinScore         float64 `mapstructure:"min_score"`
	MinMembers       int     `mapstructure:"min_members"`
	PhaseBEnabled    bool    `mapstructure:"phase_b_enabled"`
	PhaseBMinScore   float64 `mapstructure:"phase_b_min_score"`
	PhaseBMinMembers int     `mapstructure:"phase_b_min_members"`
	MinPhaseBResults int     `mapstructure:"min_phase_b_results"`
}

type BookConfig struct {
	Enabled               bool             `mapstructure:"enabled"`
	InitialTimeshiftWeeks int              `mapstructure:"initial_timeshift_weeks"`
	DefaultFormat         string           `mapstructure:"default_format"`
	Filter                BookFilterConfig `mapstructure:"filter"`
}

type BookFilterConfig struct {
	MinRating  float64 `mapstructure:"min_rating"`
	MinRatings int     `mapstructure:"min_ratings"`
}

const configName = "config"
const appName = "wmdl"

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName(configName)
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		v.AddConfigPath(filepath.Join(xdg, appName))
	}
	if home := os.Getenv("HOME"); home != "" {
		v.AddConfigPath(filepath.Join(home, ".config", appName))
	}
	v.AddConfigPath(".")

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []string

	if c.TMDB.APIKey == "" {
		errs = append(errs, "tmdb.api_key is required (get one at https://www.themoviedb.org/settings/api)")
	}
	if c.Prowlarr.URL == "" {
		errs = append(errs, "prowlarr.url is required")
	}
	if c.Prowlarr.APIKey == "" {
		errs = append(errs, "prowlarr.api_key is required")
	}

	switch c.Downloader.Type {
	case "qbittorrent":
		if c.Downloader.Qbittorrent.URL == "" {
			errs = append(errs, "downloader.qbittorrent.url is required when downloader.type is qbittorrent")
		}
	case "transmission":
		if c.Downloader.Transmission.URL == "" {
			errs = append(errs, "downloader.transmission.url is required when downloader.type is transmission")
		}
	case "deluge":
		if c.Downloader.Deluge.URL == "" {
			errs = append(errs, "downloader.deluge.url is required when downloader.type is deluge")
		}
	case "":
		errs = append(errs, "downloader.type is required (qbittorrent, transmission, or deluge)")
	default:
		errs = append(errs, fmt.Sprintf("unknown downloader.type %q (must be qbittorrent, transmission, or deluge)", c.Downloader.Type))
	}

	// Validate Radarr config if URL is set (partially configured)
	if c.Library.Radarr.URL != "" && c.Library.Radarr.APIKey == "" {
		errs = append(errs, "library.radarr.api_key is required when library.radarr.url is set")
	}

	// Validate Sonarr config if URL is set (partially configured)
	if c.Library.Sonarr.URL != "" && c.Library.Sonarr.APIKey == "" {
		errs = append(errs, "library.sonarr.api_key is required when library.sonarr.url is set")
	}

	// Validate Audiobookshelf config if URL is set (partially configured)
	if c.Library.Audiobookshelf.URL != "" && c.Library.Audiobookshelf.APIKey == "" {
		errs = append(errs, "library.audiobookshelf.api_key is required when library.audiobookshelf.url is set")
	}

	// Validate log level
	// Validate book config
	if c.MediaTypes.Books.Enabled {
		switch c.MediaTypes.Books.DefaultFormat {
		case "ebook", "audiobook", "both", "":
		default:
			errs = append(errs, "media_types.books.default_format must be one of: ebook, audiobook, both")
		}
		if c.MediaTypes.Books.InitialTimeshiftWeeks < 0 {
			errs = append(errs, "media_types.books.initial_timeshift_weeks must be >= 0")
		}
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error", "":
	default:
		errs = append(errs, fmt.Sprintf("log.level must be one of: debug, info, warn, error"))
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("browser.debug_port", 9222)
	v.SetDefault("browser.profile", "wmd-review")
	v.SetDefault("browser.binary", "brave")
	v.SetDefault("downloader.type", "qbittorrent")
	v.SetDefault("downloader.categories.movies", "Movies")
	v.SetDefault("downloader.categories.tv", "TV")
	v.SetDefault("downloader.categories.music", "Music")
	v.SetDefault("show_top_n", 10)
	v.SetDefault("min_seeders", 3)
	v.SetDefault("preferred_release_groups", []string{})
	v.SetDefault("poster_mode", "auto")
	v.SetDefault("process_mode", "batch")
	v.SetDefault("check_collections", true)
	v.SetDefault("cache_ttl_hours", 48)
	v.SetDefault("log.level", "info")

	v.SetDefault("quality.movies.resolution", "2160p")
	v.SetDefault("quality.movies.prefer_hdr", true)
	v.SetDefault("quality.movies.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.movies.codec_priority", []string{"h265", "x265", "h264", "av1"})
	v.SetDefault("quality.tv.resolution", "1080p")
	v.SetDefault("quality.tv.prefer_hdr", false)
	v.SetDefault("quality.tv.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.tv.codec_priority", []string{"h265", "x265", "h264", "av1"})
	v.SetDefault("prowlarr.timeout", 120)
	v.SetDefault("prowlarr.indexer_id.videos", 0)
	v.SetDefault("prowlarr.indexer_id.music", 0)
	v.SetDefault("prowlarr.indexer_id.anime", 0)
	v.SetDefault("prowlarr.indexer_id.books", 0)
	v.SetDefault("library.radarr.monitor", false)
	v.SetDefault("library.radarr.timeout", 120)
	v.SetDefault("library.sonarr.monitor_new_episodes", true)
	v.SetDefault("library.sonarr.season_folders", true)
	v.SetDefault("library.sonarr.timeout", 120)
	v.SetDefault("library.lidarr.timeout", 120)
	v.SetDefault("library.lidarr.monitor_new_albums", true)
	v.SetDefault("library.lidarr.monitor", "all")

	v.SetDefault("media_types.movies", true)
	v.SetDefault("media_types.tv", true)
	v.SetDefault("quality.music.format_priority", []string{"flac", "mp3", "aac"})
	v.SetDefault("quality.music.bitrate_priority", []string{"lossless", "320", "v0", "v2"})
	v.SetDefault("media_types.music.enabled", true)
	v.SetDefault("media_types.music.initial_timeshift_weeks", 1)
	v.SetDefault("media_types.music.filter.min_critic_score", 75)
	v.SetDefault("media_types.music.filter.min_critic_reviews", 5)
	v.SetDefault("media_types.music.filter.min_user_score", 75)
	v.SetDefault("media_types.music.filter.min_user_ratings", 200)
	v.SetDefault("media_types.music.filter.include_must_hear", true)

	v.SetDefault("media_types.anime.enabled", true)
	v.SetDefault("media_types.anime.min_score", 7.0)
	v.SetDefault("media_types.anime.min_members", 50000)
	v.SetDefault("media_types.anime.phase_b_enabled", true)
	v.SetDefault("media_types.anime.phase_b_min_score", 7.5)
	v.SetDefault("media_types.anime.phase_b_min_members", 100000)
	v.SetDefault("media_types.anime.min_phase_b_results", 3)

	v.SetDefault("quality.anime.resolution", "1080p")
	v.SetDefault("quality.anime.prefer_hdr", false)
	v.SetDefault("quality.anime.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.anime.codec_priority", []string{"h265", "h264", "av1"})

	v.SetDefault("downloader.categories.anime", "Anime")

	v.SetDefault("downloader.categories.ebooks", "Ebooks")
	v.SetDefault("downloader.categories.audiobooks", "Audiobooks")

	v.SetDefault("quality.books.ebooks.format_priority", []string{"epub", "mobi", "azw3", "pdf"})
	v.SetDefault("quality.books.audiobooks.format_priority", []string{"m4b", "mp3", "flac", "aac", "opus"})

	v.SetDefault("media_types.books.enabled", true)
	v.SetDefault("media_types.books.initial_timeshift_weeks", 1)
	v.SetDefault("media_types.books.default_format", "both")
	v.SetDefault("media_types.books.filter.min_rating", 3.5)
	v.SetDefault("media_types.books.filter.min_ratings", 100)

	v.SetDefault("library.audiobookshelf.timeout", 60)
	v.SetDefault("hardcover.api_key", "")
}
