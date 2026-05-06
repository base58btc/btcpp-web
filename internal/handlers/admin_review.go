package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"

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
	{Label: "Invite to Confirm", Status: "Invited", Letter: "talkinvited", Style: "indigo"},
	{Label: "Mark Confirmed", Status: StatusAccepted, Letter: "talkconfirmed", Style: "green", RunAcceptPipeline: true},
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	pending, decisioned := splitProposalsByPending(loadConfProposals(ctx, conf))

	err = ctx.TemplateCache.ExecuteTemplate(w, "admin/conf_dashboard.tmpl", &OrganizerDashboardPage{
		Conf:           conf,
		PendingCount:   len(pending),
		DecisionedCount: len(decisioned),
		FlashMessage:   r.URL.Query().Get("flash"),
		Year:           helpers.CurrentYear(),
	})
	if err != nil {
		ctx.Err.Printf("/admin/conf/%s render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// ReviewProposals walks through one pending proposal at a time at
// /admin/conf/{tag}/review. Optional `?id=<proposalID>` jumps to a
// specific row (after-action redirect uses this to advance to the next
// pending). With no id we pick the first by creation order.
func ReviewProposals(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/admin/conf/%s/review render: %s", conf.Tag, err)
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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

	if action.RunAcceptPipeline {
		if _, err := newAcceptPipeline(ctx).AcceptProposal(proposalID); err != nil {
			ctx.Err.Printf("/admin/conf/%s/review accept pipeline: %s", conf.Tag, err)
			redirectReview(w, r, conf, proposalID, "Accept failed: "+err.Error())
			return
		}
	} else {
		if err := getters.UpdateProposalStatus(ctx, proposalID, action.Status); err != nil {
			ctx.Err.Printf("/admin/conf/%s/review update status %q: %s", conf.Tag, action.Status, err)
			redirectReview(w, r, conf, proposalID, "Status update failed: "+err.Error())
			return
		}
	}

	// Fan out the onlyfor letter to every speaker on the proposal.
	// Best-effort: if the email send fails we still log success on the
	// status change — the admin can re-fire the letter from the email
	// composer later.
	if err := emails.SendOnlyForProposal(ctx, action.Letter, proposal, conf); err != nil {
		ctx.Err.Printf("/admin/conf/%s/review send %s (continuing): %s", conf.Tag, action.Letter, err)
	}

	// Refresh the pending list so we can advance to the next un-actioned
	// proposal. The cache invalidation in UpdateProposalStatus + the
	// in-place mutation we wired earlier means the next call sees this
	// proposal's new status and skips it.
	pending, _ := splitProposalsByPending(loadConfProposals(ctx, conf))
	next := nextProposalAfter(pending, proposalID)
	flash := fmt.Sprintf("%s — letter %q queued.", action.Label, action.Letter)
	if next != nil {
		http.Redirect(w, r,
			fmt.Sprintf("/admin/conf/%s/review?id=%s&flash=%s",
				conf.Tag, next.ID, url.QueryEscape(flash)),
			http.StatusSeeOther)
		return
	}
	// Queue empty — bounce to the conf dashboard with the success flash.
	http.Redirect(w, r,
		fmt.Sprintf("/admin/conf/%s/?flash=%s", conf.Tag, url.QueryEscape(flash+" Queue is now empty.")),
		http.StatusSeeOther)
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
// button. Centralized here so the template stays terse.
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
	}
	return "bg-gray-600 hover:bg-gray-700 text-white"
}

// ActionKey is the public accessor templates use to build form action
// URLs.
func (a reviewAction) ActionKey() string { return a.actionKey() }

func redirectReview(w http.ResponseWriter, r *http.Request, conf *types.Conf, proposalID, flash string) {
	http.Redirect(w, r,
		fmt.Sprintf("/admin/conf/%s/review?id=%s&flash=%s",
			conf.Tag, proposalID, url.QueryEscape(flash)),
		http.StatusSeeOther)
}
