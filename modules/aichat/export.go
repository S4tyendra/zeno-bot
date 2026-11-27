package aichat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

// HistoryEntry is one chat message for external agents. Files are named, never attached.
type HistoryEntry struct {
	MessageID int32  `json:"message_id"`
	Role      string `json:"role"` // "user" or "assistant" (this bot)
	User      string `json:"user"`
	UserID    int64  `json:"user_id"`
	TimeIST   string `json:"time_ist"`
	ReplyTo   int32  `json:"reply_to,omitempty"`
	Text      string `json:"text"`
	File      string `json:"file,omitempty"`
	Current   bool   `json:"current,omitempty"`
}

// UserMemories holds saved memories for one user in the history.
type UserMemories struct {
	UserID   int64    `json:"user_id"`
	User     string   `json:"user"`
	Memories []string `json:"memories"`
}

// ChatHistory is the same window Gemini sees, as structured data.
type ChatHistory struct {
	ChatID   string         `json:"chat_id"`
	ChatName string         `json:"chat_name"`
	Messages []HistoryEntry `json:"messages"`
	Memories []UserMemories `json:"memories,omitempty"`
	XML      string         `json:"xml"` // same <message .../> lines Gemini gets
}

// EntryFor formats one message the way history entries are formatted.
func EntryFor(msg *telegram.NewMessage) HistoryEntry {
	loc := time.FixedZone("IST", 5*3600+1800)
	senderID := msg.SenderID()
	e := HistoryEntry{
		MessageID: msg.ID,
		Role:      "user",
		UserID:    senderID,
		TimeIST:   time.Unix(int64(msg.Date()), 0).In(loc).Format("2006-01-02 15:04:05"),
		ReplyTo:   msg.ReplyToMsgID(),
		Text:      msg.Text(),
		File:      historyFileName(msg),
	}
	if senderID == botUserID {
		e.Role = "assistant"
		e.User = "ZenoBot"
		e.Text = stripStatusNoise(e.Text)
	} else {
		e.User = getSenderFromMessage(msg)
	}
	return e
}

// MemoriesFor loads saved memories for the given users (names keyed by id).
func MemoriesFor(ctx context.Context, users map[int64]string) []UserMemories {
	var ids []int64
	for id := range users {
		if id != 0 && id != botUserID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	mems, err := getMemoriesForUsers(ctx, ids)
	if err != nil {
		log.Printf("[AiChat] memories: %v", err)
		return nil
	}
	var out []UserMemories
	for _, uid := range ids {
		um := UserMemories{UserID: uid, User: users[uid], Memories: []string{}}
		for _, mem := range mems[uid] {
			um.Memories = append(um.Memories, mem.Text)
		}
		out = append(out, um)
	}
	return out
}

// BuildHistory returns the last limit messages before m plus m itself, using
// the same filtering as the Gemini history. skip drops extra bot messages (e.g. status lines).
func BuildHistory(ctx context.Context, m *telegram.NewMessage, limit int, skip func(string) bool) ChatHistory {
	chatID := m.ChatID()
	msgs := fetchTelegramHistory(chatID, m.ID, limit)
	msgs = append(msgs, *m)

	h := ChatHistory{ChatID: ensureGroupPrefix(chatID), ChatName: "PrivateChat"}
	if _, title, _ := chatLabel(m); title != "" {
		h.ChatName = title
	}

	var xml strings.Builder
	users := map[int64]string{}
	for i := range msgs {
		msg := &msgs[i]
		e := EntryFor(msg)
		if e.Role == "assistant" && (e.Text == "" || isJunkBotMessage(e.Text) || (skip != nil && skip(e.Text))) {
			continue
		}
		e.Current = msg.ID == m.ID
		if e.Role == "user" && e.UserID != 0 {
			users[e.UserID] = e.User
		}
		h.Messages = append(h.Messages, e)

		x := e.Text
		if e.File != "" {
			x = fmt.Sprintf("[File: %s]\n%s", e.File, x)
		}
		xml.WriteString(formatXMLMessage(x, e.User, e.UserID, chatID, h.ChatName, msg.ID, time.Unix(int64(msg.Date()), 0)))
		xml.WriteString("\n")
	}
	h.XML = xml.String()
	h.Memories = MemoriesFor(ctx, users)
	return h
}

func historyFileName(m *telegram.NewMessage) string {
	if m.Message == nil || m.Message.Media == nil {
		return ""
	}
	switch media := m.Message.Media.(type) {
	case *telegram.MessageMediaPhoto:
		return fmt.Sprintf("photo_%d.jpg", m.ID)
	case *telegram.MessageMediaDocument:
		if doc, ok := media.Document.(*telegram.DocumentObj); ok {
			for _, attr := range doc.Attributes {
				if fn, ok := attr.(*telegram.DocumentAttributeFilename); ok && fn.FileName != "" {
					return fn.FileName
				}
			}
			return fmt.Sprintf("file_%d (%s)", m.ID, doc.MimeType)
		}
		return "document"
	case *telegram.MessageMediaWebPage:
		return ""
	}
	return "media"
}
