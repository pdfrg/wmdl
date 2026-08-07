package review

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/pdfrg/wmdl/internal/db"
	"github.com/pdfrg/wmdl/internal/model"
)

func (t *TUI) rightWidth() int {
	if t.width <= 0 {
		return 70
	}
	right := t.width - posterCols - 2
	if right < 20 {
		right = 20
	}
	return right
}

func (t *TUI) availableContentLines() int {
	reserved := 4
	if t.flashMsg != "" {
		reserved += 2
	}
	avail := t.height - reserved
	if avail < 5 {
		avail = 5
	}
	return avail
}

func (t *TUI) shouldPadForPoster() bool {
	switch t.posterMode {
	case PosterOff:
		return false
	case PosterText:
		return true
	default:
		return kittySupported
	}
}

func (t *TUI) buildReviewContent() string {
	rw := t.rightWidth()

	it := t.currentItem()
	if it == nil {
		if t.filter == model.MediaTypeAnime && t.prevAnimeWeek != "" {
			return fmt.Sprintf("No new anime items for W%d.\nPreviously-reviewed anime items are in %s.\nRun: wmdl review --week %s",
				t.week, t.prevAnimeWeek, t.prevAnimeWeek)
		}
		return "no items match the current filter"
	}

	var b strings.Builder

	if it.albumEvent != nil {
		if t.shouldPadForPoster() {
			posterBlock := t.buildPosterBlock()
			posterLines := strings.Split(posterBlock, "\n")
			musicContent := t.buildMusicContent(it.albumEvent, rw, t.availableContentLines())
			musicLines := strings.Split(musicContent, "\n")

			maxLines := len(posterLines)
			if len(musicLines) > maxLines {
				maxLines = len(musicLines)
			}

			for i := 0; i < maxLines; i++ {
				leftPart := ""
				if i < len(posterLines) {
					leftPart = posterLines[i] + " "
				} else {
					leftPart = strings.Repeat(" ", posterCols+1)
				}

				rightPart := ""
				if i < len(musicLines) {
					rightPart = musicLines[i]
				}

				b.WriteString(leftPart)
				b.WriteString(" ")
				b.WriteString(rightPart)
				if i < maxLines-1 {
					b.WriteString("\n")
				}
			}
		} else {
			b.WriteString(t.buildMusicContent(it.albumEvent, rw, t.availableContentLines()))
		}
		return b.String()
	}

	if it.bookEvent != nil {
		if t.shouldPadForPoster() {
			posterBlock := t.buildPosterBlock()
			posterLines := strings.Split(posterBlock, "\n")
			bookContent := t.buildBookContent(it.bookEvent, rw, t.availableContentLines())
			bookLines := strings.Split(bookContent, "\n")

			maxLines := len(posterLines)
			if len(bookLines) > maxLines {
				maxLines = len(bookLines)
			}

			for i := 0; i < maxLines; i++ {
				leftPart := ""
				if i < len(posterLines) {
					leftPart = posterLines[i] + " "
				} else {
					leftPart = strings.Repeat(" ", posterCols+1)
				}

				rightPart := ""
				if i < len(bookLines) {
					rightPart = bookLines[i]
				}

				b.WriteString(leftPart)
				b.WriteString(" ")
				b.WriteString(rightPart)
				if i < maxLines-1 {
					b.WriteString("\n")
				}
			}
		} else {
			b.WriteString(t.buildBookContent(it.bookEvent, rw, t.availableContentLines()))
		}
		return b.String()
	}

	tl := it.event.Title
	ev := it.event.Event

	if t.shouldPadForPoster() {
		posterBlock := t.buildPosterBlock()
		posterLines := strings.Split(posterBlock, "\n")
		rightContent := t.buildRightContent(tl, ev, rw, t.availableContentLines())
		rightLines := strings.Split(rightContent, "\n")

		maxLines := len(posterLines)
		if len(rightLines) > maxLines {
			maxLines = len(rightLines)
		}

		for i := 0; i < maxLines; i++ {
			leftPart := ""
			if i < len(posterLines) {
				leftPart = posterLines[i] + " "
			} else {
				leftPart = strings.Repeat(" ", posterCols+1)
			}

			rightPart := ""
			if i < len(rightLines) {
				rightPart = rightLines[i]
			}

			b.WriteString(leftPart)
			b.WriteString(" ")
			b.WriteString(rightPart)
			if i < maxLines-1 {
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(t.buildRightContent(tl, ev, rw, t.availableContentLines()))
	}

	return b.String()
}

// albumNotesURL returns the event notes when the source stores a direct URL
// there (e.g. rpcharts' Radio Paradise album page), else "".
func albumNotesURL(ev *model.AlbumReleaseEvent) string {
	if ev == nil {
		return ""
	}
	n := strings.TrimSpace(ev.Notes)
	if strings.HasPrefix(n, "http://") || strings.HasPrefix(n, "https://") {
		return n
	}
	return ""
}

// musicDateLabel formats the release date for an album's review detail.
// rpcharts dates are the tracker's first-seen date (not the commercial
// release date), so they get an explicit label; AllMusic only has a release
// month. All other sources show the raw date.
func musicDateLabel(source, date string) string {
	if date == "" {
		return ""
	}
	if source == "allmusic" {
		if t, err := time.Parse("2006-01-02", date); err == nil {
			return t.Format("January 2006")
		}
	}
	if source == "rpcharts" {
		return "first seen by rpcharts: " + date
	}
	return date
}

func (t *TUI) buildMusicContent(ae *db.EventWithAlbumRelease, rw int, maxLines int) string {
	var b strings.Builder

	rls := ae.Release
	ev := ae.Event

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decoration = " "
			decorationStyle = approvedStyle
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	avail := rw - 1
	if avail < 10 {
		avail = 10
	}

	title := rls.ArtistName + " - " + rls.Title
	if rls.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, rls.Year)
	}

	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title))

	b.WriteString("\n\n")

	isAllMusic := ev.Source == "allmusic"

	var tagParts []string
	tagParts = append(tagParts, fmt.Sprintf("[album] %s", string(rls.AlbumType)))
	if isAllMusic {
		tagParts = append(tagParts, "AllMusic Editor's Choice")
	} else if ev.Source != "" {
		tagParts = append(tagParts, ev.Source)
	}
	if label := musicDateLabel(ev.Source, rls.ReleaseDate); label != "" {
		tagParts = append(tagParts, label)
	}
	b.WriteString(tagStyle.Render(strings.Join(tagParts, " · ")))

	if rls.MBID == "" {
		b.WriteString("\n")
		b.WriteString(rejectedStyle.Render("⚠ No MusicBrainz match — may not add to Lidarr"))
	}

	if isAllMusic {
		b.WriteString("\n")
		b.WriteString(approvedStyle.Render("🏅 AllMusic Editor's Choice"))
	}

	b.WriteString("\n")

	var scoreParts []string
	if rls.AOTYCriticScore > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AOTY critic: %.0f (%d reviews)", rls.AOTYCriticScore, rls.AOTYCriticCount))
	}
	if rls.AOTYUserScore > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AOTY user: %.0f (%d ratings)", rls.AOTYUserScore, rls.AOTYUserCount))
	}
	if rls.AllMusicRating > 0 {
		scoreParts = append(scoreParts, fmt.Sprintf("AllMusic: %.0f/10", rls.AllMusicRating))
	}
	if len(scoreParts) > 0 {
		b.WriteString(ratingsLine.Render(strings.Join(scoreParts, " · ")))
	}

	if len(scoreParts) > 0 || rls.AOTYMustHear {
		b.WriteString("\n")
	}

	if rls.AOTYMustHear {
		b.WriteString(approvedStyle.Render("★ Must Hear (Editor's Pick)"))
	}

	if rls.Genres != "" {
		b.WriteString("\n")
		b.WriteString(rtStyle.Width(rw).Render("genres: " + rls.Genres))
	}

	if rls.Overview != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(rls.Overview)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	b.WriteString("\n")
	b.WriteString(ratingsLine.Render(fmt.Sprintf("MB artist rating: %s", fmtRating(rls.MBRating))))

	if u := albumNotesURL(ev); u != "" {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(u))
	}

	it = t.currentItem()
	if it != nil && it.albumEvent != nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}
	}

	return b.String()
}

