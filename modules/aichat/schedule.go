package aichat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"github.com/google/uuid"

	"zeno/db"
)

// ist is India Standard Time. Bare timestamps on schedule_message are IST.
var ist = time.FixedZone("IST", 5*3600+30*60)

func startScheduler() {
	go func() {
		refreshKnownChats()
		due := time.NewTicker(5 * time.Second)
		chats := time.NewTicker(10 * time.Minute)
		defer due.Stop()
		defer chats.Stop()
		for {
			select {
			case <-due.C:
				dispatchDueScheduled()
			case <-chats.C:
				refreshKnownChats()
			}
		}
	}()
}

func parseScheduleTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, fmt.Errorf("time is required")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// No zone: the timestamp is IST.
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"02 Jan 2006 15:04:05",
		"02 Jan 2006 15:04",
		"02 Jan 2006, 15:04",
	} {
		if t, err := time.ParseInLocation(layout, s, ist); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q — use ISO-8601, bare values are IST (example 2026-11-19T21:30:00)", raw)
}

func formatIST(t time.Time) string {
	return t.In(ist).Format(time.RFC3339)
}

func argString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := args[k].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func argInt64(args map[string]any, keys ...string) (int64, bool) {
	for _, k := range keys {
		v, ok := args[k]
		if !ok || v == nil {
			continue
		}
		if n, ok := asInt64(v); ok {
			return n, true
		}
	}
	return 0, false
}

func hasArg(args map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := args[k]; ok && v != nil {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
				continue
			}
			return true
		}
	}
	return false
}

func executeScheduleMessage(args map[string]any, userID int64) map[string]any {
	if db.Pool == nil {
		return map[string]any{"success": false, "error": "database not ready"}
	}
	action := strings.ToLower(argString(args, "action"))
	switch action {
	case "list":
		return listScheduled(args)
	case "create":
		return createScheduled(args, userID)
	case "edit":
		return editScheduled(args)
	case "delete":
		return deleteScheduled(args)
	default:
		return map[string]any{"success": false, "error": "action must be list, create, edit, or delete"}
	}
}

func listScheduled(args map[string]any) map[string]any {
	var chatID *int64
	if id, ok := argInt64(args, "chatid", "chat_id"); ok {
		chatID = &id
	}

	rows, err := db.Pool.Query(context.Background(), `
		SELECT s.id, s.chat_id, COALESCE(k.title, ''), s.message, s.send_at, s.status, s.error, s.mode
		FROM scheduled_messages s
		LEFT JOIN known_chats k ON k.chat_id = s.chat_id
		WHERE ($1::bigint IS NULL OR s.chat_id = $1)
		  AND s.status IN ('pending', 'failed')
		ORDER BY s.send_at ASC
		LIMIT 100`, chatID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, title, message, status, errText, mode string
		var cid int64
		var sendAt time.Time
		if err := rows.Scan(&id, &cid, &title, &message, &sendAt, &status, &errText, &mode); err != nil {
			continue
		}
		item := map[string]any{
			"scheduledid": id,
			"chatid":      cid,
			"chat_name":   title,
			"message":     message,
			"time_ist":    formatIST(sendAt),
			"status":      status,
			"mode":        mode,
		}
		if errText != "" {
			item["error"] = errText
		}
		items = append(items, item)
	}
	return map[string]any{"success": true, "count": len(items), "messages": items}
}

