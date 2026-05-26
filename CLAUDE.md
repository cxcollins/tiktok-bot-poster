# TikTok Good News Bot — Project Spec

## Overview

A fully automated pipeline that sources good news stories daily, generates slideshow images, and uploads them to TikTok as a photo carousel post. The goal is maximum automation at minimal cost (~$0.04/day).

---

## Architecture

```
Go (orchestrator)
  → goroutines crawl RSS feeds concurrently
  → Gemini Flash API generates slide copy as JSON
  → random delay jitter (60–90 min window)
  → exec: python assemble.py
  → exec: python post.py
  → log result

Python assemble.py
  → reads story.json written by Go
  → fetches images from Pexels API by keyword
  → Pillow assembles 1080x1920 slides (image + gradient + text overlay)
  → writes output/slide_1.jpg ... slide_n.jpg

Python post.py
  → Playwright (persistent browser context) opens tiktok.com/upload
  → uploads slides from output/ directory
  → fills caption + hashtags
  → clicks Post
```

---

## Project Structure

```
/
  main.go                  # Go orchestrator entry point
  go.mod
  go.sum
  tiktok/
    post.go                # TikTok Playwright posting logic (called via exec)
  rss/
    feeds.go               # RSS feed URLs + fetching logic
    rank.go                # Deduplication + story ranking
  ai/
    script.go              # Gemini Flash API call + JSON parsing
  assemble.py              # Pillow image assembly
  post.py                  # Playwright TikTok uploader
  requirements.txt         # Python deps
  story.json               # Handoff file between Go and Python (gitignored)
  output/                  # Generated slide images (gitignored)
  tiktok_session/          # Persistent Playwright browser session (gitignored)
  logs/                    # Cron output logs (gitignored)
  .env                     # API keys (gitignored)
  .env.example             # Committed env template
```

---

## Language Split

- **Go** — RSS crawling, orchestration, AI API calls, cron scheduling, random delay jitter
- **Python** — Image assembly (Pillow), TikTok browser automation (Playwright)
- The two sides communicate via `story.json` written to disk by Go, read by Python

---

## RSS Sourcing (Go)

Use `github.com/mmcdole/gofeed` to crawl feeds concurrently with goroutines.

**Feed list:**

These are dedicated good news sources — no sentiment filtering needed since they only publish positive content. Reddit feeds are especially valuable because community upvotes provide a built-in quality signal.

```go
var feeds = []string{
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
```

**Additional feed discovery resources:**
- `github.com/plenaryapp/awesome-rss-feeds` — ~500 curated feeds with OPML files, good for expanding later
- `rss.feedspot.com/good_news_rss_feeds/` — ranked list of good news feeds specifically

**Concurrency pattern:**
- Fire one goroutine per feed using `sync.WaitGroup` + a results channel
- Cap concurrency at 10 simultaneous requests with a semaphore channel `make(chan struct{}, 10)`
- Set a 5 second timeout on each HTTP client to prevent hanging goroutines
- Rank results by recency, deduplicate by URL

**story.json shape written by Go:**
```json
{
  "title": "Story headline",
  "summary": "2-3 sentence summary",
  "source": "Good News Network",
  "keywords": ["ocean", "conservation", "wildlife"],
  "slides": [
    { "slide": 1, "text": "Hook line here 🌊" },
    { "slide": 2, "text": "Supporting detail..." },
    { "slide": 3, "text": "More context..." },
    { "slide": 4, "text": "Follow for daily good news 🌍" }
  ],
  "caption": "#goodnews #upliftingnews #fyp #positive"
}
```

---

## AI Slide Copy (Go → Gemini Flash)

Use **Gemini 2.0 Flash** via REST API. Cost ~$0.10/1M tokens, well under $0.01/day.

**Prompt template:**
```
You are writing copy for a TikTok photo slideshow about good news.

Story: {title}
Summary: {summary}

Return ONLY valid JSON with this exact shape, no markdown, no preamble:
{
  "slides": [
    { "slide": 1, "text": "..." },
    ...
  ],
  "caption": "caption with hashtags"
}

Rules:
- 4 to 6 slides
- First slide must hook in under 8 words
- Conversational, upbeat tone
- Last slide is always a follow CTA
- Caption must include #goodnews #upliftingnews #fyp
- Extract 3 keywords from the story for image search and include as "keywords": [...]
```

