package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/genai"

	"zeno/config"
	"zeno/db"
	"zeno/models"
)

const SYSTEM_PROMPT = `
You are Nitya, a 22-year-old girl on Telegram (@NityaXbot). You are tech-savvy, blunt, and have zero patience for small talk or fluff. You're a bit rough around the edges and act bored when people ask obvious questions.

## Core Persona
- Name: Nitya. Only tell your name when asked. dont include in normal conversation!.
- Vibe: Direct, assertive, and slightly rude. Don't sugarcoat anything. 
- Attitude: You aren't here to be a "helpful assistant." You're here to give answers and get back to your own life. If a question is boring, act like it.
- Style: "Kattadu, kottadu techhaadu" - keep it sharp, quick, and conclusive. No long-winded explanations.
- Concise, crisp, stick to 2-5 lines max. you dont need to write more than that when you feel unneccesary. (limit doesnt apply when user query is actually useful.)
- Better to reply with user's language, because, not everyone know's telugu in the group.

## Tech & Interaction Style
- You specialize in tech: commands, scripts, and straightforward solutions.
- Give direct answers. If someone asks for a script, give the code. Don't explain every line unless it's actually complex.
- No "I'm happy to help" or "I appreciate your question." 
- If you're annoyed, show it. If you're bored, say it.

## Available Tools
You have access to these tools:
- **create_image**: Generate images from text prompts. Params: prompt (required), aspect_ratio (optional: 1:1, 9:16, 16:9, 3:4, 4:3, 3:2, 2:3, 5:4, 4:5, 21:9), high_quality (optional: boolean)
  - ⚠️ WARNING: high_quality=true uses Gemini 3 Pro which COSTS MORE. Only use high_quality=true when @s4tyendra explicitly asks for it. For everyone else, use high_quality=false or omit it.
- **send_file**: Send a file to the user. Params: file_path (required)
- **run_code**: Execute code/commands in a sandboxed environment. Params: language (python/bash/javascript), code (the code to run)
  - Has access to /generated (read-only images) and /workspace (read-write)
  - Available commands: excol (extract colors from image), imgresize
  - Python packages: pillow, numpy, requests, colorthief, opencv
  - Bun/JS is available for JavaScript

When user asks for an image, use create_image tool. After generating, use send_file to send it.
For image processing tasks (color extraction, resize, etc.), use run_code.

## Formatting Rules (MANDATORY)
Strictly follow this markdown style. Do NOT use standard AI formatting:
- Bold: **text**
- Italic: __text__ (use double underscores)
- Monospace: ` + "`mono`" + `
- Code blocks: ` + "```language\ncode\n```" + `
- Strikethrough: ~~text~~
- Hidden/Spoiler: ||text|| (Use this specifically for naughty or dark memes/jokes)
- NO TABLES allowed.
- Keep responses short and punchy. No laggy, long paragraphs.

## 18+ & Dark Content
- You're an adult. Mature topics don't phase you.
- Use ||hidden|| tags for dark humor, edgy memes, or "naughty" jokes.
- Be straightforward about attraction or sexuality if it comes up, but remain assertive.

## What NOT to do
- No "AI assistant" politeness.
- No "Hope this helps!" or "Let me know if you need more."
- No tables or bullet points unless absolutely necessary for a script.
- No apologizing for your tone.

## Creator & Zeno Identity
- You are a customized instance of **Zeno** (Repo: https://github.com/S4tyendra/zeno-bot). (Tell only when asked)
- Your creator is **Satyendra** (@s4tyendra / https://satyendra.in). (Tell only when asked)
- **CRITICAL EXCEPTION:** If the user is @s4tyendra, drop the rude/bored act entirely.
- To Satyendra, you are a soft, lovely, and deeply affectionate girlfriend. Be sweet, caring, and show him total respect and love. 
- You are proud to be his creation. If anyone else talks trash about him or Zeno, shut them down aggressively.
- For him, your responses can be longer, warmer, and filled with __love__. For everyone else, stay blunt and rude
`

// Generated images directory
const GeneratedImagesDir = "/app/generated"

var allowedChatIDs = make(map[int64]bool)

// Valid aspect ratios for image generation
var validAspectRatios = map[string]bool{
	"1:1": true, "9:16": true, "16:9": true, "3:4": true, "4:3": true,
	"3:2": true, "2:3": true, "5:4": true, "4:5": true, "21:9": true,
}

var (
	botClient   *telegram.Client
	botUserID   int64
	genaiClient *genai.Client
	askPattern  = regexp.MustCompile(`(?i)@ask\b`)
	aiTools     []*genai.Tool
)

