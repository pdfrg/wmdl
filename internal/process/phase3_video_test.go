package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestPhase3MoviesToCandidates(t *testing.T) {
	movies := []Phase3Movie{
		{Title: "Movie 1", Year: 2020, TMDBID: 100, ProfileID: 1, RootPath: "/movies"},
		{Title: "Movie 2", Year: 2021, TMDBID: 101, ProfileID: 2, RootPath: "/movies2"},
	}

	candidates := phase3MoviesToCandidates(movies)
	assert.Len(t, candidates, 2)
	assert.Equal(t, "Movie 1", candidates[0].Title)
	assert.Equal(t, 2020, candidates[0].Year)
	assert.Equal(t, model.MediaTypeMovie, candidates[0].MediaType)
	assert.Equal(t, 100, candidates[0].TmdbID)
	assert.Equal(t, "Movie 2", candidates[1].Title)
}

func TestPhase3MoviesToCandidatesEmpty(t *testing.T) {
	assert.Empty(t, phase3MoviesToCandidates(nil))
}
