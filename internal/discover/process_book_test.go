package discover

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanBookDescription(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold and italic tags stripped",
			in:   "<b>1940s Hong Kong</b>\nWhen Japanese soldiers invade, she escapes.\n<i>Italic aside</i>",
			want: "1940s Hong Kong\nWhen Japanese soldiers invade, she escapes.\nItalic aside",
		},
		{
			name: "paragraphs become blank-line separated",
			in:   "<p>First paragraph.</p><p>Second paragraph.</p><p>Third.</p>",
			want: "First paragraph.\n\nSecond paragraph.\n\nThird.",
		},
		{
			name: "paragraphs with newlines preserved",
			in:   "<p>First paragraph.</p>\n<p>Second paragraph.</p>",
			want: "First paragraph.\n\nSecond paragraph.",
		},
		{
			name: "br tags become newlines",
			in:   "Line one<br>Line two<br/>Line three<br />",
			want: "Line one\nLine two\nLine three",
		},
		{
			name: "html entities decoded",
			in:   "Tom &amp; Jerry &amp; the &nbsp;case",
			want: "Tom & Jerry & the case",
		},
		{
			name: "curly quote entities decoded",
			in:   "&#8220;Quoted&#8221; text",
			want: "\u201cQuoted\u201d text",
		},
		{
			name: "realistic multi-paragraph blurb",
			in:   "<p><b>1960s San Francisco</b>\nMarigold has a knack for secrets.</p>\n\n<p>Her mother vanishes before her eyes.</p>",
			want: "1960s San Francisco\nMarigold has a knack for secrets.\n\nHer mother vanishes before her eyes.",
		},
		{
			name: "plain text unchanged",
			in:   "A simple description with no tags.",
			want: "A simple description with no tags.",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "whitespace trimmed",
			in:   "  \n\t Leading and trailing whitespace.  \n",
			want: "Leading and trailing whitespace.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanBookDescription(tt.in))
		})
	}
}

func TestNormalizeAuthorName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean name unchanged",
			in:   "Steve Hawk",
			want: "Steve Hawk",
		},
		{
			name: "runs of spaces collapsed",
			in:   "Steve                                         Hawk",
			want: "Steve Hawk",
		},
		{
			name: "double space collapsed",
			in:   "Emily  Jane",
			want: "Emily Jane",
		},
		{
			name: "tabs and newlines collapsed",
			in:   "Jon\t\tRonson\nSmith",
			want: "Jon Ronson Smith",
		},
		{
			name: "leading and trailing whitespace trimmed",
			in:   "  Steve Hawk  \n\t",
			want: "Steve Hawk",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "only whitespace",
			in:   "   \t\n  ",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeAuthorName(tt.in))
		})
	}
}
