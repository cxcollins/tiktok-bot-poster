# Program Flow

High-level walkthrough of one full pipeline run, from cron firing to a posted TikTok.
Each step links to the file and lines that implement it.

---

## At a glance

```
cron (8am)
  └─ ./bin/pipeline                                       ← Go binary, single entry point
       ├─ 1. random delay jitter        (0–90 min)        ← spreads post time across the morning
       ├─ 2. crawl 13 RSS feeds         (concurrent)      ← good news sources + Reddit + science
       ├─ 3. dedupe + rank by recency                     ← pick the freshest unique story
       ├─ 4. Gemini 2.0 Flash call                        ← turn (title, summary) into slide JSON
       ├─ 5. write story.json                             ← handoff file to Python
       ├─ 6. exec python3 assemble.py                     ← Pexels images + Pillow text overlay
       │      └─ writes output/slide_1.jpg … slide_N.jpg
       └─ 7. exec python3 post.py                         ← Playwright drives TikTok upload
              └─ uses ./tiktok_session/ for persistent login
```

Two languages, one handoff file. Go does everything that benefits from concurrency, typed JSON, and a single static binary (orchestration, networking, AI). Python does everything where the library ecosystem is unbeatable (Pillow for image composition, Playwright for browser automation).

---

## Step 1 — Random delay

Cron fires at a fixed time, but every account that posts at exactly 8:00:00 every day looks like a bot. So Go sleeps a random 0–90 minutes before doing anything.

