package afk

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/amarnathcjd/gogram/telegram"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/db"
	"zeno/models"
)

var (
	afkCache          sync.Map // Stores userID (int64) -> true
	afkUsernamesCache sync.Map // Stores usernameLower (string) -> userID (int64)
)

// Register registers the AFK module command and event handlers.
func Register(client *telegram.Client) {
	LoadAFKCache()
	client.On("cmd:afk", handleAFK)
	client.On("message", handleMessage)
}

// LoadAFKCache fetches all active AFK users from MongoDB and stores them in the in-memory cache.
func LoadAFKCache() {
	col := db.Collection("afk")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		log.Printf("[AFK] Failed to load AFK cache: %v", err)
		return
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var state models.AFKState
		if err := cursor.Decode(&state); err == nil {
			afkCache.Store(state.UserID, true)
			if state.Username != "" {
				afkUsernamesCache.Store(strings.ToLower(state.Username), state.UserID)
			}
			count++
		}
	}
	log.Printf("[AFK] Cache loaded with %d active AFK users", count)
}

func handleAFK(m *telegram.NewMessage) error {
	userID := m.SenderID()
	if userID == 0 {
		return nil
	}

	username := ""
	if m.Sender != nil {
		username = m.Sender.Username
	}

	reason := strings.TrimSpace(m.Args())
	col := db.Collection("afk")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state := models.AFKState{
		UserID:   userID,
		Username: username,
		AFKTime:  time.Now(),
		Reason:   reason,
	}

	opts := options.Replace().SetUpsert(true)
	_, err := col.ReplaceOne(ctx, bson.M{"_id": userID}, state, opts)
	if err != nil {
		log.Printf("[AFK] Failed to save AFK state for %d: %v", userID, err)
		m.Reply("❌ Failed to set AFK status.")
		return nil
	}

	// Update local cache
	afkCache.Store(userID, true)
	if username != "" {
		afkUsernamesCache.Store(strings.ToLower(username), userID)
	}

	senderName := getSenderFirstName(m.Sender, userID)
	var response string
	escapedName := html.EscapeString(senderName)
	if reason != "" {
		response = fmt.Sprintf("<b>%s is now AFK</b>\n\nReason: <i>%s</i>", escapedName, html.EscapeString(reason))
	} else {
		response = fmt.Sprintf("<b>%s is now AFK</b>", escapedName)
	}

	m.Reply(response, &telegram.SendOptions{ParseMode: "HTML"})
	return nil
}

