package process

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pdfrg/wmdl/internal/config"
	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
)

func (e *Executor) BookClientAvailable() bool {
	return e.bookClient != nil
}

func (e *Executor) HCClientAvailable() bool {
	return e.hc != nil
}

func (e *Executor) ProcessBookLibraryDecisions(ctx context.Context, events []db.EventWithBook, downloaded map[int64]*BookDownloadInfo) {
	mode := e.cfg.MediaTypeMode(model.MediaTypeBook)
	autoConfirm := mode == config.ProcessModeAuto || mode == config.ProcessModeYolo

	if e.bookClient == nil || e.SkipBook() {
		return
	}

	existingBooks, err := e.bookClient.GetAllBooks(ctx)
	llBooks := make(map[string]bool)
	if err == nil {
		for _, b := range existingBooks {
			llBooks[b.BookID] = true
		}
	}

	for _, evt := range events {
		if evt.Book.HardcoverID == 0 {
			e.log.Warn().Str("book", evt.Book.Title).Msg("no HardcoverID, skipping LazyLibrarian")
			continue
		}

		title := evt.Book.Title
		author := evt.Author.Name
		bookID := strconv.Itoa(int(evt.Book.HardcoverID))

		if llBooks[bookID] {
			info := downloaded[evt.Event.ID]
			if info == nil {
				continue
			}
			e.applyBookFormatDecisions(ctx, title, bookID, evt.Event.FormatPref, info)
			continue
		}

		if !autoConfirm {
			label := fmt.Sprintf("  Add \"%s\" by %s to LazyLibrarian?", title, author)
			if !PromptYesNo(ctx, label) {
				e.log.Info().Str("book", title).Msg("skipped LazyLibrarian (user declined)")
				continue
			}
		}

		if _, err := e.bookClient.AddBook(ctx, bookID); err != nil {
			e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian addBook failed (check LazyLibrarian server — likely a server-side lookup error)")
			continue
		}

		e.applyBookFormatDecisions(ctx, title, bookID, evt.Event.FormatPref, downloaded[evt.Event.ID])

		e.markBookDownloaded(ctx, evt)
		e.log.Info().Str("book", title).Str("ll_id", bookID).Msg("LazyLibrarian: added")
	}
}

func (e *Executor) applyBookFormatDecisions(ctx context.Context, title, bookID string, pref model.BookFormat, info *BookDownloadInfo) {
	ebookWanted := pref == model.BookFormatEbook || pref == model.BookFormatBoth
	audiobookWanted := pref == model.BookFormatAudiobook || pref == model.BookFormatBoth

	if ebookWanted {
		if info != nil && info.EbookDownloaded {
			if err := e.bookClient.UnqueueBook(ctx, bookID, model.BookFormatEbook); err != nil {
				e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian unqueueBook ebook")
			}
		} else {
			if err := e.bookClient.QueueBook(ctx, bookID, model.BookFormatEbook); err != nil {
				e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian queueBook ebook")
			}
		}
	}

	if audiobookWanted {
		if info != nil && info.AudiobookDownloaded {
			if err := e.bookClient.UnqueueBook(ctx, bookID, model.BookFormatAudiobook); err != nil {
				e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian unqueueBook audiobook")
			}
		} else {
			if err := e.bookClient.QueueBook(ctx, bookID, model.BookFormatAudiobook); err != nil {
				e.log.Warn().Err(err).Str("book", title).Msg("lazylibrarian queueBook audiobook")
			}
		}
	}
}

func (e *Executor) ComputeBookPhase3Candidates(ctx context.Context, events []db.EventWithBook) {
	if e.hc == nil || e.bookClient == nil || e.SkipBook() {
		return
	}
	seenSeries := make(map[string]bool)
	for _, evt := range events {
		if evt.Book.SeriesID == "" || evt.Book.HardcoverID == 0 {
			continue
		}
		if seenSeries[evt.Book.SeriesID] {
			continue
		}
		seenSeries[evt.Book.SeriesID] = true

		seriesID, err := strconv.Atoi(evt.Book.SeriesID)
		if err != nil {
			continue
		}

		hcBooks, err := e.hc.GetSeriesBooks(ctx, seriesID)
		if err != nil {
			e.log.Warn().Err(err).Str("series", evt.Book.SeriesName).Msg("book phase 3: GetSeriesBooks failed")
			continue
		}
		if len(hcBooks) == 0 {
			continue
		}

		llMembers, err := e.bookClient.GetSeriesMembers(ctx, evt.Book.SeriesID)
		if err != nil {
			e.log.Warn().Err(err).Str("series", evt.Book.SeriesName).Msg("book phase 3: GetSeriesMembers failed, assuming none")
		}
		owned := make(map[string]bool)
		for _, m := range llMembers {
			owned[m.BookID] = true
		}

		var missing int
		for _, hcb := range hcBooks {
			hcIDStr := strconv.Itoa(hcb.HCID)
			if owned[hcIDStr] {
				continue
			}
			synthEvent := db.EventWithBook{
				Event: &model.BookReleaseEvent{
					FormatPref: model.BookFormatBoth,
				},
				Book: &model.Book{
					Title:       hcb.Title,
					HardcoverID: hcb.HCID,
					ISBN13:      hcb.ISBN13,
					ISBN10:      hcb.ISBN10,
					ASIN:        "",
					ReleaseYear: hcb.ReleaseYear,
				},
				Author: &model.Author{
					Name: hcb.Author,
				},
			}
			for _, format := range []model.BookFormat{model.BookFormatEbook, model.BookFormatAudiobook} {
				sr := e.SearchBook(ctx, synthEvent, format)
				if sr != nil && len(sr.Top) > 0 {
					e.BookPhase3SearchPhase = append(e.BookPhase3SearchPhase, sr)
					missing++
				}
			}
		}
		if missing > 0 {
			e.log.Info().Int("count", missing).Str("series", evt.Book.SeriesName).Msg("book phase 3 candidates found")
		}
	}
}

