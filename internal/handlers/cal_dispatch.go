package handlers

import (
	"fmt"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/types"
)

// dispatchKind picks REQUEST or CANCEL semantics for the ICS the
// pipeline produces.
type dispatchKind int

const (
	kindRequest dispatchKind = iota
	kindCancel
)

// DispatchTalkICSForProposal looks up the ConfTalk for a proposal,
// builds a synthetic Talk view, and dispatches one calendar
// invite to each speaker on the proposal. Used by the per-card
// "Send / Resend / Update cal invite" button on the proposals
// admin page and by the auto-fire hooks (schedule changes,
// speaker-add).
//
// Returns nil when the proposal has no ConfTalk yet (auto-fire
// callers blast through every status transition; "no ConfTalk →
// nothing to invite" is the common case, not an error). Returns
// an error only when a ConfTalk exists but is missing scheduled
// times (a real misconfiguration).
func DispatchTalkICSForProposal(ctx *config.AppContext, proposal *types.Proposal, conf *types.Conf, speakers []*types.Speaker, force bool) error {
	if proposal == nil {
		return fmt.Errorf("dispatchTalkICSForProposal: nil proposal")
	}
	ct := getters.FetchConfTalkByProposal(proposal.ID)
	if ct == nil {
		return nil // not scheduled — no calendar-side state to update
	}
	if ct.Sched == nil || ct.Sched.End == nil {
		return fmt.Errorf("proposal %q has no scheduled time", proposal.Title)
	}
	talk := &types.Talk{
		ID:          ct.ID,
		Name:        proposal.Title,
		Description: proposal.Description,
		Sched:       ct.Sched,
		Speakers:    speakers,
		Venue:       ct.Venue,
		CalNotif:    ct.CalNotif,
	}
	return DispatchTalkICSForTalk(ctx, talk, conf, kindRequest, force)
}

// DispatchTalkICSCancelForProposal fires CANCEL ICS to every
// speaker on a proposal whose ConfTalk currently has a non-empty
// CalNotif. Used by the terminal-decline status transitions
// (WeDecline / Rejected / TheyDecline) and by the schedule-unplace
// path. Silent no-op when no CalNotif is set — that means no
// invite was ever sent, so there's nothing to cancel.
func DispatchTalkICSCancelForProposal(ctx *config.AppContext, proposal *types.Proposal, conf *types.Conf, speakers []*types.Speaker) error {
	if proposal == nil {
		return nil
	}
	ct := getters.FetchConfTalkByProposal(proposal.ID)
	if ct == nil || ct.CalNotif == "" {
		return nil
	}
	if ct.Sched == nil || ct.Sched.End == nil {
		// CalNotif present but no scheduled times — odd state,
		// but a CANCEL needs DTSTART/DTEND so we skip rather
		// than render an invalid VEVENT.
		ctx.Infos.Printf("dispatchTalkICSCancelForProposal %q: CalNotif set but no Sched.End — skipping CANCEL", proposal.Title)
		return nil
	}
	talk := &types.Talk{
		ID:          ct.ID,
		Name:        proposal.Title,
		Description: proposal.Description,
		Sched:       ct.Sched,
		Speakers:    speakers,
		Venue:       ct.Venue,
		CalNotif:    ct.CalNotif,
	}
	return DispatchTalkICSForTalk(ctx, talk, conf, kindCancel, false)
}

