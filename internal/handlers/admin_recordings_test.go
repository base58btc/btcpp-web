package handlers

import (
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
