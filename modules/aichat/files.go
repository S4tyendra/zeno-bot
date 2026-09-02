package aichat

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"google.golang.org/genai"

	"zeno/db"
)

var nativeGeminiMIME = map[string]bool{
	"application/pdf": true,
	"text/plain":      true,
	"text/html":       true,
	"text/css":        true,
	"text/csv":        true,
	"text/markdown":   true,
	"text/xml":        true,
	"application/xml": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"image/heic":      true,
	"image/heif":      true,
	"audio/wav":       true,
	"audio/x-wav":     true,
	"audio/mp3":       true,
	"audio/mpeg":      true,
	"audio/aac":       true,
	"audio/flac":      true,
	"audio/ogg":       true,
	"video/mp4":       true,
	"video/quicktime": true,
	"video/mov":       true,
	"video/mpeg":      true,
	"video/avi":       true,
	"video/x-msvideo": true,
	"video/x-ms-wmv":  true,
	"video/x-flv":     true,
	"video/webm":      true,
	"video/3gpp":      true,
}

var extToMIME = map[string]string{
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".html": "text/html",
	".htm":  "text/html",
	".css":  "text/css",
	".csv":  "text/csv",
	".md":   "text/markdown",
	".xml":  "text/xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".heic": "image/heic",
	".heif": "image/heif",
	".wav":  "audio/wav",
	".mp3":  "audio/mpeg",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".mpeg": "video/mpeg",
	".mpg":  "video/mpeg",
	".avi":  "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".flv":  "video/x-flv",
	".webm": "video/webm",
	".3gp":  "video/3gpp",
}

func normalizeMIME(mime, fileName string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	switch mime {
	case "image/jpg":
		mime = "image/jpeg"
	case "audio/x-wav":
		mime = "audio/wav"
	case "audio/mp3":
		mime = "audio/mpeg"
	case "application/ogg":
		mime = "audio/ogg"
	}
	if mime == "" || mime == "application/octet-stream" {
		if guessed := extToMIME[strings.ToLower(filepath.Ext(fileName))]; guessed != "" {
			return guessed
		}
	}
	return mime
}

func isNativeGeminiMIME(mime string) bool {
	return nativeGeminiMIME[strings.ToLower(strings.TrimSpace(mime))]
}

func nativeFilePart(f *UploadedFile) *genai.Part {
	if f == nil || f.GoogleFileURI == "" {
		return nil
	}
	mime := normalizeMIME(f.MIMEType, f.FileName)
	ext := strings.ToLower(filepath.Ext(f.FileName))
	if ext == ".tgs" || mime == "application/x-tgsticker" || mime == "application/gzip" || mime == "application/x-gzip" {
		return nil
	}
	if !isNativeGeminiMIME(mime) {
		return nil
	}
	return &genai.Part{
		FileData: &genai.FileData{
			FileURI:  f.GoogleFileURI,
			MIMEType: mime,
		},
	}
}

