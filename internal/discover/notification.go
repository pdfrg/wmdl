package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

func sourceDisplayName(source string, mt model.MediaType) string {
	switch source {
	case "tmdb-discover":
		return "tmdb " + string(mt)
	case "flixpatrol":
		return "flixpatrol " + string(mt)
	case "goodreads_blog":
		return "goodreads blog"
	case "anilist":
		return "anilist (completed)"
	case "anilist-airing":
		return "anilist (airing)"
	case "tenrai":
		return "tenrai (completed)"
	case "tenrai-airing":
		return "tenrai (airing)"
	case "jikan":
		return "jikan (completed)"
	case "jikan-airing":
		return "jikan (airing)"
	default:
		return source
	}
}

func (r *Runner) sendNotification(ctx context.Context, wantMovie, wantTV, wantAnime, wantMusic, wantBooks bool, bookItems []ScrapedItem, progYear, progWeek int) {
	if r.notify == nil {
		return
	}

	sourceCounts, err := r.db.PendingEventCountsBySource(ctx, progYear, progWeek)
	if err != nil {
		r.log.Warn().Err(err).Msg("failed to query pending event counts for notification")
		sourceCounts = make(map[string]int)
	}
	sourceNotes := make(map[string]string)

	if wantMovie && r.cfg.MediaTypes.Movies.Enabled {
		for _, s := range r.cfg.MediaTypes.Movies.Scrapers {
			label := sourceDisplayName(s, model.MediaTypeMovie)
			if _, ok := sourceCounts[label]; !ok {
				sourceCounts[label] = 0
			}
		}
	}
	if wantTV && r.cfg.MediaTypes.TV.Enabled {
		for _, s := range r.cfg.MediaTypes.TV.Scrapers {
			label := sourceDisplayName(s, model.MediaTypeTV)
			if _, ok := sourceCounts[label]; !ok {
				sourceCounts[label] = 0
			}
		}
	}

	if wantAnime && r.cfg.MediaTypes.Anime.Enabled {
		for _, label := range []string{
			"anilist (completed)", "tenrai (completed)",
		} {
			if _, ok := sourceCounts[label]; !ok {
				sourceCounts[label] = 0
			}
		}
		if r.cfg.MediaTypes.Anime.PhaseBEnabled {
			for _, label := range []string{
				"anilist (airing)", "tenrai (airing)",
			} {
				if _, ok := sourceCounts[label]; !ok {
					sourceCounts[label] = 0
				}
			}
		}
	}

	if wantMusic && r.cfg.MediaTypes.Music.Enabled {
		for _, s := range r.cfg.MediaTypes.Music.Scrapers {
			label := sourceDisplayName(s, model.MediaTypeMusic)
			if _, ok := sourceCounts[label]; !ok {
				sourceCounts[label] = 0
			}
			if s == "allmusic" && sourceCounts[label] == 0 {
				checkYear, checkWeek := r.targetYear, r.targetWeek
				if !r.hasTargetWeek {
					checkYear, checkWeek = time.Now().ISOWeek()
				}
				cfgVal := r.cfg.MediaTypes.Music.LookbackWeeks
				if cfgVal <= 0 {
					cfgVal = 1
				}
				lo, _ := r.lookbackRange("music", cfgVal)
				checkYear, checkWeek = addISOWeekOffset(checkYear, checkWeek, lo)
				weekStart, weekEnd := wmdlWeekRange(checkYear, checkWeek)
				scrapeMonth, _ := computeAllMusicTarget(weekStart, weekEnd)
				if scrapeMonth == 0 {
					sourceNotes[label] = "not a boundary week"
				}
			}
		}
	}

	if wantBooks && r.cfg.MediaTypes.Books.Enabled {
		for _, s := range r.cfg.MediaTypes.Books.Scrapers {
			label := sourceDisplayName(s, model.MediaTypeBook)
			if _, ok := sourceCounts[label]; !ok {
				sourceCounts[label] = 0
			}
		}
	}

	r.markScrapeFailures(sourceCounts, sourceNotes)

	bsNote := r.bookshopPrePopSummary(bookItems)

	pendingTotal := 0
	for _, n := range sourceCounts {
		pendingTotal += n
	}

	// No pending events. If scrapers failed, send an explicit alert so a total
	// outage is visible; otherwise this week simply has nothing to report.
	if pendingTotal == 0 {
		if bsNote != "" {
			// Scrapers ran fine, but everything found belongs to a future
			// review week (bookshop pre-population). Report it so a quiet
			// week doesn't look like a broken scraper.
			msg := fmt.Sprintf("No releases pending for %d-W%02d.\n\n%s", progYear, progWeek, bsNote)
			if err := r.notify.Send("wmdl: Bookshop Pre-Population", msg, 5); err != nil {
				r.log.Warn().Err(err).Msg("notification failed")
			}
			return
		}
		if len(r.failedScrapers) == 0 {
			r.log.Debug().Msg("no pending events to notify about")
			return
		}
		msg := "No new releases found — scrape failures:\n"
		labels := make([]string, 0, len(sourceNotes))
		for l := range sourceNotes {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for _, l := range labels {
			msg += fmt.Sprintf("\n\t%s: %s", l, sourceNotes[l])
		}
		if err := r.notify.Send("wmdl: Scrape Failures", msg, 8); err != nil {
			r.log.Warn().Err(err).Msg("failure notification failed")
		}
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%d new release%s** ready for review:\n",
		pendingTotal, map[bool]string{true: "s", false: ""}[pendingTotal != 1])

	labels := make([]string, 0, len(sourceCounts))
	for l := range sourceCounts {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	for _, l := range labels {
		if note, ok := sourceNotes[l]; ok {
			fmt.Fprintf(&sb, "\n- %s: %d (%s)", l, sourceCounts[l], note)
		} else {
			fmt.Fprintf(&sb, "\n- %s: %d", l, sourceCounts[l])
		}
		if l == "bookshop" && bsNote != "" {
			fmt.Fprintf(&sb, "\n  %s", bsNote)
		}
	}

	// Item lists come from the persisted pending events for this week, not the
	// raw scrape batch: items dropped by quality/content filters, filed under a
	// different lookback week, or pre-populated for a future week (bookshop)
	// never become pending events here, so they must not appear below.
	for _, sec := range r.pendingNotificationSections(ctx, progYear, progWeek) {
		if len(sec.lines) == 0 {
			continue
		}
		sb.WriteString("\n\n**")
		sb.WriteString(sec.heading)
		sb.WriteString(":**")
		for _, line := range sec.lines {
			sb.WriteString("\n- ")
			sb.WriteString(line)
		}
	}

	if err := r.notify.Send("wmdl: New Releases", sb.String(), 5); err != nil {
		r.log.Warn().Err(err).Msg("notification failed")
	}
}

// notifySection is one heading + item list in a discover notification.
type notifySection struct {
	heading string
	lines   []string
}

// pendingNotificationSections renders the per-title lists for a week from the
// persisted pending events, so the notification matches what `wmdl review`
// will show. This intentionally ignores the raw scrape batch passed to
// sendNotification: those items may have been rejected by quality/content
// filters or filed under a different week.
func (r *Runner) pendingNotificationSections(ctx context.Context, year, week int) []notifySection {
	var sections []notifySection

	events, err := r.db.ListEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
	if err != nil {
		r.log.Warn().Err(err).Msg("loading pending events for notification")
	}
	var movieLines, tvLines, animeLines []string
	for _, ev := range events {
		if ev.Title == nil {
			continue
		}
		label := notifyTitleLabel(ev.Title.Title, ev.Title.Year, ev.Title.Networks)
		switch ev.Title.MediaType {
		case model.MediaTypeTV:
			tvLines = append(tvLines, label)
		case model.MediaTypeAnime:
			animeLines = append(animeLines, label)
		default:
			movieLines = append(movieLines, label)
		}
	}
	sections = append(sections,
		notifySection{"Movies", movieLines},
		notifySection{"TV", tvLines},
		notifySection{"Anime", animeLines},
	)

	albumEvents, err := r.db.ListAlbumReleaseEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
	if err != nil {
		r.log.Warn().Err(err).Msg("loading pending albums for notification")
	}
	var musicLines []string
	for _, ae := range albumEvents {
		if ae.Release == nil {
			continue
		}
		musicLines = append(musicLines, notifyTitleLabel(
			ae.Release.ArtistName+" — "+ae.Release.Title, ae.Release.Year, ""))
	}
	sections = append(sections, notifySection{"Music", musicLines})

	bookEvents, err := r.db.ListBookEventsByWeekAndStatus(ctx, year, week, model.StatusPending)
	if err != nil {
		r.log.Warn().Err(err).Msg("loading pending books for notification")
	}
	var bookLines []string
	for _, be := range bookEvents {
		if be.Book == nil {
			continue
		}
		label := be.Book.Title
		if be.Author != nil && be.Author.Name != "" {
			label = be.Author.Name + " — " + label
		}
		bookLines = append(bookLines, notifyTitleLabel(label, be.Book.ReleaseYear, ""))
	}
	sections = append(sections, notifySection{"Books", bookLines})

	return sections
}

// notifyTitleLabel appends the year and network/studio suffixes used in the
// per-title notification lists.
func notifyTitleLabel(title string, year int, networks string) string {
	label := title
	if year > 0 {
		label = fmt.Sprintf("%s (%d)", label, year)
	}
	if networks != "" {
		label += " [" + networks + "]"
	}
	return label
}

// bookshopPrePopSummary summarizes where this run's bookshop finds will be
// stored (their timeshifted review week), so notifications can distinguish
// "0 pending this week" from "the scraper found nothing". Returns "" when no
// bookshop items with a parseable release date were scraped.
func (r *Runner) bookshopPrePopSummary(items []ScrapedItem) string {
	weekCounts := make(map[string]int)
	total := 0
	for _, item := range items {
		if item.Source != "bookshop" && !strings.Contains(item.Source, "bookshop") {
			continue
		}
		if item.ReleaseDate == "" {
			continue
		}
		var t time.Time
		var err error
		for _, f := range []string{"January 2, 2006", "Jan 2, 2006", "2006-01-02"} {
			t, err = time.Parse(f, item.ReleaseDate)
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		sy, sw := r.bookshopStorageWeek(y, w)
		weekCounts[fmt.Sprintf("%d-W%02d", sy, sw)]++
		total++
	}
	if total == 0 {
		return ""
	}

	weeks := make([]string, 0, len(weekCounts))
	for wk := range weekCounts {
		weeks = append(weeks, wk)
	}
	sort.Strings(weeks)

	parts := make([]string, 0, len(weeks))
	for _, wk := range weeks {
		parts = append(parts, fmt.Sprintf("%d → %s", weekCounts[wk], wk))
	}
	// Single week (the common case): drop the per-batch count prefix.
	stored := strings.Join(parts, ", ")
	if len(parts) == 1 {
		stored = weeks[0]
	}
	return fmt.Sprintf("%d scraped today, stored under %s (reviewable in a future week)",
		total, stored)
}

// markScrapeFailures annotates source counts whose provider did not scrape
// this run, so "0 items" reads differently from an actual failure. Labels
// mirror the zero-fill blocks in sendNotification so they render with the
// same text. Deprecated providers (e.g. jikan) are intentionally not mapped.
func (r *Runner) markScrapeFailures(sourceCounts map[string]int, sourceNotes map[string]string) {
	if len(r.failedScrapers) == 0 {
		return
	}
	mark := func(scraper string, mt model.MediaType) {
		if reason, ok := r.failedScrapers[scraper]; ok {
			sourceNotes[sourceDisplayName(scraper, mt)] = sanitizeFailureReason(reason, maxAPIFailureDetail)
		}
	}

	if r.cfg.MediaTypes.Movies.Enabled {
		for _, s := range r.cfg.MediaTypes.Movies.Scrapers {
			mark(s, model.MediaTypeMovie)
		}
	}
	if r.cfg.MediaTypes.TV.Enabled {
		for _, s := range r.cfg.MediaTypes.TV.Scrapers {
			mark(s, model.MediaTypeTV)
		}
	}
	if r.cfg.MediaTypes.Anime.Enabled {
		for _, name := range []string{"anilist", "tenrai"} {
			if reason, ok := r.failedScrapers[name]; ok {
				reason = sanitizeFailureReason(reason, maxAPIFailureDetail)
				sourceNotes[name+" (completed)"] = reason
				if r.cfg.MediaTypes.Anime.PhaseBEnabled {
					sourceNotes[name+" (airing)"] = reason
				}
			}
		}
	}
	if r.cfg.MediaTypes.Music.Enabled {
		for _, s := range r.cfg.MediaTypes.Music.Scrapers {
			mark(s, model.MediaTypeMusic)
		}
	}
	if r.cfg.MediaTypes.Books.Enabled {
		for _, s := range r.cfg.MediaTypes.Books.Scrapers {
			mark(s, model.MediaTypeBook)
		}
	}
}
