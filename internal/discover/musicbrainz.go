package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const mbBase = "https://musicbrainz.org/ws/2"

// MBClient handles MusicBrainz API lookups with rate limiting.
type MBClient struct {
	baseURL   string
	client    *http.Client
	lastReq   time.Time
	mu        sync.Mutex
	userAgent string
}

func NewMBClient() *MBClient {
	return &MBClient{
		baseURL:   mbBase,
		client:    &http.Client{Timeout: 10 * time.Second},
		userAgent: "wmdl/0.1.0 (https://github.com/pdfrg/wmdl)",
	}
}

// mbArtistResult represents search results for an artist.
type mbArtistResult struct {
	Artists []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Score   int    `json:"score"`
		Type    string `json:"type,omitempty"`
		Country string `json:"country,omitempty"`
	} `json:"artists"`
	Count int `json:"count"`
}

// mbReleaseGroupResult represents search results for release groups.
type mbReleaseGroupResult struct {
	ReleaseGroups []struct {
		ID               string `json:"id"`
		Title            string `json:"title"`
		Score            int    `json:"score"`
		PrimaryType      string `json:"primary-type,omitempty"`
		FirstReleaseDate string `json:"first-release-date,omitempty"`
		ArtistCredit     []struct {
			Name   string `json:"name"`
			Artist struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"artist"`
		} `json:"artist-credit,omitempty"`
	} `json:"release-groups"`
	Count int `json:"count"`
}

// mbReleaseGroupDetail holds detailed info about a release group.
type mbReleaseGroupDetail struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	PrimaryType       string `json:"primary-type,omitempty"`
	SecondaryTypeList []struct {
		Name string `json:"name"`
	} `json:"secondary-type-list,omitempty"`
	FirstReleaseDate string `json:"first-release-date,omitempty"`
	Rating           *struct {
		Value     float64 `json:"value"`
		VoteCount int     `json:"vote-count"`
	} `json:"rating,omitempty"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags,omitempty"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres,omitempty"`
	ArtistCredit []struct {
		Name   string `json:"name"`
		Artist struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit,omitempty"`
}

// MBArtistResult is the public result from an artist search.
type MBArtistResult struct {
	MBID    string
	Name    string
	Country string
}

// MBReleaseGroupResult is the public result from a release group search.
type MBReleaseGroupResult struct {
	MBID             string
	Title            string
	Score            int // MusicBrainz relevance score (0-100)
	PrimaryType      string
	SecondaryTypes   []string
	FirstReleaseDate string
	ArtistMBID       string
	ArtistName       string
	Rating           float64 // MusicBrainz rating (1-5 scale)
	Genres           []string
	Tags             []string
}

// MBArtistDetail holds detailed info about an artist fetched by MBID.
type MBArtistDetail struct {
	MBID           string
	Name           string
	Type           string // "Person" or "Group"
	Country        string // ISO code (e.g. "US", "AU")
	Area           string
	BeginArea      string // birthplace or origin area name
	BeginDate      string
	EndDate        string
	Disambiguation string
	Tags           []string
	Genres         []string
	Rating         float64
	RatingVotes    int
	WikidataURL    string
}

// SearchArtist searches MusicBrainz for an artist by name.
// Returns the best-matching result.
func (c *MBClient) SearchArtist(ctx context.Context, artistName string) (*MBArtistResult, error) {
	c.rateLimit()

	query := url.QueryEscape(fmt.Sprintf(`artist:"%s"`, sanitizeLucene(artistName)))
	u := fmt.Sprintf("%s/artist/?query=%s&fmt=json&limit=5", c.baseURL, query)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb artist search: %w", err)
	}
	defer resp.Body.Close()

	var result mbArtistResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding mb response: %w", err)
	}

	if len(result.Artists) == 0 {
		return nil, nil
	}

	// Return the highest-scored result
	best := &result.Artists[0]
	return &MBArtistResult{
		MBID:    best.ID,
		Name:    best.Name,
		Country: best.Country,
	}, nil
}

