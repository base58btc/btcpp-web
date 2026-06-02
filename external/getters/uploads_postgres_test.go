package getters

import "testing"

func TestPostgresUploadKeyUsesContentHashAndExtension(t *testing.T) {
	key := postgresUploadKey("image/jpeg", "headshot.JPG", []byte("same bytes"))

	if key != "uploads/58100dc8fc06.jpg" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestNormalizeUploadContentType(t *testing.T) {
	if got := normalizeUploadContentType(""); got != "application/octet-stream" {
		t.Fatalf("blank content type = %q", got)
	}
	if got := normalizeUploadContentType(" image/png "); got != "image/png" {
		t.Fatalf("trimmed content type = %q", got)
	}
}
