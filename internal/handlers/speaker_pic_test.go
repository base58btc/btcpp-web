package handlers

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"btcpp-web/internal/imgproc"
)

type fakeSpaces struct {
	mu         sync.Mutex
	configured bool
	existing   map[string]bool
	uploads    map[string][]byte
	uploadErr  map[string]error
}

func newFakeSpaces() *fakeSpaces {
	return &fakeSpaces{
		configured: true,
		existing:   map[string]bool{},
		uploads:    map[string][]byte{},
		uploadErr:  map[string]error{},
	}
}

func (f *fakeSpaces) IsConfigured() bool { return f.configured }

func (f *fakeSpaces) Exists(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existing[key]
}

func (f *fakeSpaces) Upload(key string, data []byte, _ string, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.uploadErr[key]; err != nil {
		return "", err
	}
	f.uploads[key] = data
	f.existing[key] = true
	return "https://fake/" + key, nil
}

type pipelineRecorder struct {
	avifSizes []int
	avifErr   map[int]error
}

func newRecordingPipeline(t *testing.T, sp *fakeSpaces) (photoPipeline, *pipelineRecorder) {
	t.Helper()
	rec := &pipelineRecorder{
		avifErr: map[int]error{},
	}
	p := photoPipeline{
		spaces: sp,
		makeAVIF: func(_ []byte, size int) ([]byte, error) {
			rec.avifSizes = append(rec.avifSizes, size)
			if err := rec.avifErr[size]; err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf("avif-%d", size)), nil
		},
		logf: t.Logf,
	}
	return p, rec
}

func TestMirrorPicToSpaces_NotConfigured(t *testing.T) {
	sp := newFakeSpaces()
	sp.configured = false
	p, rec := newRecordingPipeline(t, sp)

	p.mirrorPicToSpaces([]byte("raw"), "image/jpeg", ".jpg")

	if len(sp.uploads) != 0 {
		t.Errorf("expected no uploads when not configured, got %d", len(sp.uploads))
	}
	if len(rec.avifSizes) != 0 {
		t.Errorf("expected no AVIF encodes; got %v", rec.avifSizes)
	}
}

func TestMirrorPicToSpaces_FreshUpload(t *testing.T) {
	sp := newFakeSpaces()
	p, rec := newRecordingPipeline(t, sp)
	raw := []byte("photo bytes")
	shortID := imgproc.ShortID(raw)

	p.mirrorPicToSpaces(raw, "image/jpeg", ".jpg")

	wantKeys := []string{
		"speakers/" + shortID + ".jpg",
		"speakers/" + shortID + "-800.avif",
		"speakers/" + shortID + "-400.avif",
	}
	for _, k := range wantKeys {
		if _, ok := sp.uploads[k]; !ok {
			t.Errorf("missing upload %q; got keys: %v", k, mapKeys(sp.uploads))
		}
	}
	if want := []int{800, 400}; !intsEqual(rec.avifSizes, want) {
		t.Errorf("avif sizes: got %v, want %v", rec.avifSizes, want)
	}
}

func TestMirrorPicToSpaces_FullDedupe(t *testing.T) {
	sp := newFakeSpaces()
	raw := []byte("photo bytes")
	shortID := imgproc.ShortID(raw)
	for _, k := range []string{
		"speakers/" + shortID + ".jpg",
		"speakers/" + shortID + "-800.avif",
		"speakers/" + shortID + "-400.avif",
	} {
		sp.existing[k] = true
	}

	p, rec := newRecordingPipeline(t, sp)
	p.mirrorPicToSpaces(raw, "image/jpeg", ".jpg")

	if len(sp.uploads) != 0 {
		t.Errorf("expected no re-uploads when all exist; got %v", mapKeys(sp.uploads))
	}
	if len(rec.avifSizes) != 0 {
		t.Errorf("expected no ffmpeg work when derivatives exist; got %v", rec.avifSizes)
	}
}

