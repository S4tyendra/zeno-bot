package connectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

// Allowed reports whether a message may use connector commands.
type Allowed func(*telegram.NewMessage) bool

// Register wires /s3 and /cookies.
func Register(client *telegram.Client, allowed Allowed) {
	client.On("cmd:s3", func(m *telegram.NewMessage) error {
		if allowed != nil && !allowed(m) {
			return nil
		}
		return handleS3(m)
	})
	client.On("cmd:cookies", func(m *telegram.NewMessage) error {
		if allowed != nil && !allowed(m) {
			return nil
		}
		return handleCookies(client, m)
	})
}

func handleS3(m *telegram.NewMessage) error {
	args := strings.TrimSpace(m.Args())
	if args == "" || args == "show" {
		c, err := Load(context.Background(), m.SenderID())
		if err != nil {
			m.Reply("No S3 connector.\n\nSend:\n`/s3 endpoint=https://s3.amazonaws.com`\n`bucket=my-bucket`\n`access_key=...`\n`secret_key=...`\n`region=us-east-1`\n`prefix=zeno/`\n`public_base=https://cdn.example.com` (optional, skips expiring links)", &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
		key := c.AccessKey
		if len(key) > 4 {
			key = key[:4] + "…"
		}
		m.Reply(fmt.Sprintf("S3 for you\nendpoint: `%s`\nbucket: `%s`\nregion: `%s`\nprefix: `%s`\naccess_key: `%s`\npublic_base: `%s`",
			c.Endpoint, c.Bucket, c.Region, c.Prefix, key, c.PublicBase), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	if args == "clear" {
		_ = Clear(context.Background(), m.SenderID())
		m.Reply("S3 connector removed.")
		return nil
	}
	kv := ParseKV(args)
	c, _ := Load(context.Background(), m.SenderID())
	c.UserID = m.SenderID()
	if v := kv["endpoint"]; v != "" {
		c.Endpoint = v
	}
	if v := kv["bucket"]; v != "" {
		c.Bucket = v
	}
	if v := kv["region"]; v != "" {
		c.Region = v
	}
	if v := kv["access_key"]; v != "" {
		c.AccessKey = v
	}
	if v := kv["secret_key"]; v != "" {
		c.SecretKey = v
	}
	if v, ok := kv["prefix"]; ok {
		c.Prefix = v
	}
	if v, ok := kv["public_base"]; ok {
		c.PublicBase = v
	}
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		m.Reply("Need endpoint, bucket, access_key, and secret_key.")
		return nil
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := Save(ctx, c); err != nil {
		m.Reply("Could not save: " + err.Error())
		return nil
	}
	m.Reply(fmt.Sprintf("Saved S3 connector for bucket `%s`.", c.Bucket), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}

func handleCookies(client *telegram.Client, m *telegram.NewMessage) error {
	args := strings.TrimSpace(m.Args())
	dir := ProfileDir(m.SenderID())
	where := "your"
	if !m.IsPrivate() {
		dir = GroupProfileDir(m.ChatID())
		where = "this group's"
	}
	if args == "clear" {
		_ = os.Remove(filepath.Join(dir, "cookies.json"))
		_ = os.Remove(filepath.Join(dir, "cookies.txt"))
		m.Reply("Cookies cleared.")
		return nil
	}
	var raw []byte
	if m.ReplyToMsgID() != 0 && client != nil {
		msgs, err := client.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{m.ReplyToMsgID()}})
		if err == nil && len(msgs) > 0 && msgs[0].Media() != nil {
			path, err := client.DownloadMedia(msgs[0].Media(), &telegram.DownloadOptions{})
			if err != nil {
				m.Reply("Could not download that file: " + err.Error())
				return nil
			}
			defer os.Remove(path)
			raw, err = os.ReadFile(path)
			if err != nil {
				m.Reply(err.Error())
				return nil
			}
		}
	}
	if len(raw) == 0 && args != "" {
		raw = []byte(args)
	}
	if len(raw) == 0 {
		n := CookieCountDir(dir)
		m.Reply(fmt.Sprintf("%s cookies: %d\n\nReply to a Netscape cookies.txt or a JSON export with /cookies, or paste the file contents after the command. /cookies clear wipes them. In a group this is the shared browser.", where, n))
		return nil
	}
	n, err := ImportCookiesDir(dir, raw)
	if err != nil {
		m.Reply("Could not read cookies: " + err.Error())
		return nil
	}
	m.Reply(fmt.Sprintf("Saved %d cookies for %s browser. They load the next time it starts.", n, where))
	return nil
}
