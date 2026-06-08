"""Assemble TikTok slideshow images from story.json.

Reads story.json (written by Go), fetches keyword-matched photos from Pexels,
overlays slide text on a 1080x1920 canvas, and writes output/slide_N.jpg.
"""
from __future__ import annotations

import io
import json
import os
import random
import sys
import textwrap
from pathlib import Path

import requests
from dotenv import load_dotenv
from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = Path(__file__).resolve().parent
STORY_PATH = ROOT / "story.json"
OUTPUT_DIR = ROOT / "output"
FONT_PATH = ROOT / "assets" / "font.ttf"

CANVAS = (1080, 1920)
PEXELS_ENDPOINT = "https://api.pexels.com/v1/search"


def load_font(size: int) -> ImageFont.FreeTypeFont:
    if FONT_PATH.exists():
        return ImageFont.truetype(str(FONT_PATH), size=size)
    # Fallback so dev runs work before a font is committed.
    print(f"warn: {FONT_PATH} missing, falling back to default font", file=sys.stderr)
    return ImageFont.load_default()


def fetch_pexels_image(query: str, api_key: str) -> Image.Image | None:
    """Search Pexels for `query` and return a randomly chosen result as PIL Image."""
    try:
        resp = requests.get(
            PEXELS_ENDPOINT,
            headers={"Authorization": api_key},
            params={"query": query, "per_page": 15, "orientation": "portrait"},
            timeout=15,
        )
        resp.raise_for_status()
        photos = resp.json().get("photos", [])
        if not photos:
            return None
        photo = random.choice(photos)
        src = photo["src"].get("portrait") or photo["src"].get("large2x") or photo["src"]["large"]
        img_resp = requests.get(src, timeout=20)
        img_resp.raise_for_status()
        return Image.open(io.BytesIO(img_resp.content)).convert("RGB")
    except Exception as exc:
        print(f"pexels: query {query!r} failed: {exc}", file=sys.stderr)
        return None


def cover_resize(img: Image.Image, size: tuple[int, int]) -> Image.Image:
    """Resize to fully cover `size` (CSS object-fit: cover), then center-crop."""
    target_w, target_h = size
    scale = max(target_w / img.width, target_h / img.height)
    new_size = (int(img.width * scale) + 1, int(img.height * scale) + 1)
    img = img.resize(new_size, Image.LANCZOS)
    left = (img.width - target_w) // 2
    top = (img.height - target_h) // 2
    return img.crop((left, top, left + target_w, top + target_h))


def gradient_overlay(size: tuple[int, int]) -> Image.Image:
    """Black gradient that fades in over the bottom 40% of the canvas."""
    w, h = size
    overlay = Image.new("RGBA", size, (0, 0, 0, 0))
    start_y = int(h * 0.6)
    pixels = overlay.load()
    for y in range(start_y, h):
        # Linear ramp 0 → 200 alpha across the bottom 40%.
        alpha = int(200 * (y - start_y) / (h - start_y))
        for x in range(w):
            pixels[x, y] = (0, 0, 0, alpha)
    return overlay


def wrap_text(text: str, font: ImageFont.FreeTypeFont, max_width: int) -> list[str]:
    """Greedy word wrap that respects pixel width."""
    words = text.split()
    if not words:
        return []
    lines: list[str] = []
    current = words[0]
    for word in words[1:]:
        candidate = f"{current} {word}"
        if font.getlength(candidate) <= max_width:
            current = candidate
        else:
            lines.append(current)
            current = word
    lines.append(current)
    return lines


def draw_text(canvas: Image.Image, text: str) -> None:
    draw = ImageDraw.Draw(canvas)
    w, h = canvas.size
    margin = 80
    max_width = w - 2 * margin

    # Pick a font size that yields at most ~6 lines.
    for size in (88, 80, 72, 64, 56):
        font = load_font(size)
        lines = wrap_text(text, font, max_width)
        if len(lines) <= 6:
            break

    line_height = int(size * 1.25)
    block_height = line_height * len(lines)
    # Anchor the text in the lower third of the slide.
    start_y = int(h * 0.62) + (int(h * 0.30) - block_height) // 2

    for i, line in enumerate(lines):
        line_w = font.getlength(line)
        x = (w - line_w) // 2
        y = start_y + i * line_height
        # Soft shadow for legibility on busy photos.
        for dx, dy in ((-2, 0), (2, 0), (0, -2), (0, 2)):
            draw.text((x + dx, y + dy), line, font=font, fill=(0, 0, 0))
        draw.text((x, y), line, font=font, fill=(255, 255, 255))


def render_slide(background: Image.Image | None, text: str, out_path: Path) -> None:
    if background is None:
        # Fallback: solid gradient if Pexels fails.
        canvas = Image.new("RGB", CANVAS, (20, 30, 60))
    else:
        canvas = cover_resize(background, CANVAS)
        # A subtle blur keeps text readable on busy photos.
        canvas = canvas.filter(ImageFilter.GaussianBlur(radius=1))

    overlay = gradient_overlay(CANVAS)
    canvas = Image.alpha_composite(canvas.convert("RGBA"), overlay).convert("RGB")
    draw_text(canvas, text)
    canvas.save(out_path, "JPEG", quality=92)
    print(f"wrote {out_path.relative_to(ROOT)}")


def main() -> int:
    load_dotenv(ROOT / ".env")
    api_key = os.environ.get("PEXELS_API_KEY")
    if not api_key:
        print("PEXELS_API_KEY not set", file=sys.stderr)
        return 1

    if not STORY_PATH.exists():
        print(f"missing {STORY_PATH}", file=sys.stderr)
        return 1

    story = json.loads(STORY_PATH.read_text())
    slides = story.get("slides") or []
    if not slides:
        print("story.json has no slides", file=sys.stderr)
        return 1

    OUTPUT_DIR.mkdir(exist_ok=True)
    # Clear any leftovers from previous runs so we don't upload stale images.
    for old in OUTPUT_DIR.glob("slide_*.jpg"):
        old.unlink()

    for entry in slides:
        idx = entry.get("slide", 0)
        text = entry.get("text", "")
        keywords = entry.get("keywords") or [text or "good news"]
        query = random.choice(keywords)
        bg = fetch_pexels_image(query, api_key)
        out_path = OUTPUT_DIR / f"slide_{idx}.jpg"
        render_slide(bg, text, out_path)

    print(f"assembled {len(slides)} slides")
    return 0


if __name__ == "__main__":
    sys.exit(main())
