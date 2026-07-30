package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/quality"
)

func TestMergeReleases(t *testing.T) {
	r1 := quality.ParsedRelease{RawTitle: "Release A", InfoHash: "aaa", Guid: "guid-a"}
	r2 := quality.ParsedRelease{RawTitle: "Release B", InfoHash: "bbb", Guid: "guid-b"}
	r3 := quality.ParsedRelease{RawTitle: "Release C", InfoHash: "aaa", Guid: "guid-c"} // duplicate by infohash
	r4 := quality.ParsedRelease{RawTitle: "Release D", InfoHash: "", Guid: "guid-d"}

	t.Run("dedup by infohash", func(t *testing.T) {
		result := mergeReleases([]quality.ParsedRelease{r1, r2}, []quality.ParsedRelease{r3, r4})
		assert.Len(t, result, 3)
	})

	t.Run("empty first slice", func(t *testing.T) {
		result := mergeReleases(nil, []quality.ParsedRelease{r1})
		assert.Len(t, result, 1)
	})

	t.Run("empty second slice", func(t *testing.T) {
		result := mergeReleases([]quality.ParsedRelease{r1}, nil)
		assert.Len(t, result, 1)
	})

	t.Run("both empty", func(t *testing.T) {
		result := mergeReleases(nil, nil)
		assert.Empty(t, result)
	})

	t.Run("no infohash, dedup by guid", func(t *testing.T) {
		r5 := quality.ParsedRelease{RawTitle: "Release E", InfoHash: "", Guid: "guid-e"}
		r6 := quality.ParsedRelease{RawTitle: "Release F", InfoHash: "", Guid: "guid-e"} // duplicate by guid
		result := mergeReleases([]quality.ParsedRelease{r5}, []quality.ParsedRelease{r6})
		assert.Len(t, result, 1)
	})
}
