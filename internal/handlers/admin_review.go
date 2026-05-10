package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

// pendingReviewStatuses is the set of proposal statuses surfaced in
// the review queue — anything where the program team still owes the
// applicant a yes/no/wait-and-see decision. Already-decisioned
// proposals (Invited / Accepted / TheyDecline / WeDecline / Rejected /
// Waitlisted) drop out of the queue.
var pendingReviewStatuses = map[string]bool{
	"":         true, // Notion select unset — treat as pending
	"Applied":  true,
	"InReview": true,
}

// reviewActions lists the buttons rendered on the per-proposal review
// page, in display order. Each entry maps a button label to the
// status that gets written to Notion + the onlyfor letter that gets
// fanned out to every speaker on the proposal.
var reviewActions = []reviewAction{
	{Label: "Invite to Confirm", Status: "Invited", Letter: "talkinvited", Style: "green"},
	{Label: "Mark Confirmed", Status: StatusAccepted, Letter: "talkconfirmed", Style: "link", RunAcceptPipeline: true},
	{Label: "Waitlist", Status: "Waitlisted", Letter: "talkwaitlisted", Style: "yellow"},
	{Label: "Decline", Status: "WeDecline", Letter: "talkdeclined", Style: "gray"},
	{Label: "Reject", Status: "Rejected", Letter: "talkrejected", Style: "red"},
}

type reviewAction struct {
	Label  string
	Status string
	Letter string
	Style  string // tailwind color family for the button
	// RunAcceptPipeline triggers the existing acceptPipeline (creates
	// the ConfTalk row, etc.) rather than just flipping Status. Used
	// for the "Mark Confirmed" path so admin and speaker-side accepts
	// converge on the same downstream side-effects.
	RunAcceptPipeline bool
}

// OrganizerDashboard is the per-event admin landing at
// /admin/conf/{tag}/. Hub for everything organizer-y for one
// conference: review applications today, more tools as we add them.
func OrganizerDashboard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	id := requireConfAdmin(w, r, ctx)
	if id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	pending, decisioned := splitProposalsByPending(loadConfProposals(ctx, conf))

	// Populate countdown bounds (doors-open / doors-close) on a
	// shallow copy of conf so the cached pointer isn't mutated.
	// Drives the countdown widget at the top of conf_dashboard.
	confCopy := *conf
	var infosByDay map[int]*types.ConfInfo
	if cis, err := getters.ListConfInfos(ctx, conf.Tag); err == nil {
		infosByDay = confInfosByDay(cis)
	}
	confCopy.CountdownStart, confCopy.CountdownEnd = computeCountdownBounds(&confCopy, infosByDay)

	err = ctx.TemplateCache.ExecuteTemplate(w, "admin/conf_dashboard.tmpl", &OrganizerDashboardPage{
		Conf:            &confCopy,
		PendingCount:    len(pending),
		DecisionedCount: len(decisioned),
		FlashMessage:    r.URL.Query().Get("flash"),
		IsGlobalAdmin:   id.IsGlobalAdmin(),
		Year:            helpers.CurrentYear(),
	})
	if err != nil {
		ctx.Err.Printf("/%s/admin render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// ReviewProposals walks through one pending proposal at a time at
// /admin/conf/{tag}/review. Optional `?id=<proposalID>` jumps to a
// specific row (after-action redirect uses this to advance to the next
// pending). With no id we pick the first by creation order.
func ReviewProposals(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	all := loadConfProposals(ctx, conf)
	pending, _ := splitProposalsByPending(all)

	wanted := r.URL.Query().Get("id")
	current, idx := pickProposal(pending, wanted)

	page := &ReviewProposalPage{
		Conf:         conf,
		Total:        len(pending),
		Index:        idx, // 0 when current is nil
		Actions:      reviewActions,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}
	if current != nil {
		page.Current = current
		page.Speakers = resolveProposalSpeakers(current)
		// Pre-compute the next URL so the action POSTs can simply pick
		// it off the page; saves recomputing in each handler.
		if next := nextProposalAfter(pending, current.ID); next != nil {
			page.NextID = next.ID
		}
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/review_proposal.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/review render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// ReviewProposalAction handles POSTs from the review page's action
// buttons. Updates Notion status, optionally runs the accept pipeline,
// fans out the onlyfor letter, and redirects back to the next pending
// proposal in the queue.
//
// Path: /admin/conf/{tag}/review/{proposalID}/{action}
//
// `action` is one of: invite / confirm / waitlist / decline / reject
// — see reviewActions for the full mapping to (status, onlyfor tag).
func ReviewProposalAction(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	vars := mux.Vars(r)
	proposalID := vars["proposalID"]
	actionKey := vars["action"]

	action, ok := lookupReviewAction(actionKey)
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return
	}

	freshAccept := false
	if action.RunAcceptPipeline {
		res, err := newAcceptPipeline(ctx).AcceptProposal(proposalID)
		if err != nil {
			ctx.Err.Printf("/%s/admin/review accept pipeline: %s", conf.Tag, err)
			redirectReview(w, r, conf, proposalID, "Accept failed: "+err.Error())
			return
		}
		freshAccept = !res.AlreadyAccepted
	} else {
		if err := getters.UpdateProposalStatus(ctx, proposalID, action.Status); err != nil {
			ctx.Err.Printf("/%s/admin/review update status %q: %s", conf.Tag, action.Status, err)
			redirectReview(w, r, conf, proposalID, "Status update failed: "+err.Error())
			return
		}
	}

	if freshAccept {
		// Mark Confirmed: fanoutAcceptedProposal already sends
		// talkconfirmed AND issues complimentary speaker tickets to
		// every speaker on the proposal. Skip the separate letter
		// send below to avoid double-emailing.
		fanoutAcceptedProposal(ctx, proposal, conf)
	} else {
		// Every other status change (Invite / Waitlist / Decline /
		// Reject) fires its own letter. Best-effort — admin can
		// re-fire from the email composer if the send blips.
		if err := emails.SendOnlyForProposal(ctx, action.Letter, proposal, conf, ""); err != nil {
			ctx.Err.Printf("/%s/admin/review send %s (continuing): %s", conf.Tag, action.Letter, err)
		}
	}

	flash := fmt.Sprintf("%s — letter %q queued.", action.Label, action.Letter)

	// When the action came from the applicants table (or any other
	// page that wants to stay in place), the form supplies a
	// ?return=<path> query param. Bounce there with a flash instead
	// of advancing through the review queue.
	if dest := safeReturnTo(r.URL.Query().Get("return")); dest != "" {
		http.Redirect(w, r, appendFlash(dest, flash), http.StatusSeeOther)
		return
	}

	// Default: advance to the next un-actioned proposal in the
	// review queue. The cache invalidation in UpdateProposalStatus +
	// the in-place mutation we wired earlier means the next call
	// sees this proposal's new status and skips it.
	pending, _ := splitProposalsByPending(loadConfProposals(ctx, conf))
	next := nextProposalAfter(pending, proposalID)
	if next != nil {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/review?id=%s&flash=%s",
				conf.Tag, next.ID, url.QueryEscape(flash)),
			http.StatusSeeOther)
		return
	}
	// Queue empty — bounce to the conf dashboard with the success flash.
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/?flash=%s", conf.Tag, url.QueryEscape(flash+" Queue is now empty.")),
		http.StatusSeeOther)
}

