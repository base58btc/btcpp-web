package handlers

import (
	"fmt"

	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/imgproc"
)

// photoSpaces is the subset of the spaces package the photo pipeline needs.
// Production wraps the package-level functions; tests pass a fake.
type photoSpaces interface {
	IsConfigured() bool
	Exists(key string) bool
	Upload(key string, data []byte, contentType, hash string) (string, error)
}

type spacesPkgAdapter struct{}

func (spacesPkgAdapter) IsConfigured() bool                                                { return spaces.IsConfigured() }
func (spacesPkgAdapter) Exists(key string) bool                                            { return spaces.Exists(key) }
func (spacesPkgAdapter) Upload(key string, data []byte, ct, hash string) (string, error) { return spaces.Upload(key, data, ct, hash) }

// photoPipeline carries the side-effecting collaborators used by the photo
// upload methods. Production code calls newPhotoPipeline; tests construct
// the struct directly with fakes.
type photoPipeline struct {
	spaces   photoSpaces
	makeAVIF func(data []byte, size int) ([]byte, error)
	logf     func(format string, args ...interface{})
}

func newPhotoPipeline(ctx *config.AppContext) photoPipeline {
	return photoPipeline{
		spaces:   spacesPkgAdapter{},
		makeAVIF: imgproc.MakeAVIF,
		logf:     ctx.Err.Printf,
	}
}

// mirrorPicToSpaces uploads the original cropped speaker photo plus 800px
// and 400px AVIF derivatives. The Spaces key is a content-derived short ID
// so identical photos dedupe across applicants. Layout:
//
//	speakers/{shortID}{ext}        (original)
//	speakers/{shortID}-800.avif    (display size)
//	speakers/{shortID}-400.avif    (thumbnail — Speaker.Photo points here)
//
// Designed to be called from a goroutine; failures are logged and do not
// abort. Each upload is gated by spaces.Exists so re-runs are idempotent.
func (p photoPipeline) mirrorPicToSpaces(raw []byte, contentType, ext string) {
	if !p.spaces.IsConfigured() {
		return
	}

	shortID := imgproc.ShortID(raw)
	normPhoto := shortID + ext
	origKey := "speakers/" + normPhoto

	if !p.spaces.Exists(origKey) {
		if _, err := p.spaces.Upload(origKey, raw, contentType, ""); err != nil {
			p.logf("speaker pic spaces orig upload failed (%s): %s", shortID, err)
			return
		}
	}

	for _, size := range []int{800, 400} {
		key := fmt.Sprintf("speakers/%s-%d.avif", shortID, size)
		if p.spaces.Exists(key) {
			continue
		}
		avif, err := p.makeAVIF(raw, size)
		if err != nil {
			p.logf("speaker pic avif%d generation failed (%s): %s", size, shortID, err)
			continue
		}
		if _, err := p.spaces.Upload(key, avif, "image/avif", ""); err != nil {
			p.logf("speaker pic spaces avif%d upload failed (%s): %s", size, shortID, err)
		}
	}
}

// mirrorOrgLogoToSpaces uploads the org-logo as-is (no crop, no resize, no
// AVIF derivatives) under sponsors/{shortID}{ext}. Idempotent via
// spaces.Exists.
func (p photoPipeline) mirrorOrgLogoToSpaces(raw []byte, contentType, ext string) {
	if !p.spaces.IsConfigured() {
		return
	}
	shortID := imgproc.ShortID(raw)
	key := "sponsors/" + shortID + ext
	if p.spaces.Exists(key) {
		return
	}
	if _, err := p.spaces.Upload(key, raw, contentType, ""); err != nil {
		p.logf("org logo spaces upload failed (%s): %s", shortID, err)
	}
}