// DispatchTalkICSRemoved handles the speaker-removal flow:
//
//  1. Send a CANCEL ICS to the removed speaker so the event
//     vanishes from their calendar.
//  2. Send a force-bumped REQUEST to the remaining speakers so
//     their copy of the event picks up the new (smaller) ATTENDEE
//     list with an incremented SEQUENCE.
//
// `removedEmail` is the address to send the CANCEL to;
// `remaining` is the slice of speakers still on the proposal
// after the removal landed. Caller is responsible for filtering
// out the removed speaker from `remaining` before calling.
//
// Silent no-op when the proposal has no ConfTalk or no CalNotif —
// the removed speaker was never invited, nothing to cancel.
func DispatchTalkICSRemoved(ctx *config.AppContext, proposal *types.Proposal, conf *types.Conf, removedEmail string, removedName string, remaining []*types.Speaker) error {
	if proposal == nil {
		return nil
	}
	ct := getters.FetchConfTalkByProposal(proposal.ID)
	if ct == nil || ct.CalNotif == "" {
		return nil
	}
	if ct.Sched == nil || ct.Sched.End == nil {
		return nil
	}

	// CANCEL to the removed speaker. Build a single-attendee Talk
	// view so DispatchTalkICSForTalk's per-recipient loop only
	// fans to the one removed address.
	if removedEmail != "" {
		removedSpeaker := &types.Speaker{Email: removedEmail, Name: removedName}
		cancelTalk := &types.Talk{
			ID:          ct.ID,
			Name:        proposal.Title,
			Description: proposal.Description,
			Sched:       ct.Sched,
			Speakers:    []*types.Speaker{removedSpeaker},
			Venue:       ct.Venue,
			CalNotif:    ct.CalNotif,
		}
		if err := DispatchTalkICSForTalk(ctx, cancelTalk, conf, kindCancel, false); err != nil {
			ctx.Err.Printf("dispatchTalkICSRemoved CANCEL %q → %s: %s", proposal.Title, removedEmail, err)
			// Non-fatal — still try the REQUEST update below.
		}
	}

	// REQUEST(force=true) to the remaining speakers so their
	// copy of the event reflects the trimmed ATTENDEE list and
	// advances SEQUENCE. The CANCEL above already bumped the
	// sequence stored on CalNotif; re-fetching ensures we
	// compose against the freshly-stamped state.
	if len(remaining) == 0 {
		return nil
	}
	updatedCT := getters.FetchConfTalkByProposal(proposal.ID)
	if updatedCT == nil {
		updatedCT = ct
	}
	updateTalk := &types.Talk{
		ID:          updatedCT.ID,
		Name:        proposal.Title,
		Description: proposal.Description,
		Sched:       updatedCT.Sched,
		Speakers:    remaining,
		Venue:       updatedCT.Venue,
		CalNotif:    updatedCT.CalNotif,
	}
	return DispatchTalkICSForTalk(ctx, updateTalk, conf, kindRequest, true)
}

func (k dispatchKind) method() string {
	if k == kindCancel {
		return ics.MethodCancel
	}
	return ics.MethodRequest
}

func (k dispatchKind) summaryVerb() string {
	if k == kindCancel {
		return "cancelled"
	}
	return "scheduled"
}

