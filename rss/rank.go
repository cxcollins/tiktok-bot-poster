package rss

import (
	"sort"
	"strings"
)

// Rank deduplicates by URL (then title), drops empty entries, and sorts by recency
// so the freshest story is at index 0.
func Rank(stories []Story) []Story {
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

// Top returns the most recent story, or false if none are available.
func Top(stories []Story) (Story, bool) {
	if len(stories) == 0 {
		return Story{}, false
	}
	return stories[0], true
}
