"""Upload assembled slides to TikTok via Playwright.

Uses a persistent browser context (./tiktok_session) so login survives across
runs. The very first time you run this, log in manually in the window that
opens — subsequent runs will be pre-authenticated.

Run manually first:
    python post.py
"""
from __future__ import annotations

import json
import random
import sys
import time
from pathlib import Path

from playwright.sync_api import Page, TimeoutError as PWTimeoutError, sync_playwright

ROOT = Path(__file__).resolve().parent.parent
STORY_PATH = ROOT / "story.json"
OUTPUT_DIR = ROOT / "output"
SESSION_DIR = ROOT / "tiktok_session"

UPLOAD_URL = "https://www.tiktok.com/upload"

# Centralized selectors so they can be patched fast when TikTok changes its UI.
SELECTORS = {
    "file_input": "input[type=file]",
    # The caption editor is a contenteditable div inside the upload page.
    "caption": "div[contenteditable='true']",
    # The Post button — TikTok labels it "Post" in English; aria-label is more stable than text.
    "post_button": "button[data-e2e='post_video_button'], button:has-text('Post')",
}


def human_pause(min_ms: int = 1000, max_ms: int = 3000) -> None:
    time.sleep(random.randint(min_ms, max_ms) / 1000)


def collect_slides() -> list[Path]:
    slides = sorted(
        OUTPUT_DIR.glob("slide_*.jpg"),
        key=lambda p: int(p.stem.split("_")[1]),
    )
    if not slides:
        raise SystemExit(f"no slides found in {OUTPUT_DIR}")
    return slides


def upload(page: Page, slides: list[Path], caption: str) -> None:
    page.goto(UPLOAD_URL, wait_until="domcontentloaded")
    human_pause(2000, 4000)

    # The upload page sometimes loads the file input inside an iframe.
    file_input = page.locator(SELECTORS["file_input"]).first
    try:
        file_input.wait_for(state="attached", timeout=15_000)
    except PWTimeoutError:
        for frame in page.frames:
            if frame.locator(SELECTORS["file_input"]).count() > 0:
                file_input = frame.locator(SELECTORS["file_input"]).first
                break
        else:
            raise SystemExit("could not find file input on upload page")

    file_input.set_input_files([str(p) for p in slides])
    print(f"uploaded {len(slides)} slides, waiting for TikTok to process")
    human_pause(8000, 12_000)

    # Caption box — TikTok renders it in a Lexical-style editor.
    caption_box = page.locator(SELECTORS["caption"]).first
    try:
        caption_box.wait_for(state="visible", timeout=20_000)
    except PWTimeoutError:
        for frame in page.frames:
            if frame.locator(SELECTORS["caption"]).count() > 0:
                caption_box = frame.locator(SELECTORS["caption"]).first
                break

    caption_box.click()
    human_pause(500, 1500)
    # Clear any default placeholder content.
    page.keyboard.press("Control+A")
    page.keyboard.press("Delete")
    caption_box.type(caption, delay=50)
    human_pause(2000, 4000)

    post_button = page.locator(SELECTORS["post_button"]).first
    post_button.wait_for(state="visible", timeout=20_000)
    human_pause(1000, 2500)
    post_button.click()
    print("clicked Post — waiting for confirmation")

    # Wait for navigation away from the upload page or a success indicator.
    try:
        page.wait_for_url(lambda url: "/upload" not in url, timeout=60_000)
        print("post confirmed — TikTok navigated away from upload page")
    except PWTimeoutError:
        print("warn: did not detect navigation, post may have failed", file=sys.stderr)


def main() -> int:
    if not STORY_PATH.exists():
        print(f"missing {STORY_PATH}", file=sys.stderr)
        return 1
    story = json.loads(STORY_PATH.read_text())
    caption = story.get("caption", "#goodnews #upliftingnews #fyp")
    slides = collect_slides()

    SESSION_DIR.mkdir(exist_ok=True)

    with sync_playwright() as pw:
        context = pw.chromium.launch_persistent_context(
            user_data_dir=str(SESSION_DIR),
            headless=False,
            args=["--disable-blink-features=AutomationControlled"],
            viewport={"width": 1280, "height": 900},
        )
        page = context.new_page()
        try:
            upload(page, slides, caption)
        finally:
            human_pause(3000, 5000)
            context.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