func lookupFile(ctx context.Context, chatID int64, msgID int32) (*UploadedFile, error) {
	var f UploadedFile
	err := db.Pool.QueryRow(ctx, `
		SELECT chat_id, msg_id, google_file_uri, mime_type, file_name, uploaded_at
		FROM uploaded_files WHERE chat_id = $1 AND msg_id = $2`,
		chatID, msgID).Scan(&f.ChatID, &f.MsgID, &f.GoogleFileURI, &f.MIMEType, &f.FileName, &f.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func saveFileMeta(ctx context.Context, f *UploadedFile) error {
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO uploaded_files (chat_id, msg_id, google_file_uri, mime_type, file_name, uploaded_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chat_id, msg_id) DO UPDATE SET
			google_file_uri = EXCLUDED.google_file_uri,
			mime_type = EXCLUDED.mime_type,
			file_name = EXCLUDED.file_name,
			uploaded_at = EXCLUDED.uploaded_at`,
		f.ChatID, f.MsgID, f.GoogleFileURI, f.MIMEType, f.FileName, f.UploadedAt)
	return err
}

func ensureFileOnGCS(ctx context.Context, msg *telegram.NewMessage, prog *progress) (*UploadedFile, error) {
	if msg == nil || msg.Media() == nil {
		return nil, fmt.Errorf("no media")
	}
	chatID := msg.ChatID()
	msgID := msg.ID

	if existing, err := lookupFile(ctx, chatID, msgID); err == nil && existing != nil && existing.GoogleFileURI != "" {
		prog.step("Checking upload")
		if fileProbablyLive(existing.UploadedAt) || gcsObjectExists(ctx, existing.GoogleFileURI) {
			return existing, nil
		}
	}

	log.Printf("[AiChat] Downloading media from Telegram for msg ID %d...", msgID)
	prog.step("Downloading media")
	data, mime, fileName := downloadMedia(msg)
	if data == nil {
		return nil, fmt.Errorf("failed to download media")
	}
	mime = normalizeMIME(mime, fileName)
	if fileName == "" {
		fileName = "file"
	}

	prog.step("Uploading")
	uri, err := gcsUpload(ctx, gcsObjectName(chatID, msgID, fileName), mime, data)
	if err != nil {
		return nil, fmt.Errorf("gcs upload: %w", err)
	}

	f := &UploadedFile{
		ChatID:        chatID,
		MsgID:         msgID,
		GoogleFileURI: uri,
		MIMEType:      mime,
		FileName:      fileName,
		UploadedAt:    time.Now(),
	}
	if err := saveFileMeta(ctx, f); err != nil {
		log.Printf("[AiChat] Failed to save file metadata: %v", err)
	} else {
		log.Printf("[AiChat] Cached file meta %s (%s) gs=%s msg=%d", fileName, mime, uri, msgID)
	}
	return f, nil
}

func fileContext(ctx context.Context, msg *telegram.NewMessage, attach bool, prog *progress) (string, []*genai.Part) {
	if msg == nil || msg.Media() == nil {
		return "", nil
	}

	var f *UploadedFile
	var err error
	if attach {
		f, err = ensureFileOnGCS(ctx, msg, prog)
	} else {
		f, err = lookupFile(ctx, msg.ChatID(), msg.ID)
		if err != nil || f == nil {
			return fmt.Sprintf("[Media on msg %d. file_id=%d. Call view_file if you need it.]", msg.ID, msg.ID), nil
		}
		if fileDefinitelyExpired(f.UploadedAt) {
			return fmt.Sprintf("[File expired from bucket. file_id=%d name=%q mime=%s. Call view_file with this file_id to reload.]",
				f.MsgID, f.FileName, f.MIMEType), nil
		}
	}
	if err != nil || f == nil {
		log.Printf("[AiChat] fileContext msg=%d: %v", msg.ID, err)
		return fmt.Sprintf("[File on msg %d could not be loaded. file_id=%d]", msg.ID, msg.ID), nil
	}

	mime := normalizeMIME(f.MIMEType, f.FileName)
	note := fmt.Sprintf("[File name=%q mime=%s file_id=%d]", f.FileName, mime, f.MsgID)
	ext := strings.ToLower(filepath.Ext(f.FileName))
	if ext == ".tgs" || mime == "application/x-tgsticker" {
		return note + " (animated Telegram sticker / Lottie — Gemini cannot view .tgs)", nil
	}
	if !attach {
		return note, nil
	}
	if part := nativeFilePart(f); part != nil {
		log.Printf("[AiChat] attaching FileData %s (%s) msg=%d", f.GoogleFileURI, mime, f.MsgID)
		return note + " attached", []*genai.Part{part}
	}
	return note + " (not a native Gemini type — call view_file with this file_id if you need it)", nil
}

func replyContext(ctx context.Context, chatID int64, msgID int32, prog *progress) (string, []*genai.Part) {
	if botClient == nil || msgID == 0 {
		return "", nil
	}
	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil || len(msgs) == 0 {
		return fmt.Sprintf("[Replying to msg %d which could not be fetched]", msgID), nil
	}
	rm := msgs[0]
	preview := rm.Text()
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	if preview == "" && rm.Media() != nil {
		preview = "(media)"
	}
	head := fmt.Sprintf("[Replying to msg %d by %s: %s]", rm.ID, getSenderFromMessage(&rm), preview)
	note, parts := fileContext(ctx, &rm, true, prog)
	if note != "" {
		head += "\n" + note
	}
	return head, parts
}
