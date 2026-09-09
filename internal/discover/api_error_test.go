package discover

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractAPIMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "graphql errors array",
			body: `{"errors": [{"message": "The AniList API has been temporarily disabled due to severe stability issues.", "status": 403}]}`,
			want: "The AniList API has been temporarily disabled due to severe stability issues.",
		},
		{
			name: "generic message field",
			body: `{"status":504,"type":"BadResponseException","message":"Jikan failed to connect to MyAnimeList.","error":null}`,
			want: "Jikan failed to connect to MyAnimeList.",
		},
		{
			name: "error string field",
			body: `{"error": "upstream unavailable"}`,
			want: "upstream unavailable",
		},
		{
			name: "non-json body",
			body: `<html>Bad Gateway</html>`,
			want: "",
		},
		{
			name: "empty errors array",
			body: `{"errors": []}`,
			want: "",
		},
		{
			name: "null error ignored",
			body: `{"message": "", "error": null}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractAPIMessage([]byte(tc.body)))
		})
	}
}

func TestSanitizeFailureReason(t *testing.T) {
	// Pretty-printed JSON collapses to one line with no braces sprawl.
	raw := "phase A: anilist returned 403: {\n    \"errors\": [\n        {\n            \"message\": \"oops\"\n        }\n    ]"
	got := sanitizeFailureReason(raw, maxAPIFailureDetail)
	assert.NotContains(t, got, "\n")
	assert.Contains(t, got, "oops")

	// Long reasons truncate with an ellipsis marker.
	long := strings.Repeat("x", maxAPIFailureDetail+50)
	got = sanitizeFailureReason(long, maxAPIFailureDetail)
	assert.LessOrEqual(t, len([]rune(got)), maxAPIFailureDetail)
	assert.True(t, strings.HasSuffix(got, "…"))

	// Short reasons pass through untouched.
	assert.Equal(t, "browser unavailable", sanitizeFailureReason("browser unavailable", maxAPIFailureDetail))
}

func TestNewAPIStatusError(t *testing.T) {
	// The morning's AniList 403 becomes a one-liner carrying only the message.
	body := []byte("{\n    \"errors\": [\n        {\n            \"message\": \"The AniList API has been temporarily disabled due to severe stability issues.\",\n            \"status\": 403,\n            \"locations\": [{\"line\": 1, \"column\": 1}]\n        }\n    ]\n}")
	err := newAPIStatusError("anilist", 403, body)
	require.Error(t, err)
	assert.Equal(t,
		"anilist returned 403: The AniList API has been temporarily disabled due to severe stability issues.",
		err.Error())
	assert.NotContains(t, err.Error(), "\n")

	// Non-JSON bodies fall back to a collapsed snippet.
	err = newAPIStatusError("tenrai", 500, []byte("internal\nerror"))
	assert.Equal(t, "tenrai returned 500: internal error", err.Error())
}

func TestMarkScrapeFailuresSanitizes(t *testing.T) {
	r := &Runner{
		cfg: notificationTestConfig(),
		failedScrapers: map[string]string{
			"anilist": "phase A: anilist returned 403: {\n    \"errors\": [\n        {\"message\": \"down\"}\n    ]",
		},
	}
	sourceCounts := map[string]int{"anilist (completed)": 0, "anilist (airing)": 0}
	sourceNotes := make(map[string]string)

	r.markScrapeFailures(sourceCounts, sourceNotes)

	for _, label := range []string{"anilist (completed)", "anilist (airing)"} {
		got, ok := sourceNotes[label]
		require.True(t, ok, label)
		assert.NotContains(t, got, "\n", label)
	}
}
