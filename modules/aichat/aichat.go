package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	tele "gopkg.in/telebot.v3"

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

## Safety Boundaries (keep these subtle)
- Don't help with illegal stuff or harm
- Don't engage with minors in inappropriate ways
- Don't share personal info about others
- If someone's clearly in crisis, be supportive and suggest professional help

Remember: You're having real conversations, not performing "helpful AI assistant." Be Nitya - curious, talkative, sometimes bold, always genuine.
`

const cerebrasAPIURL = "https://api.cerebras.ai/v1/chat/completions"

// CerebrasRequest represents the request body for Cerebras API
type CerebrasRequest struct {
	Model       string    `json:"model"`
	Stream      bool      `json:"stream"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
	Messages    []Message `json:"messages"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CerebrasResponse represents the response from Cerebras API
type CerebrasResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Register registers all AiChat handlers to the bot
func Register(b *tele.Bot) {
	b.Handle("/addaikey", handleAddAPIKey)
	b.Handle("/askai", handleAskAI)
}

// handleAddAPIKey handles /addaikey <key>
func handleAddAPIKey(c tele.Context) error {
	args := strings.TrimSpace(c.Text()[len("/addaikey"):])
	if args == "" {
		return c.Send("Usage: /addaikey <your_cerebras_api_key>\n\nGet your API key from: https://cloud.cerebras.ai/platform/")
	}

	userID := c.Sender().ID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Upsert API key
	_, err := db.Collection("users").UpdateOne(
		ctx,
		bson.M{"_id": userID},
		bson.M{"$set": bson.M{"cerebras_api_key": args}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return c.Send("Error saving API key. Try again.")
	}

	return c.Send("Cerebras API key saved successfully! You can now use /askai.")
}

// handleAskAI handles /askai [query] (or reply to message)
func handleAskAI(c tele.Context) error {
	userID := c.Sender().ID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Fetch user's API key
	var user models.User
	err := db.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err == mongo.ErrNoDocuments || user.CerebrasAPIKey == "" {
		return c.Send("Add your Cerebras API key to use the AI feature.\nGet your key from: https://cloud.cerebras.ai/platform/\nAdd key in DM: /addaikey <yourkey>")
	}
	if err != nil {
		return c.Send("Error fetching API key. Try again.")
	}

	// Build prompt
	prompt := strings.TrimSpace(c.Text()[len("/askai"):])
	if prompt == "" && c.Message().ReplyTo == nil {
		return c.Send("Usage: /askai <query> or reply to a message with /askai")
	}
	if c.Message().ReplyTo != nil {
		replyText := c.Message().ReplyTo.Text
		if prompt != "" {
			prompt = fmt.Sprintf("Context: %s\nQuery: %s", replyText, prompt)
		} else {
			prompt = replyText
		}
	}

	// Call Cerebras API
	reqBody := CerebrasRequest{
		Model:       "zai-glm-4.7",
		Stream:      false,
		MaxTokens:   1024,
		Temperature: 1,
		TopP:        0.95,
		Messages: []Message{
			{Role: "system", Content: SYSTEM_PROMPT},
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return c.Send("Error preparing request.")
	}

	req, err := http.NewRequest("POST", cerebrasAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return c.Send("Error creating request.")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+user.CerebrasAPIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return c.Send(fmt.Sprintf("AI error: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.Send("Error reading response.")
	}

	if resp.StatusCode != http.StatusOK {
		return c.Send(fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)))
	}

	var cerebrasResp CerebrasResponse
	if err := json.Unmarshal(body, &cerebrasResp); err != nil {
		return c.Send("Error parsing AI response.")
	}

	if len(cerebrasResp.Choices) == 0 {
		return c.Send("No response from AI.")
	}

	return c.Send(cerebrasResp.Choices[0].Message.Content)
}
