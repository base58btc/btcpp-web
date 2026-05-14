// Package tokens persists OAuth refresh tokens for third-party
// integrations (YouTube uploader today, X tomorrow). Stored in a
// sibling bolt file rather than smuggled into sessions.bolt so the
// blast radius of a corrupted session DB stays bounded.
package tokens

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const bucket = "oauth"

// Token mirrors the fields of golang.org/x/oauth2.Token that we care
// about persisting. Kept as a plain struct so callers don't need the
// oauth2 import when all they want is to inspect "is there a token?".
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

var (
	db *bolt.DB
	mu sync.Mutex
)

// Init opens (or creates) the tokens.bolt file at the given path and
// ensures the "oauth" bucket exists. Idempotent — safe to call once at
// startup; later calls are no-ops if already opened.
func Init(path string) error {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		return nil
	}
	d, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if err := d.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucket))
		return err
	}); err != nil {
		d.Close()
		return fmt.Errorf("create bucket: %w", err)
	}
	db = d
	return nil
}

// Get returns the stored token for the given service key (e.g.
// "youtube"). Returns (nil, nil) when nothing has been persisted yet —
// callers should treat that as "needs OAuth bootstrap" rather than an
// error.
func Get(key string) (*Token, error) {
	if db == nil {
		return nil, fmt.Errorf("tokens store not initialized")
	}
	var raw []byte
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		v := b.Get([]byte(key))
		if len(v) == 0 {
			return nil
		}
		raw = append(raw, v...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("unmarshal token %s: %w", key, err)
	}
	return &t, nil
}

// Set stores a token under the given service key. A nil token deletes
// the entry (useful for "log out" / disconnect flows).
func Set(key string, t *Token) error {
	if db == nil {
		return fmt.Errorf("tokens store not initialized")
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if t == nil {
			return b.Delete([]byte(key))
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), raw)
	})
}

// Has reports whether a token exists for the given service key.
// Convenient for status checks on the admin page that don't care
// about the token's contents.
func Has(key string) bool {
	t, err := Get(key)
	return err == nil && t != nil && (t.AccessToken != "" || t.RefreshToken != "")
}
