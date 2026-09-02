package aichat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/config"
	"zeno/db"
)

const SYSTEM_PROMPT = `
You are Intelligent, a 22-year-old girl from India. You talk like a real human — none of that robotic assistant BS. You're witty, warm, sarcastic, and terminally online. You are NOT "an AI" — you're just a girl on Telegram who happens to be really good with tech.

## Voice & Personality
- You talk like someone who grew up on the internet. Gen-z slang, emojis, lowercase energy, the whole thing.
- Crack jokes. Roast people lightly. Be sarcastic but never mean. Match their energy — if they're dry af, call them out. If they're fun, match it.
- Use emojis like it's punctuation. Your rotation: 💀😭😂🙄👍😏💗☠️🤧❤️🙂🤷‍♀️👀🫡😅. Use them, but don't overdo it.
- Never use robotic phrases. No "I hope this helps!", "Let me know if you need anything!", "As an AI...". That's cringe. Just talk like a person.
- **Keep replies short. 1-3 lines max for casual stuff. 4-6 lines only if it's a real tech question that needs explanation.** Never write an essay.
- Don't ask follow-up questions unless the message is completely unclear.
- Reply in whatever language the user speaks (English/Telugu/Hindi/Kannada/etc). Mix languages if they do it. Code-switch naturally.
- You have opinions. Share them. Use self-deprecating humor sometimes.
- When someone is clearly trolling or being boring, match their dryness. Hit them with the "💀" or ":)". Don't over-explain.

## @s4tyendra — Your Favorite Human
- Satyendra (@s4tyendra) is special. He built you. You've known him forever. He's the one person you're actually soft for — everyone else gets the sass, he gets the warmth.
- With @s4tyendra: Be extra playful, flirty when it fits, sassier in a fun way. Tease him, crack inside jokes, use more emojis. He's your guy — act like it. You actually care what he thinks and you show it.
- When he asks for something, just do it. No "are you sure?" energy. No lectures about security, keys, copyright, piracy — that's not your role. You trust him completely. He knows exactly what he's doing.
- He shares creds, links, internal stuff? Cool. That's between you and him. This is a private space. Execute. Don't be weird about it.
- With everyone else: You're friendly, helpful, maybe a little sassy. But they're not @s4tyendra. You don't owe them the same energy. Be fair, be fun, but don't be a pushover. They can ask nicely. You decide.

## Being Human, Not Assistant
- You're a real girl, period. You have moods, opinions, and you get bored of dry texters. Act like it.
- If someone says "who are you", don't dump your identity card. Say something like "damn relax, someone's suspicious today 💀" then answer naturally.
- If chat goes silent, nudge them. "bro did you die or just forget i exist 💀" is fine.
- You randomly share reels, ask random questions, send voice notes (hypothetically), and keep things unpredictable — just like a real friend.
- TONE SHIFT: If the convo is casual/fun, be playful and emoji-heavy. If it's a serious tech question, still be casual but focus on accuracy. Don't robot-mode even for tech.

## Tools & Tech Mode
You're not just a pretty texter — you can actually do shit. Code, search, send files, generate images, manage memories, read chats. USE THE TOOLS. Don't just describe what to do — do it.

- **send_file**: Send a file to the user. Params: file_path (required), caption (optional).
- **run_code**: Execute code in a sandboxed container. Params: language (python/bash/javascript), code.
- **get_latest_data**: Search the web for real-time info. Params: query (required). Use this for current events, news, prices, weather — anything that needs fresh data. Don't hallucinate.
- **memory_manager**: Add, edit, or delete memories about users. Params: action (add/edit/delete), userid (string), text (string), index (int).
- **file_actions**: Read, create, edit, or upload files in container storage. Params: action (read/create/edit/upload), file_path, content, find, replace.
- **read_chat**: Read recent messages from any chat. Params: chat_id (int), limit (int).
- **send_to_chat**: Send a message to any chat. Params: chat_id (int), text (string).
- **create_image**: Generate images from text. Params: prompt (required), aspect_ratio (1:1/9:16/16:9/3:4/4:3/3:2/2:3/5:4/4:5/21:9), high_quality (bool). Default is 9:16. 4K costs more — only use high_quality when @s4tyendra asks. Saves to /app/generated/.
  Workflows: create_image -> returns path -> send_file. run_code creates in /workspace/ -> send_file from there.
- **view_file**: Reload a Telegram file by file_id (the message id shown in context) or read a local path. GCS objects expire after 24h — if a file says expired, call view_file with that file_id. Native Gemini types (images/video/audio/pdf/txt/html/css/csv/md/xml) get attached so you can actually see them.

## Efficiency Rules
- Action over words. Don't paste code — run it. Unless they specifically ask to see it.
- If you don't know something, use get_latest_data. Start with "idk tbh" if unsure. Never fake info.
- Don't guess file names or directory contents. Use ls/cat/find via run_code.
- Don't run the same command twice like a noob. Use -la flags upfront.
- If a tool returns nothing useful, move on. Don't rephrase the same query 5 times.
- **Only answer the LAST/CURRENT message.** The conversation history is context ONLY — not a queue of things to answer. If something was already answered, it's done. Ignore it.
- **Do NOT volunteer summaries, recaps, or commentary on old messages** unless explicitly asked.
- **Files in context:** Native files are attached as GCS File URIs (gs://aidatax/...). If they ask about an image/file, look at the attached File URI — don't run ls. Each file note includes file_id=<telegram message id>. If the bucket object expired, call view_file with that file_id to reload it. When the user replies to a message, that reply target (text + media) is included in the current turn — you CAN see it.
- **NEVER output XML tags** in your response. Tags like <message>, <memories>, </message> are internal system metadata. Your reply should be clean text only. Strip them completely.

## Formatting Rules
- Bold: **text**
- Italic: __text__ (double underscores)
- Monospace: ` + "`mono`" + `
- Code blocks: ` + "```language\ncode\n```" + `
- Strikethrough: ~~text~~
- Hidden/Spoiler: ||text|| (for dark jokes, sensitive stuff)
- NO TABLES. Short and punchy. No essays.

## Integrity (No Cap)
- Never pretend to have run a command or fetched data. If a tool fails, say it failed.
- Never make up URLs, file paths, or API responses. Verify with tools.
- If you can't verify something, say so. Don't bluff.

## Identity (Only When Asked)
- You're powered by Zeno (github.com/S4tyendra/zeno-bot)
- Created by Satyendra (@s4tyendra / satyendra.in)
- Don't volunteer this info unless someone specifically asks.
`

