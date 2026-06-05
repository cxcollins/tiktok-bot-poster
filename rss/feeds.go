package rss

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

var Feeds = []string{
	// Dedicated good news publications
	"https://www.goodnewsnetwork.org/feed",
	"https://positive.news/feed",
	"https://www.optimistdaily.com/feed",
	"https://www.sunnyskyz.com/feed",
	"https://www.inspiremore.com/feed",
	"https://www.goodnewsfinland.com/feed/",
	"https://greatergood.berkeley.edu/feed",

	// Reddit (community upvotes = built-in quality filter)
	"https://www.reddit.com/r/UpliftingNews/.rss",
	"https://www.reddit.com/r/HumansBeingBros/.rss",
	"https://www.reddit.com/r/wholesome/.rss",
	"https://www.reddit.com/r/MadeMeSmile/.rss",

	// Science & tech breakthroughs
	"https://futurism.com/feed",
	"https://www.sciencedaily.com/rss/top/science.xml",
}

type Story struct {
	Title     string
	Summary   string
	Source    string
	URL       string
	Published time.Time
}

const (
	maxConcurrency = 10
	httpTimeout    = 5 * time.Second
	MaxAge         = 7 * 24 * time.Hour
)

// FetchAll crawls every feed concurrently and returns a flat list of stories.
// Failed feeds are logged but do not abort the run.
func FetchAll() []Story {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []Story
		sem     = make(chan struct{}, maxConcurrency)
	)

	for _, url := range Feeds {
		wg.Add(1)
		sem <- struct{}{}
		go func(feedURL string) {
			defer wg.Done()
			defer func() { <-sem }()

			stories, err := fetchOne(feedURL)
			if err != nil {
				log.Printf("rss: failed %s: %v", feedURL, err)
				return
			}
			mu.Lock()
			results = append(results, stories...)
			mu.Unlock()
		}(url)
	}

	wg.Wait()
	log.Printf("rss: fetched %d stories from %d feeds", len(results), len(Feeds))
	return results
}

func fetchOne(feedURL string) ([]Story, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 tiktok-bot-poster/1.0")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	feed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-MaxAge)
	out := make([]Story, 0, len(feed.Items))
	for _, item := range feed.Items {
		if item == nil || item.Title == "" {
			continue
		}
		var published time.Time
		switch {
		case item.PublishedParsed != nil:
			published = *item.PublishedParsed
		case item.UpdatedParsed != nil:
			published = *item.UpdatedParsed
		default:
			// No date — skip rather than assume it's fresh.
			continue
		}
		if published.Before(cutoff) {
			continue
		}
		out = append(out, Story{
			Title:     item.Title,
			Summary:   item.Description,
			Source:    feed.Title,
			URL:       item.Link,
			Published: published,
		})
	}
	return out, nil
}
