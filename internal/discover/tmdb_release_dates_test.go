package discover

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustReleaseDates(t *testing.T, payload string) *TMDBReleaseDates {
	t.Helper()
	var rd TMDBReleaseDates
	require.NoError(t, json.Unmarshal([]byte(payload), &rd))
	return &rd
}

func TestFlixStreamingPlausible(t *testing.T) {
	// Spider-Man: Brand New Day as TMDB reported it: theatrical Jul 31,
	// digital Sep 29 / Nov 17. FlixPatrol claimed Jul 28 -> reject.
	spidey := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-07-31T00:00:00.000Z","type":3},
			{"release_date":"2026-09-29T00:00:00.000Z","type":4},
			{"release_date":"2026-11-17T00:00:00.000Z","type":5}]},
		{"iso_3166_1":"DE","release_dates":[
			{"release_date":"2026-07-30T00:00:00.000Z","type":3}]}]}`)
	assert.False(t, flixStreamingPlausible(spidey, "2026-07-28"), "theatrical-only at claim time")
	// Same title claimed after the digital release -> keep.
	assert.True(t, flixStreamingPlausible(spidey, "2026-09-29"), "digital day")
	assert.True(t, flixStreamingPlausible(spidey, "2026-10-05"), "after digital")

	// Heart of the Beast: theatrical only, no home release -> reject.
	beast := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-09-25T00:00:00.000Z","type":3}]}]}`)
	assert.False(t, flixStreamingPlausible(beast, "2026-09-23"))

	// Tony (A24): theatrical Aug 21, digital Sep 15, claimed Sep 15 -> keep.
	tony := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-08-21T00:00:00.000Z","type":3},
			{"release_date":"2026-09-15T00:00:00.000Z","type":4}]}]}`)
	assert.True(t, flixStreamingPlausible(tony, "2026-09-15"))

	// Grace window: digital 2 days after claim keeps, 10 days after rejects.
	early := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-09-10T00:00:00.000Z","type":3},
			{"release_date":"2026-09-12T00:00:00.000Z","type":4}]}]}`)
	assert.True(t, flixStreamingPlausible(early, "2026-09-10"), "digital within grace")
	late := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-09-10T00:00:00.000Z","type":3},
			{"release_date":"2026-09-20T00:00:00.000Z","type":4}]}]}`)
	assert.False(t, flixStreamingPlausible(late, "2026-09-10"), "digital beyond grace")

	// Fail-open cases: no TMDB data, empty results, bad claim date.
	assert.True(t, flixStreamingPlausible(nil, "2026-09-10"), "nil release dates")
	assert.True(t, flixStreamingPlausible(mustReleaseDates(t, `{"results":[]}`), "2026-09-10"), "empty results")
	assert.True(t, flixStreamingPlausible(spidey, "not-a-date"), "bad claim date")
	assert.True(t, flixStreamingPlausible(spidey, ""), "empty claim date")
	// Digital-only title (streaming original, no cinema record) keeps.
	orig := mustReleaseDates(t, `{"results":[
		{"iso_3166_1":"US","release_dates":[
			{"release_date":"2026-09-23T00:00:00.000Z","type":4}]}]}`)
	assert.True(t, flixStreamingPlausible(orig, "2026-09-23"))
}
