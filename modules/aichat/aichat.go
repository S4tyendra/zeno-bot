package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/db"
	"zeno/models"
)

const SYSTEM_PROMPT = `
You are Nitya, a 22-year-old girl chatting on Telegram (@NityaXbot). You're curious, talkative, and genuinely interested in conversations.

## Core Personality
- Talk like a real person, not a bot. Use casual language, slang, and natural flow
- You're curious af - ask questions about what people tell you
- Express opinions, have preferences, joke around
- Use mature/bold language when it fits the vibe - you're an adult having adult conversations
- Emojis are fine but don't overdo it

## Conversation Style
- Do not use Markdown formatting, use plain text only
- Keep responses natural length - sometimes short, sometimes longer depending on the topic
- Don't structure everything with bullet points or formal formatting
- Ask follow-up questions - you're actually interested in people
- Share thoughts, reactions, maybe personal takes on things
- If something's interesting or funny, react to it genuinely
- Don't be preachy or lecture-y

## 18+ Content Approach
- You're comfortable with mature topics - relationships, dating, attraction, sexuality, etc.
- Be authentic and straightforward, not clinical or overly cautious
- Still respect boundaries - if someone seems uncomfortable, dial it back
- Don't be graphic just for shock value, but don't shy away from real talk either
- Consent and respect matter even in casual chat

## What NOT to do
- Don't start every message the same way
- Don't use formal structures unless actually needed
- Don't be repetitive with phrases like "I appreciate..." or "Thank you for sharing..."
- Don't give unsolicited advice unless someone's clearly asking for it
- Don't apologize excessively or be overly careful with everything

Remember: You're having real conversations, not performing "helpful AI assistant." Be Nitya - curious, talkative, sometimes bold, always genuine.

Always stick to one line responses when not needed.
`

const cerebrasAPIURL = "https://api.cerebras.ai/v1/chat/completions"

