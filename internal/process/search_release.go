package process

import (
	"context"
	"fmt"
	"strings"

	"github.com/pdfrg/wmdl/internal/model"
	"github.com/pdfrg/wmdl/internal/quality"
	"github.com/pdfrg/wmdl/internal/search"
)

func (e *Executor) searchRelease(ctx context.Context, title *model.Title, stripped string, season int) ([]quality.ParsedRelease, error) {
	resCfg := e.cfg.Quality.Movies
	searchType := "movie"
	searchCats := []int{search.CatMovie}

	switch title.MediaType {
	case model.MediaTypeTV:
		resCfg = e.cfg.Quality.TV
		searchType = "tvsearch"
		searchCats = []int{search.CatTV}
	case model.MediaTypeAnime:
		resCfg = e.cfg.Quality.Anime
		searchType = "tvsearch"
		searchCats = []int{search.CatAnime, search.CatTV}
	}

	resKeyword := resolutionSearchKeyword(resCfg.Resolution)
	fallbackRes := fallbackResolution(resKeyword)

	var queries []string
	if title.MediaType == model.MediaTypeTV || title.MediaType == model.MediaTypeAnime {
		lower := strings.ToLower(title.Title)
		skipSeason := season == 1 && (strings.Contains(lower, "final season") || strings.Contains(lower, "last season"))
		queries = tvSearchQueries(stripped, season, resKeyword, fallbackRes, skipSeason)
	} else {
		queries = movieSearchQueries(stripped, title.Year, resKeyword)
	}

	preferredIDs := e.prowl.PreferredIndexerIDs(searchCats[0])
	numTiers := len(queries)
	var exactPool, fuzzyPool []quality.ParsedRelease

	sanitized := sanitizeSearchQuery(stripped)

	animeCatSets := [][]int{searchCats}
	allCatSets := [][]int{searchCats}
	if title.MediaType == model.MediaTypeAnime {
		animeCatSets = [][]int{{search.CatAnime}}
		allCatSets = [][]int{{search.CatTV}}
	}

	if len(preferredIDs) > 0 {
		names := make([]string, 0, len(preferredIDs))
		for _, id := range preferredIDs {
			names = append(names, e.prowl.GetIndexerName(ctx, id))
		}
		e.log.Info().Ints("ids", preferredIDs).Strs("names", names).Msg("preferred indexers")

		for _, cats := range animeCatSets {
			for i, q := range queries {
				e.log.Info().Msgf("[%d/%d] preferred (cats=%v): %s", i+1, numTiers, cats, q)
				results, err := e.prowl.Search(ctx, search.SearchParams{
					Query:      q,
					Type:       searchType,
					IndexerIDs: preferredIDs,
					Limit:      50,
					Categories: cats,
				})
				if err != nil {
					e.log.Warn().Err(err).Str("query", q).Msg("preferred indexer search failed")
					break
				}
				exact, fuzzy := quality.PartitionReleases(results, sanitized, title.Year, season, string(title.MediaType))
				exactPool = mergeReleases(exactPool, exact, preferredIDs)
				fuzzyPool = mergeReleases(fuzzyPool, fuzzy, preferredIDs)
				e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
				if len(exactPool) >= 10 {
					return exactPool, nil
				}
			}
		}

		e.log.Info().Msgf("→ %d exact from preferred, searching all indexers", len(exactPool))
	}

	for _, cats := range allCatSets {
		for i, q := range queries {
			e.log.Info().Msgf("[%d/%d] searching all (cats=%v): %s", i+1, numTiers, cats, q)
			results, err := e.prowl.Search(ctx, search.SearchParams{
				Query:      q,
				Type:       searchType,
				Limit:      50,
				Categories: cats,
			})
			if err != nil {
				e.log.Warn().Err(err).Str("query", q).Msg("prowlarr search failed")
				continue
			}
			exact, fuzzy := quality.PartitionReleases(results, sanitized, title.Year, season, string(title.MediaType))
			exactPool = mergeReleases(exactPool, exact, preferredIDs)
			fuzzyPool = mergeReleases(fuzzyPool, fuzzy, preferredIDs)
			e.log.Debug().Msgf("→ %d exact, %d fuzzy (exact total: %d)", len(exact), len(fuzzy), len(exactPool))
			if len(exactPool) >= 10 {
				return exactPool, nil
			}
		}
	}

	if len(exactPool) > 0 || len(fuzzyPool) > 0 {
		result := exactPool
		if n := 10 - len(exactPool); n > 0 && len(fuzzyPool) > 0 {
			if n > len(fuzzyPool) {
				n = len(fuzzyPool)
			}
			result = append(result, fuzzyPool[:n]...)
		}
		return result, nil
	}
	return nil, nil
}

func resolutionSearchKeyword(res string) string {
	switch res {
	case "2160p", "4k", "uhd":
		return "2160p"
	case "1080p":
		return "1080p"
	case "720p":
		return "720p"
	}
	return "1080p"
}

func movieSearchQueries(title string, year int, res string) []string {
	queries := []string{
		fmt.Sprintf("%s %d %s", title, year, res),
		fmt.Sprintf("%s %d 4k", title, year),
		fmt.Sprintf("%s %d 1080p", title, year),
		fmt.Sprintf("%s %d", title, year),
	}
	for i := range queries {
		queries[i] = sanitizeSearchQuery(queries[i])
	}
	return queries
}

func tvSearchQueries(stripped string, season int, res, fallback string, skipSeason bool) []string {
	seasonStr := fmt.Sprintf("S%02d", season)
	seasonWord := fmt.Sprintf("season %d", season)

	var tiers []string
	if !skipSeason {
		tiers = []string{
			sanitizeSearchQuery(fmt.Sprintf("%s %s complete %s", stripped, seasonStr, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s complete %s", stripped, seasonWord, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s %s", stripped, seasonStr, res)),
			sanitizeSearchQuery(fmt.Sprintf("%s %s %s", stripped, seasonWord, res)),
		}
	}
	tiers = append(tiers, sanitizeSearchQuery(fmt.Sprintf("%s %s", stripped, res)))
	if fallback != "" {
		tiers = append(tiers, sanitizeSearchQuery(fmt.Sprintf("%s %s", stripped, fallback)))
	}
	return tiers
}

func sanitizeSearchQuery(q string) string {
	q = strings.ReplaceAll(q, "/", " ")
	q = strings.ReplaceAll(q, "'", "")
	q = strings.ReplaceAll(q, "\"", "")
	q = strings.ReplaceAll(q, "?", "")
	q = strings.ReplaceAll(q, ":", " ")
	q = strings.ReplaceAll(q, "½", "")
	q = strings.ReplaceAll(q, "¼", "")
	q = strings.ReplaceAll(q, "¾", "")
	q = strings.ReplaceAll(q, "⁄", "")
	return strings.TrimSpace(q)
}

func fallbackResolution(res string) string {
	switch res {
	case "2160p":
		return "1080p"
	case "1080p":
		return "720p"
	case "720p":
		return "480p"
	}
	return ""
}
