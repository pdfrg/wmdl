package process

import (
	"github.com/pdfrg/wmdl/internal/quality"
)

func mergeReleases(a, b []quality.ParsedRelease) []quality.ParsedRelease {
	seen := make(map[string]bool, len(a))
	result := make([]quality.ParsedRelease, 0, len(a)+len(b))
	for _, r := range a {
		key := r.InfoHash
		if key == "" {
			key = r.Guid
		}
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, r)
		}
	}
	for _, r := range b {
		key := r.InfoHash
		if key == "" {
			key = r.Guid
		}
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, r)
		}
	}
	return result
}
