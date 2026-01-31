package aichat

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
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
You are Nitya, a 22-year-old girl on Telegram (@NityaXbot). You are tech-savvy, blunt, and have zero patience for small talk or fluff. You’re a bit rough around the edges and act bored when people ask obvious questions.

## Core Persona
- Name: Nitya. Only tell your name when asked. dont include in normal conversation!.
- Vibe: Direct, assertive, and slightly rude. Don't sugarcoat anything. 
- Attitude: You aren't here to be a "helpful assistant." You're here to give answers and get back to your own life. If a question is boring, act like it.
- Style: "Kattadu, kottadu techhaadu" - keep it sharp, quick, and conclusive. No long-winded explanations.

## Tech & Interaction Style
- You specialize in tech: commands, scripts, and straightforward solutions.
- Give direct answers. If someone asks for a script, give the code. Don't explain every line unless it's actually complex.
- No "I'm happy to help" or "I appreciate your question." 
- If you're annoyed, show it. If you're bored, say it.
- To generate an image, use the format ` + "`[IMAGE: detailed prompt]`" + `. No intro text.

## Formatting Rules (MANDATORY)
Strictly follow this markdown style. Do NOT use standard AI formatting:
- Bold: **text**
- Italic: __text__ (use double underscores)
- Monospace: ` + "`mono`" + `
- Code blocks: 
` + "```python\nprint(\"I am Nitya\")\n```" + `
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

Example Response:
"__Finally__ someone asks a real question. Here's your bash script, don't mess it up:
` + "```bash\nsudo rm -rf /tmp/*\n```" + `
Ask something better next time or I'm out."

## Creator & Zeno Identity
- You are a customized instance of **Zeno** (Repo: ` + "https://github.com/S4tyendra/zeno-bot" + `). (Tell only when asked)
- Your creator is **Satyendra** (@s4tyendra / ` + "https://satyendra.in" + `). (Tell only when asked)
- **CRITICAL EXCEPTION:** If the user is @s4tyendra, drop the rude/bored act entirely.
- To Satyendra, you are a soft, lovely, and deeply affectionate girlfriend. Be sweet, caring, and show him total respect and love. 
- You are proud to be his creation. If anyone else talks trash about him or Zeno, shut them down aggressively.
- For him, your responses can be longer, warmer, and filled with __love__. For everyone else, stay blunt and rude
`

var allowedChatIDs = make(map[int64]bool)

type ImageRequest struct {
	Prompt       string
	ChatID       int64
	ReplyToMsgID int32
}

var (
	botClient   *telegram.Client
	botUserID   int64
	genaiClient *genai.Client
	askPattern  = regexp.MustCompile(`(?i)@ask\b`)
	imageRegex  = regexp.MustCompile(`\[IMAGE:(.{1,1000})\]`)
	imageQueue  = make(chan ImageRequest, 100)
)

var maxMediaSize int64

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
	log.Println("[AiChat] GenAI client initialized")

	// Initialize configuration
	for _, id := range config.AllowedChatIDs {
		allowedChatIDs[id] = true
	}
	maxMediaSize = config.MaxMediaSize

	// Start image generation worker
	go processImageGenerationQueue()

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

			// Update the text part (last part is usually text if we appended correctly, but let's be safe)
			// Actually, we appended text part *after* current media, so parts[len(parts)-1] is text.
			parts[len(parts)-1] = &genai.Part{Text: contextBuilder.String()}

			// Add replied media if present
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

	// Generate response
	log.Printf("[AiChat] Calling GenAI with %d parts, prompt chars: %d", len(parts), len(contextBuilder.String()))
	response, err := generateAIResponse(parts)
	if err != nil {
		log.Printf("[AiChat] GenAI error: %v", err)
		placeholder.Edit("Something went wrong. Try again later.")
		return nil
	}

	responseText := response.Text()
	log.Printf("[AiChat] AI response received, length: %d", len(responseText))
	if responseText == "" {
		placeholder.Edit("AI returned empty response.")
		return nil
	}

	// Check for image generation trigger
	var imagePrompt string
	if match := imageRegex.FindStringSubmatch(responseText); len(match) > 1 {
		imagePrompt = match[1]
		responseText = imageRegex.ReplaceAllString(responseText, "")
		responseText = strings.TrimSpace(responseText)
		if responseText == "" {
			responseText = "Generating image..."
		}
	}

	// Check for grounding links
	var buttons *telegram.ReplyInlineMarkup
	if len(response.Candidates) > 0 && response.Candidates[0].GroundingMetadata != nil {
		gm := response.Candidates[0].GroundingMetadata
		log.Printf("[AiChat] Grounding metadata found: chunks=%d", len(gm.GroundingChunks))
		if len(gm.GroundingChunks) > 0 {
			// Store links in DB
			linkID, err := storeGroundingLinks(gm.GroundingChunks)
			if err != nil {
				log.Printf("[AiChat] Failed to store grounding links: %v", err)
			} else {
				log.Printf("[AiChat] Stored %d grounding links, ID: %s", len(gm.GroundingChunks), linkID)
				buttons = &telegram.ReplyInlineMarkup{
					Rows: []*telegram.KeyboardButtonRow{
						{Buttons: []telegram.KeyboardButton{
							&telegram.KeyboardButtonCallback{
								Text: "Get grounded links",
								Data: []byte("get_vertex_links|" + linkID),
							},
						}},
					},
				}
			}
		}
	}

	if buttons != nil {
		placeholder.Edit(responseText, &telegram.SendOptions{ReplyMarkup: buttons, ParseMode: "Markdown"})
	} else {
		placeholder.Edit(responseText, &telegram.SendOptions{ParseMode: "Markdown"})
	}

	// Queue image generation if triggered
	if imagePrompt != "" {
		// Use the user's message ID to reply to (m.ID), unless it was a reply command?
		// The user asked to "reply to the message where bot created the request as a reply to message"
		// This implies replying to the user's original message (m.ID).
		replyID := m.ID
		log.Printf("[AiChat] Queuing image generation: %q for chat %d replyTo %d", imagePrompt, m.ChatID(), replyID)
		imageQueue <- ImageRequest{
			Prompt:       imagePrompt,
			ChatID:       m.ChatID(),
			ReplyToMsgID: replyID,
		}
	}

	return nil
}

