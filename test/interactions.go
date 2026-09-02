package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

const (
	credPath = "/home/satya/Downloads/massive-acrobat-324913-b78905fc65a0.json"
	project  = "massive-acrobat-324913"
	location = "global"
	model    = "gemini-3.5-flash-lite"
)

func main() {
	_ = godotenv.Load("../.env")
	_ = godotenv.Load(".env")

	ctx := context.Background()
	raw, err := os.ReadFile(credPath)
	if err != nil {
		log.Fatalf("read creds: %v", err)
	}
	creds, err := credentials.NewCredentialsFromJSON(credentials.ServiceAccount, raw, &credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		log.Fatalf("parse creds: %v", err)
	}
	tok, err := creds.Token(ctx)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
	log.Printf("got access token (expires %s)", tok.Expiry.Format(time.RFC3339))

	url := fmt.Sprintf("https://aiplatform.googleapis.com/v1beta1/projects/%s/locations/%s/interactions", project, location)

	prompt := `Alice, Bob, and Carol each live in a different house on the same street: red, green, and blue.
The person who lives in the red house owns a cat.
Bob does not live in the green house.
Carol owns a dog.
The green house is to the left of the red house.
Alice does not own a cat.
Who lives in each house, and what pet do they own?`
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	geminiURLs := []string{
		"https://generativelanguage.googleapis.com/v1beta/interactions",
		"https://generativelanguage.googleapis.com/v1beta2/interactions",
	}
	var usedURL, usedModel string
	for _, u := range append([]string{url}, geminiURLs...) {
		for _, m := range []string{model, "gemini-3.6-flash", "gemini-3.8-flash"} {
			ok, msg := probeModel(ctx, u, tok.Value, m)
			if ok {
				usedURL, usedModel = u, m
				log.Printf("probe OK url=%s model=%s", u, m)
				break
			}
			log.Printf("probe FAIL url=%s model=%s: %s", u, m, truncate(msg, 220))
		}
		if usedModel != "" {
			break
		}
	}

	if usedModel != "" {
		log.Printf("=== Interactions API stream  model=%s thinking_level=high thinking_summaries=auto ===", usedModel)
		runStream(ctx, usedURL, tok.Value, map[string]any{
			"model":  usedModel,
			"input":  prompt,
			"store":  false,
			"stream": true,
			"generation_config": map[string]any{
				"thinking_level":     "high",
				"thinking_summaries": "auto",
				"temperature":        0.4,
			},
		})
	} else {
		log.Printf("Interactions API not usable with this Vertex SA")
	}

	log.Printf("=== Vertex generateContentStream model=%s includeThoughts thinking_level=high ===", model)
	runGenerateContentStream(ctx, creds, prompt)
}

