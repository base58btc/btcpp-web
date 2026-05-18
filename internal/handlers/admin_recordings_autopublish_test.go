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
	row := &RecordingRow{Recording: rec}
	if !shouldUploadRecordingToYouTube(row) {
		t.Fatalf("recording with FileURI, PublishAt, and no YTLink should upload")
	}
	row.YTStatus = recordingStatusFailed
	if shouldUploadRecordingToYouTube(row) {
		t.Fatalf("failed YouTube recording should wait for explicit retry")
	}
	row.YTStatus = recordingStatusPending
	row.YTURL = "https://youtu.be/example"
	if shouldUploadRecordingToYouTube(row) {
		t.Fatalf("recording with YTLink should not upload again")
	}

	rec.PublishAt = &due
	row.XStatus = ""
	row.XURL = ""
	if !shouldPostRecordingToX(row, time.Now()) {
		t.Fatalf("due recording with YTLink should post to X")
	}
	rec.PublishAt = &future
	if shouldPostRecordingToX(row, time.Now()) {
		t.Fatalf("future recording should not post to X yet")
	}
	rec.PublishAt = &due
	row.XStatus = recordingStatusScheduled
	if shouldPostRecordingToX(row, time.Now()) {
		t.Fatalf("recording already scheduled on X should not post again")
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

func TestRecordingSourceObjectKeyNormalizesSpacesValues(t *testing.T) {
	tests := map[string]string{
		" videos/talk.mp4 ": "videos/talk.mp4",
		"/videos/talk.mp4":  "videos/talk.mp4",
		"https://btcpp.nyc3.digitaloceanspaces.com/videos/talk.mp4":     "videos/talk.mp4",
		"https://btcpp.nyc3.digitaloceanspaces.com/videos/talk%201.mp4": "videos/talk 1.mp4",
	}

	for in, want := range tests {
		if got := recordingSourceObjectKey(in); got != want {
			t.Fatalf("recordingSourceObjectKey(%q) = %q, want %q", in, got, want)
		}
	}
}
