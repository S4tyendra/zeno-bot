package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	BotToken       string
	MongoDBURL     string
	AppID          int
	AppHash        string
	AIStudioAPIKey string
	AllowedChatIDs []int64
	MaxMediaSize   int64
	DefaultModel   string
	ImageModel     string
	HighImageModel string
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

	AIStudioAPIKey = os.Getenv("AISTUDIO_API_KEY")
	if AIStudioAPIKey == "" {
		log.Fatal("AISTUDIO_API_KEY is required")
	}

	allowedChatIDsStr := os.Getenv("ALLOWED_CHAT_IDS")
	if allowedChatIDsStr != "" {
		ids := strings.Split(allowedChatIDsStr, ",")
		for _, id := range ids {
			idInt, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
			if err == nil {
				AllowedChatIDs = append(AllowedChatIDs, idInt)
			}
		}
	}

	maxMediaSizeStr := os.Getenv("MAX_MEDIA_SIZE")
	if maxMediaSizeStr != "" {
		MaxMediaSize, _ = strconv.ParseInt(maxMediaSizeStr, 10, 64)
	}
	if MaxMediaSize == 0 {
		MaxMediaSize = 5 * 1024 * 1024 // 5MB default
	}

	DefaultModel = os.Getenv("DEFAULT_MODEL")
	if DefaultModel == "" {
		DefaultModel = "gemini-3.0-flash-preview"
	}

	ImageModel = os.Getenv("IMAGE_MODEL")
	if ImageModel == "" {
		ImageModel = "gemini-2.5-flash-image"
	}

	HighImageModel = os.Getenv("HIGH_IMAGE_MODEL")
	if HighImageModel == "" {
		HighImageModel = "gemini-3.0-pro-image-preview"
	}
}
