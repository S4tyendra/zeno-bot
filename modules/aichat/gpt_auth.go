package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/db"
)

const (
	gptClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	gptRefreshURL = "https://auth.openai.com/oauth/token"
	gptMongoID    = "gpt_auth"
)

type gptTokenDoc struct {
	ID           string `bson:"_id"`
	AccessToken  string `bson:"access_token"`
	RefreshToken string `bson:"refresh_token"`
	IDToken      string `bson:"id_token"`
	AccountID    string `bson:"account_id"`
	LastRefresh  time.Time `bson:"last_refresh"`
}

var (
	gptAuthMu    sync.RWMutex
	gptAuthCache *gptTokenDoc
)

// GetGPTAuth returns a valid (unexpired) token doc, refreshing if needed.
func GetGPTAuth() (*gptTokenDoc, error) {
	gptAuthMu.RLock()
	cached := gptAuthCache
	gptAuthMu.RUnlock()

	if cached == nil {
		return loadGPTAuth()
	}
	if time.Since(cached.LastRefresh) > 58*time.Minute {
		return refreshGPTToken(cached)
	}
	return cached, nil
}

// loadGPTAuth loads from MongoDB; if absent, seeds from env and writes to DB.
func loadGPTAuth() (*gptTokenDoc, error) {
	col := db.Collection("gpt_auth")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc gptTokenDoc
	err := col.FindOne(ctx, bson.M{"_id": gptMongoID}).Decode(&doc)
	if err == nil {
		log.Println("[GPT Auth] Loaded tokens from MongoDB")
		// Check if stale
		if time.Since(doc.LastRefresh) > 58*time.Minute {
			return refreshGPTToken(&doc)
		}
		gptAuthMu.Lock()
		gptAuthCache = &doc
		gptAuthMu.Unlock()
		return &doc, nil
	}

	// Not in DB — seed from .env / local file
	log.Println("[GPT Auth] Not in DB, seeding from env/.codex/auth.json")
	doc, seedErr := seedGPTAuthFromEnv()
	if seedErr != nil {
		return nil, fmt.Errorf("GPT auth not in DB and env seed failed: %v", seedErr)
	}

	// Persist to DB for next time
	saveGPTAuthToDB(&doc)
	gptAuthMu.Lock()
	gptAuthCache = &doc
	gptAuthMu.Unlock()
	return &doc, nil
}

// seedGPTAuthFromEnv tries env vars first, then ~/.codex/auth.json.
func seedGPTAuthFromEnv() (gptTokenDoc, error) {
	idToken := os.Getenv("GPT_ID_TOKEN")
	refreshToken := os.Getenv("GPT_REFRESH_TOKEN")
	accountID := os.Getenv("GPT_ACCOUNT_ID")

	if idToken != "" && refreshToken != "" && accountID != "" {
		log.Println("[GPT Auth] Seeding from env vars")
		doc := gptTokenDoc{
			ID:           gptMongoID,
			IDToken:      idToken,
			RefreshToken: refreshToken,
			AccountID:    accountID,
			LastRefresh:  time.Now().Add(-60 * time.Minute), // force refresh on first use
		}
		// Generate a fresh access_token via refresh
		return refreshGPTTokenRaw(doc)
	}

	// Fallback: ~/.codex/auth.json
	home, _ := os.UserHomeDir()
	authFile := home + "/.codex/auth.json"
	data, err := ioutil.ReadFile(authFile)
	if err != nil {
		return gptTokenDoc{}, fmt.Errorf("GPT_ID_TOKEN/REFRESH_TOKEN not set and %s missing: %v", authFile, err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return gptTokenDoc{}, err
	}

	tokens, _ := raw["tokens"].(map[string]interface{})
	lastRefStr, _ := raw["last_refresh"].(string)
	lastRef, _ := time.Parse("2006-01-02T15:04:05.000Z", strings.ReplaceAll(lastRefStr, "+00:00", "Z"))

	return gptTokenDoc{
		ID:           gptMongoID,
		AccessToken:  asStr(tokens["access_token"]),
		RefreshToken: asStr(tokens["refresh_token"]),
		IDToken:      asStr(tokens["id_token"]),
		AccountID:    asStr(tokens["account_id"]),
		LastRefresh:  lastRef,
	}, nil
}

func refreshGPTToken(doc *gptTokenDoc) (*gptTokenDoc, error) {
	refreshed, err := refreshGPTTokenRaw(*doc)
	if err != nil {
		return doc, err // return stale but don't crash
	}
	saveGPTAuthToDB(&refreshed)
	gptAuthMu.Lock()
	gptAuthCache = &refreshed
	gptAuthMu.Unlock()
	return &refreshed, nil
}

func refreshGPTTokenRaw(doc gptTokenDoc) (gptTokenDoc, error) {
	log.Println("[GPT Auth] Refreshing access token...")
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": doc.RefreshToken,
		"client_id":     gptClientID,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(gptRefreshURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return doc, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		return doc, fmt.Errorf("refresh failed %d: %s", resp.StatusCode, string(b))
	}

	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)

	if at, ok := res["access_token"].(string); ok {
		doc.AccessToken = at
	}
	if rt, ok := res["refresh_token"].(string); ok {
		doc.RefreshToken = rt
	}
	if it, ok := res["id_token"].(string); ok {
		doc.IDToken = it
	}
	doc.LastRefresh = time.Now().UTC()
	return doc, nil
}

func saveGPTAuthToDB(doc *gptTokenDoc) {
	col := db.Collection("gpt_auth")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := col.UpdateOne(ctx,
		bson.M{"_id": gptMongoID},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Printf("[GPT Auth] Failed to save to DB: %v", err)
	} else {
		log.Println("[GPT Auth] Saved tokens to MongoDB")
	}
}

func asStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
