package aichat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/connectors"
)

func executeYtDlp(args map[string]any, chatID, userID int64, private bool) map[string]any {
	rawURL := argString(args, "url")
	if rawURL == "" || !strings.HasPrefix(rawURL, "http") {
		return map[string]any{"success": false, "error": "url is required"}
	}
	name := argString(args, "name", "filename")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	status := startProgress(chatID, "⬇️ yt-dlp")
	link, local, size, err := connectors.SaveYtDlp(ctx, userID, connectors.ScopeProfile(private, chatID, userID), rawURL, name, func(line string) {
		if strings.HasPrefix(line, "[download]") || strings.Contains(line, "Merger") {
			status.line(line)
		}
	})
	if err != nil {
		status.done("❌ " + err.Error())
		return map[string]any{"success": false, "error": err.Error()}
	}
	if link != "" {
		status.done("✅ " + link)
		return map[string]any{"success": true, "url": link, "bytes": size}
	}
	status.done("✅ saved " + local)
	return map[string]any{"success": true, "file_path": local, "bytes": size}
}

func executeCopyLink(args map[string]any, chatID, userID int64) map[string]any {
	if userID == 0 {
		return map[string]any{"success": false, "error": "missing user"}
	}
	name := argString(args, "name", "filename")
	if p := argString(args, "file_path", "path"); p != "" {
		if !allowedUploadPath(p) {
			return map[string]any{"success": false, "error": "path must be under /workspace, /app/generated, or /tmp"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		link, size, err := connectors.PutFile(ctx, userID, p, name)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		return map[string]any{"success": true, "url": link, "bytes": size, "file_path": p}
	}
	fid := asInt32(args["file_id"])
	if fid == 0 {
		if n, ok := argInt64(args, "file_id"); ok {
			fid = int32(n)
		}
	}
	if fid == 0 {
		return map[string]any{"success": false, "error": "file_path or file_id is required"}
	}
	return copyTelegramFile(chatID, userID, fid, name)
}

func copyTelegramFile(chatID, userID int64, msgID int32, name string) map[string]any {
	if botClient == nil {
		return map[string]any{"success": false, "error": "bot is not ready"}
	}
	msgs, err := botClient.GetMessages(chatID, &telegram.SearchOption{IDs: []int32{msgID}})
	if err != nil || len(msgs) == 0 || msgs[0].Media() == nil {
		return map[string]any{"success": false, "error": "that message has no file"}
	}
	path, err := botClient.DownloadMedia(msgs[0].Media(), &telegram.DownloadOptions{})
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	defer os.Remove(path)
	if name == "" {
		name = filepath.Base(path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	link, size, err := connectors.PutFile(ctx, userID, path, name)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "url": link, "bytes": size, "file_id": msgID}
}

func allowedUploadPath(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	if strings.Contains(abs, "/profiles/") {
		return false
	}
	for _, root := range []string{"/workspace", "/app/generated", "/tmp"} {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