func (e *Executor) processPhase3BookBatchItem(ctx context.Context, item *BatchItem) {
	sr := item.BookResult
	if sr == nil || e.SkipBook() {
		return
	}
	ae := sr.Event
	hcID := strconv.Itoa(ae.Book.HardcoverID)
	if hcID == "0" || hcID == "" {
		return
	}

	var chosen []quality.ParsedBookRelease
	for _, sel := range item.Selected {
		for _, br := range sr.Top {
			if br.Guid == sel.Guid {
				chosen = append(chosen, br)
				break
			}
		}
	}
	if len(chosen) == 0 {
		return
	}

	category := e.cfg.Downloader.Categories.Ebooks
	if sr.Format == model.BookFormatAudiobook && e.cfg.Downloader.Categories.Audiobooks != "" {
		category = e.cfg.Downloader.Categories.Audiobooks
	}
	for _, br := range chosen {
		uri := br.DownloadURL
		if uri == "" {
			uri = br.MagnetURL
		}
		if uri != "" {
			if _, err := e.dl.AddTorrent(ctx, uri, download.WithCategory(category)); err != nil {
				e.log.Warn().Err(err).Str("book", ae.Book.Title).Msg("book phase 3: add to client failed")
			}
		}
	}

	if _, err := e.bookClient.AddBook(ctx, hcID); err != nil {
		e.log.Warn().Err(err).Str("book", ae.Book.Title).Msg("book phase 3: addBook failed (check LazyLibrarian server — likely a server-side lookup error)")
	} else {
		if err := e.bookClient.UnqueueBook(ctx, hcID, model.BookFormatEbook); err != nil {
			e.log.Warn().Err(err).Str("book", ae.Book.Title).Msg("book phase 3: unqueueBook ebook")
		}
		if err := e.bookClient.UnqueueBook(ctx, hcID, model.BookFormatAudiobook); err != nil {
			e.log.Warn().Err(err).Str("book", ae.Book.Title).Msg("book phase 3: unqueueBook audiobook")
		}
	}

	e.log.Info().Str("book", ae.Book.Title).Msg("book phase 3 item downloaded")
}

func buildBookQualityPrefs(cfg *config.Config, preferredIDs []int) quality.BookQualityPrefs {
	return quality.BookQualityPrefs{
		EbookFormatPriority:     cfg.Quality.Books.Ebooks.FormatPriority,
		AudiobookFormatPriority: cfg.Quality.Books.Audiobooks.FormatPriority,
		MinSeeders:              cfg.MinSeeders,
		PreferredGroups:         cfg.PreferredGroups,
		PreferredIndexerIDs:     preferredIDs,
	}
}

func buildQualityPrefs(cfg *config.Config, mediaType model.MediaType, preferredIDs []int) quality.QualityPrefs {
	var qc config.MediaQualityConfig
	if mediaType == model.MediaTypeTV {
		qc = cfg.Quality.TV
	} else {
		qc = cfg.Quality.Movies
	}

	res := 1080
	switch qc.Resolution {
	case "2160p", "4k", "uhd":
		res = 2160
	case "1080p":
		res = 1080
	case "720p":
		res = 720
	}

	return quality.QualityPrefs{
		TargetResolution:    res,
		PreferHDR:           qc.PreferHDR,
		SourcePriority:      qc.SourcePriority,
		CodecPriority:       qc.CodecPriority,
		PreferredGroups:     cfg.PreferredGroups,
		MinSeeders:          cfg.MinSeeders,
		PreferredIndexerIDs: preferredIDs,
	}
}
