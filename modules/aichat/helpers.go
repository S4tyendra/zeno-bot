package aichat

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"
)

type ChatMessage struct {
	Sender string
	Text   string
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