// GetArtist fetches detailed artist info by MBID (not a search — direct lookup).
func (c *MBClient) GetArtist(ctx context.Context, mbid string) (*MBArtistDetail, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/artist/%s?inc=tags+genres+ratings+annotation+url-rels+aliases&fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb artist detail: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Country        string `json:"country"`
		Disambiguation string `json:"disambiguation"`
		Area           *struct {
			Name string `json:"name"`
		} `json:"area,omitempty"`
		BeginArea *struct {
			Name string `json:"name"`
		} `json:"begin-area,omitempty"`
		LifeSpan *struct {
			Begin string `json:"begin"`
			End   string `json:"end"`
		} `json:"life-span,omitempty"`
		Tags []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"tags,omitempty"`
		Genres []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"genres,omitempty"`
		Rating *struct {
			Value     float64 `json:"value"`
			VoteCount int     `json:"votes-count"`
		} `json:"rating,omitempty"`
		Relations []struct {
			Type string `json:"type"`
			URL  *struct {
				Resource string `json:"resource"`
			} `json:"url,omitempty"`
		} `json:"relations,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding mb artist detail: %w", err)
	}

	det := &MBArtistDetail{
		MBID:           raw.ID,
		Name:           raw.Name,
		Type:           raw.Type,
		Country:        raw.Country,
		Disambiguation: raw.Disambiguation,
	}
	if raw.Area != nil {
		det.Area = raw.Area.Name
	}
	if raw.BeginArea != nil {
		det.BeginArea = raw.BeginArea.Name
	}
	if raw.LifeSpan != nil {
		det.BeginDate = raw.LifeSpan.Begin
		det.EndDate = raw.LifeSpan.End
	}
	for _, g := range raw.Genres {
		det.Genres = append(det.Genres, g.Name)
	}
	for _, t := range raw.Tags {
		det.Tags = append(det.Tags, t.Name)
	}
	if raw.Rating != nil {
		det.Rating = raw.Rating.Value
		det.RatingVotes = raw.Rating.VoteCount
	}
	for _, r := range raw.Relations {
		if r.URL != nil && r.Type == "wikidata" {
			det.WikidataURL = r.URL.Resource
			break
		}
	}

	return det, nil
}

// SearchReleaseGroup searches for a release group by album title and artist.
// Tries multiple query strategies, gathers candidate results, and returns the
// best-ranked match. year and albumType are optional hints (0/"" when unknown)
// used to prefer the correct release among same-title variants.
//
// Query generation is layered:
//   - primary: the raw title/artist, plus punctuation-cleaned title variants
//     (parenthetical/bracket suffixes and trailing dates stripped, slashes
//     spaced out) and the artist name before a conjunction (e.g. "&");
//   - fallback: only reached when the primary pass finds no plausible match.
//     Adds artist variants (leading "The" dropped, internal spaces joined for
//     word-squashed MB names like "Trashcan Sinatras", the name after a
//     conjunction like "GA-20"), and for titles shaped "Composer: Work" also
//     searches the work title alone under both the performer and the composer.
func (c *MBClient) SearchReleaseGroup(ctx context.Context, albumTitle, artistName string, year int, albumType string) (*MBReleaseGroupResult, error) {
	cleanTitle := cleanQueryTitle(albumTitle)
	firstArtist := firstArtistName(artistName)

	var titles []string
	addTitle := func(t string) {
		if t != "" && !slices.Contains(titles, t) {
			titles = append(titles, t)
		}
	}
	addTitle(albumTitle)
	addTitle(cleanTitle)
	if spaced := spaceSlashes(cleanTitle); spaced != cleanTitle {
		addTitle(spaced)
	}

	var artists []string
	addArtist := func(a string) {
		if a != "" && !slices.Contains(artists, a) {
			artists = append(artists, a)
		}
	}
	addArtist(artistName)
	addArtist(firstArtist)

	// Fallback title/artist variants, tried only if the primary pass misses.
	var fbTitles []string
	var fbArtists []string
	if composer, work, ok := composerWork(cleanTitle); ok {
		if !slices.Contains(titles, work) {
			fbTitles = append(fbTitles, work)
		}
		if !slices.Contains(titles, composer) {
			fbTitles = append(fbTitles, composer)
		}
		if !slices.Contains(artists, composer) {
			fbArtists = append(fbArtists, composer)
		}
	}
	for _, v := range artistQueryVariants(artistName) {
		if !slices.Contains(artists, v) && !slices.Contains(fbArtists, v) {
			fbArtists = append(fbArtists, v)
		}
	}

	candidates := c.searchReleaseGroups(ctx, releaseGroupQueries, titles, artists, fbTitles, fbArtists, albumTitle, artistName, year, albumType)
	if best := c.pickBestReleaseGroup(candidates, albumTitle, artistName, year, albumType); best != nil {
		return best, nil
	}

	// Fall back to the release field, which can surface release-group titles
	// that index slightly differently than their releases.
	candidates = c.searchReleaseGroups(ctx, releaseFieldQueries, titles, artists, fbTitles, fbArtists, albumTitle, artistName, year, albumType)
	return c.pickBestReleaseGroup(candidates, albumTitle, artistName, year, albumType), nil
}

type queryBuilder func(title, artist string) []string

// searchReleaseGroups builds the ordered query list (primary pairs first,
// fallback variants after) and accumulates candidates until a plausible match
// is found, so the common case makes a single rate-limited API call.
func (c *MBClient) searchReleaseGroups(ctx context.Context, build queryBuilder, titles, artists, fbTitles, fbArtists []string, albumTitle, artistName string, year int, albumType string) []*MBReleaseGroupResult {
	var queries []string
	for _, t := range titles {
		for _, a := range artists {
			queries = append(queries, build(t, a)...)
		}
	}
	for _, t := range fbTitles {
		for _, a := range artists {
			queries = append(queries, build(t, a)...)
		}
	}
	for _, t := range fbTitles {
		for _, a := range fbArtists {
			queries = append(queries, build(t, a)...)
		}
	}
	for _, t := range titles {
		for _, a := range fbArtists {
			queries = append(queries, build(t, a)...)
		}
	}
	return c.searchAllCandidates(ctx, queries, albumTitle, artistName, year, albumType)
}

// SearchReleaseGroupByAlbum searches for a release group by album title only
// (no artist constraint). Used as a blind check when the artist-specific search
// finds no match — if the album exists under a different artist, the scrape
// likely associated the wrong artist name. The caller must still verify the
// artist name of the returned result.
func (c *MBClient) SearchReleaseGroupByAlbum(ctx context.Context, albumTitle string, year int) (*MBReleaseGroupResult, error) {
	cleanTitle := stripTitleParens(albumTitle)

	var queries []string
	queries = append(queries, fmt.Sprintf(`releasegroup:"%s"`, sanitizeLucene(albumTitle)))
	if cleanTitle != albumTitle {
		queries = append(queries, fmt.Sprintf(`releasegroup:"%s"`, sanitizeLucene(cleanTitle)))
	}

	candidates := c.searchAllCandidates(ctx, queries, albumTitle, "", year, "")
	if best := c.pickBestReleaseGroup(candidates, albumTitle, "", year, ""); best != nil {
		return best, nil
	}

	var relQueries []string
	relQueries = append(relQueries, fmt.Sprintf(`release:"%s"`, sanitizeLucene(albumTitle)))
	if cleanTitle != albumTitle {
		relQueries = append(relQueries, fmt.Sprintf(`release:"%s"`, sanitizeLucene(cleanTitle)))
	}
	candidates = c.searchAllCandidates(ctx, relQueries, albumTitle, "", year, "")
	return c.pickBestReleaseGroup(candidates, albumTitle, "", year, ""), nil
}

func releaseGroupQueries(albumTitle, artistName string) []string {
	return []string{fmt.Sprintf(`releasegroup:"%s" AND artist:"%s"`, sanitizeLucene(albumTitle), sanitizeLucene(artistName))}
}

func releaseFieldQueries(albumTitle, artistName string) []string {
	return []string{fmt.Sprintf(`release:"%s" AND artist:"%s"`, sanitizeLucene(albumTitle), sanitizeLucene(artistName))}
}

// searchAllCandidates runs query strategies in order, accumulating candidates.
// It stops early once a query yields a candidate that scores as a plausible
// match, so the common case (a specific query hitting on the first try) makes
// a single rate-limited API call.
func (c *MBClient) searchAllCandidates(ctx context.Context, queries []string, albumTitle, artistName string, year int, albumType string) []*MBReleaseGroupResult {
	var candidates []*MBReleaseGroupResult
	for _, q := range queries {
		results, err := c.searchReleaseGroupsOnce(ctx, q)
		if err != nil {
			// Network/API error — try next fallback
			continue
		}
		candidates = append(candidates, results...)
		if c.pickBestReleaseGroup(candidates, albumTitle, artistName, year, albumType) != nil {
			break
		}
	}
	return candidates
}

func (c *MBClient) searchReleaseGroupsOnce(ctx context.Context, query string) ([]*MBReleaseGroupResult, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/release-group/?query=%s&fmt=json&limit=10", c.baseURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb release group search: %w", err)
	}
	defer resp.Body.Close()

	var result mbReleaseGroupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding mb response: %w", err)
	}

	results := make([]*MBReleaseGroupResult, 0, len(result.ReleaseGroups))
	for _, rg := range result.ReleaseGroups {
		var artistMBID, artistCreditName string
		if len(rg.ArtistCredit) > 0 {
			artistCreditName = rg.ArtistCredit[0].Name
			artistMBID = rg.ArtistCredit[0].Artist.ID
		}
		results = append(results, &MBReleaseGroupResult{
			MBID:             rg.ID,
			Title:            rg.Title,
			Score:            rg.Score,
			PrimaryType:      rg.PrimaryType,
			FirstReleaseDate: rg.FirstReleaseDate,
			ArtistMBID:       artistMBID,
			ArtistName:       artistCreditName,
		})
	}
	return results, nil
}

// pickBestReleaseGroup scores candidate release groups and returns the best
// match for the scraped album (or nil if none plausibly matches). Ranking
// weighs exact title and artist equality, primary-type preference, year
// closeness, and finally MusicBrainz's own relevance score.
func (c *MBClient) pickBestReleaseGroup(candidates []*MBReleaseGroupResult, albumTitle, artistName string, year int, albumType string) *MBReleaseGroupResult {
	uniq := make(map[string]*MBReleaseGroupResult)
	for _, cand := range candidates {
		if cand == nil || cand.MBID == "" {
			continue
		}
		prev, ok := uniq[cand.MBID]
		if !ok || cand.Score > prev.Score {
			uniq[cand.MBID] = cand
		}
	}

	var winner *MBReleaseGroupResult
	bestScore := -1
	for _, cand := range uniq {
		if s := scoreReleaseGroupCandidate(cand, albumTitle, artistName, year, albumType); s > bestScore {
			bestScore = s
			winner = cand
		}
	}
	return winner
}

func scoreReleaseGroupCandidate(cand *MBReleaseGroupResult, albumTitle, artistName string, year int, albumType string) int {
	if cand.Title == "" || albumTitle == "" {
		return -1
	}
	score := 0

	nt := normalizeTitleForCompare(cand.Title)
	titleOK := false
	for _, t := range titleCompareForms(albumTitle) {
		if t == "" {
			continue
		}
		if nt == t {
			titleOK = true
			score += 60
			break
		}
		if strings.HasPrefix(nt, t) || strings.HasPrefix(t, nt) {
			titleOK = true
			score += 30
			break
		}
	}

	artistOK := false
	if artistName != "" {
		if artistNamesMatchVariants(artistName, cand.ArtistName) {
			artistOK = true
			score += 50
		} else if composer, _, ok := composerWork(cleanQueryTitle(albumTitle)); ok && composer != "" && artistNamesMatchVariants(composer, cand.ArtistName) {
			// Classical releases are credited to the composer, not the performer.
			artistOK = true
			score += 50
		}
	} else {
		artistOK = true
	}

	if !titleOK || !artistOK {
		// Classical releases sometimes re-title the work on the release group
		// (e.g. "Archipel (Debussy: La Mer - Ireland: Sarnia)" vs scraped
		// "Archipel: Claude Debussy - La Mer; John Ireland - Sarnia"). When the
		// artist (performer or composer), year, and type all align and the
		// titles share meaningful tokens, accept as a weak match.
		if artistOK && yearCompatible(cand.FirstReleaseDate, year) && typeCompatible(cand.PrimaryType, albumType) &&
			titleTokenOverlap(nt, titleCompareForms(albumTitle)) >= 2 {
			return 40 + cand.Score/10
		}
		return -1
	}

	if want := canonicalAlbumType(albumType); want != "" {
		if canonicalAlbumType(cand.PrimaryType) == want {
			score += 30
		}
	} else {
		// No type hint from the scrape; prefer full-length/EP over
		// singles/compilations/mixtapes since the scrapers never emit those.
		switch canonicalAlbumType(cand.PrimaryType) {
		case "album", "ep":
			score += 20
		case "single", "compilation", "mixtape":
			score += 5
		}
	}

	if year > 0 {
		if cy := releaseYear(cand.FirstReleaseDate); cy == year {
			score += 25
		} else if cy > 0 {
			diff := cy - year
			if diff < 0 {
				diff = -diff
			}
			if diff == 1 {
				score += 10
			}
		}
	}

	score += cand.Score / 10
	return score
}

// canonicalAlbumType maps both MusicBrainz primary types and wmdl AlbumType
// values onto a common set for comparison ("LP" and "Album" are equivalent).
func canonicalAlbumType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "album", "lp", "full-length":
		return "album"
	case "ep":
		return "ep"
	case "single":
		return "single"
	case "live":
		return "live"
	case "soundtrack":
		return "soundtrack"
	case "compilation":
		return "compilation"
	case "mixtape":
		return "mixtape"
	case "remix":
		return "remix"
	case "reissue":
		return "reissue"
	case "box set":
		return "box set"
	case "dj mix":
		return "dj mix"
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

// releaseYear extracts the 4-digit year from a MusicBrainz first-release-date.
func releaseYear(date string) int {
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, date); err == nil {
			return t.Year()
		}
	}
	return 0
}

// stripTitleParens removes trailing parenthetical groups like "(The Piano Versions)".
func stripTitleParens(title string) string {
	re := regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	return strings.TrimSpace(re.ReplaceAllString(title, ""))
}

// firstArtistName returns the artist name before the first conjunction separator.
// "Jeff Parker & ETA IVtet" → "Jeff Parker"
func firstArtistName(name string) string {
	re := regexp.MustCompile(`\s*(&|feat\.|ft\.|with|vs\.|\+|/)\s*.*$`)
	return strings.TrimSpace(re.ReplaceAllString(name, ""))
}

// ── Title cleaning / variants ──

var (
	trailingBracketsRe = regexp.MustCompile(`(?:\s*\[[^\]]*\]\s*)+$`)
	trailingDateRe     = regexp.MustCompile(`[\s,()]*\d{1,2}/\d{1,2}/\d{2,4}[\s)]*$`)
	slashSpacingRe     = regexp.MustCompile(`\s*/\s*`)
	composerWorkRe     = regexp.MustCompile(`^([^:]{1,80}):\s*(.+)$`)
)

// cleanQueryTitle normalizes a scraped title for MusicBrainz queries: it strips
// trailing parenthetical groups, bracketed edition markers like
// "[30th Anniversary] [Expanded Edition]" / "[Blue]", and trailing dates like
// "(7/3/66)" or ", 7/3/66" that scrapers append.
func cleanQueryTitle(title string) string {
	t := stripTitleParens(title)
	t = trailingBracketsRe.ReplaceAllString(t, "")
	t = trailingDateRe.ReplaceAllString(t, "")
	return strings.TrimSpace(t)
}

// spaceSlashes pads "/" with spaces ("Same Sun/Same Sky" → "Same Sun / Same Sky"),
// matching how MusicBrainz indexes titles that use a spaced slash.
func spaceSlashes(title string) string {
	return slashSpacingRe.ReplaceAllString(title, " / ")
}

// normalizeTitleForCompare prepares a title for scoring: normalizes text and
// treats ";" and "/" (with any spacing) as equivalent separators, so a scraped
// "Holst: The Planets; Bax: Tintagel" compares equal to MB's
// "Holst: The Planets / Bax: Tintagel".
func normalizeTitleForCompare(s string) string {
	s = strings.ReplaceAll(s, ";", "/")
	return slashSpacingRe.ReplaceAllString(normalizeText(s), "/")
}

// titleCompareForms returns the normalized title forms a candidate may match:
// the cleaned full title and, for "Composer: Work" titles, the work alone.
func titleCompareForms(title string) []string {
	t := cleanQueryTitle(title)
	forms := []string{normalizeTitleForCompare(t)}
	if _, work, ok := composerWork(t); ok && work != "" {
		forms = append(forms, normalizeTitleForCompare(work))
	}
	return forms
}

// composerWork splits a "Composer: Work" title into its leading name and the
// work portion, e.g. "Steve Reich: The Sextets" → ("Steve Reich", "The Sextets").
// It drives the fallback to composer-credited release groups that MusicBrainz
// uses for classical releases.
func composerWork(title string) (composer, work string, ok bool) {
	m := composerWorkRe.FindStringSubmatch(strings.TrimSpace(title))
	if len(m) != 3 {
		return "", "", false
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), true
}

// ── Artist name variants ──

var (
	leadingTheRe       = regexp.MustCompile(`^The\s+`)
	afterConjunctionRe = regexp.MustCompile(`^\s*.*?\s*(?:&|feat\.|ft\.|with|vs\.|\+|/)\s*(.+?)\s*$`)
)

// artistQueryVariants returns deterministic variants of a scraped artist name
// to probe in MusicBrainz queries and match verification:
//   - the raw name and the name before a conjunction (existing behavior);
//   - the name after a conjunction, e.g. "GA-20" from "Charlie Musselwhite & GA-20";
//   - the name with a leading "The" dropped, covering bands indexed without it;
//   - the last token (surname/distinctive word), which resolves word-joined
//     MusicBrainz names like "Trashcan Sinatras" from "The Trash Can Sinatras".
//
// Variants are only ever probed as fallbacks and must still pass strict title,
// type, and year scoring, so false positives are unlikely.
func artistQueryVariants(name string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(name)
	add(firstArtistName(name))
	if m := afterConjunctionRe.FindStringSubmatch(name); len(m) == 2 {
		add(strings.TrimSpace(m[1]))
	}
	if noThe := strings.TrimSpace(leadingTheRe.ReplaceAllString(name, "")); noThe != strings.TrimSpace(name) {
		add(noThe)
	}
	// Only probe the last token for multi-word names (3+ words), where it is a
	// distinctive word; short names like "Blue Dogs" would probe too loosely.
	if fields := strings.Fields(name); len(fields) >= 3 {
		add(fields[len(fields)-1])
	}
	return out
}

// namesEquivalent reports whether two artist names are the same after text
// normalization or after removing all whitespace (bridging word-joined MB
// names such as "Trashcan Sinatras" with spaced scraped names).
func namesEquivalent(a, b string) bool {
	if artistNamesMatch(a, b) {
		return true
	}
	squash := func(s string) string { return strings.Join(strings.Fields(normalizeText(s)), "") }
	return squash(a) == squash(b)
}

// artistNamesMatchVariants reports whether the MusicBrainz credit name matches
// the scraped artist name or any of its known variants.
func artistNamesMatchVariants(scraped, credit string) bool {
	if namesEquivalent(scraped, credit) {
		return true
	}
	for _, v := range artistQueryVariants(scraped) {
		if namesEquivalent(v, credit) {
			return true
		}
	}
	return false
}

// releaseGroupArtistMatches verifies that a candidate release group's artist
// credit corresponds to the scraped artist(s) or, for classical "Composer: Work"
// titles, to the composer credited in MusicBrainz.
func releaseGroupArtistMatches(credit string, artists []string, albumTitle string) bool {
	for _, a := range artists {
		if artistNamesMatchVariants(a, credit) {
			return true
		}
	}
	if composer, _, ok := composerWork(cleanQueryTitle(albumTitle)); ok {
		if artistNamesMatchVariants(composer, credit) {
			return true
		}
	}
	return false
}

// ── Weak classical match support ──

// yearCompatible reports whether the release group's first-release year is
// within one year of the scraped year (unknown years are always compatible).
func yearCompatible(date string, year int) bool {
	if year <= 0 {
		return true
	}
	cy := releaseYear(date)
	if cy == 0 {
		return true
	}
	d := cy - year
	if d < 0 {
		d = -d
	}
	return d <= 1
}

// typeCompatible reports whether the candidate's primary type is compatible
// with the scraped type hint (or a plausible album/ep/single when no hint).
func typeCompatible(candType, albumType string) bool {
	want := canonicalAlbumType(albumType)
	if want == "" {
		switch canonicalAlbumType(candType) {
		case "album", "ep", "single":
			return true
		default:
			return false
		}
	}
	return canonicalAlbumType(candType) == want
}

// titleTokenOverlap returns the largest count of meaningful shared tokens
// between the candidate title and any scraped title form.
func titleTokenOverlap(candTitle string, forms []string) int {
	setC := tokenSet(candTitle)
	best := 0
	for _, f := range forms {
		setF := tokenSet(f)
		n := 0
		for t := range setC {
			if setF[t] {
				n++
			}
		}
		if n > best {
			best = n
		}
	}
	return best
}

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range tokenRe.FindAllString(s, -1) {
		if len(t) > 2 {
			set[t] = true
		}
	}
	return set
}

// sanitizeLucene removes double quotes that would otherwise break a Lucene
// phrase query (they terminate the phrase and let unrelated results through).
func sanitizeLucene(s string) string {
	return strings.ReplaceAll(s, `"`, "'")
}

