package discover

import (
	"context"
	"fmt"
	"sort"
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

func (r *Runner) sendNotification(ctx context.Context, wantMovie, wantTV, wantAnime, wantMusic, wantBooks bool, uniqueVideos, uniqueAnime, uniqueMusic, bookItems []ScrapedItem, progYear, progWeek, totalEvents int) {
	if r.notify == nil || totalEvents == 0 {
		return
	}

	sourceCounts, err := r.db.PendingEventCountsBySource(ctx, progYear, progWeek)
	if err != nil {
		r.log.Warn().Err(err).Msg("failed to query pending event counts for notification")
		sourceCounts = make(map[string]int)
	}
	sourceNotes := make(map[string]string)

	pendingTotal := 0
	for _, n := range sourceCounts {
		pendingTotal += n
	}
	if pendingTotal == 0 {
		r.log.Debug().Msg("no pending events to notify about")
		return
	}

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

	msg := fmt.Sprintf("**%d new release%s** ready for review:\n",
		pendingTotal, map[bool]string{true: "s", false: ""}[pendingTotal != 1])

	labels := make([]string, 0, len(sourceCounts))
	for l := range sourceCounts {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	for _, l := range labels {
		if note, ok := sourceNotes[l]; ok {
			msg += fmt.Sprintf("\n\t%s: %d (%s)", l, sourceCounts[l], note)
		} else {
			msg += fmt.Sprintf("\n\t%s: %d", l, sourceCounts[l])
		}
	}

	var movieItems, tvItems []ScrapedItem
	for _, item := range uniqueVideos {
		switch item.MediaType {
		case model.MediaTypeTV:
			tvItems = append(tvItems, item)
		default:
			movieItems = append(movieItems, item)
		}
	}

	appendSection := func(items []ScrapedItem, heading string, includeArtist bool) {
		if len(items) == 0 {
			return
		}
		msg += fmt.Sprintf("\n**%s:**", heading)
		for _, item := range items {
			label := item.Title
			if includeArtist {
				label = item.ArtistName + " — " + label
			}
			if item.Year > 0 {
				msg += fmt.Sprintf("\n- %s (%d)", label, item.Year)
			} else {
				msg += fmt.Sprintf("\n- %s", label)
			}
		}
	}

	appendSection(movieItems, "Movies", false)
	appendSection(tvItems, "TV", false)
	appendSection(uniqueAnime, "Anime", false)
	appendSection(uniqueMusic, "Music", true)
	appendSection(bookItems, "Books", true)

	if err := r.notify.Send("wmdl: New Releases", msg, 5); err != nil {
		r.log.Warn().Err(err).Msg("notification failed")
	}
}
