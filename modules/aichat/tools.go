package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/config"
)

const GeneratedImagesDir = "/app/generated"

var validAspectRatios = map[string]bool{
	"1:1": true, "9:16": true, "16:9": true, "3:4": true, "4:3": true,
	"3:2": true, "2:3": true, "5:4": true, "4:5": true, "21:9": true,
}

var aiTools []*genai.Tool

func init() {
	var createImageParams genai.Schema
	json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"prompt": {
				"type": "string",
				"description": "Detailed prompt describing the image to generate"
			},
			"aspect_ratio": {
				"type": "string",
				"description": "Aspect ratio. Values: 1:1, 9:16, 16:9, 3:4, 4:3, 3:2, 2:3, 5:4, 4:5, 21:9. Empty for auto."
			},
			"high_quality": {
				"type": "boolean",
				"description": "Use HIGH mode (4K resolution). COSTS MORE - only use when @s4tyendra explicitly requests."
			}
		},
		"required": ["prompt"]
	}`), &createImageParams)

	var sendFileParams genai.Schema
	json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Path to the file to send"
			}
		},
		"required": ["file_path"]
	}`), &sendFileParams)

	var runCodeParams genai.Schema
	json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"language": {
				"type": "string",
				"description": "Programming language: python, bash, or javascript",
				"enum": ["python", "bash", "javascript"]
			},
			"code": {
				"type": "string",
				"description": "The code to execute. For bash, can be a command like 'excol /generated/img.png'"
			}
		},
		"required": ["language", "code"]
	}`), &runCodeParams)

	var getLatestDataParams genai.Schema
	json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query for real-time web information. Be specific for best results."
			}
		},
		"required": ["query"]
	}`), &getLatestDataParams)

	aiTools = []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        "create_image",
					Description: "Generate an image from a text prompt. Returns the file path of the generated image.",
					Parameters:  &createImageParams,
				},
				{
					Name:        "send_file",
					Description: "Send a file to the user in the chat. Use after generating an image.",
					Parameters:  &sendFileParams,
				},
				{
					Name:        "run_code",
					Description: "Execute code in a sandboxed container. Has access to /generated (images) and /workspace. Available: python, bash, javascript (bun).",
					Parameters:  &runCodeParams,
				},
				{
					Name:        "get_latest_data",
					Description: "Search the web for real-time, up-to-date information. Use when asked about current events, news, recent happenings, live scores, weather, or anything requiring fresh data. Returns answer with cited sources.",
					Parameters:  &getLatestDataParams,
				},
			},
		},
	}

	os.MkdirAll(GeneratedImagesDir, 0755)
}

func executeFunctionCall(fc *genai.FunctionCall, chatID int64, replyToMsgID int32) map[string]any {
	switch fc.Name {
	case "create_image":
		return executeCreateImage(fc.Args)
	case "send_file":
		return executeSendFile(fc.Args, chatID, replyToMsgID)
	case "run_code":
		return executeRunCode(fc.Args)
	case "get_latest_data":
		return executeGetLatestData(fc.Args)
	default:
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Unknown function: %s", fc.Name),
		}
	}
}

func executeCreateImage(args map[string]any) map[string]any {
	prompt, _ := args["prompt"].(string)
	aspectRatio, _ := args["aspect_ratio"].(string)
	highQuality, _ := args["high_quality"].(bool)

	if prompt == "" {
		return map[string]any{"success": false, "error": "prompt is required"}
	}

	if aspectRatio != "" && !validAspectRatios[aspectRatio] {
		aspectRatio = ""
	}

	model := config.ImageModel
	if highQuality {
		model = config.HighImageModel
	}

	log.Printf("[AiChat] Generating image with model %s (high=%v, aspect=%s): %s", model, highQuality, aspectRatio, prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if aspectRatio == "" {
		aspectRatio = "9:16"
	}

	imageSize := "1K"
	if highQuality {
		imageSize = "4K"
	}

	imgConfig := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
		ImageConfig: &genai.ImageConfig{
			AspectRatio: aspectRatio,
			ImageSize:   imageSize,
		},
	}

	resp, err := genaiClient.Models.GenerateContent(ctx, model, genai.Text(prompt), imgConfig)
	if err != nil {
		log.Printf("[AiChat] Image generation failed: %v", err)
		return map[string]any{"success": false, "error": err.Error()}
	}

	if len(resp.Candidates) == 0 {
		return map[string]any{"success": false, "error": "No image generated"}
	}

	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				ext := ".png"
				if strings.Contains(part.InlineData.MIMEType, "jpeg") {
					ext = ".jpg"
				} else if strings.Contains(part.InlineData.MIMEType, "webp") {
					ext = ".webp"
				}

				filename := fmt.Sprintf("img_%d%s", time.Now().UnixNano(), ext)
				filePath := filepath.Join(GeneratedImagesDir, filename)

				err := os.WriteFile(filePath, part.InlineData.Data, 0644)
				if err != nil {
					log.Printf("[AiChat] Failed to save image: %v", err)
					return map[string]any{"success": false, "error": "Failed to save image"}
				}

				log.Printf("[AiChat] Image saved to %s (%d bytes)", filePath, len(part.InlineData.Data))
				return map[string]any{
					"success":   true,
					"file_path": filePath,
					"prompt":    prompt,
					"size":      len(part.InlineData.Data),
				}
			}
		}
	}

	return map[string]any{"success": false, "error": "No image data in response"}
}

