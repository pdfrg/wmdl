//go:build integration

package discover

import (
	"testing"
	"time"

	"github.com/pdfrg/wmdl/internal/config"
)

func TestAniListPhaseA(t *testing.T) {
	cfg := config.AnimeConfig{
		Enabled:       true,
		MinScore:      0,
		MinMembers:    0,
		PhaseBEnabled: false,
	}

	year, week := time.Now().ISOWeek()
	ws, we := wmdlWeekRange(year, week)
	t.Logf("Current wmdl week: %d-W%02d (%s to %s)", year, week, ws.Format("2006-01-02"), we.Format("2006-01-02"))

	p := NewAniListProvider(cfg)
	items, err := p.Scrape()
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	t.Logf("Phase A found %d items", len(items))
	if len(items) == 0 {
		t.Log("0 items — this week has no completed anime ending in window (may be normal)")
		return
	}

	for i, item := range items {
		if i > 2 {
			break
		}
		t.Logf("  %s (year=%d, source=%s, mal_id=%d, score=%.1f, members=%d)",
			item.Title, item.Year, item.Source, item.MalID, item.ImdbRating, item.AnimeMembers)
	}
}

func TestAniListPhaseB(t *testing.T) {
	cfg := config.AnimeConfig{
		Enabled:          true,
		MinScore:         10,
		MinMembers:       999999999,
		PhaseBEnabled:    true,
		PhaseBMinScore:   0,
		PhaseBMinMembers: 0,
		MinPhaseBResults: 999,
	}

	p := NewAniListProvider(cfg)
	items, err := p.Scrape()
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	t.Logf("Phase B found %d items (max 25 from single page)", len(items))
	if len(items) == 0 {
		t.Fatal("no phase B items found — Phase B query may be broken")
	}

	for i, item := range items {
		if i >= 5 && i < len(items)-1 {
			continue
		}
		t.Logf("  %s (source=%s, mal_id=%d, score=%.1f, members=%d)",
			item.Title, item.Source, item.MalID, item.ImdbRating, item.AnimeMembers)
	}

	for _, item := range items {
		if item.Source != "anilist-airing" {
			t.Errorf("expected source 'anilist-airing', got %q", item.Source)
		}
		if item.MalID == 0 {
			t.Logf("note: item %q has no MAL ID (not indexed on MAL)", item.Title)
		}
	}
}

func TestAniListScoreFilter(t *testing.T) {
	cfg := config.AnimeConfig{
		Enabled:       true,
		MinScore:      9.0,
		MinMembers:    50000,
		PhaseBEnabled: false,
	}

	p := NewAniListProvider(cfg)
	items, err := p.Scrape()
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	t.Logf("Score-filtered (MinScore=9.0, MinMembers=50000): %d items", len(items))
	for _, item := range items {
		if item.ImdbRating < 9.0 {
			t.Errorf("item %s has score %.1f < 9.0", item.Title, item.ImdbRating)
		}
		if item.AnimeMembers < 50000 {
			t.Errorf("item %s has members %d < 50000", item.Title, item.AnimeMembers)
		}
	}
}
