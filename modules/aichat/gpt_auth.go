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

	"zeno/db"
)

const (
	gptClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	gptRefreshURL = "https://auth.openai.com/oauth/token"
	gptAuthID     = "gpt_auth"
)

type gptTokenDoc struct {
	ID           string
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	LastRefresh  time.Time
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

// loadGPTAuth loads from Postgres; if absent, seeds from env and writes to DB.
func loadGPTAuth() (*gptTokenDoc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc gptTokenDoc
	doc.ID = gptAuthID
	err := db.Pool.QueryRow(ctx, `
		SELECT access_token, refresh_token, id_token, account_id, last_refresh
		FROM gpt_auth WHERE id = $1`, gptAuthID).Scan(
		&doc.AccessToken, &doc.RefreshToken, &doc.IDToken, &doc.AccountID, &doc.LastRefresh,
	)
	if err == nil {
		log.Println("[GPT Auth] Loaded tokens from Postgres")
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
			ID:           gptAuthID,
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
		ID:           gptAuthID,
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
		// If the refresh token was already rotated by another instance, wipe
		// the stale DB record and re-seed fresh credentials from .env.
		if isRefreshTokenReused(err) {
			log.Println("[GPT Auth] refresh_token_reused — wiping DB and re-seeding from env")
			purgeGPTAuthFromDB()
			gptAuthMu.Lock()
			gptAuthCache = nil
			gptAuthMu.Unlock()
			return loadGPTAuth()
		}
		return doc, err
	}
	saveGPTAuthToDB(&refreshed)
	gptAuthMu.Lock()
	gptAuthCache = &refreshed
	gptAuthMu.Unlock()
	return &refreshed, nil
}

// isRefreshTokenReused reports whether err is an OpenAI refresh_token_reused 401.
func isRefreshTokenReused(err error) bool {
	return strings.Contains(err.Error(), "refresh_token_reused")
}

// purgeGPTAuthFromDB deletes the stored GPT auth row from Postgres.
func purgeGPTAuthFromDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.Pool.Exec(ctx, `DELETE FROM gpt_auth WHERE id = $1`, gptAuthID)
	if err != nil {
		log.Printf("[GPT Auth] Failed to purge DB record: %v", err)
	} else {
		log.Println("[GPT Auth] Purged stale token record from Postgres")
	}
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

	rawBody, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return doc, fmt.Errorf("refresh failed %d: %s", resp.StatusCode, string(rawBody))
	}

	var res map[string]interface{}
	json.Unmarshal(rawBody, &res)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.Pool.Exec(ctx, `
		INSERT INTO gpt_auth (id, access_token, refresh_token, id_token, account_id, last_refresh)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			id_token = EXCLUDED.id_token,
			account_id = EXCLUDED.account_id,
			last_refresh = EXCLUDED.last_refresh`,
		gptAuthID, doc.AccessToken, doc.RefreshToken, doc.IDToken, doc.AccountID, doc.LastRefresh)
	if err != nil {
		log.Printf("[GPT Auth] Failed to save to DB: %v", err)
	} else {
		log.Println("[GPT Auth] Saved tokens to Postgres")
	}
}

func asStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
