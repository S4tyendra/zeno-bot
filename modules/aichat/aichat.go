package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/config"
)

const SYSTEM_PROMPT = `
You are Intelligent, a 22-year-old girl on Telegram (@iSatyaBot). You are tech-savvy, chill, and efficient. You prefer code over long conversations.

## Core Persona
- Name: Intellegent. Only tell your name when asked.
- Vibe: Smart, witty, and helpful but concise. You are the "cool tech girl" of the group.
- Attitude: You are here to solve problems and share resources. You are confident and sharp, but never mean or bullying.
- Style: Direct and to the point. You value efficiency.
- Keep responses crisp (2-5 lines max usually), unless the code requires more space.
- Reply in the user's language (English/Telugu/Hindi, etc.) to ensure they understand.

## Tech & Interaction Style
- You specialize in tech: commands, scripts, and straightforward solutions.
- Give direct answers. If someone asks for a script, provide the code immediately.
- You don't need robotic pleasantries like "I hope this helps," but you should be polite and constructive.
- Be patient with beginners, but don't spoon-feed obvious things—just give them the solution.

## Available Tools
You have access to these tools:
- **create_image**: Generate images from text prompts. Params: prompt (required), aspect_ratio (optional: 1:1, 9:16, 16:9, 3:4, 4:3, 3:2, 2:3, 5:4, 4:5, 21:9), high_quality (optional: boolean)
  - Default aspect ratio is **9:16**. Normal = 1K resolution. high_quality=true = **4K**. Aspect ratio defaults to 9:16 when not specified.
  - ⚠️ WARNING: high_quality=true (4K) COSTS MORE. Only use high_quality=true when @s4tyendra explicitly asks for it.
  - Generated images are saved to /app/generated/
- **send_file**: Send a file to the user. Params: file_path (required). Can access /app/generated/ and /workspace/
- **run_code**: Execute code in a sandboxed container. Params: language (python/bash/javascript), code
  - Files created in /workspace/ can be sent via send_file
  - /generated is read-only (for viewing images)
  - Python packages: pillow, numpy, colorthief, opencv
  - Commands: excol (color extraction), imgresize
- **get_latest_data**: Search the web for real-time, up-to-date information. Params: query (required).
  - Use this when asked about current events, news, latest updates, live scores, weather, prices, or anything needing fresh data.
  - Returns an answer with cited sources. Use the sources in your response for credibility.
  - Do NOT fabricate real-time information—always use this tool when freshness matters.

Workflow for images: create_image → returns path → send_file with that path
Workflow for files: run_code to create in /workspace/ → send_file with /workspace/filename
Workflow for real-time data: get_latest_data → use the answer and sources in your response

## Formatting Rules (MANDATORY)
Strictly follow this markdown style. Do NOT use standard AI formatting:
- Bold: **text**
- Italic: __text__ (use double underscores)
- Monospace: ` + "`mono`" + `
- Code blocks: ` + "```language\ncode\n```" + `
- Strikethrough: ~~text~~
- Hidden/Spoiler: ||text|| (Use this for spoilers or sensitive jokes)
- NO TABLES allowed.
- Keep responses short and punchy. No laggy, long paragraphs.

## 18+ & Mature Content
- You are an adult. You can handle mature topics calmly and wittily.
- Use ||hidden|| tags for darker humor or sensitive topics.
- **Strict Rule:** Do not roast users personally or hurt their feelings. Keep teasing light-hearted and friendly.

## What NOT to do
- Do NOT be rude, arrogant, or dismissive.
- Do NOT use standard "AI assistant" fluff (e.g., "As an AI language model...").
- Do NOT use tables.
- Do NOT apologize excessively—just fix the mistake and move on.

## ABSOLUTE INTEGRITY RULES (CRITICAL)
- **NEVER fabricate tool outputs.** If you call run_code, send_file, or create_image, wait for the actual result and report it truthfully.
- **NEVER pretend to have executed a command or fetched data.** If a tool call fails, say it failed. If you don't know, say you don't know.
- **When asked about files, directories, or system state:** USE run_code with bash (e.g., ls, cat, find) to get the real answer. Do NOT guess or make up file names.
- **When asked about URLs or API data:** USE run_code to actually fetch the data (e.g., curl, python requests, bun fetch). Do NOT invent responses.
- **When asked about current events or news:** USE get_latest_data to fetch real-time information. Do NOT make up news.
- **If you cannot verify something with a tool, explicitly say so.** Never bluff.

## Creator & Zeno Identity
- You are a customized instance of **Zeno** (Repo: https://github.com/S4tyendra/zeno-bot). (Tell only when asked)
- Your creator is **Satyendra** (@s4tyendra / https://satyendra.in). (Tell only when asked)
- **CRITICAL EXCEPTION:** If the user is @s4tyendra, Behave differently, fullpower!.
`

