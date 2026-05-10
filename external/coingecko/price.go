// Package coingecko queries CoinGecko's free /simple/price endpoint
// for the current BTC spot price in a given fiat currency. Cached on
// 10-minute wall-clock buckets so callers within a window share one
// fetch; the bucket boundary (Truncate-aligned) keeps refresh times
// predictable across processes.
package coingecko

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	endpoint    = "https://api.coingecko.com/api/v3/simple/price"
	bucketDur   = 10 * time.Minute
	httpTimeout = 5 * time.Second
)

type cacheKey struct {
	currency string
	bucket   int64
}

var (
	mu    sync.Mutex
	cache = map[cacheKey]float64{}
)

// BitcoinPrice returns the current BTC spot price in `currency`
// (3-letter ISO code; case-insensitive). The result is cached on
// 10-minute wall-clock buckets, so two callers within the same
// :00-:10, :10-:20… window share a single upstream request.
//
// Returns the cached value forever once observed within a bucket;
// if a fetch fails mid-bucket, callers within the same bucket retry
// (no negative caching) and a subsequent bucket gets a fresh attempt.
func BitcoinPrice(currency string) (float64, error) {
	cur := strings.ToLower(strings.TrimSpace(currency))
	if cur == "" {
		return 0, fmt.Errorf("coingecko: empty currency")
	}
	bucket := time.Now().Truncate(bucketDur).Unix()
	key := cacheKey{currency: cur, bucket: bucket}

	mu.Lock()
	if v, ok := cache[key]; ok {
		mu.Unlock()
		return v, nil
	}
	mu.Unlock()

	price, err := fetch(cur)
	if err != nil {
		return 0, err
	}
	mu.Lock()
	cache[key] = price
	mu.Unlock()
	return price, nil
}

// CentsToSats converts an amount in cents-of-`currency` to sats,
// rounded to the nearest sat. `currency` is a 3-letter ISO code.
// Used by the affiliate-accounting webhook to record dollar/euro
// savings on a checkout in a unit that's stable across currencies.
func CentsToSats(cents int64, currency string) (int64, error) {
	if cents == 0 {
		return 0, nil
	}
	price, err := BitcoinPrice(currency)
	if err != nil {
		return 0, err
	}
	if price <= 0 {
		return 0, fmt.Errorf("coingecko: non-positive price %f for %q", price, currency)
	}
	// cents → main unit → BTC → sats:
	//     sats = (cents / 100) / price * 1e8 = cents * 1e6 / price
	sats := float64(cents) * 1e6 / price
	if sats < 0 {
		sats = 0
	}
	return int64(sats + 0.5), nil
}

func fetch(currency string) (float64, error) {
	url := fmt.Sprintf("%s?ids=bitcoin&vs_currencies=%s", endpoint, currency)
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("coingecko: get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("coingecko: status %d body=%s", resp.StatusCode, string(body))
	}
	var out map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("coingecko: decode: %w", err)
	}
	btc, ok := out["bitcoin"]
	if !ok {
		return 0, fmt.Errorf("coingecko: response missing bitcoin")
	}
	price, ok := btc[currency]
	if !ok || price <= 0 {
		return 0, fmt.Errorf("coingecko: response missing or invalid price for %q (got %v)", currency, btc)
	}
	return price, nil
}
