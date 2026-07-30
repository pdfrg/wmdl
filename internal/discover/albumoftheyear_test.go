package discover

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/model"
)

func TestPassesMusicFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter config.MusicFilterConfig
		item   ScrapedItem
		want   bool
	}{
		{
			name:   "LP meets critic threshold",
			filter: config.MusicFilterConfig{MinCriticScore: 70, MinCriticReviews: 5},
			item:   ScrapedItem{AlbumType: model.AlbumTypeLP, AOTYCriticScore: 80, AOTYCriticCount: 10},
			want:   true,
		},
		{
			name:   "LP below critic threshold",
			filter: config.MusicFilterConfig{MinCriticScore: 70, MinCriticReviews: 5, MinUserScore: 100, MinUserRatings: 1000},
			item:   ScrapedItem{AlbumType: model.AlbumTypeLP, AOTYCriticScore: 60, AOTYCriticCount: 10},
			want:   false,
		},
		{
			name:   "EP meets user threshold",
			filter: config.MusicFilterConfig{MinUserScore: 75, MinUserRatings: 20, MinCriticScore: 100, MinCriticReviews: 1000},
			item:   ScrapedItem{AlbumType: model.AlbumTypeEP, AOTYUserScore: 80, AOTYUserCount: 50},
			want:   true,
		},
		{
			name:   "must hear included",
			filter: config.MusicFilterConfig{IncludeMustHear: true, MinCriticScore: 100, MinCriticReviews: 1000, MinUserScore: 100, MinUserRatings: 1000},
			item:   ScrapedItem{AlbumType: model.AlbumTypeLP, AOTYMustHear: true},
			want:   true,
		},
		{
			name:   "reissue excluded",
			filter: config.MusicFilterConfig{MinCriticScore: 100, MinCriticReviews: 1000, MinUserScore: 100, MinUserRatings: 1000},
			item:   ScrapedItem{AlbumType: model.AlbumTypeReissue},
			want:   false,
		},
		{
			name:   "live album meets critic score only (special type)",
			filter: config.MusicFilterConfig{MinCriticScore: 60, MinUserScore: 100},
			item:   ScrapedItem{AlbumType: model.AlbumTypeLive, AOTYCriticScore: 75},
			want:   true,
		},
		{
			name:   "soundtrack no threshold",
			filter: config.MusicFilterConfig{MinCriticScore: 100, MinCriticReviews: 1000, MinUserScore: 100, MinUserRatings: 1000},
			item:   ScrapedItem{AlbumType: model.AlbumTypeSoundtrack},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := passesMusicFilter(tt.filter, tt.item)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAOTYParseBlock(t *testing.T) {
	tests := []struct {
		name string
		html string
		want *ScrapedItem
	}{
		{
			name: "standard LP album block",
			html: `<div class="albumBlock" data-type="lp">
				<div class="artistTitle">Test Artist</div>
				<div class="albumTitle">Test Album</div>
				<div class="type">January 15</div>
				<div class="ratingRow">
					<div class="ratingText">critic</div>
					<div class="rating">85</div>
					<div class="ratingText">(12)</div>
				</div>
				<div class="ratingRow">
					<div class="ratingText">user</div>
					<div class="rating">78</div>
					<div class="ratingText">(45)</div>
				</div>
				<a class="albumBlock" href="/album/12345/"></a>
			</div>`,
			want: &ScrapedItem{
				Title:           "Test Album",
				ArtistName:      "Test Artist",
				AlbumType:       model.AlbumTypeLP,
				AOTYCriticScore: 85,
				AOTYCriticCount: 12,
				AOTYUserScore:   78,
				AOTYUserCount:   45,
			},
		},
		{
			name: "mixtape excluded",
			html: `<div class="albumBlock" data-type="mixtape">
				<div class="artistTitle">Artist</div>
				<div class="albumTitle">Mixtape</div>
				<div class="type">Feb 1</div>
			</div>`,
			want: nil,
		},
		{
			name: "reissue excluded",
			html: `<div class="albumBlock" data-type="reissue">
				<div class="artistTitle">Artist</div>
				<div class="albumTitle">Reissue</div>
				<div class="type">Mar 1</div>
			</div>`,
			want: nil,
		},
		{
			name: "must hear badge",
			html: `<div class="albumBlock" data-type="lp">
				<div class="artistTitle">Artist</div>
				<div class="albumTitle">Great Album</div>
				<div class="type">April 10</div>
				<div class="mustHear"></div>
				<a class="albumBlock" href="/album/999/"></a>
			</div>`,
			want: &ScrapedItem{
				Title:        "Great Album",
				ArtistName:   "Artist",
				AlbumType:    model.AlbumTypeLP,
				AOTYMustHear: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &AOTYProvider{}
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			assert.NoError(t, err)

			block := doc.Find("div.albumBlock")
			got := p.parseBlock(block)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.NotNil(t, got)
			assert.Equal(t, tt.want.Title, got.Title)
			assert.Equal(t, tt.want.ArtistName, got.ArtistName)
			assert.Equal(t, tt.want.AlbumType, got.AlbumType)
			assert.Equal(t, tt.want.AOTYCriticScore, got.AOTYCriticScore)
			assert.Equal(t, tt.want.AOTYCriticCount, got.AOTYCriticCount)
			assert.Equal(t, tt.want.AOTYUserScore, got.AOTYUserScore)
			assert.Equal(t, tt.want.AOTYUserCount, got.AOTYUserCount)
			assert.Equal(t, tt.want.AOTYMustHear, got.AOTYMustHear)
			if tt.name == "standard LP album block" {
				assert.Contains(t, got.AOTYURL, "/album/12345")
				assert.Contains(t, got.ReleaseDate, "-01-15", "release date should end with -01-15")
			}
		})
	}
}
