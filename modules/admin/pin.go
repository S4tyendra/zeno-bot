package admin

import (
	"fmt"
	"log"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
)

// handlePin pins the replied-to message.
//   - /pin (reply)               -> pin with notification
//   - /pin silent|s|quiet (reply) -> pin silently
func handlePin(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}
	if m.ReplyToMsgID() == 0 {
		m.Reply("ℹ️ Reply to the message you want to pin.")
		return nil
	}

	arg := strings.ToLower(strings.TrimSpace(m.Args()))
	silent := arg == "silent" || arg == "s" || arg == "quiet"

	if _, err := m.Client.PinMessage(m.ChatID(), m.ReplyToMsgID(), &telegram.PinOptions{Silent: silent}); err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to pin message: %v", err))
		return nil
	}

	log.Printf("[Admin] /pin msg %d in chat %d (silent=%v) by %d", m.ReplyToMsgID(), m.ChatID(), silent, m.SenderID())
	return nil
}

// handleUnpin unpins the replied-to message.
func handleUnpin(m *telegram.NewMessage) error {
	if !isAdmin(m) {
		m.Reply("🚫 Not authorized.")
		return nil
	}
	if m.ReplyToMsgID() == 0 {
		m.Reply("ℹ️ Reply to the message you want to unpin.")
		return nil
	}

	if _, err := m.Client.PinMessage(m.ChatID(), m.ReplyToMsgID(), &telegram.PinOptions{Unpin: true}); err != nil {
		m.Reply(fmt.Sprintf("❌ Failed to unpin message: %v", err))
		return nil
	}

	log.Printf("[Admin] /unpin msg %d in chat %d by %d", m.ReplyToMsgID(), m.ChatID(), m.SenderID())
	m.Reply("✅ Message unpinned.")
	return nil
}
