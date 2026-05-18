package handlers

import (
	"strings"
	"testing"
	"time"

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

func TestRecordingXMainCopyFormatsDefaultPost(t *testing.T) {
	got := recordingXMainCopy(nil, &RecordingRow{
		Recording: &types.Recording{TalkName: "Fallback title"},
		ConfTalk: &types.ConfTalk{
			Conf: &types.Conf{Desc: "bitcoin++ Test", Location: "Austin"},
			Proposal: &types.Proposal{
				Title: "A Great Talk",
			},
		},
		Speakers: []*types.Speaker{
			{Name: "Ada Lovelace", Twitter: types.Twitter{Handle: "ada"}},
			{Name: "Grace Hopper", Twitter: types.Twitter{Handle: "grace"}},
			{Name: "Satoshi Nakamoto"},
		},
	})

	want := "POSTED 🎥: A Great Talk\n\nFeaturing: Ada Lovelace (@ada), Grace Hopper (@grace), Satoshi Nakamoto\n\nfrom bitcoin++ Test (Austin)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRecordingXSocialPostUsesTwitterInternally(t *testing.T) {
	rec := &types.Recording{ID: "recording-recordings-test-x"}

	if got := recordingSocialPostRef(rec, recordingPlatformX); got != "recording:recording-recordings-test-x:twitter" {
		t.Fatalf("x social post ref = %q", got)
	}
	if recordingPlatformX != "twitter" {
		t.Fatalf("x platform = %q, want twitter", recordingPlatformX)
	}
}

func TestRecordingXReplyCopyForPostRequiresYouTubeURL(t *testing.T) {
	noYT := recordingXReplyCopyForPost(nil, &RecordingRow{
		Recording: &types.Recording{TalkName: "No YouTube yet"},
	})
	if noYT != "" {
		t.Fatalf("reply without YouTube URL = %q, want empty", noYT)
	}

	withYT := recordingXReplyCopyForPost(nil, &RecordingRow{
		Recording: &types.Recording{TalkName: "With YouTube"},
		YTURL:     "https://youtu.be/example",
	})
	if !strings.Contains(withYT, "https://youtu.be/example") {
		t.Fatalf("reply with YouTube URL = %q, want URL included", withYT)
	}
}

func TestDefaultXReplyCopyDoesNotLinkTalkPage(t *testing.T) {
	got := defaultXReplyCopy(nil, &RecordingRow{
		Recording: &types.Recording{TalkName: "With YouTube"},
		ConfTalk:  &types.ConfTalk{Conf: &types.Conf{Tag: "oldconf"}},
		YTURL:     "https://youtu.be/example",
	})

	if strings.Contains(got, "#talks") || strings.Contains(got, "More:") {
		t.Fatalf("reply copy = %q, want no talk page link", got)
	}
}

func TestNextTicketConfFromListChoosesNextActiveFutureConf(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	current := &types.Conf{
		Ref:       "current",
		Tag:       "current",
		Active:    true,
		StartDate: now.Add(24 * time.Hour),
		Tickets:   []*types.ConfTicket{{ID: "current-ticket"}},
	}
	want := &types.Conf{
		Ref:       "next",
		Tag:       "next",
		Active:    true,
		StartDate: now.Add(48 * time.Hour),
		Tickets:   []*types.ConfTicket{{ID: "next-ticket"}},
	}
	later := &types.Conf{
		Ref:       "later",
		Tag:       "later",
		Active:    true,
		StartDate: now.Add(72 * time.Hour),
		Tickets:   []*types.ConfTicket{{ID: "later-ticket"}},
	}

	got := nextTicketConfFromList([]*types.Conf{
		later,
		{Ref: "inactive", Tag: "inactive", Active: false, StartDate: now.Add(time.Hour), Tickets: []*types.ConfTicket{{ID: "inactive-ticket"}}},
		{Ref: "no-tickets", Tag: "no-tickets", Active: true, StartDate: now.Add(time.Hour)},
		current,
		want,
	}, current, now)

	if got != want {
		t.Fatalf("next ticket conf = %#v, want %#v", got, want)
	}
}
