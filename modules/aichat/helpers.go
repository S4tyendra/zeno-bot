package aichat

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

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

func truncateTerminalOutput(output string) string {
	if len(output) <= 4000 {
		return output
	}
	firstPart := output[:2000]
	lastPart := output[len(output)-2000:]
	return firstPart + "\n\n... (truncated output) ...\n\n" + lastPart
}

func ensureGroupPrefix(chatID int64) string {
	idStr := fmt.Sprintf("%d", chatID)
	if chatID < 0 {
		if !strings.HasPrefix(idStr, "-100") {
			idStr = "-100" + strings.TrimPrefix(idStr, "-")
		}
	}
	return idStr
}

func formatXMLMessage(msgText string, senderName string, userID int64, chatID int64, chatName string, msgID int32, timestamp time.Time) string {
	if len(senderName) > 30 {
		senderName = senderName[:30]
	}
	if len(chatName) > 30 {
		chatName = chatName[:30]
	}
	loc := time.FixedZone("IST", 5*3600+1800)
	timeIST := timestamp.In(loc).Format("2006-01-02 15:04:05")

	formattedChatIDStr := ensureGroupPrefix(chatID)

	return fmt.Sprintf("<message user=%q userid=%q chatid=%q chatName=%q timeIST=%q messageid=%d>%s</message>",
		senderName, fmt.Sprintf("%d", userID), formattedChatIDStr, chatName, timeIST, msgID, msgText)
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
		if len(name) > 30 {
			return name[:30]
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
			if len(name) > 30 {
				return name[:30]
			}
			return name
		}
		return fmt.Sprintf("%d", msg.Sender.ID)
	}
	return "Unknown"
}

func fetchTelegramHistory(chatID int64, currentMsgID int32, excludeReplyID int32, limit int) []telegram.NewMessage {
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

	var messages []telegram.NewMessage
	for i := 0; i < len(ids); i += 200 {
		end := i + 200
		if end > len(ids) {
			end = len(ids)
		}
		chunk, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: ids[i:end]})
		if err != nil {
			log.Printf("[AiChat] GetMessages error in chunk: %v", err)
			continue
		}
		messages = append(messages, chunk...)
	}

	var result []telegram.NewMessage
	for _, msg := range messages {
		if msg.ID == currentMsgID || (excludeReplyID != 0 && msg.ID == excludeReplyID) {
			continue
		}

		text := msg.Text()
		if text == "" && msg.Media() == nil {
			continue
		}
		if strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "/askai") && !strings.HasPrefix(text, "/gpt") {
			continue
		}

		result = append(result, msg)
		if len(result) >= limit {
			break
		}
	}

	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

func getMessageWithMedia(chatID int64, msgID int32) (*ChatMessage, []*genai.Part) {
	if botClient == nil {
		return nil, nil
	}

	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil || len(msgs) == 0 {
		return nil, nil
	}

	msg := msgs[0]
	text := msg.Text()

	var mediaParts []*genai.Part
	if msg.Media() != nil {
		mediaData, mimeType, fileName := downloadMedia(&msg)
		if mediaData != nil {
			if strings.HasPrefix(mimeType, "image/") {
				mediaParts = append(mediaParts, &genai.Part{
					InlineData: &genai.Blob{
						Data:     mediaData,
						MIMEType: mimeType,
					},
				})
				text = fmt.Sprintf("[Image File: %s]\n%s", fileName, text)
			} else if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
				mediaParts = append(mediaParts, &genai.Part{
					InlineData: &genai.Blob{
						Data:     mediaData,
						MIMEType: "application/pdf",
					},
				})
				text = fmt.Sprintf("[PDF File: %s]\n%s", fileName, text)
			} else if isTextFile(fileName, mimeType) {
				text = fmt.Sprintf("\n--- File: %s ---\n%s\n---\n%s", fileName, string(mediaData), text)
			} else {
				text = fmt.Sprintf("[Unsupported File: %s]\n%s", fileName, text)
			}
		}
	}

	chatMsg := &ChatMessage{
		Sender: getSenderFromMessage(&msg),
		Text:   text,
	}

	return chatMsg, mediaParts
}

func downloadMedia(msg *telegram.NewMessage) ([]byte, string, string) {
	if msg.Message == nil || msg.Message.Media == nil {
		return nil, "", ""
	}

	var fileName string
	var mimeType string

	switch media := msg.Message.Media.(type) {
	case *telegram.MessageMediaPhoto:
		mimeType = "image/jpeg"
		fileName = "photo.jpg"
	case *telegram.MessageMediaDocument:
		mimeType = "application/octet-stream"
		if doc, ok := media.Document.(*telegram.DocumentObj); ok {
			if doc.MimeType != "" {
				mimeType = doc.MimeType
			}
			for _, attr := range doc.Attributes {
				if fn, ok := attr.(*telegram.DocumentAttributeFilename); ok && fn.FileName != "" {
					fileName = fn.FileName
					break
				}
			}
		}
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


