package model

type MediaType string

const (
	MediaTypeMovie MediaType = "movie"
	MediaTypeTV    MediaType = "tv"
	MediaTypeMusic MediaType = "music"
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
	ID        int64
	MBID      string
	Name      string
	LidarrID  int64
	CreatedAt string
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