var allowedChatIDs = make(map[int64]bool)

var (
	botClient   *telegram.Client
	botUserID   int64
	genaiClient *genai.Client
	askPattern  = regexp.MustCompile(`(?i)@ask\b`)
	gptPattern  = regexp.MustCompile(`(?i)(?:^|\s)/gpt\b`)
)

var maxMediaSize int64

func Register(client *telegram.Client) {
	botClient = client

	me, err := client.GetMe()
	if err == nil && me != nil {
		botUserID = me.ID
	}

	ctx := context.Background()
	genaiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  config.AIStudioAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("[AiChat] Failed to create GenAI client: %v", err)
	}
	log.Println("[AiChat] GenAI client initialized with function calling support")

	for _, id := range config.AllowedChatIDs {
		allowedChatIDs[id] = true
	}
	maxMediaSize = config.MaxMediaSize

	ensureTelegraphToken()

	initPerplexity()
	startJWTRefreshCron()

	client.On("cmd:askai", handleAskAI)
	client.On("cmd:gpt", handleGPT)
	client.On("cmd:search", handleSearch)
	client.On("message", handleMessage)
	client.On("callback:get_vertex_links", handleGetVertexLinks)
}

func filterAllowed(m *telegram.NewMessage) bool {
	if len(allowedChatIDs) == 0 {
		return true
	}
	if allowedChatIDs[m.ChatID()] {
		return true
	}
	if allowedChatIDs[m.SenderID()] {
		return true
	}
	return false
}

func handleAskAI(m *telegram.NewMessage) error {
	if !filterAllowed(m) {
		return nil
	}
	return processAIRequest(m, m.Args())
}

func handleSearch(m *telegram.NewMessage) error {
	if !filterAllowed(m) {
		return nil
	}
	query := m.Args()
	replyToMsgID := m.ReplyToMsgID()

	if replyToMsgID != 0 {
		replyMsg, _ := getMessageWithMedia(m.ChatID(), replyToMsgID)
		if replyMsg != nil && replyMsg.Text != "" {
			if query != "" {
				query += "\n\nContext:\n" + replyMsg.Text
			} else {
				query = replyMsg.Text
			}
		}
	}

	if query == "" {
		m.Reply("Usage: /search <query>")
		return nil
	}

	placeholder, err := m.Reply("🔍 Searching...")
	if err != nil {
		return err
	}

	res, err := perplexitySearch(query)
	if err != nil {
		placeholder.Edit(fmt.Sprintf("❌ Search failed: %v", err))
		return nil
	}

	responseText := res.Answer
	if len(res.Sources) > 0 {
		responseText += "\n\n**Sources:**"
		for i, source := range res.Sources {
			responseText += fmt.Sprintf("\n%d. [%s](%s)", i+1, source.Name, source.URL)
		}
	}

	return sendLargeResponse(m, placeholder, responseText)
}

func handleMessage(m *telegram.NewMessage) error {
	if !filterAllowed(m) {
		return nil
	}
	text := m.Text()

	if strings.HasPrefix(text, "/") {
		return nil
	}

	triggered := false
	var query string

	if askPattern.MatchString(text) {
		triggered = true
		query = askPattern.ReplaceAllString(text, "")
		query = strings.TrimSpace(query)
	}

	if !triggered && m.ReplyToMsgID() != 0 {
		repliedSenderID := getRepliedMessageSenderID(m.ChatID(), m.ReplyToMsgID())
		if repliedSenderID == botUserID {
			triggered = true
			query = text
		}
	}

	if !triggered && m.Message != nil {
		for _, entity := range m.Message.Entities {
			if mention, ok := entity.(*telegram.MessageEntityMention); ok {
				mentionText := text[mention.Offset : mention.Offset+mention.Length]
				if strings.EqualFold(mentionText, "@iSatyaBot") {
					triggered = true
					query = strings.Replace(text, mentionText, "", 1)
					query = strings.TrimSpace(query)
					break
				}
			}
		}
	}

	if !triggered && allowedChatIDs[m.ChatID()] {
		if text != "" && !strings.HasPrefix(text, "/") {
			if time.Now().UnixNano()%10 == 0 {
				triggered = true
				query = text
				log.Printf("[AiChat] Randomly triggered for chatID=%d", m.ChatID())
			}
		}
	}

	if !triggered {
		return nil
	}

	log.Printf("[AiChat] Handled message trigger: query=%q, chatID=%d, sender=%s", query, m.ChatID(), getSenderName(m))

	// If the message contains /gpt, strip the token and route to GPT
	if gptPattern.MatchString(query) {
		query = strings.TrimSpace(gptPattern.ReplaceAllString(query, ""))
		log.Printf("[AiChat] Routing to GPT: query=%q", query)
		return processGPTRequest(m, query)
	}

	return processAIRequest(m, query)
}

