package downloader

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
	"zeno/db"
	"zeno/modules/aichat"
)

const (
	adminUserID int64 = 1089528685
)

func isAdminUser(userID int64) bool {
	return userID == adminUserID
}

func Register(client *telegram.Client) {
	StartQueueWorker(client)

	client.On("cmd:download", handleDownload)
	client.On("cmd:dl", handleDownload)

	client.On("cmd:fastdownload", handleFastDownload)
	client.On("cmd:fastdl", handleFastDownload)
	client.On("cmd:fdl", handleFastDownload)

	client.On("cmd:ytdlp", handleYtDlp)
	client.On("cmd:ytdl", handleYtDlp)
	client.On("cmd:yt-dlp", handleYtDlp)

	client.On("cmd:rename", handleRename)
	client.On("cmd:upload", handleUpload)
	client.On("cmd:up", handleUpload)

	client.On("cmd:tasks", handleTasks)
	client.On("cmd:dlqueue", handleTasks)
	client.On("cmd:queue", handleTasks)

	client.On("cmd:cancel", handleCancel)

	client.On("callback:up", handleUploadCallback)
	log.Println("[Downloader] Module registered successfully")
}

func isURL(s string) bool {
	u := strings.ToLower(s)
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func handleDownload(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	replyToID := m.ReplyToMsgID()

	// 1. Reply to a Telegram message
	if replyToID != 0 {
		// If user explicitly passed a URL while replying, download that URL
		if len(fields) > 0 && isURL(fields[0]) {
			rawURL := fields[0]
			customName := strings.TrimSpace(strings.TrimPrefix(rawArgs, rawURL))
			if isM3U8URL(rawURL) || isMediaSiteURL(rawURL) {
				return enqueueDownload(m, TaskTypeYTDLP, rawURL, customName, replyToID)
			}
			return enqueueDownload(m, TaskTypeURL, rawURL, customName, replyToID)
		}

		// Otherwise, download the replied Telegram media directly
		customName := rawArgs
		return enqueueDownload(m, TaskTypeTG, "", customName, replyToID)
	}

	// 2. Direct command without reply (requires URL)
	if len(fields) == 0 {
		m.Reply("ℹ️ **Usage:**\n• Reply to a Telegram media with `/download [custom_name]`\n• `/download <url> [custom_name]`\n• `/fastdownload <url> [custom_name]` (16 parallel connections)\n• `/ytdlp <url> [custom_name]` (video/audio/m3u8)", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	rawURL := fields[0]
	if !isURL(rawURL) {
		m.Reply("❌ First argument must be an `http://` or `https://` URL.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	customName := strings.TrimSpace(strings.TrimPrefix(rawArgs, rawURL))

	// Auto-route m3u8 or media platforms to yt-dlp
	if isM3U8URL(rawURL) || isMediaSiteURL(rawURL) {
		return enqueueDownload(m, TaskTypeYTDLP, rawURL, customName, 0)
	}

	return enqueueDownload(m, TaskTypeURL, rawURL, customName, 0)
}

func handleFastDownload(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	if len(fields) == 0 {
		m.Reply("ℹ️ **Usage:** `/fastdownload <url> [custom_name]`\nAccelerates download with aria2c (16 parallel connections).", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	rawURL := fields[0]
	if !isURL(rawURL) {
		m.Reply("❌ First argument must be an `http://` or `https://` URL.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	customName := strings.TrimSpace(strings.TrimPrefix(rawArgs, rawURL))

	if isM3U8URL(rawURL) || isMediaSiteURL(rawURL) {
		return enqueueDownload(m, TaskTypeYTDLP, rawURL, customName, m.ReplyToMsgID())
	}

	return enqueueDownload(m, TaskTypeFast, rawURL, customName, m.ReplyToMsgID())
}

func handleYtDlp(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	if len(fields) == 0 {
		m.Reply("ℹ️ **Usage:** `/ytdlp <url> [custom_name]`\nDownloads media from YouTube, Instagram, Twitter, TikTok, or raw `.m3u8` streams.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	rawURL := fields[0]
	if !isURL(rawURL) {
		m.Reply("❌ First argument must be an `http://` or `https://` URL.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	customName := strings.TrimSpace(strings.TrimPrefix(rawArgs, rawURL))

	return enqueueDownload(m, TaskTypeYTDLP, rawURL, customName, m.ReplyToMsgID())
}

func handleRename(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	replyToID := m.ReplyToMsgID()

	if len(fields) == 0 && replyToID == 0 {
		m.Reply("ℹ️ **Usage:**\n• `/rename <task_id> <new_filename>`\n• Reply to a completed task with `/rename <new_filename>`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var targetTask *DownloadTask
	var newName string

	if replyToID != 0 {
		targetTask = findTaskByReply(ctx, m.ChatID(), replyToID)
		newName = rawArgs
	} else if len(fields) >= 2 {
		taskID, err := strconv.ParseInt(fields[0], 10, 64)
		if err == nil {
			targetTask, _ = GetTask(ctx, taskID)
			newName = strings.TrimSpace(strings.TrimPrefix(rawArgs, fields[0]))
		}
	}

	if targetTask == nil {
		m.Reply("❌ Could not find the specified download task. Use `/rename <task_id> <new_filename>`.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if targetTask.Status != StatusCompleted {
		m.Reply(fmt.Sprintf("❌ Task #%d is not completed yet (Status: `%s`).", targetTask.ID, targetTask.Status), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if newName == "" {
		m.Reply("❌ New filename cannot be empty.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	oldPath := targetTask.FilePath
	if !fileExists(oldPath) || !isUnderDownloads(oldPath) {
		m.Reply("❌ Original file not found on disk.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	newName = sanitizeFileName(newName)
	if newName == "" {
		m.Reply("❌ New filename cannot be empty.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	newName = preserveExt(newName, oldPath)

	dir := filepath.Dir(oldPath)
	newPath := filepath.Join(dir, newName)
	if !isUnderDownloads(newPath) {
		m.Reply("❌ Invalid filename.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		m.Reply(fmt.Sprintf("❌ Rename failed: `%v`", err), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	_ = UpdateTaskFilePath(ctx, targetTask.ID, newPath, newName)
	targetTask.FilePath = newPath
	targetTask.CustomName = newName

	markup := telegram.InlineDataGrid(2,
		"📹 Video", fmt.Sprintf("up:video:%d", targetTask.ID),
		"📁 Document", fmt.Sprintf("up:doc:%d", targetTask.ID),
		"🎵 Audio", fmt.Sprintf("up:audio:%d", targetTask.ID),
		"🖼️ Photo", fmt.Sprintf("up:photo:%d", targetTask.ID),
		"⚡ Auto Upload", fmt.Sprintf("up:auto:%d", targetTask.ID),
	)

	m.Reply(fmt.Sprintf("✅ <b>Renamed Task #%d!</b>\n\n📄 <b>New File:</b> <code>%s</code>\n📦 <b>Size:</b> <code>%s</code>\n\nReady to upload:",
		targetTask.ID, html.EscapeString(newName), FormatBytes(targetTask.FileSize)), &telegram.SendOptions{
		ParseMode:   "HTML",
		ReplyMarkup: markup,
	})
	return nil
}

func handleUpload(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var targetTask *DownloadTask
	mode := UploadAuto

	if len(fields) >= 1 {
		taskID, err := strconv.ParseInt(fields[0], 10, 64)
		if err == nil {
			targetTask, _ = GetTask(ctx, taskID)
			if len(fields) >= 2 {
				mode = parseUploadMode(fields[1])
			}
		}
	}

	if targetTask == nil && m.ReplyToMsgID() != 0 {
		targetTask = findTaskByReply(ctx, m.ChatID(), m.ReplyToMsgID())
		if len(fields) >= 1 {
			mode = parseUploadMode(fields[0])
		}
	}

	if targetTask == nil {
		m.Reply("ℹ️ **Usage:**\n• `/upload <task_id> [auto|video|doc|file|audio|photo]`\n• Reply to a downloaded file message with `/upload [mode]`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if targetTask.Status != StatusCompleted {
		m.Reply(fmt.Sprintf("❌ Task #%d is not completed yet (Status: `%s`).", targetTask.ID, targetTask.Status), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if !fileExists(targetTask.FilePath) || !isUnderDownloads(targetTask.FilePath) {
		m.Reply("❌ File does not exist on disk.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	return enqueueUpload(m, targetTask, mode)
}

func handleTasks(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tasks, err := GetActiveAndRecentTasks(ctx)
	if err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to fetch tasks: `%v`", err))
		return nil
	}

	if len(tasks) == 0 {
		m.Reply("📋 **Download Queue is empty.**\n\nNo active or recent tasks found.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	var sb strings.Builder
	sb.WriteString("📋 <b>Downloader Tasks & Queue</b>\n\n")

	for _, t := range tasks {
		statusIcon := "⏳"
		switch t.Status {
		case StatusProcessing:
			statusIcon = "⚡"
		case StatusCompleted:
			statusIcon = "✅"
		case StatusFailed:
			statusIcon = "❌"
		case StatusCancelled:
			statusIcon = "🛑"
		}

		name := t.CustomName
		if name == "" && t.FilePath != "" {
			name = filepath.Base(t.FilePath)
		}
		if name == "" {
			name = t.URLOrPath
		}
		if len(name) > 35 {
			name = name[:32] + "..."
		}

		sizeStr := ""
		if t.FileSize > 0 {
			sizeStr = fmt.Sprintf(" (%s)", FormatBytes(t.FileSize))
		}

		sb.WriteString(fmt.Sprintf("%s <b>#%d</b> <code>[%s]</code> <code>%s</code>%s — <i>%s</i>\n", statusIcon, t.ID, t.TaskType, html.EscapeString(name), sizeStr, t.Status))
	}

	sb.WriteString("\n<i>Use /cancel &lt;id&gt; to cancel an active/queued task.</i>")
	m.Reply(sb.String(), &telegram.SendOptions{ParseMode: "HTML"})
	return nil
}

func handleCancel(m *telegram.NewMessage) error {
	if !aichat.FilterAllowed(m) {
		return nil
	}

	rawArgs := strings.TrimSpace(m.Args())
	fields := strings.Fields(rawArgs)
	if len(fields) == 0 {
		m.Reply("ℹ️ **Usage:** `/cancel <task_id>`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	taskID, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		m.Reply("❌ Invalid task ID. Must be a number.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = CancelTask(ctx, taskID, m.SenderID(), isAdminUser(m.SenderID()))
	if err != nil {
		m.Reply(fmt.Sprintf("❌ Could not cancel: %v", err), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	m.Reply(fmt.Sprintf("🛑 Task #%d has been cancelled.", taskID), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}

func handleUploadCallback(cb *telegram.CallbackQuery) error {
	data := string(cb.Data)
	parts := strings.Split(data, ":")
	if len(parts) < 3 || parts[0] != "up" {
		cb.Answer("Invalid action", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	mode := parseUploadMode(parts[1])
	taskID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		cb.Answer("Invalid task ID", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetTask, err := GetTask(ctx, taskID)
	if err != nil || targetTask == nil {
		cb.Answer("Task not found", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	if !fileExists(targetTask.FilePath) || !isUnderDownloads(targetTask.FilePath) {
		cb.Answer("File no longer exists on disk", &telegram.CallbackOptions{Alert: true})
		return nil
	}

	cb.Answer(fmt.Sprintf("Queued upload as %s!", mode), nil)

	uploadTask := &DownloadTask{
		TaskType:   TaskTypeUpload,
		ChatID:     cb.ChatID,
		ReplyMsgID: targetTask.ReplyMsgID,
		UserID:     cb.SenderID,
		CustomName: string(mode),
		FilePath:   targetTask.FilePath,
		FileSize:   targetTask.FileSize,
	}

	newID, err := EnqueueTask(ctx, uploadTask)
	if err != nil {
		return err
	}

	queueCount, _ := GetQueuedCount(ctx)
	if queueCount < 1 {
		queueCount = 1
	}
	statusMsg, err := botClient.SendMessage(cb.ChatID,
		fmt.Sprintf("⏳ <b>Upload Task #%d Queued</b> <code>[%s]</code>\n\n📄 <b>File:</b> <code>%s</code>\n📦 <b>Size:</b> <code>%s</code>\n📊 <b>Queue Position:</b> <code>#%d</code>",
			newID, mode, html.EscapeString(filepath.Base(targetTask.FilePath)), FormatBytes(targetTask.FileSize), queueCount),
		&telegram.SendOptions{ParseMode: "HTML"},
	)
	if err == nil && statusMsg != nil {
		_ = UpdateTaskStatusMsgID(ctx, newID, statusMsg.ID)
	}

	return nil
}

func enqueueDownload(m *telegram.NewMessage, taskType TaskType, rawURL, customName string, replyMsgID int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetDesc := customName
	if targetDesc == "" && rawURL != "" {
		targetDesc = rawURL
	}
	if targetDesc == "" {
		targetDesc = "Telegram Media"
	}
	if len(targetDesc) > 40 {
		targetDesc = targetDesc[:37] + "..."
	}

	statusMsg, err := m.Reply(
		fmt.Sprintf("⏳ <b>Task Queued</b> <code>[%s]</code>\n\n🎯 <b>Target:</b> <code>%s</code>",
			taskType, html.EscapeString(targetDesc)),
		&telegram.SendOptions{ParseMode: "HTML"},
	)
	var statusMsgID int32
	if err == nil && statusMsg != nil {
		statusMsgID = statusMsg.ID
	}

	task := &DownloadTask{
		TaskType:    taskType,
		ChatID:      m.ChatID(),
		ReplyMsgID:  replyMsgID,
		StatusMsgID: statusMsgID,
		UserID:      m.SenderID(),
		URLOrPath:   rawURL,
		CustomName:  sanitizeFileName(customName),
	}

	taskID, err := EnqueueTask(ctx, task)
	if err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to enqueue task: %v", err))
		return nil
	}

	queueCount, _ := GetQueuedCount(ctx)
	if queueCount < 1 {
		queueCount = 1
	}

	if statusMsg != nil {
		_, _ = statusMsg.Edit(
			fmt.Sprintf("⏳ <b>Task #%d Queued</b> <code>[%s]</code>\n\n🎯 <b>Target:</b> <code>%s</code>\n📊 <b>Queue Position:</b> <code>#%d</code>",
				taskID, taskType, html.EscapeString(targetDesc), queueCount),
			&telegram.SendOptions{ParseMode: "HTML"},
		)
	}

	return nil
}

func enqueueUpload(m *telegram.NewMessage, targetTask *DownloadTask, mode UploadMode) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	uploadTask := &DownloadTask{
		TaskType:   TaskTypeUpload,
		ChatID:     m.ChatID(),
		ReplyMsgID: targetTask.ReplyMsgID,
		UserID:     m.SenderID(),
		CustomName: string(mode),
		FilePath:   targetTask.FilePath,
		FileSize:   targetTask.FileSize,
	}

	newID, err := EnqueueTask(ctx, uploadTask)
	if err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to enqueue upload: %v", err))
		return nil
	}

	queueCount, _ := GetQueuedCount(ctx)
	if queueCount < 1 {
		queueCount = 1
	}
	statusMsg, err := m.Reply(
		fmt.Sprintf("⏳ <b>Upload Task #%d Queued</b> <code>[%s]</code>\n\n📄 <b>File:</b> <code>%s</code>\n📦 <b>Size:</b> <code>%s</code>\n📊 <b>Queue Position:</b> <code>#%d</code>",
			newID, mode, html.EscapeString(filepath.Base(targetTask.FilePath)), FormatBytes(targetTask.FileSize), queueCount),
		&telegram.SendOptions{ParseMode: "HTML"},
	)
	if err == nil && statusMsg != nil {
		_ = UpdateTaskStatusMsgID(ctx, newID, statusMsg.ID)
	}

	return nil
}

func findTaskByReply(ctx context.Context, chatID int64, replyMsgID int32) *DownloadTask {
	var t DownloadTask
	err := db.Pool.QueryRow(ctx, `
		SELECT id, task_type, chat_id, reply_msg_id, status_msg_id, user_id, url_or_path, custom_name, file_path, file_size, status, error_msg, created_at, updated_at
		FROM download_tasks
		WHERE chat_id = $1 AND (status_msg_id = $2 OR reply_msg_id = $2)
		ORDER BY id DESC LIMIT 1`, chatID, replyMsgID).Scan(
		&t.ID, &t.TaskType, &t.ChatID, &t.ReplyMsgID, &t.StatusMsgID, &t.UserID, &t.URLOrPath, &t.CustomName,
		&t.FilePath, &t.FileSize, &t.Status, &t.ErrorMsg, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == nil {
		return &t
	}
	return nil
}
