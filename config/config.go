package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

var (
	BotToken   string
	MongoDBURL string
)

func Load() {
	_ = godotenv.Load()

	BotToken = os.Getenv("BOT_TOKEN")
	if BotToken == "" {
		log.Fatal("BOT_TOKEN is required")
	}

	MongoDBURL = os.Getenv("MONGODB_URL")
	if MongoDBURL == "" {
		MongoDBURL = "mongodb://localhost:27017"
	}
}