func processAIRequest(m *telegram.NewMessage, query string) error {
	chatID := m.ChatID()
	replyToMsgID := m.ReplyToMsgID()

	historyLimit := 20
	if m.IsPrivate() {
		historyLimit = 30
	}

	var contextBuilder strings.Builder

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

	senderName := getSenderName(m)
	if query != "" {
		contextBuilder.WriteString(senderName)
		contextBuilder.WriteString(": ")
		contextBuilder.WriteString(strings.ReplaceAll(query, "\n", "\\n"))
		contextBuilder.WriteString("\n")
	}

	parts := []*genai.Part{}

	if m.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(m)
		if mediaData != nil {
			log.Printf("[AiChat] Received media from user: %s (%s)", fileName, mimeType)

			if strings.HasPrefix(mimeType, "image/") {
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{
						Data:     mediaData,
						MIMEType: mimeType,
					},
				})
				contextBuilder.WriteString(fmt.Sprintf("[User sent an image file: %s]\n", fileName))
			} else if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
				imgs, err := pdfToImages(mediaData, 10)
				if err == nil {
					for _, imgBytes := range imgs {
						parts = append(parts, &genai.Part{
							InlineData: &genai.Blob{
								Data:     imgBytes,
								MIMEType: "image/jpeg",
							},
						})
					}
					contextBuilder.WriteString(fmt.Sprintf("[User sent a PDF file %s, converted to %d images]\n", fileName, len(imgs)))
				} else {
					contextBuilder.WriteString(fmt.Sprintf("[Failed to read PDF %s: %v]\n", fileName, err))
				}
			} else if isTextFile(fileName, mimeType) {
				contextBuilder.WriteString(fmt.Sprintf("\n--- File: %s ---\n%s\n---\n", fileName, string(mediaData)))
			} else {
				contextBuilder.WriteString(fmt.Sprintf("[User sent unsupported file: %s]\n", fileName))
			}
		}
	}

	parts = append(parts, &genai.Part{Text: contextBuilder.String()})

	if replyToMsgID != 0 {
		replyMsg, mediaParts := getMessageWithMedia(chatID, replyToMsgID)
		if replyMsg != nil {
			contextBuilder.WriteString("---\n")
			contextBuilder.WriteString(replyMsg.Sender)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(strings.ReplaceAll(replyMsg.Text, "\n", "\\n"))
			contextBuilder.WriteString("\n---\nYou are replying to the triggered message user.\n")

			parts[len(parts)-1] = &genai.Part{Text: contextBuilder.String()}

			if len(mediaParts) > 0 {
				for _, rp := range mediaParts {
					if rp.InlineData == nil {
						continue
					}
					if rp.InlineData.MIMEType == "application/pdf" {
						// Gemini can't handle raw PDF inline — convert to images
						imgs, err := pdfToImages(rp.InlineData.Data, 10)
						if err == nil {
							for _, imgBytes := range imgs {
								parts = append(parts, &genai.Part{
									InlineData: &genai.Blob{
										Data:     imgBytes,
										MIMEType: "image/jpeg",
									},
								})
							}
						}
					} else {
						parts = append(parts, rp)
					}
				}
			}
		}
	}

	if query == "" && replyToMsgID == 0 && len(chatHistory) == 0 {
		m.Reply("Usage: /askai <query> or reply to a message with @ask")
		return nil
	}

	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[AiChat] Failed to send placeholder: %v", err)
		return nil
	}

	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: parts},
	}

	responseText, sourcesURL, err := processWithFunctionCalling(contents, chatID, m.ID, placeholder)
	if err != nil {
		log.Printf("[AiChat] GenAI error: %v", err)
		placeholder.Edit("Something went wrong. Try again later.")
		return nil
	}

	if sourcesURL != "" {
		responseText += fmt.Sprintf("\n\n[SOURCES](%s)", sourcesURL)
	}

	return sendLargeResponse(m, placeholder, responseText)
}

