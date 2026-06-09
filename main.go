package main

import (
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"os"
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

	// 1. Crawl feeds and dedupe.
	candidates := rss.Deduplicate(rss.FetchAll())
	if len(candidates) == 0 {
		log.Fatal("no stories returned from any feed")
	}
	log.Printf("rss: %d candidate stories after dedupe", len(candidates))

	// 2. Ask Gemini to pick the most compelling stories.
	top, err := ai.SelectTopStories(candidates, ai.SlideCount)
	if err != nil {
		log.Fatalf("select: %v", err)
	}
	log.Printf("selected %d stories", len(top))

	// 3. Generate slide copy via Gemini.
	script, err := ai.GenerateScript(top)
	if err != nil {
		log.Fatalf("gemini: %v", err)
	}
	log.Printf("script: %d slides", len(script.Slides))

	// 4. Generate per-slide background images via Gemini.
	if err := ai.GenerateImages(script.Slides, "output"); err != nil {
		log.Fatalf("images: %v", err)
	}
	log.Printf("images: backgrounds generated")

	// 5. Persist story.json for the Python side.
	if err := writeStory(storyFile, script); err != nil {
		log.Fatalf("write %s: %v", storyFile, err)
	}
	log.Printf("wrote %s", storyFile)

	// 5. Assemble slides.
	if *skipAssemble {
		log.Print("skipping assemble.py (flag)")
	} else {
		if err := tiktok.Assemble(); err != nil {
			log.Fatalf("assemble: %v", err)
		}
		log.Print("assemble.py done")
	}

	// 6. Post to TikTok.
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
