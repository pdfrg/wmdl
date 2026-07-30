package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/download"
	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

type BookSearchResult struct {
	Event  db.EventWithBook
	Format model.BookFormat
	Top    []quality.ParsedBookRelease
	Error  error
}

type BookDownloadInfo struct {
	EbookDownloaded     bool
	AudiobookDownloaded bool
}

func bookSearchQueries(evt db.EventWithBook) []string {
	var queries []string
	seen := make(map[string]bool)

	add := func(q string) {
		q = strings.TrimSpace(q)
		if q != "" && !seen[q] {
			seen[q] = true
			queries = append(queries, q)
		}
	}

	add(evt.Book.ISBN13)
	add(evt.Book.ASIN)
	add(evt.Book.ISBN10)

	mainTitle := evt.Book.Title
	if idx := strings.Index(mainTitle, ": "); idx > 0 {
		mainTitle = strings.TrimSpace(mainTitle[:idx])
	}

	if evt.Author.Name != "" {
		add(fmt.Sprintf("%s %s", evt.Author.Name, mainTitle))
	}
	add(mainTitle)

	return queries
}

func (e *Executor) SearchBook(ctx context.Context, evt db.EventWithBook, format model.BookFormat) *BookSearchResult {
	label := string(format)
	e.log.Info().Str("book", evt.Book.Title).Str("author", evt.Author.Name).Str("format", label).Msg("searching book")

	queries := bookSearchQueries(evt)
	if len(queries) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	cat := search.CatBookEbook
	if format == model.BookFormatAudiobook {
		cat = search.CatAudioAudiobook
	}
	bookCatSets := [][]int{{cat}, {search.CatBook}}

	preferredID := e.prowl.PreferredIndexerID(cat)
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	if preferredID > 0 {
		name := e.prowl.GetIndexerName(ctx, preferredID)
		e.log.Info().Str("name", name).Int("id", preferredID).Msg("preferred indexer (books)")

		for _, cats := range bookCatSets {
			for i, q := range queries {
				e.log.Info().Msgf("[%d/%d] preferred (cats=%v): %s", i+1, numTiers, cats, q)
				results, err := e.prowl.Search(ctx, search.SearchParams{
					Query:      q,
					Type:       "search",
					IndexerID:  preferredID,
					Limit:      50,
					Categories: cats,
				})
				if err != nil {
					e.log.Warn().Err(err).Str("book", evt.Book.Title).Msg("preferred indexer search failed")
					continue
				}
				exact, fuzzy := quality.PartitionBookReleasesRaw(results, evt.Author.Name, evt.Book.Title)
				exactPool = mergeReleases(exactPool, exact)
				fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
				e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
				if len(exact) > 0 {
					break
				}
				if len(exactPool) >= e.cfg.ShowTopN {
					return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format, preferredID)
				}
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	for _, cats := range bookCatSets {
		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] searching all (cats=%v): %s", i+1, numTiers, cats, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       "search",
				Limit:      50,
				Categories: cats,
			})
			if err != nil {
				e.log.Warn().Err(err).Str("book", evt.Book.Title).Msg("prowlarr book search failed")
				continue
			}
			exact, fuzzy := quality.PartitionBookReleasesRaw(results, evt.Author.Name, evt.Book.Title)
			exactPool = mergeReleases(exactPool, exact)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exact) > 0 {
				break
			}
			if len(exactPool) >= e.cfg.ShowTopN {
				return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format, preferredID)
			}
		}
	}

	return e.buildBookSearchResult(evt, exactPool, fuzzyPool, format, preferredID)
}

