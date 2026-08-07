package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestCountReviewStatus(t *testing.T) {
	tests := []struct {
		name        string
		events      []db.EventWithTitle
		albumEvents []db.EventWithAlbumRelease
		bookEvents  []db.EventWithBook
		approved    int
		decided     int
		hasPending  bool
	}{
		{
			name: "all types approved",
			events: []db.EventWithTitle{
				{Event: &model.ReleaseEvent{Status: model.StatusApproved}},
			},
			albumEvents: []db.EventWithAlbumRelease{
				{Event: &model.AlbumReleaseEvent{Status: model.StatusApproved}},
			},
			bookEvents: []db.EventWithBook{
				{Event: &model.BookReleaseEvent{Status: model.StatusApproved}},
			},
			approved:   3,
			decided:    3,
			hasPending: false,
		},
		{
			name: "pending albums after decided videos",
			events: []db.EventWithTitle{
				{Event: &model.ReleaseEvent{Status: model.StatusRejected}},
				{Event: &model.ReleaseEvent{Status: model.StatusApproved}},
			},
			albumEvents: []db.EventWithAlbumRelease{
				{Event: &model.AlbumReleaseEvent{Status: model.StatusPending}},
			},
			approved:   1,
			decided:    2,
			hasPending: true,
		},
		{
			name: "pending video with decided album and book",
			events: []db.EventWithTitle{
				{Event: &model.ReleaseEvent{Status: model.StatusPending}},
			},
			albumEvents: []db.EventWithAlbumRelease{
				{Event: &model.AlbumReleaseEvent{Status: model.StatusApproved}},
				{Event: &model.AlbumReleaseEvent{Status: model.StatusRejected}},
			},
			bookEvents: []db.EventWithBook{
				{Event: &model.BookReleaseEvent{Status: model.StatusDownloaded}},
			},
			approved:   1,
			decided:    3,
			hasPending: true,
		},
		{
			name: "all rejected",
			events: []db.EventWithTitle{
				{Event: &model.ReleaseEvent{Status: model.StatusRejected}},
			},
			albumEvents: []db.EventWithAlbumRelease{
				{Event: &model.AlbumReleaseEvent{Status: model.StatusRejected}},
			},
			bookEvents: []db.EventWithBook{
				{Event: &model.BookReleaseEvent{Status: model.StatusRejected}},
			},
			approved:   0,
			decided:    3,
			hasPending: false,
		},
		{
			name:       "empty",
			approved:   0,
			decided:    0,
			hasPending: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approved, decided, hasPending := countReviewStatus(tt.events, tt.albumEvents, tt.bookEvents)
			assert.Equal(t, tt.approved, approved)
			assert.Equal(t, tt.decided, decided)
			assert.Equal(t, tt.hasPending, hasPending)
		})
	}
}

func TestReviewReReviewPrompt(t *testing.T) {
	tests := []struct {
		name       string
		reviewed   bool
		hasPending bool
		want       bool
	}{
		{
			name:       "reviewed week with new pending items",
			reviewed:   true,
			hasPending: true,
			want:       false,
		},
		{
			name:       "reviewed week fully decided",
			reviewed:   true,
			hasPending: false,
			want:       true,
		},
		{
			name:       "unreviewed week with pending items",
			reviewed:   false,
			hasPending: true,
			want:       false,
		},
		{
			name:       "unreviewed week nothing pending",
			reviewed:   false,
			hasPending: false,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, reviewReReviewPrompt(tt.reviewed, tt.hasPending))
		})
	}
}
