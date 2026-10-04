package db

import (
	"context"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestTitleNetworksRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	tv := &model.Title{
		TmdbID:    111,
		Title:     "Severance",
		Year:      2022,
		MediaType: model.MediaTypeTV,
		Networks:  "Apple TV+",
	}
	if _, err := d.UpsertTitle(ctx, tv); err != nil {
		t.Fatalf("UpsertTitle tv: %v", err)
	}
	got, err := d.GetTitleByTmdbID(ctx, 111)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got == nil || got.Networks != "Apple TV+" {
		t.Fatalf("expected networks 'Apple TV+', got %+v", got)
	}

	// Re-upsert without networks must preserve the stored value.
	bare := &model.Title{
		TmdbID:    111,
		Title:     "Severance",
		Year:      2022,
		MediaType: model.MediaTypeTV,
	}
	if _, err := d.UpsertTitle(ctx, bare); err != nil {
		t.Fatalf("UpsertTitle bare: %v", err)
	}
	got, err = d.GetTitleByTmdbID(ctx, 111)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got == nil || got.Networks != "Apple TV+" {
		t.Fatalf("expected preserved networks 'Apple TV+', got %+v", got)
	}

	// New non-empty value overwrites.
	movie := &model.Title{
		TmdbID:    111,
		Title:     "Severance",
		Year:      2022,
		MediaType: model.MediaTypeTV,
		Networks:  "Netflix, HBO",
	}
	if _, err := d.UpsertTitle(ctx, movie); err != nil {
		t.Fatalf("UpsertTitle overwrite: %v", err)
	}
	got, err = d.GetTitleByTmdbID(ctx, 111)
	if err != nil {
		t.Fatalf("GetTitleByTmdbID: %v", err)
	}
	if got == nil || got.Networks != "Netflix, HBO" {
		t.Fatalf("expected networks 'Netflix, HBO', got %+v", got)
	}
}
