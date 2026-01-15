package main

import (
	"log"
	"time"

	tele "gopkg.in/telebot.v3"

	"zeno/config"
	"zeno/db"
	"zeno/modules"
)

func main() {
	config.Load()

	db.Connect()
	defer db.Disconnect()

	b, err := tele.NewBot(tele.Settings{
		Token:  config.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal(err)
	}

	modules.RegisterAll(b)

	log.Println("Bot started!")
	b.Start()
}