func (t *TUI) buildBookContent(be *db.EventWithBook, rw int, maxLines int) string {
	var b strings.Builder

	book := be.Book
	author := be.Author
	ev := be.Event

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decorationStyle = approvedStyle
			switch ev.FormatPref {
			case model.BookFormatEbook:
				decoration = " "
			case model.BookFormatAudiobook:
				decoration = " "
			default:
				decoration = " "
			}
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	avail := rw - 1
	if avail < 10 {
		avail = 10
	}

	title := author.Name + " — " + book.Title
	if book.ReleaseYear > 0 {
		title = fmt.Sprintf("%s (%d)", title, book.ReleaseYear)
	}

	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title))

	b.WriteString("\n\n")

	var tagParts []string
	tagParts = append(tagParts, "[book]")
	tagParts = append(tagParts, string(ev.FormatPref))
	if ev.Source != "" {
		sources := strings.Split(ev.Source, ",")
		tagParts = append(tagParts, strings.Join(sources, " + "))
	}
	b.WriteString(tagStyle.Render(strings.Join(tagParts, " · ")))

	if book.Rating > 0 {
		b.WriteString("\n")
		var scoreParts []string
		scoreParts = append(scoreParts, fmt.Sprintf("Rating: %.1f", book.Rating))
		if book.RatingsCount > 0 {
			scoreParts = append(scoreParts, fmt.Sprintf("%d ratings", book.RatingsCount))
		}
		if book.ShelvingsCount > 0 {
			scoreParts = append(scoreParts, fmtShelvings(book.ShelvingsCount)+" shelvings")
		}
		b.WriteString(ratingsLine.Render(strings.Join(scoreParts, " · ")))
	}

	if strings.Contains(ev.Source, "bookmarks") {
		verdict, total := parseBmVerdict(ev.Notes)
		if verdict != "" {
			b.WriteString("\n")
			display := "Book Marks: " + verdict
			if total > 0 {
				display += fmt.Sprintf(" — %d reviews", total)
			}
			b.WriteString(ratingsLine.Render(display))
		}
	}

	if strings.Contains(ev.Source, "goodreads_blog") {
		var badge string
		if strings.Contains(ev.Notes, ":weekly|") {
			badge = "Readers' Pick"
		} else if strings.Contains(ev.Notes, ":editors|") {
			badge = "Editors' Pick"
		}
		if badge != "" {
			b.WriteString("\n")
			b.WriteString(ratingsLine.Render(badge))
		}
	}

	var detailParts []string
	if book.ReleaseDate != "" {
		detailParts = append(detailParts, book.ReleaseDate)
	}
	if book.Publisher != "" {
		detailParts = append(detailParts, book.Publisher)
	}
	if book.Language != "" {
		detailParts = append(detailParts, book.Language)
	}
	if book.Pages > 0 {
		detailParts = append(detailParts, fmt.Sprintf("%d pages", book.Pages))
	}
	if book.AudioSeconds > 0 {
		h := book.AudioSeconds / 3600
		m := (book.AudioSeconds % 3600) / 60
		if h > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%dh %dm", h, m))
		} else {
			detailParts = append(detailParts, fmt.Sprintf("%dm", m))
		}
	}
	if len(detailParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(detailParts, " · ")))
	}

	var idParts []string
	if book.ISBN13 != "" {
		idParts = append(idParts, "ISBN: "+book.ISBN13)
	}
	if book.ASIN != "" {
		idParts = append(idParts, "ASIN: "+book.ASIN)
	}
	if len(idParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(idParts, " · ")))
	}

	var metaParts []string
	if book.LiteraryType != "" {
		metaParts = append(metaParts, book.LiteraryType)
	}
	if book.Tags != "" {
		metaParts = append(metaParts, book.Tags)
	}
	if len(metaParts) > 0 {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(strings.Join(metaParts, " · ")))
	}

	it = t.currentItem()
	if it != nil && it.bookEvent != nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}

		if seriesStr := it.bookSeriesStr(t.libraryCache); seriesStr != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("  " + seriesStr))
		}
	}

	if book.Description != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(book.Description)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	return b.String()
}

