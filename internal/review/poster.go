package review

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/disintegration/imaging"
	"github.com/dolmen-go/kittyimg"

	"github.com/pdfrg/wmdl/internal/model"

	_ "image/jpeg"
)

const (
	tmdbImageBase = "https://image.tmdb.org/t/p/w500"
	maxPosterSize = 5 << 20
	httpTimeout   = 30 * time.Second
)

type PosterMode int

const (
	PosterAuto PosterMode = iota
	PosterKitty
	PosterText
	PosterOff
)

func ParsePosterMode(s string) PosterMode {
	switch strings.ToLower(s) {
	case "kitty":
		return PosterKitty
	case "text":
		return PosterText
	case "off":
		return PosterOff
	default:
		return PosterAuto
	}
}

func getPosterImage(tl *model.Title) (image.Image, error) {
	if tl.PosterPath == "" || tl.TmdbID == 0 {
		return nil, fmt.Errorf("no poster data")
	}

	cacheDir, err := posterCacheDir()
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%d.png", tl.TmdbID))

	if img, err := loadCachedPoster(cachePath); err == nil {
		return img, nil
	}

	url := tmdbImageBase + tl.PosterPath
	img, err := fetchPoster(url)
	if err != nil {
		return nil, err
	}

	if err := savePosterCache(cachePath, img); err != nil {
		log.Warn().Err(err).Msg("failed to save poster cache")
	}
	return img, nil
}

func posterCacheDir() (string, error) {
	cacheBase := os.Getenv("XDG_CACHE_HOME")
	if cacheBase == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME not set")
		}
		cacheBase = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(cacheBase, "wmdl", "posters")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func loadCachedPoster(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	return img, err
}

func savePosterCache(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, img)
}

func fetchPoster(url string) (image.Image, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poster fetch returned %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxPosterSize)
	img, _, err := image.Decode(limited)
	if err != nil {
		return nil, fmt.Errorf("decoding poster: %w", err)
	}
	return img, nil
}

func buildPosterAPC(img image.Image) string {
	resized := resizePosterForDisplay(img, posterCols, fontW, fontH)

	var buf bytes.Buffer
	if err := kittyimg.Fprint(&buf, resized); err != nil {
		return ""
	}
	return buf.String()
}

func resizePosterForDisplay(img image.Image, targetCols, cellW, cellH int) image.Image {
	srcBounds := img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return img
	}

	targetPixW := targetCols * cellW
	targetPixH := int(float64(targetPixW) * float64(srcH) / float64(srcW))

	return imaging.Fit(img, targetPixW, targetPixH, imaging.Lanczos)
}

func buildPosterClear() string {
	return "\x1b_Ga=d\x1b\\"
}

func renderTextPlaceholder(cols, rows int) string {
	if cols < 10 {
		cols = 10
	}
	if rows < 4 {
		rows = 4
	}
	top := "┌" + strings.Repeat("─", cols-2) + "┐"
	bottom := "└" + strings.Repeat("─", cols-2) + "┘"
	blank := "│" + strings.Repeat(" ", cols-2) + "│"

	words := []string{"POSTER", "NOT", "AVAILABLE"}
	contentLines := rows - 2

	lines := []string{top}
	if contentLines <= len(words) {
		for i := 0; i < contentLines; i++ {
			lines = append(lines, "│"+centerText(words[i], cols-2)+"│")
		}
	} else {
		padBefore := (contentLines - len(words)) / 2
		padAfter := contentLines - len(words) - padBefore
		for i := 0; i < padBefore; i++ {
			lines = append(lines, blank)
		}
		for _, w := range words {
			lines = append(lines, "│"+centerText(w, cols-2)+"│")
		}
		for i := 0; i < padAfter; i++ {
			lines = append(lines, blank)
		}
	}
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

func centerText(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	pad := w - len(s)
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
