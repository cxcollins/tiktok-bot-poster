package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"tiktok-bot-poster/rss"
)

const (
	geminiModel   = "gemini-3.5-flash"
	geminiTimeout = 30 * time.Second
	SlideCount    = 8
)

// Slide is one story rendered as a single TikTok slide.
type Slide struct {
	Slide    int      `json:"slide"`
	Text     string   `json:"text"`
	Source   string   `json:"source"`
	Keywords []string `json:"keywords"`
}

// Script is the structure we hand off to Python via story.json.
type Script struct {
	Slides  []Slide `json:"slides"`
	Caption string  `json:"caption"`
}

// modelSlide is what Gemini returns per story.
type modelSlide struct {
	Slide    int      `json:"slide"`
	Text     string   `json:"text"`
	Keywords []string `json:"keywords"`
}

type modelOutput struct {
	Slides  []modelSlide `json:"slides"`
	Caption string       `json:"caption"`
}

// GenerateScript calls Gemini Flash to turn N good-news stories into one slide each.
func GenerateScript(stories []rss.Story) (*Script, error) {
	if len(stories) == 0 {
		return nil, errors.New("no stories provided")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("GEMINI_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), geminiTimeout)
	defer cancel()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}

	prompt := buildPrompt(stories)
	temp := float32(0.8)
	config := &genai.GenerateContentConfig{
		Temperature:      &temp,
		ResponseMIMEType: "application/json",
	}

	resp, err := client.Models.GenerateContent(ctx, geminiModel, genai.Text(prompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini generate: %w", err)
	}

	text := stripCodeFence(strings.TrimSpace(resp.Text()))
	if text == "" {
		return nil, errors.New("gemini returned no text")
	}

	var out modelOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("decode model JSON: %w (raw: %s)", err, text)
	}
	if len(out.Slides) == 0 {
		return nil, errors.New("model returned zero slides")
	}

	// Attach source attribution back to each slide by index alignment with the input.
	slides := make([]Slide, 0, len(out.Slides))
	for _, m := range out.Slides {
		i := m.Slide - 1
		source := ""
		if i >= 0 && i < len(stories) {
			source = stories[i].Source
		}
		slides = append(slides, Slide{
			Slide:    m.Slide,
			Text:     m.Text,
			Source:   source,
			Keywords: m.Keywords,
		})
	}

	return &Script{
		Slides:  slides,
		Caption: out.Caption,
	}, nil
}

func buildPrompt(stories []rss.Story) string {
	var b strings.Builder
	b.WriteString("You are writing copy for a TikTok photo slideshow of good news headlines.\n")
	b.WriteString("Each slide is ONE separate story. Write a short, punchy hook for each.\n\n")
	b.WriteString("Stories:\n")
	for i, s := range stories {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, s.Title, s.Summary)
	}
	fmt.Fprintf(&b, `
Return ONLY valid JSON with this exact shape, no markdown, no preamble:
{
  "slides": [
    { "slide": 1, "text": "...", "keywords": ["...", "...", "..."] }
  ],
  "caption": "caption with hashtags"
}

Rules:
- Produce exactly %d slides, one per numbered story above, in the same order
- Each "text" is a single conversational hook under 14 words
- Upbeat, no clickbait, no quotation marks around the text
- For each slide include 3 concrete visual keywords for image search (objects, places, subjects — not abstract words)
- Caption must include #goodnews #upliftingnews #fyp`, len(stories))
	return b.String()
}

// stripCodeFence removes ```json ... ``` wrappers if the model returns them
// despite being asked not to.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