// normalizeText normalizes text for fuzzy comparisons: lowercases, folds smart
// quotes/apostrophes, maps every Unicode dash variant to ASCII '-', applies
// NFKD (dropping combining marks), and collapses whitespace. NFKD alone does
// not fold characters like U+2010 HYPHEN into U+002D, which is why MusicBrainz
// names such as "The All‐American Rejects" otherwise fail to match scraped
// names that use a plain hyphen.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(
		"\u2018", "'", "\u2019", "'", "\u02bc", "'", // curly/final quotes
		"\u2010", "-", // HYPHEN
		"\u2011", "-", // NON-BREAKING HYPHEN
		"\u2012", "-", // FIGURE DASH
		"\u2013", "-", // EN DASH
		"\u2014", "-", // EM DASH
		"\u2015", "-", // HORIZONTAL BAR
		"\u2043", "-", // HYPHEN BULLET
		"\u2212", "-", // MINUS SIGN
		"\ufe63", "-", // SMALL HYPHEN-MINUS
		"\uff0d", "-", // FULLWIDTH HYPHEN-MINUS
	).Replace(s)
	t := norm.NFKD.String(s)
	var out strings.Builder
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// GetReleaseGroupDetail fetches detailed info about a release group including
// rating, tags, and genres.
func (c *MBClient) GetReleaseGroupDetail(ctx context.Context, mbid string) (*MBReleaseGroupResult, error) {
	c.rateLimit()

	u := fmt.Sprintf("%s/release-group/%s?inc=tags+genres+ratings+artist-credits&fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mb release group detail: %w", err)
	}
	defer resp.Body.Close()

	var detail mbReleaseGroupDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decoding mb detail: %w", err)
	}

	var artistMBID, artistName string
	if len(detail.ArtistCredit) > 0 {
		artistName = detail.ArtistCredit[0].Name
		artistMBID = detail.ArtistCredit[0].Artist.ID
	}

	var rating float64
	if detail.Rating != nil {
		rating = detail.Rating.Value
	}

	result := &MBReleaseGroupResult{
		MBID:             detail.ID,
		Title:            detail.Title,
		PrimaryType:      detail.PrimaryType,
		FirstReleaseDate: detail.FirstReleaseDate,
		ArtistMBID:       artistMBID,
		ArtistName:       artistName,
		Rating:           rating,
	}
	for _, st := range detail.SecondaryTypeList {
		result.SecondaryTypes = append(result.SecondaryTypes, st.Name)
	}
	for _, g := range detail.Genres {
		result.Genres = append(result.Genres, g.Name)
	}
	for _, t := range detail.Tags {
		result.Tags = append(result.Tags, t.Name)
	}
	return result, nil
}

// rateLimit ensures at most 1 request per second to MusicBrainz.
func (c *MBClient) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastReq)
	if elapsed < time.Second {
		time.Sleep(time.Second - elapsed)
	}
	c.lastReq = time.Now()
}
