package modules

import (
	"github.com/amarnathcjd/gogram/telegram"

	"zeno/modules/aichat"
)

func RegisterAll(client *telegram.Client) {
	aichat.Register(client)
}
