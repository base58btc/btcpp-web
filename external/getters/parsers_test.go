package getters

import (
	"testing"
	"time"

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

func TestParseSpeakerTrimsURLFields(t *testing.T) {
	speaker := parseSpeaker("speaker-id", map[string]notion.PropertyValue{
		"Name":          richTextProp("Anita"),
		"NormPhoto":     richTextProp(""),
		"Email":         {Email: "hi@anita.onl"},
		"Phone":         richTextProp(""),
		"Signal":        richTextProp(""),
		"Telegram":      richTextProp(""),
		"Twitter":       richTextProp(""),
		"npub":          richTextProp(""),
		"Github":        {URL: " https://github.com/anita "},
		"Instagram":     richTextProp(""),
		"LinkedIn":      richTextProp(" linkedin.com/in/anita "),
		"Website":       {URL: " https://anita.onl "},
		"Company":       richTextProp(""),
		"OrgPhoto":      richTextProp(""),
		"AvailToHire":   {},
		"LookingToHire": {},
		"TShirt":        {},
		"Roles":         {},
	})

	if speaker.Github != "https://github.com/anita" {
		t.Fatalf("speaker.Github = %q, want trimmed URL", speaker.Github)
	}
	if speaker.Website != "https://anita.onl" {
		t.Fatalf("speaker.Website = %q, want trimmed URL", speaker.Website)
	}
	if speaker.LinkedIn != "linkedin.com/in/anita" {
		t.Fatalf("speaker.LinkedIn = %q, want trimmed value", speaker.LinkedIn)
	}
}

func TestParseConfTimezoneFromRichText(t *testing.T) {
	conf := parseConf("conf-id", map[string]notion.PropertyValue{
		"Name":           richTextProp("vienna"),
		"Active":         {},
		"Desc":           richTextProp("Vienna"),
		"OG_Flavor":      {},
		"Emoji":          {},
		"Tagline":        {},
		"DateDesc":       {},
		"Location":       {},
		"Venue":          {},
		"VenueMap":       {},
		"VenueWebsite":   {},
		"Show Hacks":     {},
		"Has Satellites": {},
		"OrientCalNotif": {},
		"StartDate": {
			Date: &notion.Date{Start: time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)},
		},
		"EndDate":  {},
		"Timezone": richTextProp("Europe/Vienna"),
	})

	if conf.Timezone != "Europe/Vienna" {
		t.Fatalf("conf.Timezone = %q, want Europe/Vienna", conf.Timezone)
	}
	if got := conf.Loc().String(); got != "Europe/Vienna" {
		t.Fatalf("conf.Loc = %q, want Europe/Vienna", got)
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