func executeSendFile(args map[string]any, chatID int64, replyToMsgID int32) map[string]any {
	filePath, _ := args["file_path"].(string)

	if filePath == "" {
		return map[string]any{"success": false, "error": "file_path is required"}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return map[string]any{"success": false, "error": "File not found"}
	}

	log.Printf("[AiChat] Sending file %s to chat %d", filePath, chatID)

	_, err := botClient.SendMedia(chatID, filePath, &telegram.MediaOptions{
		ReplyTo: &telegram.InputReplyToMessage{
			ReplyToMsgID: replyToMsgID,
		},
		Caption:       "🎨 Generated image",
		ForceDocument: true,
	})

	if err != nil {
		log.Printf("[AiChat] Failed to send file: %v", err)
		return map[string]any{"success": false, "error": err.Error()}
	}

	return map[string]any{"success": true, "message": "File sent successfully"}
}

func executeRunCode(args map[string]any) map[string]any {
	language, _ := args["language"].(string)
	code, _ := args["code"].(string)

	if language == "" || code == "" {
		return map[string]any{"success": false, "error": "language and code are required"}
	}

	validLanguages := map[string]bool{"python": true, "bash": true, "javascript": true}
	if !validLanguages[language] {
		return map[string]any{"success": false, "error": "Invalid language. Use: python, bash, or javascript"}
	}

	containerName := os.Getenv("CODE_RUNNER_CONTAINER")
	if containerName == "" {
		containerName = "zeno-code-runner"
	}

	var cmdArgs []string
	switch language {
	case "python":
		cmdArgs = []string{"docker", "exec", containerName, "python3", "-c", code}
	case "bash":
		cmdArgs = []string{"docker", "exec", containerName, "bash", "-c", code}
	case "javascript":
		cmdArgs = []string{"docker", "exec", containerName, "bun", "-e", code}
	}

	log.Printf("[AiChat] Running code (%s): %s", language, truncateString(code, 100))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	errOutput := stderr.String()

	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{"success": false, "error": "Execution timed out (30s limit)"}
	}

	if err != nil {
		log.Printf("[AiChat] Code execution error: %v, stderr: %s", err, errOutput)
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Execution failed: %s", errOutput),
			"output":  output,
		}
	}

	log.Printf("[AiChat] Code execution successful, output length: %d", len(output))
	return map[string]any{"success": true, "output": output}
}

func executeGetLatestData(args map[string]any) map[string]any {
	query, _ := args["query"].(string)
	if query == "" {
		return map[string]any{"success": false, "error": "query is required"}
	}

	log.Printf("[Perplexity] Searching: %s", truncateString(query, 100))

	result, err := perplexitySearch(query)
	if err != nil {
		log.Printf("[Perplexity] Search failed: %v", err)
		return map[string]any{"success": false, "error": err.Error()}
	}

	resp := map[string]any{
		"success": true,
		"answer":  result.Answer,
	}

	// Upload sources to Telegraph if we have any
	if len(result.Sources) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Query: %s\n\n", query))
		for i, s := range result.Sources {
			sb.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, s.Name, s.URL))
		}
		if len(result.RelatedQueries) > 0 {
			sb.WriteString("Related:\n")
			for _, rq := range result.RelatedQueries {
				sb.WriteString(fmt.Sprintf("• %s\n", rq))
			}
		}

		url, err := uploadToTelegraph(fmt.Sprintf("Sources: %s", truncateString(query, 60)), sb.String())
		if err != nil {
			log.Printf("[Perplexity] Failed to upload sources to Telegraph: %v", err)
		} else {
			resp["sources_url"] = url
			log.Printf("[Perplexity] Sources uploaded: %s", url)
		}
	}

	return resp
}
