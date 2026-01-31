package aichat

import (
	"context"
	"fmt"
	"log"
	"os"

	"google.golang.org/genai"
)

func main() {
	ctx := context.Background()

	// Create client with API key from environment or directly
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  os.Getenv("GOOGLE_API_KEY"), // or set directly: "your-api-key"
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Configure generation with all features
	config := &genai.GenerateContentConfig{
		// System instruction (system prompt)
		SystemInstruction: &genai.Content{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{Text: "You are a helpful AI assistant with access to real-time information. Provide detailed, accurate answers with citations when using web sources. Be thorough and technical when needed."},
			},
		},

		// Generation parameters
		Temperature:     genai.Ptr(0.7),
		TopP:            genai.Ptr(0.95),
		TopK:            genai.Ptr(40),
		MaxOutputTokens: genai.Ptr(8192),
		CandidateCount:  genai.Ptr(1),

		// Safety settings - BLOCK_NONE for all categories
		SafetySettings: []*genai.SafetySetting{
			{
				Category:  genai.HarmCategoryHarassment,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategoryHateSpeech,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategorySexuallyExplicit,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategoryDangerousContent,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
		},

		// Enable thinking mode (for deeper reasoning)
		ThinkingConfig: &genai.ThinkingConfig{
			// Available options: ThinkingLevelMedium, ThinkingLevelHigh
			ThinkingLevel: genai.ThinkingLevelHigh,
		},

		// Tools: Google Search grounding + URL context
		Tools: []*genai.Tool{
			{
				GoogleSearch: &genai.GoogleSearch{},
			},
			{
				URLContext: &genai.URLContext{},
			},
		},

		// Response modalities
		ResponseModalities: []string{"TEXT"},
	}

	// Example 1: Text-only generation with grounding
	fmt.Println("=== Example 1: Text with Google Search Grounding ===")
	textExample(ctx, client, config)

	// Example 3: Image input (JPEG, PNG, GIF, WEBM)
	fmt.Println("\n=== Example 3: Image Analysis ===")
	imageExample(ctx, client, config)

	// Example 4: Multiple file types (PDF, video, audio)
	fmt.Println("\n=== Example 4: File Upload (PDF, Video, etc.) ===")
	fileExample(ctx, client, config)
}

// Example 1: Text generation with Google Search grounding
func textExample(ctx context.Context, client *genai.Client, config *genai.GenerateContentConfig) {
	contents := []*genai.Content{
		{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{Text: "What are the latest developments in Gemini 2.5 models as of January 2026?"},
			},
		},
	}

	response, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", contents, config)
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(response.Text())

	// Check grounding metadata
	if len(response.Candidates) > 0 && response.Candidates[0].GroundingMetadata != nil {
		fmt.Println("\n--- Grounding Sources ---")
		for i, chunk := range response.Candidates[0].GroundingMetadata.GroundingChunks {
			if chunk.Web != nil {
				fmt.Printf("%d. %s - %s\n", i+1, chunk.Web.Title, chunk.Web.URI)
			}
		}
	}
}


func imageExample(ctx context.Context, client *genai.Client, config *genai.GenerateContentConfig) {
	// Method 1: Inline image data (for images < 20MB)
	imageData, err := os.ReadFile("example.jpg") // or .png, .gif, .webm
	if err != nil {
		log.Printf("Error reading image: %v\n", err)
		return
	}

	contents := []*genai.Content{
		{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{Text: "Describe this image in detail and identify any technical elements."},
				{InlineData: &genai.Blob{
					Data:     imageData,
					MIMEType: "image/jpeg", // or "image/png", "image/gif", "video/webm"
				}},
			},
		},
	}

	response, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", contents, config)
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(response.Text())
}

// Example 4: File uploads (PDF, video, audio, documents)
func fileExample(ctx context.Context, client *genai.Client, config *genai.GenerateContentConfig) {
	// Upload a file (PDF, video, audio, etc.)
	file, err := client.Files.UploadFromPath(ctx, "document.pdf", &genai.UploadFileConfig{
		MIMEType:    "application/pdf", // or "video/mp4", "audio/mpeg", "video/webm"
		DisplayName: "Technical Document",
	})
	if err != nil {
		log.Printf("Error uploading file: %v\n", err)
		return
	}
	defer client.Files.Delete(ctx, file.Name, nil)

	fmt.Printf("Uploaded file: %s (State: %s)\n", file.Name, file.State)

	// Wait for file processing
	for file.State == genai.FileStateProcessing {
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			log.Printf("Error checking file: %v\n", err)
			return
		}
	}

	// Use the file in generation
	contents := []*genai.Content{
		{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{Text: "Summarize the key points from this document."},
				{FileData: &genai.FileData{
					FileURI:  file.URI,
					MIMEType: file.MIMEType,
				}},
			},
		},
	}

	response, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", contents, config)
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(response.Text())
}