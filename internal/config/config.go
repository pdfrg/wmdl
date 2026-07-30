package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/pdfrg/wmdl/internal/model"
)

type Config struct {
	Log              LogConfig        `mapstructure:"log"`
	Hardcover        HardcoverConfig  `mapstructure:"hardcover"`
	Notifier         NotifierConfig   `mapstructure:"notifier"`
	Browser          BrowserConfig    `mapstructure:"browser"`
	Prowlarr         ProwlarrConfig   `mapstructure:"prowlarr"`
	TMDB             TMDBConfig       `mapstructure:"tmdb"`
	OMDB             OMDBConfig       `mapstructure:"omdb"`
	Downloader       DownloaderConfig `mapstructure:"downloader"`
	Library          LibraryConfig    `mapstructure:"library"`
	Quality          QualityConfig    `mapstructure:"quality"`
	MediaTypes       MediaTypesConfig `mapstructure:"media_types"`
	Hooks            HooksConfig      `mapstructure:"hooks"`
	ShowTopN         int              `mapstructure:"show_top_n"`
	MinSeeders       int              `mapstructure:"min_seeders"`
	PreferredGroups  []string         `mapstructure:"preferred_release_groups"`
	PosterMode       string           `mapstructure:"poster_mode"`
	ProcessMode      string           `mapstructure:"process_mode"`
	CheckCollections bool             `mapstructure:"check_collections"`
	CacheTTLHours    int              `mapstructure:"cache_ttl_hours"`
	IncludeBacklog   bool             `mapstructure:"include_backlog"`
}

