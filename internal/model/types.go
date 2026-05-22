package model

type MediaType string

const (
	MediaTypeMovie MediaType = "movie"
	MediaTypeTV    MediaType = "tv"
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
	ID              int64
	TmdbID          int
	TvdbID          int
	Title           string
	Year            int
	MediaType       MediaType
	ImdbID          string
	ImdbRating      float64
	RTURL           string
	RTCriticsScore  float64
	RTAudienceScore float64
	TmdbRating      float64
	MetacriticScore float64
	YoutubeViews    int64
	USRating        string
	Overview        string
	Genres          string
	Runtime         int
	PosterPath      string
	CreatedAt       string
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
	Year       int
	Week       int
	WeekDate   string
	Discovered bool
	Reviewed   bool
	Processed  bool
	UpdatedAt  string
}

type ParsedRelease struct {
	RawTitle     string
	Resolution   int
	HDR          bool
	Source       string
	Codec        string
	ReleaseGroup string
	Seeders      int
	SizeBytes    int64
	PublishedAt  string
	DownloadURL  string
	MagnetURL    string
	InfoHash     string
	Score        int
}
