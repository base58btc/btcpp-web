package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

// OrgSearch returns up to 10 orgs whose name contains the `q` query
// parameter, as JSON `[{id, name, website}, ...]`. Used by the org
// autocomplete on the speaker-info editor.
//
// Public (no HMAC) — org names + websites are already shown publicly on
// conf pages. Empty queries return an empty list.
func OrgSearch(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	q := r.URL.Query().Get("q")
	orgs, err := getters.SearchOrgsByName(ctx.Notion, q, 10)
	if err != nil {
		ctx.Err.Printf("/api/orgs/search: %s", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	type orgHit struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Website string `json:"website,omitempty"`
	}
	out := make([]orgHit, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, orgHit{ID: o.Ref, Name: o.Name, Website: o.Website})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		ctx.Err.Printf("/api/orgs/search encode: %s", err)
	}
}

// dashboardAuthForProposal validates the magic-link HMAC and confirms the
// authed email is one of the speakers on the given proposal. Returns the
// proposal, the user's SpeakerConf for it, and the encoded HMAC/email so
// callers can build redirect URLs.
//
// Errors are written to `w` directly — callers can return immediately on a
// non-nil error.
func dashboardAuthForProposal(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, proposalID string) (*types.Proposal, *types.SpeakerConf, string, string, error) {
	email, encHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return nil, nil, "", "", fmt.Errorf("auth: %w", err)
	}
	encEmail := r.URL.Query().Get("em")

	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return nil, nil, "", "", fmt.Errorf("load proposal %s: %w", proposalID, err)
	}

	_, scs, err := getters.GetSpeakerConfsByEmail(ctx, email)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return nil, nil, "", "", fmt.Errorf("speakerconfs by email: %w", err)
	}
	for _, sc := range scs {
		for _, p := range sc.Proposals {
			if p != nil && p.ID == proposalID {
				return proposal, sc, encHMAC, encEmail, nil
			}
		}
	}
	http.Error(w, "you don't have access to this talk", http.StatusForbidden)
	return nil, nil, "", "", fmt.Errorf("email %s not on proposal %s", email, proposalID)
}

