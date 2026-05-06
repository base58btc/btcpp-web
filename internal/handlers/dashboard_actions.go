package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/missives"
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

// DashboardTalkDetails renders a read-only summary of a proposal —
// surfaced from the dashboard for talks in a terminal status
// (TheyDecline / WeDecline / Rejected) where editing is no longer
// applicable but the user may still want to look back at what they
// submitted: title / type / duration / description / setup notes /
// comments / fellow speakers / scheduled time (if any).
func DashboardTalkDetails(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, _, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard details: %s", err)
		return
	}
	// Attach ConfTalk + Recording for accepted-but-then-canceled cases —
	// if a talk got scheduled before being declined, the details page
	// should still show the time.
	if ct, err := getters.GetConfTalkByProposal(ctx, proposalID); err == nil && ct != nil {
		proposal.ConfTalk = ct
	}
	page := &TalkDetailsPage{
		Proposal: proposal,
		Conf:     proposal.ScheduleFor,
		Speakers: resolveProposalSpeakers(proposal),
		HMAC:     encHMAC,
		Email:    encEmail,
		Year:     helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_talk_details.tmpl", page); err != nil {
		ctx.Err.Printf("/dashboard details render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
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

	// Same gate the dashboard template uses to hide the "Edit speaker
	// info" link — keep them aligned so a link the user can see is
	// always backed by a form they can actually submit.
	locked := false
	lockReason := ""
	if conf == nil || !conf.CanInvite() {
		locked = true
		lockReason = "the conference is within 4 days (or no longer active) — speaker info is locked"
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

	// DaysList[0] is the setup day (one before StartDate) — this is
	// when the speakers' dinner happens, so it's what we surface in
	// the RSVP label. Mirrors the apply form's RSVPFor wiring.
	rsvpDayList := conf.DaysList("", true)
	rsvpFor := ""
	if len(rsvpDayList) > 0 {
		rsvpFor = rsvpDayList[0].ItemDesc
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
		RSVPFor:             rsvpFor,
		Year:                helpers.CurrentYear(),
	})
	if err != nil {
		ctx.Err.Printf("/dashboard edit-conf render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// DashboardEditSpeaker renders / handles the user's row in the
// Speakers DB. Auth is by magic-link email — the user can only edit
// the speaker row whose email matches the authed identity.
//
// Mode is "edit" when GetSpeakersByEmail returns at least one row; in
// that case the form's POST patches the existing row via UpdateSpeaker.
// Mode is "create" when there's no row yet — common for volunteer-
// or ticket-only contacts who want to add themselves to the speakers
// DB. The POST creates a new row via CreateSpeaker.
func DashboardEditSpeaker(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, encHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		ctx.Infos.Printf("/dashboard/speaker auth: %s", err)
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	encEmail := r.URL.Query().Get("em")

	speakers, err := getters.GetSpeakersByEmail(ctx.Notion, email)
	if err != nil {
		ctx.Err.Printf("/dashboard/speaker lookup %s: %s", email, err)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	var sp *types.Speaker
	if len(speakers) > 0 {
		sp = speakers[0]
	}

	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
		}
		if sp == nil {
			handleCreateSpeakerPOST(w, r, ctx, email, encHMAC, encEmail)
			return
		}
		handleUpdateSpeakerPOST(w, r, ctx, sp, encHMAC, encEmail)
		return
	}

	mode := "create"
	if sp != nil {
		mode = "edit"
	}
	page := &EditSpeakerPage{
		Speaker:      sp,
		HMAC:         encHMAC,
		Email:        encEmail,
		EmailPlain:   email,
		Mode:         mode,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_speaker.tmpl", page); err != nil {
		ctx.Err.Printf("/dashboard/speaker render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// handleUpdateSpeakerPOST applies the form fields to the existing
// Speaker row via the sparse SpeakerUpdate API. Empty fields are
// passed through, but the Notion library treats them as no-ops via
// speakerUpdateProps which builds the property map from non-empty
// strings + booleans.
func handleUpdateSpeakerPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, sp *types.Speaker, encHMAC, encEmail string) {
	picRaw, picContentType, picExt, picErr := readMultipartFile(r, "PicFile")
	hasNewPic := picErr == nil && len(picRaw) > 0
	if picErr != nil && picErr != http.ErrMissingFile {
		ctx.Err.Printf("/dashboard/speaker read pic: %s", picErr)
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape("Photo upload failed.")),
			http.StatusSeeOther)
		return
	}
	up := getters.SpeakerUpdate{
		Phone:     strings.TrimSpace(r.FormValue("Phone")),
		Signal:    strings.TrimSpace(r.FormValue("Signal")),
		Telegram:  strings.TrimSpace(r.FormValue("Telegram")),
		Twitter:   strings.TrimSpace(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		TShirt:    validShirtCode(strings.TrimSpace(r.FormValue("TShirt"))),
	}
	if hasNewPic {
		up.Photo = imgproc.ShortID(picRaw) + picExt
	}
	if err := getters.UpdateSpeaker(ctx.Notion, sp.ID, up); err != nil {
		ctx.Err.Printf("/dashboard/speaker update %s: %s", sp.ID, err)
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape("Update failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}
	if hasNewPic {
		// Patch the cached Speaker so the dashboard's next render
		// reflects the new photo without waiting for a periodic
		// refresh, then fire-and-forget the Spaces upload.
		sp.Photo = up.Photo
		go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Speaker info updated."), http.StatusSeeOther)
}

// handleCreateSpeakerPOST mints a new Speakers row for an
// authenticated user who didn't have one yet. The email is forced to
// the magic-link-authed value so a user can't create a profile with
// someone else's email.
func handleCreateSpeakerPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, email, encHMAC, encEmail string) {
	name := strings.TrimSpace(r.FormValue("Name"))
	if name == "" {
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape("Name is required.")),
			http.StatusSeeOther)
		return
	}
	picRaw, picContentType, picExt, picErr := readMultipartFile(r, "PicFile")
	hasNewPic := picErr == nil && len(picRaw) > 0
	if picErr != nil && picErr != http.ErrMissingFile {
		ctx.Err.Printf("/dashboard/speaker (create) read pic: %s", picErr)
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape("Photo upload failed.")),
			http.StatusSeeOther)
		return
	}
	// Mirror the talk-application form's required set: Name (already
	// checked above), Phone, Signal, Github, and a profile photo. The
	// browser-side `required` attrs gate normal submissions; this is
	// the server-side backstop for handcrafted POSTs.
	in := getters.SpeakerInput{
		Name:      name,
		Email:     email,
		Phone:     strings.TrimSpace(r.FormValue("Phone")),
		Signal:    strings.TrimSpace(r.FormValue("Signal")),
		Telegram:  strings.TrimSpace(r.FormValue("Telegram")),
		Twitter:   strings.TrimSpace(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		TShirt:    validShirtCode(strings.TrimSpace(r.FormValue("TShirt"))),
	}
	if missing := firstMissingProfileField(in.Phone, in.Signal, in.Github, hasNewPic); missing != "" {
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape(missing+" is required.")),
			http.StatusSeeOther)
		return
	}
	if hasNewPic {
		in.Photo = imgproc.ShortID(picRaw) + picExt
	}
	if _, err := getters.CreateSpeaker(ctx.Notion, in); err != nil {
		ctx.Err.Printf("/dashboard/speaker create %s: %s", email, err)
		http.Redirect(w, r,
			fmt.Sprintf("/dashboard/speaker?hr=%s&em=%s&flash=%s",
				encHMAC, encEmail, url.QueryEscape("Create failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}
	if hasNewPic {
		go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Profile created."), http.StatusSeeOther)
}

// firstMissingProfileField returns the user-facing label of the first
// required profile field that's empty, or "" when all are filled.
// hasPhoto is the boolean form because the photo lives outside the
// form's text values (multipart blob).
func firstMissingProfileField(phone, signal, github string, hasPhoto bool) string {
	if phone == "" {
		return "Phone"
	}
	if signal == "" {
		return "Signal"
	}
	if github == "" {
		return "Github"
	}
	if !hasPhoto {
		return "Photo"
	}
	return ""
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

// DashboardRemoveCoSpeaker drops one co-speaker from a talk's speakers
// list. Called from the × button on each non-self speaker pill on the
// dashboard.
//
// Removing yourself goes through DashboardWithdraw instead (which has
// the panel-vs-talk logic for what to do with the proposal). Refuses
// to remove the last speaker (would orphan the proposal — better to
// withdraw the talk outright). Refuses on terminal proposals and
// outside the conf's CanInvite window so the speaker list can't be
// edited after the program is locked in.
func DashboardRemoveCoSpeaker(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	vars := mux.Vars(r)
	proposalID := vars["proposalID"]
	targetSpeakerConfID := vars["speakerConfID"]

	proposal, requestingSC, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard remove co-speaker: %s", err)
		return
	}
	if targetSpeakerConfID == requestingSC.ID {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Use Withdraw to remove yourself from a talk."), http.StatusSeeOther)
		return
	}
	if isTerminalProposalStatus(proposal.Status) {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "This talk is finalized — co-speakers can't be removed."), http.StatusSeeOther)
		return
	}
	if proposal.ScheduleFor == nil || !proposal.ScheduleFor.CanInvite() {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "It's too close to the conference — contact us to remove a co-speaker."), http.StatusSeeOther)
		return
	}
	if len(proposal.SpeakerConfRefs) <= 1 {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Can't remove the last speaker — withdraw the talk instead."), http.StatusSeeOther)
		return
	}
	// Defend against tampered IDs: target must actually be on this proposal.
	onProposal := false
	for _, ref := range proposal.SpeakerConfRefs {
		if ref == targetSpeakerConfID {
			onProposal = true
			break
		}
	}
	if !onProposal {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "That speaker isn't on this talk."), http.StatusSeeOther)
		return
	}

	if err := getters.RemoveProposalFromSpeakerConf(ctx, targetSpeakerConfID, proposalID); err != nil {
		ctx.Err.Printf("/dashboard remove co-speaker %s from %s: %s", targetSpeakerConfID, proposalID, err)
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Couldn't remove co-speaker — please try again."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Co-speaker removed."), http.StatusSeeOther)
}

// DashboardConfirmTalk is the GET-friendly one-click variant of
// DashboardAcceptInvite — clicked from the talkinvited email's
// "Confirm Attendance" button. Validates the magic-link, runs the
// accept pipeline, redirects to /dashboard with a flash. Idempotent
// against re-clicks (already-accepted talks just flash a "thanks,
// already done" message).
func DashboardConfirmTalk(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, _, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard confirm: %s", err)
		return
	}
	if proposal.Status == StatusAccepted {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Already confirmed — thanks!"), http.StatusSeeOther)
		return
	}
	if isTerminalProposalStatus(proposal.Status) {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "This talk is no longer pending — please reach out if that's a surprise."), http.StatusSeeOther)
		return
	}
	res, err := newAcceptPipeline(ctx).AcceptProposal(proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard confirm pipeline: %s", err)
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Something went wrong confirming — please use the Accept button on your dashboard."), http.StatusSeeOther)
		return
	}
	// Side effects only on the fresh-accept path — re-clicks of the
	// email link have AlreadyAccepted=true and shouldn't re-send the
	// letter or re-issue tickets.
	if !res.AlreadyAccepted {
		fanoutAcceptedProposal(ctx, proposal, proposal.ScheduleFor)
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Talk confirmed! Your speaker ticket is on the way."), http.StatusSeeOther)
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
	flash := "Talk accepted! Your speaker ticket is on the way."
	if res.AlreadyAccepted {
		flash = "Talk was already accepted."
	} else {
		fanoutAcceptedProposal(ctx, proposal, proposal.ScheduleFor)
	}
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, flash), http.StatusSeeOther)
}

