package handlers

import (
	"testing"
	"time"

	"btcpp-web/internal/types"
)

func TestRecordingAutopublishEligibility(t *testing.T) {
	future := time.Now().Add(time.Hour)
	due := time.Now().Add(-time.Minute)

	rec := &types.Recording{
		FileURI:   "videos/talk.mp4",
		PublishAt: &future,
	}
	if !shouldUploadRecordingToYouTube(rec) {
		t.Fatalf("recording with FileURI, PublishAt, and no YTLink should upload")
	}
	rec.YTStatus = recordingStatusFailed
	if shouldUploadRecordingToYouTube(rec) {
		t.Fatalf("failed YouTube recording should wait for explicit retry")
	}
	rec.YTStatus = recordingStatusPending
	rec.YTLink = "https://youtu.be/example"
	if shouldUploadRecordingToYouTube(rec) {
		t.Fatalf("recording with YTLink should not upload again")
	}

	rec.PublishAt = &due
	rec.XStatus = ""
	rec.XLink = ""
	if !shouldPostRecordingToX(rec, time.Now()) {
		t.Fatalf("due recording with YTLink should post to X")
	}
	rec.PublishAt = &future
	if shouldPostRecordingToX(rec, time.Now()) {
		t.Fatalf("future recording should not post to X yet")
	}
}

func TestXFailureFingerprintChangesByStatusAndMessage(t *testing.T) {
	a := xFailureFingerprint(recordingStatusFailed, "upload failed")
	b := xFailureFingerprint(recordingStatusAuthRequired, "upload failed")
	c := xFailureFingerprint(recordingStatusFailed, "different")
	if a == b || a == c || b == c {
		t.Fatalf("fingerprints should differ by status and message")
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(a))
	}
}
