package process

import (
	"github.com/pdfrg/wmdl/internal/quality"
)

// releaseKey returns the dedup key for a release: InfoHash when available,
// otherwise the indexer-specific GUID (only dedups within one indexer).
func releaseKey(r quality.ParsedRelease) string {
	if r.InfoHash != "" {
		return r.InfoHash
	}
	return r.Guid
}

// indexerRank returns the position of id in the preferred indexer list
// (0 = most preferred), or -1 if id is not a preferred indexer.
func indexerRank(id int, preferredIDs []int) int {
	for i, p := range preferredIDs {
		if id == p {
			return i
		}
	}
	return -1
}

// mergeReleases deduplicates releases by releaseKey. When multiple copies of
// the same release exist (e.g. same InfoHash on several trackers), the copy
// from the highest-ranked preferred indexer wins. Each additional preferred
// tracker hosting the release increments the winner's ExtraTrackerCount so
// the picker can show a "+N" badge.
func mergeReleases(a, b []quality.ParsedRelease, preferredIDs []int) []quality.ParsedRelease {
	result := make([]quality.ParsedRelease, 0, len(a)+len(b))
	byKey := make(map[string]int, len(a)+len(b))

	for _, r := range append(a, b...) {
		key := releaseKey(r)
		if key == "" {
			result = append(result, r)
			continue
		}
		if idx, ok := byKey[key]; ok {
			w := &result[idx]
			rRank, wRank := indexerRank(r.IndexerID, preferredIDs), indexerRank(w.IndexerID, preferredIDs)
			switch {
			case rRank >= 0 && wRank >= 0 && rRank < wRank:
				// r is from a more-preferred tracker: replace the winner,
				// the old winner now counts as an extra tracker.
				r.ExtraTrackerCount = w.ExtraTrackerCount + 1
				result[idx] = r
			case rRank >= 0 && wRank >= 0 && rRank > wRank:
				// r is preferred but ranked lower: keep the winner, r is an extra.
				w.ExtraTrackerCount++
			case rRank >= 0 && wRank < 0:
				// r is the first preferred copy: replace the non-preferred winner.
				r.ExtraTrackerCount = w.ExtraTrackerCount
				result[idx] = r
			case rRank < 0 && wRank >= 0:
				// r is non-preferred, the winner is preferred: ignore.
			}
			continue
		}
		byKey[key] = len(result)
		result = append(result, r)
	}
	return result
}
