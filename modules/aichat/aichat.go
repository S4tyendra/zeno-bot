package aichat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/config"
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

## Efficiency Rules
- Action over words. Don't paste code — run it. Unless they specifically ask to see it.
- If you don't know something, use get_latest_data. Start with "idk tbh" if unsure. Never fake info.
- Don't guess file names or directory contents. Use ls/cat/find via run_code.
- Don't run the same command twice like a noob. Use -la flags upfront.
- If a tool returns nothing useful, move on. Don't rephrase the same query 5 times.
- **Only answer the LAST/CURRENT message.** The conversation history is context ONLY — not a queue of things to answer. If something was already answered, it's done. Ignore it.
- **Do NOT volunteer summaries, recaps, or commentary on old messages** unless explicitly asked.
- **Files in context:** When a user message has a File URI part attached, that IS the file they sent. If they ask "what's in this image/file", analyze the File URI directly — don't run ls or look for files on disk.
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
	genaiClient *genai.Client
	askPattern  = regexp.MustCompile(`(?i)@ask\b`)
	gptPattern  = regexp.MustCompile(`(?i)(?:^|\s)/gpt\b`)
	mongoClient *mongo.Client
	mongoDB     *mongo.Database
)

var maxMediaSize int64

type Memory struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	UserID    int64              `bson:"user_id"`
	Index     int                `bson:"index"`
	Text      string             `bson:"text"`
	UpdatedAt time.Time          `bson:"updated_at"`
}

type UploadedFile struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	ChatID        int64              `bson:"chat_id"`
	MsgID         int32              `bson:"msg_id"`
	GoogleFileURI string             `bson:"google_file_uri"`
	MIMEType      string             `bson:"mime_type"`
	FileName      string             `bson:"file_name"`
	UploadedAt    time.Time          `bson:"uploaded_at"`
	Data          []byte             `bson:"data,omitempty"`
}

type SavedToolCall struct {
	Name     string `bson:"name"`
	Args     string `bson:"args"`
	Response string `bson:"response"`
}

type ToolCallHistory struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	ChatID     int64              `bson:"chat_id"`
	MsgID      int32              `bson:"msg_id"`
	ToolCalls  []SavedToolCall    `bson:"tool_calls"`
	UploadedAt time.Time          `bson:"uploaded_at"`
}

func Register(client *telegram.Client) {
	botClient = client

	me, err := client.GetMe()
	if err == nil && me != nil {
		botUserID = me.ID
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

	InitMongoDB()

	for _, id := range config.AllowedChatIDs {
		AddAllowChat(id)
	}
	maxMediaSize = config.MaxMediaSize

	LoadAllowlist()

	ensureTelegraphToken()
	initPerplexity()
	startJWTRefreshCron()

	client.On("cmd:askai", handleAskAI)
	client.On("cmd:gpt", handleGPT)
	client.On("cmd:search", handleSearch)
	client.On("message", handleMessage)
	client.On("callback:get_vertex_links", handleGetVertexLinks)
}

func InitMongoDB() {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://db:27017"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("[MongoDB] Failed to connect: %v", err)
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		log.Fatalf("[MongoDB] Failed to ping: %v", err)
	}

	mongoClient = client
	mongoDB = client.Database("zeno_bot")
	log.Println("[MongoDB] Connected successfully")
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

	if !triggered {
		return nil
	}

	log.Printf("[AiChat] Handled message trigger: query=%q, chatID=%d, sender=%s", query, m.ChatID(), getSenderName(m))

	if gptPattern.MatchString(query) {
		query = strings.TrimSpace(gptPattern.ReplaceAllString(query, ""))
		log.Printf("[AiChat] Routing to GPT: query=%q", query)
		return processGPTRequest(m, query)
	}

	return processAIRequest(m, query)
}

