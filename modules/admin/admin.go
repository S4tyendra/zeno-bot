package admin

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"zeno/config"
	"zeno/db"
	"zeno/modules/aichat"

	"github.com/amarnathcjd/gogram/telegram"
)

const (
	adminUsername       = "s4tyendra"
	adminUserID   int64 = 1089528685
)

func isAdmin(m *telegram.NewMessage) bool {
	if m.SenderID() == adminUserID {
		return true
	}
	if m.Sender != nil && strings.EqualFold(m.Sender.Username, adminUsername) {
		return true
	}
	return false
}

func Register(client *telegram.Client) {
	client.On("cmd:logs", handleLogs)
	client.On("cmd:allowai", handleAllowAI)
	client.On("cmd:noallowai", handleNoAllowAI)
	client.On("cmd:setmodel", handleSetModel)
	client.On("cmd:getmodel", handleGetModel)
	client.On("cmd:sudoers", handleSudoers)
}

func handleLogs(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}

	args := m.Args()
	lines := "50"
	if args != "" {
		lines = args
	}

	log.Printf("[Admin] /logs requested by %d, lines=%s", m.SenderID(), lines)

	cli := aichat.ContainerCLI()
	// Primary: nerdctl logs with explicit containerd address + namespace
	cmd := exec.Command(cli, "--address", "/run/containerd/containerd.sock", "--namespace", "default", "logs", "--tail", lines, "zeno-bot")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: without explicit flags (picks up CONTAINERD_ADDRESS env var)
		cmd = exec.Command(cli, "logs", "--tail", lines, "zeno-bot")
		out, err = cmd.CombinedOutput()
		if err != nil {
			m.Reply(fmt.Sprintf("❌ Failed to fetch logs: `%v`\n```\n%s\n```", err, strings.TrimSpace(string(out))), &telegram.SendOptions{ParseMode: "Markdown"})
			return nil
		}
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		m.Reply("📋 No logs found.")
		return nil
	}

	if len(output) > 3900 {
		output = output[len(output)-3900:]
	}

	m.Reply(fmt.Sprintf("📋 **Last %s lines:**\n```\n%s\n```", lines, output), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}

// handleAllowAI implements:
//   - /allowai in a group → allow the group chat (everyone inside can use)
//   - /allowai replying to a user in a group → allow that specific user (they can use anywhere)
func handleAllowAI(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}

	// Replied-to message → allow that specific user
	if m.ReplyToMsgID() != 0 {
		// We need the sender of the replied-to message
		// gogram: get the message to find sender
		msgs, err := m.Client.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{m.ReplyToMsgID()}})
		if err != nil || len(msgs) == 0 {
			m.Reply("❌ Couldn't fetch replied-to message.")
			return nil
		}
		target := msgs[0]
		uid := target.SenderID()
		aichat.AddAllowUser(uid)
		log.Printf("[Admin] /allowai granted USER %d by %d", uid, m.SenderID())

		name := fmt.Sprintf("User `%d`", uid)
		if target.Sender != nil && target.Sender.Username != "" {
			name = "@" + target.Sender.Username
		} else if target.Sender != nil && target.Sender.FirstName != "" {
			name = target.Sender.FirstName
		}
		m.Reply(fmt.Sprintf("✅ %s can now use AI anywhere.", name), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	// No reply → allow the current chat/group
	if m.IsPrivate() {
		m.Reply("ℹ️ Use this in a group to allow the group, or reply to a user to allow them individually.")
		return nil
	}

	chatID := m.ChatID()
	aichat.AddAllowChat(chatID)
	log.Printf("[Admin] /allowai granted CHAT %d by %d", chatID, m.SenderID())
	m.Reply("✅ This group is now allowed to use AI.")
	return nil
}

// handleNoAllowAI removes access.
//   - /noallowai in group → remove group
//   - /noallowai reply → remove that user
func handleNoAllowAI(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}

	if m.ReplyToMsgID() != 0 {
		msgs, err := m.Client.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{m.ReplyToMsgID()}})
		if err != nil || len(msgs) == 0 {
			m.Reply("❌ Couldn't fetch replied-to message.")
			return nil
		}
		target := msgs[0]
		uid := target.SenderID()
		aichat.RemoveAllow(uid)
		log.Printf("[Admin] /noallowai revoked USER %d by %d", uid, m.SenderID())

		name := fmt.Sprintf("User `%d`", uid)
		if target.Sender != nil && target.Sender.Username != "" {
			name = "@" + target.Sender.Username
		}
		m.Reply(fmt.Sprintf("🚫 %s's AI access revoked.", name), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	if m.IsPrivate() {
		m.Reply("ℹ️ Use this in a group to remove the group's access.")
		return nil
	}

	chatID := m.ChatID()
	aichat.RemoveAllow(chatID)
	log.Printf("[Admin] /noallowai revoked CHAT %d by %d", chatID, m.SenderID())
	m.Reply("🚫 This group's AI access has been revoked.")
	return nil
}

func handleSetModel(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}
	args := strings.Fields(m.Args())
	if len(args) < 2 {
		m.Reply("ℹ️ Usage: `/setmodel <default|image|highimage> <model_name>`", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	key := args[0]
	modelName := args[1]
	
	switch strings.ToLower(key) {
	case "default", "image", "highimage":
	default:
		m.Reply("❌ Invalid model key. Use `default`, `image`, or `highimage`.", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	
	err := db.SetRuntimeModel(key, modelName)
	if err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to save model: %v", err))
		return nil
	}
	
	m.Reply(fmt.Sprintf("✅ Model `%s` updated to `%s`", key, modelName), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}

func handleGetModel(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}
	
	def := db.GetRuntimeModel("default", config.DefaultModel)
	img := db.GetRuntimeModel("image", config.ImageModel)
	himg := db.GetRuntimeModel("highimage", config.HighImageModel)
	
	msg := fmt.Sprintf("🤖 **Current Models**\n\nDefault: `%s`\nImage: `%s`\nHighImage: `%s`", def, img, himg)
	m.Reply(msg, &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}

func handleSudoers(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}
	
	args := strings.Fields(m.Args())
	if len(args) == 0 {
		m.Reply("ℹ️ Usage: `/sudoers add|remove` (adds/removes current chat for startup/shutdown messages)", &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}
	
	action := strings.ToLower(args[0])
	chatID := m.ChatID()
	
	if action == "add" {
		_, err := db.Pool.Exec(context.Background(), `INSERT INTO startup_chats (chat_id) VALUES ($1) ON CONFLICT DO NOTHING`, chatID)
		if err != nil {
			m.Reply(fmt.Sprintf("❌ DB error: %v", err))
			return nil
		}
		m.Reply("✅ Current chat added to startup/shutdown broadcast list.")
	} else if action == "remove" {
		_, err := db.Pool.Exec(context.Background(), `DELETE FROM startup_chats WHERE chat_id = $1`, chatID)
		if err != nil {
			m.Reply(fmt.Sprintf("❌ DB error: %v", err))
			return nil
		}
		m.Reply("✅ Current chat removed from startup/shutdown broadcast list.")
	} else {
		m.Reply("❌ Invalid action. Use `add` or `remove`.", &telegram.SendOptions{ParseMode: "Markdown"})
	}
	return nil
}
