package aichat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/config"
	"zeno/db"
)

const (
	pplxRefreshURL = "https://www.perplexity.ai/rest/auth/refresh_perplexity_jwt"
	pplxAskURL     = "https://www.perplexity.ai/rest/sse/perplexity_ask"
	pplxUserAgent  = "Ask/2.75.2/260554 (Android; Version 13; Google Pixel 7/sdk_gphone64_x86_64-userdebug 13 TE1A.240213.009 12342917 dev-keys) SDK 33"
	pplxUserID     = "1ac8f92f-2826-4510-9849-05bf4d27ba22"
	pplxDeviceID   = "b3b10b8550e2ae80"
	pplxXDeviceID  = "android:a3b10b8550e2ae79"
)

var (
	pplxMu  sync.RWMutex
	pplxJWT string
)

type PerplexityResult struct {
	Answer         string
	Sources        []PerplexitySource
	RelatedQueries []string
}

type PerplexitySource struct {
	Name    string
	URL     string
	Snippet string
}

// initPerplexity bootstraps JWT: env seed → refresh → store in MongoDB.
// On subsequent runs, DB JWT is used directly (env is just the bootstrap seed).
func initPerplexity() {
	// Try loading from DB first (already bootstrapped in a previous run)
	dbJWT, err := loadJWTFromDB()
	if err == nil && dbJWT != "" {
		pplxMu.Lock()
		pplxJWT = dbJWT
		pplxMu.Unlock()
		log.Println("[Perplexity] Loaded JWT from DB")
		return
	}

	// First-time: use env seed to generate a fresh JWT
	seedJWT := config.PerplexityJWT
	if seedJWT == "" {
		log.Println("[Perplexity] No JWT in env or DB — get_latest_data unavailable")
		return
	}

	log.Println("[Perplexity] Bootstrapping: refreshing env seed JWT...")
	newJWT, err := refreshJWT(seedJWT)
	if err != nil {
		log.Printf("[Perplexity] Seed refresh failed: %v — using seed directly", err)
		newJWT = seedJWT
	}

	pplxMu.Lock()
	pplxJWT = newJWT
	pplxMu.Unlock()

	if err := storeJWTInDB(newJWT); err != nil {
		log.Printf("[Perplexity] Failed to store JWT in DB: %v", err)
	} else {
		log.Println("[Perplexity] JWT bootstrapped and stored in DB")
	}
}

// startJWTRefreshCron runs in a goroutine, refreshes JWT every 4-7 hours (random delay).
func startJWTRefreshCron() {
	go func() {
		for {
			// Random delay between 4-7 hours
			const minSec = 4 * 60 * 60
			const rangeSec = 3 * 60 * 60 // 7h - 4h
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(rangeSec)))
			delay := time.Duration(minSec+int(n.Int64())) * time.Second

			log.Printf("[Perplexity] Next JWT refresh in %v", delay.Round(time.Minute))
			time.Sleep(delay)

			// Read current JWT from DB (source of truth)
			currentJWT, err := loadJWTFromDB()
			if err != nil || currentJWT == "" {
				pplxMu.RLock()
				currentJWT = pplxJWT
				pplxMu.RUnlock()
			}

			if currentJWT == "" {
				log.Println("[Perplexity] No JWT available for refresh, skipping cycle")
				continue
			}

			newJWT, err := refreshJWT(currentJWT)
			if err != nil {
				log.Printf("[Perplexity] Refresh failed: %v", err)
				continue
			}

			pplxMu.Lock()
			pplxJWT = newJWT
			pplxMu.Unlock()

			if err := storeJWTInDB(newJWT); err != nil {
				log.Printf("[Perplexity] Failed to store refreshed JWT: %v", err)
			}

			log.Println("[Perplexity] JWT refreshed and stored successfully")
		}
	}()
}

// ── JWT CRUD ─────────────────────────────────────────────────────────

