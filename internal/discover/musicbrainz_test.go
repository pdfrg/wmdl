package discover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtistNamesMatch(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"exact", "The All-American Rejects", "The All-American Rejects", true},
		{"case insensitive", "the ALL-american rejects", "THE ALL-AMERICAN REJECTS", true},
		{"unicode hyphen U+2010", "The All‐American Rejects", "The All-American Rejects", true},
		{"non-breaking hyphen U+2011", "The All‑American Rejects", "The All-American Rejects", true},
		{"en dash", "The All–American Rejects", "The All-American Rejects", true},
		{"em dash", "The All—American Rejects", "The All-American Rejects", true},
		{"minus sign", "The All−American Rejects", "The All-American Rejects", true},
		{"curly apostrophe", "Bob’s Band", "Bob's Band", true},
		{"accent fold", "Björk", "Bjork", true},
		{"whitespace collapse", "Foo    Bar", "Foo Bar", true},
		{"trailing space", "Foo Bar ", "Foo Bar", true},
		{"different artist", "The All-American Rejects", "Foo Fighters", false},
		{"partially shared word", "Bon Iver", "Iver", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := artistNamesMatch(tt.a, tt.b); got != tt.want {
				t.Errorf("artistNamesMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func cand(id, title, primary, date, artist, artistMBID string, score int) *MBReleaseGroupResult {
	return &MBReleaseGroupResult{
		MBID:             id,
		Title:            title,
		Score:            score,
		PrimaryType:      primary,
		FirstReleaseDate: date,
		ArtistMBID:       artistMBID,
		ArtistName:       artist,
	}
}

// Reproduces the reported regression: "The All-American Rejects - Sandbox".
// MusicBrainz returns a Single, the Album, and an EP under the same title with
// equal relevance; the Album must win on year + primary-type preference.
func TestPickBestReleaseGroup_SandboxAlbum(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("79f1e7b5-a897-4d8d-b62b-92e5731b2865", "Sandbox", "Single", "2025-04-24", "The All‐American Rejects", "aa1", 100),
		cand("4c379675-b7c0-41ae-a613-e5519dee7de4", "Sandbox", "Album", "2026-05-15", "The All‐American Rejects", "aa1", 100),
		cand("47931197-5b31-4100-8f20-19198a8737ff", "Sandbox", "EP", "2025-06-05", "The All‐American Rejects", "aa1", 100),
	}

	got := mb.pickBestReleaseGroup(candidates, "Sandbox", "The All-American Rejects", 2026, "")
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.MBID != "4c379675-b7c0-41ae-a613-e5519dee7de4" {
		t.Errorf("expected Album MBID, got %q (%s)", got.MBID, got.PrimaryType)
	}
	if got.ArtistMBID == "" {
		t.Error("expected ArtistMBID to be populated")
	}
}

func TestPickBestReleaseGroup_ExactTypeHint(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("ep-1", "Narrow Head", "EP", "2026-03-01", "Narrow Head", "nh", 100),
		cand("al-1", "Narrow Head", "Album", "2026-03-01", "Narrow Head", "nh", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "Narrow Head", "Narrow Head", 2026, "ep")
	if got == nil || got.MBID != "ep-1" {
		t.Errorf("expected EP to win with ep type hint, got %+v", got)
	}
}

func TestPickBestReleaseGroup_ArtistMismatch(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("other-1", "Sandbox", "Album", "2026-05-15", "Some Other Artist", "x", 100),
	}
	if got := mb.pickBestReleaseGroup(candidates, "Sandbox", "The All-American Rejects", 2026, ""); got != nil {
		t.Errorf("expected no match on artist mismatch, got %q", got.MBID)
	}
}

func TestPickBestReleaseGroup_NoCandidates(t *testing.T) {
	mb := NewMBClient()
	if got := mb.pickBestReleaseGroup(nil, "Sandbox", "The All-American Rejects", 2026, ""); got != nil {
		t.Errorf("expected nil for empty candidates, got %+v", got)
	}
}