// DashboardInviteCoSpeaker renders the inviter-side "share a link"
// page: confirms which talk the invite is for and shows the URL the
// existing speaker copies to send their co-speaker.
//
// The recipient hits InviteSpeaker (a different handler, public, token-
// authed) which is where the speaker-side fields actually get written.
// This page just mints the URL.
//
// Refuses to mint links for terminal proposals or confs already inside
// the CanInvite window — same gates the dashboard template uses to
// decide whether to show the entry-point link in the first place.
func DashboardInviteCoSpeaker(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	proposal, _, encHMAC, encEmail, err := dashboardAuthForProposal(w, r, ctx, proposalID)
	if err != nil {
		ctx.Err.Printf("/dashboard invite: %s", err)
		return
	}
	conf := proposal.ScheduleFor

	if isTerminalProposalStatus(proposal.Status) {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "This talk is finalized — co-speakers can't be added."), http.StatusSeeOther)
		return
	}
	if conf == nil || !conf.CanInvite() {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "It's too close to the conference — contact us to add a co-speaker."), http.StatusSeeOther)
		return
	}

	// Lazy-mint a token on first invite. Persist it on the proposal so
	// the public invite-speaker handler can validate inbound URLs by
	// equality, and admins can revoke a leaked link by rotating or
	// clearing the field in Notion.
	if proposal.InviteToken == "" {
		token := helpers.MintInviteToken()
		if err := getters.SetProposalInviteToken(ctx, proposalID, token); err != nil {
			ctx.Err.Printf("/dashboard invite mint token: %s", err)
			http.Error(w, "Could not create invite link — please try again.", http.StatusInternalServerError)
			return
		}
		proposal.InviteToken = token
	}

	page := &InviteCoSpeakerPage{
		Proposal:  proposal,
		Conf:      conf,
		HMAC:      encHMAC,
		Email:     encEmail,
		InviteURL: helpers.InviteLink(ctx, proposalID, proposal.InviteToken),
		Year:      helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_invite.tmpl", page); err != nil {
		ctx.Err.Printf("/dashboard invite render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// InviteSpeaker is the public landing page the invited co-speaker
// hits via the shareable link. GET renders the same form the apply
// flow uses (embeds/talk.tmpl with InviteMode=true), with talk-content
// fields hidden — the proposal already exists. POST collects the full
// SpeakerConf payload (hometown / availability / org / dinner /
// recording opt-in / etc.) and runs it through JoinProposal, which
// upserts Speaker + Org and links the new SpeakerConf to the existing
// proposal.
//
// The token in the URL is matched against proposal.InviteToken — a
// random value stored on the Notion row. Admins revoke a leaked link
// by clearing or rotating the field in Notion; the next request 403s.
// Anyone with the link can submit — that's the point. The proposal
// can't be mutated beyond "add a speaker" via this path, so the blast
// radius of a leaked link is bounded.
func InviteSpeaker(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	proposalID := mux.Vars(r)["proposalID"]
	token := r.URL.Query().Get("t")

	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Error(w, "Talk not found.", http.StatusNotFound)
		return
	}
	if token == "" || proposal.InviteToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(proposal.InviteToken)) != 1 {
		http.Error(w, "Invalid or revoked invite link.", http.StatusForbidden)
		return
	}
	conf := proposal.ScheduleFor
	if isTerminalProposalStatus(proposal.Status) {
		http.Error(w, "This talk is finalized — no new speakers can be added.", http.StatusGone)
		return
	}
	if conf == nil || !conf.CanInvite() {
		http.Error(w, "It's too close to the conference to add a co-speaker.", http.StatusGone)
		return
	}

	confs := listConfs(w, ctx)

	if r.Method == http.MethodPost {
		handleInviteSpeakerPOST(w, r, ctx, proposal, conf, confs)
		return
	}

	daylist := conf.DaysList("days-", true)
	page := &SpeakerPage{
		Conf:             conf,
		Confs:            confs,
		ConfItems:        helpers.GetOtherConfs(confs, *conf),
		DaysList:         daylist[1:],
		RSVPFor:          daylist[0].ItemDesc,
		PresentationType: helpers.GetPresentationTypes(),
		RecordingOptions: helpers.GetRecordingOptions(),
		InviteMode:       true,
		InviteToken:      token,
		Proposal:         proposal,
		Year:             helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/talk.tmpl", page); err != nil {
		ctx.Err.Printf("/invite-speaker render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// handleInviteSpeakerPOST mirrors the multipart-form handling in
// RenderSpeakerConf's POST branch but routes through JoinProposal
// (no new Proposal, attach to the inviter's existing one). The submit
// pipeline returns ErrSpeakerApp-shaped responses on failure so HTMX
// renders the inline error block in the form.
func handleInviteSpeakerPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, proposal *types.Proposal, conf *types.Conf, confs []*types.Conf) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		ctx.Err.Printf("/invite-speaker parseform: %s", err)
		w.Write([]byte(helpers.ErrSpeakerApp("Error parsing form.")))
		return
	}
	dec := newFormDecoder()
	var talkapp types.TalkApp
	if err := dec.Decode(&talkapp, r.PostForm); err != nil {
		ctx.Err.Printf("/invite-speaker decode: %s", err)
		w.Write([]byte(helpers.ErrSpeakerApp("Unable to register you: form parsing error")))
		return
	}
	talkapp.ParseAvailability("days-", r.PostForm)
	talkapp.DinnerRSVP = r.PostForm.Get("DinnerOpt") == "Yes"
	talkapp.OtherEvents = helpers.ParseFormConfs("conf-", r.PostForm, confs)
	talkapp.ScheduleFor = conf

	if alreadyOnProposal(proposal, talkapp.Email) {
		w.Write([]byte(helpers.ErrSpeakerApp("You're already a speaker on this talk.")))
		return
	}

	picRaw, picContentType, picExt, err := readMultipartFile(r, "PicFile")
	hasNewPic := err == nil && len(picRaw) > 0
	if err != nil && err != http.ErrMissingFile {
		ctx.Err.Printf("/invite-speaker read pic: %s", err)
		w.Write([]byte(helpers.ErrSpeakerApp("Error uploading pfp.")))
		return
	}
	if hasNewPic {
		talkapp.NormPhoto = imgproc.ShortID(picRaw) + picExt
	}

	logoRaw, logoContentType, logoExt, logoErr := readMultipartFile(r, "OrgLogoFile")
	hasLogo := logoErr == nil && len(logoRaw) > 0
	if logoErr != nil && logoErr != http.ErrMissingFile {
		ctx.Err.Printf("/invite-speaker read logo: %s", logoErr)
		w.Write([]byte(helpers.ErrSpeakerApp("Error uploading org logo.")))
		return
	}
	if hasLogo {
		talkapp.OrgLogo = imgproc.ShortID(logoRaw) + logoExt
	}

	res, err := newSubmitPipeline(ctx).JoinProposal(&talkapp, proposal.ID)
	if err != nil {
		ctx.Err.Printf("/invite-speaker join pipeline: %s", err)
		if errors.Is(err, ErrDuplicateSpeakerEmail) {
			w.Write([]byte(helpers.ErrSpeakerApp("That email already has multiple speaker records — please contact us to resolve.")))
		} else {
			w.Write([]byte(helpers.ErrSpeakerApp("Unable to add you as a co-speaker.")))
		}
		return
	}

	// Mirror the inverse Proposal → SpeakerConf relation. Non-fatal —
	// JoinProposal already wrote the canonical edge.
	if err := getters.AddSpeakerConfToProposal(ctx, proposal.ID, res.SpeakerConfID); err != nil {
		ctx.Err.Printf("/invite-speaker mirror to proposal (continuing): %s", err)
	}

	// Fire-and-forget Spaces uploads, same as the apply form.
	if hasNewPic {
		go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
	}
	if hasLogo {
		go newPhotoPipeline(ctx).mirrorOrgLogoToSpaces(logoRaw, logoContentType, logoExt)
	}

	// Newsletter subscription mirrors the apply form's "talkapp" list
	// for this conf — the new speaker gets the same conf-specific
	// updates as someone who applied directly.
	newslist := missives.MakeApplicationSublist(conf.Tag, "talkapp", talkapp.Subscribe)
	if err := missives.NewSubs(ctx, talkapp.Email, newslist); err != nil {
		ctx.Err.Printf("/invite-speaker newsletter sub (continuing): %s", err)
	}

	// HTMX swaps innerHTML of #result with this content; we render a
	// success message that includes a link to the dashboard so the
	// user can keep going.
	dashURL := helpers.EmailLink(ctx, talkapp.Email, "/dashboard")
	w.Write([]byte(helpers.SuccessApp(fmt.Sprintf(
		`You've been added to "%s"! <a href="%s" class="underline font-semibold">Open your dashboard &rarr;</a>`,
		proposal.Title, dashURL,
	))))
}

// alreadyOnProposal reports whether the email is already linked to this
// proposal via one of its existing speakers. Case-insensitive match —
// Notion stores email as text and casing varies.
func alreadyOnProposal(p *types.Proposal, email string) bool {
	for _, sc := range p.Speakers {
		if sc == nil || sc.Speaker == nil {
			continue
		}
		if strings.EqualFold(sc.Speaker.Email, email) {
			return true
		}
	}
	return false
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
