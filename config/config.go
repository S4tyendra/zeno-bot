package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

var (
	BotToken   string
	MongoDBURL string
	AppID      int
	AppHash    string
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

	appIDStr := os.Getenv("APP_ID")
	if appIDStr == "" {
		log.Fatal("APP_ID is required")
	}
	var err error
	AppID, err = strconv.Atoi(appIDStr)
	if err != nil {
		log.Fatal("APP_ID must be a valid integer")
	}

	AppHash = os.Getenv("APP_HASH")
	if AppHash == "" {
		log.Fatal("APP_HASH is required")
	}
}
