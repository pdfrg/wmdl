package discover

import (
	"fmt"
	"time"

	"github.com/pdfrg/wmdl/internal/model"
)

func (r *Runner) buildProviders(wantMovie, wantTV, wantAnime, wantMusic, wantBooks bool) ([]ReleaseProvider, error) {
	providers := []ReleaseProvider{}

	if (wantMovie && r.cfg.MediaTypes.Movies.Enabled) ||
		(wantTV && r.cfg.MediaTypes.TV.Enabled) {

		typeReq := make(map[string]map[model.MediaType]bool)

		if wantMovie && r.cfg.MediaTypes.Movies.Enabled {
			for _, s := range r.cfg.MediaTypes.Movies.Scrapers {
				if typeReq[s] == nil {
					typeReq[s] = make(map[model.MediaType]bool)
				}
				typeReq[s][model.MediaTypeMovie] = true
			}
		}
		if wantTV && r.cfg.MediaTypes.TV.Enabled {
			for _, s := range r.cfg.MediaTypes.TV.Scrapers {
				if typeReq[s] == nil {
					typeReq[s] = make(map[model.MediaType]bool)
				}
				typeReq[s][model.MediaTypeTV] = true
			}
		}

		for scraperName, types := range typeReq {
			switch scraperName {
			case "dvdsreleasedates":
				lo, hi := r.lookbackRange("physical", r.cfg.MediaTypes.PhysicalLookbackWeeks)
				for wk := lo; wk <= hi; wk++ {
					dvd := NewDVDReleaseDates()
					if len(types) == 1 {
						for mt := range types {
							dvd.SetMediaTypeFilter(mt)
						}
					}
					if r.hasTargetWeek {
						py, pw := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
						dvd.SetWeekRange(py, pw)
					}
					providers = append(providers, dvd)
				}

			case "tmdb-discover", "flixpatrol":
				for mt := range types {
					var cfgVal int
					var key string
					switch mt {
					case model.MediaTypeMovie:
						cfgVal = r.cfg.MediaTypes.Movies.StreamingLookbackWeeks
						key = "movie"
					case model.MediaTypeTV:
						cfgVal = r.cfg.MediaTypes.TV.StreamingLookbackWeeks
						key = "tv"
					}
					if cfgVal <= 0 {
						cfgVal = 8
					}
					lo, hi := r.lookbackRange(key, cfgVal)
					for wk := lo; wk <= hi; wk++ {
						sy, sw := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
						switch scraperName {
						case "tmdb-discover":
							tmdb := NewTMDBDiscoverProvider(r.tmdb)
							tmdb.SetMediaTypeFilter(mt)
							if r.hasTargetWeek {
								tmdb.SetWeekRange(sy, sw)
							}
							providers = append(providers, tmdb)
						case "flixpatrol":
							if r.browserCtx == nil {
								r.log.Warn().Msg("flixpatrol: browser unavailable, skipping")
								continue
							}
							fp := NewFlixPatrolProvider(r.debugURL, r.browserCtx)
							fp.SetMediaTypeFilter(mt)
							if r.hasTargetWeek {
								fp.SetWeekRange(sy, sw)
							}
							providers = append(providers, fp)
						}
					}
				}

			default:
				r.log.Warn().Str("scraper", scraperName).Msg("unknown video scraper configured")
			}
		}
	}

	if wantAnime && r.cfg.MediaTypes.Anime.Enabled {
		lo, hi := r.lookbackRange("anime", r.cfg.MediaTypes.Anime.LookbackWeeks)
		for wk := lo; wk <= hi; wk++ {
			anilist := NewAniListProvider(r.cfg.MediaTypes.Anime)
			if r.hasTargetWeek {
				animeYear, animeWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
				anilist.SetWeekRange(animeYear, animeWeek)
			}
			providers = append(providers, anilist)

			tenrai := NewTenraiAnimeProvider(r.cfg.MediaTypes.Anime)
			if r.hasTargetWeek {
				animeYear, animeWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
				tenrai.SetWeekRange(animeYear, animeWeek)
			}
			providers = append(providers, tenrai)

			jikan := NewJikanAnimeProvider(r.cfg.MediaTypes.Anime)
			if r.hasTargetWeek {
				animeYear, animeWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
				jikan.SetWeekRange(animeYear, animeWeek)
			}
			providers = append(providers, jikan)
		}
	}

	if wantBooks && r.cfg.MediaTypes.Books.Enabled {
		bookYear, bookWeek := r.targetYear, r.targetWeek
		if !r.hasTargetWeek {
			bookYear, bookWeek = time.Now().ISOWeek()
		}

		hasBookshop := false
		for _, n := range r.cfg.MediaTypes.Books.Scrapers {
			if n == "bookshop" {
				hasBookshop = true
				break
			}
		}

		bookLo, bookHi := r.lookbackRange("book", r.cfg.MediaTypes.Books.LookbackWeeks)
		for wk := bookLo; wk <= bookHi; wk++ {
			scrapeYear, scrapeWeek := addISOWeekOffset(bookYear, bookWeek, wk)
			for _, name := range r.cfg.MediaTypes.Books.Scrapers {
				switch name {
				case "bookshop":
					continue
				case "goodreads":
					if r.browserCtx == nil {
						r.log.Warn().Msg("goodreads: browser unavailable, skipping")
						continue
					}
					gr := NewGoodreadsProvider(r.debugURL, r.browserCtx)
					gr.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, gr)
				case "goodreads_blog":
					grb := NewGoodreadsBlogProvider()
					grb.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, grb)
				case "bookmarks":
					bm := NewBookMarksProvider()
					bm.SetWeekRange(scrapeYear, scrapeWeek)
					providers = append(providers, bm)
				default:
					r.log.Warn().Str("scraper", name).Msg("unknown book scraper configured")
				}
			}
		}

		if hasBookshop {
			if r.hasLookbackOverride("book") {
				r.log.Info().Msg("bookshop: does not support lookback, scraping current real week as usual")
			}
			if r.browserCtx == nil {
				r.log.Warn().Msg("bookshop: browser unavailable, skipping")
			} else {
				bs := NewBookshopProvider(r.debugURL, r.browserCtx)
				realYear, realWeek := time.Now().ISOWeek()
				bs.SetWeekRange(realYear, realWeek)
				providers = append(providers, bs)
			}
		}
	}

	if wantMusic && r.cfg.MediaTypes.Music.Enabled {
		cfgVal := r.cfg.MediaTypes.Music.LookbackWeeks
		if cfgVal <= 0 {
			cfgVal = 1
		}
		lo, hi := r.lookbackRange("music", cfgVal)
		rpChartsAdded := false
		for wk := lo; wk <= hi; wk++ {
			musicYear, musicWeek := addISOWeekOffset(r.targetYear, r.targetWeek, wk)
			for _, name := range r.cfg.MediaTypes.Music.Scrapers {
				switch name {
				case "albumoftheyear":
					aoty := NewAOTYProvider(r.cfg.MediaTypes.Music.Filter)
					if r.hasTargetWeek {
						aoty.SetWeekRange(musicYear, musicWeek)
					}
					providers = append(providers, aoty)

				case "allmusic":
					if r.browserCtx == nil {
						r.log.Warn().Msg("allmusic: browser unavailable, skipping")
						continue
					}
					allmusic := NewAllMusicProvider(r.debugURL, r.browserCtx)
					if r.hasTargetWeek {
						allmusic.SetWeekRange(musicYear, musicWeek)
					}
					providers = append(providers, allmusic)

				case "rpcharts":
					if !rpChartsAdded {
						providers = append(providers, NewRPChartsProvider(r.cfg.MediaTypes.Music.RPChartsStations))
						rpChartsAdded = true
					}

				default:
					r.log.Warn().Str("scraper", name).Msg("unknown music scraper configured")
				}
			}
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers enabled for type filter %q", r.mediaTypeFilter)
	}

	return providers, nil
}
