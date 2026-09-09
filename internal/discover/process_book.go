package discover

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) processBookItem(ctx context.Context, item ScrapedItem, progYear, progWeek int) error {
	r.log.Info().Str("title", item.Title).Str("author", item.ArtistName).Msg("processing book item")

	var notesISBN string
	if item.Notes != "" {
		for _, part := range strings.Split(item.Notes, "|") {
			if strings.HasPrefix(part, "isbn=") {
				notesISBN = strings.TrimPrefix(part, "isbn=")
			} else if strings.HasPrefix(part, "ean=") && notesISBN == "" {
				notesISBN = strings.TrimPrefix(part, "ean=")
			}
		}
	}

	var hcResult *HCBookResult
	if r.hc != nil {
		hcResult = r.searchHardcoverWithFallback(ctx, item.Title, item.ArtistName, notesISBN)
		if hcResult == nil {
			r.log.Warn().Str("book", item.Title).Msg("hardcover search failed, falling back to Open Library")
		}
	}

	var olResult *OLBookResult
	if hcResult == nil || hcResult.ISBN13 == "" || (hcResult.ReleaseDate == "" && hcResult.ReleaseYear == 0) {
		var olErr error
		olResult, olErr = r.ol.SearchBook(ctx, item.Title, item.ArtistName)
		if olErr != nil {
			if notesISBN != "" {
				olResult, olErr = r.ol.SearchBook(ctx, item.Title, notesISBN)
			}
		}
		if olErr != nil {
			r.log.Warn().Err(olErr).Str("book", item.Title).Msg("openlibrary search failed, storing without enrichment")
		}
	}

	authorName := item.ArtistName
	authorOLID := ""
	authorBio := ""
	authorBorn := ""
	authorDied := ""
	authorImage := ""
	var hcAuthorID int

	if hcResult != nil && hcResult.Author != nil {
		authorName = hcResult.Author.Name
		authorOLID = hcResult.Author.OLID
		authorBio = hcResult.Author.Bio
		authorBorn = hcResult.Author.BornDate
		authorDied = hcResult.Author.DeathDate
		authorImage = hcResult.Author.ImageURL
		hcAuthorID = hcResult.Author.ID
	} else if olResult != nil && olResult.Author != nil {
		authorName = olResult.Author.Name
		authorOLID = olResult.Author.OLID

		if olResult.Author.OLID != "" {
			authorCtx, authorCancel := context.WithTimeout(ctx, 15*time.Second)
			defer authorCancel()
			olAuthor, olErr := r.ol.GetAuthor(authorCtx, olResult.Author.OLID)
			if olErr == nil && olAuthor != nil {
				authorBio = olAuthor.Bio
				authorBorn = olAuthor.BornDate
				authorDied = olAuthor.DeathDate
				authorImage = olAuthor.ImageURL
			}
		}
	}

	// Normalize the enriched author name: some sources (notably the Hardcover
	// API) return names with runs of whitespace (e.g. "Steve         Hawk").
	authorName = normalizeAuthorName(authorName)

	author := &model.Author{
		HardcoverID: hcAuthorID,
		OLID:        authorOLID,
		Name:        authorName,
		Bio:         authorBio,
		BornDate:    authorBorn,
		DeathDate:   authorDied,
		ImageURL:    authorImage,
	}

	isbn10, isbn13, asin := "", "", ""
	seriesID, seriesName := "", ""
	pages := 0
	audioSeconds := 0
	publisher := ""
	language := ""
	tags := ""
	literaryType := ""
	hcBookID := 0
	hcSlug := ""
	olWorkID := ""
	description := item.Overview
	imageURL := item.ImageURL
	releaseDate := item.ReleaseDate
	releaseYear := item.Year
	shelvingsCount := item.ShelvingsCount
	title := item.Title
	subtitle := ""

	if hcResult != nil {
		isbn10 = hcResult.ISBN10
		isbn13 = hcResult.ISBN13
		asin = hcResult.ASIN
		pages = hcResult.Pages
		seriesID = hcResult.SeriesID
		seriesName = hcResult.SeriesName
		audioSeconds = hcResult.AudioSeconds
		publisher = hcResult.Publisher
		language = hcResult.Language
		tags = strings.Join(hcResult.Tags, ", ")
		literaryType = hcResult.LiteraryType
		hcBookID = hcResult.ID
		hcSlug = hcResult.Slug
		olWorkID = hcResult.OLID
		if hcResult.Title != "" {
			title = hcResult.Title
		}
		subtitle = hcResult.Subtitle
		if hcResult.Description != "" {
			description = hcResult.Description
		}
		if hcResult.ImageURL != "" {
			if !strings.Contains(hcResult.ImageURL, "/external_data/") {
				imageURL = hcResult.ImageURL
			} else if imageURL == "" {
				imageURL = hcResult.ImageURL
			}
		}
		if !strings.Contains(item.Source, "bookshop") {
			if hcResult.ReleaseDate != "" {
				releaseDate = hcResult.ReleaseDate
			} else if olResult != nil && olResult.ReleaseDate != "" {
				releaseDate = olResult.ReleaseDate
			}
		}
		if hcResult.ReleaseYear > 0 {
			releaseYear = hcResult.ReleaseYear
		} else if olResult != nil && olResult.ReleaseYear > 0 {
			releaseYear = olResult.ReleaseYear
		}
	} else if olResult != nil {
		olWorkID = olResult.OLID
		isbn10 = olResult.ISBN10
		isbn13 = olResult.ISBN13
		if olResult.Title != "" {
			title = olResult.Title
		}
		subtitle = olResult.Subtitle
		if olResult.Description != "" {
			description = olResult.Description
		}
		if imageURL == "" && olResult.ImageURL != "" {
			imageURL = olResult.ImageURL
		}
		if olResult.ReleaseDate != "" {
			releaseDate = olResult.ReleaseDate
		}
		if olResult.ReleaseYear > 0 {
			releaseYear = olResult.ReleaseYear
		}
		tags = strings.Join(olResult.Subjects, ", ")
	}

	if isbn13 == "" && notesISBN != "" {
		if len(notesISBN) == 13 {
			isbn13 = notesISBN
		} else if len(notesISBN) == 10 {
			isbn10 = notesISBN
		}
	}

	description = cleanBookDescription(description)

	if releaseDate == "" && (item.Source == "bookshop" || strings.Contains(item.Source, "bookshop")) {
		if item.ReleaseDate != "" {
			for _, f := range []string{"January 2, 2006", "Jan 2, 2006"} {
				if t, err := time.Parse(f, item.ReleaseDate); err == nil {
					releaseDate = t.Format("2006-01-02")
					break
				}
			}
		}
	}

	rating := item.ImdbRating
	ratingsCount := item.RatingsCount

	if HasOverrideGenre(&r.cfg.MediaTypes.Books.Filter.ContentFilter, tags) {
		r.log.Info().Str("book", item.Title).Msg("override genre matched, skipping score filter")
	} else {
		minRating := r.cfg.MediaTypes.Books.Filter.MinRating
		minRatings := r.cfg.MediaTypes.Books.Filter.MinRatings

		if ratingsCount > 0 {
			if minRatings > 0 && ratingsCount < minRatings {
				r.log.Info().Str("book", item.Title).Int("ratings", ratingsCount).Int("min", minRatings).Msg("below min_ratings filter, skipping")
				return nil
			}
		}
		if rating > 0 {
			if minRating > 0 && rating < minRating {
				r.log.Info().Str("book", item.Title).Float64("rating", rating).Float64("min", minRating).Msg("below min_rating filter, skipping")
				return nil
			}
		}
	}

	imageURL = upgradeGoodreadsImage(imageURL)
	imageURL = upgradeBookmarksImage(imageURL)

	if releaseDate == "" {
		r.log.Info().Str("book", item.Title).Str("author", item.ArtistName).
			Msg("no release date available from enrichment, skipping")
		return nil
	}

	var t time.Time
	var parseErr error
	for _, f := range []string{"2006-01-02", "January 2, 2006", "Jan 2, 2006"} {
		t, parseErr = time.Parse(f, releaseDate)
		if parseErr == nil {
			break
		}
	}

	var storeYear, storeWeek int

	isBookshop := item.Source == "bookshop" || strings.Contains(item.Source, "bookshop")

	if isBookshop && parseErr == nil {
		storeYear, storeWeek = t.ISOWeek()
	} else if !isBookshop && parseErr == nil {
		scrapeYear, scrapeWeek := r.bookTargetWeekFrom(progYear, progWeek)
		scrapeEnd := tuesdayOfISOWeek(scrapeYear, scrapeWeek)
		scrapeStart := scrapeEnd.AddDate(0, 0, -6)
		if t.Before(scrapeStart) || t.After(scrapeEnd) {
			r.log.Info().Str("book", item.Title).Str("date", releaseDate).
				Str("window", fmt.Sprintf("%s – %s", scrapeStart.Format("Jan 2"), scrapeEnd.Format("Jan 2"))).
				Msg("not in target week, skipping")
			return nil
		}
	}

	filterBook := &model.Book{
		Language: language,
		Tags:     tags,
	}
	if result := FilterBook(&r.cfg.MediaTypes.Books.Filter.ContentFilter, filterBook); !result.Passed {
		r.log.Info().Str("book", title).Str("reason", result.Reason).Msg("content filter: skipping")
		return nil
	}

	err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
		authorID, err := r.db.UpsertAuthorTx(ctx, tx, author)
		if err != nil {
			return fmt.Errorf("saving author: %w", err)
		}

		book := &model.Book{
			AuthorID:       authorID,
			Title:          title,
			Subtitle:       subtitle,
			HardcoverID:    hcBookID,
			HardcoverSlug:  hcSlug,
			OLID:           olWorkID,
			ISBN10:         isbn10,
			ISBN13:         isbn13,
			ASIN:           asin,
			Pages:          pages,
			AudioSeconds:   audioSeconds,
			Description:    description,
			ReleaseDate:    releaseDate,
			ReleaseYear:    releaseYear,
			Rating:         rating,
			RatingsCount:   ratingsCount,
			ShelvingsCount: shelvingsCount,
			ImageURL:       imageURL,
			Language:       language,
			Publisher:      publisher,
			Tags:           tags,
			LiteraryType:   literaryType,
			SeriesID:       seriesID,
			SeriesName:     seriesName,
		}
		bookID, err := r.db.UpsertBookTx(ctx, tx, book)
		if err != nil {
			return fmt.Errorf("saving book: %w", err)
		}

		existing, err := r.db.GetLatestBookReleaseEventTx(ctx, tx, bookID)
		if err != nil {
			return fmt.Errorf("checking existing events: %w", err)
		}

		if existing != nil {
			switch existing.Status {
			case model.StatusPending:
				if existing.Source != item.Source {
					existingSrcSet := make(map[string]bool)
					for _, s := range strings.Split(existing.Source, ",") {
						existingSrcSet[strings.TrimSpace(s)] = true
					}
					needsMerge := false
					for _, s := range strings.Split(item.Source, ",") {
						if !existingSrcSet[strings.TrimSpace(s)] {
							needsMerge = true
							break
						}
					}
					if needsMerge {
						a := &ScrapedItem{Source: existing.Source, Notes: existing.Notes}
						b := &ScrapedItem{Source: item.Source, Notes: item.Notes}
						mergeBookItems(a, b)
						if err := r.db.UpdateBookReleaseEventSourceAndNotesTx(ctx, tx, existing.ID, a.Source, a.Notes); err != nil {
							return fmt.Errorf("updating event source: %w", err)
						}
					}
				}
				return nil
			case model.StatusApproved, model.StatusDownloaded:
				return nil
			case model.StatusRejected:
				return nil
			}
		}

		formatPref := model.BookFormat(r.cfg.MediaTypes.Books.DefaultFormat)

		evtYear, evtWeek := progYear, progWeek
		if isBookshop && storeYear > 0 {
			evtYear, evtWeek = r.bookshopStorageWeek(storeYear, storeWeek)
		}
		evt := &model.BookReleaseEvent{
			BookID:      bookID,
			Source:      item.Source,
			ReleaseDate: releaseDate,
			FormatPref:  formatPref,
			Notes:       item.Notes,
			Status:      model.StatusPending,
			ISOYear:     evtYear,
			ISOWeek:     evtWeek,
		}
		if _, err := r.db.CreateBookReleaseEventTx(ctx, tx, evt); err != nil {
			return fmt.Errorf("saving book release event: %w", err)
		}

		return nil
	})

	return err
}