func createScheduled(args map[string]any, userID int64) map[string]any {
	chatID, ok := argInt64(args, "chatid", "chat_id")
	if !ok || chatID == 0 {
		return map[string]any{"success": false, "error": "chatid is required"}
	}
	message := argString(args, "message")
	if message == "" {
		return map[string]any{"success": false, "error": "message is required"}
	}
	if len(message) > 4000 {
		return map[string]any{"success": false, "error": "message is longer than 4000 characters"}
	}
	when, err := parseScheduleTime(argString(args, "time"))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if when.Before(time.Now().Add(-30 * time.Second)) {
		return map[string]any{"success": false, "error": "time is in the past (IST). pass a future ISO timestamp"}
	}
	mode := strings.ToLower(argString(args, "mode"))
	if mode == "" {
		mode = "send"
	}
	if mode != "send" && mode != "ai" {
		return map[string]any{"success": false, "error": "mode must be send or ai"}
	}

	id := uuid.New().String()[:8]
	_, err = db.Pool.Exec(context.Background(), `
		INSERT INTO scheduled_messages (id, chat_id, user_id, mode, message, send_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')`,
		id, chatID, userID, mode, message, when.UTC())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}

	title := lookupChatTitle(chatID)
	log.Printf("[Schedule] created id=%s chat=%d at=%s", id, chatID, formatIST(when))
	return map[string]any{
		"success":     true,
		"scheduledid": id,
		"chatid":      chatID,
		"chat_name":   title,
		"time_ist":    formatIST(when),
		"message":     message,
		"mode":        mode,
		"status":      "pending",
	}
}

func editScheduled(args map[string]any) map[string]any {
	id := argString(args, "scheduledid", "scheduled_id")
	if id == "" {
		return map[string]any{"success": false, "error": "scheduledid is required"}
	}

	var chatID int64
	var message, status, mode string
	var sendAt time.Time
	err := db.Pool.QueryRow(context.Background(), `
		SELECT chat_id, message, send_at, status, mode
		FROM scheduled_messages WHERE id = $1`, id).Scan(&chatID, &message, &sendAt, &status, &mode)
	if err != nil {
		return map[string]any{"success": false, "error": "scheduled message not found"}
	}
	if status != "pending" && status != "failed" {
		return map[string]any{"success": false, "error": "only pending or failed schedules can be edited"}
	}

	if hasArg(args, "new_chatid", "new_chat_id") {
		n, ok := argInt64(args, "new_chatid", "new_chat_id")
		if !ok || n == 0 {
			return map[string]any{"success": false, "error": "new_chatid is invalid"}
		}
		chatID = n
	}
	if hasArg(args, "new_message") {
		message = argString(args, "new_message")
		if message == "" {
			return map[string]any{"success": false, "error": "new_message is empty"}
		}
		if len(message) > 4000 {
			return map[string]any{"success": false, "error": "message is longer than 4000 characters"}
		}
	}
	if hasArg(args, "new_time") {
		sendAt, err = parseScheduleTime(argString(args, "new_time"))
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		if sendAt.Before(time.Now().Add(-30 * time.Second)) {
			return map[string]any{"success": false, "error": "new_time is in the past (IST)"}
		}
	}
	if hasArg(args, "new_mode") {
		mode = strings.ToLower(argString(args, "new_mode"))
		if mode != "send" && mode != "ai" {
			return map[string]any{"success": false, "error": "new_mode must be send or ai"}
		}
	}

	tag, err := db.Pool.Exec(context.Background(), `
		UPDATE scheduled_messages
		SET chat_id = $2, message = $3, send_at = $4, mode = $5, status = 'pending', error = '', updated_at = NOW()
		WHERE id = $1 AND status IN ('pending', 'failed')`,
		id, chatID, message, sendAt.UTC(), mode)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if tag.RowsAffected() == 0 {
		return map[string]any{"success": false, "error": "scheduled message was already sent"}
	}
	return map[string]any{
		"success":     true,
		"scheduledid": id,
		"chatid":      chatID,
		"chat_name":   lookupChatTitle(chatID),
		"time_ist":    formatIST(sendAt),
		"message":     message,
		"mode":        mode,
		"status":      "pending",
	}
}

