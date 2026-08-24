package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/config"
	"zeno/db"
	"zeno/modules"
	"zeno/modules/artifacts"
)

func main() {
	config.Load()

	db.Connect()
	defer db.Disconnect()

	artifacts.StartServer()

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
	
	rows, err := db.Pool.Query(context.Background(), `SELECT chat_id FROM startup_chats`)
	if err == nil {
		var chatID int64
		for rows.Next() {
			if err := rows.Scan(&chatID); err == nil {
				client.SendMessage(chatID, "🚀 **Zeno Bot Started**\nAll systems online.", &telegram.SendOptions{ParseMode: "Markdown"})
			}
		}
		rows.Close()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		<-sig
		log.Println("Shutting down...")
		rows, err := db.Pool.Query(context.Background(), `SELECT chat_id FROM startup_chats`)
		if err == nil {
			var chatID int64
			for rows.Next() {
				if err := rows.Scan(&chatID); err == nil {
					client.SendMessage(chatID, "🛑 **Zeno Bot Shutting Down**\nGoing offline.", &telegram.SendOptions{ParseMode: "Markdown"})
				}
			}
			rows.Close()
		}
		os.Exit(0)
	}()

	client.Idle()
}
