package utility

import (
	"fmt"

	"github.com/amarnathcjd/gogram/telegram"
)

func handleID(m *telegram.NewMessage) error {
	senderID := m.SenderID()

	if m.IsPrivate() {
		m.Reply(fmt.Sprintf("👤 **Your ID:** `%d`", senderID), &telegram.SendOptions{ParseMode: "Markdown"})
		return nil
	}

	chatID := m.ChatID()
	m.Reply(fmt.Sprintf("👤 **Your ID:** `%d`\n💬 **Chat ID:** `%d`", senderID, chatID), &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}
