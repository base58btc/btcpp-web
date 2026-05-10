package ics

import (
	"testing"
	"time"
)

func TestCalNotifRoundTrip(t *testing.T) {
	c := CalNotif{
		UID:      "talk-abc123@btcpp.dev",
		Sequence: 7,
		HashHex:  "deadbeef",
	}
	got, ok := ParseCalNotif(c.String())
	if !ok {
		t.Fatalf("round trip parse failed for %q", c.String())
	}
	if got != c {
		t.Errorf("round trip mismatch: got %+v want %+v", got, c)
	}
}

func TestParseCalNotifRejects(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"plain-google-event-id-with-no-colons",
		"only-one:colon",
		"too:many:colons:here:fail",
		"talk@btcpp.dev:notanint:deadbeef",
		"talk@btcpp.dev:-1:deadbeef",
		"talk@btcpp.dev:1:short",
		"talk@btcpp.dev:1:nothex!!",
	}
	for _, in := range cases {
		if _, ok := ParseCalNotif(in); ok {
			t.Errorf("expected reject for %q", in)
		}
	}
}

func TestParseCalNotifAccepts(t *testing.T) {
	cases := []string{
		"talk-abc@btcpp.dev:0:00000000",
		"shift-xyz@btcpp.dev:42:DeadBeef",
		"short:1:cafef00d",
	}
	for _, in := range cases {
		if _, ok := ParseCalNotif(in); !ok {
			t.Errorf("expected accept for %q", in)
		}
	}
}

func TestNewUID(t *testing.T) {
	if got := NewUID("talk", "abc123"); got != "talk-abc123@btcpp.dev" {
		t.Errorf("NewUID talk: got %q", got)
	}
	if got := NewUID("shift", "xyz"); got != "shift-xyz@btcpp.dev" {
		t.Errorf("NewUID shift: got %q", got)
	}
}

func TestContentHashStability(t *testing.T) {
	start := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)

	h1 := ContentHash(start, end, "vienna", "Why Bitcoin++")
	h2 := ContentHash(start, end, "vienna", "Why Bitcoin++")
	if h1 != h2 {
		t.Errorf("hash not stable: %q vs %q", h1, h2)
	}
	if len(h1) != 8 {
		t.Errorf("hash should be 8 hex chars, got %d (%q)", len(h1), h1)
	}
}

func TestContentHashChanges(t *testing.T) {
	start := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)
	base := ContentHash(start, end, "vienna", "Why Bitcoin++")

	// Different start time → different hash.
	if got := ContentHash(start.Add(15*time.Minute), end, "vienna", "Why Bitcoin++"); got == base {
		t.Errorf("expected hash change on start shift, both %q", base)
	}
	// Different end time → different hash (talk-length resize).
	if got := ContentHash(start, end.Add(15*time.Minute), "vienna", "Why Bitcoin++"); got == base {
		t.Errorf("expected hash change on end shift, both %q", base)
	}
	// Different conf tag → different hash.
	if got := ContentHash(start, end, "berlin26", "Why Bitcoin++"); got == base {
		t.Errorf("expected hash change on conf swap")
	}
	// Different title → different hash.
	if got := ContentHash(start, end, "vienna", "Why Bitcoin++ Matters"); got == base {
		t.Errorf("expected hash change on title edit")
	}
}

func TestContentHashTimezoneInvariant(t *testing.T) {
	// 9:00 in NYC == 14:00 UTC (winter). Same instant should hash
	// identically regardless of how the caller carried the timezone,
	// because we normalize to UTC before hashing.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("LoadLocation failed: %s", err)
	}
	startUTC := time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)
	startLocal := time.Date(2026, 1, 10, 9, 0, 0, 0, loc)
	if !startUTC.Equal(startLocal) {
		t.Fatalf("test setup error: %s vs %s", startUTC, startLocal)
	}
	end := startUTC.Add(30 * time.Minute)

	h1 := ContentHash(startUTC, end, "atx25", "Talk")
	h2 := ContentHash(startLocal, end, "atx25", "Talk")
	if h1 != h2 {
		t.Errorf("expected hash invariant under TZ representation: %q vs %q", h1, h2)
	}
}

func TestNextSeqFirstSend(t *testing.T) {
	seq, send := NextSeq(CalNotif{}, false, "deadbeef", false)
	if seq != 0 || !send {
		t.Errorf("first send: got seq=%d send=%v want 0/true", seq, send)
	}
}

func TestNextSeqUnchangedSkips(t *testing.T) {
	prev := CalNotif{UID: "x", Sequence: 4, HashHex: "deadbeef"}
	seq, send := NextSeq(prev, true, "deadbeef", false)
	if send {
		t.Errorf("unchanged hash should skip; got send=true seq=%d", seq)
	}
	if seq != 4 {
		t.Errorf("seq should be left at prev when skipping; got %d", seq)
	}
}

func TestNextSeqUnchangedForceBumps(t *testing.T) {
	prev := CalNotif{UID: "x", Sequence: 4, HashHex: "deadbeef"}
	seq, send := NextSeq(prev, true, "deadbeef", true)
	if !send {
		t.Errorf("force=true should send")
	}
	if seq != 5 {
		t.Errorf("force bump should increment, got %d", seq)
	}
}

func TestNextSeqChangedBumps(t *testing.T) {
	prev := CalNotif{UID: "x", Sequence: 4, HashHex: "deadbeef"}
	seq, send := NextSeq(prev, true, "cafef00d", false)
	if !send || seq != 5 {
		t.Errorf("hash change should send + bump; got seq=%d send=%v", seq, send)
	}
}