type HooksConfig struct {
	PreDiscover  string `mapstructure:"pre_discover"`
	PostDiscover string `mapstructure:"post_discover"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

type NotifierConfig struct {
	Service              string `mapstructure:"service"`
	URL                  string `mapstructure:"url"`
	Token                string `mapstructure:"token"`
	CustomTemplate       string `mapstructure:"custom_template"`
	SearchCompleteNotify bool   `mapstructure:"search_complete_notify"`
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
	Videos     int `mapstructure:"videos"`
	Music      int `mapstructure:"music"`
	Anime      int `mapstructure:"anime"`
	Ebooks     int `mapstructure:"ebooks"`
	Audiobooks int `mapstructure:"audiobooks"`
}

type TMDBConfig struct {
	APIKey      string `mapstructure:"api_key"`
	AccessToken string `mapstructure:"access_token"`
}

type OMDBConfig struct {
	APIKey string `mapstructure:"api_key"`
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
	MusicTrial string `mapstructure:"music_trial"`
	Anime      string `mapstructure:"anime"`
	Ebooks     string `mapstructure:"ebooks"`
	Audiobooks string `mapstructure:"audiobooks"`
}

type LibraryConfig struct {
	Radarr        RadarrConfig        `mapstructure:"radarr"`
	Sonarr        SonarrConfig        `mapstructure:"sonarr"`
	Lidarr        LidarrConfig        `mapstructure:"lidarr"`
	LazyLibrarian LazyLibrarianConfig `mapstructure:"lazylibrarian"`
	BookBackend   string              `mapstructure:"book_backend"`
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

type LazyLibrarianConfig struct {
	URL     string `mapstructure:"url"`
	APIKey  string `mapstructure:"api_key"`
	Timeout int    `mapstructure:"timeout"`
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
	PhysicalLookbackWeeks int         `mapstructure:"physical_lookback_weeks"`
	Movies                MovieConfig `mapstructure:"movies"`
	TV                    TVConfig    `mapstructure:"tv"`
	Music                 MusicConfig `mapstructure:"music"`
	Anime                 AnimeConfig `mapstructure:"anime"`
	Books                 BookConfig  `mapstructure:"books"`
}

// Mode selects the processing pipeline for this media type:
//   - full          — wmdl searches via Prowlarr, downloads via client, adds to *arr
//   - prowlarr-grab — wmdl searches via Prowlarr, grabs via Prowlarr's configured download client
//   - arr           — wmdl adds to *arr as monitored (with SearchNow), *arr handles search+download
//   - auto          — like arr but auto-yes for all prompts (review TUI still mandatory)
//   - yolo          — fully automatic, no TUI, cron-friendly

type MovieConfig struct {
	Enabled                bool          `mapstructure:"enabled"`
	Mode                   string        `mapstructure:"mode"`
	Scrapers               []string      `mapstructure:"scrapers"`
	StreamingLookbackWeeks int           `mapstructure:"streaming_lookback_weeks"`
	Filter                 ContentFilter `mapstructure:"filter"`
}

type TVConfig struct {
	Enabled                bool          `mapstructure:"enabled"`
	Mode                   string        `mapstructure:"mode"`
	Scrapers               []string      `mapstructure:"scrapers"`
	StreamingLookbackWeeks int           `mapstructure:"streaming_lookback_weeks"`
	Filter                 ContentFilter `mapstructure:"filter"`
}

type ContentFilter struct {
	BlockedLanguages []string `mapstructure:"blocked_languages"`
	BlockedCountries []string `mapstructure:"blocked_countries"`
	BlockedGenres    []string `mapstructure:"blocked_genres"`
	AllowedLanguages []string `mapstructure:"allowed_languages"`
	OverrideGenres   []string `mapstructure:"override_genres"`
}

type MusicConfig struct {
	Enabled       bool              `mapstructure:"enabled"`
	Mode          string            `mapstructure:"mode"`
	Scrapers      []string          `mapstructure:"scrapers"`
	LookbackWeeks int               `mapstructure:"lookback_weeks"`
	Filter        MusicFilterConfig `mapstructure:"filter"`
}

type MusicFilterConfig struct {
	ContentFilter    `mapstructure:",squash"`
	MinCriticScore   int  `mapstructure:"min_critic_score"`
	MinCriticReviews int  `mapstructure:"min_critic_reviews"`
	MinUserScore     int  `mapstructure:"min_user_score"`
	MinUserRatings   int  `mapstructure:"min_user_ratings"`
	IncludeMustHear  bool `mapstructure:"include_must_hear"`
}

type AnimeConfig struct {
	Enabled               bool          `mapstructure:"enabled"`
	Mode                  string        `mapstructure:"mode"`
	LookbackWeeks         int           `mapstructure:"lookback_weeks"`
	MinScore              float64       `mapstructure:"min_score"`
	MinMembers            int           `mapstructure:"min_members"`
	PhaseBEnabled         bool          `mapstructure:"phase_b_enabled"`
	PhaseBMinScore        float64       `mapstructure:"phase_b_min_score"`
	PhaseBMinMembers      int           `mapstructure:"phase_b_min_members"`
	MinPhaseBResults      int           `mapstructure:"min_phase_b_results"`
	FilterFlixPatrolAnime bool          `mapstructure:"filter_flixpatrol_anime"`
	Filter                ContentFilter `mapstructure:"filter"`
}

type BookConfig struct {
	Enabled       bool             `mapstructure:"enabled"`
	Mode          string           `mapstructure:"mode"`
	Scrapers      []string         `mapstructure:"scrapers"`
	LookbackWeeks int              `mapstructure:"lookback_weeks"`
	DefaultFormat string           `mapstructure:"default_format"`
	Filter        BookFilterConfig `mapstructure:"filter"`
}

type BookFilterConfig struct {
	ContentFilter `mapstructure:",squash"`
	MinRating     float64 `mapstructure:"min_rating"`
	MinRatings    int     `mapstructure:"min_ratings"`
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

const (
	ProcessModeFull         = "full"
	ProcessModeArr          = "arr"
	ProcessModeAuto         = "auto"
	ProcessModeYolo         = "yolo"
	ProcessModeProwlarrGrab = "prowlarr-grab"
	ProcessModeBatch        = "batch"
)

var validModes = map[string]bool{
	ProcessModeFull:         true,
	ProcessModeProwlarrGrab: true,
	ProcessModeArr:          true,
	ProcessModeAuto:         true,
	ProcessModeYolo:         true,
	ProcessModeBatch:        true,
}

func (c *Config) UsedModes() map[string]bool {
	modes := make(map[string]bool)
	if c.MediaTypes.Movies.Enabled {
		modes[c.MediaTypes.Movies.Mode] = true
	}
	if c.MediaTypes.TV.Enabled {
		modes[c.MediaTypes.TV.Mode] = true
	}
	if c.MediaTypes.Music.Enabled {
		modes[c.MediaTypes.Music.Mode] = true
	}
	if c.MediaTypes.Anime.Enabled {
		modes[c.MediaTypes.Anime.Mode] = true
	}
	if c.MediaTypes.Books.Enabled {
		modes[c.MediaTypes.Books.Mode] = true
	}
	return modes
}

func (c *Config) MediaTypeMode(mt model.MediaType) string {
	switch mt {
	case model.MediaTypeMovie:
		return c.MediaTypes.Movies.Mode
	case model.MediaTypeTV:
		return c.MediaTypes.TV.Mode
	case model.MediaTypeAnime:
		return c.MediaTypes.Anime.Mode
	case model.MediaTypeMusic:
		return c.MediaTypes.Music.Mode
	case model.MediaTypeBook:
		return c.MediaTypes.Books.Mode
	default:
		return ProcessModeFull
	}
}

func (c *Config) Validate() error {
	var errs []string

	// Validate mode values for all media types
	type mtMode struct {
		label string
		mode  string
	}
	for _, mt := range []mtMode{
		{"media_types.movies.mode", c.MediaTypes.Movies.Mode},
		{"media_types.tv.mode", c.MediaTypes.TV.Mode},
		{"media_types.music.mode", c.MediaTypes.Music.Mode},
		{"media_types.anime.mode", c.MediaTypes.Anime.Mode},
		{"media_types.books.mode", c.MediaTypes.Books.Mode},
	} {
		if mt.mode != "" && !validModes[mt.mode] {
			errs = append(errs, fmt.Sprintf("%s must be one of: %s, %s, %s, %s, %s", mt.label, ProcessModeFull, ProcessModeProwlarrGrab, ProcessModeArr, ProcessModeAuto, ProcessModeYolo))
		}
	}

	if c.TMDB.APIKey == "" {
		errs = append(errs, "tmdb.api_key is required (get one at https://www.themoviedb.org/settings/api)")
	}

	// Conditional service requirements based on active modes
	modes := c.UsedModes()
	needsProwlarr := modes[ProcessModeFull] || modes[ProcessModeProwlarrGrab]
	needsDownloader := modes[ProcessModeFull]

	if needsProwlarr {
		if c.Prowlarr.URL == "" {
			errs = append(errs, "prowlarr.url is required for full/prowlarr-grab modes")
		}
		if c.Prowlarr.APIKey == "" {
			errs = append(errs, "prowlarr.api_key is required for full/prowlarr-grab modes")
		}
	}

	if c.Downloader.Type != "" {
		switch c.Downloader.Type {
		case "qbittorrent", "transmission", "deluge":
		default:
			errs = append(errs, "downloader.type must be one of: qbittorrent, transmission, deluge")
		}
	}

	if needsDownloader {
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
			errs = append(errs, "downloader.type is required for full mode (qbittorrent, transmission, or deluge)")
		default:
			errs = append(errs, fmt.Sprintf("unknown downloader.type %q (must be qbittorrent, transmission, or deluge)", c.Downloader.Type))
		}
	}

	// Mode-aware *arr validation: require config when arr/auto/yolo modes are used
	needsLibMode := func(mode string) bool {
		return mode == ProcessModeArr || mode == ProcessModeAuto || mode == ProcessModeYolo
	}

	if c.MediaTypes.Movies.Enabled && needsLibMode(c.MediaTypes.Movies.Mode) {
		if c.Library.Radarr.URL == "" {
			errs = append(errs, "library.radarr.url is required when media_types.movies.mode is arr/auto/yolo")
		}
		if c.Library.Radarr.URL != "" && c.Library.Radarr.APIKey == "" {
			errs = append(errs, "library.radarr.api_key is required when media_types.movies.mode is arr/auto/yolo")
		}
	}

	if (c.MediaTypes.TV.Enabled || c.MediaTypes.Anime.Enabled) &&
		(needsLibMode(c.MediaTypes.TV.Mode) || needsLibMode(c.MediaTypes.Anime.Mode)) {
		if c.Library.Sonarr.URL == "" {
			errs = append(errs, "library.sonarr.url is required when media_types.tv/anime.mode is arr/auto/yolo")
		}
		if c.Library.Sonarr.URL != "" && c.Library.Sonarr.APIKey == "" {
			errs = append(errs, "library.sonarr.api_key is required when media_types.tv/anime.mode is arr/auto/yolo")
		}
	}

	if c.MediaTypes.Music.Enabled && needsLibMode(c.MediaTypes.Music.Mode) {
		if c.Library.Lidarr.URL == "" {
			errs = append(errs, "library.lidarr.url is required when media_types.music.mode is arr/auto/yolo")
		}
		if c.Library.Lidarr.URL != "" && c.Library.Lidarr.APIKey == "" {
			errs = append(errs, "library.lidarr.api_key is required when media_types.music.mode is arr/auto/yolo")
		}
	}

	// Validate Radarr config if URL is set (partially configured — non-mode-specific)
	if c.Library.Radarr.URL != "" && c.Library.Radarr.APIKey == "" {
		errs = append(errs, "library.radarr.api_key is required when library.radarr.url is set")
	}

	// Validate Sonarr config if URL is set (partially configured — non-mode-specific)
	if c.Library.Sonarr.URL != "" && c.Library.Sonarr.APIKey == "" {
		errs = append(errs, "library.sonarr.api_key is required when library.sonarr.url is set")
	}

	// Validate Lidarr config if URL is set (partially configured — non-mode-specific)
	if c.Library.Lidarr.URL != "" && c.Library.Lidarr.APIKey == "" {
		errs = append(errs, "library.lidarr.api_key is required when library.lidarr.url is set")
	}

	// Validate LazyLibrarian config
	bookBackend := c.Library.BookBackend
	if bookBackend == "" {
		bookBackend = "lazylibrarian"
	}
	if bookBackend == "lazylibrarian" {
		if c.Library.LazyLibrarian.URL != "" && c.Library.LazyLibrarian.APIKey == "" {
			errs = append(errs, "library.lazylibrarian.api_key is required when library.lazylibrarian.url is set")
		}
	} else if bookBackend != "none" && bookBackend != "" {
		errs = append(errs, "library.book_backend must be one of: lazylibrarian, none")
	}

	if c.MediaTypes.Books.Enabled && needsLibMode(c.MediaTypes.Books.Mode) {
		if c.Library.LazyLibrarian.URL == "" {
			errs = append(errs, "library.lazylibrarian.url is required when media_types.books.mode is arr/auto/yolo")
		}
		if c.Library.LazyLibrarian.URL != "" && c.Library.LazyLibrarian.APIKey == "" {
			errs = append(errs, "library.lazylibrarian.api_key is required when media_types.books.mode is arr/auto/yolo")
		}
	}

	// Validate book config
	if c.MediaTypes.Books.Enabled {
		switch c.MediaTypes.Books.DefaultFormat {
		case "ebook", "audiobook", "both":
		default:
			errs = append(errs, "media_types.books.default_format must be one of: ebook, audiobook, both")
		}
		if c.MediaTypes.Books.LookbackWeeks < 0 {
			errs = append(errs, "media_types.books.lookback_weeks must be >= 0")
		}
	}
	if c.MediaTypes.Music.LookbackWeeks < 0 {
		errs = append(errs, "media_types.music.lookback_weeks must be >= 0")
	}
	if c.MediaTypes.Movies.StreamingLookbackWeeks < 0 {
		errs = append(errs, "media_types.movies.streaming_lookback_weeks must be >= 0")
	}
	if c.MediaTypes.TV.StreamingLookbackWeeks < 0 {
		errs = append(errs, "media_types.tv.streaming_lookback_weeks must be >= 0")
	}
	if c.MediaTypes.PhysicalLookbackWeeks < 0 {
		errs = append(errs, "media_types.physical_lookback_weeks must be >= 0")
	}
	if c.MediaTypes.Anime.LookbackWeeks < 0 {
		errs = append(errs, "media_types.anime.lookback_weeks must be >= 0")
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error", "":
	default:
		errs = append(errs, "log.level must be one of: debug, info, warn, error")
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

// setDefaults registers programmatic default values for every config key.
// When adding or changing defaults here, also update config.yaml.example
// so the two stay in sync.
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
	v.SetDefault("include_backlog", false)
	v.SetDefault("log.level", "info")

	v.SetDefault("quality.movies.resolution", "2160p")
	v.SetDefault("quality.movies.prefer_hdr", true)
	v.SetDefault("quality.movies.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.movies.codec_priority", []string{"h265", "h264", "av1"})
	v.SetDefault("quality.tv.resolution", "1080p")
	v.SetDefault("quality.tv.prefer_hdr", false)
	v.SetDefault("quality.tv.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.tv.codec_priority", []string{"h265", "h264", "av1"})
	v.SetDefault("prowlarr.timeout", 120)
	v.SetDefault("prowlarr.indexer_id.videos", 0)
	v.SetDefault("prowlarr.indexer_id.music", 0)
	v.SetDefault("prowlarr.indexer_id.anime", 0)
	v.SetDefault("prowlarr.indexer_id.ebooks", 0)
	v.SetDefault("prowlarr.indexer_id.audiobooks", 0)
	v.SetDefault("library.radarr.monitor", false)
	v.SetDefault("library.radarr.timeout", 300)
	v.SetDefault("library.sonarr.monitor_new_episodes", true)
	v.SetDefault("library.sonarr.season_folders", true)
	v.SetDefault("library.sonarr.timeout", 300)
	v.SetDefault("library.lidarr.timeout", 300)
	v.SetDefault("library.lidarr.monitor_new_albums", true)
	v.SetDefault("library.lidarr.monitor", "all")
	v.SetDefault("library.lazylibrarian.timeout", 120)
	v.SetDefault("library.book_backend", "lazylibrarian")

	v.SetDefault("hooks.pre_discover", "")
	v.SetDefault("hooks.post_discover", "")

	v.SetDefault("media_types.physical_lookback_weeks", 0)
	v.SetDefault("media_types.movies.enabled", true)
	v.SetDefault("media_types.movies.mode", "full")
	v.SetDefault("media_types.movies.scrapers", []string{"dvdsreleasedates", "tmdb-discover", "flixpatrol"})
	v.SetDefault("media_types.movies.streaming_lookback_weeks", 8)
	v.SetDefault("media_types.tv.enabled", true)
	v.SetDefault("media_types.tv.mode", "full")
	v.SetDefault("media_types.tv.scrapers", []string{"dvdsreleasedates", "tmdb-discover", "flixpatrol"})
	v.SetDefault("media_types.tv.streaming_lookback_weeks", 8)
	v.SetDefault("quality.music.format_priority", []string{"flac", "mp3", "aac"})
	v.SetDefault("quality.music.bitrate_priority", []string{"lossless", "320", "v0", "v2"})
	v.SetDefault("media_types.music.enabled", true)
	v.SetDefault("media_types.music.mode", "full")
	v.SetDefault("media_types.music.scrapers", []string{"albumoftheyear", "allmusic"})
	v.SetDefault("media_types.music.lookback_weeks", 1)
	v.SetDefault("media_types.music.filter.min_critic_score", 75)
	v.SetDefault("media_types.music.filter.min_critic_reviews", 5)
	v.SetDefault("media_types.music.filter.min_user_score", 75)
	v.SetDefault("media_types.music.filter.min_user_ratings", 200)
	v.SetDefault("media_types.music.filter.include_must_hear", true)

	v.SetDefault("media_types.anime.enabled", true)
	v.SetDefault("media_types.anime.mode", "full")
	v.SetDefault("media_types.anime.lookback_weeks", 0)
	v.SetDefault("media_types.anime.min_score", 7.0)
	v.SetDefault("media_types.anime.min_members", 50000)
	v.SetDefault("media_types.anime.phase_b_enabled", true)
	v.SetDefault("media_types.anime.phase_b_min_score", 7.5)
	v.SetDefault("media_types.anime.phase_b_min_members", 100000)
	v.SetDefault("media_types.anime.min_phase_b_results", 3)
	v.SetDefault("media_types.anime.filter_flixpatrol_anime", false)

	v.SetDefault("quality.anime.resolution", "1080p")
	v.SetDefault("quality.anime.prefer_hdr", false)
	v.SetDefault("quality.anime.source_priority", []string{"bluray", "web-dl", "webrip"})
	v.SetDefault("quality.anime.codec_priority", []string{"h265", "h264", "av1"})

	v.SetDefault("downloader.categories.anime", "Anime")

	v.SetDefault("downloader.categories.ebooks", "Books")
	v.SetDefault("downloader.categories.audiobooks", "Books")

	v.SetDefault("quality.books.ebooks.format_priority", []string{"epub", "mobi", "azw3", "pdf"})
	v.SetDefault("quality.books.audiobooks.format_priority", []string{"m4b", "mp3", "flac", "aac", "opus"})

	v.SetDefault("media_types.books.enabled", true)
	v.SetDefault("media_types.books.mode", "full")
	// goodreads_blog — readers weekly picks + editors monthly picks, no chromedp needed
	// goodreads       — monthly popular_by_date (includes ratings/shelvings), requires chromedp
	// bookshop        — curated weekly list
	// bookmarks       — LitHub-based, more "highbrow" content
	v.SetDefault("media_types.books.scrapers", []string{"goodreads_blog", "bookshop", "bookmarks"})
	v.SetDefault("media_types.books.lookback_weeks", 1)
	v.SetDefault("media_types.books.default_format", "both")
	v.SetDefault("media_types.books.filter.min_rating", 3.5)
	v.SetDefault("media_types.books.filter.min_ratings", 100)

	v.SetDefault("omdb.api_key", "")
	v.SetDefault("hardcover.api_key", "")
	v.SetDefault("notifier.search_complete_notify", false)
}
