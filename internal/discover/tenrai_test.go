//go:build integration

package discover

import (
	"testing"

	"github.com/pdfrg/wmdl/internal/config"
)

func TestTenraiPhaseA(t *testing.T) {
	cfg := config.AnimeConfig{
		Enabled:       true,
		MinScore:      0,
		MinMembers:    0,
		PhaseBEnabled: false,
	}
	p := NewTenraiAnimeProvider(cfg)
	items, err := p.Scrape()
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}
	t.Logf("Tenrai Phase A found %d items", len(items))
	if len(items) == 0 {
		t.Log("0 items — no completions in current week window (may be normal)")
		return
	}
	for i, item := range items {
		if i > 2 {
			break
		}
		t.Logf("  %s (year=%d, mal_id=%d, score=%.1f, members=%d)",
			item.Title, item.Year, item.MalID, item.ImdbRating, item.AnimeMembers)
	}
}

func TestTenraiPhaseB(t *testing.T) {
	cfg := config.AnimeConfig{
		Enabled:          true,
		MinScore:         10,
		MinMembers:       999999999,
		PhaseBEnabled:    true,
		PhaseBMinScore:   0,
		PhaseBMinMembers: 0,
		MinPhaseBResults: 999,
	}
	p := NewTenraiAnimeProvider(cfg)
	items, err := p.Scrape()
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}
	t.Logf("Tenrai Phase B found %d items", len(items))
	if len(items) == 0 {
		t.Fatal("no Phase B items found")
	}
	for i, item := range items {
		if i > 5 {
			break
		}
		t.Logf("  %s (mal_id=%d, score=%.1f, members=%d)",
			item.Title, item.MalID, item.ImdbRating, item.AnimeMembers)
	}
}
