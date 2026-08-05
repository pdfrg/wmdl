package discover

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBookshopAnnotations(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "single paragraph annotation",
			html: `<div class="bulleted-lists list-rich-text text-sm lg:text-base"><p>A single paragraph blurb.</p></div>`,
			want: []string{"A single paragraph blurb."},
		},
		{
			name: "multi-paragraph annotation on one line",
			html: `<div class="bulleted-lists list-rich-text text-sm lg:text-base"><p><b>1940s Hong Kong</b> First part.</p><p><b>1960s San Francisco</b> Second part.</p><p>Final part.</p></div>`,
			want: []string{"1940s Hong Kong First part.\n\n1960s San Francisco Second part.\n\nFinal part."},
		},
		{
			name: "multi-paragraph annotation with newlines",
			html: `<div class="bulleted-lists list-rich-text text-sm lg:text-base">
<p><i>Opening quote.</i></p>

<p>After spending eight centuries, Anna finds peace.</p>

<p>Only one thing is certain.</p>
</div>`,
			want: []string{"Opening quote.\n\nAfter spending eight centuries, Anna finds peace.\n\nOnly one thing is certain."},
		},
		{
			name: "multiple annotation divs",
			html: `<div class="bulleted-lists list-rich-text text-sm lg:text-base"><p>Book one blurb.</p></div>
<div class="bulleted-lists list-rich-text text-sm lg:text-base"><p><b>Book two</b> blurb.</p></div>`,
			want: []string{"Book one blurb.", "Book two blurb."},
		},
		{
			name: "no annotations",
			html: `<div class="books">no descriptions here</div>`,
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseBookshopAnnotations(tt.html))
		})
	}
}
