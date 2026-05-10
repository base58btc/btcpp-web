package ics

import (
	"strings"

	"btcpp-web/internal/types"
)

// Reply-To addresses for cal-invite emails. The envelope From stays
// as `hello@btcpp.dev` (existing DKIM/SPF stays intact); the
// Reply-To is what speakers / volunteers see when they hit Reply.
// These same addresses go into the ICS ORGANIZER field — RFC-5545
// metadata, not envelope-related.
const (
	ReplyToTalk      = "speak@btcpp.dev"
	ReplyToTalkName  = "bitcoin++ speakers"
	ReplyToShift     = "volunteer@btcpp.dev"
	ReplyToShiftName = "bitcoin++ volunteers"
)

// MapVenue resolves the raw `talk.Venue` slug stored on a ConfTalk
// row (`one`, `two`, `three`) to a human-readable stage label for
// the ICS LOCATION field. Returns the empty string for unknown /
// blank input — callers should fall back to `conf.Venue` (or any
// other suitable label) when this returns "".
func MapVenue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "one":
		return "Main Stage"
	case "two":
		return "Talks Stage"
	case "three":
		return "Workshops Stage"
	}
	return ""
}

// BuildTalkEvent assembles the Event for a single ConfTalk + the
// recipient receiving this email. Returns the assembled Event ready
// to feed into Render.
//
// The returned Event always has exactly one Attendee — the
// recipient. We send per-recipient ICS (instead of a single shared
// ICS) so each speaker's calendar shows themselves as the only
// attendee, mirroring how Gmail / Outlook send invitations and
// avoiding the "everyone on the panel can see everyone else's
// email" footgun.
//
// `nextSeq` should come from NextSeq applied against the prior
// CalNotif state. `confVenue` is the fallback location string used
// when the talk's Venue slug doesn't resolve to a known stage.
func BuildTalkEvent(
	talk *types.ConfTalk,
	proposal *types.Proposal,
	conf *types.Conf,
	recipient Attendee,
	method string,
	uid string,
	nextSeq int,
	confVenue string,
) Event {
	location := MapVenue(talk.Venue)
	if location == "" {
		location = confVenue
	}

	title := ""
	desc := ""
	if proposal != nil {
		title = proposal.Title
		desc = proposal.Description
	}

	end := talk.Sched.Start
	if talk.Sched.End != nil {
		end = *talk.Sched.End
	}

	return Event{
		Method:        method,
		UID:           uid,
		Sequence:      nextSeq,
		Status:        statusFor(method),
		Summary:       "speak @ btc++: " + title,
		Description:   desc,
		Location:      location,
		Start:         talk.Sched.Start,
		End:           end,
		TZ:            conf.Loc(),
		Organizer:     ReplyToTalk,
		OrganizerName: ReplyToTalkName,
		Attendees:     []Attendee{recipient},
	}
}

// BuildShiftEvent is the volunteer-shift counterpart of
// BuildTalkEvent. Same per-recipient pattern.
func BuildShiftEvent(
	shift *types.WorkShift,
	conf *types.Conf,
	recipient Attendee,
	method string,
	uid string,
	nextSeq int,
) Event {
	desc := ""
	if shift.Type != nil {
		desc = shift.Type.LongDesc
	}

	end := shift.ShiftTime.Start
	if shift.ShiftTime.End != nil {
		end = *shift.ShiftTime.End
	}

	return Event{
		Method:        method,
		UID:           uid,
		Sequence:      nextSeq,
		Status:        statusFor(method),
		Summary:       "vol shift @ btc++: " + shift.Name,
		Description:   desc,
		Location:      conf.Venue,
		Start:         shift.ShiftTime.Start,
		End:           end,
		TZ:            conf.Loc(),
		Organizer:     ReplyToShift,
		OrganizerName: ReplyToShiftName,
		Attendees:     []Attendee{recipient},
	}
}

// statusFor picks the right STATUS for the METHOD. CANCEL → CANCELLED,
// everything else → CONFIRMED.
func statusFor(method string) string {
	if method == MethodCancel {
		return StatusCancelled
	}
	return StatusConfirmed
}
