package handlers

import (
	"net/url"
	"testing"

	"btcpp-web/internal/types"
)

func TestNewFormDecoderTrimsStrings(t *testing.T) {
	var vol types.Volunteer
	values := url.Values{
		"Name":     {" Alice "},
		"Email":    {" alice@example.com "},
		"Phone":    {" +15551234567 "},
		"Signal":   {" alice.99 "},
		"Twitter":  {" @alice "},
		"Nostr":    {" npub1alice "},
		"Comments": {" hello "},
	}

	if err := newFormDecoder().Decode(&vol, values); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	trimVolunteer(&vol)

	if vol.Name != "Alice" || vol.Email != "alice@example.com" || vol.Phone != "+15551234567" || vol.Signal != "alice.99" {
		t.Fatalf("decoded fields not trimmed: %+v", vol)
	}
	if vol.Twitter.Handle != "alice" || vol.Nostr != "npub1alice" || vol.Comments != "hello" {
		t.Fatalf("decoded social/text fields not trimmed: %+v", vol)
	}
}
