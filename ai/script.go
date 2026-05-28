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
)

const (
	geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	geminiTimeout  = 30 * time.Second
)

type Slide struct {
	Slide int    `json:"slide"`
	Text  string `json:"text"`
}

// Script is the structure we hand off to Python via story.json.
type Script struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Source   string   `json:"source"`
	Keywords []string `json:"keywords"`
	Slides   []Slide  `json:"slides"`
	Caption  string   `json:"caption"`
}

// modelOutput is the JSON we expect Gemini to return.
type modelOutput struct {
	Slides   []Slide  `json:"slides"`
	Caption  string   `json:"caption"`
	Keywords []string `json:"keywords"`
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

// GenerateScript calls Gemini Flash to turn a (title, summary) into slide copy.
func GenerateScript(title, summary, source string) (*Script, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("GEMINI_API_KEY not set")
	}

	prompt := buildPrompt(title, summary)

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

	return &Script{
		Title:    title,
		Summary:  summary,
		Source:   source,
		Keywords: out.Keywords,
		Slides:   out.Slides,
		Caption:  out.Caption,
	}, nil
}

func buildPrompt(title, summary string) string {
	return fmt.Sprintf(`You are writing copy for a TikTok photo slideshow about good news.

Story: %s
Summary: %s

Return ONLY valid JSON with this exact shape, no markdown, no preamble:
{
  "slides": [
    { "slide": 1, "text": "..." }
  ],
  "caption": "caption with hashtags",
  "keywords": ["...", "...", "..."]
}

Rules:
- 4 to 6 slides
- First slide must hook in under 8 words
- Conversational, upbeat tone
- Last slide is always a follow CTA
- Caption must include #goodnews #upliftingnews #fyp
- Extract 3 keywords from the story for image search and include as "keywords": [...]`, title, summary)
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