func (e *Executor) buildBookSearchResult(evt db.EventWithBook, exactPool, fuzzyPool []quality.ParsedRelease, format model.BookFormat, preferredID int) *BookSearchResult {
	label := string(format)
	if len(exactPool) == 0 && len(fuzzyPool) == 0 {
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	parseBookResults := func(releases []quality.ParsedRelease) []quality.ParsedBookRelease {
		var result []quality.ParsedBookRelease
		for _, pr := range releases {
			br := quality.ParseBookRelease(pr.RawTitle)
			br.ParsedRelease = pr
			result = append(result, br)
		}
		return result
	}

	exactBooks := parseBookResults(exactPool)
	fuzzyBooks := parseBookResults(fuzzyPool)

	switch format {
	case model.BookFormatEbook:
		exactBooks = filterEbookResults(exactBooks)
		fuzzyBooks = filterEbookResults(fuzzyBooks)
	case model.BookFormatAudiobook:
		exactBooks = filterAudiobookResults(exactBooks)
		fuzzyBooks = filterAudiobookResults(fuzzyBooks)
	}

	if len(exactBooks) == 0 && len(fuzzyBooks) == 0 {
		e.log.Info().Str("book", evt.Book.Title).Str("format", label).Msg("no results match format filter")
		e.Unfound = append(e.Unfound, fmt.Sprintf("%s by %s [%s]", evt.Book.Title, evt.Author.Name, label))
		return &BookSearchResult{Event: evt, Format: format}
	}

	prefs := buildBookQualityPrefs(e.cfg, preferredID)
	showTopN := e.cfg.ShowTopN

	var top []quality.ParsedBookRelease
	if len(exactBooks) > 0 {
		top = quality.SortBookTop(exactBooks, prefs, showTopN)
		if n := showTopN - len(top); n > 0 && len(fuzzyBooks) > 0 {
			fuzzyTop := quality.SortBookTop(fuzzyBooks, prefs, n)
			top = append(top, fuzzyTop...)
		}
	} else if len(fuzzyBooks) > 0 {
		top = quality.SortBookTop(fuzzyBooks, prefs, showTopN)
	}

	return &BookSearchResult{Event: evt, Format: format, Top: top}
}

func filterEbookResults(books []quality.ParsedBookRelease) []quality.ParsedBookRelease {
	var out []quality.ParsedBookRelease
	for _, b := range books {
		if b.IsEbook && b.EbookFormat != "" {
			out = append(out, b)
		} else if !b.IsAudiobook {
			b.IsEbook = true
			b.EbookFormat = "unknown"
			out = append(out, b)
		}
	}
	return out
}

func filterAudiobookResults(books []quality.ParsedBookRelease) []quality.ParsedBookRelease {
	var out []quality.ParsedBookRelease
	for _, b := range books {
		if b.IsAudiobook && b.AudiobookFormat != "" {
			out = append(out, b)
		} else if !b.IsEbook {
			b.IsAudiobook = true
			b.AudiobookFormat = "unknown"
			out = append(out, b)
		}
	}
	return out
}

func (e *Executor) presentBookPicker(ctx context.Context, sr *BookSearchResult) ([]quality.ParsedBookRelease, error) {
	if len(sr.Top) == 0 {
		return nil, nil
	}

	parsed := make([]quality.ParsedRelease, len(sr.Top))
	for i, br := range sr.Top {
		parsed[i] = br.ParsedRelease
	}

	pickerTitle := sr.Event.Book.Title
	if sr.Format != "" {
		pickerTitle = fmt.Sprintf("%s [%s]", sr.Event.Book.Title, sr.Format)
	}
	sel := NewSelector(pickerTitle, parsed)
	chosen, err := sel.Run()
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, nil
	}

	var result []quality.ParsedBookRelease
	for _, c := range chosen {
		for _, br := range sr.Top {
			if br.Guid == c.Guid {
				result = append(result, br)
				break
			}
		}
	}
	return result, nil
}

func (e *Executor) addBookToClient(ctx context.Context, evt db.EventWithBook, chosen []quality.ParsedBookRelease, format model.BookFormat) int {
	cat := e.cfg.Downloader.Categories.Ebooks
	if format == model.BookFormatAudiobook {
		cat = e.cfg.Downloader.Categories.Audiobooks
	}

	var added int
	for _, r := range chosen {
		url := r.DownloadURL
		if url == "" {
			url = r.MagnetURL
		}
		if url == "" {
			e.log.Warn().Str("title", r.RawTitle).Msg("book release has no download URL or magnet")
			continue
		}

		var tid string
		var dlErr error
		if strings.HasPrefix(url, "magnet:") {
			tid, dlErr = e.dl.AddMagnet(ctx, url, download.WithCategory(cat))
			if dlErr != nil {
				e.log.Warn().Err(dlErr).Str("title", r.RawTitle).Msg("adding book magnet")
				continue
			}
			e.log.Info().Str("title", r.RawTitle).Str("tid", tid).Str("category", cat).Msg("book magnet added")
		} else {
			tid, dlErr = e.dl.AddTorrent(ctx, url, download.WithCategory(cat))
			if dlErr != nil {
				e.log.Warn().Err(dlErr).Str("title", r.RawTitle).Msg("adding book torrent")
				continue
			}
			e.log.Info().Str("title", r.RawTitle).Str("tid", tid).Str("category", cat).Msg("book torrent added")
		}

		quality := r.EbookFormat
		if r.IsAudiobook && r.AudiobookFormat != "" {
			quality = r.AudiobookFormat
		}
		if quality == "" {
			quality = r.Codec
		}

		dl := &model.BookDownload{
			BookID:           evt.Book.ID,
			BookReleaseEvent: evt.Event.ID,
			Format:           format,
			Quality:          quality,
			SourceType:       r.Source,
			Codec:            r.Codec,
			InfoHash:         r.InfoHash,
			Category:         cat,
			ClientTorrentID:  tid,
			Status:           model.DownloadAdded,
		}
		if _, err := e.db.CreateBookDownload(ctx, dl); err != nil {
			e.log.Warn().Err(err).Msg("saving book download record")
		}
		added++
	}
	return added
}