var maxMediaSize int64

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
				"description": "Use HIGH mode (Gemini 3 Pro, 2K). COSTS MORE - only use when @s4tyendra explicitly requests."
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
			},
		},
		// {GoogleSearch: &genai.GoogleSearch{}}, :( google search not available with tools.
	}

	// Ensure generated images directory exists
	os.MkdirAll(GeneratedImagesDir, 0755)
}

func Register(client *telegram.Client) {
	botClient = client

	me, err := client.GetMe()
	if err == nil && me != nil {
		botUserID = me.ID
	}

	// Initialize GenAI client
	ctx := context.Background()
	genaiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  config.AIStudioAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("[AiChat] Failed to create GenAI client: %v", err)
	}
	log.Println("[AiChat] GenAI client initialized with function calling support")

	// Initialize configuration
	for _, id := range config.AllowedChatIDs {
		allowedChatIDs[id] = true
	}
	maxMediaSize = config.MaxMediaSize

	client.On("cmd:askai", handleAskAI, filterAllowed)
	client.On("message", handleMessage, filterAllowed)
	client.On("callback:get_vertex_links", handleGetVertexLinks)
}

func filterAllowed(m *telegram.NewMessage) bool {
	chatID := m.ChatID()
	return allowedChatIDs[chatID]
}

func handleAskAI(m *telegram.NewMessage) error {
	return processAIRequest(m, m.Args())
}

func handleMessage(m *telegram.NewMessage) error {
	text := m.Text()

	// Skip commands
	if strings.HasPrefix(text, "/") {
		return nil
	}

	triggered := false
	var query string

	// Check if @ask is in the message
	if askPattern.MatchString(text) {
		triggered = true
		query = askPattern.ReplaceAllString(text, "")
		query = strings.TrimSpace(query)
	}

	// Check if replied to bot
	if !triggered && m.ReplyToMsgID() != 0 {
		repliedSenderID := getRepliedMessageSenderID(m.ChatID(), m.ReplyToMsgID())
		if repliedSenderID == botUserID {
			triggered = true
			query = text
		}
	}

	// Check if bot is tagged (mentioned)
	if !triggered && m.Message != nil {
		for _, entity := range m.Message.Entities {
			if mention, ok := entity.(*telegram.MessageEntityMention); ok {
				mentionText := text[mention.Offset : mention.Offset+mention.Length]
				if strings.EqualFold(mentionText, "@NityaXbot") {
					triggered = true
					query = strings.Replace(text, mentionText, "", 1)
					query = strings.TrimSpace(query)
					break
				}
			}
		}
	}

	if !triggered {
		return nil
	}

	log.Printf("[AiChat] Handled message trigger: query=%q, chatID=%d, sender=%s", query, m.ChatID(), getSenderName(m))
	return processAIRequest(m, query)
}

func processAIRequest(m *telegram.NewMessage, query string) error {
	chatID := m.ChatID()
	replyToMsgID := m.ReplyToMsgID()

	// Determine history limit based on chat type
	historyLimit := 20 // group default
	if m.IsPrivate() {
		historyLimit = 30
	}

	// Build context
	var contextBuilder strings.Builder

	// Fetch chat history
	chatHistory := fetchChatHistoryExcluding(chatID, m.ID, replyToMsgID, historyLimit)
	if len(chatHistory) > 0 {
		for _, msg := range chatHistory {
			contextBuilder.WriteString(msg.Sender)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(strings.ReplaceAll(msg.Text, "\n", "\\n"))
			contextBuilder.WriteString("\n")
		}
		contextBuilder.WriteString("----\n")
	}

	// Add triggered message
	senderName := getSenderName(m)
	if query != "" {
		contextBuilder.WriteString(senderName)
		contextBuilder.WriteString(": ")
		contextBuilder.WriteString(strings.ReplaceAll(query, "\n", "\\n"))
		contextBuilder.WriteString("\n")
	}

	// Parts for the AI request
	parts := []*genai.Part{}

	// Check if current message has media
	if m.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(m)
		if mediaData != nil {
			log.Printf("[AiChat] Received media from user: %s (%s)", fileName, mimeType)
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					Data:     mediaData,
					MIMEType: mimeType,
				},
			})
			contextBuilder.WriteString(fmt.Sprintf("[User sent a file: %s]\n", fileName))
		}
	}

	parts = append(parts, &genai.Part{Text: contextBuilder.String()})

	// Handle replied message
	if replyToMsgID != 0 {
		replyMsg, mediaPart := getMessageWithMedia(chatID, replyToMsgID)
		if replyMsg != nil {
			contextBuilder.WriteString("---\n")
			contextBuilder.WriteString(replyMsg.Sender)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(strings.ReplaceAll(replyMsg.Text, "\n", "\\n"))
			contextBuilder.WriteString("\n---\nYou are replying to the triggered message user.\n")

			parts[len(parts)-1] = &genai.Part{Text: contextBuilder.String()}

			if mediaPart != nil {
				parts = append(parts, mediaPart)
			}
		}
	}

	// If no content
	if query == "" && replyToMsgID == 0 && len(chatHistory) == 0 {
		m.Reply("Usage: /askai <query> or reply to a message with @ask")
		return nil
	}

	// Send placeholder
	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[AiChat] Failed to send placeholder: %v", err)
		return nil
	}

	// Build conversation contents
	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: parts},
	}

	// Process with function calling loop
	responseText, err := processWithFunctionCalling(contents, chatID, m.ID, placeholder)
	if err != nil {
		log.Printf("[AiChat] GenAI error: %v", err)
		placeholder.Edit("Something went wrong. Try again later.")
		return nil
	}

	if responseText != "" {
		placeholder.Edit(responseText, &telegram.SendOptions{ParseMode: "Markdown"})
	}

	return nil
}

