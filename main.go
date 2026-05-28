package main

import (
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"tiktok-bot-poster/ai"
	"tiktok-bot-poster/rss"
	"tiktok-bot-poster/tiktok"
)

const storyFile = "story.json"

func main() {
	// Flags let you skip stages while developing.
	maxJitter := flag.Int("jitter", 90, "max minutes of random delay before running")
	skipPost := flag.Bool("skip-post", false, "skip running post.py (useful while testing)")
	skipAssemble := flag.Bool("skip-assemble", false, "skip running assemble.py")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if err := godotenv.Load(); err != nil {
		log.Printf("warn: no .env file loaded: %v", err)
	}

	randomDelay(0, *maxJitter)

	// 1. Crawl feeds.
	stories := rss.Rank(rss.FetchAll())
	top, ok := rss.Top(stories)
	if !ok {
		log.Fatal("no stories returned from any feed")
	}
	log.Printf("selected story: %q (%s)", top.Title, top.Source)

	// 2. Generate slide copy via Gemini.
	script, err := ai.GenerateScript(top.Title, cleanSummary(top.Summary), top.Source)
	if err != nil {
		log.Fatalf("gemini: %v", err)
	}
	log.Printf("script: %d slides, %d keywords", len(script.Slides), len(script.Keywords))

	// 3. Persist story.json for the Python side.
	if err := writeStory(storyFile, script); err != nil {
		log.Fatalf("write %s: %v", storyFile, err)
	}
	log.Printf("wrote %s", storyFile)

	// 4. Assemble slides.
	if *skipAssemble {
		log.Print("skipping assemble.py (flag)")
	} else {
		if err := tiktok.Assemble(); err != nil {
			log.Fatalf("assemble: %v", err)
		}
		log.Print("assemble.py done")
	}

	// 5. Post to TikTok.
	if *skipPost {
		log.Print("skipping post.py (flag)")
		return
	}
	if err := tiktok.Post(); err != nil {
		log.Fatalf("post: %v", err)
	}
	log.Print("post.py done — pipeline finished")
}

func randomDelay(minMinutes, maxMinutes int) {
	if maxMinutes <= minMinutes {
		return
	}
	rand.Seed(time.Now().UnixNano())
	span := (maxMinutes - minMinutes) * 60
	n := rand.Intn(span) + minMinutes*60
	dur := time.Duration(n) * time.Second
	log.Printf("sleeping %s before run", dur)
	time.Sleep(dur)
}

func writeStory(path string, s *ai.Script) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// cleanSummary strips HTML tags that often appear in RSS descriptions
// (especially Reddit) so the prompt stays focused on prose.
func cleanSummary(s string) string {
	out := strings.Builder{}
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	collapsed := strings.Join(strings.Fields(out.String()), " ")
	if len(collapsed) > 1000 {
		collapsed = collapsed[:1000]
	}
	return collapsed
}