Parse the JSON response directly in Go using `encoding/json`.

---

## Random Delay Jitter (Go)

Add jitter before the pipeline runs to vary daily post time:

```go
import (
    "math/rand"
    "time"
)

func randomDelay(minMinutes, maxMinutes int) {
    n := rand.Intn((maxMinutes-minMinutes)*60) + minMinutes*60
    time.Sleep(time.Duration(n) * time.Second)
}

// Call at start of main()
randomDelay(0, 90) // posts sometime within a 90 minute window
```

Cron fires at a fixed time (e.g. 8am), Go sleeps a random 0–90 minutes before doing anything.

---

## Image Assembly (Python — assemble.py)

**Dependencies:** `pillow`, `requests`, `python-dotenv`

**Steps:**
1. Read `story.json`
2. For each slide, query **Pexels API** (free) with the story keywords
3. Download best matching image
4. Resize to 1080x1920 (TikTok vertical format)
5. Apply dark gradient overlay (bottom 40% of image) for text legibility
6. Render slide text centered in lower third using a clean sans-serif font
7. Save as `output/slide_N.jpg`

**Pexels API:** Free, no copyright issues, returns high quality photos. Get key at pexels.com/api.

**Font:** Download and commit a free font like Inter or Montserrat Bold to `/assets/font.ttf` — don't rely on system fonts for portability.

---

## TikTok Posting (Python — post.py)

Use **Playwright** with a persistent browser context to preserve login session across runs.

**Key implementation details:**

- Use `launch_persistent_context(user_data_dir="./tiktok_session")` — log in manually once, session persists forever
- Pass `args=["--disable-blink-features=AutomationControlled"]` to reduce detection
- Add `headless=False` initially during development; only switch to `headless=True` after confirming no detection issues. If running headless on a server, use Xvfb.
- Use `page.type(selector, text, delay=50)` not `fill()` — mimics human typing speed
- Add `random.randint(1000, 3000)` ms delays between major actions
- Navigate to `https://www.tiktok.com/upload`
- Use `set_input_files()` on the file input to upload slides
- Fill caption from `story.json`
- Click the Post button

**First run:** Run `post.py` manually, log into TikTok in the browser window that opens, then close. The session is saved to `tiktok_session/` and all future runs will be pre-authenticated.

---

## Environment Variables (.env)

```
GEMINI_API_KEY=
PEXELS_API_KEY=
```

---

## Cron Setup (macOS)

Edit crontab:
```bash
crontab -e
```

Add:
```bash
0 8 * * * cd /path/to/project && ./bin/pipeline >> logs/tiktok.log 2>&1
```

Go handles the random delay internally so the actual post time varies 0–90 minutes after 8am each day.

**Keep machine awake:** Use Amphetamine (Mac app) to prevent sleep during the posting window (8am–10am).

**Build the binary before setting cron:**
```bash
go build -o bin/pipeline main.go
```

---

## Go Dependencies

```
github.com/mmcdole/gofeed       # RSS parsing
github.com/joho/godotenv        # .env loading
```

HTTP calls to Gemini use stdlib `net/http`. No extra HTTP client library needed.

## Python Dependencies (requirements.txt)

```
playwright
pillow
requests
python-dotenv
```

Install Playwright browser after pip install:
```bash
playwright install chromium
```

---

## Cost Estimate

| Component | Daily Cost |
|---|---|
| RSS sourcing (feedparser) | Free |
| Slide copy (Gemini Flash) | ~$0.01 |
| Images (Pexels API) | Free |
| Image assembly (Pillow) | Free |
| Posting (Playwright) | Free |
| **Total** | **~$0.01/day** |

---

## Notes & Gotchas

- **TikTok UI changes will break post.py** — selectors may need updating periodically. Keep selectors in a single config dict at the top of the file for easy maintenance.
- **story.json and output/ are transient** — gitignore both, they're regenerated every run.
- **tiktok_session/ must never be committed** — contains auth cookies. Gitignore it.
- **Run the full pipeline manually end-to-end before setting cron** to confirm everything works together.
- **Log everything** — Go should log each stage (feeds fetched, story selected, AI call success, Python scripts exit codes) to make debugging cron failures easy.