// searchHardcoverWithFallback searches Hardcover for a book, retrying with
// progressively shorter title variants when the full scraped title misses.
// This matters because SearchBook requires the returned record's title to
// contain the whole query title, while scraped titles often append the
// subtitle (sometimes without any delimiter, e.g. slug-recovered bookmarks
// titles). A miss here means no Hardcover ID, which the book manager needs.
// A candidate is only accepted when its author matches (when known) and its
// title is compatible via sameBookTitle, so truncated queries cannot latch
// onto a different book.
func (r *Runner) searchHardcoverWithFallback(ctx context.Context, title, author, notesISBN string) *HCBookResult {
	queries := []string{title}
	if stripped := strings.TrimSpace(bookSubtitleRe.ReplaceAllString(title, "")); stripped != "" && stripped != title {
		queries = append(queries, stripped)
	}
	// Leading-word prefixes for concatenated subtitles with no delimiter.
	words := strings.Fields(title)
	for n := 6; n >= 4; n-- {
		if len(words) > n {
			queries = append(queries, strings.Join(words[:n], " "))
		}
	}
	if notesISBN != "" {
		queries = append(queries, title+" "+notesISBN)
	}
	for _, q := range queries {
		res, err := r.hc.SearchBook(ctx, q, author)
		if err != nil {
			r.log.Debug().Err(err).Str("query", q).Msg("hardcover search error, trying shorter title")
			continue
		}
		if res == nil || res.Title == "" {
			continue
		}
		if res.Author != nil && !sameBookAuthor(res.Author.Name, author) {
			r.log.Debug().Str("query", q).Str("got_author", res.Author.Name).Str("want_author", author).
				Msg("hardcover candidate author mismatch, trying shorter title")
			continue
		}
		if !sameBookTitle(res.Title, title) {
			r.log.Debug().Str("query", q).Str("got_title", res.Title).
				Msg("hardcover candidate title mismatch, trying shorter title")
			continue
		}
		if q != title {
			r.log.Info().Str("book", title).Str("query", q).Int("hardcover_id", res.ID).
				Msg("hardcover match via shortened title")
		}
		return res
	}
	return nil
}

