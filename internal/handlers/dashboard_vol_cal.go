package handlers

import (
	"fmt"
	"net/http"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

// DashboardVolShiftICS serves a downloadable .ics for one volunteer
// shift, gated on the dashboard's hr+em magic-link params. Used by
// the "Add to calendar" pill next to each shift on /dashboard so a
// volunteer can re-add a single shift to their calendar without
// triggering an email round-trip through the mailer.
//
// METHOD:PUBLISH with no ATTENDEE list — distinct UID stem from
// the original per-vol shift invite so a volunteer who also keeps
// the emailed invite ends up with the same event in their
// calendar (clients match on UID; the public-feed UID would be a
// second entry, so we deliberately use a different stem here too).
//
// Authorization: email is on the shift's AssigneesRef. The HMAC
// guards against URL forgery, the assignee check guards against
// pulling a co-worker's shift.
//
// Path: GET /dashboard/vol/{shiftRef}/calendar.ics?hr=&em=
func DashboardVolShiftICS(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	shiftRef := mux.Vars(r)["shiftRef"]
	if shiftRef == "" {
		http.NotFound(w, r)
		return
	}

	allShifts, err := getters.FetchShiftsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/dashboard/vol/%s/calendar.ics shifts: %s", shiftRef, err)
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		return
	}
	var shift *types.WorkShift
	for _, s := range allShifts {
		if s != nil && s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil || shift.Conf == nil || shift.ShiftTime == nil || shift.ShiftTime.End == nil {
		http.NotFound(w, r)
		return
	}

	// Authorize: the requesting email must match a volunteer
	// who's been assigned to this shift. Read the vol roster for
	// the conf and check email → vol.Ref → shift.AssigneesRef.
	vols, err := getters.ListVolunteersForConf(ctx, shift.Conf.Ref)
	if err != nil {
		ctx.Err.Printf("/dashboard/vol/%s/calendar.ics vols: %s", shiftRef, err)
		http.Error(w, "Unable to verify assignment", http.StatusInternalServerError)
		return
	}
	authorized := false
	for _, v := range vols {
		if v == nil || v.Email != email {
			continue
		}
		for _, ref := range shift.AssigneesRef {
			if ref == v.Ref {
				authorized = true
				break
			}
		}
		break
	}
	if !authorized {
		http.NotFound(w, r) // same response as "no such shift" — don't leak existence
		return
	}

	conf := shift.Conf
	desc := ""
	if shift.Type != nil {
		desc = shift.Type.LongDesc
	}
	event := ics.Event{
		Method:        ics.MethodPublish,
		UID:           ics.NewUID("shift-self", shift.Ref),
		Sequence:      0,
		Status:        ics.StatusConfirmed,
		Summary:       "vol shift @ btc++: " + shift.Name,
		Description:   desc,
		Location:      conf.Venue,
		Start:         shift.ShiftTime.Start,
		End:           *shift.ShiftTime.End,
		TZ:            conf.Loc(),
		Organizer:     ics.ReplyToShift,
		OrganizerName: ics.ReplyToShiftName,
	}
	icsBytes := ics.Render(event)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-shift-%s.ics"`,
		conf.Tag, shiftRef[:8]))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(icsBytes)
}

// DashboardVolShiftsResend re-fires the per-vol DispatchShiftICS
// to the calling volunteer for every shift they're assigned to at
// the given conf. Used by the "Resend Cal Invites for Shifts"
// button on /dashboard — recovery path for a volunteer who missed
// or dismissed the original invites and wants them re-emailed.
//
// force=true so the dispatch's hash-unchanged short-circuit
// doesn't suppress the resend (the explicit click is the intent;
// shift CalNotif advances SEQUENCE by one per shift).
//
// Path: POST /dashboard/vol/{conf}/shifts/resend-invites?hr=&em=
func DashboardVolShiftsResend(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, encHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	encEmail := r.URL.Query().Get("em")
	confTag := mux.Vars(r)["conf"]
	if confTag == "" {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Missing conf"), http.StatusSeeOther)
		return
	}

	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		ctx.Err.Printf("/dashboard/vol/%s/shifts/resend-invites list vols: %s", confTag, err)
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Lookup failed"), http.StatusSeeOther)
		return
	}
	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Not a volunteer at that event"), http.StatusSeeOther)
		return
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/dashboard/vol/%s/shifts/resend-invites confs: %s", confTag, err)
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Lookup failed"), http.StatusSeeOther)
		return
	}
	var conf *types.Conf
	for _, c := range confs {
		if c != nil && c.Tag == confTag {
			conf = c
			break
		}
	}
	if conf == nil {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Unknown conf"), http.StatusSeeOther)
		return
	}

	// vol.WorkShifts is already populated by the dashboard
	// load. Refetch the canonical shift list so re-sends pick
	// up fresh ShiftTime values (in case the volcoord moved
	// shifts since the dashboard render).
	confShifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		ctx.Err.Printf("/dashboard/vol/%s/shifts/resend-invites shifts: %s", confTag, err)
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "Lookup failed"), http.StatusSeeOther)
		return
	}
	mine := getSelectedShifts(vol, confShifts)
	if len(mine) == 0 {
		http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, "No shifts assigned at that event"), http.StatusSeeOther)
		return
	}

	recipient := ics.Attendee{Email: vol.Email, Name: vol.Name}
	sent := 0
	for _, shift := range mine {
		if shift.ShiftTime == nil || shift.ShiftTime.End == nil {
			continue
		}
		if dErr := DispatchShiftICS(ctx, shift, conf, []ics.Attendee{recipient}, kindRequest, true); dErr != nil {
			ctx.Err.Printf("/dashboard/vol/%s/shifts/resend-invites shift %q: %s", confTag, shift.Name, dErr)
			continue
		}
		sent++
	}

	flash := fmt.Sprintf("Re-sent %d shift cal invite(s) to %s.", sent, vol.Email)
	http.Redirect(w, r, dashboardRedirect(encHMAC, encEmail, flash), http.StatusSeeOther)
}

