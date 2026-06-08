package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"

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

	ctx, cancel := context.WithTimeout(context.Background(), geminiTimeout)
	defer cancel()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}

	prompt := buildSelectPrompt(candidates, n)
	temp := float32(0.4)
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