func mergeBookItems(a, b *ScrapedItem) {
	aSrc := a.Source
	bSrc := b.Source

	seen := map[string]bool{}
	for _, s := range strings.Split(aSrc, ",") {
		seen[strings.TrimSpace(s)] = true
	}
	for _, s := range strings.Split(bSrc, ",") {
		s = strings.TrimSpace(s)
		if !seen[s] {
			if a.Source != "" {
				a.Source += ","
			}
			a.Source += s
			seen[s] = true
		}
	}

	var notesParts []string
	if a.Notes != "" {
		notesParts = append(notesParts, aSrc+":"+a.Notes)
	}
	if b.Notes != "" {
		notesParts = append(notesParts, bSrc+":"+b.Notes)
	}
	if len(notesParts) > 0 {
		a.Notes = strings.Join(notesParts, "||")
	}

	if b.ImdbRating > a.ImdbRating {
		a.ImdbRating = b.ImdbRating
	}
	if b.RatingsCount > a.RatingsCount {
		a.RatingsCount = b.RatingsCount
	}
	if b.ShelvingsCount > a.ShelvingsCount {
		a.ShelvingsCount = b.ShelvingsCount
	}

	if len(b.Overview) > len(a.Overview) {
		a.Overview = b.Overview
	}

	if a.ReleaseDate == "" && b.ReleaseDate != "" {
		a.ReleaseDate = b.ReleaseDate
	}

	if a.ImageURL == "" && b.ImageURL != "" {
		a.ImageURL = b.ImageURL
	}
}

