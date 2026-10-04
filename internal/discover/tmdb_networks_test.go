package discover

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustDetails(t *testing.T, payload string) *TMDBDetails {
	t.Helper()
	var d TMDBDetails
	require.NoError(t, json.Unmarshal([]byte(payload), &d))
	return &d
}

func TestNetworkNames(t *testing.T) {
	d := mustDetails(t, `{"name":"Severance","networks":[
		{"id":213,"name":"Apple TV+"}]}`)
	assert.Equal(t, "Apple TV+", d.NetworkNames())

	// Capped at 3.
	d = mustDetails(t, `{"name":"Co-production","networks":[
		{"id":1,"name":"BBC"},{"id":2,"name":"HBO"},
		{"id":3,"name":"Netflix"},{"id":4,"name":"AMC"}]}`)
	assert.Equal(t, "BBC, HBO, Netflix", d.NetworkNames())

	// Empty names skipped, missing networks -> "".
	d = mustDetails(t, `{"name":"No network"}`)
	assert.Equal(t, "", d.NetworkNames())
	d = mustDetails(t, `{"name":"Blank","networks":[{"id":9,"name":""}]}`)
	assert.Equal(t, "", d.NetworkNames())
}

func TestStudioNames(t *testing.T) {
	d := mustDetails(t, `{"title":"Marty Supreme","production_companies":[
		{"id":1,"name":"A24"}]}`)
	assert.Equal(t, "A24", d.StudioNames())

	// Capped at 3.
	d = mustDetails(t, `{"title":"Ensemble","production_companies":[
		{"id":1,"name":"A24"},{"id":2,"name":"Warner Bros."},
		{"id":3,"name":"Universal"},{"id":4,"name":"Paramount"}]}`)
	assert.Equal(t, "A24, Warner Bros., Universal", d.StudioNames())

	d = mustDetails(t, `{"title":"Indie"}`)
	assert.Equal(t, "", d.StudioNames())
}

func TestJoinTMDBNames(t *testing.T) {
	assert.Equal(t, "", joinTMDBNames(nil, 3))
	assert.Equal(t, "HBO", joinTMDBNames([]string{"HBO"}, 3))
	assert.Equal(t, "a, b, c", joinTMDBNames([]string{"a", "b", "c", "d"}, 3))
}