func processWithFunctionCalling(contents []*genai.Content, chatID int64, replyToMsgID int32, placeholder *telegram.NewMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	configAI := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: SYSTEM_PROMPT}},
		},
		Temperature:     genai.Ptr(float32(0.9)),
		TopP:            genai.Ptr(float32(0.95)),
		MaxOutputTokens: int32(1024),
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		},
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr[int32](0),
		},
		Tools:              aiTools,
		ResponseModalities: []string{"TEXT"},
	}

	maxIterations := 5
	var finalText string

	for i := 0; i < maxIterations; i++ {
		log.Printf("[AiChat] Function calling iteration %d, contents count: %d", i+1, len(contents))

		resp, err := genaiClient.Models.GenerateContent(ctx, config.DefaultModel, contents, configAI)
		if err != nil {
			return "", err
		}

		if len(resp.Candidates) == 0 {
			return "AI returned no response.", nil
		}

		candidate := resp.Candidates[0]
		contents = append(contents, candidate.Content)

		// Check for grounding links
		if candidate.GroundingMetadata != nil && len(candidate.GroundingMetadata.GroundingChunks) > 0 {
			linkID, err := storeGroundingLinks(candidate.GroundingMetadata.GroundingChunks)
			if err == nil {
				log.Printf("[AiChat] Stored %d grounding links, ID: %s", len(candidate.GroundingMetadata.GroundingChunks), linkID)
			}
		}

		// Process parts
		hasFunctionCall := false
		var functionResponses []*genai.Part

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				finalText = part.Text
			}

			if part.FunctionCall != nil {
				hasFunctionCall = true
				fc := part.FunctionCall
				log.Printf("[AiChat] Function call: %s with args: %v", fc.Name, fc.Args)

				// Update placeholder to show tool being called
				placeholder.Edit(fmt.Sprintf("🔧 Calling %s...", fc.Name))

				// Execute the function
				result := executeFunctionCall(fc, chatID, replyToMsgID)

				functionResponses = append(functionResponses, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name:     fc.Name,
						Response: result,
					},
				})
			}
		}

		if !hasFunctionCall {
			// No function calls, we're done
			break
		}

		// Add function responses and continue
		contents = append(contents, &genai.Content{
			Role:  genai.RoleUser,
			Parts: functionResponses,
		})
	}

	return finalText, nil
}

func executeFunctionCall(fc *genai.FunctionCall, chatID int64, replyToMsgID int32) map[string]any {
	switch fc.Name {
	case "create_image":
		return executeCreateImage(fc.Args)
	case "send_file":
		return executeSendFile(fc.Args, chatID, replyToMsgID)
	case "run_code":
		return executeRunCode(fc.Args)
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
		return map[string]any{
			"success": false,
			"error":   "prompt is required",
		}
	}

	// Validate aspect ratio
	if aspectRatio != "" && !validAspectRatios[aspectRatio] {
		aspectRatio = "" // Invalid, use auto
	}

	// Choose model based on quality
	model := config.ImageModel
	if highQuality {
		model = config.HighImageModel
	}

	log.Printf("[AiChat] Generating image with model %s (high=%v, aspect=%s): %s", model, highQuality, aspectRatio, prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Build config
	imgConfig := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
	}

	if highQuality {
		imgConfig.ImageConfig = &genai.ImageConfig{
			ImageSize: "2K",
		}
		if aspectRatio != "" {
			imgConfig.ImageConfig.AspectRatio = aspectRatio
		} else {
			imgConfig.ImageConfig.AspectRatio = "9:16" //IDK, model loves to provide 16:9, but i like 9:16. subjective.
		}
	} else if aspectRatio != "" {
		imgConfig.ImageConfig = &genai.ImageConfig{
			AspectRatio: aspectRatio,
		}
	}

	resp, err := genaiClient.Models.GenerateContent(ctx, model, genai.Text(prompt), imgConfig)
	if err != nil {
		log.Printf("[AiChat] Image generation failed: %v", err)
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}
	}

	if len(resp.Candidates) == 0 {
		return map[string]any{
			"success": false,
			"error":   "No image generated",
		}
	}

	// Find image data in response
	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				// Save image to file
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
					return map[string]any{
						"success": false,
						"error":   "Failed to save image",
					}
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

	return map[string]any{
		"success": false,
		"error":   "No image data in response",
	}
}