// AdminCancelTalk flips an Accepted proposal back to TheyDecline —
// the path for "speaker pulls out after confirming". Surfaced only
// on /{conf}/admin/applicants (not the review queue) and only when
// the row is in the Accepted state. No letter is sent: the speaker
// initiated the cancellation externally; admin is just recording it.
//
// Path: POST /{conf}/admin/applicants/{proposalID}/cancel
func AdminCancelTalk(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := mux.Vars(r)["proposalID"]
	if proposalID == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=Missing+proposal", conf.Tag), http.StatusSeeOther)
		return
	}

	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=Proposal+not+found", conf.Tag), http.StatusSeeOther)
		return
	}
	if proposal.Status != StatusAccepted {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=%s",
				conf.Tag,
				url.QueryEscape(fmt.Sprintf("Only Accepted talks can be cancelled (was %q)", proposal.Status))),
			http.StatusSeeOther)
		return
	}

	if err := getters.UpdateProposalStatus(ctx, proposalID, "TheyDecline"); err != nil {
		ctx.Err.Printf("/%s/admin/applicants/%s/cancel update status: %s", conf.Tag, proposalID, err)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=%s",
				conf.Tag,
				url.QueryEscape("Cancel failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}

	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/applicants?flash=%s",
			conf.Tag,
			url.QueryEscape(fmt.Sprintf("Cancelled — %q is now TheyDecline.", proposal.Title))),
		http.StatusSeeOther)
}

