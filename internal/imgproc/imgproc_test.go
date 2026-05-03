package imgproc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"testing"
)

func makeTestJPEG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encoding test jpeg: %v", err)
	}
	return buf.Bytes()
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not in PATH; skipping AVIF roundtrip test")
	}
}

func TestMakeAVIF_RoundTrip(t *testing.T) {
	requireFFmpeg(t)

	in := makeTestJPEG(t, 1024)

	for _, size := range []int{800, 400} {
		size := size
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			out, err := MakeAVIF(in, size)
			if err != nil {
				t.Fatalf("MakeAVIF(%d): %v", size, err)
			}
			if len(out) < 100 {
				t.Errorf("output suspiciously small: %d bytes", len(out))
			}
			// ISO BMFF: first 4 bytes are box length, then "ftypavif"
			if len(out) < 12 || !bytes.Equal(out[4:12], []byte("ftypavif")) {
				t.Errorf("output is not AVIF; header=% x", out[:min(16, len(out))])
			}
		})
	}
}

func TestMakeAVIF_BadInput(t *testing.T) {
	requireFFmpeg(t)

	_, err := MakeAVIF([]byte("definitely not an image"), 400)
	if err == nil {
		t.Fatal("expected error for non-image input, got nil")
	}
}

func TestMakeAVIF_EmptyInput(t *testing.T) {
	requireFFmpeg(t)

	_, err := MakeAVIF(nil, 400)
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestShortID_Deterministic(t *testing.T) {
	a := []byte("hello world")
	if got, want := ShortID(a), ShortID(a); got != want {
		t.Errorf("ShortID is not deterministic: %s vs %s", got, want)
	}
}

func TestShortID_DifferentInputs(t *testing.T) {
	if ShortID([]byte("foo")) == ShortID([]byte("bar")) {
		t.Error("distinct inputs collided to the same ShortID")
	}
}

func TestShortID_Format(t *testing.T) {
	id := ShortID([]byte("anything"))
	if len(id) != 12 {
		t.Errorf("expected 12-char hex; got %d-char %q", len(id), id)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in ShortID %q", c, id)
		}
	}
}
