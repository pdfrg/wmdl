package db

import (
	"context"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(t.TempDir() + "/wmdl-test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return d
}

func TestOpenAndMigrate(t *testing.T) {
	d := openTestDB(t)
	if d == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestUpsertTitle(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{
		TmdbID:    100,
		Title:     "Test Movie",
		Year:      2025,
		MediaType: model.MediaTypeMovie,
		ImdbID:    "tt1234567",
	}

	id, err := d.UpsertTitle(ctx, t1)
	if err != nil {
		t.Fatalf("UpsertTitle: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := d.GetTitleByTmdbID(ctx, 100)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got == nil {
		t.Fatal("expected title, got nil")
	}
	if got.Title != "Test Movie" {
		t.Fatalf("expected 'Test Movie', got %q", got.Title)
	}
	if got.Year != 2025 {
		t.Fatalf("expected year 2025, got %d", got.Year)
	}
}

func TestUpsertTitleUpdatesExisting(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{TmdbID: 100, Title: "Old Title", Year: 2025, MediaType: model.MediaTypeMovie}
	if _, err := d.UpsertTitle(ctx, t1); err != nil {
		t.Fatalf("first UpsertTitle: %v", err)
	}

	t2 := &model.Title{TmdbID: 100, Title: "Updated Title", Year: 2025, MediaType: model.MediaTypeMovie}
	if _, err := d.UpsertTitle(ctx, t2); err != nil {
		t.Fatalf("second UpsertTitle: %v", err)
	}

	got, err := d.GetTitleByTmdbID(ctx, 100)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got.Title != "Updated Title" {
		t.Fatalf("expected 'Updated Title', got %q", got.Title)
	}
}

func TestCreateAndListReleaseEvents(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{TmdbID: 200, Title: "TV Show", Year: 2025, MediaType: model.MediaTypeTV}
	titleID, err := d.UpsertTitle(ctx, t1)
	if err != nil {
		t.Fatalf("UpsertTitle: %v", err)
	}

	ev := &model.ReleaseEvent{
		TitleID:     titleID,
		Source:      "test",
		ReleaseType: model.ReleaseStreaming,
		ReleaseDate: "2025-01-01",
		Status:      model.StatusPending,
	}

	evID, err := d.CreateReleaseEvent(ctx, ev)
	if err != nil {
		t.Fatalf("CreateReleaseEvent: %v", err)
	}
	if evID == 0 {
		t.Fatal("expected non-zero event ID")
	}

	events, err := d.ListReleaseEvents(ctx, model.StatusPending)
	if err != nil {
		t.Fatalf("ListReleaseEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Source != "test" {
		t.Fatalf("expected source 'test', got %q", events[0].Source)
	}
}

func TestUpdateReleaseEventStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{TmdbID: 300, Title: "Test", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, t1)
	evID, _ := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusPending,
	})

	if err := d.UpdateReleaseEventStatus(ctx, evID, model.StatusApproved); err != nil {
		t.Fatalf("UpdateReleaseEventStatus: %v", err)
	}

	events, _ := d.ListReleaseEvents(ctx, model.StatusApproved)
	if len(events) != 1 {
		t.Fatalf("expected 1 approved event, got %d", len(events))
	}
}

func TestCreateAndGetDownload(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t1 := &model.Title{TmdbID: 400, Title: "Download Test", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, t1)
	evID, _ := d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusApproved,
	})

	dl := &model.Download{
		TitleID:        titleID,
		ReleaseEventID: evID,
		Quality:        "1080p",
		SourceType:     "web-dl",
		Codec:          "h264",
		InfoHash:       "abc123",
		Category:       "Movies",
		Status:         model.DownloadAdded,
	}

	dlID, err := d.CreateDownload(ctx, dl)
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if dlID == 0 {
		t.Fatal("expected non-zero download ID")
	}

	got, err := d.GetDownloadByTitleID(ctx, titleID)
	if err != nil {
		t.Fatalf("GetDownloadByTitleID: %v", err)
	}
	if got == nil {
		t.Fatal("expected download, got nil")
	}
	if got.InfoHash != "abc123" {
		t.Fatalf("expected info_hash 'abc123', got %q", got.InfoHash)
	}
}

func TestUpsertAndGetWeekState(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	ws := &model.WeekState{
		Year:       2025,
		Week:       10,
		WeekDate:   "2025-03-03",
		Discovered: true,
		Reviewed:   false,
		Processed:  false,
	}

	if err := d.UpsertWeekState(ctx, ws); err != nil {
		t.Fatalf("UpsertWeekState: %v", err)
	}

	got, err := d.GetWeekState(ctx, 2025, 10)
	if err != nil {
		t.Fatalf("GetWeekState: %v", err)
	}
	if got == nil {
		t.Fatal("expected week state, got nil")
	}
	if !got.Discovered {
		t.Fatal("expected discovered=true")
	}
	if got.Reviewed {
		t.Fatal("expected reviewed=false")
	}
}

