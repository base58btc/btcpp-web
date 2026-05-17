package xposter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveExtractDirRoundTripSkipsChromeLocks(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Default"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Default", "Cookies"), []byte("cookie-db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SingletonLock"), []byte("lock"), 0600); err != nil {
		t.Fatal(err)
	}

	raw, err := archiveDir(src)
	if err != nil {
		t.Fatalf("archiveDir: %v", err)
	}
	dst := t.TempDir()
	if err := extractDir(raw, dst); err != nil {
		t.Fatalf("extractDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "Default", "Cookies"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "cookie-db" {
		t.Fatalf("extracted file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "SingletonLock")); !os.IsNotExist(err) {
		t.Fatalf("SingletonLock should not be archived, stat err=%v", err)
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	if _, err := safeJoin(t.TempDir(), "../outside"); err == nil {
		t.Fatalf("safeJoin accepted traversal path")
	}
}

func TestNewXScheduleFieldsFormatsLocalTime(t *testing.T) {
	loc := time.FixedZone("test", -5*60*60)
	got := newXScheduleFields(time.Date(2026, time.May, 17, 0, 4, 0, 0, loc))

	if got.Month != "May" || got.MonthShort != "May" || got.Day != "17" || got.Year != "2026" {
		t.Fatalf("date fields = %#v", got)
	}
	if got.Hour != "12" || got.Minute != "04" || got.Period != "AM" {
		t.Fatalf("time fields = %#v", got)
	}

	got = newXScheduleFields(time.Date(2026, time.May, 17, 15, 30, 0, 0, loc))
	if got.Hour != "3" || got.Minute != "30" || got.Period != "PM" {
		t.Fatalf("afternoon time fields = %#v", got)
	}
}
