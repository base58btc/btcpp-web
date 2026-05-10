package ics

import (
	"fmt"
	"strings"
	"time"
)

// Method values for VCALENDAR.
const (
	MethodRequest = "REQUEST"
	MethodCancel  = "CANCEL"
	// MethodPublish is for public "add to calendar" downloads
	// where the recipient is opting in rather than receiving an
	// invitation. No ATTENDEE list (or empty) is expected; the
	// client treats the import as a one-way add.
	MethodPublish = "PUBLISH"
)

// Status values for VEVENT.
const (
	StatusConfirmed = "CONFIRMED"
	StatusCancelled = "CANCELLED"
)

// Attendee is one entry in the ICS ATTENDEE list. For the per-
// recipient invite pattern the dispatcher uses, each rendered ICS
// has exactly one Attendee — the person receiving this email.
type Attendee struct {
	Email string
	Name  string
}

// Event is the inputs for one VEVENT inside a VCALENDAR. UID and
// Sequence come from CalNotif; the rest is per-talk / per-shift
// content. End is required (no all-day support); TZ may be nil
// (renders in UTC with Z-suffix and no VTIMEZONE block).
type Event struct {
	Method string // MethodRequest or MethodCancel

	// Per-event metadata.
	UID      string
	Sequence int
	Status   string // StatusConfirmed or StatusCancelled

	Summary     string
	Description string
	Location    string

	Start time.Time
	End   time.Time
	TZ    *time.Location

	// Organizer is the mailbox shown as the meeting organizer in
	// the recipient's calendar. RFC-5545 metadata; doesn't affect
	// the email envelope-From.
	Organizer     string // bare email address (no scheme)
	OrganizerName string // optional CN

	Attendees []Attendee
}

// Render produces the VCALENDAR document. Output is CRLF-terminated
// and 75-octet line-folded per RFC-5545 §3.1; safe to drop straight
// into a Content-Type: text/calendar attachment.
func Render(e Event) []byte {
	var b strings.Builder

	method := e.Method
	if method == "" {
		method = MethodRequest
	}
	status := e.Status
	if status == "" {
		if method == MethodCancel {
			status = StatusCancelled
		} else {
			status = StatusConfirmed
		}
	}

	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:-//btcpp.dev//conf//EN")
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:"+method)

	tzID := ""
	if e.TZ != nil {
		tzID = e.TZ.String()
		writeVTimezone(&b, e.TZ, e.Start)
	}

	writeLine(&b, "BEGIN:VEVENT")
	writeLine(&b, "UID:"+e.UID)
	writeLine(&b, "DTSTAMP:"+formatUTC(time.Now()))
	if tzID != "" {
		writeLine(&b, "DTSTART;TZID="+tzID+":"+formatLocal(e.Start, e.TZ))
		writeLine(&b, "DTEND;TZID="+tzID+":"+formatLocal(e.End, e.TZ))
	} else {
		writeLine(&b, "DTSTART:"+formatUTC(e.Start))
		writeLine(&b, "DTEND:"+formatUTC(e.End))
	}
	writeLine(&b, "SEQUENCE:"+itoa(e.Sequence))
	writeLine(&b, "STATUS:"+status)

	if e.Summary != "" {
		writeLine(&b, "SUMMARY:"+escapeText(e.Summary))
	}
	if e.Description != "" {
		writeLine(&b, "DESCRIPTION:"+escapeText(e.Description))
	}
	if e.Location != "" {
		writeLine(&b, "LOCATION:"+escapeText(e.Location))
	}
	if e.Organizer != "" {
		if e.OrganizerName != "" {
			writeLine(&b, "ORGANIZER;CN="+escapeText(e.OrganizerName)+":mailto:"+e.Organizer)
		} else {
			writeLine(&b, "ORGANIZER:mailto:"+e.Organizer)
		}
	}
	for _, a := range e.Attendees {
		params := "ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE"
		if a.Name != "" {
			params = "CN=" + escapeText(a.Name) + ";" + params
		}
		writeLine(&b, "ATTENDEE;"+params+":mailto:"+a.Email)
	}

	writeLine(&b, "END:VEVENT")
	writeLine(&b, "END:VCALENDAR")

	return []byte(b.String())
}

// writeVTimezone emits a minimal STANDARD-only VTIMEZONE block. We
// don't model DST transitions: confs run ≤ 4 days, well inside a
// single offset window, so TZOFFSETFROM == TZOFFSETTO derived from
// the anchor moment's offset is accurate. A multi-day conf that
// straddled a DST boundary would silently render the post-boundary
// half off-by-an-hour; flagged in the dispatch layer where we have
// access to start AND end and can refuse outright.
func writeVTimezone(b *strings.Builder, loc *time.Location, anchor time.Time) {
	_, offsetSecs := anchor.In(loc).Zone()
	off := formatTZOffset(offsetSecs)
	writeLine(b, "BEGIN:VTIMEZONE")
	writeLine(b, "TZID:"+loc.String())
	writeLine(b, "BEGIN:STANDARD")
	writeLine(b, "DTSTART:19700101T000000")
	writeLine(b, "TZOFFSETFROM:"+off)
	writeLine(b, "TZOFFSETTO:"+off)
	writeLine(b, "END:STANDARD")
	writeLine(b, "END:VTIMEZONE")
}

// formatTZOffset turns +/- seconds into the RFC-5545 +HHMM / -HHMM
// shape (e.g. -0500, +0100, +0930).
func formatTZOffset(secs int) string {
	sign := "+"
	if secs < 0 {
		sign = "-"
		secs = -secs
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

// formatUTC renders a time as an RFC-5545 UTC stamp ("Z"-suffixed).
// Used for DTSTAMP and for the no-TZ fallback path.
func formatUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// formatLocal renders a time in the given location as the RFC-5545
// "floating local" shape used alongside TZID=...
func formatLocal(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("20060102T150405")
}

// escapeText applies the RFC-5545 §3.3.11 TEXT escape: backslash,
// semicolon, comma, and newline get backslash-prefixed (newlines
// become the two-char sequence `\n`). Used for SUMMARY,
// DESCRIPTION, LOCATION, and any CN= parameter value.
func escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			// Drop bare CR — typically paired with LF and the LF
			// already produced an escape; standalone CR isn't a
			// thing in RFC-5545 TEXT.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeLine appends a content line, fold-wrapped at 75 octets per
// RFC-5545 §3.1, terminated with CRLF. Folded continuation lines
// start with a single space, and that space counts toward the 75-
// octet budget — so chunks after the first cap at 74 content bytes.
func writeLine(b *strings.Builder, line string) {
	const firstMax = 75
	const contMax = 74 // 75 - 1 for the leading space
	first := true
	for len(line) > 0 {
		max := firstMax
		if !first {
			max = contMax
		}
		if len(line) <= max {
			if !first {
				b.WriteString(" ")
			}
			b.WriteString(line)
			b.WriteString("\r\n")
			return
		}
		// Step back so we don't split a UTF-8 multi-byte sequence.
		split := max
		for split > 0 && isUTF8Cont(line[split]) {
			split--
		}
		if !first {
			b.WriteString(" ")
		}
		b.WriteString(line[:split])
		b.WriteString("\r\n")
		line = line[split:]
		first = false
	}
}

// isUTF8Cont reports whether b is a UTF-8 continuation byte
// (10xxxxxx).
func isUTF8Cont(b byte) bool {
	return b&0xC0 == 0x80
}

// itoa is a tiny strconv.Itoa shim so this file doesn't have to
// import strconv on top of the rest. Inlined for clarity.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