// DashboardEditProposal renders / handles the speaker-side proposal editor.
//
// GET: load the proposal, check the edit-lock, render the edit form.
// POST: same auth/lock checks, then UpdateProposal and redirect to the
// dashboard with a flash.
func DashboardEditProposal(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, _, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard edit: %s", err)
		return
	}

	confTalk, err := getters.GetConfTalkByProposal(ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard edit get conftalk: %s", err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	locked, lockReason := proposalEditLocked(proposal, confTalk)

	if r.Method == http.MethodPost {
		if locked {
			http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Edits are locked: "+lockReason), http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			ctx.Err.Printf("/dashboard edit parseform: %s", err)
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
			ctx.Err.Printf("/dashboard edit update: %s", err)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Talk updated."), http.StatusSeeOther)
		return
	}

	// Speakers can self-select talk / workshop / panel. Keynote and
	// hackathon are admin-set; if the proposal already has one of those
	// types, prepend it so saving the form doesn't silently downgrade it.
	talkTypes := []string{"talk", "workshop", "panel"}
	if t := proposal.TalkType; t != "" {
		known := false
		for _, allowed := range talkTypes {
			if allowed == t {
				known = true
				break
			}
		}
		if !known {
			talkTypes = append([]string{t}, talkTypes...)
		}
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_talk.tmpl", &EditProposalPage{
		Proposal:   proposal,
		Conf:       proposal.ScheduleFor,
		HMAC:       encHMAC,
		Email:      encEmail,
		Locked:     locked,
		LockReason: lockReason,
		TalkTypes:  talkTypes,
		Durations:  []int{5, 20, 30, 45, 60, 90, 120, 180},
		Year:       helpers.CurrentYear(),
	})
	if err != nil {
		ctx.Err.Printf("/dashboard edit render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// DashboardEditSpeakerConf renders / handles the per-conference speaker-info
// editor: the user's per-conf SpeakerConf data (hometown, avails, recording
// OK, company, dietary, etc.), shared across all their talks at that conf.
//
// GET shows the form; POST writes the update via UpdateSpeakerConf. Same
// 7-day edit lock as the proposal editor, but conf-wide rather than
// per-talk: once the conf is within a week, speaker info is frozen.
func DashboardEditSpeakerConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confTag := mux.Vars(r)["confTag"]
	email, encHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		ctx.Err.Printf("/dashboard edit-conf auth: %s", err)
		return
	}
	encEmail := r.URL.Query().Get("em")

	_, scs, err := getters.GetSpeakerConfsByEmail(ctx, email)
	if err != nil {
		ctx.Err.Printf("/dashboard edit-conf load: %s", err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	var target *types.SpeakerConf
	var conf *types.Conf
	for _, sc := range scs {
		if c := speakerConfConf(sc); c != nil && c.Tag == confTag {
			target = sc
			conf = c
			break
		}
	}
	if target == nil {
		http.Error(w, "no speaker record at that conf", http.StatusForbidden)
		return
	}

	locked := false
	lockReason := ""
	if conf != nil && time.Until(conf.StartDate) <= 7*24*time.Hour {
		locked = true
		lockReason = "the conference is within 7 days — speaker info is locked"
	}

	if r.Method == http.MethodPost {
		if locked {
			http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Edits are locked: "+lockReason), http.StatusSeeOther)
			return
		}
		// Multipart so we can accept an optional OrgLogoFile upload along
		// with the other fields. 10MB cap mirrors the apply form.
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			ctx.Err.Printf("/dashboard edit-conf parseform: %s", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		fields := getters.SpeakerConfFields{
			Company:      r.PostForm.Get("Company"),
			OrgID:        r.PostForm.Get("OrgID"),
			ComingFrom:   r.PostForm.Get("ComingFrom"),
			Availability: r.PostForm["Availability"],
			RecordOK:     r.PostForm.Get("RecordOK"),
			Visa:         r.PostForm.Get("Visa"),
			FirstEvent:   r.PostForm.Get("FirstEvent") == "on",
			DinnerRSVP:   r.PostForm.Get("DinnerRSVP") == "on",
			Sponsor:      r.PostForm.Get("Sponsor") == "on",
		}
		// Optional org logo upload — present only if the user picked a
		// file. Empty fields.OrgPhoto leaves the existing filename intact.
		logoRaw, logoContentType, logoExt, logoErr := readMultipartFile(r, "OrgLogoFile")
		hasLogo := logoErr == nil && len(logoRaw) > 0
		if logoErr != nil && logoErr != http.ErrMissingFile {
			ctx.Err.Printf("/dashboard edit-conf read logo: %s", logoErr)
			http.Error(w, "logo upload failed", http.StatusBadRequest)
			return
		}
		if hasLogo {
			fields.OrgPhoto = imgproc.ShortID(logoRaw) + logoExt
		}
		if err := getters.UpdateSpeakerConf(ctx, target.ID, fields); err != nil {
			ctx.Err.Printf("/dashboard edit-conf update: %s", err)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		// Mirror the new logo to Spaces — fire-and-forget to keep the
		// redirect snappy. Spaces upload is the slow part; the Notion
		// write already completed.
		if hasLogo {
			go newPhotoPipeline(ctx).mirrorOrgLogoToSpaces(logoRaw, logoContentType, logoExt)
		}
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Speaker info updated."), http.StatusSeeOther)
		return
	}

	// Hide the "first bitcoin++" checkbox when the speaker has any prior
	// purchase row — same logic the apply form uses. Best-effort.
	var returning bool
	if reg, err := getters.EmailHasRegistration(ctx, email); err == nil {
		returning = reg
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_speakerconf.tmpl", &EditSpeakerConfPage{
		SpeakerConf:         target,
		Conf:                conf,
		HMAC:                encHMAC,
		Email:               encEmail,
		Locked:              locked,
		LockReason:          lockReason,
		// No prefix — Notion stores Avails option values as bare dates
		// (the apply form strips its "days-" prefix before saving), so
		// the edit form matches that format directly for both the
		// pre-check and the round-trip.
		DaysList:            conf.DaysList("", false),
		RecordingOptions:    helpers.GetRecordingOptions(),
		IsReturningAttendee: returning,
		Year:                helpers.CurrentYear(),
	})
	if err != nil {
		ctx.Err.Printf("/dashboard edit-conf render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// DashboardWithdraw lets a logged-in speaker withdraw from a proposal.
//
// For panels, only that speaker is removed (proposal stays). For every
// other talk type, the whole proposal flips to TheyDecline since one speaker
// withdrawing kills a single-presenter talk and is also taken to mean the
// session as a whole isn't happening.
//
// Refuses to act on already-terminal proposals (Accepted/declined/rejected).
func DashboardWithdraw(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, speakerConf, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard withdraw: %s", err)
		return
	}
	if isTerminalProposalStatus(proposal.Status) {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Talk is already in a final state."), http.StatusSeeOther)
		return
	}

	flash := ""
	if proposal.TalkType == "panel" {
		if err := getters.RemoveProposalFromSpeakerConf(ctx, speakerConf.ID, proposalID); err != nil {
			ctx.Err.Printf("/dashboard withdraw remove panelist: %s", err)
			http.Error(w, "withdraw failed", http.StatusInternalServerError)
			return
		}
		flash = "You've been removed from the panel."
	} else {
		if err := getters.UpdateProposalStatus(ctx, proposalID, "TheyDecline"); err != nil {
			ctx.Err.Printf("/dashboard withdraw update status: %s", err)
			http.Error(w, "withdraw failed", http.StatusInternalServerError)
			return
		}
		flash = "Your talk has been withdrawn."
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, flash), http.StatusSeeOther)
}

// DashboardAcceptInvite promotes an Invited proposal to Accepted, creating
// the ConfTalk row via the existing acceptPipeline. First speaker to click
// promotes the whole talk; subsequent clicks short-circuit because
// AcceptProposal returns AlreadyAccepted.
func DashboardAcceptInvite(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, _, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard accept: %s", err)
		return
	}
	if proposal.Status != "Invited" && proposal.Status != StatusAccepted {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "This talk isn't currently invited — nothing to accept."), http.StatusSeeOther)
		return
	}
	res, err := newAcceptPipeline(ctx).AcceptProposal(proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard accept pipeline: %s", err)
		http.Error(w, "accept failed", http.StatusInternalServerError)
		return
	}
	flash := "Talk accepted! We'll be in touch with scheduling details."
	if res.AlreadyAccepted {
		flash = "Talk was already accepted."
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, flash), http.StatusSeeOther)
}

// isTerminalProposalStatus returns true for statuses where the speaker has
// no further actions to take (the talk is done one way or the other).
func isTerminalProposalStatus(status string) bool {
	switch status {
	case StatusAccepted, "TheyDecline", "WeDecline", "Rejected":
		return true
	}
	return false
}

// proposalEditLocked reports whether speaker-side edits are frozen for this
// proposal. Edits lock once a ConfTalk exists with a scheduled time AND the
// conf is within 7 days of starting (or already past) — we don't want
// last-minute changes appearing in printed schedules / the live website.
//
// Until then, speakers can edit freely (Applied/InReview/Invited). Anything
// terminal but un-promoted (TheyDecline / WeDecline / Rejected) is also
// locked since there's nothing to edit.
func proposalEditLocked(proposal *types.Proposal, confTalk *types.ConfTalk) (bool, string) {
	if proposal == nil {
		return true, "unknown talk"
	}
	if isTerminalProposalStatus(proposal.Status) && proposal.Status != StatusAccepted {
		return true, "this talk has been finalized — contact us to make changes"
	}
	if confTalk == nil || confTalk.Sched == nil {
		return false, ""
	}
	if proposal.ScheduleFor == nil {
		return false, ""
	}
	if time.Until(proposal.ScheduleFor.StartDate) <= 7*24*time.Hour {
		return true, "the conference is within 7 days — talk details are locked"
	}
	return false, ""
}

func dashboardRedirect(encHMAC, encEmail, flash string) string {
	q := url.Values{}
	if encHMAC != "" {
		q.Set("hr", encHMAC)
	}
	if encEmail != "" {
		q.Set("em", encEmail)
	}
	if flash != "" {
		q.Set("flash", flash)
	}
	return "/dashboard?" + q.Encode()
}
