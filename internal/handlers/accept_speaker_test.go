package handlers

import (
	"errors"
	"strings"
	"testing"

	"btcpp-web/external/getters"
	"btcpp-web/internal/types"
)

type acceptRecorder struct {
	loadCalledWith []string
	confTalksCreated []getters.ConfTalkInput
	statusUpdates  []struct {
		ID     string
		Status string
	}
}

func makeProposal(status string, conf *types.Conf) *types.Proposal {
	return &types.Proposal{
		ID:          "prop-1",
		Title:       "On Bitcoin",
		Description: "A talk",
		TalkType:    "talk",
		Status:      status,
		ScheduleFor: conf,
	}
}

func newAcceptRecorder(t *testing.T, proposal *types.Proposal, opts ...func(*acceptDeps)) (acceptPipeline, *acceptRecorder) {
	t.Helper()
	rec := &acceptRecorder{}
	deps := acceptDeps{
		loadProposal: func(id string) (*types.Proposal, error) {
			rec.loadCalledWith = append(rec.loadCalledWith, id)
			if proposal == nil {
				return nil, errors.New("not found")
			}
			return proposal, nil
		},
		createConfTalk: func(in getters.ConfTalkInput) (string, error) {
			rec.confTalksCreated = append(rec.confTalksCreated, in)
			return "ct-" + in.ConfTag, nil
		},
		updateProposalStatus: func(id, status string) error {
			rec.statusUpdates = append(rec.statusUpdates, struct {
				ID     string
				Status string
			}{id, status})
			return nil
		},
		logf: t.Logf,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	return acceptPipeline{deps: deps}, rec
}

func TestAcceptProposal_HappyPath(t *testing.T) {
	conf := &types.Conf{Tag: "berlin26", Ref: "conf-berlin26"}
	prop := makeProposal("InReview", conf)
	p, rec := newAcceptRecorder(t, prop)

	result, err := p.AcceptProposal("prop-1")
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	if result.AlreadyAccepted {
		t.Error("AlreadyAccepted should be false")
	}
	if result.ProposalID != "prop-1" {
		t.Errorf("ProposalID: got %q, want prop-1", result.ProposalID)
	}
	if result.ConfTalkID != "ct-berlin26" {
		t.Errorf("ConfTalkID: got %q, want ct-berlin26", result.ConfTalkID)
	}

	if len(rec.confTalksCreated) != 1 {
		t.Fatalf("expected 1 conf talk created; got %d", len(rec.confTalksCreated))
	}
	ct := rec.confTalksCreated[0]
	if ct.ConfTag != "berlin26" {
		t.Errorf("ConfTalk ConfTag: got %q, want berlin26", ct.ConfTag)
	}
	if ct.ProposalID != "prop-1" {
		t.Errorf("ConfTalk ProposalID: got %q, want prop-1", ct.ProposalID)
	}

	if len(rec.statusUpdates) != 1 || rec.statusUpdates[0].Status != "Accepted" {
		t.Errorf("expected one status update to Accepted; got %v", rec.statusUpdates)
	}
}

func TestAcceptProposal_AlreadyAcceptedShortCircuits(t *testing.T) {
	conf := &types.Conf{Tag: "berlin26", Ref: "conf-berlin26"}
	prop := makeProposal("Accepted", conf)
	p, rec := newAcceptRecorder(t, prop)

	result, err := p.AcceptProposal("prop-1")
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	if !result.AlreadyAccepted {
		t.Error("expected AlreadyAccepted=true")
	}
	if len(rec.confTalksCreated)+len(rec.statusUpdates) != 0 {
		t.Errorf("no side effects expected on already-accepted; got confTalks=%d statusFlips=%d",
			len(rec.confTalksCreated), len(rec.statusUpdates))
	}
}

func TestAcceptProposal_NoScheduleForFails(t *testing.T) {
	prop := makeProposal("InReview", nil)
	p, rec := newAcceptRecorder(t, prop)

	_, err := p.AcceptProposal("prop-1")
	if err == nil || !strings.Contains(err.Error(), "no scheduled conference") {
		t.Fatalf("expected 'no scheduled conference' error; got %v", err)
	}
	if len(rec.confTalksCreated)+len(rec.statusUpdates) != 0 {
		t.Error("no writes should occur when proposal has no conference")
	}
}

func TestAcceptProposal_ConfTalkFailureLeavesStatusUntouched(t *testing.T) {
	conf := &types.Conf{Tag: "berlin26", Ref: "conf-berlin26"}
	prop := makeProposal("InReview", conf)
	p, rec := newAcceptRecorder(t, prop, func(d *acceptDeps) {
		d.createConfTalk = func(in getters.ConfTalkInput) (string, error) {
			return "", errors.New("notion 503")
		}
	})

	_, err := p.AcceptProposal("prop-1")
	if err == nil {
		t.Fatal("expected error from createConfTalk")
	}
	if len(rec.statusUpdates) != 0 {
		t.Error("status MUST NOT flip when ConfTalk create fails — required for safe re-run")
	}
}

func TestAvif400Name(t *testing.T) {
	cases := map[string]string{
		"abc123.jpg":  "abc123-400.avif",
		"abc123.jpeg": "abc123-400.avif",
		"abc123.png":  "abc123-400.avif",
		"abc123.webp": "abc123-400.avif",
		"":            "",
		"noext":       "", // no extension means we can't safely strip
	}
	for in, want := range cases {
		if got := avif400Name(in); got != want {
			t.Errorf("avif400Name(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapPresTypeToTalkType(t *testing.T) {
	cases := map[string]string{
		"lntalk":      "talk",
		"20talk":      "talk",
		"45talk":      "talk",
		"45panel":     "panel",
		"45workshop":  "workshop",
		"60workshop":  "workshop",
		"90workshop":  "workshop",
		"120workshop": "workshop",
		"30talk":      "talk",
		"60panel":     "panel",
		"60keynote":   "keynote",
		"hackathon":   "hackathon",
		"":            "",
		"unknown":     "",
	}
	for in, want := range cases {
		if got := mapPresTypeToTalkType(in); got != want {
			t.Errorf("mapPresTypeToTalkType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidShirtCode(t *testing.T) {
	cases := map[string]string{
		"LS":      "LS",
		"LM":      "LM",
		"LL":      "LL",
		"MS":      "MS",
		"MM":      "MM",
		"ML":      "ML",
		"MXL":     "MXL",
		"MXXL":   "MXXL",
		"MXXXL":  "MXXXL",
		"":        "",
		"unknown": "",
		"S":       "", // legacy unisex codes no longer accepted
		"M":       "",
		"XL":      "",
	}
	for in, want := range cases {
		if got := validShirtCode(in); got != want {
			t.Errorf("validShirtCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeUniqueTags(t *testing.T) {
	cases := []struct {
		name          string
		existing, add []string
		want          []string
	}{
		{"both empty", nil, nil, []string{}},
		{"no overlap", []string{"a"}, []string{"b"}, []string{"a", "b"}},
		{"full overlap", []string{"a", "b"}, []string{"a", "b"}, []string{"a", "b"}},
		{"partial overlap preserves order", []string{"berlin25", "atx25"}, []string{"berlin26", "atx25"}, []string{"berlin25", "atx25", "berlin26"}},
		{"drops empty strings", []string{"", "a"}, []string{"", "b"}, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := getters.MergeUniqueTags(c.existing, c.add)
			if !stringsEq(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func stringsEq(a, b []string) bool {
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
