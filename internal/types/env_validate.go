package types

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// DeriveHMACKey validates the configured HMAC secret before deriving the
// fixed-size key used by auth links, checkout price signatures, and
// newsletter tokens. An empty secret would otherwise deterministically become
// sha256(""), making every signed URL forgeable by anyone reading the code.
func DeriveHMACKey(secret string) ([32]byte, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return [32]byte{}, errors.New("HMAC_SECRET must not be empty")
	}
	return sha256.Sum256([]byte(secret)), nil
}

// ApplyDefaults fills operational defaults that are safe in both local
// config.toml development and production env-var config.
func (env *EnvConfig) ApplyDefaults() {
	if env.CacheTTLSec == 0 {
		env.CacheTTLSec = 300
	}
	if env.Recordings.PollSec == 0 {
		env.Recordings.PollSec = 60
	}
	if strings.TrimSpace(env.Recordings.NotifyEmail) == "" {
		env.Recordings.NotifyEmail = "nifty@btcpp.dev"
	}
	if strings.TrimSpace(env.Recordings.YouTubeTokenObject) == "" {
		env.Recordings.YouTubeTokenObject = "private/social/youtube-token.json.enc"
	}
	if strings.TrimSpace(env.Recordings.X.ProfileObject) == "" {
		env.Recordings.X.ProfileObject = "private/social/x-chrome-profile-staging.tgz.enc"
		if env.Prod {
			env.Recordings.X.ProfileObject = "private/social/x-chrome-profile-prod.tgz.enc"
		}
	}
	if env.Recordings.X.PostTimeoutSec == 0 {
		env.Recordings.X.PostTimeoutSec = 300
	}
	if env.Recordings.X.AuthWaitSec == 0 {
		env.Recordings.X.AuthWaitSec = 300
	}
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
	if env.Recordings.X.Enabled {
		if strings.TrimSpace(env.Recordings.EncryptionKey) == "" {
			missing = append(missing, "SOCIAL_STATE_KEY or X_PROFILE_ARCHIVE_KEY")
		}
		if strings.TrimSpace(env.Spaces.Endpoint) == "" ||
			strings.TrimSpace(env.Spaces.Bucket) == "" ||
			strings.TrimSpace(env.Spaces.Key) == "" ||
			strings.TrimSpace(env.Spaces.Secret) == "" {
			missing = append(missing, "Spaces config for X uploader")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
