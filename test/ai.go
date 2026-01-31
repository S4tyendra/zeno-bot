package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

func main() {
	err := godotenv.Load("../.env")
	if err != nil {
		err = godotenv.Load(".env")
		if err != nil {
			log.Printf("Warning: .env file not found")
		}
	}

	apiKey := os.Getenv("AISTUDIO_API_KEY")
	if apiKey == "" {
		log.Fatal("AISTUDIO_API_KEY is not set")
	}

	imageModel := os.Getenv("IMAGE_MODEL")
	if imageModel == "" {
		imageModel = "gemini-2.5-flash-image"
	}

	prompt := "A cyberpunk style workspace with a glowing Samsung phone on a desk, blue neon tears leaking from the screen, high-tech aesthetic, dark moody lighting, 4k resolution"
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("Failed to create GenAI client: %v", err)
	}

	log.Printf("Testing model: %s", imageModel)
	log.Printf("Prompt: %s", prompt)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
	}

	resp, err := client.Models.GenerateContent(
		ctx,
		imageModel,
		genai.Text(prompt),
		config,
	)

	if err != nil {
		log.Fatalf("API call failed: %v", err)
	}

	fmt.Printf("\n--- Response Summary ---\n")
	fmt.Printf("Candidates: %d\n", len(resp.Candidates))

	for i, candidate := range resp.Candidates {
		fmt.Printf("\nCandidate %d:\n", i)
		fmt.Printf("  FinishReason: %v\n", candidate.FinishReason)
		fmt.Printf("  Parts: %d\n", len(candidate.Content.Parts))

		for j, part := range candidate.Content.Parts {
			fmt.Printf("  Part %d:\n", j)
			if part.Text != "" {
				fmt.Printf("    Text: %q\n", part.Text)
			}
			if part.InlineData != nil {
				fmt.Printf("    InlineData: MimeType=%s, DataLength=%d\n", part.InlineData.MIMEType, len(part.InlineData.Data))

				filename := fmt.Sprintf("test_output_%d_%d.png", i, j)
				err := os.WriteFile(filename, part.InlineData.Data, 0644)
				if err != nil {
					fmt.Printf("    Error saving file: %v\n", err)
				} else {
					fmt.Printf("    Saved to: %s\n", filename)
				}
			}
			if part.FileData != nil {
				fmt.Printf("    FileData: MIMEType=%s\n", part.FileData.MIMEType)
			}
		}

		if candidate.SafetyRatings != nil {
			fmt.Printf("  Safety Ratings:\n")
			for _, rating := range candidate.SafetyRatings {
				fmt.Printf("    %s: %s (Probability: %s)\n", rating.Category, rating.Blocked, rating.Probability)
			}
		}
	}
}
