// Package ics generates RFC-5545 calendar invitations and decides
// when to (re-)send them. The package is intentionally I/O-free —
// callers (typically the email dispatch in handlers) feed it domain
// data, get back ICS bytes plus an updated CalNotif string to stamp
// back to Notion.
//
// The CalNotif string is the persisted state-of-the-invite: a
// `"UID:Sequence:Hashbytes"` triple stored in the Notion rich_text
// column on ConfTalk / WorkShift. UID is the RFC-5545 unique
// identifier (stable per row), Sequence is the RFC-5545 SEQUENCE
// counter (monotonically advances), Hashbytes is a 4-byte content
// fingerprint we use to decide whether anything material changed
// since the last send.
package ics

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// CalNotif is the parsed shape of the CalNotif rich_text column on
// ConfTalk / WorkShift rows in Notion.
type CalNotif struct {
	UID      string
	Sequence int
	HashHex  string
}

// String serializes back to the wire format ("UID:Sequence:Hash").
func (c CalNotif) String() string {
	return c.UID + ":" + strconv.Itoa(c.Sequence) + ":" + c.HashHex
}

// ParseCalNotif decodes the wire format. The bool is `false` when
// the input is empty OR malformed — callers should treat that as
// "no prior send" and proceed as a fresh first invite. Bad data on
// existing rows (legacy Google event IDs from the previous
// implementation, blank cells) parses cleanly to (zero-CalNotif,
// false) so the cutover is silent.
func ParseCalNotif(s string) (CalNotif, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return CalNotif{}, false
	}
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return CalNotif{}, false
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil || seq < 0 {
		return CalNotif{}, false
	}
	if !looksLikeHash(parts[2]) {
		return CalNotif{}, false
	}
	return CalNotif{UID: parts[0], Sequence: seq, HashHex: parts[2]}, true
}

// looksLikeHash returns true when s is exactly 8 hex characters —
// the format produced by ContentHash. Used by ParseCalNotif to fail
// closed on legacy / malformed data.
func looksLikeHash(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// NewUID builds a stable RFC-5545 UID for a calendar entity.
//
//	kind = "talk"   → "talk-<notion-id>@btcpp.dev"  (ConfTalk)
//	kind = "shift"  → "shift-<notion-id>@btcpp.dev" (WorkShift)
//	kind = "orient" → "orient-<conf-tag>-<vol-email>@btcpp.dev" (orientation)
//
// The UID must not change across re-sends: it's how recipient
// calendars match an UPDATE / CANCEL to the original REQUEST.
func NewUID(kind, id string) string {
	return kind + "-" + id + "@btcpp.dev"
}

// ContentHash returns the 8-hex-char fingerprint used by the
// Sequence-bump rule. We hash start time + end time + conf tag +
// title; the start/end times are normalized to UTC so a daylight-
// savings-aware re-render of the same wall-clock moment doesn't
// look like a change. End time is included so a talk-length
// resize triggers a re-send.
func ContentHash(start, end time.Time, confTag, title string) string {
	h := sha256.New()
	h.Write([]byte(start.UTC().Format(time.RFC3339)))
	h.Write([]byte("|"))
	h.Write([]byte(end.UTC().Format(time.RFC3339)))
	h.Write([]byte("|"))
	h.Write([]byte(confTag))
	h.Write([]byte("|"))
	h.Write([]byte(title))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:4])
}

// NextSeq applies the Sequence-bump rule given the prior CalNotif
// state and the freshly-computed content hash. Returns the new
// Sequence and a flag telling the caller whether the send should
// actually go out (false = nothing changed; skip the email).
//
// `force=true` overrides the unchanged-content skip. Used for paths
// where the attendee set changed without title / time changing —
// adding a co-speaker, for example, is RFC-5545-significant but the
// hash inputs we picked don't include attendees.
func NextSeq(prev CalNotif, prevValid bool, newHash string, force bool) (seq int, send bool) {
	if !prevValid {
		// First send (empty / unparseable CalNotif).
		return 0, true
	}
	if prev.HashHex == newHash && !force {
		// Idempotent re-render: same state, no email.
		return prev.Sequence, false
	}
	// Either content changed, or caller forced a bump.
	return prev.Sequence + 1, true
}
