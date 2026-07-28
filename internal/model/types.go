package model

type MediaType string

const (
	MediaTypeMovie MediaType = "movie"
	MediaTypeTV    MediaType = "tv"
	MediaTypeMusic MediaType = "music"
	MediaTypeAnime MediaType = "anime"
	MediaTypeBook  MediaType = "book"
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
	ID                  int64
	TmdbID              int
	TvdbID              int
	MalID               int // MyAnimeList ID (anime)
	Title               string
	TmdbTitle           string // TMDB-matched title (may differ from scraped Title)
	Year                int
	MediaType           MediaType
	ImdbID              string
	ImdbRating          float64
	ImdbVotes           int64
	Awards              string
	BoxOffice           string
	Director            string
	Writer              string
	Actors              string
	RTURL               string
	RTCriticsScore      float64
	RTAudienceScore     float64
	RTAudienceRealScore float64
	RTRealVotes         int
	TmdbRating          float64
	MetacriticScore     float64
	YoutubeViews        int64
	USRating            string
	OriginalLanguage    string
	OriginCountry       string
	Overview            string
	Genres              string
	Runtime             int
	PosterPath          string
	CreatedAt           string

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

	// Collection membership (populated from TMDB during discovery)
	CollectionID   int    `json:"collection_id"`
	CollectionName string `json:"collection_name"`
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

type AlbumRelease struct {
	ID         int64
	ArtistName string // from scraper, always
	Title      string // from scraper, always
	Year       int
	MBID       string // MusicBrainz release group ID
	ArtistMBID string // MusicBrainz artist ID
	AlbumType  AlbumType

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
	ReleaseID      int64
	Source         string // "albumoftheyear"
	ReleaseDate    string
	Status         ReleaseStatus
	PreviousStatus ReleaseStatus
	Notes          string
	CreatedAt      string
	ISOYear        int
	ISOWeek        int
}

type BookFormat string

const (
	BookFormatEbook     BookFormat = "ebook"
	BookFormatAudiobook BookFormat = "audiobook"
	BookFormatBoth      BookFormat = "both"
)

type Author struct {
	ID          int64
	HardcoverID int
	OLID        string // Open Library author ID
	Name        string
	Bio         string
	BornDate    string
	DeathDate   string
	ImageURL    string
	Identifiers string // JSON: VIAF, ISNI, etc.
	Links       string // JSON: external links (Wikipedia, Goodreads, etc.)
	CreatedAt   string
}

type Book struct {
	ID             int64
	AuthorID       int64
	Title          string
	Subtitle       string
	HardcoverID    int
	HardcoverSlug  string
	OLID           string // Open Library work ID
	ISBN10         string
	ISBN13         string
	ASIN           string
	Pages          int
	AudioSeconds   int
	Description    string
	ReleaseDate    string
	ReleaseYear    int
	Rating         float64
	RatingsCount   int
	ShelvingsCount int
	ImageURL       string
	Language       string
	Publisher      string
	Tags           string // comma-separated
	LiteraryType   string // fiction / nonfiction
	SeriesID       string // Hardcover series ID or slug
	SeriesName     string
	CreatedAt      string
}

type BookReleaseEvent struct {
	ID                 int64
	BookID             int64
	Source             string // "goodreads"
	ReleaseDate        string
	FormatPref         BookFormat // ebook, audiobook, both (from config default)
	Status             ReleaseStatus
	PreviousStatus     ReleaseStatus
	Notes              string
	CreatedAt          string
	ISOYear            int
	ISOWeek            int
	EbookProcessed     bool
	AudiobookProcessed bool
}

type BookDownload struct {
	ID               int64
	BookID           int64
	BookReleaseEvent int64
	Format           BookFormat
	Quality          string
	SourceType       string
	Codec            string
	InfoHash         string
	Category         string
	Status           DownloadStatus
	ClientTorrentID  string
	CreatedAt        string
}

type AuthorResult struct {
	AuthorID string
	Name     string
}

type BookResult struct {
	BookID string
	Title  string
}

type BookStatus struct {
	BookID      string
	Title       string
	Status      string // Skipped, Wanted, Have, Open, Ignored, Snatched, Failed
	AudioStatus string
	BookFile    string
	AudioFile   string
	Isbn        string
}

type SeriesMember struct {
	Position   int
	Title      string
	AuthorName string
	AuthorID   string
	BookID     string
	PubDate    string // release date from metadata source, e.g. "2026-08-15" or "2026"
}