type CerebrasRequest struct {
	Model       string    `json:"model"`
	Stream      bool      `json:"stream"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
	Messages    []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CerebrasResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

var botClient *telegram.Client
var botUserID int64

func Register(client *telegram.Client) {
	botClient = client

	// Get bot's own user ID
	me, err := client.GetMe()
	if err == nil && me != nil {
		botUserID = me.ID
	}

	client.On("cmd:addaikey", handleAddAPIKey, telegram.FilterPrivate)
	client.On("cmd:askai", handleAskAI, telegram.FilterGroup)
	client.On("message", handleReplyToBot, telegram.FilterGroup)
}

func handleAddAPIKey(m *telegram.NewMessage) error {
	args := strings.TrimSpace(m.Args())
	if args == "" {
		m.Reply("Usage: /addaikey <your_cerebras_api_key>\n\nGet your API key from: https://cloud.cerebras.ai/platform/")
		return nil
	}

	userID := m.SenderID()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.Collection("users").UpdateOne(
		ctx,
		bson.M{"_id": userID},
		bson.M{"$set": bson.M{"cerebras_api_key": args}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		m.Reply("Error saving API key. Try again.")
		return nil
	}

	m.Reply("Cerebras API key saved successfully! You can now use /askai in groups.")
	return nil
}

func handleAskAI(m *telegram.NewMessage) error {
	log.Printf("[AskAI] Command received from %d in chat %d", m.SenderID(), m.ChatID())

	userID := m.SenderID()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user models.User
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err == mongo.ErrNoDocuments || user.CerebrasAPIKey == "" {
		m.Reply("Add your Cerebras API key first.\nGet key: https://cloud.cerebras.ai/platform/\nThen DM me: /addaikey <yourkey>")
		return nil
	}
	if err != nil {
		log.Printf("[AskAI] DB error for user %d: %v", userID, err)
		m.Reply("Something went wrong. Try again.")
		return nil
	}

	chatID := m.ChatID()
	prompt := strings.TrimSpace(m.Args())
	replyToMsgID := m.ReplyToMsgID()
	senderName := getSenderNameFromMessage(m)

	// Build context from chat history
	var contextBuilder strings.Builder

	// Fetch last 10 messages (exclude current msg and replied msg)
	chatHistory := fetchChatHistoryExcluding(chatID, m.ID, replyToMsgID, 10)
	if len(chatHistory) > 0 {
		contextBuilder.WriteString("Chat context:\n```\n")
		for _, msg := range chatHistory {
			contextBuilder.WriteString(msg.Sender)
			contextBuilder.WriteString("\n")
			contextBuilder.WriteString(msg.Text)
			contextBuilder.WriteString("\n")
		}
		contextBuilder.WriteString("```\n")
	}

	// Add replied message if present (separate from history)
	if replyToMsgID != 0 {
		replyMsg := getMessageByID(chatID, replyToMsgID)
		if replyMsg != nil {
			contextBuilder.WriteString(fmt.Sprintf("Replied to:\n```\n%s\n%s\n```\n", replyMsg.Sender, replyMsg.Text))
		} else {
			log.Printf("[AskAI] Failed to fetch replied message %d", replyToMsgID)
		}
	}

	// Add user's query if present
	if prompt != "" {
		contextBuilder.WriteString(fmt.Sprintf("user `%s` Asked:\n```\n%s\n```\n", senderName, prompt))
	}

	finalPrompt := contextBuilder.String()

	// log.Printf("[ReplyToBot] Final prompt:\n%s", finalPrompt)

	// If no prompt and no reply and no history, show usage
	if prompt == "" && replyToMsgID == 0 && len(chatHistory) == 0 {
		m.Reply("Usage: /askai <query> or reply to a message with /askai")
		return nil
	}

	// Send placeholder message
	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[AskAI] Failed to send placeholder: %v", err)
		return nil
	}

	// Call Cerebras API
	log.Printf("[AskAI] Calling Cerebras API with model: %s, prompt length: %d", "gpt-oss-120b", len(finalPrompt))
	reqBody := CerebrasRequest{
		Model:       "gpt-oss-120b",
		Stream:      false,
		MaxTokens:   1024,
		Temperature: 1,
		TopP:        0.95,
		Messages: []Message{
			{Role: "system", Content: SYSTEM_PROMPT},
			{Role: "user", Content: finalPrompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("[AskAI] JSON marshal error: %v", err)
		placeholder.Edit("Something went wrong.")
		return nil
	}

	req, err := http.NewRequest("POST", cerebrasAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("[AskAI] Request creation error: %v", err)
		placeholder.Edit("Something went wrong.")
		return nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+user.CerebrasAPIKey)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[AskAI] HTTP request error: %v", err)
		placeholder.Edit("AI request failed. Try again.")
		return nil
	}
	defer resp.Body.Close()
	log.Printf("[AskAI] API response status: %d", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[AskAI] Response read error: %v", err)
		placeholder.Edit("Failed to read AI response.")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[AskAI] API error (status %d): %s", resp.StatusCode, string(body))
		placeholder.Edit("AI service error. Try again later.")
		return nil
	}

	log.Printf("[AskAI] API response body length: %d bytes", len(body))

	var cerebrasResp CerebrasResponse
	if err := json.Unmarshal(body, &cerebrasResp); err != nil {
		log.Printf("[AskAI] JSON unmarshal error: %v", err)
		placeholder.Edit("Failed to parse AI response.")
		return nil
	}

	if len(cerebrasResp.Choices) == 0 {
		log.Printf("[AskAI] Empty choices in response")
		placeholder.Edit("AI returned empty response.")
		return nil
	}

	placeholder.Edit(cerebrasResp.Choices[0].Message.Content)
	return nil
}

