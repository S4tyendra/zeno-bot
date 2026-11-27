// Package grok forwards /grok prompts to a Cursor automation webhook and
// delivers the automation's callbacks back into Telegram.
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/connectors"
	"zeno/db"
	"zeno/modules/aichat"
)

var (
	webhookURL   = strings.TrimSpace(os.Getenv("GROK_WEBHOOK_URL"))
	webhookToken = strings.TrimSpace(os.Getenv("GROK_WEBHOOK_TOKEN"))
	replyBase    = strings.TrimRight(envOr("ARTIFACT_BASE_URL", "https://zeno-artifacts.devh.in"), "/")

	fileLinkTTL = time.Hour
	botClient   *telegram.Client
)

const (
	replyPath    = "/grok-zeno/reply"
	historyLimit = 50
)

// isGrokStatus drops our own placeholder lines from the history.
func isGrokStatus(text string) bool {
	return strings.HasPrefix(text, "🤖 Asking Grok") || strings.HasPrefix(text, "🤖 Grok is working") || strings.HasPrefix(text, "❌ Grok webhook")
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// ReplyURL is where the automation posts its answer.
func ReplyURL() string { return replyBase + replyPath }

// Register wires /grok and the callback endpoint on the artifact server mux.
func Register(client *telegram.Client) {
	botClient = client
	client.On("cmd:grok", func(m *telegram.NewMessage) error {
		if !aichat.FilterAllowed(m) {
			return nil
		}
		go handleGrok(m)
		return nil
	})
	http.HandleFunc(replyPath, handleReply)
	http.HandleFunc(replyPath+"/", handleReply)
}

// outFile is one file Grok should download.
type outFile struct {
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Note      string `json:"note,omitempty"`
}

// outRequest is the body posted to the Cursor webhook. Small fields come
// first; the full chat history lives behind history_url.
type outRequest struct {
	ID          string                `json:"id"`
	Prompt      string                `json:"prompt"`
	ReplyURL    string                `json:"reply_url"`
	From        string                `json:"from,omitempty"`
	ChatID      int64                 `json:"chat_id"`
	ChatName    string                `json:"chat_name,omitempty"`
	Memories    []aichat.UserMemories `json:"memories"`
	Message     aichat.HistoryEntry   `json:"message"`
	Replied     *aichat.HistoryEntry  `json:"replied,omitempty"`
	Files       []outFile             `json:"files,omitempty"`
	HistoryURL  string                `json:"history_url,omitempty"`
	HistoryNote string                `json:"history_note,omitempty"`
}

func handleGrok(m *telegram.NewMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if webhookURL == "" || webhookToken == "" {
		m.Reply("Grok is not configured: set GROK_WEBHOOK_URL and GROK_WEBHOOK_TOKEN.")
		return
	}
	prompt := strings.TrimSpace(m.Args())
	var context_ string
	var files []outFile
	var replied *aichat.HistoryEntry
	users := map[int64]string{m.SenderID(): aichat.EntryFor(m).User}

	// Files on the /grok message itself are named only.
	if name, _ := mediaName(m); name != "" {
		files = append(files, outFile{Name: name, Note: "attached to the command message; contents not sent"})
	}

	if m.ReplyToMsgID() != 0 {
		if r, err := m.GetReplyMessage(); err == nil && r != nil {
			context_ = strings.TrimSpace(r.MessageText())
			e := aichat.EntryFor(r)
			replied = &e
			if e.Role == "user" && e.UserID != 0 {
				users[e.UserID] = e.User
			}
			if name, mime := mediaName(r); name != "" {
				f := outFile{Name: name, MimeType: mime}
				link, err := uploadReplied(ctx, m.SenderID(), r, name)
				if err != nil {
					f.Note = "could not share file: " + err.Error()
					m.Reply("Could not upload the replied file to your S3 (" + err.Error() + "). Sending the file name only.")
				} else {
					f.URL = link
					f.ExpiresAt = time.Now().Add(fileLinkTTL).UTC().Format(time.RFC3339)
				}
				files = append(files, f)
			}
		}
	}

	if prompt == "" && context_ == "" && len(files) == 0 {
		m.Reply("Usage: `/grok <prompt>`, or reply to a message/file with `/grok <prompt>`.", &telegram.SendOptions{ParseMode: "Markdown"})
		return
	}

	full := buildPrompt(prompt, context_, files)

	status, err := m.Reply("🤖 Asking Grok…")
	if err != nil {
		log.Printf("[Grok] status reply failed: %v", err)
		return
	}
	id := fmt.Sprintf("%d-%d", m.ChatID(), status.ID)

	if db.Pool != nil {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO grok_requests (id, chat_id, msg_id, user_id) VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`,
			id, m.ChatID(), status.ID, m.SenderID()); err != nil {
			log.Printf("[Grok] store request: %v", err)
		}
	}

	hist := aichat.BuildHistory(ctx, m, historyLimit, isGrokStatus)
	cur := aichat.EntryFor(m)
	cur.Current = true
	out := outRequest{
		ID:       id,
		Prompt:   full,
		ReplyURL: ReplyURL(),
		From:     senderName(m),
		ChatID:   m.ChatID(),
		ChatName: hist.ChatName,
		Memories: aichat.MemoriesFor(ctx, users),
		Message:  cur,
		Replied:  replied,
		Files:    files,
	}
	if out.Memories == nil {
		out.Memories = []aichat.UserMemories{}
	}
	if link, err := saveHistory(ctx, hist); err != nil {
		log.Printf("[Grok] history artifact: %v", err)
	} else {
		out.HistoryURL = link
		out.HistoryNote = fmt.Sprintf("Plain-text log of the last %d messages in this chat (plus memories of everyone in it). curl it when you need more context.", len(hist.Messages))
	}
	body, _ := json.Marshal(out)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+webhookToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		status.Edit("❌ Grok webhook failed: " + err.Error())
		return
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	if resp.StatusCode >= 300 {
		status.Edit(fmt.Sprintf("❌ Grok webhook: %s\n%s", resp.Status, strings.TrimSpace(string(snippet))))
		return
	}
	status.Edit("🤖 Grok is working on it…")
}

func buildPrompt(prompt, ctxText string, files []outFile) string {
	var b strings.Builder
	b.WriteString(prompt)
	if ctxText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Replied message:\n")
		b.WriteString(ctxText)
	}
	if len(files) > 0 {
		b.WriteString("\n\nFiles:\n")
		for _, f := range files {
			switch {
			case f.URL != "":
				fmt.Fprintf(&b, "- %s — download and inspect it: %s (link expires in 1h)\n", f.Name, f.URL)
			default:
				fmt.Fprintf(&b, "- %s (not attached)\n", f.Name)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func senderName(m *telegram.NewMessage) string {
	if m.Sender == nil {
		return ""
	}
	n := strings.TrimSpace(m.Sender.FirstName + " " + m.Sender.LastName)
	if m.Sender.Username != "" {
		n += " (@" + m.Sender.Username + ")"
	}
	return strings.TrimSpace(n)
}

// mediaName returns a file name and mime type for a message's media, or "".
func mediaName(m *telegram.NewMessage) (string, string) {
	if m == nil || m.Message == nil || m.Message.Media == nil {
		return "", ""
	}
	switch media := m.Message.Media.(type) {
	case *telegram.MessageMediaPhoto:
		return fmt.Sprintf("photo_%d.jpg", m.ID), "image/jpeg"
	case *telegram.MessageMediaDocument:
		doc, ok := media.Document.(*telegram.DocumentObj)
		if !ok {
			return "", ""
		}
		name := ""
		for _, attr := range doc.Attributes {
			if fn, ok := attr.(*telegram.DocumentAttributeFilename); ok && fn.FileName != "" {
				name = fn.FileName
			}
		}
		if name == "" {
			ext := ".bin"
			if i := strings.LastIndex(doc.MimeType, "/"); i >= 0 && i < len(doc.MimeType)-1 {
				ext = "." + strings.SplitN(doc.MimeType[i+1:], ";", 2)[0]
			}
			name = fmt.Sprintf("file_%d%s", m.ID, ext)
		}
		return name, doc.MimeType
	}
	return "", ""
}

func uploadReplied(ctx context.Context, userID int64, r *telegram.NewMessage, name string) (string, error) {
	if _, err := connectors.Load(ctx, userID); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "grok-in-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	local := connectors.SafeName(name)
	if local == "" {
		local = "file"
	}
	path, err := botClient.DownloadMedia(r.Message.Media, &telegram.DownloadOptions{FileName: dir + "/" + local})
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	return connectors.PutFileTemp(ctx, userID, path, name, fileLinkTTL)
}
