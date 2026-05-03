package handlers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

const StatusAccepted = "Accepted"

// ErrDuplicateSpeakerEmail is returned when two or more Speakers share the
// applicant's email — a data-integrity issue the admin must resolve manually.
var ErrDuplicateSpeakerEmail = errors.New("duplicate speaker emails")

// acceptDeps carries the side-effecting collaborators used by AcceptProposal.
// Production wires these to the getters package; tests pass fakes.
type acceptDeps struct {
	loadProposal         func(proposalID string) (*types.Proposal, error)
	createConfTalk       func(in getters.ConfTalkInput) (string, error)
	updateProposalStatus func(proposalID, status string) error
	logf                 func(format string, args ...interface{})
}

type acceptPipeline struct {
	deps acceptDeps
}

// AcceptResult summarizes what AcceptProposal did, for use in admin flash
// messages.
type AcceptResult struct {
	ProposalID      string
	ConfTalkID      string
	AlreadyAccepted bool
}

func newAcceptPipeline(ctx *config.AppContext) acceptPipeline {
	return acceptPipeline{deps: acceptDeps{
		loadProposal: func(id string) (*types.Proposal, error) {
			return getters.GetProposal(ctx, id)
		},
		createConfTalk: func(in getters.ConfTalkInput) (string, error) {
			return getters.CreateConfTalk(ctx.Notion, in)
		},
		updateProposalStatus: func(id, s string) error {
			return getters.UpdateProposalStatus(ctx, id, s)
		},
		logf: ctx.Err.Printf,
	}}
}

// AcceptProposal promotes a Proposal into a ConfTalk row and flips the
// proposal's Status to "Accepted". The Status flip is the LAST step so any
// partial failure leaves the proposal in its prior state and re-runs are safe.
func (p acceptPipeline) AcceptProposal(proposalID string) (AcceptResult, error) {
	result := AcceptResult{ProposalID: proposalID}

	proposal, err := p.deps.loadProposal(proposalID)
	if err != nil {
		return result, fmt.Errorf("load proposal: %w", err)
	}

	if proposal.Status == StatusAccepted {
		result.AlreadyAccepted = true
		return result, nil
	}

	if proposal.ScheduleFor == nil || proposal.ScheduleFor.Tag == "" {
		return result, errors.New("proposal has no scheduled conference; nothing to promote")
	}

	confTalkID, err := p.deps.createConfTalk(getters.ConfTalkInput{
		ConfTag:    proposal.ScheduleFor.Tag,
		ProposalID: proposalID,
	})
	if err != nil {
		return result, fmt.Errorf("create conf talk: %w", err)
	}
	result.ConfTalkID = confTalkID

	if err := p.deps.updateProposalStatus(proposalID, StatusAccepted); err != nil {
		return result, fmt.Errorf("update proposal status: %w", err)
	}

	return result, nil
}

// avif400Name converts a TalkApp NormPhoto value (e.g. "abc123def456.jpg")
// to the 400x400 AVIF derivative's filename ("abc123def456-400.avif"). The
// 400 AVIF is what we surface on the Speakers DB so the conf page renders
// the optimized thumbnail directly. Returns "" when given an empty input or
// a value with no extension to trim.
func avif400Name(normPhoto string) string {
	if normPhoto == "" {
		return ""
	}
	ext := filepath.Ext(normPhoto)
	if ext == "" {
		return ""
	}
	return strings.TrimSuffix(normPhoto, ext) + "-400.avif"
}

// mapPresTypeToTalkType collapses the application's presentation-length
// options onto the Talks DB's five accepted Talk Type values
// (talk / workshop / panel / keynote / hackathon) by substring match on the
// presType ID. Form options like "lntalk", "20talk", "45panel", "60workshop"
// all carry the type word in their ID, so new variants pick up the right
// mapping automatically. Returns "" for unrecognized values.
func mapPresTypeToTalkType(presType string) string {
	switch {
	case strings.Contains(presType, "talk"):
		return "talk"
	case strings.Contains(presType, "workshop"):
		return "workshop"
	case strings.Contains(presType, "panel"):
		return "panel"
	case strings.Contains(presType, "keynote"):
		return "keynote"
	case strings.Contains(presType, "hackathon"):
		return "hackathon"
	default:
		return ""
	}
}

// validShirtCode returns the input if it's one of the Speakers DB TShirt
// select options, else "" — guards against bad form input writing an
// invalid Notion option.
func validShirtCode(shirt string) string {
	switch shirt {
	case "LS", "LM", "LL", "MS", "MM", "ML", "MXL", "MXXL", "MXXXL":
		return shirt
	default:
		return ""
	}
}
