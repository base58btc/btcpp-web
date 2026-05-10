package ics

import (
	"strings"
	"testing"
	"time"
)

func mustLoadLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("LoadLocation %s failed (system tzdata missing?): %s", name, err)
	}
	return loc
}

// unfold reverses RFC-5545 line folding so substring tests can
// match against the logical content lines without worrying about
// where the renderer chose to wrap.
func unfold(s string) string {
	return strings.ReplaceAll(s, "\r\n ", "")
}

func TestRenderRequestStructure(t *testing.T) {
	loc := mustLoadLoc(t, "Europe/Vienna")
	start := time.Date(2026, 5, 10, 9, 0, 0, 0, loc)
	end := start.Add(30 * time.Minute)

	rendered := string(Render(Event{
		Method:        MethodRequest,
		UID:           "talk-abc@btcpp.dev",
		Sequence:      0,
		Summary:       "speak @ btc++: Why Bitcoin++",
		Description:   "Some talk description.",
		Location:      "Main Stage",
		Start:         start,
		End:           end,
		TZ:            loc,
		Organizer:     "speak@btcpp.dev",
		OrganizerName: "bitcoin++ speakers",
		Attendees: []Attendee{
			{Email: "alice@example.com", Name: "Alice"},
		},
	}))
	out := unfold(rendered)

	mustContain := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//btcpp.dev//conf//EN",
		"METHOD:REQUEST",
		"BEGIN:VTIMEZONE",
		"TZID:Europe/Vienna",
		"END:VTIMEZONE",
		"BEGIN:VEVENT",
		"UID:talk-abc@btcpp.dev",
		"DTSTART;TZID=Europe/Vienna:20260510T090000",
		"DTEND;TZID=Europe/Vienna:20260510T093000",
		"SEQUENCE:0",
		"STATUS:CONFIRMED",
		"SUMMARY:speak @ btc++: Why Bitcoin++",
		"LOCATION:Main Stage",
		"ORGANIZER;CN=bitcoin++ speakers:mailto:speak@btcpp.dev",
		"ATTENDEE;CN=Alice;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:alice@example.com",
		"END:VEVENT",
		"END:VCALENDAR",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("Render() missing %q in unfolded output\n%s", m, rendered)
		}
	}

	// CRLF line endings (check on the raw, not unfolded).
	if !strings.Contains(rendered, "\r\n") {
		t.Errorf("expected CRLF line endings in output")
	}
}

func TestRenderCancelStatus(t *testing.T) {
	loc := mustLoadLoc(t, "America/New_York")
	start := time.Date(2026, 1, 10, 9, 0, 0, 0, loc)

	out := string(Render(Event{
		Method:    MethodCancel,
		UID:       "talk-abc@btcpp.dev",
		Sequence:  3,
		Summary:   "speak @ btc++: Talk",
		Start:     start,
		End:       start.Add(30 * time.Minute),
		TZ:        loc,
		Organizer: "speak@btcpp.dev",
		Attendees: []Attendee{{Email: "alice@example.com"}},
	}))
	if !strings.Contains(out, "METHOD:CANCEL") {
		t.Errorf("missing METHOD:CANCEL")
	}
	if !strings.Contains(out, "STATUS:CANCELLED") {
		t.Errorf("missing STATUS:CANCELLED")
	}
	if !strings.Contains(out, "SEQUENCE:3") {
		t.Errorf("missing SEQUENCE:3")
	}
}

func TestRenderEscapesText(t *testing.T) {
	out := string(Render(Event{
		Method:    MethodRequest,
		UID:       "talk-abc@btcpp.dev",
		Summary:   "weird; comma, backslash\\ and newline\nhere",
		Start:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		Attendees: []Attendee{{Email: "x@y.z"}},
	}))
	wantSummary := `SUMMARY:weird\; comma\, backslash\\ and newline\nhere`
	if !strings.Contains(out, wantSummary) {
		t.Errorf("expected escaped summary %q\n%s", wantSummary, out)
	}
}

func TestRenderNoTZUsesUTC(t *testing.T) {
	out := string(Render(Event{
		Method:    MethodRequest,
		UID:       "talk-abc@btcpp.dev",
		Start:     time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC),
		Attendees: []Attendee{{Email: "x@y.z"}},
	}))
	if strings.Contains(out, "BEGIN:VTIMEZONE") {
		t.Errorf("expected no VTIMEZONE block when TZ nil\n%s", out)
	}
	if !strings.Contains(out, "DTSTART:20260101T140000Z") {
		t.Errorf("expected UTC-style DTSTART")
	}
}

func TestRenderLineFolding(t *testing.T) {
	long := strings.Repeat("a", 200)
	out := string(Render(Event{
		Method:    MethodRequest,
		UID:       "talk-abc@btcpp.dev",
		Summary:   long,
		Start:     time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC),
		Attendees: []Attendee{{Email: "x@y.z"}},
	}))
	// Folded continuation lines start with " " after a CRLF. Check
	// that no single line in the output exceeds 75 octets.
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line exceeds 75 octets (%d): %q", len(line), line)
		}
	}
}

func TestMapVenue(t *testing.T) {
	cases := map[string]string{
		"one":      "Main Stage",
		"two":      "Talks Stage",
		"three":    "Workshops Stage",
		"  ONE  ":  "Main Stage",
		"":         "",
		"unknown":  "",
		"main":     "",
	}
	for in, want := range cases {
		if got := MapVenue(in); got != want {
			t.Errorf("MapVenue(%q): got %q want %q", in, got, want)
		}
	}
}

func TestFormatTZOffset(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, "+0000"},
		{3600, "+0100"},
		{-18000, "-0500"},
		{34200, "+0930"},
	}
	for _, c := range cases {
		if got := formatTZOffset(c.secs); got != c.want {
			t.Errorf("formatTZOffset(%d): got %q want %q", c.secs, got, c.want)
		}
	}
}
