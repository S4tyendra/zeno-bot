package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

const credPath = "/home/satya/Downloads/massive-acrobat-324913-b78905fc65a0.json"

func main() {
	ctx := context.Background()
	raw, err := os.ReadFile(credPath)
	if err != nil {
		log.Fatal(err)
	}
	creds, err := credentials.NewCredentialsFromJSON(credentials.ServiceAccount, raw, &credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		log.Fatal(err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     "massive-acrobat-324913",
		Location:    "global",
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		log.Fatal(err)
	}

	model := "gemini-3.5-flash-lite"
	if len(os.Args) > 1 {
		model = os.Args[1]
	}
	prompt := `Alice, Bob, and Carol each live in a different house: red, green, and blue.
The person in the red house owns a cat.
Bob does not live in the green house.
Carol owns a dog.
The green house is to the left of the red house.
Alice does not own a cat.
Who lives in each house, and what pet do they own?`

	cfg := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   genai.ThinkingLevelHigh,
		},
	}

	log.Printf("streaming %s thinking=HIGH include_thoughts=true", model)
	start := time.Now()
	var thoughts, answer strings.Builder
	chunks := 0
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	for chunk, err := range client.Models.GenerateContentStream(ctx, model, genai.Text(prompt), cfg) {
		if err != nil {
			log.Fatalf("stream: %v", err)
		}
		chunks++
		if chunk == nil || len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
			continue
		}
		for _, p := range chunk.Candidates[0].Content.Parts {
			if p.Text == "" {
				continue
			}
			if p.Thought {
				thoughts.WriteString(p.Text)
				fmt.Printf("[THOUGHT] %q\n", clip(p.Text, 120))
			} else {
				answer.WriteString(p.Text)
				fmt.Printf("[TEXT] %q\n", clip(p.Text, 120))
			}
		}
	}

	fmt.Printf("\n===== chunks=%d thoughts=%d answer=%d elapsed=%s =====\n",
		chunks, thoughts.Len(), answer.Len(), time.Since(start).Round(time.Millisecond))
	if thoughts.Len() > 0 {
		fmt.Printf("--- THOUGHTS ---\n%s\n", thoughts.String())
	}
	if answer.Len() > 0 {
		fmt.Printf("--- ANSWER ---\n%s\n", answer.String())
	}
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