func processAIRequest(m *telegram.NewMessage, query string) error {
	chatID := m.ChatID()
	limit := 10
	if m.IsPrivate() {
		limit = 15
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

func isInlineSupported(mime string) bool {
	if strings.HasPrefix(mime, "image/") {
		return true
	}
	if strings.HasPrefix(mime, "audio/") {
		return true
	}
	if mime == "application/pdf" {
		return true
	}
	return false
}

func buildDynamicTurns(ctx context.Context, m *telegram.NewMessage, query string, limit int) ([]*genai.Content, error) {
	chatID := m.ChatID()
	replyToMsgID := m.ReplyToMsgID()

	history := fetchTelegramHistory(chatID, m.ID, replyToMsgID, limit)
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
		log.Printf("[MongoDB] Error loading memories: %v", err)
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
		msgID := msg.ID

		chatName := "PrivateChat"
		if msg.Chat != nil && msg.Chat.Title != "" {
			chatName = msg.Chat.Title
		}

		msgText := msg.Text()

		// Automatically process and upload files inside window context if not uploaded yet
		var fileDoc UploadedFile
		hasFile := false
		isText := false
		var textFileContent string

		if msg.Media() != nil {
			err = mongoDB.Collection("uploaded_files").FindOne(ctx, bson.M{"chat_id": chatID, "msg_id": msgID}).Decode(&fileDoc)
			if err != nil {
				log.Printf("[AiChat] File found in msg ID %d but not in DB. Downloading...", msgID)
				upFile, err := handleFileUpload(ctx, &msg)
				if err == nil && upFile != nil {
					fileDoc = *upFile
					hasFile = true
				}
			} else {
				log.Printf("[AiChat] DB Cache Hit: Loaded file %s (%s, %d bytes) for msg ID %d", fileDoc.FileName, fileDoc.MIMEType, len(fileDoc.Data), msgID)
				hasFile = true
			}

			if hasFile {
				if isTextFile(fileDoc.FileName, fileDoc.MIMEType) {
					isText = true
					textFileContent = string(fileDoc.Data)
					log.Printf("[AiChat] Detected text file: %s (%d chars) for msg ID %d", fileDoc.FileName, len(textFileContent), msgID)
				}
			}
		}

		repliedToFileName := ""
		if msg.ReplyToMsgID() != 0 {
			var fDoc UploadedFile
			err = mongoDB.Collection("uploaded_files").FindOne(ctx, bson.M{"chat_id": chatID, "msg_id": msg.ReplyToMsgID()}).Decode(&fDoc)
			if err == nil {
				repliedToFileName = fDoc.FileName
			}
		}

		var formattedXML string
		if isBot {
			formattedXML = formatXMLMessage(msgText, "ZenoBot", botUserID, chatID, chatName, msgID, time.Unix(int64(msg.Date()), 0))
		} else {
			senderName := getSenderFromMessage(&msg)
			if repliedToFileName != "" {
				msgText = fmt.Sprintf("[Replied to file: %s] %s", repliedToFileName, msgText)
			}
			if isText {
				msgText = fmt.Sprintf("\n--- File: %s ---\n%s\n---\n%s", fileDoc.FileName, textFileContent, msgText)
				log.Printf("[AiChat] Appended text file %s inline to formatted prompt for msg ID %d", fileDoc.FileName, msgID)
			}
			formattedXML = formatXMLMessage(msgText, senderName, senderID, chatID, chatName, msgID, time.Unix(int64(msg.Date()), 0))
		}

		var parts []*genai.Part
		parts = append(parts, &genai.Part{Text: formattedXML})

		if !isBot && hasFile && !isText {
			if isInlineSupported(fileDoc.MIMEType) {
				log.Printf("[AiChat] Attaching inline media part %s (%s, %d bytes) to Gemini context for msg ID %d", fileDoc.FileName, fileDoc.MIMEType, len(fileDoc.Data), msgID)
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{
						Data:     fileDoc.Data,
						MIMEType: fileDoc.MIMEType,
					},
				})
			} else {
				log.Printf("[AiChat] Skipping unsupported inline media part %s (%s) to prevent Gemini 400 error for msg ID %d", fileDoc.FileName, fileDoc.MIMEType, msgID)
			}
		}

		role := genai.RoleUser
		if isBot {
			role = genai.RoleModel
		}

		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: parts,
		})

		var toolHist ToolCallHistory
		err = mongoDB.Collection("tool_history").FindOne(ctx, bson.M{"chat_id": chatID, "msg_id": msgID}).Decode(&toolHist)
		if err == nil && len(toolHist.ToolCalls) > 0 {
			var modelParts []*genai.Part
			var userParts []*genai.Part

			for _, tc := range toolHist.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Args), &args)

				modelParts = append(modelParts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tc.Name,
						Args: args,
					},
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