func (e *Executor) markBookDownloaded(ctx context.Context, evt db.EventWithBook) {
	if err := e.db.UpdateBookReleaseEventStatus(ctx, evt.Event.ID, model.StatusDownloaded); err != nil {
		e.log.Warn().Err(err).Msg("marking book as downloaded")
	}
}

func (e *Executor) PickBook(ctx context.Context, sr *BookSearchResult) (int, error) {
	if len(sr.Top) == 0 {
		return 0, nil
	}
	chosen, err := e.presentBookPicker(ctx, sr)
	if err != nil {
		return 0, err
	}
	if len(chosen) == 0 {
		label := fmt.Sprintf("%s by %s [%s]", sr.Event.Book.Title, sr.Event.Author.Name, sr.Format)
		e.Skipped = append(e.Skipped, label)
		return 0, nil
	}
	return e.addBookToClient(ctx, sr.Event, chosen, sr.Format), nil
}

func (e *Executor) SearchBooksAll(ctx context.Context, events []db.EventWithBook) []*BookSearchResult {
	e.log.Info().Msgf("Searching %d book(s)...", len(events))
	var results []*BookSearchResult
	for i, evt := range events {
		e.log.Info().Str("book", evt.Book.Title).Str("author", evt.Author.Name).Msgf("[%d/%d] searching", i+1, len(events))
		pref := evt.Event.FormatPref
		ebookDone := evt.Event.EbookProcessed
		audiobookDone := evt.Event.AudiobookProcessed
		needsEbook := (pref == model.BookFormatEbook || pref == model.BookFormatBoth) && !ebookDone
		needsAudiobook := (pref == model.BookFormatAudiobook || pref == model.BookFormatBoth) && !audiobookDone
		if needsEbook {
			results = append(results, e.SearchBook(ctx, evt, model.BookFormatEbook))
		}
		if needsAudiobook {
			results = append(results, e.SearchBook(ctx, evt, model.BookFormatAudiobook))
		}
	}
	return results
}

func (e *Executor) PickBookResults(ctx context.Context, results []*BookSearchResult) map[int64]*BookDownloadInfo {
	done := make(map[int64]*BookDownloadInfo)
	events := make(map[int64]db.EventWithBook)

	for _, sr := range results {
		n, err := e.PickBook(ctx, sr)
		if errors.Is(err, ErrAbort) {
			e.log.Info().Msg("pipeline aborted by user")
			break
		}
		if err != nil {
			e.log.Warn().Err(err).Str("book", sr.Event.Book.Title).Msg("book picker error")
			continue
		}
		if n > 0 {
			if err := e.db.MarkBookFormatProcessed(ctx, sr.Event.Event.ID, sr.Format); err != nil {
				e.log.Warn().Err(err).Msg("marking book format processed")
			}
			id := sr.Event.Event.ID
			if _, ok := done[id]; !ok {
				done[id] = &BookDownloadInfo{
					EbookDownloaded:     sr.Event.Event.EbookProcessed,
					AudiobookDownloaded: sr.Event.Event.AudiobookProcessed,
				}
				events[id] = sr.Event
			}
			switch sr.Format {
			case model.BookFormatEbook:
				done[id].EbookDownloaded = true
			case model.BookFormatAudiobook:
				done[id].AudiobookDownloaded = true
			}
		}
	}

	for id, info := range done {
		pref := events[id].Event.FormatPref
		allDone := false
		switch pref {
		case model.BookFormatEbook:
			allDone = info.EbookDownloaded
		case model.BookFormatAudiobook:
			allDone = info.AudiobookDownloaded
		case model.BookFormatBoth:
			allDone = info.EbookDownloaded && info.AudiobookDownloaded
		}
		if allDone {
			e.markBookDownloaded(ctx, events[id])
		}
	}
	return done
}

