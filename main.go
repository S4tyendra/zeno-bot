package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/config"
	"zeno/db"
	"zeno/modules"
	"zeno/modules/artifacts"
)

var startTime = time.Now()

func broadcastStartup(client *telegram.Client) {
	ist := time.FixedZone("IST", 5*3600+1800)
	now := time.Now().In(ist).Format("02 Jan 2006, 15:04:05")
	def := db.GetRuntimeModel("default", config.DefaultModel)
	img := db.GetRuntimeModel("image", config.ImageModel)

	msg := fmt.Sprintf(
		"🟢 **Zeno Online**\n\n"+
			"🤖 Default Model: `%s`\n"+
			"🖼️ Image Model: `%s`\n"+
			"🕐 Started: `%s IST`\n"+
			"⚡ Artifact server: `:%s`",
		def, img, now, artifacts.ArtifactPort,
	)

	rows, err := db.Pool.Query(context.Background(), `SELECT chat_id FROM startup_chats`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var chatID int64
		if rows.Scan(&chatID) == nil {
			client.SendMessage(chatID, msg, &telegram.SendOptions{ParseMode: "Markdown"})
		}
	}
}

func broadcastShutdown(client *telegram.Client) {
	ist := time.FixedZone("IST", 5*3600+1800)
	now := time.Now().In(ist).Format("02 Jan 2006, 15:04:05")
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(
		"🔴 **Zeno Shutting Down**\n\n"+
			"⏱️ Uptime: `%s`\n"+
			"🕐 Stopped: `%s IST`",
		uptime, now,
	)

	rows, err := db.Pool.Query(context.Background(), `SELECT chat_id FROM startup_chats`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var chatID int64
		if rows.Scan(&chatID) == nil {
			client.SendMessage(chatID, msg, &telegram.SendOptions{ParseMode: "Markdown"})
		}
	}
}

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
	broadcastStartup(client)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sig
		log.Println("Shutting down...")
		broadcastShutdown(client)
		os.Exit(0)
	}()

	client.Idle()
}
