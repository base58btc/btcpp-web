package buffer

import (
	"strings"
	"testing"
)

func TestBuildAssetsBlockUsesAssetInputList(t *testing.T) {
	got := buildAssetsBlock([]string{
		"https://cdn.example.com/card.png",
		"https://cdn.example.com/speaker.png",
	})

	if strings.Contains(got, "images") {
		t.Fatalf("assets block used removed Buffer images field: %s", got)
	}
	want := `, assets: [{ image: { url: "https://cdn.example.com/card.png" } }, { image: { url: "https://cdn.example.com/speaker.png" } }]`
	if got != want {
		t.Fatalf("assets block mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildAssetsBlockEmpty(t *testing.T) {
	if got := buildAssetsBlock(nil); got != "" {
		t.Fatalf("empty assets block = %q, want empty", got)
	}
}
