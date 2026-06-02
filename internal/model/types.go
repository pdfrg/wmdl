package model

type MediaType string

const (
	MediaTypeMovie MediaType = "movie"
	MediaTypeTV    MediaType = "tv"
	MediaTypeMusic MediaType = "music"
	MediaTypeAnime MediaType = "anime"
)

type ReleaseStatus string

const (
	StatusPending    ReleaseStatus = "pending"
	StatusApproved   ReleaseStatus = "approved"
	StatusRejected   ReleaseStatus = "rejected"
	StatusDownloaded ReleaseStatus = "downloaded"
)

type ReleaseType string

const (
	ReleasePhysical  ReleaseType = "physical"
	ReleaseStreaming ReleaseType = "streaming"
)

type DownloadStatus string

const (
	DownloadAdded       DownloadStatus = "added"
	DownloadDownloading DownloadStatus = "downloading"
	DownloadComplete    DownloadStatus = "complete"
	DownloadUpgraded    DownloadStatus = "upgraded"
)

type Title struct {
	ID               int64
	TmdbID           int
	TvdbID           int
	MalID            int // MyAnimeList ID (anime)
	Title            string
	TmdbTitle        string // TMDB-matched title (may differ from scraped Title)
	Year             int
	MediaType        MediaType
	ImdbID           string
	ImdbRating       float64
	RTURL            string
	RTCriticsScore   float64
	RTAudienceScore  float64
	TmdbRating       float64
	MetacriticScore  float64
	YoutubeViews     int64
	USRating         string
	OriginalLanguage string
	OriginCountry    string
	Overview         string
	Genres           string
	Runtime          int
	PosterPath       string
	CreatedAt        string

	// Anime-specific fields (populated from Jikan)
	AnimeType     string // TV, Movie, OVA, ONA, Special, Music
	AnimeEpisodes int
	AnimeStatus   string // Finished Airing, Currently Airing, Not yet aired
	AnimeMembers  int
	AnimeRank     int
	AnimeSource   string // Original, Manga, Light novel, etc.
	AnimeStudio   string // primary studio name
	Themes        string // comma-separated theme tags
	Demographics  string // comma-separated demographic tags
	Streaming     string // comma-separated streaming service names
}

type ReleaseEvent struct {
	ID             int64
	TitleID        int64
	Source         string
	ReleaseType    ReleaseType
	ReleaseDate    string
	Status         ReleaseStatus
	PreviousStatus ReleaseStatus
	Notes          string
	CreatedAt      string
	ISOYear        int
	ISOWeek        int
}

type Download struct {
	ID              int64
	TitleID         int64
	ReleaseEventID  int64
	Quality         string
	SourceType      string
	Codec           string
	InfoHash        string
	Category        string
	Status          DownloadStatus
	ClientTorrentID string
	RadarrID        int64
	SonarrID        int64
	CreatedAt       string
}

type WeekState struct {
	Year            int
	Week            int
	WeekDate        string
	Discovered      bool
	Reviewed        bool
	Processed       bool
	UpdatedAt       string
	ApprovedCount   int // total approved + downloaded events
	DownloadedCount int // downloaded events only
}

type AlbumType string

const (
	AlbumTypeLP          AlbumType = "lp"
	AlbumTypeEP          AlbumType = "ep"
	AlbumTypeSoundtrack  AlbumType = "soundtrack"
	AlbumTypeLive        AlbumType = "live"
	AlbumTypeRemix       AlbumType = "remix"
	AlbumTypeBoxSet      AlbumType = "box set"
	AlbumTypeMixtape     AlbumType = "mixtape"
	AlbumTypeCompilation AlbumType = "compilation"
	AlbumTypeReissue     AlbumType = "reissue"
	AlbumTypeSingle      AlbumType = "single"
)

type Artist struct {
	ID             int64
	MBID           string
	Name           string
	LidarrID       int64
	Country        string // ISO country code (e.g. "US", "AU")
	ArtistType     string // "Person" or "Group"
	BeginDate      string // birth/formation date (ISO 8601)
	EndDate        string // death/dissolution date
	BeginArea      string // birthplace or origin area name
	Area           string // area name (e.g. "United States")
	Disambiguation string
	Tags           string // comma-separated MB tags
	Genres         string // comma-separated MB genre tags
	MBRating       float64
	CreatedAt      string
}

type Album struct {
	ID        int64
	ArtistID  int64
	Title     string
	Year      int
	MBID      string // MusicBrainz release group ID
	AlbumType AlbumType

	AOTYCriticScore float64
	AOTYCriticCount int
	AOTYUserScore   float64
	AOTYUserCount   int
	AOTYMustHear    bool
	AOTYURL         string
	AllMusicRating  float64 // AllMusic editor rating (1-10)
	AllMusicURL     string  // allmusic.com/album/... page

	MBRating float64

	ReleaseDate string
	Genres      string
	Overview    string
	PosterPath  string
	CreatedAt   string
}

type AlbumReleaseEvent struct {
	ID             int64
	AlbumID        int64
	Source         string // "albumoftheyear"
	ReleaseDate    string
	Status         ReleaseStatus
	PreviousStatus ReleaseStatus
	Notes          string
	CreatedAt      string
	ISOYear        int
	ISOWeek        int
}