func probeModel(ctx context.Context, url, token, modelName string) (bool, string) {
	body, _ := json.Marshal(map[string]any{
		"model":  modelName,
		"input":  "Reply with the single word pong.",
		"store":  false,
		"stream": false,
		"generation_config": map[string]any{
			"thinking_level":     "low",
			"thinking_summaries": "auto",
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 200 {
		return true, string(data)
	}
	return false, fmt.Sprintf("http %d: %s", resp.StatusCode, bytes.TrimSpace(data))
}

func runOnce(ctx context.Context, url, token string, body map[string]any) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	fmt.Printf("status=%d\n%s\n", resp.StatusCode, prettyJSON(data))
}

func runStream(ctx context.Context, url, token string, body map[string]any) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		log.Fatalf("http %d: %s", resp.StatusCode, data)
	}

	var thoughts, answer strings.Builder
	eventCount := 0
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" || payload == "" {
			continue
		}
		eventCount++

		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			fmt.Printf("  [raw %s] %s\n", eventType, truncate(payload, 200))
			continue
		}
		if et, ok := ev["event_type"].(string); ok && et != "" {
			eventType = et
		}

		switch eventType {
		case "interaction.created", "interaction.in_progress", "interaction.status_update":
			fmt.Printf("[%s] id=%v status=%v\n", eventType, nested(ev, "interaction", "id"), nested(ev, "interaction", "status"))
		case "step.start":
			stepType := nested(ev, "step", "type")
			fmt.Printf("\n--- step.start index=%v type=%v ---\n", ev["index"], stepType)
		case "step.delta":
			delta, _ := ev["delta"].(map[string]any)
			if delta == nil {
				fmt.Printf("  delta: %s\n", truncate(payload, 240))
				continue
			}
			dType, _ := delta["type"].(string)
			switch dType {
			case "thought_summary":
				text := extractThoughtText(delta)
				if text != "" {
					thoughts.WriteString(text)
					fmt.Printf("[THOUGHT] %s", visible(text))
					if !strings.HasSuffix(text, "\n") {
						fmt.Print("\n")
					}
				} else {
					fmt.Printf("[THOUGHT raw] %s\n", truncate(payload, 300))
				}
			case "thought", "thought_signature":
				fmt.Printf("[THOUGHT %s] keys=%v\n", dType, keys(delta))
			case "text":
				t, _ := delta["text"].(string)
				answer.WriteString(t)
				fmt.Printf("[TEXT] %s", visible(t))
				if !strings.HasSuffix(t, "\n") {
					fmt.Print("\n")
				}
			default:
				fmt.Printf("[DELTA %s] %s\n", dType, truncate(payload, 240))
			}
		case "step.stop":
			fmt.Printf("--- step.stop index=%v ---\n", ev["index"])
		case "interaction.completed":
			usage := nested(ev, "interaction", "usage")
			fmt.Printf("\n[%s] usage=%v elapsed=%s\n", eventType, usage, time.Since(start).Round(time.Millisecond))
		case "interaction.requires_action":
			fmt.Printf("[%s] %s\n", eventType, truncate(payload, 300))
		case "error":
			fmt.Printf("[ERROR] %s\n", payload)
		default:
			fmt.Printf("[%s] %s\n", eventType, truncate(payload, 240))
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("stream read error: %v", err)
	}

	fmt.Printf("\n===== SUMMARY events=%d thoughts_chars=%d answer_chars=%d elapsed=%s =====\n",
		eventCount, thoughts.Len(), answer.Len(), time.Since(start).Round(time.Millisecond))
	if thoughts.Len() > 0 {
		fmt.Printf("--- FULL THOUGHTS ---\n%s\n", thoughts.String())
	}
	if answer.Len() > 0 {
		fmt.Printf("--- FULL ANSWER ---\n%s\n", answer.String())
	}
	fmt.Println()
}

func extractThoughtText(delta map[string]any) string {
	if t, ok := delta["text"].(string); ok && t != "" {
		return t
	}
	if c, ok := delta["content"].(map[string]any); ok {
		if t, ok := c["text"].(string); ok {
			return t
		}
	}
	if arr, ok := delta["content"].([]any); ok {
		var b strings.Builder
		for _, item := range arr {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			if t, ok := m["text"].(string); ok {
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}

func nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func runGenerateContentStream(ctx context.Context, creds *auth.Credentials, prompt string) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     project,
		Location:    location,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		log.Fatalf("genai client: %v", err)
	}

	level := genai.ThinkingLevelHigh
	cfg := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   level,
		},
	}

	start := time.Now()
	var thoughts, answer strings.Builder
	chunks := 0
	for chunk, err := range client.Models.GenerateContentStream(ctx, model, genai.Text(prompt), cfg) {
		if err != nil {
			log.Printf("stream error after %d chunks: %v", chunks, err)
			break
		}
		chunks++
		if chunk == nil || len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
			continue
		}
		for _, part := range chunk.Candidates[0].Content.Parts {
			if part.Text == "" {
				continue
			}
			if part.Thought {
				thoughts.WriteString(part.Text)
				fmt.Printf("[THOUGHT] %s\n", visible(part.Text))
			} else {
				answer.WriteString(part.Text)
				fmt.Printf("[TEXT] %s\n", visible(part.Text))
			}
		}
	}
	fmt.Printf("\n===== generateContent SUMMARY chunks=%d thoughts_chars=%d answer_chars=%d elapsed=%s =====\n",
		chunks, thoughts.Len(), answer.Len(), time.Since(start).Round(time.Millisecond))
	if thoughts.Len() > 0 {
		fmt.Printf("--- FULL THOUGHTS ---\n%s\n", thoughts.String())
	}
	if answer.Len() > 0 {
		fmt.Printf("--- FULL ANSWER ---\n%s\n", answer.String())
	}
}

func prettyJSON(b []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return string(b)
	}
	s := buf.String()
	if len(s) > 8000 {
		return s[:8000] + "\n...truncated..."
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func visible(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}