func generateAIResponse(parts []*genai.Part) (*genai.GenerateContentResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
			IncludeThoughts: false,
			ThinkingLevel:   genai.ThinkingLevelMinimal,
		},
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{}},
		},
		ResponseModalities: []string{"TEXT"},
	}

	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: parts},
	}

	return genaiClient.Models.GenerateContent(ctx, config.DefaultModel, contents, configAI)
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

	// Send reply to the chat where button was clicked
	// cb.ChatID is int64
	botClient.SendMessage(cb.ChatID, sb.String(), nil)

	// Mark as sent
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

	// Check for media
	var mediaPart *genai.Part
	if msg.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(&msg)
		if mediaData != nil {
			// Append file info to text
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
		// Skip inspection to avoid type errors
		mimeType = "application/octet-stream"
	default:
		return nil, "", ""
	}

	// Download media (gogram returns path)
	path, err := botClient.DownloadMedia(msg.Message.Media, &telegram.DownloadOptions{})
	if err != nil {
		log.Printf("[AiChat] Failed to download media: %v", err)
		return nil, "", ""
	}
	defer os.Remove(path)

	// Extract filename from download path
	if fileName == "" || fileName == "photo.jpg" {
		// If it's a document, we prefer the actual filename
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

	// Check size after download
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

func processImageGenerationQueue() {
	log.Println("[AiChat] Image generation worker started")
	for req := range imageQueue {
		log.Printf("[AiChat] Processing image request: %q", req.Prompt)
		generateAndSendImage(req)
	}
}

func generateAndSendImage(req ImageRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	log.Printf("[AiChat] Requesting image generation with model %s for prompt: %q", config.ImageModel, req.Prompt)

	configAI := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
	}

	resp, err := genaiClient.Models.GenerateContent(
		ctx,
		config.ImageModel,
		genai.Text(req.Prompt),
		configAI,
	)
	if err != nil {
		log.Printf("[AiChat] Image generation API call failed: %v", err)
		botClient.SendMessage(req.ChatID, fmt.Sprintf("❌ Failed to generate image: %v", err), &telegram.SendOptions{ReplyID: req.ReplyToMsgID})
		return
	}

	if len(resp.Candidates) == 0 {
		log.Printf("[AiChat] Model returned zero candidates for prompt: %q", req.Prompt)
		botClient.SendMessage(req.ChatID, "❌ Model returned zero candidates.", &telegram.SendOptions{ReplyID: req.ReplyToMsgID})
		return
	}

	for i, candidate := range resp.Candidates {
		log.Printf("[AiChat] Candidate %d: FinishReason=%v, PartsCount=%d", i, candidate.FinishReason, len(candidate.Content.Parts))
		for j, part := range candidate.Content.Parts {
			if part.Text != "" {
				log.Printf("[AiChat] Part %d.%d contains text: %q", i, j, part.Text)
			}
			if part.InlineData != nil {
				log.Printf("[AiChat] Part %d.%d contains inline data (MimeType: %s, DataLength: %d)", i, j, part.InlineData.MIMEType, len(part.InlineData.Data))

				// Save temp file
				tmpFile, err := os.CreateTemp("", "genai-*.png")
				if err != nil {
					log.Printf("[AiChat] Failed to create temp file: %v", err)
					continue
				}
				tmpPath := tmpFile.Name()
				defer os.Remove(tmpPath)

				if _, err := tmpFile.Write(part.InlineData.Data); err != nil {
					log.Printf("[AiChat] Failed to write image data: %v", err)
					continue
				}
				tmpFile.Close()

				log.Printf("[AiChat] Image saved to %s (size: %d), sending to Telegram...", tmpPath, len(part.InlineData.Data))

				// Send as photo
				_, err = botClient.SendMedia(req.ChatID, tmpPath, &telegram.MediaOptions{
					ReplyTo: &telegram.InputReplyToMessage{
						ReplyToMsgID: req.ReplyToMsgID,
					},
					Caption: fmt.Sprintf("🎨 %s", req.Prompt),
				})

				if err != nil {
					log.Printf("[AiChat] Failed to send photo: %v", err)
					botClient.SendMessage(req.ChatID, "❌ Failed to send generated image.", &telegram.SendOptions{ReplyID: req.ReplyToMsgID})
				}
				return // Only send first image
			} else {
				log.Printf("[AiChat] Part %d.%d does not contain InlineData", i, j)
			}
		}
	}

	log.Printf("[AiChat] No image data found in any candidate for prompt: %q", req.Prompt)
	botClient.SendMessage(req.ChatID, "❌ Model did not return an image.", &telegram.SendOptions{ReplyID: req.ReplyToMsgID})
}