func deleteScheduled(args map[string]any) map[string]any {
	id := argString(args, "scheduledid", "scheduled_id")
	if id == "" {
		return map[string]any{"success": false, "error": "scheduledid is required"}
	}
	tag, err := db.Pool.Exec(context.Background(), `
		DELETE FROM scheduled_messages
		WHERE id = $1 AND status IN ('pending', 'failed')`, id)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if tag.RowsAffected() == 0 {
		return map[string]any{"success": false, "error": "no pending schedule with that id"}
	}
	return map[string]any{"success": true, "deleted": id}
}

func dispatchDueScheduled() {
	if db.Pool == nil || botClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A crash mid-send leaves status=sending. Put those back after a short wait.
	_, _ = db.Pool.Exec(ctx, `
		UPDATE scheduled_messages
		SET status = 'pending', updated_at = NOW()
		WHERE status = 'sending' AND updated_at < NOW() - INTERVAL '20 minutes'`)

	rows, err := db.Pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM scheduled_messages
			WHERE status = 'pending' AND send_at <= NOW()
			ORDER BY send_at ASC
			LIMIT 20
			FOR UPDATE SKIP LOCKED
		)
		UPDATE scheduled_messages s
		SET status = 'sending', updated_at = NOW()
		FROM due
		WHERE s.id = due.id
		RETURNING s.id, s.chat_id, s.user_id, s.mode, s.message`)
	if err != nil {
		log.Printf("[Schedule] due query failed: %v", err)
		return
	}
	defer rows.Close()

	type due struct {
		id      string
		chatID  int64
		userID  int64
		mode    string
		message string
	}
	var batch []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.chatID, &d.userID, &d.mode, &d.message); err == nil {
			batch = append(batch, d)
		}
	}
	for _, d := range batch {
		if d.mode == "ai" {
			err := RunScheduledPrompt(d.chatID, d.userID, d.message)
			if err != nil {
				log.Printf("[Schedule] ai failed id=%s chat=%d: %v", d.id, d.chatID, err)
				_, _ = db.Pool.Exec(context.Background(), `
					UPDATE scheduled_messages
					SET status = 'failed', error = $2, updated_at = NOW()
					WHERE id = $1 AND status = 'sending'`, d.id, truncateString(err.Error(), 400))
				continue
			}
			_, _ = db.Pool.Exec(context.Background(), `
				UPDATE scheduled_messages
				SET status = 'sent', sent_at = NOW(), error = '', updated_at = NOW()
				WHERE id = $1 AND status = 'sending'`, d.id)
			log.Printf("[Schedule] ai ran id=%s chat=%d", d.id, d.chatID)
			continue
		}
		msg, err := botClient.SendMessage(d.chatID, d.message, &telegram.SendOptions{ParseMode: "Markdown"})
		if err != nil {
			msg, err = botClient.SendMessage(d.chatID, d.message, nil)
		}
		if err != nil {
			log.Printf("[Schedule] send failed id=%s chat=%d: %v", d.id, d.chatID, err)
			_, _ = db.Pool.Exec(context.Background(), `
				UPDATE scheduled_messages
				SET status = 'failed', error = $2, updated_at = NOW()
				WHERE id = $1 AND status = 'sending'`, d.id, truncateString(err.Error(), 400))
			continue
		}
		_, _ = db.Pool.Exec(context.Background(), `
			UPDATE scheduled_messages
			SET status = 'sent', sent_at = NOW(), error = '', updated_at = NOW()
			WHERE id = $1 AND status = 'sending'`, d.id)
		log.Printf("[Schedule] sent id=%s chat=%d msg=%d", d.id, d.chatID, msg.ID)
	}
}

func lookupChatTitle(chatID int64) string {
	if db.Pool == nil {
		return ""
	}
	var title string
	_ = db.Pool.QueryRow(context.Background(), `SELECT title FROM known_chats WHERE chat_id = $1`, chatID).Scan(&title)
	return title
}

func rememberChat(chatID int64, title, kind string) {
	title = strings.TrimSpace(title)
	if db.Pool == nil || chatID == 0 || title == "" || title == "PrivateChat" {
		return
	}
	if len(title) > 80 {
		title = title[:80]
	}
	if kind == "" {
		kind = "group"
	}
	_, err := db.Pool.Exec(context.Background(), `
		INSERT INTO known_chats (chat_id, title, kind, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (chat_id) DO UPDATE
		SET title = EXCLUDED.title, kind = EXCLUDED.kind, updated_at = NOW()`,
		chatID, title, kind)
	if err != nil {
		log.Printf("[Schedule] remember chat %d failed: %v", chatID, err)
	}
}

func chatLabel(m *telegram.NewMessage) (int64, string, string) {
	if m == nil {
		return 0, "", ""
	}
	id := m.ChatID()
	if m.Channel != nil && strings.TrimSpace(m.Channel.Title) != "" {
		kind := "group"
		if m.Channel.Broadcast && !m.Channel.Megagroup {
			kind = "channel"
		}
		return id, m.Channel.Title, kind
	}
	if m.Chat != nil && strings.TrimSpace(m.Chat.Title) != "" {
		kind := "group"
		if m.Sender != nil && m.Chat.ID == m.Sender.ID {
			kind = "user"
		}
		return id, m.Chat.Title, kind
	}
	return id, "", ""
}

func rememberMessageChat(m *telegram.NewMessage) {
	id, title, kind := chatLabel(m)
	rememberChat(id, title, kind)
}

func refreshKnownChats() {
	if botClient == nil || db.Pool == nil {
		return
	}
	dialogs, err := botClient.GetDialogs(&telegram.DialogOptions{Limit: 100})
	if err != nil {
		log.Printf("[Schedule] dialog refresh failed: %v", err)
		return
	}
	n := 0
	for _, d := range dialogs {
		title, kind := dialogTitle(d)
		if title == "" {
			continue
		}
		rememberChat(d.GetID(), title, kind)
		n++
	}
	log.Printf("[Schedule] remembered %d chats", n)
}

func dialogTitle(d telegram.TLDialog) (string, string) {
	id := d.GetID()
	switch {
	case d.IsUser():
		u, err := botClient.GetUser(id)
		if err != nil || u == nil {
			return "", ""
		}
		name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
		if u.Username != "" {
			if name != "" {
				name += " "
			}
			name += "@" + u.Username
		}
		return strings.TrimSpace(name), "user"
	case d.IsChat():
		c, err := botClient.GetChat(id)
		if err != nil || c == nil {
			return "", ""
		}
		return c.Title, "group"
	case d.IsChannel():
		ch, err := botClient.GetChannel(id)
		if err != nil || ch == nil {
			return "", ""
		}
		kind := "group"
		if ch.Broadcast && !ch.Megagroup {
			kind = "channel"
		}
		return ch.Title, kind
	default:
		return "", ""
	}
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}

// knownChatsBlock lists recent chat names and ids for the system prompt so a
// name like "devchat" can be matched to a chat id without asking.
func knownChatsBlock() string {
	if db.Pool == nil {
		return ""
	}
	rows, err := db.Pool.Query(context.Background(), `
		SELECT chat_id, title, kind
		FROM known_chats
		ORDER BY CASE WHEN kind = 'user' THEN 1 ELSE 0 END, updated_at DESC
		LIMIT 120`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	n := 0
	for rows.Next() {
		var id int64
		var title, kind string
		if err := rows.Scan(&id, &title, &kind); err != nil {
			continue
		}
		if n == 0 {
			b.WriteString("\n\n<known_chats>\n")
			b.WriteString("Recent chats you can message or read. When the user names a chat (devchat, \"the memes group\"), pick the matching id from this list yourself. Match loosely on the name. Groups are listed before users, most recently active first. If two names are close, use the earlier one. Do not ask for the id when a name matches.\n")
		}
		fmt.Fprintf(&b, "<peer id=\"%d\" kind=%q>%s</peer>\n", id, kind, xmlEscape(title))
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString("</known_chats>")
	return b.String()
}