// AdminResendSpeakerTickets walks every Accepted proposal for a conf
// and issues a complimentary "speaker"-type ticket to each attached
// speaker who doesn't already have a registration for that conf
// (paid, volunteer, or speaker — any type counts).
//
// Used by the "Resend tickets" button on the applicants table —
// patches gaps where a talk got Accepted before the comp-ticket
// pipeline existed, or where an admin manually flipped a status in
// Notion bypassing the normal accept flow.
//
// Path: POST /admin/applicants/{conf}/resend-tickets
func AdminResendSpeakerTickets(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	var issued, skipped, failed int
	seen := map[string]bool{}
	for _, p := range loadConfProposals(ctx, conf) {
		if p == nil || p.Status != StatusAccepted {
			continue
		}
		for _, ref := range p.SpeakerConfRefs {
			sc := getters.FetchSpeakerConfByID(ref)
			if sc == nil || sc.Speaker == nil || sc.Speaker.Email == "" {
				continue
			}
			email := strings.ToLower(strings.TrimSpace(sc.Speaker.Email))
			if seen[email] {
				continue
			}
			seen[email] = true

			has, err := emailHasConfRegistration(ctx, email, conf.Ref)
			if err != nil {
				ctx.Err.Printf("/%s/admin/applicants/resend-tickets lookup %s: %s", conf.Tag, email, err)
				failed++
				continue
			}
			if has {
				skipped++
				continue
			}
			issueSpeakerTicket(ctx, sc.Speaker.Email, conf)
			issued++
		}
	}

	flash := fmt.Sprintf("Resend tickets: issued=%d, skipped (already have one)=%d, failed=%d", issued, skipped, failed)
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, url.QueryEscape(flash)),
		http.StatusSeeOther)
}

// emailHasConfRegistration reports whether an email already has any
// PurchasesDb row for the given conf — paid ticket, volunteer comp,
// previous speaker comp, doesn't matter. Used by the resend-tickets
// flow to avoid double-issuing.
func emailHasConfRegistration(ctx *config.AppContext, email, confRef string) (bool, error) {
	regs, err := getters.ListRegistrationsByEmail(ctx, email)
	if err != nil {
		return false, err
	}
	for _, r := range regs {
		if r != nil && r.ConfRef == confRef {
			return true, nil
		}
	}
	return false, nil
}