func TestCanonicalAlbumType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Album", "album"},
		{"album", "album"},
		{"LP", "album"},
		{"lp", "album"},
		{"EP", "ep"},
		{"Single", "single"},
		{"Box Set", "box set"},
	}
	for _, tt := range tests {
		if got := canonicalAlbumType(tt.in); got != tt.want {
			t.Errorf("canonicalAlbumType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReleaseYear(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"2026-05-15", 2026},
		{"2026-05", 2026},
		{"2026", 2026},
		{"", 0},
		{"garbage", 0},
	}
	for _, tt := range tests {
		if got := releaseYear(tt.in); got != tt.want {
			t.Errorf("releaseYear(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestSearchReleaseGroup_UsesReleaseGroupFieldAndPicksAlbum(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"release-groups": [
				{"id":"79f1e7b5-a897-4d8d-b62b-92e5731b2865","title":"Sandbox","score":100,"primary-type":"Single","first-release-date":"2025-04-24","artist-credit":[{"name":"The All‐American Rejects","artist":{"id":"a1"}}]},
				{"id":"4c379675-b7c0-41ae-a613-e5519dee7de4","title":"Sandbox","score":100,"primary-type":"Album","first-release-date":"2026-05-15","artist-credit":[{"name":"The All‐American Rejects","artist":{"id":"a1"}}]},
				{"id":"47931197-5b31-4100-8f20-19198a8737ff","title":"Sandbox","score":100,"primary-type":"EP","first-release-date":"2025-06-05","artist-credit":[{"name":"The All‐American Rejects","artist":{"id":"a1"}}]}
			]
		}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "Sandbox", "The All-American Rejects", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.MBID != "4c379675-b7c0-41ae-a613-e5519dee7de4" {
		t.Errorf("expected Album MBID, got %q", got.MBID)
	}
	if len(calls) != 1 {
		t.Errorf("expected exactly 1 API call (early exit), got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[0], `releasegroup:"Sandbox"`) {
		t.Errorf("expected releasegroup field in query, got %q", calls[0])
	}
	if !strings.Contains(calls[0], `artist:"The All-American Rejects"`) {
		t.Errorf("expected artist constraint in query, got %q", calls[0])
	}
}

func TestSearchReleaseGroup_EmptyResults(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "Does Not Exist", "Some Artist", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result, got %+v", got)
	}
	if len(calls) != 4 {
		t.Errorf("expected all 4 query variants to run when no hits, got %d: %v", len(calls), calls)
	}
}

func TestSearchReleaseGroupByAlbum_BlindSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"release-groups": [
				{"id":"al-9","title":"Sandbox","score":100,"primary-type":"Album","first-release-date":"2026-05-15","artist-credit":[{"name":"The All‐American Rejects","artist":{"id":"a1"}}]}
			]
		}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroupByAlbum(context.Background(), "Sandbox", 2026)
	if err != nil {
		t.Fatalf("SearchReleaseGroupByAlbum: %v", err)
	}
	if got == nil || got.MBID != "al-9" {
		t.Errorf("expected album match, got %+v", got)
	}
}

func TestMBResponseShape(t *testing.T) {
	// Sanity check that the raw struct matches MusicBrainz's JSON shape,
	// including the hyphen-rich artist credit name.
	raw := []byte(`{
		"count": 3,
		"release-groups": [{
			"id": "4c379675-b7c0-41ae-a613-e5519dee7de4",
			"score": 100,
			"primary-type": "Album",
			"first-release-date": "2026-05-15",
			"artist-credit": [{"name": "The All‐American Rejects", "artist": {"id": "a1"}}]
		}]
	}`)
	var result mbReleaseGroupResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.ReleaseGroups) != 1 {
		t.Fatalf("expected 1 release group, got %d", len(result.ReleaseGroups))
	}
	rg := result.ReleaseGroups[0]
	if rg.ID != "4c379675-b7c0-41ae-a613-e5519dee7de4" || rg.PrimaryType != "Album" || rg.FirstReleaseDate != "2026-05-15" {
		t.Errorf("unexpected parsed fields: %+v", rg)
	}
	if len(rg.ArtistCredit) == 0 || rg.ArtistCredit[0].Name != "The All‐American Rejects" {
		t.Errorf("artist credit not parsed: %+v", rg.ArtistCredit)
	}
}
