package admin

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

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
		// Fallback: try reading from docker logs
		cmd = exec.Command("docker", "logs", "--tail", lines, "zeno-bot")
		out, err = cmd.CombinedOutput()
		if err != nil {
			// Last fallback: tail the reflex output
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

	// Telegram message limit is ~4096 chars
	if len(output) > 3900 {
		output = output[len(output)-3900:]
	}

	m.Reply(fmt.Sprintf("📋 **Last %s lines:**\n```\n%s\n```", lines, output), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}
