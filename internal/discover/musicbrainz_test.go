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
	if len(calls) != 2 {
		t.Errorf("expected both query fields (release-group + release) to run when no hits, got %d: %v", len(calls), calls)
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

func TestCleanQueryTitle(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"12 Golden Country Greats [30th Anniversary] [Expanded Edition]", "12 Golden Country Greats"},
		{"Shiverstruck [Blue]", "Shiverstruck"},
		{"Fillmore Auditorium, San Francisco, CA, 7/3/66", "Fillmore Auditorium, San Francisco, CA"},
		{"Fillmore Auditorium, San Francisco, CA (7/3/66)", "Fillmore Auditorium, San Francisco, CA"},
		{"Something (The Piano Versions)", "Something"},
		{"Plain Title", "Plain Title"},
	}
	for _, tt := range tests {
		if got := cleanQueryTitle(tt.in); got != tt.want {
			t.Errorf("cleanQueryTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestComposerWork(t *testing.T) {
	tests := []struct {
		in, composer, work string
		ok                 bool
	}{
		{"Steve Reich: The Sextets", "Steve Reich", "The Sextets", true},
		{"Carl Vine: Child's Play", "Carl Vine", "Child's Play", true},
		{"Archipel: Claude Debussy - La Mer; John Ireland - Sarnia", "Archipel", "Claude Debussy - La Mer; John Ireland - Sarnia", true},
		{"Ever the Optimist", "", "", false},
		{"20 All Time Greats of the 50’s", "", "", false},
	}
	for _, tt := range tests {
		composer, work, ok := composerWork(tt.in)
		if ok != tt.ok || composer != tt.composer || work != tt.work {
			t.Errorf("composerWork(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.in, composer, work, ok, tt.composer, tt.work, tt.ok)
		}
	}
}

func TestArtistQueryVariants(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"The Trash Can Sinatras", []string{"The Trash Can Sinatras", "Trash Can Sinatras", "Sinatras"}},
		{"Charlie Musselwhite & GA-20", []string{"Charlie Musselwhite & GA-20", "Charlie Musselwhite", "GA-20"}},
		{"The Revivalists", []string{"The Revivalists", "Revivalists"}},
		{"Ween", []string{"Ween"}},
	}
	for _, tt := range tests {
		got := artistQueryVariants(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("artistQueryVariants(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("artistQueryVariants(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestArtistNamesMatchVariants(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"The Trash Can Sinatras", "Trashcan Sinatras", true},
		{"Charlie Musselwhite & GA-20", "GA-20", true},
		{"The Revivalists", "Revivalists", true},
		{"The All-American Rejects", "The All‐American Rejects", true},
		{"Marty Robbins", "Various Artists", false},
		{"The Trash Can Sinatras", "Foo Fighters", false},
	}
	for _, tt := range tests {
		if got := artistNamesMatchVariants(tt.a, tt.b); got != tt.want {
			t.Errorf("artistNamesMatchVariants(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestReleaseGroupArtistMatches(t *testing.T) {
	// Composer-credited classical release matches via the "Composer: Work" title.
	if !releaseGroupArtistMatches("Steve Reich", []string{"Colin Currie"}, "Steve Reich: The Sextets") {
		t.Error("expected composer-credited release to match classical title")
	}
	// Various Artists compilation must NOT match a specific artist.
	if releaseGroupArtistMatches("Various Artists", []string{"Marty Robbins"}, "20 All Time Greats of the 50’s") {
		t.Error("expected Various Artists compilation to NOT match a specific artist")
	}
	// Performer credit matches directly.
	if !releaseGroupArtistMatches("Aline Piboule", []string{"Aline Piboule"}, "Archipel: Claude Debussy - La Mer; John Ireland - Sarnia") {
		t.Error("expected performer credit to match")
	}
}

func TestNormalizeTitleForCompare(t *testing.T) {
	tests := [][2]string{
		{"Holst: The Planets; Bax: Tintagel", "Holst: The Planets / Bax: Tintagel"},
		{"Same Sun/Same Sky", "Same Sun / Same Sky"},
	}
	for _, tt := range tests {
		if normalizeTitleForCompare(tt[0]) != normalizeTitleForCompare(tt[1]) {
			t.Errorf("normalizeTitleForCompare(%q) != normalizeTitleForCompare(%q)", tt[0], tt[1])
		}
	}
}

func TestPickBestReleaseGroup_ClassicalComposerCredit(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("col-1", "The Sextets", "Album", "2026-04-10", "Steve Reich", "sr", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "Steve Reich: The Sextets", "Colin Currie", 2026, "")
	if got == nil || got.MBID != "col-1" {
		t.Errorf("expected composer-credited classical match, got %+v", got)
	}
}

func TestPickBestReleaseGroup_SlashSemicolonTitle(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("pap-1", "Holst: The Planets / Bax: Tintagel", "Album", "2026-03-20", "Holst", "h", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "Holst: The Planets; Bax: Tintagel", "Antonio Pappano", 2026, "")
	if got == nil || got.MBID != "pap-1" {
		t.Errorf("expected semicolon/slash title equivalence, got %+v", got)
	}
}

func TestPickBestReleaseGroup_ClassicalWeakTitleMatch(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("pib-1", "Archipel (Debussy: La Mer - Ireland: Sarnia)", "Album", "2026-03-27", "Aline Piboule", "ap", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "Archipel: Claude Debussy - La Mer; John Ireland - Sarnia", "Aline Piboule", 2026, "")
	if got == nil || got.MBID != "pib-1" {
		t.Errorf("expected weak classical token-overlap match, got %+v", got)
	}
}

func TestPickBestReleaseGroup_WordSquashedArtist(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("tcs-1", "Ever The Optimist", "Album", "2026-07-31", "Trashcan Sinatras", "ts", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "Ever the Optimist", "The Trash Can Sinatras", 2026, "")
	if got == nil || got.MBID != "tcs-1" {
		t.Errorf("expected word-squashed artist variant match, got %+v", got)
	}
}

func TestPickBestReleaseGroup_ConjunctionArtist(t *testing.T) {
	mb := NewMBClient()
	candidates := []*MBReleaseGroupResult{
		cand("ga-1", "BLUES NOW", "Album", "2026-07-31", "GA-20", "ga", 100),
	}
	got := mb.pickBestReleaseGroup(candidates, "BLUES NOW", "Charlie Musselwhite & GA-20", 2026, "")
	if got == nil || got.MBID != "ga-1" {
		t.Errorf("expected after-conjunction artist variant match, got %+v", got)
	}
}

func TestSearchReleaseGroup_BracketSuffixUsesCleanedTitle(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Query().Get("query"), `releasegroup:"12 Golden Country Greats"`) {
			w.Write([]byte(`{
				"release-groups": [
					{"id":"d6d2ee0d-791d-3270-a7d1-a49d7b3e8ebc","title":"12 Golden Country Greats","score":100,"primary-type":"Album","first-release-date":"1996-07-16","artist-credit":[{"name":"Ween","artist":{"id":"w"}}]}
				]
			}`))
			return
		}
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "12 Golden Country Greats [30th Anniversary] [Expanded Edition]", "Ween", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got == nil || got.MBID != "d6d2ee0d-791d-3270-a7d1-a49d7b3e8ebc" {
		t.Fatalf("expected bracket-stripped match, got %+v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("expected primary + cleaned-title query, got %d: %v", len(calls), calls)
	}
}

func TestSearchReleaseGroup_ClassicalFallbackQueries(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		calls = append(calls, q)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, `releasegroup:"The Sextets" AND artist:"Colin Currie"`) {
			w.Write([]byte(`{
				"release-groups": [
					{"id":"45454175-f1f6-4646-a471-9b7fe4601058","title":"The Sextets","score":100,"primary-type":"Album","first-release-date":"2026-04-10","artist-credit":[{"name":"Steve Reich","artist":{"id":"sr"}}]}
				]
			}`))
			return
		}
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "Steve Reich: The Sextets", "Colin Currie", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got == nil || got.MBID != "45454175-f1f6-4646-a471-9b7fe4601058" {
		t.Fatalf("expected classical composer-credit match, got %+v", got)
	}
	found := false
	for _, q := range calls {
		if strings.Contains(q, `releasegroup:"The Sextets"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected work-only fallback query to be issued, calls: %v", calls)
	}
}

func TestSearchReleaseGroup_SpacedSlashVariant(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		calls = append(calls, q)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, `releasegroup:"Same Sun / Same Sky"`) {
			w.Write([]byte(`{
				"release-groups": [
					{"id":"ec099900-9b98-4efe-91eb-d575eccbbfeb","title":"Same Sun / Same Sky","score":100,"primary-type":"Album","first-release-date":"2026-07-31","artist-credit":[{"name":"The Darling Buds","artist":{"id":"db"}}]}
				]
			}`))
			return
		}
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "Same Sun/Same Sky", "The Darling Buds", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got == nil || got.MBID != "ec099900-9b98-4efe-91eb-d575eccbbfeb" {
		t.Fatalf("expected spaced-slash match, got %+v", got)
	}
}

func TestSearchReleaseGroup_ComposerAsArtistFallback(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		calls = append(calls, q)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, `releasegroup:"Child's Play" AND artist:"Carl Vine"`) {
			w.Write([]byte(`{
				"release-groups": [
					{"id":"46ae4f72-36b5-4fc9-bad0-5bd90a0c9eea","title":"Child's Play","score":100,"primary-type":"Album","first-release-date":"2026-04","artist-credit":[{"name":"Carl Vine","artist":{"id":"cv"}}]}
				]
			}`))
			return
		}
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), "Carl Vine: Child's Play", "Umberto Clerici", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got == nil || got.MBID != "46ae4f72-36b5-4fc9-bad0-5bd90a0c9eea" {
		t.Fatalf("expected composer-as-artist fallback match, got %+v", got)
	}
	found := false
	for _, q := range calls {
		if strings.Contains(q, `artist:"Carl Vine"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected composer-as-artist query to be issued, calls: %v", calls)
	}
}

func TestSearchReleaseGroup_QuotesAreSanitized(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"release-groups":[]}`))
	}))
	defer srv.Close()

	mb := NewMBClient()
	mb.baseURL = srv.URL

	got, err := mb.SearchReleaseGroup(context.Background(), `Shostakovich: Symphonies Nos. 2 "To October" & 5`, "John Storgårds", 2026, "")
	if err != nil {
		t.Fatalf("SearchReleaseGroup: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no match, got %+v", got)
	}
	for _, q := range calls {
		if strings.Contains(q, `"To October"`) {
			t.Errorf("query contains unsanitized double quotes: %q", q)
		}
	}
	if len(calls) == 0 || !strings.Contains(calls[0], `releasegroup:"Shostakovich: Symphonies Nos. 2 'To October' & 5"`) {
		t.Errorf("expected sanitized primary query, first call: %v", calls)
	}
}