// DispatchTalkICSForTalk fans an RFC-5545 ICS invite (or
// cancellation) out to every speaker on a talk. Drives the
// CalNotif state machine end-to-end:
//
//  1. Read prior CalNotif from talk.CalNotif.
//  2. Compute new content hash from start/end/conf-tag/title.
//  3. NextSeq decides whether to send (skip when nothing changed
//     unless force=true).
//  4. For each speaker email: build a per-recipient ICS, send a
//     Mail with the ICS attached and Reply-To: speak@btcpp.dev.
//  5. Stamp the new CalNotif ("UID:Sequence:Hash") back to the
//     ConfTalk row.
//
// Best-effort: send errors are logged + counted but don't fail the
// dispatch as a whole. Returns nil if any speaker received the
// invite, or the first error otherwise.
//
// `force=true` skips the "hash unchanged → skip" short-circuit.
// Used by the speaker-add path where the attendee set changed but
// the title/time hash didn't.
//
// Phase 2 ships with hand-built letter bodies (the talkspeakerinvite
// / talkunscheduled Notion letters land in Phase 3). Once those
// exist, swap the per-recipient ComposeAndSendMail call to
// emails.ExecLetter with the appropriate OnlyFor tag.
func DispatchTalkICSForTalk(ctx *config.AppContext, talk *types.Talk, conf *types.Conf, kind dispatchKind, force bool) error {
	if talk == nil || conf == nil {
		return fmt.Errorf("dispatchTalkICS: nil talk or conf")
	}
	if talk.Sched == nil || talk.Sched.End == nil {
		return fmt.Errorf("dispatchTalkICS %q: no scheduled end time", talk.Name)
	}
	if len(talk.Speakers) == 0 {
		return fmt.Errorf("dispatchTalkICS %q: no speakers", talk.Name)
	}
	end := *talk.Sched.End

	prev, prevValid := ics.ParseCalNotif(talk.CalNotif)
	uid := prev.UID
	if !prevValid || uid == "" {
		uid = ics.NewUID("talk", talk.ID)
	}

	newHash := ics.ContentHash(talk.Sched.Start, end, conf.Tag, talk.Name)
	seq, send := ics.NextSeq(prev, prevValid, newHash, force || kind == kindCancel)
	if !send {
		ctx.Infos.Printf("dispatchTalkICS %q: hash unchanged, skipping", talk.Name)
		return nil
	}

	method := kind.method()
	location := ics.MapVenue(talk.Venue)
	if location == "" {
		location = conf.Venue
	}

	confDateLabel := talk.Sched.Start.In(conf.Loc()).Format("Mon Jan 2 · 3:04 PM")
	body := buildTalkBody(kind, talk, conf, confDateLabel, location)
	htmlBody, _ := emails.BuildHTMLEmail(ctx, []byte(body))
	title := fmt.Sprintf("[%s] %s — talk %s", conf.Desc, talk.Name, kind.summaryVerb())

	var firstErr error
	sentCount := 0
	for _, sp := range talk.Speakers {
		if sp == nil || sp.Email == "" {
			continue
		}
		event := ics.Event{
			Method:        method,
			UID:           uid,
			Sequence:      seq,
			Status:        statusForKind(kind),
			Summary:       "speak @ btc++: " + talk.Name,
			Description:   talk.Description,
			Location:      location,
			Start:         talk.Sched.Start,
			End:           end,
			TZ:            conf.Loc(),
			Organizer:     ics.ReplyToTalk,
			OrganizerName: ics.ReplyToTalkName,
			Attendees: []ics.Attendee{
				{Email: sp.Email, Name: sp.Name},
			},
		}
		icsBytes := ics.Render(event)
		mail := &emails.Mail{
			JobKey:   talkJobKey(talk.ID, seq, sp.Email, kind),
			Email:    sp.Email,
			Title:    title,
			SendAt:   time.Now(),
			ReplyTo:  ics.ReplyToTalk,
			TextBody: []byte(body),
			HTMLBody: htmlBody,
			Files: []*emails.EmailFile{{
				Bytes:       icsBytes,
				ContentType: icsContentType(kind),
				Name:        "invite.ics",
			}},
		}
		if err := emails.ComposeAndSendMail(ctx, mail); err != nil {
			ctx.Err.Printf("dispatchTalkICS %q → %s: %s", talk.Name, sp.Email, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sentCount++
	}

	if sentCount == 0 {
		return firstErr
	}

	// Stamp the new CalNotif back. On CANCEL we keep the old hash
	// (so a later un-cancel + reschedule produces a coherent diff)
	// and just bump the sequence. On REQUEST the hash advances.
	stampHash := newHash
	if kind == kindCancel {
		stampHash = prev.HashHex
		if stampHash == "" {
			stampHash = newHash
		}
	}
	stamp := ics.CalNotif{UID: uid, Sequence: seq, HashHex: stampHash}.String()
	if err := getters.TalkUpdateCalNotif(ctx, talk.ID, stamp); err != nil {
		ctx.Err.Printf("dispatchTalkICS %q calnotif writeback: %s", talk.Name, err)
	} else {
		ctx.Infos.Printf("dispatchTalkICS %q: %s seq=%d sent=%d/%d hash=%s",
			talk.Name, method, seq, sentCount, len(talk.Speakers), stampHash)
	}
	return nil
}

// DispatchShiftICS fans an RFC-5545 ICS invite (or cancel) out to a
// list of pre-resolved volunteer recipients. Handles the same
// CalNotif state machine as DispatchTalkICSForTalk but against the
// WorkShift row.
//
// recipients is the slice of (email, name) pairs to invite — caller
// is responsible for resolving AssigneesRef → volunteer emails.
func DispatchShiftICS(ctx *config.AppContext, shift *types.WorkShift, conf *types.Conf, recipients []ics.Attendee, kind dispatchKind, force bool) error {
	if shift == nil || conf == nil {
		return fmt.Errorf("dispatchShiftICS: nil shift or conf")
	}
	if shift.ShiftTime == nil || shift.ShiftTime.End == nil {
		return fmt.Errorf("dispatchShiftICS %q: no scheduled end time", shift.Name)
	}
	if len(recipients) == 0 {
		// Nothing to send to. Treat as a no-op rather than an
		// error — empty assignee lists are normal.
		return nil
	}
	end := *shift.ShiftTime.End

	prev, prevValid := ics.ParseCalNotif(shift.CalNotif)
	uid := prev.UID
	if !prevValid || uid == "" {
		uid = ics.NewUID("shift", shift.Ref)
	}

	newHash := ics.ContentHash(shift.ShiftTime.Start, end, conf.Tag, shift.Name)
	seq, send := ics.NextSeq(prev, prevValid, newHash, force || kind == kindCancel)
	if !send {
		ctx.Infos.Printf("dispatchShiftICS %q: hash unchanged, skipping", shift.Name)
		return nil
	}

	method := kind.method()
	desc := ""
	if shift.Type != nil {
		desc = shift.Type.LongDesc
	}
	confDateLabel := shift.ShiftTime.Start.In(conf.Loc()).Format("Mon Jan 2 · 3:04 PM")
	body := buildShiftBody(kind, shift, conf, confDateLabel)
	htmlBody, _ := emails.BuildHTMLEmail(ctx, []byte(body))
	title := fmt.Sprintf("[%s] Volunteer shift %s — %s", conf.Desc, shift.Name, kind.summaryVerb())

	var firstErr error
	sentCount := 0
	for _, recip := range recipients {
		if recip.Email == "" {
			continue
		}
		event := ics.Event{
			Method:        method,
			UID:           uid,
			Sequence:      seq,
			Status:        statusForKind(kind),
			Summary:       "vol shift @ btc++: " + shift.Name,
			Description:   desc,
			Location:      conf.Venue,
			Start:         shift.ShiftTime.Start,
			End:           end,
			TZ:            conf.Loc(),
			Organizer:     ics.ReplyToShift,
			OrganizerName: ics.ReplyToShiftName,
			Attendees:     []ics.Attendee{recip},
		}
		icsBytes := ics.Render(event)
		mail := &emails.Mail{
			JobKey:   shiftJobKey(shift.Ref, seq, recip.Email, kind),
			Email:    recip.Email,
			Title:    title,
			SendAt:   time.Now(),
			ReplyTo:  ics.ReplyToShift,
			TextBody: []byte(body),
			HTMLBody: htmlBody,
			Files: []*emails.EmailFile{{
				Bytes:       icsBytes,
				ContentType: icsContentType(kind),
				Name:        "invite.ics",
			}},
		}
		if err := emails.ComposeAndSendMail(ctx, mail); err != nil {
			ctx.Err.Printf("dispatchShiftICS %q → %s: %s", shift.Name, recip.Email, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sentCount++
	}

	if sentCount == 0 {
		return firstErr
	}

	stampHash := newHash
	if kind == kindCancel {
		stampHash = prev.HashHex
		if stampHash == "" {
			stampHash = newHash
		}
	}
	stamp := ics.CalNotif{UID: uid, Sequence: seq, HashHex: stampHash}.String()
	if err := getters.ShiftUpdateCalNotif(ctx, shift.Ref, stamp); err != nil {
		ctx.Err.Printf("dispatchShiftICS %q calnotif writeback: %s", shift.Name, err)
	} else {
		ctx.Infos.Printf("dispatchShiftICS %q: %s seq=%d sent=%d/%d hash=%s",
			shift.Name, method, seq, sentCount, len(recipients), stampHash)
	}
	return nil
}

// DispatchShiftICSCancelForVol fires a CANCEL ICS to one
// volunteer for one shift. Used by the volunteer-unassign paths
// (vol self-remove, vol declines volunteering, admin removes vol)
// so the dropped shift vanishes from that volunteer's calendar
// without disturbing the other assignees still on it.
//
// Silent no-op when the shift has no CalNotif (it was never
// invited out) or when the volunteer's email is unknown.
func DispatchShiftICSCancelForVol(ctx *config.AppContext, shift *types.WorkShift, conf *types.Conf, volEmail, volName string) error {
	if shift == nil || conf == nil {
		return nil
	}
	if shift.CalNotif == "" {
		return nil
	}
	if volEmail == "" {
		return nil
	}
	if shift.ShiftTime == nil || shift.ShiftTime.End == nil {
		return nil
	}
	return DispatchShiftICS(ctx, shift, conf,
		[]ics.Attendee{{Email: volEmail, Name: volName}},
		kindCancel, false)
}

// DispatchOrientICS sends a volunteer-orientation invite to one
// volunteer. State is tracked at the conf level on
// Conf.OrientCalNotif (a "UID:Sequence:Hashbytes" triple in the
// same shape as ConfTalk.CalNotif) so the per-vol UID + SEQUENCE
// stay coherent across signups.
//
// All volunteers share the same UID per conf (one logical event;
// the recipient's calendar matches against UID, not who the
// invite was addressed to). Each volunteer is sent the event
// exactly once at signup — the email is NOT hash-gated here
// (every new vol needs their first-send email even when the
// orientation time hasn't changed). The hash-gate only kicks in
// on the bulk BroadcastOrientICS path triggered by the volcoord
// "Resend orientation invite" button.
//
// State writeback bumps SEQUENCE only when the content hash has
// drifted since the last per-vol send — so a new vol signing up
// AFTER an admin edits the orientation time gets the new state
// stamped (UID stable, seq+1) and the new time. Already-invited
// vols don't get an automatic update; the volcoord broadcast
// button covers that case.
func DispatchOrientICS(ctx *config.AppContext, conf *types.Conf, vol ics.Attendee, start, end time.Time, orientLink string) error {
	if conf == nil {
		return fmt.Errorf("dispatchOrientICS: nil conf")
	}
	if vol.Email == "" {
		return nil
	}

	prev, prevValid := ics.ParseCalNotif(conf.OrientCalNotif)
	uid := prev.UID
	if !prevValid || uid == "" {
		uid = ics.NewUID("orient", conf.Tag)
	}

	// Title used both in the SUMMARY and in the hash — keep
	// stable so the only inputs that bump SEQUENCE are the
	// start / end times.
	title := fmt.Sprintf("Volunteer Orientation: %s", conf.Desc)
	newHash := ics.ContentHash(start, end, conf.Tag, title)

	// Pick seq + decide whether to stamp:
	//   - No prior state → seq=0, stamp the initial UID/hash.
	//   - Hash matches → use stored seq, no stamp needed (state
	//     is already current).
	//   - Hash drifted → seq+1, stamp the new state so future
	//     vols see the new time.
	// In all three cases we send the email — this is the per-vol
	// first-send path, every volunteer needs their copy.
	var seq int
	stamp := false
	switch {
	case !prevValid:
		seq = 0
		stamp = true
	case prev.HashHex == newHash:
		seq = prev.Sequence
	default:
		seq = prev.Sequence + 1
		stamp = true
	}

	event := ics.Event{
		Method:        ics.MethodRequest,
		UID:           uid,
		Sequence:      seq,
		Status:        ics.StatusConfirmed,
		Summary:       fmt.Sprintf("vol orientation @ btc++: %s", conf.Desc),
		Description:   "Volunteer orientation — please attend before doors open.",
		Location:      orientationLocation(orientLink),
		Start:         start,
		End:           end,
		TZ:            conf.Loc(),
		Organizer:     ics.ReplyToShift,
		OrganizerName: ics.ReplyToShiftName,
		Attendees:     []ics.Attendee{vol},
	}
	icsBytes := ics.Render(event)
	body := fmt.Sprintf("Volunteer orientation for %s\n\nWhen: %s (%s)\nWhere: %s\n",
		conf.Desc, start.In(conf.Loc()).Format("Mon Jan 2 · 3:04 PM"), conf.Timezone, orientationLocation(orientLink))
	htmlBody, _ := emails.BuildHTMLEmail(ctx, []byte(body))
	mail := &emails.Mail{
		JobKey:   fmt.Sprintf("orient-%s-s%d-%s", conf.Tag, seq, vol.Email),
		Email:    vol.Email,
		Title:    fmt.Sprintf("[%s] Volunteer orientation", conf.Desc),
		SendAt:   time.Now(),
		ReplyTo:  ics.ReplyToShift,
		TextBody: []byte(body),
		HTMLBody: htmlBody,
		Files: []*emails.EmailFile{{
			Bytes:       icsBytes,
			ContentType: "text/calendar; method=REQUEST; charset=utf-8",
			Name:        "invite.ics",
		}},
	}
	if err := emails.ComposeAndSendMail(ctx, mail); err != nil {
		return err
	}

	// Stamp state only when we initialized (first send) or
	// advanced (orientation time drifted) — hash-matched sends
	// reuse the existing CalNotif unchanged.
	if stamp {
		s := ics.CalNotif{UID: uid, Sequence: seq, HashHex: newHash}.String()
		if err := getters.ConfUpdateOrientCalNotif(ctx, conf.Ref, s); err != nil {
			ctx.Err.Printf("dispatchOrientICS %s calnotif writeback: %s", conf.Tag, err)
		}
	}
	return nil
}

// BroadcastOrientICS fans the orientation invite out to a list of
// recipients with a single shared (UID, Sequence) tuple — used by
// the volcoord "Resend orientation" button when the orientation
// time has changed and the admin wants to propagate the new time
// to every already-invited volunteer in one shot.
//
// Unlike DispatchOrientICS (per-vol, hash-gated), this always
// sends regardless of hash match: the explicit click is the
// intent. SEQUENCE bumps once (the broadcast IS the update), and
// the same seq lands in every recipient's email.
//
// Returns nil + a counts log on partial success; first error
// otherwise. Empty recipients is a no-op.
func BroadcastOrientICS(ctx *config.AppContext, conf *types.Conf, start, end time.Time, orientLink string, recipients []ics.Attendee) (sent int, err error) {
	if conf == nil {
		return 0, fmt.Errorf("broadcastOrientICS: nil conf")
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	prev, prevValid := ics.ParseCalNotif(conf.OrientCalNotif)
	uid := prev.UID
	if !prevValid || uid == "" {
		uid = ics.NewUID("orient", conf.Tag)
	}

	title := fmt.Sprintf("Volunteer Orientation: %s", conf.Desc)
	newHash := ics.ContentHash(start, end, conf.Tag, title)
	// force=true: a broadcast click always sends + bumps. The
	// "skip when hash unchanged" path is reserved for the
	// per-vol signup dispatch.
	seq, _ := ics.NextSeq(prev, prevValid, newHash, true)

	body := fmt.Sprintf("Volunteer orientation for %s\n\nWhen: %s (%s)\nWhere: %s\n",
		conf.Desc, start.In(conf.Loc()).Format("Mon Jan 2 · 3:04 PM"), conf.Timezone, orientationLocation(orientLink))
	htmlBody, _ := emails.BuildHTMLEmail(ctx, []byte(body))

	var firstErr error
	for _, vol := range recipients {
		if vol.Email == "" {
			continue
		}
		event := ics.Event{
			Method:        ics.MethodRequest,
			UID:           uid,
			Sequence:      seq,
			Status:        ics.StatusConfirmed,
			Summary:       fmt.Sprintf("vol orientation @ btc++: %s", conf.Desc),
			Description:   "Volunteer orientation — please attend before doors open.",
			Location:      orientationLocation(orientLink),
			Start:         start,
			End:           end,
			TZ:            conf.Loc(),
			Organizer:     ics.ReplyToShift,
			OrganizerName: ics.ReplyToShiftName,
			Attendees:     []ics.Attendee{vol},
		}
		icsBytes := ics.Render(event)
		mail := &emails.Mail{
			JobKey:   fmt.Sprintf("orient-%s-s%d-%s", conf.Tag, seq, vol.Email),
			Email:    vol.Email,
			Title:    fmt.Sprintf("[%s] Volunteer orientation (updated)", conf.Desc),
			SendAt:   time.Now(),
			ReplyTo:  ics.ReplyToShift,
			TextBody: []byte(body),
			HTMLBody: htmlBody,
			Files: []*emails.EmailFile{{
				Bytes:       icsBytes,
				ContentType: "text/calendar; method=REQUEST; charset=utf-8",
				Name:        "invite.ics",
			}},
		}
		if sErr := emails.ComposeAndSendMail(ctx, mail); sErr != nil {
			ctx.Err.Printf("broadcastOrientICS %s → %s: %s", conf.Tag, vol.Email, sErr)
			if firstErr == nil {
				firstErr = sErr
			}
			continue
		}
		sent++
	}

	if sent == 0 {
		return 0, firstErr
	}

	stamp := ics.CalNotif{UID: uid, Sequence: seq, HashHex: newHash}.String()
	if wErr := getters.ConfUpdateOrientCalNotif(ctx, conf.Ref, stamp); wErr != nil {
		ctx.Err.Printf("broadcastOrientICS %s calnotif writeback: %s", conf.Tag, wErr)
	}
	ctx.Infos.Printf("broadcastOrientICS %s: seq=%d sent=%d/%d hash=%s", conf.Tag, seq, sent, len(recipients), newHash)
	return sent, nil
}

func orientationLocation(orientLink string) string {
	if strings.TrimSpace(orientLink) != "" {
		return strings.TrimSpace(orientLink)
	}
	return "Online"
}

func statusForKind(k dispatchKind) string {
	if k == kindCancel {
		return ics.StatusCancelled
	}
	return ics.StatusConfirmed
}

func icsContentType(k dispatchKind) string {
	if k == kindCancel {
		return "text/calendar; method=CANCEL; charset=utf-8"
	}
	return "text/calendar; method=REQUEST; charset=utf-8"
}

// talkJobKey / shiftJobKey produce a deterministic job-key per
// (entity, sequence, recipient, kind) combo so the upstream mailer's
// dedupe layer treats every distinct sequence-bump or recipient as
// its own send. Unique enough to keep the mailer happy; stable
// enough that an intra-process retry inside the same sequence is
// idempotent.
func talkJobKey(talkID string, seq int, email string, k dispatchKind) string {
	return fmt.Sprintf("ics-talk-%s-s%d-%s-%s", talkID, seq, email, k.method())
}

func shiftJobKey(shiftRef string, seq int, email string, k dispatchKind) string {
	return fmt.Sprintf("ics-shift-%s-s%d-%s-%s", shiftRef, seq, email, k.method())
}

// buildTalkBody composes the human-readable email body for a talk
// REQUEST or CANCEL. Phase 2 ships with hand-built copy here; Phase
// 3 swaps to Notion-managed letter content via emails.ExecLetter
// once the talkspeakerinvite / talkunscheduled letters are
// authored.
func buildTalkBody(kind dispatchKind, talk *types.Talk, conf *types.Conf, dateLabel, location string) string {
	if kind == kindCancel {
		return fmt.Sprintf(
			"Your talk %q at %s has been removed from the schedule.\n\n"+
				"The attached calendar cancellation removes the event from your calendar.\n\n"+
				"If this was a mistake, reply to this email and we'll sort it out.\n",
			talk.Name, conf.Desc)
	}
	return fmt.Sprintf(
		"Your talk %q at %s is scheduled.\n\n"+
			"When: %s (%s)\nWhere: %s\n\n"+
			"The attached calendar invite (.ics) lets you add or update the event\n"+
			"on your calendar. Re-sends with the same time + title will silently\n"+
			"update the existing entry.\n",
		talk.Name, conf.Desc, dateLabel, conf.Timezone, location)
}

func buildShiftBody(kind dispatchKind, shift *types.WorkShift, conf *types.Conf, dateLabel string) string {
	if kind == kindCancel {
		return fmt.Sprintf(
			"Your volunteer shift %q at %s has been cancelled.\n\n"+
				"The attached calendar cancellation removes the event from your calendar.\n",
			shift.Name, conf.Desc)
	}
	return fmt.Sprintf(
		"Your volunteer shift %q at %s is scheduled.\n\n"+
			"When: %s (%s)\nWhere: %s\n\n"+
			"The attached calendar invite (.ics) lets you add or update the\n"+
			"event on your calendar.\n",
		shift.Name, conf.Desc, dateLabel, conf.Timezone, conf.Venue)
}