func TestMirrorPicToSpaces_OrigUploadFails(t *testing.T) {
	sp := newFakeSpaces()
	raw := []byte("photo")
	shortID := imgproc.ShortID(raw)
	sp.uploadErr["speakers/"+shortID+".jpg"] = errors.New("network down")

	p, rec := newRecordingPipeline(t, sp)
	p.mirrorPicToSpaces(raw, "image/jpeg", ".jpg")

	if len(rec.avifSizes) != 0 {
		t.Errorf("AVIF encoding should be skipped when orig upload fails; got %v", rec.avifSizes)
	}
}

func TestMirrorPicToSpaces_AVIFEncodeFailureContinues(t *testing.T) {
	sp := newFakeSpaces()
	p, rec := newRecordingPipeline(t, sp)
	rec.avifErr[800] = errors.New("ffmpeg crashed")
	raw := []byte("photo")
	shortID := imgproc.ShortID(raw)

	p.mirrorPicToSpaces(raw, "image/jpeg", ".jpg")

	if _, ok := sp.uploads["speakers/"+shortID+".jpg"]; !ok {
		t.Error("orig should still upload")
	}
	if _, ok := sp.uploads["speakers/"+shortID+"-800.avif"]; ok {
		t.Error("800 must not upload after encode failure")
	}
	if _, ok := sp.uploads["speakers/"+shortID+"-400.avif"]; !ok {
		t.Error("400 should still upload despite 800 failure")
	}
}

func TestMirrorPicToSpaces_DerivativeExists_SkipsEncode(t *testing.T) {
	sp := newFakeSpaces()
	raw := []byte("photo")
	shortID := imgproc.ShortID(raw)
	sp.existing["speakers/"+shortID+"-800.avif"] = true

	p, rec := newRecordingPipeline(t, sp)
	p.mirrorPicToSpaces(raw, "image/jpeg", ".jpg")

	if want := []int{400}; !intsEqual(rec.avifSizes, want) {
		t.Errorf("expected only 400 encode (800 already exists); got %v", rec.avifSizes)
	}
}

func TestMirrorOrgLogoToSpaces_NotConfigured(t *testing.T) {
	sp := newFakeSpaces()
	sp.configured = false
	p, _ := newRecordingPipeline(t, sp)
	p.mirrorOrgLogoToSpaces([]byte("logo"), "image/png", ".png")
	if len(sp.uploads) != 0 {
		t.Errorf("expected no uploads when not configured; got %d", len(sp.uploads))
	}
}

func TestMirrorOrgLogoToSpaces_FreshUpload(t *testing.T) {
	sp := newFakeSpaces()
	p, rec := newRecordingPipeline(t, sp)
	raw := []byte("logo bytes")
	shortID := imgproc.ShortID(raw)
	wantKey := "sponsors/" + shortID + ".png"

	p.mirrorOrgLogoToSpaces(raw, "image/png", ".png")

	if _, ok := sp.uploads[wantKey]; !ok {
		t.Errorf("expected upload to %q; got keys %v", wantKey, mapKeys(sp.uploads))
	}
	if len(sp.uploads) != 1 {
		t.Errorf("expected exactly 1 upload (no AVIF derivatives); got %d", len(sp.uploads))
	}
	if len(rec.avifSizes) != 0 {
		t.Errorf("org logo path must not call AVIF encoder; got %v", rec.avifSizes)
	}
}

func TestMirrorOrgLogoToSpaces_DedupeOnExists(t *testing.T) {
	sp := newFakeSpaces()
	raw := []byte("logo bytes")
	shortID := imgproc.ShortID(raw)
	sp.existing["sponsors/"+shortID+".png"] = true

	p, _ := newRecordingPipeline(t, sp)
	p.mirrorOrgLogoToSpaces(raw, "image/png", ".png")

	if len(sp.uploads) != 0 {
		t.Errorf("should not re-upload when key already exists; got %v", mapKeys(sp.uploads))
	}
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