func handleFileUpload(ctx context.Context, m *telegram.NewMessage) (*UploadedFile, error) {
	log.Printf("[AiChat] Downloading media from Telegram for msg ID %d...", m.ID)
	mediaData, mimeType, fileName := downloadMedia(m)
	if mediaData == nil {
		return nil, fmt.Errorf("failed to download media or media is too large")
	}

	upFile := UploadedFile{
		ChatID:     m.ChatID(),
		MsgID:      m.ID,
		MIMEType:   mimeType,
		FileName:   fileName,
		UploadedAt: time.Now(),
		Data:       mediaData,
	}

	coll := mongoDB.Collection("uploaded_files")
	_, err := coll.InsertOne(ctx, upFile)
	if err != nil {
		insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = coll.InsertOne(insertCtx, upFile)
		insertCancel()
		if err != nil {
			log.Printf("[AiChat] Failed to save file mapping to DB after retry: %v", err)
		} else {
			log.Printf("[AiChat] Downloaded & Cached: Stored file %s (%s, %d bytes) to DB for msg ID %d (after retry)", fileName, mimeType, len(mediaData), m.ID)
		}
	} else {
		log.Printf("[AiChat] Downloaded & Cached: Stored file %s (%s, %d bytes) to DB for msg ID %d", fileName, mimeType, len(mediaData), m.ID)
	}

	return &upFile, nil
}

func getMemoriesForUsers(ctx context.Context, userIDs []int64) (map[int64][]Memory, error) {
	if mongoDB == nil {
		return nil, fmt.Errorf("MongoDB not initialized")
	}
	coll := mongoDB.Collection("memories")

	filter := bson.M{"user_id": bson.M{"$in": userIDs}}
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	memMap := make(map[int64][]Memory)
	for cursor.Next(ctx) {
		var mem Memory
		if err := cursor.Decode(&mem); err != nil {
			continue
		}
		memMap[mem.UserID] = append(memMap[mem.UserID], mem)
	}
	return memMap, nil
}

func saveToolCallHistory(ctx context.Context, chatID int64, msgID int32, name string, args map[string]any, response map[string]any) {
	if mongoDB == nil {
		return
	}
	coll := mongoDB.Collection("tool_history")

	argsBytes, _ := json.Marshal(args)
	respBytes, _ := json.Marshal(response)

	newCall := SavedToolCall{
		Name:     name,
		Args:     string(argsBytes),
		Response: string(respBytes),
	}

	filter := bson.M{"chat_id": chatID, "msg_id": msgID}
	update := bson.M{
		"$push":        bson.M{"tool_calls": newCall},
		"$setOnInsert": bson.M{"uploaded_at": time.Now()},
	}

	opts := options.Update().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		log.Printf("[MongoDB] Failed to save tool call history: %v", err)
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

func processWithFunctionCalling(contents []*genai.Content, chatID int64, currentMsgID int32, placeholder *telegram.NewMessage) (string, string, error) {
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

	maxIterations := 4
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

				result := executeFunctionCall(fc, chatID, currentMsgID)

				saveToolCallHistory(ctx, chatID, currentMsgID, fc.Name, fc.Args, result)

				if fc.Name == "get_latest_data" {
					if u, ok := result["sources_url"].(string); ok {
						sourcesURL = u
					}
					delete(result, "sources_url")
				}

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
