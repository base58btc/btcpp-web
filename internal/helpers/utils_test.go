package helpers

import (
	"strings"
	"testing"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func testAppContext(t *testing.T) *config.AppContext {
	t.Helper()
	key, err := types.DeriveHMACKey("test-secret")
	if err != nil {
		t.Fatalf("DeriveHMACKey: %s", err)
	}
	return &config.AppContext{
		Env: &types.EnvConfig{HMACKey: key},
	}
}

func TestEmailHMACRoundTrip(t *testing.T) {
	ctx := testAppContext(t)
	token := CreateEmailHMACTTL(ctx, "user@example.test", time.Minute)

	if !VerifyEmailHMAC(ctx, token, "user@example.test") {
		t.Fatal("expected token to verify")
	}
}

func TestEmailHMACRejectsWrongEmail(t *testing.T) {
	ctx := testAppContext(t)
	token := CreateEmailHMACTTL(ctx, "user@example.test", time.Minute)

	if VerifyEmailHMAC(ctx, token, "attacker@example.test") {
		t.Fatal("expected token to fail for a different email")
	}
}

func TestEmailHMACRejectsExpiredToken(t *testing.T) {
	ctx := testAppContext(t)
	token := CreateEmailHMACTTL(ctx, "user@example.test", -time.Second)

	if VerifyEmailHMAC(ctx, token, "user@example.test") {
		t.Fatal("expected expired token to fail")
	}
}

func TestEmailHMACRejectsLegacyTokenShape(t *testing.T) {
	ctx := testAppContext(t)

	if VerifyEmailHMAC(ctx, strings.Repeat("a", 64), "user@example.test") {
		t.Fatal("expected legacy bare-hex token to fail")
	}
}

func TestScopedHMACRoundTrip(t *testing.T) {
	ctx := testAppContext(t)
	token := CreateScopedHMAC(ctx, "media-render", "/media/imgs/example.png")

	if !VerifyScopedHMAC(ctx, "media-render", "/media/imgs/example.png", token) {
		t.Fatal("expected scoped token to verify")
	}
	if VerifyScopedHMAC(ctx, "different-purpose", "/media/imgs/example.png", token) {
		t.Fatal("expected scoped token to fail for a different purpose")
	}
}
