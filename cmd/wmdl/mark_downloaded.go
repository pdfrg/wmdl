package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func newMarkDownloadedCmd() *cobra.Command {
	var formatPref string

	cmd := &cobra.Command{
		Use:   "mark-downloaded [flags] <compound-id> [<compound-id> ...]",
		Short: "Manually mark approved items as downloaded",
		Long: `Mark approved items as downloaded when obtained through other means.

Compound IDs use the format shown in "wmdl status -v":
  release:ID   movie/TV/anime event
  book:ID      book event
  album:ID     album event

For books, use --format to specify which format(s) were downloaded when the
format preference is "both". If omitted and both formats remain, you will
be prompted interactively.`,
		Example: `  wmdl mark-downloaded release:12 book:42 album:7
  wmdl mark-downloaded book:42 --format ebook
  wmdl mark-downloaded release:12 book:99 --format both`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := dataDir()
			if err != nil {
				return err
			}
			database, err := db.Open(filepath.Join(dbPath, "wmdl.db"))
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer database.Close()

			ctx := cmd.Context()

			for _, arg := range args {
				prefix, idStr, ok := strings.Cut(arg, ":")
				if !ok {
					fmt.Fprintf(os.Stderr, "error: invalid compound ID %q (expected prefix:number)\n", arg)
					continue
				}
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: invalid number in %q: %v\n", arg, err)
					continue
				}

				switch prefix {
				case "release":
					if err := markReleaseDownloaded(ctx, database, id); err != nil {
						fmt.Fprintf(os.Stderr, "error: %v\n", err)
					}
				case "book":
					if err := markBookDownloaded(ctx, database, id, formatPref); err != nil {
						fmt.Fprintf(os.Stderr, "error: %v\n", err)
					}
				case "album":
					if err := markAlbumDownloaded(ctx, database, id); err != nil {
						fmt.Fprintf(os.Stderr, "error: %v\n", err)
					}
				default:
					fmt.Fprintf(os.Stderr, "error: unknown prefix %q in %q (expected release, book, or album)\n", prefix, arg)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&formatPref, "format", "", "book format to mark downloaded (ebook, audiobook, both)")
	return cmd
}

func markReleaseDownloaded(ctx context.Context, database *db.DB, id int64) error {
	evt, err := database.GetReleaseEventWithTitle(ctx, id)
	if err != nil {
		return fmt.Errorf("looking up release event %d: %w", id, err)
	}
	if evt == nil {
		return fmt.Errorf("release event %d not found", id)
	}
	if evt.Event.Status != model.StatusApproved {
		return fmt.Errorf("release event %d has status %q (expected approved)", id, evt.Event.Status)
	}

	if err := database.UpdateReleaseEventStatus(ctx, id, model.StatusDownloaded); err != nil {
		return fmt.Errorf("marking release event %d as downloaded: %w", id, err)
	}
	fmt.Fprintf(os.Stderr, "  marked release:%d (%s) as downloaded\n", id, evt.Title.Title)
	return nil
}

func markBookDownloaded(ctx context.Context, database *db.DB, id int64, formatPref string) error {
	evt, err := database.GetBookReleaseEventWithBook(ctx, id)
	if err != nil {
		return fmt.Errorf("looking up book release event %d: %w", id, err)
	}
	if evt == nil {
		return fmt.Errorf("book release event %d not found", id)
	}
	if evt.Event.Status != model.StatusApproved {
		return fmt.Errorf("book release event %d has status %q (expected approved)", id, evt.Event.Status)
	}

	ebookDone := evt.Event.EbookProcessed
	audiobookDone := evt.Event.AudiobookProcessed

	formats := resolveBookFormats(evt.Event.FormatPref, formatPref, ebookDone, audiobookDone, evt.Book.Title)
	if len(formats) == 0 {
		return nil // already resolved or skipped
	}

	for _, f := range formats {
		if err := database.MarkBookFormatProcessed(ctx, id, f); err != nil {
			return fmt.Errorf("marking book %d %s as processed: %w", id, f, err)
		}
		fmt.Fprintf(os.Stderr, "  marked book:%d (%s) [%s] as downloaded\n", id, evt.Book.Title, f)
	}

	// Check if all required formats are now done
	ebookDone = ebookDone || contains(formats, model.BookFormatEbook) || contains(formats, model.BookFormatBoth)
	audiobookDone = audiobookDone || contains(formats, model.BookFormatAudiobook) || contains(formats, model.BookFormatBoth)

	allDone := false
	switch evt.Event.FormatPref {
	case model.BookFormatEbook:
		allDone = ebookDone
	case model.BookFormatAudiobook:
		allDone = audiobookDone
	case model.BookFormatBoth:
		allDone = ebookDone && audiobookDone
	}

	if allDone {
		if err := database.UpdateBookReleaseEventStatus(ctx, id, model.StatusDownloaded); err != nil {
			return fmt.Errorf("updating book %d status to downloaded: %w", id, err)
		}
		fmt.Fprintf(os.Stderr, "  marked book:%d (%s) as fully downloaded\n", id, evt.Book.Title)
	} else {
		fmt.Fprintf(os.Stderr, "  book:%d (%s) still has remaining formats\n", id, evt.Book.Title)
	}
	return nil
}

func resolveBookFormats(formatPref model.BookFormat, flag string, ebookDone, audiobookDone bool, title string) []model.BookFormat {
	// If flag is given, use it directly
	if flag != "" {
		switch strings.ToLower(flag) {
		case "ebook":
			return []model.BookFormat{model.BookFormatEbook}
		case "audiobook":
			return []model.BookFormat{model.BookFormatAudiobook}
		case "both":
			return []model.BookFormat{model.BookFormatEbook, model.BookFormatAudiobook}
		default:
			fmt.Fprintf(os.Stderr, "error: unknown format %q (expected ebook, audiobook, or both)\n", flag)
			return nil
		}
	}

	// No flag — determine automatically
	switch formatPref {
	case model.BookFormatEbook:
		return []model.BookFormat{model.BookFormatEbook}
	case model.BookFormatAudiobook:
		return []model.BookFormat{model.BookFormatAudiobook}
	case model.BookFormatBoth:
		switch {
		case ebookDone && !audiobookDone:
			return []model.BookFormat{model.BookFormatAudiobook}
		case !ebookDone && audiobookDone:
			return []model.BookFormat{model.BookFormatEbook}
		case ebookDone && audiobookDone:
			fmt.Fprintf(os.Stderr, "  book already fully downloaded\n")
			return nil
		default:
			// Both remain — prompt
			return promptBookFormat(title)
		}
	}
	return nil
}

func promptBookFormat(title string) []model.BookFormat {
	fmt.Fprintf(os.Stderr, "  Which format(s) for \"%s\"? [ebook/audiobook/both]: ", title)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil
	}
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))
	switch input {
	case "ebook":
		return []model.BookFormat{model.BookFormatEbook}
	case "audiobook":
		return []model.BookFormat{model.BookFormatAudiobook}
	case "both":
		return []model.BookFormat{model.BookFormatEbook, model.BookFormatAudiobook}
	default:
		fmt.Fprintf(os.Stderr, "  unknown format %q, skipping\n", input)
		return nil
	}
}

func markAlbumDownloaded(ctx context.Context, database *db.DB, id int64) error {
	evt, err := database.GetAlbumReleaseEvent(ctx, id)
	if err != nil {
		return fmt.Errorf("looking up album release event %d: %w", id, err)
	}
	if evt == nil {
		return fmt.Errorf("album release event %d not found", id)
	}
	if evt.Event.Status != model.StatusApproved {
		return fmt.Errorf("album release event %d has status %q (expected approved)", id, evt.Event.Status)
	}

	if err := database.UpdateAlbumReleaseEventStatus(ctx, id, model.StatusDownloaded); err != nil {
		return fmt.Errorf("marking album %d as downloaded: %w", id, err)
	}
	fmt.Fprintf(os.Stderr, "  marked album:%d (%s) as downloaded\n", id, evt.Release.Title)
	return nil
}

func contains(formats []model.BookFormat, f model.BookFormat) bool {
	for _, v := range formats {
		if v == f {
			return true
		}
	}
	return false
}
