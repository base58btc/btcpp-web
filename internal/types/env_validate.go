package types

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

const MinHMACSecretBytes = 32

// DeriveHMACKey validates the configured HMAC secret before deriving the
// fixed-size key used by auth links, checkout price signatures, and
// newsletter tokens. An empty secret would otherwise deterministically become
// sha256(""), making every signed URL forgeable by anyone reading the code.
func DeriveHMACKey(secret string) ([32]byte, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < MinHMACSecretBytes {
		return [32]byte{}, fmt.Errorf("HMAC_SECRET must be at least %d bytes", MinHMACSecretBytes)
	}
	return sha256.Sum256([]byte(secret)), nil
}

// Validate checks the pieces that must be present before the HTTP server is
// allowed to boot. Optional integrations can still be empty in development,
// but production rejects missing secrets for public endpoints that would be
// unsafe with zero values.
func (env *EnvConfig) Validate() error {
	if env == nil {
		return errors.New("nil EnvConfig")
	}
	var missing []string
	if strings.TrimSpace(env.Port) == "" {
		missing = append(missing, "PORT")
	}
	if strings.TrimSpace(env.Host) == "" {
		missing = append(missing, "HOST")
	}
	if !env.MailOff && env.MailerJob <= 0 {
		missing = append(missing, "MAILER_JOB_SEC")
	}
	if env.Prod {
		required := map[string]string{
			"NOTION_TOKEN":      env.Notion.Token,
			"MAILER_SECRET":     env.MailerSecret,
			"MAILER_ENDPOINT":   env.MailEndpoint,
			"STRIPE_KEY":        env.StripeKey,
			"STRIPE_END_SECRET": env.StripeEndpointSec,
			"OPENNODE_KEY":      env.OpenNode.Key,
			"OPENNODE_ENDPOINT": env.OpenNode.Endpoint,
			"REGISTRY_PIN":      env.RegistryPin,
		}
		for name, value := range required {
			if strings.TrimSpace(value) == "" {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
