package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pdfrg/wmdl/internal/quality"
)

func TestComputeColWidths(t *testing.T) {
	releases := []quality.ParsedRelease{
		{Resolution: 1080, Source: "BluRay", Codec: "x265", Seeders: 100, SizeBytes: 5 << 30, ReleaseGroup: "GRP", IndexerName: "Indexer1"},
		{Resolution: 2160, HDR: true, Source: "WEB-DL", Codec: "x264", Seeders: 5, SizeBytes: 2 << 20, ReleaseGroup: "LongGroupName", IndexerName: "VeryLongIndexerName"},
	}

	cw := computeColWidths(releases)
	assert.Equal(t, 5, cw.resolution)    // "2160p" = 5 chars
	assert.Equal(t, 3, cw.hdr)           // "HDR"
	assert.Equal(t, 6, cw.source)        // "WEB-DL"
	assert.Equal(t, 4, cw.codec)         // "x265"
	assert.Equal(t, 3, cw.seeders)       // "100"
	assert.Equal(t, 6, cw.size)          // "5.0 GB" = 6 chars
	assert.Equal(t, 13, cw.releaseGroup) // "LongGroupName" = 13
	assert.Equal(t, 19, cw.indexerName)
}

func TestComputeColWidthsEmpty(t *testing.T) {
	cw := computeColWidths(nil)
	assert.Zero(t, cw)
}
