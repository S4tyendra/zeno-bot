package utility

import (
	"github.com/amarnathcjd/gogram/telegram"
)

func Register(client *telegram.Client) {
	client.On("cmd:math", handleMath)
	client.On("cmd:id", handleID)
}