func handleMessage(m *telegram.NewMessage) error {
	text := m.Text()
	// Skip command itself and hashtag checks to avoid immediately restoring AFK state
	if strings.HasPrefix(text, "/afk") || strings.Contains(text, "#afk") {
		return nil
	}

	userID := m.SenderID()
	if userID == 0 {
		return nil
	}

	col := db.Collection("afk")

	// Map to keep track of users we've notified in this message to prevent duplicate replies
	notified := make(map[int64]bool)

	// 1. Check if the sender is returning from AFK (using in-memory cache check first)
	if _, isAFK := afkCache.Load(userID); isAFK {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var senderAFK models.AFKState
		err := col.FindOne(ctx, bson.M{"_id": userID}).Decode(&senderAFK)
		if err == nil {
			_, deleteErr := col.DeleteOne(ctx, bson.M{"_id": userID})
			if deleteErr == nil {
				// Remove from in-memory cache
				afkCache.Delete(userID)
				if senderAFK.Username != "" {
					afkUsernamesCache.Delete(strings.ToLower(senderAFK.Username))
				}

				senderName := getSenderFirstName(m.Sender, userID)
				duration := formatDuration(time.Since(senderAFK.AFKTime))
				m.Reply(fmt.Sprintf("Welcome back %s, you are no longer AFK.\nAFK Time: <code>%s</code>", html.EscapeString(senderName), html.EscapeString(duration)), &telegram.SendOptions{ParseMode: "HTML"})
			}
		}
	}

	// 2. Check if this is a reply to an AFK user
	if m.ReplyToMsgID() != 0 {
		// Explicitly fetch replied-to message as m.ReplySenderID() does not return the correct sender in gogram
		repliedMsgs, err := m.Client.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{m.ReplyToMsgID()}})
		if err == nil && len(repliedMsgs) > 0 {
			repliedMsg := repliedMsgs[0]
			repliedSenderID := repliedMsg.SenderID()

			if repliedSenderID != 0 && repliedSenderID != userID {
				// Check in-memory cache first to avoid DB calls
				if _, isAFK := afkCache.Load(repliedSenderID); isAFK {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					var targetAFK models.AFKState
					err := col.FindOne(ctx, bson.M{"_id": repliedSenderID}).Decode(&targetAFK)
					if err == nil {
						notified[repliedSenderID] = true // Mark as notified so mention checking doesn't duplicate

						targetName := getSenderFirstName(repliedMsg.Sender, repliedSenderID)
						duration := formatDuration(time.Since(targetAFK.AFKTime))
						var response string
						escapedTargetName := html.EscapeString(targetName)
						escapedDuration := html.EscapeString(duration)
						if targetAFK.Reason != "" {
							response = fmt.Sprintf("<b>%s is AFK since</b> <code>%s</code>\n<b>Reason:</b> <i>%s</i>", escapedTargetName, escapedDuration, html.EscapeString(targetAFK.Reason))
						} else {
							response = fmt.Sprintf("<b>%s is AFK since</b> <code>%s</code>", escapedTargetName, escapedDuration)
						}
						m.Reply(response, &telegram.SendOptions{ParseMode: "HTML"})
					}
				}
			}
		}
	}

	// 3. Check if there are user mentions in the message
	if m.Message != nil && len(m.Message.Entities) > 0 {
		for _, entity := range m.Message.Entities {
			var targetID int64
			var targetName string

			if mentionName, ok := entity.(*telegram.MessageEntityMentionName); ok {
				targetID = mentionName.UserID
				targetName = "User"
			} else if mention, ok := entity.(*telegram.MessageEntityMention); ok {
				mentionText := getEntityTextUTF16(text, mention.Offset, mention.Length)
				username := strings.TrimPrefix(mentionText, "@")
				if username != "" {
					usernameLower := strings.ToLower(username)
					if idVal, ok := afkUsernamesCache.Load(usernameLower); ok {
						targetID = idVal.(int64)
					}
				}
			}

			// If the target is AFK, check the cache and respond
			if targetID != 0 && targetID != userID && !notified[targetID] {
				if _, isAFK := afkCache.Load(targetID); isAFK {
					notified[targetID] = true

					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					var targetAFK models.AFKState
					err := col.FindOne(ctx, bson.M{"_id": targetID}).Decode(&targetAFK)
					if err == nil {
						if targetName == "User" || targetName == "" {
							u, err := m.Client.GetUser(targetID)
							if err == nil && u != nil {
								targetName = getSenderFirstName(u, targetID)
							}
						}
						duration := formatDuration(time.Since(targetAFK.AFKTime))
						var response string
						escapedTargetName := html.EscapeString(targetName)
						escapedDuration := html.EscapeString(duration)
						if targetAFK.Reason != "" {
							response = fmt.Sprintf("<b>%s is AFK since</b> <code>%s</code>\n<b>Reason:</b> <i>%s</i>", escapedTargetName, escapedDuration, html.EscapeString(targetAFK.Reason))
						} else {
							response = fmt.Sprintf("<b>%s is AFK since</b> <code>%s</code>", escapedTargetName, escapedDuration)
						}
						m.Reply(response, &telegram.SendOptions{ParseMode: "HTML"})
					}
				}
			}
		}
	}

	return nil
}

func getSenderFirstName(user *telegram.UserObj, userID int64) string {
	if user == nil {
		return fmt.Sprintf("User_%d", userID)
	}
	if user.FirstName != "" {
		return user.FirstName
	}
	if user.Username != "" {
		return "@" + user.Username
	}
	return fmt.Sprintf("User_%d", userID)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, " ")
}

// getEntityTextUTF16 extracts substring using UTF-16 code units (as sent by Telegram)
func getEntityTextUTF16(text string, offset, length int32) string {
	u16 := utf16.Encode([]rune(text))
	if offset < 0 || offset >= int32(len(u16)) {
		return ""
	}
	end := offset + length
	if end > int32(len(u16)) {
		end = int32(len(u16))
	}
	return string(utf16.Decode(u16[offset:end]))
}
