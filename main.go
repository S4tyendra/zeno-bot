package main

import (
	"log"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/config"
	"zeno/db"
	"zeno/modules"
)

func main() {
	config.Load()

	db.Connect()
	defer db.Disconnect()

	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:   int32(config.AppID),
		AppHash: config.AppHash,
		Session: "data/session.dat",
		Cache:   telegram.NewCache("data/cache.db", nil),
	})
	if err != nil {
		log.Fatal(err)
	}

	if _, err := client.Conn(); err != nil {
		log.Fatal(err)
	}

	if err := client.LoginBot(config.BotToken); err != nil {
		log.Fatal(err)
	}

	modules.RegisterAll(client)

	log.Println("Bot started!")
	client.Idle()
}
