package getters

import (
	"testing"

	"btcpp-web/internal/types"
)

func TestNormalizeSpeakerInput(t *testing.T) {
	in := normalizeSpeakerInput(SpeakerInput{
		Name:      " Alice ",
		Email:     " alice@example.com ",
		Phone:     " +15551234567 ",
		Signal:    " alice.99 ",
		Telegram:  " alice_tg ",
		Twitter:   " @alice ",
		Nostr:     " npub1alice ",
		Github:    " https://github.com/alice ",
		Instagram: " alice_ig ",
		LinkedIn:  " https://linkedin.com/in/alice ",
		Website:   " https://alice.example ",
		Company:   " ACME ",
		OrgLogo:   " logo.png ",
		TShirt:    " MM ",
	})

	if in.Email != "alice@example.com" || in.Phone != "+15551234567" || in.Signal != "alice.99" {
		t.Fatalf("contact fields not trimmed: %+v", in)
	}
	if in.Twitter != "alice" || in.Website != "https://alice.example" || in.Company != "ACME" {
		t.Fatalf("social/org fields not trimmed: %+v", in)
	}
}

func TestNormalizeOrgInput(t *testing.T) {
	org := &types.Org{
		Name:      " ACME ",
		Tagline:   " Bitcoin things ",
		Email:     " hello@acme.example ",
		Website:   " https://acme.example ",
		Twitter:   types.Twitter{Handle: " @acme "},
		Nostr:     " npub1acme ",
		Matrix:    " @acme:example.com ",
		LinkedIn:  " https://linkedin.com/company/acme ",
		Instagram: " acme_ig ",
		Youtube:   " https://youtube.com/@acme ",
		Github:    " https://github.com/acme ",
		LogoLight: " https://cdn.example/light.png ",
		LogoDark:  " https://cdn.example/dark.png ",
		Notes:     " notes ",
	}
	normalizeOrgInput(org)

	if org.Name != "ACME" || org.Email != "hello@acme.example" || org.Website != "https://acme.example" {
		t.Fatalf("org fields not trimmed: %+v", org)
	}
	if org.Twitter.Handle != "acme" || org.Nostr != "npub1acme" || org.LinkedIn != "https://linkedin.com/company/acme" {
		t.Fatalf("org social fields not trimmed: %+v", org)
	}
}

func TestNormalizeVolunteerInput(t *testing.T) {
	vol := &types.Volunteer{
		Name:          " Alice ",
		Email:         " alice@example.com ",
		Phone:         " +15551234567 ",
		Signal:        " alice.99 ",
		ContactAt:     " Signal ",
		Comments:      " hello ",
		DiscoveredVia: " friend ",
		Hometown:      " Vienna ",
		Twitter:       types.Twitter{Handle: " @alice "},
		Nostr:         " npub1alice ",
		Shirt:         " MM ",
	}
	normalizeVolunteerInput(vol)

	if vol.Name != "Alice" || vol.Email != "alice@example.com" || vol.Phone != "+15551234567" || vol.Signal != "alice.99" {
		t.Fatalf("volunteer fields not trimmed: %+v", vol)
	}
	if vol.Twitter.Handle != "alice" || vol.Nostr != "npub1alice" || vol.Hometown != "Vienna" {
		t.Fatalf("volunteer social/location fields not trimmed: %+v", vol)
	}
}
