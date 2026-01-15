package modules

import (
	tele "gopkg.in/telebot.v3"

	"zeno/modules/aichat"
)

func RegisterAll(b *tele.Bot) {
	aichat.Register(b)

}
