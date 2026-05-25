package getters

import (
	"testing"

	notion "github.com/niftynei/go-notion"
)

func TestParseSpeakerTrimsEmail(t *testing.T) {
	speaker := parseSpeaker("speaker-id", map[string]notion.PropertyValue{
		"Name":          richTextProp("Anita"),
		"NormPhoto":     richTextProp(""),
		"Email":         {Email: "hi@anita.onl "},
		"Phone":         richTextProp(""),
		"Signal":        richTextProp(""),
		"Telegram":      richTextProp(""),
		"Twitter":       richTextProp(""),
		"npub":          richTextProp(""),
		"Github":        {},
		"Instagram":     richTextProp(""),
		"LinkedIn":      richTextProp(""),
		"Website":       {},
		"Company":       richTextProp(""),
		"OrgPhoto":      richTextProp(""),
		"AvailToHire":   {},
		"LookingToHire": {},
		"TShirt":        {},
		"Roles":         {},
	})

	if speaker.Email != "hi@anita.onl" {
		t.Fatalf("speaker.Email = %q, want trimmed email", speaker.Email)
	}
}

func richTextProp(s string) notion.PropertyValue {
	if s == "" {
		return notion.PropertyValue{}
	}
	return notion.PropertyValue{
		RichText: []*notion.RichText{
			{Type: notion.RichTextText, Text: &notion.Text{Content: s}},
		},
	}
}