func (e *Executor) ProcessBooks(ctx context.Context, events []db.EventWithBook) map[int64]*BookDownloadInfo {
	if len(events) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "\n── Book Processing ──")

	downloaded := make(map[int64]*BookDownloadInfo)

	for _, evt := range events {
		title := evt.Book.Title
		author := evt.Author.Name
		pref := evt.Event.FormatPref
		ebookDone := evt.Event.EbookProcessed
		audiobookDone := evt.Event.AudiobookProcessed

		needsEbook := (pref == model.BookFormatEbook || pref == model.BookFormatBoth) && !ebookDone
		needsAudiobook := (pref == model.BookFormatAudiobook || pref == model.BookFormatBoth) && !audiobookDone

		if !needsEbook && !needsAudiobook {
			continue
		}

		info := &BookDownloadInfo{
			EbookDownloaded:     ebookDone,
			AudiobookDownloaded: audiobookDone,
		}

		if needsEbook {
			fmt.Fprintf(os.Stderr, "\n  Searching %s by %s [ebook]\n", title, author)
			sr := e.SearchBook(ctx, evt, model.BookFormatEbook)
			if len(sr.Top) > 0 {
				n, err := e.PickBook(ctx, sr)
				if errors.Is(err, ErrAbort) {
					e.log.Info().Msg("pipeline aborted by user")
					return nil
				}
				if err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("ebook picker error")
				} else if n > 0 {
					if err := e.db.MarkBookFormatProcessed(ctx, evt.Event.ID, model.BookFormatEbook); err != nil {
						e.log.Warn().Err(err).Msg("marking ebook processed")
					}
					info.EbookDownloaded = true
					fmt.Fprintf(os.Stderr, "  ✓ %s by %s [ebook] (%d added)\n", title, author, n)
				} else {
					fmt.Fprintf(os.Stderr, "  %s by %s [ebook]: skipped\n", title, author)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  %s by %s [ebook]: no results\n", title, author)
			}
		}

		if needsAudiobook {
			fmt.Fprintf(os.Stderr, "\n  Searching %s by %s [audiobook]\n", title, author)
			sr := e.SearchBook(ctx, evt, model.BookFormatAudiobook)
			if len(sr.Top) > 0 {
				n, err := e.PickBook(ctx, sr)
				if errors.Is(err, ErrAbort) {
					e.log.Info().Msg("pipeline aborted by user")
					return nil
				}
				if err != nil {
					e.log.Warn().Err(err).Str("book", title).Msg("audiobook picker error")
				} else if n > 0 {
					if err := e.db.MarkBookFormatProcessed(ctx, evt.Event.ID, model.BookFormatAudiobook); err != nil {
						e.log.Warn().Err(err).Msg("marking audiobook processed")
					}
					info.AudiobookDownloaded = true
					fmt.Fprintf(os.Stderr, "  ✓ %s by %s [audiobook] (%d added)\n", title, author, n)
				} else {
					fmt.Fprintf(os.Stderr, "  %s by %s [audiobook]: skipped\n", title, author)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  %s by %s [audiobook]: no results\n", title, author)
			}
		}

		allDone := false
		switch pref {
		case model.BookFormatEbook:
			allDone = info.EbookDownloaded
		case model.BookFormatAudiobook:
			allDone = info.AudiobookDownloaded
		case model.BookFormatBoth:
			allDone = info.EbookDownloaded && info.AudiobookDownloaded
		}
		if allDone {
			e.markBookDownloaded(ctx, evt)
		}
		downloaded[evt.Event.ID] = info
	}
	return downloaded
}

func (e *Executor) processBookBatchItem(ctx context.Context, item *BatchItem, bookInfo *map[int64]*BookDownloadInfo) {
	sr := item.BookResult
	if sr == nil {
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

	n := e.addBookToClient(ctx, sr.Event, chosen, sr.Format)
	if n > 0 {
		if err := e.db.MarkBookFormatProcessed(ctx, sr.Event.Event.ID, sr.Format); err != nil {
			e.log.Warn().Err(err).Msg("marking book format processed")
		}
		id := sr.Event.Event.ID
		if *bookInfo == nil {
			*bookInfo = make(map[int64]*BookDownloadInfo)
		}
		info, ok := (*bookInfo)[id]
		if !ok {
			info = &BookDownloadInfo{
				EbookDownloaded:     sr.Event.Event.EbookProcessed,
				AudiobookDownloaded: sr.Event.Event.AudiobookProcessed,
			}
			(*bookInfo)[id] = info
		}
		switch sr.Format {
		case model.BookFormatEbook:
			info.EbookDownloaded = true
		case model.BookFormatAudiobook:
			info.AudiobookDownloaded = true
		}
	}
}
