// Package artcache holds the on-disk album-art cache used by review and
// process. The discover scraper writes entries (through the headless browser,
// since AOTY's image CDN sits behind Cloudflare); the TUIs read entries and
// must stay network-free.
package artcache

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// AlbumArtPath returns the cache file path for an album art image URL,
// hashing the URL the same way for both readers and writers.
func AlbumArtPath(imageURL string) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("empty image URL")
	}
	cacheBase := os.Getenv("XDG_CACHE_HOME")
	if cacheBase == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME not set")
		}
		cacheBase = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(cacheBase, "wmdl", "posters")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating poster cache dir: %w", err)
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(imageURL)))[:16]
	return filepath.Join(dir, "album_"+key+".png"), nil
}
