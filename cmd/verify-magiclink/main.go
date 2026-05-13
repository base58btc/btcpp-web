// verify-magiclink verifies a magic-link URL's HMAC against the local
// HMACSecret. Use to debug "this link 401s" / "this link landed me on
// dashboard-login" reports — distinguishes between a malformed link
// (bad base64, wrong byte length) and a signature mismatch (right
// shape, wrong secret or wrong email).
//
// Usage:
//
//	go run ./cmd/verify-magiclink -url 'http://localhost:8888/auth?em=...&hr=...'
//
// Reads config.toml from the cwd to pull HMACSecret. No network
// calls. No Notion writes.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/BurntSushi/toml"
)

type cfgFile struct {
	HMACSecret string `toml:"hmac_secret"`
}

func main() {
	rawURL := flag.String("url", "", "magic-link URL to verify (required)")
	flag.Parse()
	if *rawURL == "" {
		log.Fatal("required: -url")
	}

	var c cfgFile
	if _, err := toml.DecodeFile("config.toml", &c); err != nil {
		log.Fatalf("read config.toml: %s", err)
	}
	if c.HMACSecret == "" {
		log.Fatal("config.toml is missing hmac_secret — refusing to verify against a zero-byte key")
	}

	u, err := url.Parse(*rawURL)
	if err != nil {
		log.Fatalf("parse url: %s", err)
	}
	em := u.Query().Get("em")
	hr := u.Query().Get("hr")
	if em == "" || hr == "" {
		log.Fatalf("URL is missing em or hr query param (em=%q hr=%q)", em, hr)
	}

	emailBytes, err := base64.RawURLEncoding.DecodeString(em)
	if err != nil {
		log.Fatalf("decode em (base64url): %s", err)
	}
	hmacBytes, err := base64.RawURLEncoding.DecodeString(hr)
	if err != nil {
		log.Fatalf("decode hr (base64url): %s", err)
	}
	email := string(emailBytes)
	suppliedHex := string(hmacBytes)

	// Reproduce the server's key derivation: HMACKey = sha256(HMACSecret).
	keyArr := sha256.Sum256([]byte(c.HMACSecret))
	mac := hmac.New(sha256.New, keyArr[:])
	mac.Write([]byte(email))
	expectedHex := hex.EncodeToString(mac.Sum(nil))

	fmt.Printf("Decoded email: %q\n", email)
	fmt.Printf("Supplied HMAC (hex, %d chars): %s\n", len(suppliedHex), suppliedHex)
	fmt.Printf("Expected HMAC (hex, %d chars): %s\n", len(expectedHex), expectedHex)

	if suppliedHex == expectedHex {
		fmt.Println("\nMATCH — the link is valid for this HMACSecret.")
		os.Exit(0)
	}

	fmt.Println("\nMISMATCH — the link will fail VerifyEmailHMAC.")
	switch {
	case len(suppliedHex) != len(expectedHex):
		fmt.Printf("Length differs (%d vs %d). The link was probably truncated or built with a different algorithm.\n",
			len(suppliedHex), len(expectedHex))
	default:
		fmt.Println("Length matches but bytes don't. Most likely: the link was minted against a different HMACSecret")
		fmt.Println("(prod vs local config.toml mismatch, or the secret rotated since the link was generated).")
	}
	os.Exit(1)
}
