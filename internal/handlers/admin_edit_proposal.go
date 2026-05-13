package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

// AdminEditProposalPage feeds the admin-side proposal-edit form.
// Distinct from EditProposalPage (the dashboard speaker editor)
// because the form action + Back link + allowed talk-types differ.
// Admins can set keynote / hackathon (speaker-side form clamps to
// talk / workshop / panel).
type AdminEditProposalPage struct {
	Conf       *types.Conf
	Proposal   *types.Proposal
	TalkTypes  []string
	Durations  []int
	Flash      string
	FlashErr   string
	Year       uint
	// ReturnURL is the page to bounce back to after a save —
	// /{conf}/admin/applicants by default, /{conf}/admin/schedule
	// when the admin came from the schedule grid. Threaded
	// through ?return=…
	ReturnURL  string
}

// AdminEditProposal serves the admin proposal editor. GET renders
// the form pre-filled from the proposal; POST validates + calls the
// same UpdateProposal helper the speaker-side dashboard editor uses.
// requireConfAdmin (not staff) — content edits ripple into Notion
// and reach attendees via re-rendered cards / re-sent cal invites,
// so keep the surface tight.
//
// Admins skip the proposalEditLocked gate entirely — they can edit
// Scheduled talks too (e.g., fixing a typo in the title pre-event).
// The next admin-driven "Send cal updates" then propagates the
// change to attendees.
//
// Path: GET/POST /{conf}/admin/proposal/{proposalID}/edit?return=...
func AdminEditProposal(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := mux.Vars(r)["proposalID"]
	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, url.QueryEscape("Proposal not found.")),
			http.StatusSeeOther)
		return
	}

	returnURL := r.URL.Query().Get("return")
	if returnURL == "" || !safeAdminReturn(returnURL, conf.Tag) {
		returnURL = fmt.Sprintf("/%s/admin/applicants", conf.Tag)
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		desiredDur, _ := strconv.Atoi(r.PostForm.Get("DesiredDuration"))
		availDur := desiredDur
		if a, err := strconv.Atoi(r.PostForm.Get("AvailDuration")); err == nil && a > 0 {
			availDur = a
		}
		in := getters.ProposalInput{
			Title:           r.PostForm.Get("Title"),
			Description:     r.PostForm.Get("Description"),
			Setup:           r.PostForm.Get("Setup"),
			Comments:        r.PostForm.Get("Comments"),
			TalkType:        r.PostForm.Get("TalkType"),
			DesiredDuration: desiredDur,
			AvailDuration:   availDur,
		}
		if err := getters.UpdateProposal(ctx, proposalID, in); err != nil {
			ctx.Err.Printf("/%s/admin/proposal/%s/edit update: %s", conf.Tag, proposalID, err)
			renderAdminEditProposal(w, ctx, &AdminEditProposalPage{
				Conf:      conf,
				Proposal:  proposal,
				TalkTypes: adminTalkTypes(proposal.TalkType),
				Durations: adminTalkDurations,
				FlashErr:  "Couldn't save — see server logs.",
				ReturnURL: returnURL,
				Year:      helpers.CurrentYear(),
			})
			return
		}
		http.Redirect(w, r,
			returnURL+"?flash="+url.QueryEscape("Talk updated: "+in.Title),
			http.StatusSeeOther)
		return
	}

	renderAdminEditProposal(w, ctx, &AdminEditProposalPage{
		Conf:      conf,
		Proposal:  proposal,
		TalkTypes: adminTalkTypes(proposal.TalkType),
		Durations: adminTalkDurations,
		Flash:     r.URL.Query().Get("flash"),
		ReturnURL: returnURL,
		Year:      helpers.CurrentYear(),
	})
}

func renderAdminEditProposal(w http.ResponseWriter, ctx *config.AppContext, page *AdminEditProposalPage) {
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/edit_proposal.tmpl", page); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/proposal/%s/edit render: %s", page.Conf.Tag, page.Proposal.ID, err)
	}
}

// adminTalkDurations is the dropdown for the admin form. Same values
// the speaker-side form offers — keynote / hackathon don't get a
// special bucket here; the duration is independent of type.
var adminTalkDurations = []int{5, 20, 30, 45, 60, 90, 120, 180}

// adminTalkTypes returns the talk-type options for the admin form.
// Speakers can set talk / workshop / panel; admins additionally get
// keynote + hackathon. Pre-pends the proposal's current type if
// it's somehow unrecognized so saving doesn't silently downgrade.
func adminTalkTypes(current string) []string {
	out := []string{"talk", "workshop", "panel", "keynote", "hackathon"}
	if current == "" {
		return out
	}
	for _, t := range out {
		if t == current {
			return out
		}
	}
	return append([]string{current}, out...)
}

// safeAdminReturn whitelists return-URL prefixes — guards against
// open-redirect from a hand-crafted ?return=https://evil.com.
// Only /{conf}/admin* paths for the same conf are accepted; anything
// else falls back to /{conf}/admin/applicants in the caller.
func safeAdminReturn(target, confTag string) bool {
	prefix := "/" + confTag + "/admin"
	if target == prefix {
		return true
	}
	if len(target) > len(prefix) && target[:len(prefix)+1] == prefix+"/" {
		return true
	}
	return false
}
