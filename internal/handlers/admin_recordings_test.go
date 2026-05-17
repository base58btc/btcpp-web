package handlers

import (
	"strings"
	"testing"

	"btcpp-web/external/getters"
	"btcpp-web/internal/types"
)

func TestRecordingSpeakersForProposalResolvesSpeakerConfRefs(t *testing.T) {
	speaker := &types.Speaker{ID: "speaker-recordings-test-a", Name: "Ada"}
	getters.CacheSpeakerConfInsert(&types.SpeakerConf{
		ID:      "speakerconf-recordings-test-a",
		Speaker: speaker,
	})

	got := recordingSpeakersForProposal(&types.Proposal{
		SpeakerConfRefs: []string{"speakerconf-recordings-test-a"},
	})

	if len(got) != 1 {
		t.Fatalf("got %d speakers, want 1", len(got))
	}
	if got[0] != speaker {
		t.Fatalf("got speaker %#v, want %#v", got[0], speaker)
	}
}

func TestRecordingSpeakersForProposalDedupesResolvedAndEnrichedSpeakers(t *testing.T) {
	speaker := &types.Speaker{ID: "speaker-recordings-test-b", Name: "Grace"}
	getters.CacheSpeakerConfInsert(&types.SpeakerConf{
		ID:      "speakerconf-recordings-test-b",
		Speaker: speaker,
	})

	got := recordingSpeakersForProposal(&types.Proposal{
		SpeakerConfRefs: []string{"speakerconf-recordings-test-b"},
		Speakers: []*types.SpeakerConf{
			{Speaker: speaker},
		},
	})

	if len(got) != 1 {
		t.Fatalf("got %d speakers, want 1", len(got))
	}
	if got[0] != speaker {
		t.Fatalf("got speaker %#v, want %#v", got[0], speaker)
	}
}

func TestRecordingXMainCopyUsesSavedSocialPostText(t *testing.T) {
	want := "Custom X copy for this recording"
	got := recordingXMainCopy(nil, &RecordingRow{
		Recording:   &types.Recording{TalkName: "Generated title"},
		XSocialPost: &types.SocialPost{Text: want},
	})

	if got != want {
		t.Fatalf("got %q, want saved text %q", got, want)
	}
}

func TestRecordingXMainCopyFallsBackToGeneratedText(t *testing.T) {
	got := recordingXMainCopy(nil, &RecordingRow{
		Recording: &types.Recording{TalkName: "Generated title"},
	})

	if !strings.Contains(got, "Generated title") {
		t.Fatalf("got %q, want generated recording title", got)
	}
}