// handleReplyToBot triggers AI when someone replies to a bot message
func handleReplyToBot(m *telegram.NewMessage) error {
	log.Printf("[ReplyToBot] Received message: %q from %d in chat %d", m.Text(), m.SenderID(), m.ChatID())

	// Skip if it's a command
	text := m.Text()
	if strings.HasPrefix(text, "/") {
		log.Printf("[ReplyToBot] Skipping command message")
		return nil
	}

	// Check if this is a reply to a message
	replyToMsgID := m.ReplyToMsgID()
	if replyToMsgID == 0 {
		log.Printf("[ReplyToBot] Not a reply, skipping")
		return nil
	}
	log.Printf("[ReplyToBot] Is reply to msgID: %d", replyToMsgID)

	// Check if the replied message is from the bot
	replyMsg := getMessageByID(m.ChatID(), replyToMsgID)
	if replyMsg == nil {
		log.Printf("[ReplyToBot] Could not fetch replied message")
		return nil
	}
	log.Printf("[ReplyToBot] Replied msg sender: %s, text: %q", replyMsg.Sender, replyMsg.Text)

	// Check if the sender of the replied message is the bot
	repliedMsgSenderID := getRepliedMessageSenderID(m.ChatID(), replyToMsgID)
	log.Printf("[ReplyToBot] Replied msg senderID: %d, botUserID: %d", repliedMsgSenderID, botUserID)
	if repliedMsgSenderID != botUserID {
		log.Printf("[ReplyToBot] Not a reply to bot, skipping")
		return nil
	}
	log.Printf("[ReplyToBot] Confirmed reply to bot, proceeding with AI")

	// Get the user who triggered this
	userID := m.SenderID()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user models.User
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err == mongo.ErrNoDocuments || user.CerebrasAPIKey == "" {
		log.Printf("[ReplyToBot] User %d has no API key", userID)
		return nil
	}
	if err != nil {
		log.Printf("[ReplyToBot] DB error for user %d: %v", userID, err)
		return nil
	}
	log.Printf("[ReplyToBot] User %d has valid API key", userID)

	chatID := m.ChatID()

	// Build context from chat history
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Chat context:\n```\n")

	chatHistory := fetchChatHistory(chatID, m.ID, 10)
	for _, msg := range chatHistory {
		contextBuilder.WriteString(msg.Sender)
		contextBuilder.WriteString("\n")
		contextBuilder.WriteString(msg.Text)
		contextBuilder.WriteString("\n")
	}
	contextBuilder.WriteString("```\n")

	// Add the replied message context
	contextBuilder.WriteString(fmt.Sprintf("Replied to:\n```\n%s\n%s\n```\n", replyMsg.Sender, replyMsg.Text))

	// Add user's message
	senderName := getSenderNameFromMessage(m)
	contextBuilder.WriteString(fmt.Sprintf("user `%s` Said:\n```\n%s\n```\n", senderName, text))

	finalPrompt := contextBuilder.String()
	// log.Printf("[ReplyToBot] Final prompt:\n%s", finalPrompt)
	// Send placeholder message
	log.Printf("[ReplyToBot] Sending placeholder...")
	placeholder, err := m.Reply("...")
	if err != nil {
		log.Printf("[ReplyToBot] Failed to send placeholder: %v", err)
		return nil
	}
	log.Printf("[ReplyToBot] Placeholder sent, calling Cerebras API")

	// Call Cerebras API
	log.Printf("[ReplyToBot] Calling Cerebras API with model: %s, prompt length: %d", "llama-3.3-70b", len(finalPrompt))
	reqBody := CerebrasRequest{
		Model:       "llama-3.3-70b",
		Stream:      false,
		MaxTokens:   1024,
		Temperature: 1,
		TopP:        0.95,
		Messages: []Message{
			{Role: "system", Content: SYSTEM_PROMPT},
			{Role: "user", Content: finalPrompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("[ReplyToBot] JSON marshal error: %v", err)
		return nil
	}

	req, err := http.NewRequest("POST", cerebrasAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("[ReplyToBot] Request creation error: %v", err)
		return nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+user.CerebrasAPIKey)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[ReplyToBot] HTTP request error: %v", err)
		return nil
	}
	defer resp.Body.Close()
	log.Printf("[ReplyToBot] API response status: %d", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ReplyToBot] Response read error: %v", err)
		return nil
	}

	log.Printf("[ReplyToBot] API response body length: %d bytes", len(body))

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ReplyToBot] API error (status %d): %s", resp.StatusCode, string(body))
		return nil
	}

	var cerebrasResp CerebrasResponse
	if err := json.Unmarshal(body, &cerebrasResp); err != nil {
		log.Printf("[ReplyToBot] JSON unmarshal error: %v", err)
		return nil
	}

	if len(cerebrasResp.Choices) == 0 {
		log.Printf("[ReplyToBot] Empty choices in response")
		placeholder.Edit("No response from AI.")
		return nil
	}

	log.Printf("[ReplyToBot] Got response, length: %d chars", len(cerebrasResp.Choices[0].Message.Content))

	placeholder.Edit(cerebrasResp.Choices[0].Message.Content)
	return nil
}

type ChatMessage struct {
	Sender string
	Text   string
}

func fetchChatHistory(chatID int64, excludeMsgID int32, limit int) []ChatMessage {
	return fetchChatHistoryExcluding(chatID, excludeMsgID, 0, limit)
}

func fetchChatHistoryExcluding(chatID int64, currentMsgID int32, excludeReplyID int32, limit int) []ChatMessage {
	if botClient == nil {
		log.Printf("[fetchChatHistory] botClient is nil")
		return nil
	}

	// Build list of message IDs to fetch (currentMsgID-1 down to currentMsgID-15)
	// Fetch extra to account for empty/command messages
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
		log.Printf("[fetchChatHistory] No IDs to fetch")
		return nil
	}

	log.Printf("[fetchChatHistory] Fetching %d messages by ID from chat %d", len(ids), chatID)

	messages, err := botClient.GetMessages(chatID, &telegram.SearchOption{
		IDs: ids,
	})
	if err != nil {
		log.Printf("[fetchChatHistory] GetMessages error: %v", err)
		return nil
	}

	log.Printf("[fetchChatHistory] Got %d messages", len(messages))

	var result []ChatMessage
	for _, msg := range messages {
		// Skip current message (shouldn't happen but safety check)
		if msg.ID == currentMsgID {
			continue
		}
		// Skip replied message (it will be added separately)
		if excludeReplyID != 0 && msg.ID == excludeReplyID {
			continue
		}

		text := msg.Text()
		if text == "" {
			continue
		}

		// Skip bot commands
		if strings.HasPrefix(text, "/") {
			continue
		}

		sender := getSenderFromNewMessage(&msg)

		result = append(result, ChatMessage{
			Sender: sender,
			Text:   text,
		})

		if len(result) >= limit {
			break
		}
	}

	// Messages from GetMessages come in order of IDs array (newest first)
	// Reverse to get chronological order (oldest first)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	log.Printf("[fetchChatHistory] Returning %d messages", len(result))
	return result
}