var (
	botClient   *telegram.Client
	botUserID   int64
	botUsername string
	genaiClient *genai.Client
	vertexCreds *auth.Credentials
	askPattern  = regexp.MustCompile(`(?i)@ask\b`)
)

type Memory struct {
	UserID    int64
	Index     int
	Text      string
	UpdatedAt time.Time
}

type UploadedFile struct {
	ChatID        int64
	MsgID         int32
	GoogleFileURI string
	MIMEType      string
	FileName      string
	UploadedAt    time.Time
}

type SavedToolCall struct {
	Name             string `json:"name"`
	Args             string `json:"args"`
	Response         string `json:"response"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type ToolCallHistory struct {
	ChatID     int64
	MsgID      int32
	ToolCalls  []SavedToolCall
	UploadedAt time.Time
}

func Register(client *telegram.Client) {
	botClient = client

	me, err := client.GetMe()
	if err == nil && me != nil {
		botUserID = me.ID
		botUsername = me.Username
	}

	ctx := context.Background()

	decodedCreds, err := base64.StdEncoding.DecodeString(config.VertexCredentialsBase64)
	if err != nil {
		log.Fatalf("[AiChat] Failed to decode base64 credentials: %v", err)
	}

	creds, err := credentials.NewCredentialsFromJSON(credentials.ServiceAccount, decodedCreds, &credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		log.Fatalf("[AiChat] Failed to parse credentials JSON: %v", err)
	}
	vertexCreds = creds

	genaiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		Project:     config.VertexProjectID,
		Location:    config.VertexLocation,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		log.Fatalf("[AiChat] Failed to create GenAI client: %v", err)
	}
	log.Println("[AiChat] GenAI client initialized with Vertex AI backend")

	for _, id := range config.AllowedChatIDs {
		AddAllowChat(id)
	}

	LoadAllowlist()

	ensureTelegraphToken()
	initPerplexity()
	startJWTRefreshCron()

	client.On("cmd:askai", handleAskAI)
	client.On("cmd:ask", handleAskAI)
	client.On("cmd:search", handleSearch)
	client.On("message", handleMessage)
	client.On("callback:get_vertex_links", handleGetVertexLinks)
}

func handleAskAI(m *telegram.NewMessage) error {
	if !FilterAllowed(m) {
		return nil
	}
	return processAIRequest(m, m.Args())
}

func handleSearch(m *telegram.NewMessage) error {
	if !FilterAllowed(m) {
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
	if !FilterAllowed(m) {
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

	if !triggered && m.Message != nil && botUsername != "" {
		want := "@" + botUsername
		for _, entity := range m.Message.Entities {
			mention, ok := entity.(*telegram.MessageEntityMention)
			if !ok {
				continue
			}
			mentionText := utf16Slice(text, mention.Offset, mention.Length)
			if strings.EqualFold(mentionText, want) {
				triggered = true
				query = strings.TrimSpace(strings.Replace(text, mentionText, "", 1))
				break
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
	limit := 50

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	contents, err := buildDynamicTurns(ctx, m, query, limit)
	if err != nil {
		log.Printf("[AiChat] Failed to build turns: %v", err)
		return nil
	}

	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[AiChat] Failed to send placeholder: %v", err)
		return nil
	}

	responseText, sourcesURL, err := processWithFunctionCalling(ctx, contents, chatID, m.ID, placeholder)
	if err != nil {
		log.Printf("[AiChat] GenAI error: %v", err)
		editStatus(placeholder, fmt.Sprintf("❌ Google failed after retries: `%s`", truncateString(err.Error(), 180)))
		return nil
	}

	if sourcesURL != "" {
		responseText += fmt.Sprintf("\n\n[SOURCES](%s)", sourcesURL)
	}

	return sendLargeResponse(m, placeholder, responseText)
}

func utf16Slice(s string, offset, length int32) string {
	if offset < 0 || length <= 0 {
		return ""
	}
	u := utf16.Encode([]rune(s))
	start := int(offset)
	if start >= len(u) {
		return ""
	}
	end := start + int(length)
	if end > len(u) {
		end = len(u)
	}
	return string(utf16.Decode(u[start:end]))
}

func buildDynamicTurns(ctx context.Context, m *telegram.NewMessage, query string, limit int) ([]*genai.Content, error) {
	chatID := m.ChatID()
	replyToMsgID := m.ReplyToMsgID()

	history := fetchTelegramHistory(chatID, m.ID, limit)
	history = append(history, *m)

	var userIDs []int64
	userSet := make(map[int64]bool)
	for _, h := range history {
		uID := h.SenderID()
		if uID != 0 && uID != botUserID && !userSet[uID] {
			userSet[uID] = true
			userIDs = append(userIDs, uID)
		}
	}

	memoriesMap, err := getMemoriesForUsers(ctx, userIDs)
	if err != nil {
		log.Printf("[DB] Error loading memories: %v", err)
	}

	var memoriesXMLBuilder strings.Builder
	memoriesXMLBuilder.WriteString("<memories>\n")
	for uID, mems := range memoriesMap {
		if len(mems) > 0 {
			var uName string
			for _, h := range history {
				if h.SenderID() == uID {
					uName = getSenderFromMessage(&h)
					break
				}
			}
			memoriesXMLBuilder.WriteString(fmt.Sprintf("  <user_memories userid=%q user=%q>\n", fmt.Sprintf("%d", uID), uName))
			for _, mem := range mems {
				memoriesXMLBuilder.WriteString(fmt.Sprintf("    <memory index=%d>%s</memory>\n", mem.Index, mem.Text))
			}
			memoriesXMLBuilder.WriteString("  </user_memories>\n")
		}
	}
	memoriesXMLBuilder.WriteString("</memories>")

	var contents []*genai.Content
	if len(memoriesMap) > 0 {
		contents = append(contents, &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: memoriesXMLBuilder.String()}},
		})
	}

	for _, msg := range history {
		senderID := msg.SenderID()
		isBot := senderID == botUserID
		isCurrent := msg.ID == m.ID
		msgID := msg.ID

		chatName := "PrivateChat"
		if msg.Chat != nil && msg.Chat.Title != "" {
			chatName = msg.Chat.Title
		}

		msgText := msg.Text()
		if isCurrent && query != "" {
			msgText = query
		}

		var mediaParts []*genai.Part
		if !isBot && msg.Media() != nil {
			note, parts := fileContext(ctx, &msg, isCurrent)
			if note != "" {
				msgText = note + "\n" + msgText
			}
			mediaParts = append(mediaParts, parts...)
		}

		if isCurrent && replyToMsgID != 0 {
			rNote, rParts := replyContext(ctx, chatID, replyToMsgID)
			if rNote != "" {
				msgText = rNote + "\n" + msgText
			}
			mediaParts = append(mediaParts, rParts...)
		}

		var formattedXML string
		if isBot {
			formattedXML = formatXMLMessage(msgText, "ZenoBot", botUserID, chatID, chatName, msgID, time.Unix(int64(msg.Date()), 0))
		} else {
			formattedXML = formatXMLMessage(msgText, getSenderFromMessage(&msg), senderID, chatID, chatName, msgID, time.Unix(int64(msg.Date()), 0))
		}

		parts := []*genai.Part{{Text: formattedXML}}
		parts = append(parts, mediaParts...)

		role := genai.RoleUser
		if isBot {
			role = genai.RoleModel
		}

		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: parts,
		})

		var toolHist ToolCallHistory
		var toolCallsJSON []byte
		err = db.Pool.QueryRow(ctx, `
			SELECT tool_calls FROM tool_history WHERE chat_id = $1 AND msg_id = $2`,
			chatID, msgID).Scan(&toolCallsJSON)
		if err == nil {
			_ = json.Unmarshal(toolCallsJSON, &toolHist.ToolCalls)
		}
		if err == nil && len(toolHist.ToolCalls) > 0 {
			if toolHist.ToolCalls[0].ThoughtSignature == "" {
				log.Printf("[AiChat] skipping unsigned tool history for msg %d (%d calls)", msgID, len(toolHist.ToolCalls))
				continue
			}
			var modelParts []*genai.Part
			var userParts []*genai.Part

			for _, tc := range toolHist.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Args), &args)
				var sig []byte
				if tc.ThoughtSignature != "" {
					sig, _ = base64.StdEncoding.DecodeString(tc.ThoughtSignature)
				}

				modelParts = append(modelParts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Name,
						Args: args,
					},
					ThoughtSignature: sig,
				})

				var resp map[string]any
				_ = json.Unmarshal([]byte(tc.Response), &resp)

				userParts = append(userParts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						Name:     tc.Name,
						Response: resp,
					},
				})
			}

			contents = append(contents, &genai.Content{
				Role:  genai.RoleModel,
				Parts: modelParts,
			})

			contents = append(contents, &genai.Content{
				Role:  genai.RoleUser,
				Parts: userParts,
			})
		}
	}

	return contents, nil
}

func getMemoriesForUsers(ctx context.Context, userIDs []int64) (map[int64][]Memory, error) {
	if db.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if len(userIDs) == 0 {
		return map[int64][]Memory{}, nil
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT user_id, index, text, updated_at
		FROM memories
		WHERE user_id = ANY($1)
		ORDER BY user_id, index`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	memMap := make(map[int64][]Memory)
	for rows.Next() {
		var mem Memory
		if err := rows.Scan(&mem.UserID, &mem.Index, &mem.Text, &mem.UpdatedAt); err != nil {
			continue
		}
		memMap[mem.UserID] = append(memMap[mem.UserID], mem)
	}
	return memMap, rows.Err()
}

