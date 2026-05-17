package xposter

import (
	"os"
	"path/filepath"
	"testing"
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
