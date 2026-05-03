package imgproc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ShortID returns a 12-char hex content fingerprint (first 6 bytes of SHA-256).
// Same bytes always produce the same ID, so it doubles as a Spaces dedupe key:
// 48 bits of address space is plenty for our speaker photo volume.
func ShortID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:6])
}

const ffmpegTimeout = 60 * time.Second

func MakeAVIF(data []byte, size int) ([]byte, error) {
	in, err := os.CreateTemp("", "imgproc-in-*")
	if err != nil {
		return nil, fmt.Errorf("tempfile: %w", err)
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(data); err != nil {
		in.Close()
		return nil, fmt.Errorf("write input: %w", err)
	}
	in.Close()

	out, err := os.CreateTemp("", "imgproc-out-*.avif")
	if err != nil {
		return nil, fmt.Errorf("tempfile: %w", err)
	}
	outName := out.Name()
	out.Close()
	defer os.Remove(outName)

	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", in.Name(),
		"-vf", fmt.Sprintf("scale=%d:%d:flags=lanczos", size, size),
		"-c:v", "libaom-av1",
		"-still-picture", "1",
		"-cpu-used", "8",
		outName,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg %dx%d: %w (stderr: %s)", size, size, err, stderr.String())
	}
	return os.ReadFile(outName)
}
