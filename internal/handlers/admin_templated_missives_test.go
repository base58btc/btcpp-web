package handlers

import (
	"strings"
	"testing"

	"btcpp-web/internal/mtypes"
)

func TestFormFromTemplatedLetterHydratesShortcodes(t *testing.T) {
	letter := &mtypes.Letter{
		UID:         42,
		PageID:      "page-id",
		Title:       "Vienna dispatch",
		SendAt:      "6/12/2026",
		Newsletters: []string{"newsletter", "vienna"},
		Markdown: `---
template: "announce"
palette: "signal"
issue: "No. 9"
hero: "https://example.com/hero.jpg"
ticker:
  - Vienna | June 2026
  - Tickets | live
---

{{ lead "§ VIENNA" "Bitcoin++ Vienna" "A compact systems conference." }}

{{ stats "20 | talks + panels" "2 | days" "21M | coins" }}

{{ newsList "Talks | Deep protocol work | PROGRAM | https://example.com/program" "Tickets | Join us in Vienna | TICKETS | https://example.com/tickets" }}

Some freeform body copy.

{{ pullquote "Bring your weirdest debugging story." "The organizers" }}

{{ cta "JOIN US" "Get a ticket" "Seats are limited." "BUY TICKETS" "https://example.com/tickets" }}
`,
	}

	form := formFromTemplatedLetter(letter)

	if form.Template != "announce" {
		t.Fatalf("Template = %q, want announce", form.Template)
	}
	if form.Palette != "signal" {
		t.Fatalf("Palette = %q, want signal", form.Palette)
	}
	if form.Issue != "No. 9" {
		t.Fatalf("Issue = %q, want No. 9", form.Issue)
	}
	if form.Hero != "https://example.com/hero.jpg" {
		t.Fatalf("Hero = %q", form.Hero)
	}
	if form.Ticker != "Vienna | June 2026\nTickets | live" {
		t.Fatalf("Ticker = %q", form.Ticker)
	}
	if form.LeadEyebrow != "§ VIENNA" || form.LeadTitle != "Bitcoin++ Vienna" || form.LeadDeck != "A compact systems conference." {
		t.Fatalf("lead fields not hydrated: %#v", form)
	}
	if form.Stats != "20 | talks + panels\n2 | days\n21M | coins" {
		t.Fatalf("Stats = %q", form.Stats)
	}
	if !strings.Contains(form.NewsItems, "Talks | Deep protocol work | PROGRAM | https://example.com/program") {
		t.Fatalf("NewsItems = %q", form.NewsItems)
	}
	if form.Pullquote != "Bring your weirdest debugging story." || form.PullquoteBy != "The organizers" {
		t.Fatalf("pullquote fields not hydrated: %#v", form)
	}
	if form.CTAEyebrow != "JOIN US" || form.CTATitle != "Get a ticket" || form.CTASubtitle != "Seats are limited." || form.CTALabel != "BUY TICKETS" || form.CTAURL != "https://example.com/tickets" {
		t.Fatalf("CTA fields not hydrated: %#v", form)
	}
	if form.ContentMarkdown != "Some freeform body copy." {
		t.Fatalf("ContentMarkdown = %q", form.ContentMarkdown)
	}
	if strings.Contains(form.ContentMarkdown, "{{ stats") {
		t.Fatalf("ContentMarkdown still contains shortcode: %q", form.ContentMarkdown)
	}
}

func TestParseTemplatedShortcodeLineHandlesEscapes(t *testing.T) {
	name, args, ok := parseTemplatedShortcodeLine(`{{ pullquote "He said \"ship it\"" "Ops" }}`)
	if !ok {
		t.Fatal("parseTemplatedShortcodeLine did not parse shortcode")
	}
	if name != "pullquote" {
		t.Fatalf("name = %q, want pullquote", name)
	}
	if len(args) != 2 || args[0] != `He said "ship it"` || args[1] != "Ops" {
		t.Fatalf("args = %#v", args)
	}
}

func TestTemplatedMissiveTestLetterUsesCurrentFormWithoutSchedulingState(t *testing.T) {
	form := TemplatedMissiveForm{
		Title:       "Draft newsletter",
		SendAt:      "6/12/2026",
		Newsletters: "vienna, !newsletter",
		Template:    "announce",
		Palette:     "ember",
		LeadTitle:   "Current editor content",
		TestEmail:   "test@example.com",
	}

	letter := templatedMissiveTestLetter(form)
	if letter.Title != "[TEST] Draft newsletter" {
		t.Fatalf("Title = %q", letter.Title)
	}
	if letter.SendAt != "now" {
		t.Fatalf("SendAt = %q, want now", letter.SendAt)
	}
	if strings.Contains(letter.Markdown, `date:`) {
		t.Fatalf("test markdown should not include date frontmatter: %q", letter.Markdown)
	}
	if letter.OnlyFor != mtypes.OnlyForTemplated {
		t.Fatalf("OnlyFor = %q", letter.OnlyFor)
	}
	if !strings.Contains(letter.Markdown, "Current editor content") {
		t.Fatalf("Markdown did not use current form content: %q", letter.Markdown)
	}

	sub := subscriberForTemplatedMissiveTest("test@example.com", letter)
	if sub.Email != "test@example.com" {
		t.Fatalf("subscriber email = %q", sub.Email)
	}
	if got := strings.Join(sub.SubNames(), ","); got != "vienna" {
		t.Fatalf("subscriber lists = %q, want vienna", got)
	}
}

func TestBuildTemplatedMissiveMarkdownDoesNotWriteDateFrontmatter(t *testing.T) {
	markdown := buildTemplatedMissiveMarkdown(TemplatedMissiveForm{
		Title:           "No date",
		SendAt:          "5/25/2026",
		Newsletters:     "newsletter",
		Template:        "roundup",
		ContentMarkdown: "Body.",
	})
	if strings.Contains(markdown, "\ndate:") {
		t.Fatalf("templated missive markdown should not include date frontmatter: %q", markdown)
	}
}
