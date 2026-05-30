package handlers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/missives"
	"btcpp-web/internal/types"
)

// Talk-app proposal statuses we set programmatically. The full
// state machine lives in /Users/niftynei/.claude/projects/.../
// project_talk_app_states.md but two transitions matter here:
//
//   - Accepted: program-locked-in but the schedule is still in
//     draft. Admin can drag/resize the talk on the schedule UI
//     freely; no calendar invites have gone out yet.
//   - Scheduled: cal invite has been sent — the schedule is
//     committed. Subsequent edits show as "drift" on the
//     schedule UI and require an explicit "Send Cal Updates"
//     click before they propagate to attendees' calendars.
//
// The Accepted → Scheduled transition fires from the per-proposal
// "Send cal invite" button on /admin/applicants (inside
// AdminProposalSendCal). The reverse transition isn't supported —
// once an invite has gone out, the talk is "live" until cancelled.
const (
	StatusAccepted  = "Accepted"
	StatusScheduled = "Scheduled"
)

// ErrDuplicateSpeakerEmail is returned when two or more Speakers share the
// applicant's email — a data-integrity issue the admin must resolve manually.
var ErrDuplicateSpeakerEmail = errors.New("duplicate speaker emails")

// acceptDeps carries the side-effecting collaborators used by AcceptProposal.
// Production wires these to the getters package; tests pass fakes.
type acceptDeps struct {
	loadProposal         func(proposalID string) (*types.Proposal, error)
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
	AlreadyAccepted bool
}

func newAcceptPipeline(ctx *config.AppContext) acceptPipeline {
	return acceptPipeline{deps: acceptDeps{
		loadProposal: func(id string) (*types.Proposal, error) {
			return getters.GetProposal(ctx, id)
		},
		updateProposalStatus: func(id, s string) error {
			return getters.UpdateProposalStatus(ctx, id, s)
		},
		logf: ctx.Err.Printf,
	}}
}

// AcceptProposal flips the proposal's Status to "Accepted". A ConfTalk
// row is NOT created here — ConfTalks now represent "scheduled" state
// and are minted/destroyed by the schedule tool, not by acceptance.
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
		return result, errors.New("proposal has no scheduled conference; nothing to accept")
	}

	if err := p.deps.updateProposalStatus(proposalID, StatusAccepted); err != nil {
		return result, fmt.Errorf("update proposal status: %w", err)
	}

	return result, nil
}

// fanoutAcceptedProposal handles the side effects that should fire
// once a proposal flips to Accepted: send the talkconfirmed letter to
// every speaker on the proposal and issue each one a complimentary
// "speaker"-type ticket. Mirrors the volunteer-ticket flow in
// `scheduledFlow` (handlers.go) — same Entry shape, same AddTickets +
// NewTicketSub plumbing, just a "speaker" tixType and a "spkreg"
// registration hash.
//
// Errors are logged, never fatal — a flaky email send or ticket-DB
// write shouldn't block the proposal status change. Idempotency:
// AddTickets / NewTicketSub use the deterministic SpeakerRegisID, so
// re-runs (admin double-click, speaker re-clicking the email link)
// upsert rather than duplicate.
func fanoutAcceptedProposal(ctx *config.AppContext, proposal *types.Proposal, conf *types.Conf) {
	if proposal == nil || conf == nil {
		return
	}
	if err := emails.SendOnlyForProposal(ctx, "talkconfirmed", proposal, conf, ""); err != nil {
		ctx.Err.Printf("fanoutAcceptedProposal %s: send talkconfirmed: %s", proposal.ID, err)
	}
	now := time.Now()
	for _, ref := range proposal.SpeakerConfRefs {
		sc := getters.FetchSpeakerConfByID(ref)
		if sc == nil || sc.Speaker == nil || sc.Speaker.Email == "" {
			continue
		}
		if err := getters.SetSpeakerConfAcceptedAt(ctx, ref, now); err != nil {
			ctx.Err.Printf("fanoutAcceptedProposal %s: stamp AcceptedAt on %s: %s", proposal.ID, ref, err)
		}
		issueSpeakerTicket(ctx, sc.Speaker.Email, conf)
	}
}

// issueSpeakerTicket creates a complimentary "speaker"-type ticket
// for one speaker at a conf and subscribes them to the conf's
// post-purchase mailing lists. Errors are logged, not returned —
// callers fan this out across multiple speakers and don't want one
// failure to abort the rest.
func issueSpeakerTicket(ctx *config.AppContext, email string, conf *types.Conf) {
	if email == "" || conf == nil {
		return
	}
	tixType := "speaker"
	entry := types.Entry{
		ID:       types.SpeakerRegisID(conf.Ref, email),
		ConfRef:  conf.Ref,
		Currency: "USD",
		Created:  time.Now(),
		Email:    email,
		Items: []types.Item{
			{Total: 1, Desc: conf.Desc, Type: tixType},
		},
	}
	if err := getters.AddTickets(ctx, &entry, "spkreg"); err != nil {
		ctx.Err.Printf("issueSpeakerTicket add %s for %s: %s", email, conf.Tag, err)
		return
	}
	if err := missives.NewTicketSub(ctx, email, conf.Tag, tixType, false); err != nil {
		ctx.Err.Printf("issueSpeakerTicket newsletter %s for %s: %s", email, conf.Tag, err)
	}
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
