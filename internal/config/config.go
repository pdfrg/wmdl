package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Notifier        NotifierConfig   `mapstructure:"notifier"`
	Browser         BrowserConfig    `mapstructure:"browser"`
	Prowlarr        ProwlarrConfig   `mapstructure:"prowlarr"`
	TMDB            TMDBConfig       `mapstructure:"tmdb"`
	Downloader      DownloaderConfig `mapstructure:"downloader"`
	Library         LibraryConfig    `mapstructure:"library"`
	Quality         QualityConfig    `mapstructure:"quality"`
	ShowTopN        int              `mapstructure:"show_top_n"`
	MinSeeders      int              `mapstructure:"min_seeders"`
	PreferredGroups []string         `mapstructure:"preferred_release_groups"`
	PosterMode      string           `mapstructure:"poster_mode"`
	ProcessMode     string           `mapstructure:"process_mode"`
}

type NotifierConfig struct {
	GotifyURL   string `mapstructure:"gotify_url"`
	GotifyToken string `mapstructure:"gotify_token"`
}

type BrowserConfig struct {
	DebugPort int    `mapstructure:"debug_port"`
	Profile   string `mapstructure:"profile"`
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
}

type SonarrConfig struct {
	URL                string `mapstructure:"url"`
	APIKey             string `mapstructure:"api_key"`
	RootFolder         string `mapstructure:"root_folder"`
	QualityProfile     string `mapstructure:"quality_profile"`
	MonitorNewEpisodes bool   `mapstructure:"monitor_new_episodes"`
	SeasonFolders      bool   `mapstructure:"season_folders"`
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
const appName = "wmd"

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

func setDefaults(v *viper.Viper) {
	v.SetDefault("browser.debug_port", 9222)
	v.SetDefault("browser.profile", "wmd-review")
	v.SetDefault("downloader.type", "qbittorrent")
	v.SetDefault("downloader.categories.movies", "Movies")
	v.SetDefault("downloader.categories.tv", "TV")
	v.SetDefault("show_top_n", 10)
	v.SetDefault("min_seeders", 3)
	v.SetDefault("preferred_release_groups", []string{})
	v.SetDefault("poster_mode", "auto")
	v.SetDefault("process_mode", "batch")
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
	v.SetDefault("library.sonarr.monitor_new_episodes", true)
	v.SetDefault("library.sonarr.season_folders", true)
}