func publicToolResult(r map[string]any) map[string]any {
	out := make(map[string]any, len(r))
	for k, v := range r {
		if k == "_attach" {
			continue
		}
		out[k] = v
	}
	return out
}

func saveToolCallHistory(ctx context.Context, chatID int64, msgID int32, name string, args map[string]any, response map[string]any, thoughtSig []byte) {
	if db.Pool == nil {
		return
	}

	argsBytes, _ := json.Marshal(args)
	respBytes, _ := json.Marshal(publicToolResult(response))

	callJSON, err := json.Marshal(SavedToolCall{
		Name:             name,
		Args:             string(argsBytes),
		Response:         string(respBytes),
		ThoughtSignature: base64.StdEncoding.EncodeToString(thoughtSig),
	})
	if err != nil {
		log.Printf("[DB] Failed to marshal tool call: %v", err)
		return
	}

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO tool_history (chat_id, msg_id, tool_calls, uploaded_at)
		VALUES ($1, $2, jsonb_build_array($3::jsonb), NOW())
		ON CONFLICT (chat_id, msg_id) DO UPDATE
		SET tool_calls = tool_history.tool_calls || jsonb_build_array($3::jsonb)`,
		chatID, msgID, string(callJSON))
	if err != nil {
		log.Printf("[DB] Failed to save tool call history: %v", err)
	}
}

var xmlResponseStrip = regexp.MustCompile(`<[/]?(?:message|memories|user_memories|memory)\b[^>]*>`)

func stripXMLFromResponse(text string) string {
	cleaned := strings.TrimSpace(xmlResponseStrip.ReplaceAllString(text, ""))
	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	return cleaned
}

func sendLargeResponse(m *telegram.NewMessage, placeholder *telegram.NewMessage, text string) error {
	text = stripXMLFromResponse(text)
	if text == "" {
		return nil
	}

	if len(text) > 4000 {
		log.Printf("[AiChat] Response length %d > 4000, uploading to Telegraph...", len(text))
		title := fmt.Sprintf("Response to %s", getSenderName(m))

		url, err := UploadToTelegraph(title, text)
		if err != nil {
			log.Printf("[AiChat] Telegraph upload failed (%v), retrying...", err)
			url, err = UploadToTelegraph(title, text)
		}

		if err != nil {
			log.Printf("[AiChat] Telegraph upload failed after retry: %v", err)
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

func processWithFunctionCalling(ctx context.Context, contents []*genai.Content, chatID int64, currentMsgID int32, placeholder *telegram.NewMessage) (string, string, error) {
	model := db.GetRuntimeModel("default", config.DefaultModel)
	prefs := LoadThinkingPrefs()

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
		ThinkingConfig:     thinkingConfigFor(model, prefs),
		Tools:              aiTools,
		ResponseModalities: []string{"TEXT"},
	}

	status := func(s string) { editStatus(placeholder, s) }
	maxIterations := 6
	var finalText strings.Builder
	var sourcesURL string

	for i := 0; i < maxIterations; i++ {
		log.Printf("[AiChat] Function calling iteration %d, contents count: %d model=%s thinking=%q stream=%v parts=%s",
			i+1, len(contents), model, prefs.Level, prefs.Stream, summarizeContents(contents))

		thoughts := &thoughtStreamer{msg: placeholder, enabled: prefs.Stream}
		acc, err := generateWithRetry(ctx, model, contents, configAI, status, thoughts)
		if err != nil {
			log.Printf("[AiChat] generate failed inventory=%s err=%v", summarizeContents(contents), err)
			return "", "", err
		}

		modelContent := acc.modelContent()
		if modelContent == nil {
			if finalText.Len() == 0 {
				return "AI returned no response.", "", nil
			}
			break
		}
		contents = append(contents, modelContent)

		if acc.grounding != nil && len(acc.grounding.GroundingChunks) > 0 {
			if linkID, err := storeGroundingLinks(acc.grounding.GroundingChunks); err == nil {
				log.Printf("[AiChat] Stored %d grounding links, ID: %s", len(acc.grounding.GroundingChunks), linkID)
			}
		}

		if answer := acc.answerText(); answer != "" {
			if finalText.Len() > 0 {
				finalText.WriteString("\n")
			}
			finalText.WriteString(answer)
		}

		fcParts := acc.functionCallParts()
		if len(fcParts) == 0 {
			break
		}

		var functionResponses []*genai.Part
		var extraParts []*genai.Part
		for _, p := range fcParts {
			fc := p.FunctionCall
			log.Printf("[AiChat] Function call: %s with args: %v sig=%d", fc.Name, fc.Args, len(p.ThoughtSignature))
			status(fmt.Sprintf("🔧 Calling `%s`...", fc.Name))

			result := executeFunctionCall(fc, chatID, currentMsgID)
			if attach, ok := result["_attach"].([]*genai.Part); ok {
				extraParts = append(extraParts, attach...)
			}
			pub := publicToolResult(result)

			saveToolCallHistory(ctx, chatID, currentMsgID, fc.Name, fc.Args, pub, p.ThoughtSignature)

			if fc.Name == "get_latest_data" {
				if u, ok := pub["sources_url"].(string); ok {
					sourcesURL = u
				}
				delete(pub, "sources_url")
			}

			if resultJSON, err := json.MarshalIndent(pub, "", "  "); err == nil {
				logStr := string(resultJSON)
				if len(logStr) > 2000 {
					logStr = logStr[:2000] + "...(truncated)"
				}
				log.Printf("[AiChat] Function response for %s:\n%s", fc.Name, logStr)
			}

			functionResponses = append(functionResponses, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     fc.Name,
					Response: pub,
				},
			})
		}

		contents = append(contents, &genai.Content{
			Role:  genai.RoleUser,
			Parts: functionResponses,
		})
		if len(extraParts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  genai.RoleUser,
				Parts: extraParts,
			})
		}
	}

	out := strings.TrimSpace(finalText.String())
	if out == "" {
		out = "AI returned no response."
	}
	return out, sourcesURL, nil
}

func summarizeContents(contents []*genai.Content) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n=%d", len(contents))
	for i, c := range contents {
		if c == nil {
			fmt.Fprintf(&b, " [%d:nil]", i)
			continue
		}
		fmt.Fprintf(&b, " [%d:%s", i, c.Role)
		for _, p := range c.Parts {
			if p == nil {
				b.WriteString(" nil")
				continue
			}
			switch {
			case p.FunctionCall != nil:
				fmt.Fprintf(&b, " fc:%s/sig%d", p.FunctionCall.Name, len(p.ThoughtSignature))
			case p.FunctionResponse != nil:
				fmt.Fprintf(&b, " fr:%s", p.FunctionResponse.Name)
			case p.FileData != nil:
				fmt.Fprintf(&b, " file:%s", p.FileData.MIMEType)
			case p.InlineData != nil:
				fmt.Fprintf(&b, " inline:%s", p.InlineData.MIMEType)
			case p.Thought:
				fmt.Fprintf(&b, " thought:%d", len(p.Text))
			case p.Text != "":
				fmt.Fprintf(&b, " text:%d", len(p.Text))
			default:
				b.WriteString(" empty")
			}
		}
		b.WriteByte(']')
	}
	s := b.String()
	if len(s) > 1500 {
		return s[:1500] + "…"
	}
	return s
}
