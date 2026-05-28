package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Notifier         NotifierConfig   `mapstructure:"notifier"`
	Browser          BrowserConfig    `mapstructure:"browser"`
	Prowlarr         ProwlarrConfig   `mapstructure:"prowlarr"`
	TMDB             TMDBConfig       `mapstructure:"tmdb"`
	Downloader       DownloaderConfig `mapstructure:"downloader"`
	Library          LibraryConfig    `mapstructure:"library"`
	Quality          QualityConfig    `mapstructure:"quality"`
	ShowTopN         int              `mapstructure:"show_top_n"`
	MinSeeders       int              `mapstructure:"min_seeders"`
	PreferredGroups  []string         `mapstructure:"preferred_release_groups"`
	PosterMode       string           `mapstructure:"poster_mode"`
	ProcessMode      string           `mapstructure:"process_mode"`
	CheckCollections bool             `mapstructure:"check_collections"`
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
	URL       string `mapstructure:"url"`
	APIKey    string `mapstructure:"api_key"`
	Timeout   int    `mapstructure:"timeout"`
	IndexerID int    `mapstructure:"indexer_id"`
}

type TMDBConfig struct {
	APIKey      string `mapstructure:"api_key"`
	AccessToken string `mapstructure:"access_token"`
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
	Movies string `mapstructure:"movies"`
	TV     string `mapstructure:"tv"`
}

type LibraryConfig struct {
	Radarr RadarrConfig `mapstructure:"radarr"`
	Sonarr SonarrConfig `mapstructure:"sonarr"`
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
}

type MediaQualityConfig struct {
	Resolution     string   `mapstructure:"resolution"`
	PreferHDR      bool     `mapstructure:"prefer_hdr"`
	SourcePriority []string `mapstructure:"source_priority"`
	CodecPriority  []string `mapstructure:"codec_priority"`
}

const configName = "config"
const appName = "wmdl"

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName(configName)
	v.AddConfigPath(fmt.Sprintf("$XDG_CONFIG_HOME/%s", appName))
	v.AddConfigPath(fmt.Sprintf("$HOME/.config/%s", appName))
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
	v.SetDefault("show_top_n", 10)
	v.SetDefault("min_seeders", 3)
	v.SetDefault("preferred_release_groups", []string{})
	v.SetDefault("poster_mode", "auto")
	v.SetDefault("process_mode", "batch")
	v.SetDefault("check_collections", true)
	v.SetDefault("quality.movies.resolution", "2160p")
	v.SetDefault("quality.movies.prefer_hdr", true)
	v.SetDefault("quality.movies.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.movies.codec_priority", []string{"h265", "x265", "h264", "av1"})
	v.SetDefault("quality.tv.resolution", "1080p")
	v.SetDefault("quality.tv.prefer_hdr", false)
	v.SetDefault("quality.tv.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.tv.codec_priority", []string{"h265", "x265", "h264", "av1"})
	v.SetDefault("prowlarr.timeout", 120)
	v.SetDefault("prowlarr.indexer_id", 0)
	v.SetDefault("library.radarr.monitor", false)
	v.SetDefault("library.radarr.timeout", 120)
	v.SetDefault("library.sonarr.monitor_new_episodes", true)
	v.SetDefault("library.sonarr.season_folders", true)
	v.SetDefault("library.sonarr.timeout", 120)
}
