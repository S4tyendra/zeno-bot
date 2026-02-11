package modules

import (
	"github.com/amarnathcjd/gogram/telegram"

	"zeno/modules/admin"
	"zeno/modules/aichat"
	"zeno/modules/utility"
)

func RegisterAll(client *telegram.Client) {
	aichat.Register(client)
	utility.Register(client)
	admin.Register(client)
}
