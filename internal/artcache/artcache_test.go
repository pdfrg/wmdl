package artcache

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlbumArtPathDeterministic(t *testing.T) {
	// HOME-based cache dir so the test is hermetic; XDG_CACHE_HOME must not
	// leak in from the CI/dev environment.
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", dir)

	u := "https://cdn2.albumoftheyear.org/500x0/album/1944085-and-so-it-goes_122033.jpg"
	p, err := AlbumArtPath(u)
	require.NoError(t, err)

	want := fmt.Sprintf("%x", sha256.Sum256([]byte(u)))[:16]
	assert.Equal(t, filepath.Join(dir, ".cache/wmdl/posters/album_"+want+".png"), p)
	assert.DirExists(t, filepath.Join(dir, ".cache", "wmdl", "posters"))
}

func TestAlbumArtPathHonorsXDGCacheHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	p, err := AlbumArtPath("https://example.com/img.jpg")
	require.NoError(t, err)
	assert.Contains(t, p, filepath.Join(dir, "wmdl", "posters", "album_"))

	// Same key as the HOME-based derivation (cache path itself must be
	// deterministic across the two env setups).
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", home)
	p2, err := AlbumArtPath("https://example.com/img.jpg")
	require.NoError(t, err)
	assert.True(t, filepath.Base(p2) == filepath.Base(p))
}

func TestAlbumArtPathEmptyURL(t *testing.T) {
	_, err := AlbumArtPath("")
	assert.Error(t, err)
}

func TestAlbumArtPathPerm(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	_, err := AlbumArtPath("https://example.com/x.jpg")
	require.NoError(t, err)
	sw, sErr := os.Stat(filepath.Join(dir, "wmdl", "posters"))
	require.NoError(t, sErr)
	assert.Equal(t, os.FileMode(0700), sw.Mode().Perm())
}
