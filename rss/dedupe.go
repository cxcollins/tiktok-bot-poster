package rss

import (
	"sort"
	"strings"
)

// Deduplicate drops empty entries, deduplicates by URL (then title), and sorts
// by recency so the freshest story is at index 0. The Gemini-based ranking by
// "most compelling" happens later in the ai package.
func Deduplicate(stories []Story) []Story {
	seen := make(map[string]struct{}, len(stories))
	out := make([]Story, 0, len(stories))

	for _, s := range stories {
		if s.Title == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(s.URL))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(s.Title))
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Published.After(out[j].Published)
	})
	return out
}