func refreshJWT(oldJWT string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", pplxRefreshURL, nil)
	if err != nil {
		return "", err
	}

	setPplxHeaders(req, oldJWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		PerplexityJWT string `json:"perplexity_jwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode failed: %w", err)
	}

	if result.PerplexityJWT == "" {
		return "", fmt.Errorf("empty JWT in response")
	}

	return result.PerplexityJWT, nil
}

func loadJWTFromDB() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc struct {
		JWT string `bson:"jwt"`
	}
	err := db.Collection("system_settings").FindOne(ctx, bson.M{"_id": "perplexity_jwt"}).Decode(&doc)
	if err != nil {
		return "", err
	}
	return doc.JWT, nil
}

func storeJWTInDB(jwt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.Collection("system_settings").UpdateOne(
		ctx,
		bson.M{"_id": "perplexity_jwt"},
		bson.M{"$set": bson.M{"jwt": jwt, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// ── Search API ──────────────────────────────────────────────────────

func perplexitySearch(query string) (*PerplexityResult, error) {
	pplxMu.RLock()
	jwt := pplxJWT
	pplxMu.RUnlock()

	if jwt == "" {
		return nil, fmt.Errorf("no Perplexity JWT available")
	}

	body := map[string]any{
		"query_str": query + "\n\n\n\nYOU MUST SEARCH WEB FOR THIS QUERY",
		"params": map[string]any{
			"source":                          "android",
			"version":                         "2.17",
			"frontend_uuid":                   generateUUID(),
			"user_nextauth_id":                pplxUserID,
			"android_device_id":               pplxDeviceID,
			"mode":                            "concise",
			"is_related_query":                false,
			"is_voice_to_voice":               false,
			"timezone":                        "Asia/Kolkata",
			"language":                        "en-US",
			"query_source":                    "home",
			"is_incognito":                    false,
			"use_schematized_api":             true,
			"send_back_text_in_streaming_api": false,
			"supported_block_use_cases": []string{
				"answer_modes", "finance_widgets", "inline_assets",
				"inline_entity_cards", "inline_images", "knowledge_cards",
				"media_items", "place_widgets", "placeholder_cards",
				"search_result_widgets", "shopping_widgets", "sports_widgets",
				"prediction_market_widgets", "maps_preview",
			},
			"sources":          []string{"web"},
			"model_preference": "turbo",
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", pplxAskURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	setPplxHeaders(req, jwt)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("perplexity returned %d: %s", resp.StatusCode, string(snippet))
	}

	return parseSSEStream(resp.Body)
}

// parseSSEStream reads the Server-Sent Events stream and extracts
// the final completed message containing the answer + sources.
func parseSSEStream(r io.Reader) (*PerplexityResult, error) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024) // 2MB max line length for huge final messages

	var lastDataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data != "{}" && data != "" {
				lastDataLine = data
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("SSE read error: %w", err)
	}

	if lastDataLine == "" {
		return nil, fmt.Errorf("no data in SSE response")
	}

	// Parse the final JSON blob
	var msg map[string]any
	if err := json.Unmarshal([]byte(lastDataLine), &msg); err != nil {
		return nil, fmt.Errorf("parse final SSE message: %w", err)
	}

	result := &PerplexityResult{}

	// Extract answer from blocks → ask_text_0_markdown → markdown_block.answer
	if blocks, ok := msg["blocks"].([]any); ok {
		for _, block := range blocks {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			usage, _ := b["intended_usage"].(string)

			if usage == "ask_text_0_markdown" {
				if mb, ok := b["markdown_block"].(map[string]any); ok {
					if answer, ok := mb["answer"].(string); ok {
						result.Answer = answer
					}
				}
			}

			if usage == "web_results" {
				if wrb, ok := b["web_result_block"].(map[string]any); ok {
					if wrs, ok := wrb["web_results"].([]any); ok {
						for _, wr := range wrs {
							if w, ok := wr.(map[string]any); ok {
								result.Sources = append(result.Sources, PerplexitySource{
									Name:    fmt.Sprint(w["name"]),
									URL:     fmt.Sprint(w["url"]),
									Snippet: fmt.Sprint(w["snippet"]),
								})
							}
						}
					}
				}
			}
		}
	}

	// Extract related queries
	if rqs, ok := msg["related_queries"].([]any); ok {
		for _, rq := range rqs {
			if s, ok := rq.(string); ok {
				result.RelatedQueries = append(result.RelatedQueries, s)
			}
		}
	}

	if result.Answer == "" {
		return nil, fmt.Errorf("no answer found in perplexity response")
	}

	return result, nil
}

// ── Helpers ─────────────────────────────────────────────────────────

func setPplxHeaders(req *http.Request, jwt string) {
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("User-Agent", pplxUserAgent)
	req.Header.Set("X-App-Version", "2.75.2")
	req.Header.Set("X-Client-Version", "2.75.2")
	req.Header.Set("X-Client-Name", "Perplexity-Android")
	req.Header.Set("X-Client-Env", "prod")
	req.Header.Set("X-App-Apiclient", "android")
	req.Header.Set("X-App-Apiversion", "2.17")
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("X-Device-Id", pplxXDeviceID)
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
