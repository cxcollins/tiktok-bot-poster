package ai

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

const (
	imageModel        = "gemini-3.1-flash-image"
	imageTimeout      = 90 * time.Second
	imageMaxRetries   = 2
	imageRetryBackoff = 60 * time.Second
	imageConcurrency  = 4
	imageWidth        = 1080
	imageHeight       = 1920
)

// GenerateImages generates a background image per slide and writes them to
// outputDir as bg_<slide>.png. Falls back to a solid-color placeholder after
// imageMaxRetries failed attempts so the pipeline never aborts on image errors.
func GenerateImages(slides []Slide, outputDir string) error {
	if len(slides) == 0 {
		return errors.New("no slides to generate images for")
	}
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return errors.New("GEMINI_API_KEY not set")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	// Clear any leftover backgrounds from previous runs.
	matches, _ := filepath.Glob(filepath.Join(outputDir, "bg_*.png"))
	for _, m := range matches {
		_ = os.Remove(m)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, imageConcurrency)
	)
	for _, s := range slides {
		wg.Add(1)
		sem <- struct{}{}
		go func(slide Slide) {
			defer wg.Done()
			defer func() { <-sem }()

			outPath := filepath.Join(outputDir, fmt.Sprintf("bg_%d.png", slide.Slide))
			data, err := generateOneImageWithRetry(apiKey, slide)
			if err != nil {
				log.Printf("image: slide %d failed after retries, using placeholder: %v", slide.Slide, err)
				if perr := writePlaceholder(outPath); perr != nil {
					log.Printf("image: slide %d placeholder write failed: %v", slide.Slide, perr)
				}
				return
			}
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				log.Printf("image: slide %d write failed: %v", slide.Slide, err)
				return
			}
			log.Printf("image: slide %d written", slide.Slide)
		}(s)
	}
	wg.Wait()
	return nil
}

func generateOneImageWithRetry(apiKey string, slide Slide) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= imageMaxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("image: slide %d retry %d/%d after %s", slide.Slide, attempt, imageMaxRetries, imageRetryBackoff)
			time.Sleep(imageRetryBackoff)
		}
		data, err := generateOneImage(apiKey, slide)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func generateOneImage(apiKey string, slide Slide) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), imageTimeout)
	defer cancel()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}

	prompt := buildImagePrompt(slide)
	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
	}

	resp, err := client.Models.GenerateContent(ctx, imageModel, genai.Text(prompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, errors.New("no candidates returned")
	}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return part.InlineData.Data, nil
		}
	}
	return nil, errors.New("no image data in response")
}

func buildImagePrompt(slide Slide) string {
	keywords := strings.Join(slide.Keywords, ", ")
	log.Printf("build image prompt constructed, copy is: %s", slide)

	return fmt.Sprintf(
		"%s. Visual elements: %s. Photorealistic, cinematic lighting, "+
			"vertical 9:16 composition, shot on a high-end camera, "+
			"no text, no captions, no logos, no watermarks.",
		slide.Text, keywords,
	)
}

// writePlaceholder writes a solid-color PNG so the slide still renders if
// image generation fails entirely. Color is a deep navy that pairs reasonably
// with the white text overlay.
func writePlaceholder(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, imageWidth, imageHeight))
	bg := color.RGBA{R: 20, G: 30, B: 60, A: 255}
	for y := 0; y < imageHeight; y++ {
		for x := 0; x < imageWidth; x++ {
			img.Set(x, y, bg)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
