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

	"tiktok-bot-poster/rss"
)

// selectResponse is what Gemini returns when picking top stories.
type selectResponse struct {
	Picks []int `json:"picks"`
}

// SelectTopStories asks Gemini to pick the n most compelling/inspiring stories
// from the candidate pool. Returns the selected stories in the order Gemini ranked them.
func SelectTopStories(candidates []rss.Story, n int) ([]rss.Story, error) {
	if len(candidates) == 0 {
		return nil, errors.New("no candidates provided")
	}
	if n <= 0 {
		return nil, errors.New("n must be positive")
	}
	if len(candidates) <= n {
		return candidates, nil
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("GEMINI_API_KEY not set")
	}

	prompt := buildSelectPrompt(candidates, n)

	body := geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: map[string]interface{}{
			"temperature":      0.4,
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

	text := stripCodeFence(strings.TrimSpace(gr.Candidates[0].Content.Parts[0].Text))

	var sel selectResponse
	if err := json.Unmarshal([]byte(text), &sel); err != nil {
		return nil, fmt.Errorf("decode select JSON: %w (raw: %s)", err, text)
	}

	picked := make([]rss.Story, 0, n)
	seen := make(map[int]struct{}, n)
	for _, idx := range sel.Picks {
		i := idx - 1
		if i < 0 || i >= len(candidates) {
			continue
		}
		if _, dup := seen[i]; dup {
			continue
		}
		seen[i] = struct{}{}
		picked = append(picked, candidates[i])
		if len(picked) == n {
			break
		}
	}
	if len(picked) == 0 {
		return nil, errors.New("model returned no valid picks")
	}
	return picked, nil
}

func buildSelectPrompt(candidates []rss.Story, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are curating good news stories for a TikTok slideshow. ")
	fmt.Fprintf(&b, "Pick the %d MOST compelling and inspiring stories from the numbered list below.\n\n", n)
	b.WriteString("Prioritize: human kindness, scientific breakthroughs, environmental wins, ")
	b.WriteString("underdog stories, and uplifting moments that would make a viewer smile or feel hopeful.\n")
	b.WriteString("Avoid: minor local news, vague feel-good filler, anything that requires deep context to appreciate.\n\n")
	b.WriteString("Candidates:\n")
	for i, s := range candidates {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, s.Title, truncate(s.Summary, 200))
	}
	fmt.Fprintf(&b, `
Return ONLY valid JSON with this exact shape, no markdown, no preamble:
{ "picks": [3, 17, 1, ...] }

Rules:
- Return exactly %d numbers
- Each number is a story's position from the list above (1-indexed)
- Order from most to least compelling`, n)
	return b.String()
}

func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
