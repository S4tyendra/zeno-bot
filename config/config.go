package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	BotToken             string
	MongoDBURL           string
	AppID                int
	AppHash              string
	VertexProjectID         string
	VertexLocation          string
	VertexCredentialsBase64 string
	AllowedChatIDs          []int64
	MaxMediaSize         int64
	DefaultModel         string
	ImageModel           string
	HighImageModel       string
	TelegraphAccessToken string
	PerplexityJWT        string
	ExchangeRateAPIKey   string
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

	// Vertex AI backend — uses GOOGLE_APPLICATION_CREDENTIALS (service account JSON) for auth.
	VertexProjectID = os.Getenv("VERTEX_PROJECT_ID")
	if VertexProjectID == "" {
		log.Fatal("VERTEX_PROJECT_ID is required (GCP project ID for Vertex AI)")
	}

	VertexLocation = os.Getenv("VERTEX_LOCATION")
	if VertexLocation == "" {
		VertexLocation = "global"
	}

	VertexCredentialsBase64 = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_BASE64")
	if VertexCredentialsBase64 == "" {
		log.Fatal("GOOGLE_APPLICATION_CREDENTIALS_BASE64 is required (base64 encoded service account JSON key)")
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
		DefaultModel = "gemini-3.1-flash-lite"
	}

	ImageModel = os.Getenv("IMAGE_MODEL")
	if ImageModel == "" {
		ImageModel = "gemini-3.1-flash-image-preview"
	}

	HighImageModel = os.Getenv("HIGH_IMAGE_MODEL")
	if HighImageModel == "" {
		HighImageModel = "gemini-3.1-flash-image-preview"
	}

	TelegraphAccessToken = os.Getenv("TELEGRAPH_ACCESS_TOKEN")
	PerplexityJWT = os.Getenv("PERPLEXITY_JWT")
	ExchangeRateAPIKey = os.Getenv("EXCHANGERATE_API_KEY")
}