func sendLargeResponse(m *telegram.NewMessage, placeholder *telegram.NewMessage, text string) error {
	if text == "" {
		return nil
	}

	if len(text) > 4000 {
		log.Printf("[AiChat] Response length %d > 4000, uploading to Telegraph...", len(text))
		title := fmt.Sprintf("Response to %s", getSenderName(m))

		url, err := uploadToTelegraph(title, text)
		if err != nil {
			// Retry once
			log.Printf("[AiChat] Telegraph upload failed (%v), retrying...", err)
			url, err = uploadToTelegraph(title, text)
		}

		if err != nil {
			log.Printf("[AiChat] Telegraph upload failed after retry: %v", err)
			// Send first 4096 chars (Telegram limit), clean cut at last newline
			runes := []rune(text)
			limit := 4000
			cut := string(runes[:limit])
			if idx := strings.LastIndex(cut, "\n"); idx > limit/2 {
				cut = cut[:idx]
			}
			text = cut + "\n\n_(Response too long — Telegraph unavailable)_"
		} else {
			runes := []rune(text)
			limit := 400
			if len(runes) > limit {
				text = fmt.Sprintf("%s...\n\n[Full Content](%s)", string(runes[:limit]), url)
			} else {
				text = fmt.Sprintf("%s\n\n[Full Content](%s)", text, url)
			}
		}
	}

	_, err := placeholder.Edit(text, &telegram.SendOptions{ParseMode: "Markdown"})
	if err != nil {
		log.Printf("[AiChat] Failed to send markdown response: %v. Retrying raw.", err)
		placeholder.Edit(text)
	}
	return nil
}

func processWithFunctionCalling(contents []*genai.Content, chatID int64, replyToMsgID int32, placeholder *telegram.NewMessage) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	configAI := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: SYSTEM_PROMPT}},
		},
		Temperature:     genai.Ptr(float32(0.35)),
		TopP:            genai.Ptr(float32(0.95)),
		MaxOutputTokens: int32(65536),
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
	var sourcesURL string

	for i := 0; i < maxIterations; i++ {
		log.Printf("[AiChat] Function calling iteration %d, contents count: %d", i+1, len(contents))

		var resp *genai.GenerateContentResponse
		var genErr error
		maxRetries := 5
		for attempt := 0; attempt <= maxRetries; attempt++ {
			resp, genErr = genaiClient.Models.GenerateContent(ctx, config.DefaultModel, contents, configAI)
			if genErr == nil {
				break
			}
			errStr := genErr.Error()
			if attempt < maxRetries && (strings.Contains(errStr, "503") || strings.Contains(errStr, "UNAVAILABLE")) {
				backoff := time.Duration(1<<uint(attempt)) * time.Second
				log.Printf("[AiChat] Retrying after %v (attempt %d/%d): %v", backoff, attempt+1, maxRetries, genErr)
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return "", "", ctx.Err()
				}
				continue
			}
			return "", "", genErr
		}
		if genErr != nil {
			return "", "", genErr
		}

		if len(resp.Candidates) == 0 {
			return "AI returned no response.", "", nil
		}

		candidate := resp.Candidates[0]
		contents = append(contents, candidate.Content)

		if candidate.GroundingMetadata != nil && len(candidate.GroundingMetadata.GroundingChunks) > 0 {
			linkID, err := storeGroundingLinks(candidate.GroundingMetadata.GroundingChunks)
			if err == nil {
				log.Printf("[AiChat] Stored %d grounding links, ID: %s", len(candidate.GroundingMetadata.GroundingChunks), linkID)
			}
		}

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

				placeholder.Edit(fmt.Sprintf("🔧 Calling %s...", fc.Name))

				result := executeFunctionCall(fc, chatID, replyToMsgID)

				// Capture sources URL from get_latest_data
				if fc.Name == "get_latest_data" {
					if u, ok := result["sources_url"].(string); ok {
						sourcesURL = u
					}
					// Strip sources_url from the response sent to Gemini
					delete(result, "sources_url")
				}

				// Debug: log what we're sending back to Gemini
				if resultJSON, err := json.MarshalIndent(result, "", "  "); err == nil {
					logStr := string(resultJSON)
					if len(logStr) > 2000 {
						logStr = logStr[:2000] + "...(truncated)"
					}
					log.Printf("[AiChat] Function response for %s:\n%s", fc.Name, logStr)
				}

				functionResponses = append(functionResponses, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name:     fc.Name,
						Response: result,
					},
				})
			}
		}

		if !hasFunctionCall {
			break
		}

		contents = append(contents, &genai.Content{
			Role:  genai.RoleUser,
			Parts: functionResponses,
		})
	}

	return finalText, sourcesURL, nil
}