// AdminProposalInviteLink mints (if needed) the share-a-link
// InviteToken on a proposal and renders a small page showing the URL
// for the admin to copy. Mirrors DashboardInviteCoSpeaker but
// PIN-authed and with NO CanInvite gate — admins can invite right up
// to (and after) the conf, so they can patch in last-minute speaker
// substitutions.
//
// Path: GET /admin/conf/{tag}/proposal/{proposalID}/invite
func AdminProposalInviteLink(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := mux.Vars(r)["proposalID"]
	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return
	}
	if proposal.InviteToken == "" {
		token := helpers.MintInviteToken()
		if err := getters.SetProposalInviteToken(ctx, proposalID, token); err != nil {
			ctx.Err.Printf("/%s/admin/proposal/%s/invite mint: %s", conf.Tag, proposalID, err)
			redirectReview(w, r, conf, proposalID, "Couldn't mint invite link: "+err.Error())
			return
		}
		proposal.InviteToken = token
	}
	// Reuse the speaker-side template — the data shape is identical
	// (proposal title + share URL + back link). HMAC/Email aren't
	// meaningful for an admin-authed view but the template uses them
	// only to build a "back to dashboard" link, which we override to
	// the review page via a flash redirect path.
	page := &InviteCoSpeakerPage{
		Proposal:  proposal,
		Conf:      conf,
		HMAC:      "",
		Email:     "",
		InviteURL: helpers.InviteLink(ctx, proposalID, proposal.InviteToken),
		Year:      helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/proposal_invite.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/proposal/%s/invite render: %s", conf.Tag, proposalID, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// AdminProposalRemoveSpeaker removes a SpeakerConf from a proposal's
// speakers list. Mirrors DashboardRemoveCoSpeaker but PIN-authed and
// without the CanInvite / "can't remove the last speaker" /
// terminal-status gates — admins are trusted to know when to override.
//
// Path: POST /admin/conf/{tag}/proposal/{proposalID}/speakers/{speakerConfID}/remove
func AdminProposalRemoveSpeaker(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	vars := mux.Vars(r)
	proposalID := vars["proposalID"]
	speakerConfID := vars["speakerConfID"]

	if err := getters.RemoveProposalFromSpeakerConf(ctx, speakerConfID, proposalID); err != nil {
		ctx.Err.Printf("/%s/admin/proposal/%s remove speaker %s: %s", conf.Tag, proposalID, speakerConfID, err)
		redirectReview(w, r, conf, proposalID, "Remove failed: "+err.Error())
		return
	}
	redirectReview(w, r, conf, proposalID, "Speaker removed from proposal.")
}

// loadConfProposals returns every Proposal whose ScheduleFor matches
// conf, sorted by ID for stable walkthrough order. Reads from the warm
// proposals cache.
func loadConfProposals(ctx *config.AppContext, conf *types.Conf) []*types.Proposal {
	all, err := getters.ListProposals(ctx)
	if err != nil {
		ctx.Err.Printf("loadConfProposals %s: %s", conf.Tag, err)
		return nil
	}
	var out []*types.Proposal
	for _, p := range all {
		if p == nil || p.ScheduleFor == nil {
			continue
		}
		if p.ScheduleFor.Ref == conf.Ref {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func splitProposalsByPending(props []*types.Proposal) (pending, decisioned []*types.Proposal) {
	for _, p := range props {
		if pendingReviewStatuses[p.Status] {
			pending = append(pending, p)
		} else {
			decisioned = append(decisioned, p)
		}
	}
	return pending, decisioned
}

// pickProposal returns the proposal matching wantedID (when set) and
// its index in the slice, or the first entry when wantedID is empty.
// Returns (nil, 0) for an empty pending slice.
func pickProposal(pending []*types.Proposal, wantedID string) (*types.Proposal, int) {
	if len(pending) == 0 {
		return nil, 0
	}
	if wantedID == "" {
		return pending[0], 1
	}
	for i, p := range pending {
		if p.ID == wantedID {
			return p, i + 1
		}
	}
	// Wanted ID isn't pending anymore — fall back to first.
	return pending[0], 1
}

// nextProposalAfter returns the first proposal in pending that comes
// strictly after fromID, or nil at the end of the queue. Useful for
// "advance to next" redirects.
func nextProposalAfter(pending []*types.Proposal, fromID string) *types.Proposal {
	seenFrom := false
	for _, p := range pending {
		if seenFrom {
			return p
		}
		if p.ID == fromID {
			seenFrom = true
		}
	}
	// fromID not in pending (already actioned) — return whatever's first.
	if len(pending) > 0 {
		return pending[0]
	}
	return nil
}

// resolveProposalSpeakers returns the SpeakerConf objects for every
// speaker on the proposal, fully resolved (Speaker pointer attached).
// Reads from the warm SpeakerConf cache; misses are skipped.
func resolveProposalSpeakers(p *types.Proposal) []*types.SpeakerConf {
	if p == nil {
		return nil
	}
	out := make([]*types.SpeakerConf, 0, len(p.SpeakerConfRefs))
	for _, ref := range p.SpeakerConfRefs {
		if sc := getters.FetchSpeakerConfByID(ref); sc != nil {
			out = append(out, sc)
		}
	}
	return out
}

func lookupReviewAction(key string) (reviewAction, bool) {
	for _, a := range reviewActions {
		if a.actionKey() == key {
			return a, true
		}
	}
	return reviewAction{}, false
}

// actionKey is the URL-safe slug for a review action — derived from
// the first lowercased word of the label. Stable across label tweaks
// because we always use the same word as the slug source.
func (a reviewAction) actionKey() string {
	switch a.Label {
	case "Invite to Confirm":
		return "invite"
	case "Mark Confirmed":
		return "confirm"
	case "Waitlist":
		return "waitlist"
	case "Decline":
		return "decline"
	case "Reject":
		return "reject"
	}
	return ""
}

// ButtonClasses returns the tailwind color classes for the action
// button. Centralized here so the template stays terse. The "link"
// style intentionally returns no background — the template wraps it
// to render as small grey text instead of a filled button.
func (a reviewAction) ButtonClasses() string {
	switch a.Style {
	case "indigo":
		return "bg-indigo-600 hover:bg-indigo-700 text-white"
	case "green":
		return "bg-green-600 hover:bg-green-700 text-white"
	case "yellow":
		return "bg-yellow-500 hover:bg-yellow-600 text-white"
	case "gray":
		return "bg-gray-600 hover:bg-gray-700 text-white"
	case "red":
		return "bg-red-600 hover:bg-red-700 text-white"
	case "link":
		return "text-xs text-gray-500 hover:text-gray-800 underline"
	}
	return "bg-gray-600 hover:bg-gray-700 text-white"
}

// IsLink reports whether the action renders as a text-link rather
// than a filled button. Templates use this to drop the button-like
// padding/border so a "link" action sits inline like a hyperlink.
func (a reviewAction) IsLink() bool { return a.Style == "link" }

// ActionKey is the public accessor templates use to build form action
// URLs.
func (a reviewAction) ActionKey() string { return a.actionKey() }

func redirectReview(w http.ResponseWriter, r *http.Request, conf *types.Conf, proposalID, flash string) {
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/review?id=%s&flash=%s",
			conf.Tag, proposalID, url.QueryEscape(flash)),
		http.StatusSeeOther)
}
