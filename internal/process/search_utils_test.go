package process

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pdfrg/wmdl/internal/quality"
)

func TestMergeReleases(t *testing.T) {
	r1 := quality.ParsedRelease{RawTitle: "Release A", InfoHash: "aaa", Guid: "guid-a"}
	r2 := quality.ParsedRelease{RawTitle: "Release B", InfoHash: "bbb", Guid: "guid-b"}
	r3 := quality.ParsedRelease{RawTitle: "Release C", InfoHash: "aaa", Guid: "guid-c"} // duplicate by infohash
	r4 := quality.ParsedRelease{RawTitle: "Release D", InfoHash: "", Guid: "guid-d"}

	t.Run("dedup by infohash", func(t *testing.T) {
		result := mergeReleases([]quality.ParsedRelease{r1, r2}, []quality.ParsedRelease{r3, r4}, nil)
		assert.Len(t, result, 3)
	})

	t.Run("empty first slice", func(t *testing.T) {
		result := mergeReleases(nil, []quality.ParsedRelease{r1}, nil)
		assert.Len(t, result, 1)
	})

	t.Run("empty second slice", func(t *testing.T) {
		result := mergeReleases([]quality.ParsedRelease{r1}, nil, nil)
		assert.Len(t, result, 1)
	})

	t.Run("both empty", func(t *testing.T) {
		result := mergeReleases(nil, nil, nil)
		assert.Empty(t, result)
	})

	t.Run("no infohash, dedup by guid", func(t *testing.T) {
		r5 := quality.ParsedRelease{RawTitle: "Release E", InfoHash: "", Guid: "guid-e"}
		r6 := quality.ParsedRelease{RawTitle: "Release F", InfoHash: "", Guid: "guid-e"} // duplicate by guid
		result := mergeReleases([]quality.ParsedRelease{r5}, []quality.ParsedRelease{r6}, nil)
		assert.Len(t, result, 1)
	})
}

func TestMergeReleasesPrefersTopRankedIndexer(t *testing.T) {
	preferred := []int{1, 2, 3}

	// Same release on trackers 3, 1, and 2 (out of config order on purpose).
	on3 := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 3, IndexerName: "T3", Guid: "g3"}
	on1 := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 1, IndexerName: "T1", Guid: "g1"}
	on2 := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 2, IndexerName: "T2", Guid: "g2"}

	result := mergeReleases([]quality.ParsedRelease{on3}, []quality.ParsedRelease{on1, on2}, preferred)
	require.Len(t, result, 1)
	assert.Equal(t, 1, result[0].IndexerID)
	assert.Equal(t, "T1", result[0].IndexerName)
	assert.Equal(t, 2, result[0].ExtraTrackerCount)
}

func TestMergeReleasesNonPreferredDuplicatesIgnored(t *testing.T) {
	preferred := []int{1}

	winner := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 1, IndexerName: "T1", Guid: "g1"}
	other := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 99, IndexerName: "Other", Guid: "g2"}

	result := mergeReleases([]quality.ParsedRelease{winner}, []quality.ParsedRelease{other}, preferred)
	require.Len(t, result, 1)
	assert.Equal(t, 1, result[0].IndexerID)
	assert.Equal(t, 0, result[0].ExtraTrackerCount)
}

func TestMergeReleasesPreferredCopyReplacesNonPreferred(t *testing.T) {
	preferred := []int{1}

	nonPref := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 99, IndexerName: "Other", Guid: "g1"}
	pref := quality.ParsedRelease{RawTitle: "Movie", InfoHash: "h", IndexerID: 1, IndexerName: "T1", Guid: "g2"}

	result := mergeReleases([]quality.ParsedRelease{nonPref}, []quality.ParsedRelease{pref}, preferred)
	require.Len(t, result, 1)
	assert.Equal(t, 1, result[0].IndexerID)
	assert.Equal(t, 0, result[0].ExtraTrackerCount)
}

func TestIndexerRank(t *testing.T) {
	assert.Equal(t, 0, indexerRank(1, []int{1, 2, 3}))
	assert.Equal(t, 1, indexerRank(2, []int{1, 2, 3}))
	assert.Equal(t, 2, indexerRank(3, []int{1, 2, 3}))
	assert.Equal(t, -1, indexerRank(9, []int{1, 2, 3}))
	assert.Equal(t, -1, indexerRank(1, nil))
}
