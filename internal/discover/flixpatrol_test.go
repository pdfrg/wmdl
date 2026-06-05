package discover

import (
	"testing"
)

func TestParseFlixRow_AnimeGenre(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    bool
		wantTv  bool
		wantMov bool
	}{
		{
			name:   "anime genre detected",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Animation</span></div></div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Anime</span></div></div></div></a></td></tr>`,
			want:   true,
			wantTv: true,
		},
		{
			name:   "no anime genre",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Drama</span></div></div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
		{
			name:   "animation but not anime",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Animation</span></div></div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
		{
			name:    "movie with anime genre",
			html:    `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Movie </div><div class="flex flex-wrap gap-x-1"><div>Movie</div><div>Netflix</div><div class="flex gap-x-1"><div class="flex gap-x-1 items-center"><span>Anime</span></div></div></div></a></td></tr>`,
			want:    true,
			wantMov: true,
		},
		{
			name:   "no genre tags at all",
			html:   `<tr class="table-group"><td></td><td><a href="/title/test/"><div class="group-hover:underline"> Test Show </div><div class="flex flex-wrap gap-x-1"><div>TV Show</div><div>Netflix</div></div></a></td></tr>`,
			want:   false,
			wantTv: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := parseFlixRow(tt.html)
			if item.hasAnimeGenre != tt.want {
				t.Errorf("hasAnimeGenre = %v, want %v", item.hasAnimeGenre, tt.want)
			}
			if tt.wantTv && item.MediaType != "tv" {
				t.Errorf("MediaType = %q, want %q", item.MediaType, "tv")
			}
			if tt.wantMov && item.MediaType != "movie" {
				t.Errorf("MediaType = %q, want %q", item.MediaType, "movie")
			}
		})
	}
}