- [main.go:34](main.go#L34) — call site at start of `main()`
- [main.go:78-88](main.go#L78-L88) — `randomDelay()` implementation, uses `math/rand` seeded from wall-clock
- The `--jitter` flag ([main.go:23](main.go#L23)) lets you set max jitter to 0 during development so you don't have to wait

---

## Step 2 — Crawl RSS feeds concurrently

13 feeds, fetched in parallel with goroutines. Each goroutine has a 5-second HTTP timeout, and a semaphore caps parallelism at 10 in-flight requests.

- [rss/feeds.go:13-32](rss/feeds.go#L13-L32) — the feed list (good news pubs, Reddit, science)
- [rss/feeds.go:42-45](rss/feeds.go#L42-L45) — `maxConcurrency = 10`, `httpTimeout = 5s`
- [rss/feeds.go:49-78](rss/feeds.go#L49-L78) — `FetchAll()`: `sync.WaitGroup` + buffered channel acting as a semaphore + `sync.Mutex` guarding the shared results slice
- [rss/feeds.go:80-122](rss/feeds.go#L80-L122) — `fetchOne()`: per-feed HTTP call + `gofeed` parse, returns a flat `[]Story`

Failed feeds log a warning and are skipped — one bad feed doesn't take down the run.

---

## Step 3 — Deduplicate and rank

A single story can appear across multiple feeds (e.g. Reddit + the original publisher). We dedupe by URL and pick the most recent.

- [rss/rank.go:9-32](rss/rank.go#L9-L32) — `Rank()`: dedupe by lowercased URL, then sort by `Published` descending
- [rss/rank.go:34-40](rss/rank.go#L34-L40) — `Top()`: returns index 0 (the freshest), or `false` if the list is empty
- [main.go:37-42](main.go#L37-L42) — call site, fatals if no stories at all

---

## Step 4 — Gemini Flash generates slide copy

We send the chosen story's title and summary to Gemini 2.0 Flash with a strict JSON-mode response. The model returns 4–6 slides of copy, a caption with hashtags, and 3 keywords for image search.

- [ai/script.go:21-34](ai/script.go#L21-L34) — `Slide` and `Script` types — `Script` is what gets written to `story.json`
- [ai/script.go:66-139](ai/script.go#L66-L139) — `GenerateScript()`: builds the request, posts to Gemini, decodes the envelope, then decodes the model's JSON content
- [ai/script.go:76-79](ai/script.go#L76-L79) — `responseMimeType: "application/json"` forces JSON-mode so the model can't return prose
- [ai/script.go:141-163](ai/script.go#L141-L163) — `buildPrompt()`: the prompt template (8-word hook, 4–6 slides, follow CTA, hashtags)
- [ai/script.go:167-176](ai/script.go#L167-L176) — `stripCodeFence()`: defensive — strips ```` ```json ```` wrappers if the model adds them anyway
- [main.go:101-121](main.go#L101-L121) — `cleanSummary()` strips HTML before sending to Gemini (Reddit RSS summaries are HTML-heavy)

---

## Step 5 — Write story.json (the handoff)

Go and Python don't share memory. The JSON file on disk is the contract between them.

- [main.go:51-55](main.go#L51-L55) — call site
- [main.go:90-99](main.go#L90-L99) — `writeStory()`, uses `json.NewEncoder` with indent for readability when debugging
- File shape (matches `Script` struct):

```json
{
  "title": "Story headline",
  "summary": "Cleaned 1-paragraph summary",
  "source": "Good News Network",
  "keywords": ["ocean", "conservation", "wildlife"],
  "slides": [
    { "slide": 1, "text": "Hook line here 🌊" },
    { "slide": 2, "text": "Supporting detail..." }
  ],
  "caption": "#goodnews #upliftingnews #fyp #positive"
}
```

`story.json` is gitignored — it's regenerated every run.

---

## Step 6 — Assemble slides (Python)

Go execs `python3 assemble.py`. The script reads `story.json`, fetches a Pexels photo for each slide using a randomly-chosen keyword, and composites text on top.

- [tiktok/post.go:11-19](tiktok/post.go#L11-L19) — Go's `RunPython()` wrapper, pipes stdout/stderr through so logs are visible
- [tiktok/post.go:22-24](tiktok/post.go#L22-L24) — `Assemble()` shorthand that calls `RunPython("assemble.py")`
- [main.go:57-65](main.go#L57-L65) — orchestrator call site, with `--skip-assemble` flag for dev

Inside `assemble.py`:

- [assemble.py:147-179](assemble.py#L147-L179) — `main()`: load env, read `story.json`, clear stale slides, render each slide
- [assemble.py:37-57](assemble.py#L37-L57) — `fetch_pexels_image()`: `GET /v1/search`, picks a random portrait result of the top 15
- [assemble.py:60-68](assemble.py#L60-L68) — `cover_resize()`: scales-to-fill then center-crops to 1080×1920 (the equivalent of CSS `object-fit: cover`)
- [assemble.py:71-82](assemble.py#L71-L82) — `gradient_overlay()`: draws an alpha gradient over the bottom 40% so white text reads cleanly
- [assemble.py:85-100](assemble.py#L85-L100) — `wrap_text()`: pixel-aware greedy word wrap
- [assemble.py:103-128](assemble.py#L103-L128) — `draw_text()`: picks the largest font size that fits in 6 lines, anchors text in the lower third, paints a 4-direction shadow then white fill
- [assemble.py:131-144](assemble.py#L131-L144) — `render_slide()`: ties it together — Pexels image → cover-resize → blur → gradient → text → JPEG

Outputs land in `output/slide_1.jpg`, `output/slide_2.jpg`, etc. The directory is wiped at the start of each run ([assemble.py:166-168](assemble.py#L166-L168)) so we never upload stale frames.

The font lives at `assets/font.ttf`; if missing, Pillow falls back to a tiny bitmap font (legible enough to confirm rendering, ugly for production).

---

## Step 7 — Post to TikTok (Python)

Go execs `python3 post.py`. Playwright drives a real Chromium window, logged in via a persistent on-disk profile.

- [tiktok/post.go:27-29](tiktok/post.go#L27-L29) — `Post()` shorthand
- [main.go:67-75](main.go#L67-L75) — orchestrator call site, with `--skip-post` flag

Inside `post.py`:

- [post.py:28-34](post.py#L28-L34) — `SELECTORS` dict — **the single source of truth for TikTok's CSS selectors**. When TikTok changes their UI and the bot breaks, this is where you patch it.
- [post.py:103-126](post.py#L103-L126) — `main()`: reads caption from `story.json`, collects slides, launches `launch_persistent_context(user_data_dir="./tiktok_session")`
- [post.py:114-119](post.py#L114-L119) — Chromium flags: `--disable-blink-features=AutomationControlled` reduces obvious bot fingerprints
- [post.py:51-100](post.py#L51-L100) — `upload()`: navigate → set file input → wait for processing → type caption (with `delay=50` for human-like cadence) → click Post → wait for navigation away
- [post.py:37-38](post.py#L37-L38) — `human_pause()`: random 1–3s gaps between major actions, sprinkled throughout
- [post.py:41-48](post.py#L41-L48) — `collect_slides()`: globs `output/slide_*.jpg` and sorts numerically (so `slide_10.jpg` doesn't sort before `slide_2.jpg`)

**First run is interactive.** You run `python post.py` once with `story.json` and `output/` already populated, log into TikTok in the Chromium window that opens, then close it. The cookies persist in `./tiktok_session/` and every cron run after that is pre-authenticated.

`tiktok_session/` is gitignored — it contains your auth cookies.

---

## Why this split

| Concern | Language | Why |
|---|---|---|
| Cron + scheduling + jitter | Go | Single static binary, no runtime deps for cron to load |
| Concurrent network I/O (RSS) | Go | Goroutines are the right tool; semaphore + WaitGroup is idiomatic |
| HTTP + JSON to Gemini | Go | `net/http` + `encoding/json` are stdlib; types catch shape errors at compile time |
| Image composition | Python | Pillow is the dominant library; nothing in Go matches it |
| Browser automation | Python | Playwright's Python and Node bindings are the most polished; Go bindings exist but lag |

The `story.json` file is the seam. Either side can be rewritten or replaced without touching the other.

---

## Failure modes to know

1. **A feed is down** — logged, skipped. The other 12 still run. ([rss/feeds.go:65-67](rss/feeds.go#L65-L67))
2. **Gemini returns malformed JSON** — `GenerateScript` returns an error and the run fatals before any TikTok call. ([ai/script.go:124-126](ai/script.go#L124-L126))
3. **Pexels query returns zero results** — `render_slide` falls back to a solid dark-blue background instead of failing the slide. ([assemble.py:131-134](assemble.py#L131-L134))
4. **TikTok changes a selector** — `post.py` will fail with a Playwright timeout. Fix by updating [post.py:28-34](post.py#L28-L34).
5. **TikTok session expires** — re-run `python post.py` manually, log in again. The persistent context picks up where it left off.
6. **Cron silent failures** — cron line redirects both streams to `logs/tiktok.log` (`>> logs/tiktok.log 2>&1`); Go logs every stage with timestamps ([main.go:28](main.go#L28)).
