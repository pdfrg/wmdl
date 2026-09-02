package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

const rpChartsEndpoint = "https://pdfrg.github.io/rpcharts-site/data/charts.json"

// rpChartsStationSlugAll selects the aggregate "All Stations" window.
const rpChartsStationSlugAll = "all"

type rpChartsPayload struct {
	Version     int               `json:"version"`
	GeneratedAt string            `json:"generated_at"`
	Stations    []rpChartsStation `json:"stations"`
}

type rpChartsStation struct {
	Channel int            `json:"channel"`
	Slug    string         `json:"slug"`
	Name    string         `json:"name"`
	Weekly  rpChartsWindow `json:"weekly"`
	Monthly rpChartsWindow `json:"monthly"`
	Yearly  rpChartsWindow `json:"yearly"`
}

type rpChartsWindow struct {
	Period     string          `json:"period"`
	Songs      []rpChartsEntry `json:"songs"`
	Artists    []rpChartsEntry `json:"artists"`
	Albums     []rpChartsEntry `json:"albums"`
	NewSongs   []rpChartsEntry `json:"new_songs"`
	NewArtists []rpChartsEntry `json:"new_artists"`
	NewAlbums  []rpChartsEntry `json:"new_albums"`
}

type rpChartsEntry struct {
	Rank      int    `json:"rank"`
	Name      string `json:"name"`
	Sub       string `json:"sub"`
	Plays     int    `json:"plays"`
	Cover     string `json:"cover"`
	Year      int    `json:"year"`
	SongID    int    `json:"song_id"`
	AlbumID   string `json:"album_id"`
	CreatedAt string `json:"created_at"`
}

type RPChartsProvider struct {
	stations []string
	client   *http.Client
	endpoint string
}

var (
	_ ReleaseProvider = (*RPChartsProvider)(nil)
	_ WeekSettable    = (*RPChartsProvider)(nil)
)

func NewRPChartsProvider(stations []string) *RPChartsProvider {
	return &RPChartsProvider{
		stations: stations,
		client:   &http.Client{Timeout: 15 * time.Second},
		endpoint: rpChartsEndpoint,
	}
}

func (p *RPChartsProvider) Name() string {
	return "rpcharts"
}

// SetWeekRange is a no-op: rpcharts only exposes the current rolling
// weekly/monthly/yearly windows, with no historical pages.
func (p *RPChartsProvider) SetWeekRange(year, week int) {}

func (p *RPChartsProvider) Scrape() ([]ScrapedItem, error) {
	payload, err := p.fetch()
	if err != nil {
		return nil, err
	}

	// rpcharts "albums" is the weekly top-albums chart. It still contains a
	// lot of rarely-played back catalog, so we restrict to the current year
	// and the previous year (release Year from the payload). created_at is
	// the date RP's chart tracker first observed the album (tracking began
	// May 2026) — not its commercial release date, and is not used for the
	// year filter.
	currentYear := time.Now().Year()
	minYear := currentYear - 1

	var items []ScrapedItem
	for _, entry := range p.selectedAlbums(payload) {
		if entry.Year < minYear {
			continue
		}
		item := ScrapedItem{
			Title:       entry.Name,
			ArtistName:  entry.Sub,
			Year:        entry.Year,
			ReleaseDate: entry.CreatedAt,
			MediaType:   model.MediaTypeMusic,
			Source:      "rpcharts",
			ImageURL:    entry.Cover,
			Notes:       rpChartsNotes(entry),
		}
		items = append(items, item)
	}
	return items, nil
}

// rpChartsNotes builds the event notes: the Radio Paradise album URL, plus a
// "stations:" segment listing the display names of the stations whose charts
// the album appeared on (shown in `wmdl review`).
func rpChartsNotes(entry rpChartsAlbumEntry) string {
	notes := rpAlbumURL(entry.AlbumID)
	if len(entry.Stations) == 0 {
		return notes
	}
	if notes != "" {
		notes += "|"
	}
	return notes + "stations: " + strings.Join(entry.Stations, ", ")
}

func (p *RPChartsProvider) fetch() (*rpChartsPayload, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wmdl/1.0 (rpcharts provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching rpcharts data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpcharts endpoint returned %d", resp.StatusCode)
	}

	var payload rpChartsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding rpcharts data: %w", err)
	}
	return &payload, nil
}

// selectedAlbums returns the weekly top "albums" entries for the configured
// stations. The "all" slug (or an empty config) selects the All Stations
// aggregate (channel -1), a sum of plays across every feed. That aggregate is
// itself skewed toward whichever single station plays a track most (e.g.
// RockIt! leads the majority of the chart), so callers wanting a curated,
// low-replay view should pick specific station slugs instead. Specific slugs
// merge their windows; overlaps are deduplicated by the runner's seenMusic key.
func (p *RPChartsProvider) selectedAlbums(payload *rpChartsPayload) []rpChartsAlbumEntry {
	if len(p.stations) == 0 || containsStr(p.stations, rpChartsStationSlugAll) {
		for _, st := range payload.Stations {
			if st.Channel == -1 {
				return withStations(st.Weekly.Albums, st.Name)
			}
		}
		return nil
	}

	// Merge the selected stations' windows, deduplicating albums that appear
	// on more than one chart and accumulating their station display names.
	byAlbum := map[string]*rpChartsAlbumEntry{}
	var order []string
	for _, st := range payload.Stations {
		if !containsStr(p.stations, st.Slug) {
			continue
		}
		for _, e := range st.Weekly.Albums {
			key := e.AlbumID
			if key == "" {
				key = e.Name + "|" + e.Sub
			}
			existing, ok := byAlbum[key]
			if !ok {
				existing = &rpChartsAlbumEntry{rpChartsEntry: e}
				byAlbum[key] = existing
				order = append(order, key)
			}
			if !containsStr(existing.Stations, st.Name) {
				existing.Stations = append(existing.Stations, st.Name)
			}
		}
	}
	out := make([]rpChartsAlbumEntry, 0, len(order))
	for _, key := range order {
		out = append(out, *byAlbum[key])
	}
	return out
}

// rpChartsAlbumEntry is a chart album entry annotated with the display names
// of the stations whose charts it appeared on.
type rpChartsAlbumEntry struct {
	rpChartsEntry
	Stations []string
}

func withStations(entries []rpChartsEntry, station string) []rpChartsAlbumEntry {
	out := make([]rpChartsAlbumEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, rpChartsAlbumEntry{rpChartsEntry: e, Stations: []string{station}})
	}
	return out
}

func rpAlbumURL(albumID string) string {
	if albumID == "" {
		return ""
	}
	return "https://radioparadise.com/music/album/" + albumID
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}