func (r *Runner) deduplicateBookEvents(ctx context.Context) error {
	return r.db.Transaction(ctx, func(tx *sql.Tx) error {
		merged := 0

		isbnGroups, err := r.db.FindPendingBookEventsByISBNTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("finding duplicate book events by isbn: %w", err)
		}

		for isbn, entries := range isbnGroups {
			n := r.mergeBookEventGroup(ctx, tx, entries)
			merged += n
			if n > 0 {
				r.log.Info().Str("isbn", isbn).Int("merged", n).Msg("dedup: merged duplicate books by isbn13")
			}
		}

		hcGroups, err := r.db.FindPendingBookEventsByHardcoverIDTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("finding duplicate book events by hardcover_id: %w", err)
		}

		for hcID, entries := range hcGroups {
			n := r.mergeBookEventGroup(ctx, tx, entries)
			merged += n
			if n > 0 {
				r.log.Info().Int("hardcover_id", hcID).Int("merged", n).Msg("dedup: merged duplicate books by hardcover_id")
			}
		}

		// Fuzzy title pass: catches same-book editions with different ISBNs
		// and no Hardcover ID (e.g. US vs UK editions, subtitle formatting
		// differences). Only pending events from the same release week reach
		// this point, so a same-author leading-subsequence title match is
		// treated as the same book.
		titleEntries, err := r.db.FindPendingBookEventsForTitleMatchTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("finding pending book events for title match: %w", err)
		}
		for _, group := range groupBookEventsByTitle(titleEntries) {
			n := r.mergeBookEventGroup(ctx, tx, group)
			merged += n
			if n > 0 {
				r.log.Info().Int("merged", n).Msg("dedup: merged duplicate books by title match")
			}
		}

		if merged > 0 {
			r.log.Info().Int("events_merged", merged).Msg("book deduplication complete")
		}
		return nil
	})
}

