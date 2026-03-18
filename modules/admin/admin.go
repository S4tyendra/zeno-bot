package admin

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

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

	cmd := exec.Command("journalctl", "-u", "zeno", "-n", lines, "--no-pager")
	out, err := cmd.CombinedOutput()

	if err != nil {
		cmd = exec.Command("docker", "logs", "--tail", lines, "zeno-bot")
		out, err = cmd.CombinedOutput()
		if err != nil {
			cmd = exec.Command("tail", "-n", lines, "/tmp/zeno.log")
			out, err = cmd.CombinedOutput()
			if err != nil {
				m.Reply(fmt.Sprintf("❌ Failed to fetch logs: `%v`", err), &telegram.SendOptions{ParseMode: "Markdown"})
				return nil
			}
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
