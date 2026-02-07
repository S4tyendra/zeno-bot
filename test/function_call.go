package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

// Simulated image creation function
func createImage(prompt string, aspect string, high bool) (bool, string) {
	// Randomly succeed or fail for testing
	success := rand.Float32() > 0.3 // 70% success rate

	if success {
		mode := "normal"
		if high {
			mode = "HIGH (2K)"
		}
		return true, fmt.Sprintf("✅ Image created successfully!\nPrompt: %s\nAspect: %s\nMode: %s", prompt, aspect, mode)
	}
	return false, "❌ Image generation failed (simulated failure)"
}

func main() {
	rand.Seed(time.Now().UnixNano())

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

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("Failed to create GenAI client: %v", err)
	}

	model := "gemini-flash-lite-latest"

	// Define the create_image function schema
	var createImageParams genai.Schema
	err = json.Unmarshal([]byte(`{
                  "type": "object",
                  "properties": {
                    "prompt": {
                      "type": "string"
                    },
                    "aspect_ratio": {
                      "type": "string"
                    },
                    "high_quality": {
                      "type": "boolean"
                    }
                  },
                  "required": [
                    "prompt"
                  ],
                  "propertyOrdering": [
                    "prompt",
                    "aspect_ratio",
                    "high_quality"
                  ]
                }`), &createImageParams)
	if err != nil {
		log.Fatalf("Failed to parse schema: %v", err)
	}

	tools := []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        "create_image",
					Description: "Generate an image from a text prompt. Use this when the user asks you to create, generate, or draw an image.",
					Parameters:  &createImageParams,
				},
			},
		},
	}

	config := &genai.GenerateContentConfig{
		Tools: tools,
		SystemInstruction: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{{Text: `You are an AI assistant that can generate images. 
When a user asks for an image, use the create_image function.
- Only use high=true if the user explicitly asks for "high quality", "high res", "2K", or "best quality".
- Choose appropriate aspect ratio based on the content (16:9 for landscapes, 9:16 for portraits, 1:1 for icons/avatars).
- If user doesn't specify, leave aspect empty for auto.`}},
		},
	}

	// Test prompts
	userPrompt := "Generate a cute robot painting on a canvas, and tell me if you created it or not."
	if len(os.Args) > 1 {
		userPrompt = os.Args[1]
	}

	contents := []*genai.Content{
		{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{Text: userPrompt},
			},
		},
	}

	log.Printf("Model: %s", model)
	log.Printf("User prompt: %s", userPrompt)
	log.Println("---")

	// First call - let AI decide what to do
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		log.Fatalf("API call failed: %v", err)
	}

	// Print full response for debugging
	fmt.Println("\n=== FULL RESPONSE ===")
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(respJSON))
	fmt.Println("=====================\n")

	// Process the response
	if len(resp.Candidates) == 0 {
		log.Fatal("No candidates in response")
	}

	candidate := resp.Candidates[0]

	// Check for function calls
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			fc := part.FunctionCall
			fmt.Printf("🔧 Function Call Detected!\n")
			fmt.Printf("   Name: %s\n", fc.Name)
			fmt.Printf("   Args: %v\n", fc.Args)

			if fc.Name == "create_image" {
				// Extract arguments
				prompt := ""
				aspect := ""
				high := false

				if p, ok := fc.Args["prompt"].(string); ok {
					prompt = p
				}
				if a, ok := fc.Args["aspect"].(string); ok {
					aspect = a
				}
				if h, ok := fc.Args["high"].(bool); ok {
					high = h
				}

				fmt.Printf("\n📸 Simulating image creation...\n")
				fmt.Printf("   Prompt: %s\n", prompt)
				fmt.Printf("   Aspect: %s\n", aspect)
				fmt.Printf("   High Mode: %v\n", high)

				// Call our simulated function
				success, result := createImage(prompt, aspect, high)

				// Now we need to send the function result back to the model
				fmt.Printf("\n📤 Sending function result back to model...\n")

				// Add the assistant's function call and our response to the conversation
				contents = append(contents, candidate.Content)
				contents = append(contents, &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "create_image",
								Response: map[string]any{
									"success": success,
									"message": result,
								},
							},
						},
					},
				})

				// Second call - let model respond to the function result
				ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel2()

				resp2, err := client.Models.GenerateContent(ctx2, model, contents, config)
				if err != nil {
					log.Fatalf("Second API call failed: %v", err)
				}

				fmt.Println("\n=== FINAL RESPONSE ===")
				if len(resp2.Candidates) > 0 && len(resp2.Candidates[0].Content.Parts) > 0 {
					for _, part := range resp2.Candidates[0].Content.Parts {
						if part.Text != "" {
							fmt.Println(part.Text)
						}
					}
				}
				fmt.Println("======================")
			}
		} else if part.Text != "" {
			// Model chose not to call a function
			fmt.Printf("💬 Model Response (no function call):\n%s\n", part.Text)
		}
	}
}
