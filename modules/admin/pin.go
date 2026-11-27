package admin

import (
	"fmt"
	"log"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
)

// canPin reports whether the sender is allowed to run /pin and /unpin:
//   - Bot owner (Satyendra / adminUserID / adminUsername) always bypasses the caller check;
//     only the bot needs pin permission in the group.
//   - In groups/supergroups/channels, any creator or admin with PinMessages permission is allowed.
//   - Anonymous admins posting as the group itself are also allowed.
func canPin(m *telegram.NewMessage) bool {
	if isAdmin(m) {
		return true
	}
	if m.IsPrivate() {
		return false
	}

	// Anonymous admin posting as the group/channel itself.
	if m.Message != nil {
		if fromCh, ok := m.Message.FromID.(*telegram.PeerChannel); ok {
			if m.Channel != nil && fromCh.ChannelID == m.Channel.ID {
				return true
			}
			if peerCh, ok := m.Message.PeerID.(*telegram.PeerChannel); ok && fromCh.ChannelID == peerCh.ChannelID {
				return true
			}
		}
	}

	// Supergroup / channel admin check via GetChatMember.
	p, err := m.Client.GetChatMember(m.ChatID(), m.SenderID())
	if err == nil && p != nil {
		if p.Status == telegram.Creator {
			return true
		}
		if p.Status == telegram.Admin && p.Rights != nil && p.Rights.PinMessages {
			return true
		}
		return false
	}

	// Legacy basic group fallback (GetChatMember only handles channels/supergroups).
	if m.Chat != nil {
		full, fErr := m.Client.MessagesGetFullChat(m.Chat.ID)
		if fErr != nil {
			log.Printf("[Admin] canPin MessagesGetFullChat error in chat %d for user %d: %v", m.ChatID(), m.SenderID(), fErr)
			return false
		}
		if chatFull, ok := full.FullChat.(*telegram.ChatFullObj); ok {
			if parts, ok := chatFull.Participants.(*telegram.ChatParticipantsObj); ok {
				uid := m.SenderID()
				for _, part := range parts.Participants {
					switch cp := part.(type) {
					case *telegram.ChatParticipantCreator:
						if cp.UserID == uid {
							return true
						}
					case *telegram.ChatParticipantAdmin:
						if cp.UserID == uid {
							return true
						}
					}
				}
			}
		}
		return false
	}

	if err != nil {
		log.Printf("[Admin] canPin GetChatMember error in chat %d for user %d: %v", m.ChatID(), m.SenderID(), err)
	}
	return false
}

// handlePin pins the replied-to message.
//   - /pin (reply)               -> pin with notification
//   - /pin silent|s|quiet (reply) -> pin silently
func handlePin(m *telegram.NewMessage) error {
	if !canPin(m) {
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
	deleteTriggeringCommand(m)
	return nil
}

// handleUnpin unpins the replied-to message.
func handleUnpin(m *telegram.NewMessage) error {
	if !canPin(m) {
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
	deleteTriggeringCommand(m)
	return nil
}

// deleteTriggeringCommand deletes the triggering /pin or /unpin command message
// in groups when the bot has delete-messages permission, failing gracefully if not.
func deleteTriggeringCommand(m *telegram.NewMessage) {
	if m == nil || m.IsPrivate() {
		return
	}
	if _, err := m.Delete(); err != nil {
		log.Printf("[Admin] could not delete command msg %d in chat %d: %v", m.ID, m.ChatID(), err)
	}
}
