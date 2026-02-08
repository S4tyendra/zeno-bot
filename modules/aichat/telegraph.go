package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"zeno/config"
	"zeno/db"
)

type TelegraphResponse struct {
	Ok     bool `json:"ok"`
	Result struct {
		URL  string `json:"url"`
		Path string `json:"path"`
	} `json:"result"`
	Error string `json:"error"`
}

type TelegraphAccount struct {
	AccessToken string `json:"access_token" bson:"access_token"`
	ShortName   string `json:"short_name" bson:"short_name"`
	AuthUrl     string `json:"auth_url" bson:"auth_url"`
}

type CreateAccountResponse struct {
	Ok     bool             `json:"ok"`
	Result TelegraphAccount `json:"result"`
	Error  string           `json:"error"`
}

func ensureTelegraphToken() {
	if config.TelegraphAccessToken != "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check DB first
	var account TelegraphAccount
	err := db.Collection("system_settings").FindOne(ctx, bson.M{"_id": "telegraph_token"}).Decode(&account)
	if err == nil && account.AccessToken != "" {
		config.TelegraphAccessToken = account.AccessToken
		log.Println("[AiChat] Loaded Telegraph token from DB")
		return
	}

	log.Println("[AiChat] No Telegraph token found in env or DB, generating new account...")

	// Generate new token
	newAccount, err := createTelegraphAccount()
	if err != nil {
		log.Printf("[AiChat] Failed to create Telegraph account: %v", err)
		return
	}

	// Store in DB
	_, err = db.Collection("system_settings").UpdateOne(
		ctx,
		bson.M{"_id": "telegraph_token"},
		bson.M{"$set": newAccount},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Printf("[AiChat] Failed to store Telegraph token in DB: %v", err)
	} else {
		log.Println("[AiChat] Stored new Telegraph token in DB")
	}

	config.TelegraphAccessToken = newAccount.AccessToken
}

func createTelegraphAccount() (TelegraphAccount, error) {
	// API endpoint: https://api.telegra.ph/createAccount
	data := url.Values{}
	data.Set("short_name", "Nitya")
	data.Set("author_name", "Nitya")

	resp, err := http.PostForm("https://api.telegra.ph/createAccount", data)
	if err != nil {
		return TelegraphAccount{}, err
	}
	defer resp.Body.Close()

	var result CreateAccountResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TelegraphAccount{}, err
	}

	if !result.Ok {
		return TelegraphAccount{}, fmt.Errorf("telegraph api error: %s", result.Error)
	}

	return result.Result, nil
}

func uploadToTelegraph(title, content string) (string, error) {
	if config.TelegraphAccessToken == "" {
		// Fallback attempt to ensure creation if missing at runtime
		ensureTelegraphToken()
		if config.TelegraphAccessToken == "" {
			return "", fmt.Errorf("telegraph access token not available")
		}
	}

	nodes := []map[string]interface{}{
		{
			"tag":      "p",
			"children": []string{content},
		},
	}

	nodesBytes, err := json.Marshal(nodes)
	if err != nil {
		return "", err
	}

	// Prepare data
	data := url.Values{}
	data.Set("access_token", config.TelegraphAccessToken)
	data.Set("title", title)
	data.Set("author_name", "Nitya")
	data.Set("content", string(nodesBytes))
	data.Set("return_content", "true")

	resp, err := http.PostForm("https://api.telegra.ph/createPage", data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result TelegraphResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if !result.Ok {
		return "", fmt.Errorf("telegraph api error: %s", result.Error)
	}

	// Use graph.org instead of telegra.ph as it is blocked in India
	return fmt.Sprintf("https://graph.org/%s", result.Result.Path), nil
}