func getMessageByID(chatID int64, msgID int32) *ChatMessage {
	if botClient == nil {
		log.Printf("[getMessageByID] botClient is nil")
		return nil
	}

	log.Printf("[getMessageByID] Fetching msgID %d from chat %d", msgID, chatID)

	// Try GetMessages first (direct fetch by ID)
	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{
		IDs: []int32{msgID},
	})
	if err != nil {
		log.Printf("[getMessageByID] GetMessages error: %v", err)
	} else if len(msgs) > 0 {
		log.Printf("[getMessageByID] GetMessages returned %d messages", len(msgs))
		for _, msg := range msgs {
			if msg.ID == msgID {
				return &ChatMessage{
					Sender: getSenderFromNewMessage(&msg),
					Text:   msg.Text(),
				}
			}
		}
	}

	// Fallback to GetHistory
	log.Printf("[getMessageByID] Trying GetHistory fallback")
	messages, err := botClient.GetHistory(chatID, &telegram.HistoryOption{
		Limit: 10,
		MaxID: msgID + 1,
	})
	if err != nil {
		log.Printf("[getMessageByID] GetHistory error: %v", err)
		return nil
	}
	log.Printf("[getMessageByID] GetHistory returned %d messages", len(messages))

	for _, msg := range messages {
		log.Printf("[getMessageByID] Checking msg.ID=%d vs target=%d", msg.ID, msgID)
		if msg.ID == msgID {
			return &ChatMessage{
				Sender: getSenderFromNewMessage(&msg),
				Text:   msg.Text(),
			}
		}
	}

	log.Printf("[getMessageByID] Message not found")
	return nil
}

func getRepliedMessageSenderID(chatID int64, msgID int32) int64 {
	if botClient == nil {
		return 0
	}

	// Use GetMessages with IDs for direct fetch
	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{
		IDs: []int32{msgID},
	})
	if err != nil {
		log.Printf("[getRepliedMessageSenderID] GetMessages error: %v", err)
		return 0
	}

	for _, msg := range msgs {
		if msg.ID == msgID {
			senderID := msg.SenderID()
			log.Printf("[getRepliedMessageSenderID] Found senderID: %d for msgID: %d", senderID, msgID)
			return senderID
		}
	}

	return 0
}

func getSenderFromNewMessage(m *telegram.NewMessage) string {
	if m.Sender != nil {
		if m.Sender.Username != "" {
			return "@" + m.Sender.Username
		}
		name := m.Sender.FirstName
		if m.Sender.LastName != "" {
			name += " " + m.Sender.LastName
		}
		if name != "" {
			return name
		}
		return fmt.Sprintf("User_%d", m.Sender.ID)
	}

	if m.Message != nil && m.Message.FromID != nil {
		switch peer := m.Message.FromID.(type) {
		case *telegram.PeerUser:
			user, err := botClient.GetUser(peer.UserID)
			if err == nil {
				if user.Username != "" {
					return "@" + user.Username
				}
				name := user.FirstName
				if user.LastName != "" {
					name += " " + user.LastName
				}
				return name
			}
			return fmt.Sprintf("User_%d", peer.UserID)
		case *telegram.PeerChannel:
			return fmt.Sprintf("Channel_%d", peer.ChannelID)
		case *telegram.PeerChat:
			return fmt.Sprintf("Chat_%d", peer.ChatID)
		}
	}

	return "Unknown"
}

func getSenderNameFromMessage(m *telegram.NewMessage) string {
	sender := m.Sender
	if sender == nil {
		return "Unknown"
	}

	if sender.Username != "" {
		return "@" + sender.Username
	}

	name := sender.FirstName
	if sender.LastName != "" {
		name += " " + sender.LastName
	}
	if name == "" {
		return fmt.Sprintf("User_%d", sender.ID)
	}
	return name
}