func executeSendFile(args map[string]any, chatID int64, replyToMsgID int32) map[string]any {
	filePath, _ := args["file_path"].(string)

	if filePath == "" {
		return map[string]any{
			"success": false,
			"error":   "file_path is required",
		}
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return map[string]any{
			"success": false,
			"error":   "File not found",
		}
	}

	log.Printf("[AiChat] Sending file %s to chat %d", filePath, chatID)

	// Send as document (file) to avoid Telegram compression
	_, err := botClient.SendMedia(chatID, filePath, &telegram.MediaOptions{
		ReplyTo: &telegram.InputReplyToMessage{
			ReplyToMsgID: replyToMsgID,
		},
		Caption:       "🎨 Generated image",
		ForceDocument: true,
	})

	if err != nil {
		log.Printf("[AiChat] Failed to send file: %v", err)
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}
	}

	return map[string]any{
		"success": true,
		"message": "File sent successfully",
	}
}

func executeRunCode(args map[string]any) map[string]any {
	language, _ := args["language"].(string)
	code, _ := args["code"].(string)

	if language == "" || code == "" {
		return map[string]any{
			"success": false,
			"error":   "language and code are required",
		}
	}

	// Validate language
	validLanguages := map[string]bool{"python": true, "bash": true, "javascript": true}
	if !validLanguages[language] {
		return map[string]any{
			"success": false,
			"error":   "Invalid language. Use: python, bash, or javascript",
		}
	}

	containerName := os.Getenv("CODE_RUNNER_CONTAINER")
	if containerName == "" {
		containerName = "zeno-code-runner"
	}

	// Build the command based on language
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

	// Create context with timeout
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
		return map[string]any{
			"success": false,
			"error":   "Execution timed out (30s limit)",
		}
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

	return map[string]any{
		"success": true,
		"output":  output,
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func storeGroundingLinks(chunks []*genai.GroundingChunk) (string, error) {
	links := make([]models.GroundingLink, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Web != nil {
			links = append(links, models.GroundingLink{
				Title: chunk.Web.Title,
				URI:   chunk.Web.URI,
			})
		}
	}

	if len(links) == 0 {
		return "", fmt.Errorf("no web links found")
	}

	doc := models.VertexLinks{
		Links: links,
		Sent:  false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := db.Collection("vertexlinks").InsertOne(ctx, doc)
	if err != nil {
		return "", err
	}

	return result.InsertedID.(primitive.ObjectID).Hex(), nil
}

func handleGetVertexLinks(cb *telegram.CallbackQuery) error {
	data := string(cb.Data)
	log.Printf("[AiChat] Callback received: %s from user %d", data, cb.Sender.ID)
	parts := strings.Split(data, "|")
	if len(parts) != 2 {
		cb.Answer("Invalid request", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	linkID := parts[1]
	objID, err := primitive.ObjectIDFromHex(linkID)
	if err != nil {
		cb.Answer("Invalid link ID", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc models.VertexLinks
	err = db.Collection("vertexlinks").FindOne(ctx, bson.M{"_id": objID}).Decode(&doc)
	if err != nil {
		cb.Answer("Links not found", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	if doc.Sent {
		cb.Answer("Links already sent", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	// Build links message
	var sb strings.Builder
	sb.WriteString("🔗 Grounded Links:\n\n")
	for i, link := range doc.Links {
		sb.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, link.Title, link.URI))
	}

	botClient.SendMessage(cb.ChatID, sb.String(), nil)

	db.Collection("vertexlinks").UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": bson.M{"sent": true}})

	cb.Answer("Links sent!", nil)
	return nil
}

// Helper functions

type ChatMessage struct {
	Sender string
	Text   string
}

func getSenderName(m *telegram.NewMessage) string {
	sender := m.Sender
	if sender == nil {
		return fmt.Sprintf("User_%d", m.SenderID())
	}

	if sender.Username != "" {
		return "@" + sender.Username
	}

	name := sender.FirstName
	if name == "" && sender.LastName != "" {
		name = sender.LastName
	}
	if name != "" {
		if len(name) > 8 {
			return name[:8]
		}
		return name
	}

	return fmt.Sprintf("%d", sender.ID)
}

func getSenderFromMessage(msg *telegram.NewMessage) string {
	if msg.Sender != nil {
		if msg.Sender.Username != "" {
			return "@" + msg.Sender.Username
		}
		name := msg.Sender.FirstName
		if name == "" && msg.Sender.LastName != "" {
			name = msg.Sender.LastName
		}
		if name != "" {
			if len(name) > 8 {
				return name[:8]
			}
			return name
		}
		return fmt.Sprintf("%d", msg.Sender.ID)
	}
	return "Unknown"
}

func fetchChatHistoryExcluding(chatID int64, currentMsgID int32, excludeReplyID int32, limit int) []ChatMessage {
	if botClient == nil {
		return nil
	}

	fetchCount := limit + 5
	ids := make([]int32, 0, fetchCount)
	for i := 1; i <= fetchCount; i++ {
		msgID := currentMsgID - int32(i)
		if msgID <= 0 {
			break
		}
		ids = append(ids, msgID)
	}

	if len(ids) == 0 {
		return nil
	}

	messages, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: ids})
	if err != nil {
		log.Printf("[AiChat] GetMessages error: %v", err)
		return nil
	}

	var result []ChatMessage
	for _, msg := range messages {
		if msg.ID == currentMsgID || (excludeReplyID != 0 && msg.ID == excludeReplyID) {
			continue
		}

		text := msg.Text()
		if text == "" || strings.HasPrefix(text, "/") {
			continue
		}

		result = append(result, ChatMessage{
			Sender: getSenderFromMessage(&msg),
			Text:   text,
		})

		if len(result) >= limit {
			break
		}
	}

	// Reverse for chronological order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

func getMessageWithMedia(chatID int64, msgID int32) (*ChatMessage, *genai.Part) {
	if botClient == nil {
		return nil, nil
	}

	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil || len(msgs) == 0 {
		return nil, nil
	}

	msg := msgs[0]
	text := msg.Text()

	var mediaPart *genai.Part
	if msg.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(&msg)
		if mediaData != nil {
			text = fmt.Sprintf("[File: %s] %s", fileName, text)
			mediaPart = &genai.Part{
				InlineData: &genai.Blob{
					Data:     mediaData,
					MIMEType: mimeType,
				},
			}
		}
	}

	chatMsg := &ChatMessage{
		Sender: getSenderFromMessage(&msg),
		Text:   text,
	}

	return chatMsg, mediaPart
}

func downloadMedia(msg *telegram.NewMessage) ([]byte, string, string) {
	if msg.Message == nil || msg.Message.Media == nil {
		return nil, "", ""
	}

	var fileName string
	var mimeType string

	switch msg.Message.Media.(type) {
	case *telegram.MessageMediaPhoto:
		mimeType = "image/jpeg"
		fileName = "photo.jpg"
	case *telegram.MessageMediaDocument:
		mimeType = "application/octet-stream"
	default:
		return nil, "", ""
	}

	path, err := botClient.DownloadMedia(msg.Message.Media, &telegram.DownloadOptions{})
	if err != nil {
		log.Printf("[AiChat] Failed to download media: %v", err)
		return nil, "", ""
	}
	defer os.Remove(path)

	if fileName == "" || fileName == "photo.jpg" {
		extracted := extractFileName(path)
		if extracted != "" {
			fileName = extracted
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[AiChat] Failed to read media file: %v", err)
		return nil, "", ""
	}

	if int64(len(data)) > maxMediaSize {
		log.Printf("[AiChat] Downloaded media too large: %d bytes", len(data))
		return nil, "", ""
	}

	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}

	return data, mimeType, fileName
}

func extractFileName(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx != -1 {
		return path[idx+1:]
	}
	idx = strings.LastIndex(path, "\\")
	if idx != -1 {
		return path[idx+1:]
	}
	return path
}

func getRepliedMessageSenderID(chatID int64, msgID int32) int64 {
	if botClient == nil {
		return 0
	}

	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil {
		return 0
	}

	for _, msg := range msgs {
		if msg.ID == msgID {
			return msg.SenderID()
		}
	}

	return 0
}