func TestUpsertWeekStateMaxSemantics(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	ws1 := &model.WeekState{Year: 2025, Week: 10, Discovered: false, Reviewed: false, Processed: false}
	if err := d.UpsertWeekState(ctx, ws1); err != nil {
		t.Fatalf("first UpsertWeekState: %v", err)
	}

	ws2 := &model.WeekState{Year: 2025, Week: 10, Discovered: true, Reviewed: false, Processed: false}
	if err := d.UpsertWeekState(ctx, ws2); err != nil {
		t.Fatalf("second UpsertWeekState: %v", err)
	}

	got, _ := d.GetWeekState(ctx, 2025, 10)
	if !got.Discovered {
		t.Fatal("expected discovered=true after update")
	}

	ws3 := &model.WeekState{Year: 2025, Week: 10, Discovered: false, Reviewed: false, Processed: false}
	if err := d.UpsertWeekState(ctx, ws3); err != nil {
		t.Fatalf("third UpsertWeekState: %v", err)
	}

	got, _ = d.GetWeekState(ctx, 2025, 10)
	if !got.Discovered {
		t.Fatal("expected discovered=true (MAX semantics should prevent downgrade)")
	}
}

func TestGetWeekStates(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		d.UpsertWeekState(ctx, &model.WeekState{
			Year: 2025, Week: i, Discovered: i%2 == 0,
		})
	}

	all, err := d.GetWeekStates(ctx, 0)
	if err != nil {
		t.Fatalf("GetWeekStates: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 states, got %d", len(all))
	}

	limited, err := d.GetWeekStates(ctx, 2)
	if err != nil {
		t.Fatalf("GetWeekStates with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 states, got %d", len(limited))
	}
}

func TestListPendingWithTitles(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	title := &model.Title{TmdbID: 500, Title: "Pending Movie", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, title)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusPending,
	})

	results, err := d.ListPendingWithTitles(ctx)
	if err != nil {
		t.Fatalf("ListPendingWithTitles: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title.Title != "Pending Movie" {
		t.Fatalf("expected 'Pending Movie', got %q", results[0].Title.Title)
	}
}

func TestListApprovedWithTitles(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	title := &model.Title{TmdbID: 600, Title: "Approved Movie", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, title)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusApproved,
	})

	results, err := d.ListApprovedWithTitles(ctx)
	if err != nil {
		t.Fatalf("ListApprovedWithTitles: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestListEventsByWeekWithTitles(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	title := &model.Title{TmdbID: 700, Title: "Week Movie", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, title)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusPending,
		ISOYear: 2025, ISOWeek: 15,
	})

	results, err := d.ListEventsByWeekWithTitles(ctx, 2025, 15)
	if err != nil {
		t.Fatalf("ListEventsByWeekWithTitles: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	results, err = d.ListEventsByWeekWithTitles(ctx, 2025, 16)
	if err != nil {
		t.Fatalf("ListEventsByWeekWithTitles: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for week 16, got %d", len(results))
	}
}

func TestListEventsByWeekAndStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	title := &model.Title{TmdbID: 800, Title: "Filter Movie", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, title)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "test", Status: model.StatusApproved,
		ISOYear: 2025, ISOWeek: 20,
	})

	approved, _ := d.ListEventsByWeekAndStatus(ctx, 2025, 20, model.StatusApproved)
	if len(approved) != 1 {
		t.Fatalf("expected 1 approved, got %d", len(approved))
	}

	pending, _ := d.ListEventsByWeekAndStatus(ctx, 2025, 20, model.StatusPending)
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending, got %d", len(pending))
	}
}

func TestGetLatestReleaseEvent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	title := &model.Title{TmdbID: 900, Title: "Latest Event", Year: 2025, MediaType: model.MediaTypeMovie}
	titleID, _ := d.UpsertTitle(ctx, title)

	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "first", Status: model.StatusRejected, ISOYear: 2025, ISOWeek: 1,
	})
	d.CreateReleaseEvent(ctx, &model.ReleaseEvent{
		TitleID: titleID, Source: "second", Status: model.StatusPending, ISOYear: 2025, ISOWeek: 2,
	})

	latest, err := d.GetLatestReleaseEvent(ctx, titleID)
	if err != nil {
		t.Fatalf("GetLatestReleaseEvent: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest event, got nil")
	}
	if latest.Source != "second" {
		t.Fatalf("expected 'second', got %q", latest.Source)
	}
}

func TestGetLatestDiscoveredWeek(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertWeekState(ctx, &model.WeekState{Year: 2025, Week: 5, Discovered: false})
	d.UpsertWeekState(ctx, &model.WeekState{Year: 2025, Week: 10, Discovered: true})
	d.UpsertWeekState(ctx, &model.WeekState{Year: 2025, Week: 15, Discovered: true})

	latest, err := d.GetLatestDiscoveredWeek(ctx)
	if err != nil {
		t.Fatalf("GetLatestDiscoveredWeek: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest week, got nil")
	}
	if latest.Week != 15 {
		t.Fatalf("expected week 15, got %d", latest.Week)
	}
}

func TestGetTitleByTmdbID_NotFound(t *testing.T) {
	d := openTestDB(t)
	got, err := d.GetTitleByTmdbID(context.Background(), 99999)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for non-existent tmdb_id")
	}
}

func TestListTitles(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	d.UpsertTitle(ctx, &model.Title{TmdbID: 101, Title: "Movie A", Year: 2025, MediaType: model.MediaTypeMovie})
	d.UpsertTitle(ctx, &model.Title{TmdbID: 102, Title: "Movie B", Year: 2025, MediaType: model.MediaTypeMovie})

	titles, err := d.ListTitles(ctx)
	if err != nil {
		t.Fatalf("ListTitles: %v", err)
	}
	if len(titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(titles))
	}
}
