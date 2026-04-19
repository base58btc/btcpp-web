package types

import "strings"

type Twitter struct {
	Handle string
}

func (t Twitter) Link() string {
	if t.Handle == "" {
		return ""
	}
	return "https://x.com/" + t.Handle
}

func (t Twitter) Mention() string {
	if t.Handle == "" {
		return ""
	}
	return "@" + t.Handle
}

// ParseTwitter extracts just the handle from various input formats:
// "https://x.com/niftynei" -> "niftynei"
// "https://twitter.com/niftynei" -> "niftynei"
// "@niftynei" -> "niftynei"
// "niftynei" -> "niftynei"
func ParseTwitter(raw string) Twitter {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Twitter{}
	}

	// Strip URL prefix
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		raw = raw[idx+1:]
	}

	// Strip leading @
	raw = strings.TrimPrefix(raw, "@")

	if raw == "" {
		return Twitter{}
	}

	return Twitter{Handle: raw}
}
