package modules

import (
	"github.com/amarnathcjd/gogram/telegram"

	"zeno/modules/admin"
	"zeno/modules/afk"
	"zeno/modules/aichat"
	"zeno/modules/code"
	"zeno/modules/downloader"
	"zeno/modules/utility"
)

func RegisterAll(client *telegram.Client) {
	aichat.Register(client)
	downloader.Register(client)
	utility.Register(client)
	admin.Register(client)
	code.Register(client)
	afk.Register(client)
}
