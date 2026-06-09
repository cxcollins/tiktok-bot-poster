# TikTok Good News Bot — Project Spec

## Overview

A fully automated pipeline that sources good news stories daily, picks the most compelling of them, generates one slide per story, and uploads them to TikTok as a photo carousel post. Each post is 8 slides; each slide is one separate story. Goal: maximum automation at minimal cost (~$0.04/day).

---

## Architecture

```
Go (orchestrator)
  → goroutines crawl RSS feeds concurrently (filtered to past 7 days)
  → rss.Deduplicate strips dupes + sorts by recency
  → Gemini Flash API #1: pick the 8 most compelling stories
  → Gemini Flash API #2: write one hook + 3 image keywords per story
  → Gemini 3.1 Flash Image API #3: generate one bg per slide (parallel,
      with retry; falls back to a solid-color placeholder)
  → random delay jitter (60–90 min window)
  → exec: python assemble.py
  → exec: python post.py
  → log result

Python assemble.py
  → reads story.json written by Go
  → for each slide, opens output/bg_<n>.png written by Go
  → Pillow overlays gradient + text on a 1080x1920 canvas
  → writes output/slide_1.jpg ... slide_8.jpg

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
  python/
    runner.go              # Go shim that execs the Python scripts
    assemble.py            # Pillow image assembly
    post.py                # Playwright TikTok uploader
  rss/
    feeds.go               # RSS feed URLs + fetching logic (7-day age cap)
    dedupe.go              # Deduplication + recency sort
  ai/
    select.go              # Gemini call: pick the 8 most compelling stories
    script.go              # Gemini call: write one hook + keywords per slide
    image.go               # Gemini Image call: per-slide background (parallel + retry)
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
- Drop any items older than 7 days at parse time; skip items with no parseable date
- Deduplicate by URL (falling back to title) and sort by recency

**story.json shape written by Go:**
```json
{
  "slides": [
    {
      "slide": 1,
      "text": "Hook line for story 1 🌊",
      "source": "Good News Network",
      "keywords": ["ocean", "turtle", "beach"]
    },
    {
      "slide": 2,
      "text": "Hook line for story 2 🐝",
      "source": "Positive News",
      "keywords": ["bees", "flowers", "meadow"]
    }
  ],
  "caption": "#goodnews #upliftingnews #fyp"
}
```

Each slide is one independent story. Posts contain exactly 8 slides (see `ai.SlideCount`).

---

## AI Story Selection (Go → Gemini Flash)

After dedupe, the candidate pool can be 50–200 stories. `ai.SelectTopStories` sends their titles + summaries to Gemini and asks it to return the 8 most compelling/inspiring, by 1-indexed position.

**Prompt template:**
```
You are curating good news stories for a TikTok slideshow.
Pick the {n} MOST compelling and inspiring stories from the numbered list below.

Prioritize: human kindness, scientific breakthroughs, environmental wins,
underdog stories, and uplifting moments that would make a viewer smile or feel hopeful.
Avoid: minor local news, vague feel-good filler, anything that requires deep context to appreciate.

Candidates:
1. {title} — {summary}
2. ...

Return ONLY valid JSON: { "picks": [3, 17, 1, ...] }
- Exactly {n} numbers
- Each number is a story's position from the list above (1-indexed)
- Order from most to least compelling
```

---

## AI Slide Copy (Go → Gemini Flash)

`ai.GenerateScript` takes the 8 selected stories and asks Gemini for a hook + 3 image keywords per story in a single call.

**Prompt template:**
```
You are writing copy for a TikTok photo slideshow of good news headlines.
Each slide is ONE separate story. Write a short, punchy hook for each.

Stories:
1. {title}
   {summary}
2. ...

Return ONLY valid JSON with this exact shape, no markdown, no preamble:
{
  "slides": [
    { "slide": 1, "text": "...", "keywords": ["...", "...", "..."] }
  ],
  "caption": "caption with hashtags"
}

Rules:
- Produce exactly {n} slides, one per numbered story above, in the same order
- Each "text" is a single conversational hook under 14 words
- Upbeat, no clickbait, no quotation marks around the text
- For each slide include 3 concrete visual keywords for image search
  (objects, places, subjects — not abstract words)
- Caption must include #goodnews #upliftingnews #fyp
```

Both calls use stdlib `net/http` and parse the JSON response with `encoding/json`.

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

## Image Generation (Go → Gemini 3.1 Flash Image / "nano banana")

`ai.GenerateImages` runs after the script is generated and before assemble.py is execed. It produces one 9:16 background per slide, in parallel.

**Behavior:**
- Concurrency cap: `imageConcurrency = 4` goroutines at a time (free tier is 10 req/min — this leaves headroom).
- Retry: on failure, wait 60s and retry up to `imageMaxRetries = 2` times (3 attempts total).
- Fallback: if all attempts fail, write a solid navy-blue PNG placeholder at the same path so the slide still renders.
- Output: `output/bg_<n>.png` for slide n.

**Prompt template (per slide):**
```
{slide.Text}. Visual elements: {kw1}, {kw2}, {kw3}. Photorealistic,
cinematic lighting, vertical 9:16 composition, shot on a high-end camera,
no text, no captions, no logos, no watermarks.
```

The "no text/captions/logos" suffix matters — assemble.py adds the slide hook on top in Pillow, and Gemini will otherwise sometimes burn its own caption into the image.

---

## Image Assembly (Python — assemble.py)

**Dependencies:** `pillow`

**Steps:**
1. Read `story.json`
2. For each slide, open `output/bg_<n>.png` (written by Go in the previous stage)
3. Cover-resize to 1080x1920 (TikTok vertical format) and apply a soft blur
4. Apply dark gradient overlay (bottom 40% of image) for text legibility
5. Render slide text centered in lower third using a clean sans-serif font
6. Save as `output/slide_N.jpg`

If Go's image stage failed entirely and no `bg_<n>.png` exists, assemble.py falls back to a solid-color canvas.

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
google.golang.org/genai         # Gemini API client (text + image)
```

## Python Dependencies (requirements.txt)

```
playwright
pillow
```

Install Playwright browser after pip install:
```bash
playwright install chromium
```

---

## Cost Estimate

| Component | Daily Cost |
|---|---|
| RSS sourcing (gofeed) | Free |
| Story selection (Gemini Flash) | ~$0.01 |
| Slide copy (Gemini Flash) | ~$0.01 |
| Images (Gemini 3.1 Flash Image, free tier) | Free |
| Image assembly (Pillow) | Free |
| Posting (Playwright) | Free |
| **Total** | **~$0.02/day** |

---

## Notes & Gotchas

- **TikTok UI changes will break post.py** — selectors may need updating periodically. Keep selectors in a single config dict at the top of the file for easy maintenance.
- **story.json and output/ are transient** — gitignore both, they're regenerated every run.
- **tiktok_session/ must never be committed** — contains auth cookies. Gitignore it.
- **Run the full pipeline manually end-to-end before setting cron** to confirm everything works together.
- **Log everything** — Go should log each stage (feeds fetched, story selected, AI call success, Python scripts exit codes) to make debugging cron failures easy.
