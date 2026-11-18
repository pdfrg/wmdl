package discover

import (
	"testing"

	"github.com/pdfrg/wmdl/internal/testutil"
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

func TestParseBookshopAstroAnnotations(t *testing.T) {
	// Fixture captured from the live new-books page: bookshop.org renders the
	// list client-side, embedding each book's title and annotation blurb in an
	// <astro-island> props JSON attribute (HTML-escaped). The legacy
	// bulleted-lists div markup no longer appears on the page.
	fixture := testutil.LoadFixture(t, "testdata", "bookshop", "new_books_astro_island.htm")

	descs := parseBookshopAstroAnnotations(string(fixture))
	assert.Len(t, descs, 17)

	// Sample pairs: titles pair with their own annotation, not a positional
	// index that could misalign when one book lacks a blurb.
	assert.Contains(t, descs["Breaducation"], "Just when I thought everything had been said about bread making")
	assert.Contains(t, descs["Murmuration"], "character arcs pack emotional punches")
	assert.Contains(t, descs["On the Calculation of Volume (Book V)"], "Solvej Balle")

	// Descriptions are cleaned: no HTML tags remain.
	for title, d := range descs {
		assert.NotContains(t, d, "<p>", "title %s", title)
		assert.NotEmpty(t, d, "title %s", title)
	}
}

func TestParseBookshopAstroAnnotationsEmptyPage(t *testing.T) {
	assert.Empty(t, parseBookshopAstroAnnotations(`<html><body>no islands here</body></html>`))
	assert.Empty(t, parseBookshopAstroAnnotations(`<astro-island props="{&quot;region&quot;:[0,&quot;us&quot;]}"></astro-island>`))
}

func TestDecodeJSONString(t *testing.T) {
	assert.Equal(t, "plain", decodeJSONString("plain"))
	assert.Equal(t, `quote " and backslash \`, decodeJSONString(`quote \" and backslash \\`))
	// Invalid JSON body is returned unchanged.
	assert.Equal(t, "bad\\", decodeJSONString("bad\\"))
}
