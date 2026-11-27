package grok

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"zeno/db"
	"zeno/modules/aichat"
	"zeno/modules/artifacts"
)

const (
	historyDir    = "generated/grok-history"
	historyPrefix = "grok-hist-"
	historyKeep   = 10
)

// saveHistory writes the chat history as a text artifact and keeps only the newest historyKeep.
func saveHistory(ctx context.Context, h aichat.ChatHistory) (string, error) {
	if db.Pool == nil {
		return "", errors.New("db unavailable")
	}
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", err
	}
	id := historyPrefix + uuid.NewString()
	abs, err := filepath.Abs(filepath.Join(historyDir, id+".txt"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(renderHistory(h)), 0o644); err != nil {
		return "", err
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO artifacts (artifact_id, file_path, mime_type, created_at) VALUES ($1,$2,$3,NOW())`,
		id, abs, "text/plain; charset=utf-8"); err != nil {
		os.Remove(abs)
		return "", err
	}
	rotateHistory(ctx)
	return artifacts.GetArtifactURL(id), nil
}

// rotateHistory deletes all but the newest historyKeep history artifacts, files and rows.
func rotateHistory(ctx context.Context) {
	rows, err := db.Pool.Query(ctx, `SELECT artifact_id, file_path FROM artifacts WHERE artifact_id LIKE $1 ORDER BY created_at DESC OFFSET $2`,
		historyPrefix+"%", historyKeep)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id, path string
		if rows.Scan(&id, &path) == nil {
			os.Remove(path)
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) > 0 {
		db.Pool.Exec(ctx, `DELETE FROM artifacts WHERE artifact_id = ANY($1)`, ids)
	}
}

func renderHistory(h aichat.ChatHistory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Chat: %s (%s)\nMessages: %d (oldest first). Files are listed by name only.\n\n", h.ChatName, h.ChatID, len(h.Messages))
	if len(h.Memories) > 0 {
		b.WriteString("<memories>\n")
		for _, um := range h.Memories {
			if len(um.Memories) == 0 {
				continue
			}
			fmt.Fprintf(&b, "  <user_memories userid=\"%d\" user=%q>\n", um.UserID, um.User)
			for i, m := range um.Memories {
				fmt.Fprintf(&b, "    <memory index=%d>%s</memory>\n", i+1, m)
			}
			b.WriteString("  </user_memories>\n")
		}
		b.WriteString("</memories>\n\n")
	}
	b.WriteString(h.XML)
	return b.String()
}
