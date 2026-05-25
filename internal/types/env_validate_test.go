package types

import (
	"strings"
	"testing"
)

func TestDeriveHMACKeyRejectsWeakSecrets(t *testing.T) {
	for _, secret := range []string{"", "   "} {
		if _, err := DeriveHMACKey(secret); err == nil {
			t.Fatalf("DeriveHMACKey(%q) returned nil error", secret)
		}
	}
}

func TestDeriveHMACKeyAcceptsExistingSecret(t *testing.T) {
	if _, err := DeriveHMACKey("existing-prod-secret"); err != nil {
		t.Fatalf("DeriveHMACKey returned error: %s", err)
	}
}

func TestEnvConfigValidateRequiresProdSecrets(t *testing.T) {
	env := &EnvConfig{
		Port:      "8080",
		Host:      "https://example.test",
		MailerJob: 60,
		Prod:      true,
	}

	err := env.Validate()
	if err == nil {
		t.Fatal("Validate returned nil error")
	}
	for _, want := range []string{
		"NOTION_TOKEN",
		"MAILER_SECRET",
		"MAILER_ENDPOINT",
		"STRIPE_KEY",
		"STRIPE_END_SECRET",
		"OPENNODE_KEY",
		"OPENNODE_ENDPOINT",
		"REGISTRY_PIN",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate error %q missing %s", err, want)
		}
	}
}

func TestEnvConfigValidateAllowsCompleteProdConfig(t *testing.T) {
	env := &EnvConfig{
		Port:              "8080",
		Host:              "https://example.test",
		MailerJob:         60,
		MailerSecret:      "mailer",
		MailEndpoint:      "https://mailer.example.test",
		StripeKey:         "stripe",
		StripeEndpointSec: "stripe-webhook",
		RegistryPin:       "pin",
		Notion: NotionConfig{
			Token: "notion",
		},
		OpenNode: OpenNodeConfig{
			Key:      "opennode",
			Endpoint: "https://opennode.example.test",
		},
		Prod: true,
	}

	if err := env.Validate(); err != nil {
		t.Fatalf("Validate returned error: %s", err)
	}
}

func TestEnvConfigApplyDefaultsSeparatesXProfileObjects(t *testing.T) {
	staging := &EnvConfig{}
	staging.ApplyDefaults()
	if got, want := staging.Recordings.X.ProfileObject, "private/social/x-chrome-profile-staging.tgz.enc"; got != want {
		t.Fatalf("staging X profile object = %q, want %q", got, want)
	}

	prod := &EnvConfig{Prod: true}
	prod.ApplyDefaults()
	if got, want := prod.Recordings.X.ProfileObject, "private/social/x-chrome-profile-prod.tgz.enc"; got != want {
		t.Fatalf("prod X profile object = %q, want %q", got, want)
	}
}
