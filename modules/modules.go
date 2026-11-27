package modules

import (
	"github.com/amarnathcjd/gogram/telegram"

	"zeno/connectors"
	"zeno/modules/admin"
	"zeno/modules/afk"
	"zeno/modules/aichat"
	"zeno/modules/code"
	"zeno/modules/downloader"
	"zeno/modules/grok"
	"zeno/modules/utility"
)

func RegisterAll(client *telegram.Client) {
	aichat.Register(client)
	connectors.Register(client, func(m *telegram.NewMessage) bool {
		return aichat.FilterAllowed(m)
	})
	downloader.Register(client)
	grok.Register(client)
	utility.Register(client)
	admin.Register(client)
	code.Register(client)
	afk.Register(client)
}