// groupBookEventsByTitle clusters pending book events whose author matches
// and whose titles match via sameBookTitle. Events already sharing a book
// ID are skipped (nothing to merge). Each returned group spans at least two
// distinct books.
func groupBookEventsByTitle(entries []db.BookTitleEntry) [][]db.BookISBNEntry {
	// Cluster distinct book IDs by author+title match, then expand each
	// cluster to all of its events (a book may have several pending events).
	var clusters [][]int64
	assigned := make(map[int64]bool)
	for i := range entries {
		if assigned[entries[i].BookID] {
			continue
		}
		var cluster []int64
		for j := range entries {
			if assigned[entries[j].BookID] || entries[i].BookID == entries[j].BookID {
				continue
			}
			if !sameBookAuthor(entries[i].Author, entries[j].Author) {
				continue
			}
			if !sameBookTitle(entries[i].Title, entries[j].Title) {
				continue
			}
			if len(cluster) == 0 {
				cluster = append(cluster, entries[i].BookID)
				assigned[entries[i].BookID] = true
			}
			cluster = append(cluster, entries[j].BookID)
			assigned[entries[j].BookID] = true
		}
		if len(cluster) > 0 {
			clusters = append(clusters, cluster)
		}
	}
	var groups [][]db.BookISBNEntry
	for _, c := range clusters {
		wanted := make(map[int64]bool, len(c))
		for _, id := range c {
			wanted[id] = true
		}
		var group []db.BookISBNEntry
		for _, e := range entries {
			if wanted[e.BookID] {
				group = append(group, db.BookISBNEntry{BookID: e.BookID, EventID: e.EventID})
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func (r *Runner) mergeBookEventGroup(ctx context.Context, tx *sql.Tx, entries []db.BookISBNEntry) int {
	bookEvents := make(map[int64][]int64)
	bookIDs := make([]int64, 0, len(entries))
	for _, e := range entries {
		if _, ok := bookEvents[e.BookID]; !ok {
			bookIDs = append(bookIDs, e.BookID)
		}
		bookEvents[e.BookID] = append(bookEvents[e.BookID], e.EventID)
	}
	if len(bookIDs) < 2 {
		return 0
	}
	sort.Slice(bookIDs, func(i, j int) bool { return bookIDs[i] < bookIDs[j] })
	survivorBookID := bookIDs[0]

	merged := 0
	for _, dupBookID := range bookIDs[1:] {
		dupEventIDs := bookEvents[dupBookID]
		survivorEventIDs := bookEvents[survivorBookID]
		if len(survivorEventIDs) == 0 {
			continue
		}
		survivorEventID := survivorEventIDs[0]

		survivorEvent, err := r.db.GetLatestBookReleaseEventTx(ctx, tx, survivorBookID)
		if err != nil {
			r.log.Warn().Err(err).Int64("survivor_event", survivorEventID).Msg("dedup: fetch survivor event failed")
			continue
		}
		if survivorEvent == nil {
			continue
		}

		for _, dupEventID := range dupEventIDs {
			dupEvent, err := r.db.GetLatestBookReleaseEventTx(ctx, tx, dupBookID)
			if err != nil || dupEvent == nil {
				continue
			}

			a := &ScrapedItem{Source: survivorEvent.Source, Notes: survivorEvent.Notes}
			b := &ScrapedItem{Source: dupEvent.Source, Notes: dupEvent.Notes}
			mergeBookItems(a, b)
			survivorEvent.Source = a.Source
			survivorEvent.Notes = a.Notes

			if err := r.db.DeleteBookReleaseEventTx(ctx, tx, dupEventID); err != nil {
				r.log.Warn().Err(err).Int64("event", dupEventID).Msg("dedup: delete duplicate event failed")
			}
			merged++
		}

		if err := r.db.UpdateBookReleaseEventSourceAndNotesTx(ctx, tx, survivorEventID, survivorEvent.Source, survivorEvent.Notes); err != nil {
			r.log.Warn().Err(err).Int64("event", survivorEventID).Msg("dedup: update survivor event failed")
		}

		if err := r.db.DeleteBookTx(ctx, tx, dupBookID); err != nil {
			r.log.Warn().Err(err).Int64("book", dupBookID).Msg("dedup: delete duplicate book failed")
		}
	}
	return merged
}

func (r *Runner) bookTargetWeekFrom(year, week int) (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Books.LookbackWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}
	return addISOWeekOffset(year, week, timeshiftWeeks)
}

// bookshopStorageWeek maps a bookshop release-date week to the week its
// release events are stored under: the release week timeshifted back by the
// configured books lookback, which lands on the program week whose discover
// run would naturally cover these titles. Review of that program week picks
// them up ("future week pre-population").
func (r *Runner) bookshopStorageWeek(releaseYear, releaseWeek int) (int, int) {
	timeshiftWeeks := r.cfg.MediaTypes.Books.LookbackWeeks
	if timeshiftWeeks <= 0 {
		timeshiftWeeks = 1
	}
	return addISOWeekOffset(releaseYear, releaseWeek, -timeshiftWeeks)
}

var (
	htmlBlockEndRe = regexp.MustCompile(`(?i)</(?:p|div|li|h[1-6]|blockquote|ul|ol|section)>`)
	htmlBrRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagRe      = regexp.MustCompile(`<[^>]*>`)
	htmlInlineRe   = regexp.MustCompile("[ \t\u00a0]+")
	htmlLineSpace  = regexp.MustCompile("[ \t\u00a0]*\n[ \t\u00a0]*")
	htmlBlankRe    = regexp.MustCompile(`\n{3,}`)

	authorSpaceRe = regexp.MustCompile(`\s+`)
)

// normalizeAuthorName collapses runs of whitespace in an author name to a
// single space and trims surrounding whitespace. Enrichment sources
// (especially the Hardcover API) occasionally return names with embedded
// runs of spaces; storing them verbatim garbles review/process display and
// breaks exact-name author dedup.
func normalizeAuthorName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	return authorSpaceRe.ReplaceAllString(s, " ")
}

// cleanBookDescription normalizes a book description for display: block and
// break tags become paragraph breaks, remaining HTML tags are stripped,
// entities are decoded, and whitespace is collapsed.
func cleanBookDescription(s string) string {
	s = htmlBlockEndRe.ReplaceAllString(s, "\n\n")
	s = htmlBrRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = htmlInlineRe.ReplaceAllString(s, " ")
	s = htmlLineSpace.ReplaceAllString(s, "\n")
	s = htmlBlankRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func normalizeBookKey(s string) string {
	s = strings.ToLower(s)
	s = bookYearParenRe.ReplaceAllString(s, "")
	s = bookSubtitleRe.ReplaceAllString(s, "")
	// Fold hyphens/dashes to spaces so "wine-dark" and "wine dark" match.
	s = bookDashRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	t := norm.NFKD.String(s)
	var out strings.Builder
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(r)
	}
	// Collapse runs of whitespace left by dash folding.
	return strings.Join(strings.Fields(out.String()), " ")
}

// bookDashRe matches hyphen and dash variants folded to spaces in book keys.
var bookDashRe = regexp.MustCompile(`[-–—]`)

// minBookTitleMatchWords is the minimum word count for the shorter title in
// a leading-subsequence title match. This keeps trivial single-word titles
// (e.g. "Dune" vs "Dune Messiah") from matching.
const minBookTitleMatchWords = 3

// sameBookTitle reports whether two raw titles plausibly denote the same
// book: their normalized forms are equal, or one is a leading
// word-subsequence of the other. The latter covers subtitles concatenated
// without a colon delimiter (e.g. slug-recovered bookmarks titles like
// "Crossing The Wine Dark Sea Journeys Through Ancient Literature" vs
// bookshop's "Crossing the Wine-Dark Sea"). Callers must also match on
// author; the pipeline only compares books from the same release week.
func sameBookTitle(a, b string) bool {
	na, nb := normalizeBookKey(a), normalizeBookKey(b)
	if na == nb {
		return na != ""
	}
	wa, wb := strings.Fields(na), strings.Fields(nb)
	if len(wa) < minBookTitleMatchWords || len(wb) < minBookTitleMatchWords {
		return false
	}
	short, long := wa, wb
	if len(short) > len(long) {
		short, long = long, short
	}
	for i := range short {
		if short[i] != long[i] {
			return false
		}
	}
	return true
}

// sameBookAuthor reports whether two raw author names normalize equally.
func sameBookAuthor(a, b string) bool {
	na := normalizeBookKey(a)
	return na != "" && na == normalizeBookKey(b)
}

// dedupeBookItems merges duplicate book items by normalized author+title,
// merging sources/notes instead of discarding. Besides exact key matches it
// also merges leading-subsequence title variants (see sameBookTitle).
func dedupeBookItems(bookItems []ScrapedItem) []ScrapedItem {
	bookMap := make(map[string]*ScrapedItem)
	var keys []string
	for i := range bookItems {
		key := fmt.Sprintf("%s|%s", normalizeBookKey(bookItems[i].ArtistName), normalizeBookKey(bookItems[i].Title))
		if existing, ok := bookMap[key]; ok {
			mergeBookItems(existing, &bookItems[i])
			continue
		}
		var matched *ScrapedItem
		for _, k := range keys {
			if e := bookMap[k]; sameBookAuthor(e.ArtistName, bookItems[i].ArtistName) &&
				sameBookTitle(e.Title, bookItems[i].Title) {
				matched = e
				break
			}
		}
		if matched != nil {
			mergeBookItems(matched, &bookItems[i])
		} else {
			bookMap[key] = &bookItems[i]
			keys = append(keys, key)
		}
	}
	result := make([]ScrapedItem, 0, len(bookMap))
	for _, k := range keys {
		result = append(result, *bookMap[k])
	}
	return result
}

func upgradeGoodreadsImage(url string) string {
	if !strings.Contains(url, "m.media-amazon.com") && !strings.Contains(url, "gr-assets") {
		return url
	}
	if goodreadsSizeRe.MatchString(url) {
		return goodreadsSizeRe.ReplaceAllString(url, "._SX500_.")
	}
	if strings.HasSuffix(url, ".jpg") {
		return strings.Replace(url, ".jpg", "._SX500_.jpg", 1)
	}
	return url
}

func upgradeBookmarksImage(url string) string {
	if !strings.Contains(url, "s26162.pcdn.co") {
		return url
	}
	return bookmarksWpSizeRe.ReplaceAllString(url, "$2")
}