func (t *TUI) buildPosterBlock() string {
	ph := t.posterHeight()
	if !posterAvailable || t.posterMode == PosterText {
		return renderTextPlaceholder(posterCols, ph)
	}
	if t.posterImg == nil {
		return renderTextPlaceholder(posterCols, ph)
	}
	return strings.Repeat(" ", posterCols)
}

func (t *TUI) posterHeight() int {
	if fontW <= 0 || fontH <= 0 {
		return 16
	}
	aspect := 1.5
	if it := t.currentItem(); it != nil {
		if it.albumEvent != nil {
			aspect = 1.0
		}
	}
	h := int(float64(posterCols*fontW) * aspect / float64(fontH))
	if h < 10 {
		h = 10
	}
	return h
}

func (t *TUI) buildRightContent(tl *model.Title, ev *model.ReleaseEvent, rw int, maxLines int) string {
	var b strings.Builder

	title := tl.Title
	if tl.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, tl.Year)
	}
	tmdbNote := ""
	if tl.TmdbTitle != "" {
		scrapedClean := strings.TrimSpace(reviewMetaParen.ReplaceAllString(tl.Title, ""))
		tmdbClean := strings.TrimSpace(reviewMetaParen.ReplaceAllString(tl.TmdbTitle, ""))
		if !strings.EqualFold(scrapedClean, tmdbClean) {
			tmdbNote = fmt.Sprintf("TMDB: %s", tl.TmdbTitle)
		}
	}

	mediaType := "movie"
	switch tl.MediaType {
	case model.MediaTypeTV:
		mediaType = "tv"
	case model.MediaTypeAnime:
		mediaType = "anime"
	}

	releaseType := "physical"
	if ev.ReleaseType == model.ReleaseStreaming {
		releaseType = "streaming"
	}

	var notes []string
	if ev.Notes != "" {
		notes = append(notes, ev.Notes)
	} else if ev.PreviousStatus == model.StatusRejected {
		notes = append(notes, "previously rejected")
	} else if ev.PreviousStatus == model.StatusApproved {
		notes = append(notes, "previously approved")
	} else if ev.PreviousStatus == model.StatusDownloaded {
		notes = append(notes, "previously downloaded")
	}

	extra := ""
	if len(notes) > 0 {
		extra = " (" + strings.Join(notes, ", ") + ")"
	}

	decoration := " "
	decorationStyle := emptyStyle
	it := t.currentItem()
	if it != nil {
		switch it.decision {
		case decisionApproved:
			decoration = " "
			decorationStyle = approvedStyle
		case decisionRejected:
			decoration = " "
			decorationStyle = rejectedStyle
		}
	}

	avail := rw - 1
	if avail < 10 {
		avail = 10
	}
	b.WriteString(" ")
	b.WriteString(decorationStyle.Render(decoration))
	b.WriteString(titleStyle.Width(avail - 2).Render(title + extra))

	b.WriteString("\n\n")
	b.WriteString(tagStyle.Render(fmt.Sprintf("[%s] %s", mediaType, releaseType)))

	if tmdbNote != "" {
		b.WriteString("\n")
		b.WriteString(rtStyle.Render(tmdbNote))
	}

	if hasFirstMeta(tl) {
		var parts []string
		if tl.OriginCountry != "" {
			for _, c := range strings.Split(tl.OriginCountry, ",") {
				if flag := countryFlag(c); flag != "" {
					parts = append(parts, flag)
				}
			}
		}
		if lang := languageName(tl.OriginalLanguage); lang != "" {
			parts = append(parts, lang)
		}
		if tl.USRating != "" {
			parts = append(parts, "["+tl.USRating+"]")
		}
		if tl.Genres != "" {
			parts = append(parts, tl.Genres)
		}
		if tl.Runtime > 0 {
			parts = append(parts, fmtRuntime(tl.Runtime))
		}
		b.WriteString("\n")
		b.WriteString(strings.Join(parts, " · "))
	}

	if tl.MediaType == model.MediaTypeAnime {
		var animeParts []string
		if tl.AnimeType != "" {
			animeParts = append(animeParts, tl.AnimeType)
		}
		if tl.AnimeStatus != "" {
			animeParts = append(animeParts, tl.AnimeStatus)
		}
		if tl.AnimeSource != "" {
			animeParts = append(animeParts, tl.AnimeSource)
		}
		if tl.AnimeStudio != "" {
			animeParts = append(animeParts, tl.AnimeStudio)
		}
		if tl.AnimeEpisodes > 0 {
			animeParts = append(animeParts, fmt.Sprintf("%d eps", tl.AnimeEpisodes))
		}
		if len(animeParts) > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Join(animeParts, " · "))
		}

		if tl.Themes != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Themes: " + tl.Themes))
		}
		if tl.Demographics != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Demographics: " + tl.Demographics))
		}
		if tl.Streaming != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Streaming: " + tl.Streaming))
		}
	}

	if strings.HasPrefix(ev.Source, "jikan-airing") || strings.HasPrefix(ev.Source, "anilist-airing") || strings.HasPrefix(ev.Source, "tenrai-airing") {
		b.WriteString("\n\n")
		b.WriteString(warnStyle.Render("⏳ Currently Airing"))
		if strings.Contains(ev.Notes, "end=") {
			endDate := extractEndDate(ev.Notes)
			if endDate != "" {
				b.WriteString(rtStyle.Render(fmt.Sprintf(" — Final episode %s", endDate)))
			}
		}
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("⚠  No season packs available"))
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("Approve to add to Sonarr for episode management,"))
		b.WriteString("\n")
		b.WriteString(rtStyle.Render("or look for this item again after the season ends."))
		b.WriteString("\n")
	}

	if tl.MediaType != model.MediaTypeAnime {
		var creditParts []string
		if tl.Director != "" {
			creditParts = append(creditParts, fmt.Sprintf("Director: %s", tl.Director))
		}
		if tl.Writer != "" && tl.Writer != tl.Director {
			creditParts = append(creditParts, fmt.Sprintf("Writer: %s", tl.Writer))
		} else if tl.Writer != "" {
			creditParts = append(creditParts, fmt.Sprintf("Writer: %s", tl.Writer))
		}
		if tl.Actors != "" {
			creditParts = append(creditParts, fmt.Sprintf("Actors: %s", tl.Actors))
		}
		if len(creditParts) > 0 {
			b.WriteString("\n")
			for i, part := range creditParts {
				if i > 0 {
					b.WriteString("\n")
				}
				b.WriteString(rtStyle.Width(rw).Render(part))
			}
		}
	}

	if tl.MediaType == model.MediaTypeAnime {
		var ratings []string
		if tl.TmdbRating > 0 {
			ratings = append(ratings, fmt.Sprintf("MAL: %s", fmtRating(tl.TmdbRating)))
		}
		if tl.AnimeMembers > 0 {
			ratings = append(ratings, fmt.Sprintf("Members: %s", fmtMembers(tl.AnimeMembers)))
		}
		if tl.AnimeRank > 0 {
			ratings = append(ratings, fmt.Sprintf("Rank: #%d", tl.AnimeRank))
		}
		if len(ratings) > 0 {
			b.WriteString("\n\n")
			b.WriteString(ratingsLine.Render(strings.Join(ratings, " · ")))
		}
	} else {
		imdbStr := fmtRating(tl.ImdbRating)
		if tl.ImdbVotes > 0 {
			imdbStr = fmt.Sprintf("%s (%s votes)", imdbStr, fmtViews(tl.ImdbVotes))
		}
		ratings := fmt.Sprintf("TMDB: %s · IMDb: %s · MC: %s · YT: %s",
			fmtRating(tl.TmdbRating),
			imdbStr,
			fmtRating(tl.MetacriticScore),
			fmtViews(tl.YoutubeViews),
		)

		audiencePart := fmt.Sprintf("🍿 %s", fmtPct(tl.RTAudienceScore))
		if tl.RTAudienceRealScore > 0 && tl.RTRealVotes >= 30 {
			gap := tl.RTAudienceRealScore - tl.RTAudienceScore
			gapColor := "46"
			if gap < 0 {
				gapColor = "196"
			}
			gapStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(gapColor)).Render(fmt.Sprintf("%+.0f%%", gap))
			audiencePart = fmt.Sprintf("%s (real %s, %s) · %s pull votes",
				audiencePart, fmtPct(tl.RTAudienceRealScore), gapStyled, fmtViews(int64(tl.RTRealVotes)))
		}

		rtLine := fmt.Sprintf("🍅 %s · %s", fmtPct(tl.RTCriticsScore), audiencePart)
		b.WriteString("\n\n")
		b.WriteString(ratingsLine.Render(ratings))
		b.WriteString("\n")
		b.WriteString(ratingsLine.Width(rw).Render(rtLine))
	}

	if tl.MediaType != model.MediaTypeAnime {
		if tl.Awards != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Awards: " + tl.Awards))
		}
		if tl.BoxOffice != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("Box Office: " + tl.BoxOffice))
		}
	}

	it = t.currentItem()
	if it != nil && it.albumEvent == nil {
		if info := it.libraryInfo(t.libraryCache); info.status != libNone {
			style := approvedStyle
			if info.status == libPartial {
				style = warnStyle
			}
			b.WriteString("\n\n")
			b.WriteString(style.Render("Library:"))
			b.WriteString("\n")
			b.WriteString(style.Render("  " + info.label))
		}
		if collStr := it.collectionStr(t.libraryCache); collStr != "" {
			b.WriteString("\n")
			b.WriteString(rtStyle.Render("  " + collStr))
		}
	}

	if tl.Overview != "" {
		curLines := strings.Count(b.String(), "\n") + 1
		remaining := maxLines - curLines - 2
		if remaining > 0 {
			wrapped := overviewStyle.Width(rw).Render(tl.Overview)
			lines := strings.Split(wrapped, "\n")
			if len(lines) > remaining {
				lines = lines[:remaining]
				lines[remaining-1] = strings.TrimRight(lines[remaining-1], " ") + " …"
			}
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	if tl.MediaType != model.MediaTypeAnime {
		b.WriteString("\n\n")
		if tl.RTURL != "" {
			b.WriteString(rtStyle.Render("RT: " + tl.RTURL))
		} else {
			b.WriteString(rtStyle.Render("RT page unavailable"))
		}
	}

	return b.String()
}

func (t *TUI) buildConfirmContent() string {
	var b strings.Builder

	b.WriteString(sectionStyle.Render("── Items to download ──"))
	b.WriteString("\n")
	hasApproved := false
	for _, it := range t.items {
		if it.decision == decisionApproved {
			title := it.displayTitle()
			if it.albumEvent == nil && it.bookEvent == nil && it.event.Title.Year > 0 {
				title = fmt.Sprintf("%s (%d)", title, it.event.Title.Year)
			}
			b.WriteString(confirmApprovedStyle.Render("  ✓ " + title))
			b.WriteString("\n")
			hasApproved = true
		}
	}
	if !hasApproved {
		b.WriteString(confirmEmptyStyle.Render("  (none)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("── Items rejected ──"))
	b.WriteString("\n")
	hasRejected := false
	for _, it := range t.items {
		if it.decision == decisionRejected {
			title := it.displayTitle()
			if it.albumEvent == nil && it.bookEvent == nil && it.event.Title.Year > 0 {
				title = fmt.Sprintf("%s (%d)", title, it.event.Title.Year)
			}
			b.WriteString(confirmRejectedStyle.Render("  ✗ " + title))
			b.WriteString("\n")
			hasRejected = true
		}
	}
	if !hasRejected {
		b.WriteString(confirmEmptyStyle.Render("  (none)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	undecided := 0
	for _, it := range t.items {
		if it.decision == decisionNone {
			undecided++
		}
	}
	if undecided > 0 {
		b.WriteString(confirmEmptyStyle.Render(fmt.Sprintf("  %d item(s) still undecided — will be skipped", undecided)))
		b.WriteString("\n")
		b.WriteString("\n")
	}

	b.WriteString(confirmPromptStyle.Render("Are you sure?"))

	return b.String()
}
