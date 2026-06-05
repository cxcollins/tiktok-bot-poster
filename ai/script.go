package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"tiktok-bot-poster/rss"
)

const (
	geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	geminiTimeout  = 30 * time.Second
	SlideCount     = 8
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

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig map[string]interface{} `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
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

	prompt := buildPrompt(stories)

	body := geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: map[string]interface{}{
			"temperature":      0.8,
			"responseMimeType": "application/json",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), geminiTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", geminiEndpoint+"?key="+apiKey, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// TODO(connor): evaluate changing this to a include a timeout, maybe use retry logic?
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini status %d: %s", resp.StatusCode, string(raw))
	}

	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, fmt.Errorf("decode gemini envelope: %w", err)
	}
	if gr.Error != nil {
		return nil, fmt.Errorf("gemini error: %s", gr.Error.Message)
	}
	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return nil, errors.New("gemini returned no candidates")
	}

	text := strings.TrimSpace(gr.Candidates[0].Content.Parts[0].Text)
	text = stripCodeFence(text)

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
