package utility

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"zeno/config"
	"zeno/db"
)

type ExchangeRateData struct {
	Base       string             `json:"base_code"`
	Rates      map[string]float64 `json:"conversion_rates"`
	LastUpdate time.Time          `json:"last_update"`
}

var (
	currentRates *ExchangeRateData
	ratesMutex   sync.RWMutex
)

func StartCurrencyService() {
	go func() {
		// Initial delay to ensure DB connection
		time.Sleep(2 * time.Second)
		updateRates()

		// Update loop
		for {
			time.Sleep(1 * time.Hour)
			updateRates()
		}
	}()
}

func updateRates() {
	if config.ExchangeRateAPIKey == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var data ExchangeRateData
	err := db.GetSetting(ctx, "exchange_rates", &data)

	if err == nil {
		ratesMutex.Lock()
		currentRates = &data
		ratesMutex.Unlock()
	}

	if err == nil && time.Since(data.LastUpdate) < 24*time.Hour {
		return
	}

	log.Println("[Currency] Fetching new rates from API...")
	url := fmt.Sprintf("https://v6.exchangerate-api.com/v6/%s/latest/USD", config.ExchangeRateAPIKey)

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[Currency] Failed to fetch rates: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("[Currency] API returned status %d", resp.StatusCode)
		return
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("[Currency] Failed to decode response: %v", err)
		return
	}

	if data.Base != "USD" {
		log.Printf("[Currency] Unexpected base currency: %s", data.Base)
		return
	}

	data.LastUpdate = time.Now()

	err = db.SetSetting(ctx, "exchange_rates", data)

	if err != nil {
		log.Printf("[Currency] Failed to cache rates in DB: %v", err)
	} else {
		log.Printf("[Currency] Updated and cached rates: %d currencies", len(data.Rates))
	}

	ratesMutex.Lock()
	currentRates = &data
	ratesMutex.Unlock()
}

func ConvertCurrency(amount float64, from, to string) (float64, error) {
	ratesMutex.RLock()
	rates := currentRates
	ratesMutex.RUnlock()

	if rates == nil || len(rates.Rates) == 0 {
		updateRates()
		ratesMutex.RLock()
		rates = currentRates
		ratesMutex.RUnlock()

		if rates == nil {
			return 0, fmt.Errorf("exchange rates not available yet")
		}
	}

	from = strings.ToUpper(from)
	to = strings.ToUpper(to)

	fromRate, ok1 := rates.Rates[from]
	toRate, ok2 := rates.Rates[to]

	if from == "USD" {
		fromRate = 1
		ok1 = true
	}
	if to == "USD" {
		toRate = 1
		ok2 = true
	}

	if !ok1 || !ok2 {
		return 0, fmt.Errorf("unknown currency: %s or %s", from, to)
	}

	result := (amount / fromRate) * toRate
	return result, nil
}
