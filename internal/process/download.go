package process

import (
	"context"
	"fmt"

	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
	"github.com/rs/zerolog"
)

// addReleaseToClient adds a selected release to the configured download client.
//
// For releases with a Prowlarr download URL the .torrent bytes are fetched here
// and pushed directly to the client (AddTorrentData). Fetching ourselves makes
// the add synchronous and confirmable — a torrent client's async URL fetch can
// silently fail when the source is slow or overloaded (the "added but nothing
// landed" failure mode). If the fetch fails and a magnet is available it is used
// as a fallback. Magnet-only releases use AddMagnet.
func addReleaseToClient(ctx context.Context, log zerolog.Logger, dl download.Client, prowl *search.ProwlarrClient, release quality.ParsedRelease, category string) (string, error) {
	if dl == nil {
		return "", fmt.Errorf("download client not configured")
	}
	if release.DownloadURL != "" {
		data, err := prowl.FetchTorrent(ctx, release.DownloadURL)
		if err != nil {
			if release.MagnetURL != "" {
				log.Warn().Err(err).Str("release", release.RawTitle).Msg("fetching torrent via Prowlarr failed, falling back to magnet")
				return dl.AddMagnet(ctx, release.MagnetURL, download.WithCategory(category))
			}
			return "", err
		}
		name := release.RawTitle
		if name == "" {
			name = "release.torrent"
		} else {
			name += ".torrent"
		}
		return dl.AddTorrentData(ctx, name, data, download.WithCategory(category))
	}
	if release.MagnetURL != "" {
		return dl.AddMagnet(ctx, release.MagnetURL, download.WithCategory(category))
	}
	return "", fmt.Errorf("release has no download URL or magnet URI")
}

// musicQuality derives a short quality label for a music release. Music
// releases usually carry no video resolution, so the codec (FLAC/MP3) parsed
// from the title is the meaningful signal.
func musicQuality(r quality.ParsedRelease) string {
	if r.Codec != "" {
		return r.Codec
	}
	if r.Resolution > 0 {
		return fmt.Sprintf("%dp", r.Resolution)
	}
	return ""
}
