package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/types"
)

// computeScheduleDrift returns true when the ConfTalk's CalNotif
// hash doesn't match the freshly-computed ContentHash of the
// current data — i.e. the schedule UI shows a state that hasn't
// been propagated to attendees' calendars yet. False when there's
// no prior CalNotif (no baseline to drift from — talk hasn't been
// invited out yet) or when the hashes match (in sync).
func computeScheduleDrift(ct *types.ConfTalk, p *types.Proposal, conf *types.Conf) bool {
	if ct == nil || ct.Sched == nil || ct.Sched.End == nil {
		return false
	}
	prev, ok := ics.ParseCalNotif(ct.CalNotif)
	if !ok || prev.HashHex == "" {
		return false
	}
	cur := ics.ContentHash(ct.Sched.Start, *ct.Sched.End, conf.Tag, p.Title)
	return cur != prev.HashHex
}

// ScheduleSendCalUpdates handles the "Send Cal Invites" button on
// the /admin/schedule page. Does two things in one pass over every
// proposal placed on the schedule grid:
//
//   - Accepted, ConfTalk.Sched != nil → first-send. Dispatch the
//     REQUEST and flip Accepted → Scheduled so subsequent edits
//     show up as drift.
//   - Scheduled, drifted (hash != CalNotif hash) → update. Dispatch
//     a force=false REQUEST; matching cards skip via the
//     hash-unchanged short-circuit inside dispatch.
//
// Proposals that don't have a ConfTalk on the grid yet are ignored
// — nothing to send for them. Everything else (Invited, Waitlisted,
// terminal) is skipped defensively.
//
// Path: POST /{conf}/admin/schedule/sendcal-updates
func ScheduleSendCalUpdates(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	proposals := loadConfProposals(ctx, conf)
	var firstSent, updateSent, skippedClean, failed int
	for _, p := range proposals {
		if p == nil {
			continue
		}
		if p.Status != StatusAccepted && p.Status != StatusScheduled {
			continue
		}
		ct := getters.FetchConfTalkByProposal(p.ID)
		// Only act on talks that are placed on the grid —
		// no Sched means no event to invite anyone to.
		if ct == nil || ct.Sched == nil {
			continue
		}
		isFirstSend := p.Status == StatusAccepted
		// Already-Scheduled talks only fire when content drift
		// is detected; otherwise the invite already in
		// attendees' calendars is current.
		if !isFirstSend && !computeScheduleDrift(ct, p, conf) {
			skippedClean++
			continue
		}
		speakers := proposalSpeakers(p)
		if err := DispatchTalkICSForProposal(ctx, p, conf, speakers, false); err != nil {
			ctx.Err.Printf("/%s/admin/schedule/sendcal-updates %q: %s", conf.Tag, p.Title, err)
			failed++
			continue
		}
		if isFirstSend {
			firstSent++
			if err := getters.UpdateProposalStatus(ctx, p.ID, StatusScheduled); err != nil {
				ctx.Err.Printf("/%s/admin/schedule/sendcal-updates %q status flip: %s",
					conf.Tag, p.Title, err)
			}
		} else {
			updateSent++
		}
	}

	flash := fmt.Sprintf("Cal invites: %d first-sends · %d drift updates · %d clean", firstSent, updateSent, skippedClean)
	if failed > 0 {
		flash = fmt.Sprintf("%s · %d failed (see logs)", flash, failed)
	}
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/schedule?flash=%s",
			conf.Tag, url.QueryEscape(flash)),
		http.StatusSeeOther)
}

// schedulableStatuses lists the proposal statuses that should appear
// on the schedule UI. Anything terminal (rejected/declined) is filtered
// out — those talks won't run, no point dragging them around.
//
// Scheduled is included so already-invited talks stay visible (and
// editable — drag/resize on the grid is allowed; the per-card drift
// indicator + "Send Cal Updates" button is how those mutations
// propagate to attendees' calendars).
var schedulableStatuses = map[string]bool{
	"":              true, // pending review
	"Applied":       true,
	"InReview":      true,
	"Waitlisted":    true,
	"Invited":       true,
	StatusAccepted:  true,
	StatusScheduled: true,
}

// schedulePxPerMin is the grid's vertical scale. 2 px/min → a 30-minute
// talk renders as 60px tall; an 8-hour day fits in 960px (scrollable).
// Drag-drop resolution rounds Y-position to the nearest 5-minute slot.
const schedulePxPerMin = 2
const scheduleSnapMin = 15

// ScheduleConf renders the drag-and-drop schedule page at
// /admin/{tag}/schedule.
func ScheduleConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	page, err := buildSchedulePage(ctx, conf)
	if err != nil {
		ctx.Err.Printf("/%s/admin/schedule build: %s", conf.Tag, err)
		http.Error(w, "Unable to build schedule", http.StatusInternalServerError)
		return
	}
	page.FlashMessage = r.URL.Query().Get("flash")

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/schedule.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/schedule render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// SchedulePlace handles a drag-drop "place this talk in the grid"
// request. Body is JSON: {proposalID, day, venue, startMin}. We
// compute the end from the proposal's DesiredDuration (or a default),
// upsert the ConfTalk row, and redirect to the schedule page.
//
// On first placement: creates a new ConfTalk row with Sched + Venue
// already populated. On subsequent moves of an already-placed talk:
// patches the existing ConfTalk's Sched + Venue.
func SchedulePlace(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	var req struct {
		ProposalID string `json:"proposalID"`
		Day        int    `json:"day"`
		Venue      string `json:"venue"`
		StartMin   int    `json:"startMin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.ProposalID == "" || req.Day < 1 || req.Venue == "" {
		http.Error(w, "missing proposalID / day / venue", http.StatusBadRequest)
		return
	}

	proposal, err := getters.GetProposal(ctx, req.ProposalID)
	if err != nil || proposal == nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return
	}
	if !schedulableStatuses[proposal.Status] {
		http.Error(w, "proposal status doesn't allow scheduling", http.StatusConflict)
		return
	}

	dayDate := dayDateFor(conf, req.Day)

	// Preserve any existing actual duration when the talk is just
	// being moved — only fall back to scheduledDurationFor on the
	// very first placement (no ConfTalk yet). scheduledDurationFor
	// adds the Q&A buffer for talks (20m → 30m, 30m → 45m).
	dur := scheduledDurationFor(proposal)
	if dur <= 0 {
		dur = 30 // fallback when the applicant didn't specify
	}
	confTalkID := ""
	existing := getters.FetchConfTalkByProposal(req.ProposalID)
	if existing != nil {
		confTalkID = existing.ID
		if existing.Sched != nil && existing.Sched.End != nil {
			if d := endMinutesAfterStart(existing.Sched.Start, *existing.Sched.End, dur); d > 0 {
				dur = d
			}
		}
	}

	if existing == nil {
		newID, err := getters.CreateConfTalk(ctx, getters.ConfTalkInput{
			ConfTag:    conf.Tag,
			ProposalID: req.ProposalID,
		})
		if err != nil {
			ctx.Err.Printf("/%s/admin/schedule create conftalk: %s", conf.Tag, err)
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		confTalkID = newID
	}

	startTime := dayDate.Add(time.Duration(req.StartMin) * time.Minute)
	endTime := startTime.Add(time.Duration(dur) * time.Minute)

	if err := getters.UpdateConfTalkSchedule(ctx, confTalkID, req.Venue, startTime, endTime); err != nil {
		ctx.Err.Printf("/%s/admin/schedule update sched: %s", conf.Tag, err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	// No auto-fire. Schedule edits are draft-mode work; the
	// admin commits to attendees by clicking "Send Cal Invite"
	// on the proposal card (Accepted → Scheduled) or "Send Cal
	// Updates" on the schedule page (re-fan-out drifted
	// Scheduled talks). Drift is surfaced visually via the
	// orange tint on each card.
	hasDrift := false
	if ct := getters.FetchConfTalkByProposal(req.ProposalID); ct != nil {
		hasDrift = computeScheduleDrift(ct, proposal, conf)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"confTalkID": confTalkID,
		"start":      startTime.Format(time.RFC3339),
		"end":        endTime.Format(time.RFC3339),
		"hasDrift":   hasDrift,
	})
}

// ScheduleResize updates a placed talk's actual duration. Body:
// {proposalID, durationMin}. Keeps Start + Venue intact and just
// shifts the End time. Refuses on talks that aren't currently placed
// — there's no Sched to extend.
//
// DesiredDuration on the Proposal stays untouched: that's the
// applicant's stated ask, the schedule UI just records what we
// actually gave them via ConfTalk.Sched.
func ScheduleResize(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	var req struct {
		ProposalID  string `json:"proposalID"`
		DurationMin int    `json:"durationMin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.ProposalID == "" || req.DurationMin <= 0 {
		http.Error(w, "missing proposalID / durationMin", http.StatusBadRequest)
		return
	}

	ct := getters.FetchConfTalkByProposal(req.ProposalID)
	if ct == nil || ct.Sched == nil {
		http.Error(w, "talk isn't currently scheduled", http.StatusConflict)
		return
	}

	newEnd := ct.Sched.Start.Add(time.Duration(req.DurationMin) * time.Minute)
	if err := getters.UpdateConfTalkSchedule(ctx, ct.ID, ct.Venue, ct.Sched.Start, newEnd); err != nil {
		ctx.Err.Printf("/%s/admin/schedule resize: %s", conf.Tag, err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	// No auto-fire — drift is surfaced visually; updates
	// propagate via the explicit "Send Cal Updates" button.
	hasDrift := false
	if updated := getters.FetchConfTalkByProposal(req.ProposalID); updated != nil {
		if proposal, perr := getters.GetProposal(ctx, req.ProposalID); perr == nil && proposal != nil {
			hasDrift = computeScheduleDrift(updated, proposal, conf)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"durationMin": req.DurationMin,
		"hasDrift":    hasDrift,
	})
}

// ScheduleUnplace handles a drag-back-to-sidebar request. Deletes the
// ConfTalk row entirely (Notion archive, recoverable from trash).
//
// Body: {proposalID}.
func ScheduleUnplace(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	var req struct {
		ProposalID string `json:"proposalID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.ProposalID == "" {
		http.Error(w, "missing proposalID", http.StatusBadRequest)
		return
	}

	ct := getters.FetchConfTalkByProposal(req.ProposalID)
	if ct == nil {
		// Already not on the grid — nothing to do, idempotent OK.
		w.WriteHeader(http.StatusOK)
		return
	}

	// No auto-CANCEL on unplace either. The schedule UI is
	// draft work; if the talk was previously Scheduled and is
	// being yanked off the grid, the admin should explicitly
	// Decline / Cancel it from the proposal card to fan a
	// CANCEL out to attendees. Otherwise the unplaced talk's
	// existing CalNotif stays attached to the (now archived)
	// row and re-placement / re-send still produces a coherent
	// SEQUENCE diff.

	if err := getters.DeleteConfTalk(ctx, ct.ID); err != nil {
		ctx.Err.Printf("/%s/admin/schedule delete %s: %s", conf.Tag, ct.ID, err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// addTalkAllowedTypes is the set of TalkType values the "Add talk"
// form on the schedule page accepts. Keeps the dropdown honest and
// stops typo'd values from sneaking into Notion.
var addTalkAllowedTypes = map[string]bool{
	"talk":      true,
	"panel":     true,
	"keynote":   true,
	"workshop":  true,
	"hackathon": true,
}

// ScheduleAddTalk creates a single Proposal from the inline "Add
// talk/panel" form on the schedule page. Form fields:
//   - Title (required)
//   - TalkType (required, one of addTalkAllowedTypes)
//   - DesiredDuration (required, minutes)
//
// The resulting Proposal lands in the conf's sidebar with Status =
// Accepted, ready to drag onto the grid.
func ScheduleAddTalk(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	flash := func(msg string) {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/schedule?flash=%s", conf.Tag, url.QueryEscape(msg)),
			http.StatusSeeOther)
	}

	title := strings.TrimSpace(r.PostFormValue("Title"))
	talkType := strings.TrimSpace(r.PostFormValue("TalkType"))
	durStr := strings.TrimSpace(r.PostFormValue("DesiredDuration"))
	if title == "" {
		flash("Add talk: title required.")
		return
	}
	if !addTalkAllowedTypes[talkType] {
		flash("Add talk: unknown talk type.")
		return
	}
	dur, err := strconv.Atoi(durStr)
	if err != nil || dur <= 0 {
		flash("Add talk: duration must be a positive number of minutes.")
		return
	}

	if _, err := getters.CreateProposal(ctx, getters.ProposalInput{
		Title:           title,
		TalkType:        talkType,
		DesiredDuration: dur,
		AvailDuration:   dur,
		ScheduleForTag:  conf.Tag,
		Status:          StatusAccepted,
	}); err != nil {
		ctx.Err.Printf("/%s/admin/schedule add-talk %q: %s", conf.Tag, title, err)
		flash("Couldn't add talk: " + err.Error())
		return
	}
	getters.InvalidateProposalsCache()
	flash(fmt.Sprintf("Added %q (%d min %s) to the sidebar.", title, dur, talkType))
}

// hackathonProposals is the canonical set of stub proposals seeded
// by the "Add hackathon" button on the schedule page. Order matches
// how the items appear in the sidebar (alphabetical-by-title once
// inserted), and the durations match the published rundown.
var hackathonProposals = []struct {
	Title    string
	Duration int
}{
	{"Hackathon Kickoff", 30},
	{"Hacking Time", 120},
	{"Hackathon Expo", 120},
	{"Hackathon Finals", 60},
	{"Hackathon Awards", 45},
}

// ScheduleAddHackathon seeds five hackathon-shaped proposals against
// the given conf so they show up in the schedule sidebar ready to be
// placed. Idempotent: re-clicking only creates titles that don't
// already exist on the conf, so admins can add new ones (after a
// rename, say) without piling on duplicates of the rest.
func ScheduleAddHackathon(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	existing := map[string]bool{}
	for _, p := range loadConfProposals(ctx, conf) {
		if p != nil {
			existing[strings.ToLower(strings.TrimSpace(p.Title))] = true
		}
	}

	added := 0
	for _, h := range hackathonProposals {
		if existing[strings.ToLower(h.Title)] {
			continue
		}
		_, err := getters.CreateProposal(ctx, getters.ProposalInput{
			Title:           h.Title,
			TalkType:        "hackathon",
			DesiredDuration: h.Duration,
			AvailDuration:   h.Duration,
			ScheduleForTag:  conf.Tag,
			Status:          StatusAccepted,
		})
		if err != nil {
			ctx.Err.Printf("/%s/admin/schedule add-hackathon %s: %s", conf.Tag, h.Title, err)
			http.Redirect(w, r,
				fmt.Sprintf("/%s/admin/schedule?flash=%s", conf.Tag,
					url.QueryEscape("Couldn't add "+h.Title+": "+err.Error())),
				http.StatusSeeOther)
			return
		}
		added++
	}
	getters.InvalidateProposalsCache()

	flash := fmt.Sprintf("Added %d hackathon proposal(s).", added)
	if added == 0 {
		flash = "Hackathon proposals already exist for this conf — nothing added."
	}
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/schedule?flash=%s", conf.Tag, url.QueryEscape(flash)),
		http.StatusSeeOther)
}

// buildSchedulePage gathers the data the schedule template needs:
// per-day grids (venues + open/close times + already-placed talks)
// and the sidebar of unscheduled-but-schedulable proposals.
func buildSchedulePage(ctx *config.AppContext, conf *types.Conf) (*AdminSchedulePage, error) {
	infos, err := getters.ListConfInfos(ctx, conf.Tag)
	if err != nil {
		return nil, fmt.Errorf("list confinfos: %w", err)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Day < infos[j].Day })

	// Pre-build day grids keyed by Day index.
	days := make([]*ScheduleDay, 0, len(infos))
	dayByIdx := make(map[int]*ScheduleDay, len(infos))
	for _, ci := range infos {
		if ci.Day < 1 {
			continue
		}
		d := &ScheduleDay{
			Idx:    ci.Day,
			Date:   dayDateFor(conf, ci.Day),
			Info:   ci,
			Venues: ci.Venues,
			Placed: make(map[string][]*ScheduleProposal),
		}
		d.OpensMin, d.ClosesMin = dayOpenCloseMinutes(ci)
		d.HeightPx = (d.ClosesMin - d.OpensMin) * schedulePxPerMin
		d.Breaks = breaksFor(ci, d.OpensMin)
		days = append(days, d)
		dayByIdx[ci.Day] = d
	}

	proposals := loadConfProposals(ctx, conf)
	enrichDashboardProposals(ctx, wrapProposalsAsSpeakerConfs(proposals))

	var unscheduled []*ScheduleProposal
	for _, p := range proposals {
		if !schedulableStatuses[p.Status] {
			continue
		}
		sp := newScheduleProposal(p)
		sp.AvailDays, sp.NoAvail = intersectAvailability(sp.Speakers, conf)
		ct := getters.FetchConfTalkByProposal(p.ID)
		if ct == nil || ct.Sched == nil || ct.Venue == "" {
			unscheduled = append(unscheduled, sp)
			continue
		}
		// Determine which day this placement falls on by matching the
		// ConfTalk's Sched.Start date against the conf's per-day dates.
		dayIdx := dayIndexFor(conf, ct.Sched.Start)
		d := dayByIdx[dayIdx]
		if d == nil {
			// Placed on a day without a ConfInfo row — shouldn't happen
			// in practice, but if it does we surface the talk in the
			// sidebar rather than dropping it silently.
			unscheduled = append(unscheduled, sp)
			continue
		}
		sp.ConfTalkID = ct.ID
		sp.StartMin = ct.Sched.Start.Hour()*60 + ct.Sched.Start.Minute()
		if ct.Sched.End != nil {
			sp.ActualMin = endMinutesAfterStart(ct.Sched.Start, *ct.Sched.End, sp.ActualMin)
		}
		sp.TopPx = (sp.StartMin - d.OpensMin) * schedulePxPerMin
		sp.HeightPx = sp.ActualMin * schedulePxPerMin
		sp.HasDrift = computeScheduleDrift(ct, p, conf)
		d.Placed[ct.Venue] = append(d.Placed[ct.Venue], sp)
	}

	// Sidebar order: status (Accepted/Invited first → already-actionable
	// at the top), then by title.
	sort.Slice(unscheduled, func(i, j int) bool {
		ai := statusSortRank(unscheduled[i].Proposal.Status)
		aj := statusSortRank(unscheduled[j].Proposal.Status)
		if ai != aj {
			return ai < aj
		}
		return unscheduled[i].Proposal.Title < unscheduled[j].Proposal.Title
	})
	for _, sp := range unscheduled {
		// Sidebar cards size from ActualMin so admins eyeball the
		// slot size each card will occupy when dropped onto the grid
		// (talks include their Q&A buffer).
		sp.HeightPx = sp.ActualMin * schedulePxPerMin
	}

	return &AdminSchedulePage{
		Conf:        conf,
		Days:        days,
		Unscheduled: unscheduled,
		PxPerMin:    schedulePxPerMin,
		SnapMin:     scheduleSnapMin,
		Year:        helpers.CurrentYear(),
	}, nil
}

// newScheduleProposal builds the per-talk render shape. DesiredMin
// reflects the applicant's stated ask (kept read-only); ActualMin
// defaults to scheduledDurationFor(p) so unscheduled cards in the
// sidebar render at the slot size they'll occupy when placed (talks
// get a Q&A buffer; everything else == DesiredMin). Once placed, the
// schedule handler overwrites ActualMin with whatever the
// ConfTalk.Sched range says.
func newScheduleProposal(p *types.Proposal) *ScheduleProposal {
	desired := p.DesiredDuration
	if desired <= 0 {
		desired = 30
	}
	actual := scheduledDurationFor(p)
	if actual <= 0 {
		actual = desired
	}
	return &ScheduleProposal{
		Proposal:   p,
		Speakers:   resolveProposalSpeakers(p),
		DesiredMin: desired,
		ActualMin:  actual,
	}
}

// intersectAvailability returns the set of days every speaker on a
// proposal has marked themselves available for, formatted as short
// weekday labels ("Sat", "Sun") and ordered by conference day.
//
// Returns (labels, noAvail). noAvail is true when every speaker has
// availability data but the intersection is empty — speakers haven't
// landed on a shared day. Returns (nil, false) when there's no
// availability data at all (no speakers with .Availability set), so
// the UI can suppress the chip rather than misreport a conflict.
//
// Date strings stored on SpeakerConf use the "01/02/2006" format
// (the same format Conf.DaysList emits), so we compare verbatim.
func intersectAvailability(scs []*types.SpeakerConf, conf *types.Conf) ([]string, bool) {
	if conf == nil {
		return nil, false
	}
	type dayInfo struct {
		date  time.Time
		label string
	}
	dayByKey := map[string]dayInfo{}
	var orderedKeys []string
	for _, item := range conf.DaysList("", false) {
		t, err := time.Parse("01/02/2006", item.ItemID)
		if err != nil {
			continue
		}
		dayByKey[item.ItemID] = dayInfo{date: t, label: t.Format("Mon")}
		orderedKeys = append(orderedKeys, item.ItemID)
	}

	var perSpeakerSets []map[string]struct{}
	for _, sc := range scs {
		if sc == nil || len(sc.Availability) == 0 {
			continue
		}
		set := make(map[string]struct{}, len(sc.Availability))
		for _, d := range sc.Availability {
			set[d] = struct{}{}
		}
		perSpeakerSets = append(perSpeakerSets, set)
	}
	if len(perSpeakerSets) == 0 {
		return nil, false
	}

	var labels []string
	for _, key := range orderedKeys {
		ok := true
		for _, set := range perSpeakerSets {
			if _, hit := set[key]; !hit {
				ok = false
				break
			}
		}
		if ok {
			labels = append(labels, dayByKey[key].label)
		}
	}
	return labels, len(labels) == 0
}

// scheduledDurationFor returns the slot size the talk should occupy
// on the schedule grid. Speakers apply for "20m" or "30m" of content
// time, but each gets a Q&A tail (10m and 15m respectively), so the
// scheduled slot is 30m / 45m. Everything else (panels, workshops,
// lightning talks) keeps actual == desired.
func scheduledDurationFor(p *types.Proposal) int {
	if p == nil {
		return 0
	}
	if p.TalkType == "talk" {
		switch p.DesiredDuration {
		case 20:
			return 30
		case 30:
			return 45
		}
	}
	return p.DesiredDuration
}

// breaksFor returns the no-go time bands for a day — lunch + coffee
// when both endpoints are set on ConfInfo. Single-instant times (e.g.
// Breakfast where only Start is filled) are skipped because they don't
// describe a range to block out.
func breaksFor(ci *types.ConfInfo, opensMin int) []*ScheduleBreak {
	if ci == nil {
		return nil
	}
	var out []*ScheduleBreak
	add := func(label string, t *types.Times) {
		if t == nil || t.End == nil {
			return
		}
		startMin := t.Start.Hour()*60 + t.Start.Minute()
		endMin := t.End.Hour()*60 + t.End.Minute()
		if endMin <= startMin {
			return
		}
		out = append(out, &ScheduleBreak{
			Label:    label,
			StartMin: startMin,
			EndMin:   endMin,
			TopPx:    (startMin - opensMin) * schedulePxPerMin,
			HeightPx: (endMin - startMin) * schedulePxPerMin,
		})
	}
	add("Lunch", ci.Lunch)
	add("Coffee", ci.Coffee)
	return out
}

// dayOpenCloseMinutes returns the day's open/close as minute-of-day,
// derived from ConfInfo.Doors. If Doors is missing, falls back to a
// reasonable 9:00–22:00 window.
func dayOpenCloseMinutes(ci *types.ConfInfo) (open, close int) {
	open, close = 9*60, 22*60
	if ci == nil || ci.Doors == nil {
		return
	}
	open = ci.Doors.Start.Hour()*60 + ci.Doors.Start.Minute()
	if ci.Doors.End != nil {
		close = ci.Doors.End.Hour()*60 + ci.Doors.End.Minute()
	}
	if close <= open {
		close = open + 12*60
	}
	return
}

// dayDateFor returns midnight of the conf's Nth day in the conf's tz.
// Conf.StartDate may carry a non-midnight time (Notion stores whatever
// the admin enters), so we floor to the day. Critically, this is the
// function the schedule handler uses to construct a new TalkTime —
// using conf.Loc() (which prefers the explicit Notion Timezone) means
// the times the scheduler writes back to ConfTalk are in the
// conference's local zone, not the app's GMT-5.
func dayDateFor(conf *types.Conf, dayIdx int) time.Time {
	loc := conf.Loc()
	t := conf.StartDate.In(loc)
	base := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return base.AddDate(0, 0, dayIdx-1)
}

// dayIndexFor maps a wall-clock time onto a conf's day index. Day 1 =
// conf.StartDate's calendar day. Compares dates in the conf's tz to
// avoid timezone-mismatch off-by-ones.
func dayIndexFor(conf *types.Conf, when time.Time) int {
	loc := conf.Loc()
	w := when.In(loc)
	wDay := time.Date(w.Year(), w.Month(), w.Day(), 0, 0, 0, 0, loc)
	c := conf.StartDate.In(loc)
	cDay := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, loc)
	return int(wDay.Sub(cDay).Hours()/24) + 1
}

// endMinutesAfterStart returns the talk's duration in minutes given a
// stored Start/End pair. Compares wall-clock to dodge tz drift between
// the stored ConfTalk Sched and the conf's StartDate location. Falls
// back to fallbackMin when the computed duration is non-positive.
func endMinutesAfterStart(start, end time.Time, fallbackMin int) int {
	startMin := start.Hour()*60 + start.Minute()
	endMin := end.Hour()*60 + end.Minute()
	if endMin > startMin {
		return endMin - startMin
	}
	return fallbackMin
}

// statusSortRank gives the sidebar ordering — accepted/invited talks
// (most likely to be scheduled) bubble to the top.
func statusSortRank(status string) int {
	switch status {
	case "Accepted":
		return 0
	case "Invited":
		return 1
	case "InReview", "":
		return 2
	case "Applied":
		return 3
	case "Waitlisted":
		return 4
	}
	return 5
}

// wrapProposalsAsSpeakerConfs adapts a flat proposal slice into the
// shape enrichDashboardProposals expects (it walks SpeakerConfs ->
// Proposals). We don't actually have SpeakerConfs here — just synth
// one with the proposals attached so the same enricher resolves the
// co-speaker SpeakerConfs and ConfTalks for us.
func wrapProposalsAsSpeakerConfs(proposals []*types.Proposal) []*types.SpeakerConf {
	if len(proposals) == 0 {
		return nil
	}
	return []*types.SpeakerConf{{Proposals: proposals}}
}

// FormatHourMin renders a minute-of-day as "9:00 am" / "1:30 pm". Used
// in the schedule grid's left-rail time labels.
func FormatHourMin(min int) string {
	h := min / 60
	m := min % 60
	period := "am"
	display := h
	switch {
	case h == 0:
		display = 12
	case h == 12:
		period = "pm"
	case h > 12:
		display = h - 12
		period = "pm"
	}
	if m == 0 {
		return fmt.Sprintf("%d %s", display, period)
	}
	return fmt.Sprintf("%d:%02d %s", display, m, period)
}

// HourLabels returns the integer hours that fall inside [openMin,
// closeMin] inclusive, used by the template to render hour-marker
// rows down the left rail.
func HourLabels(openMin, closeMin int) []int {
	first := (openMin / 60) * 60
	if first < openMin {
		first += 60
	}
	out := []int{}
	for m := first; m <= closeMin; m += 60 {
		out = append(out, m)
	}
	return out
}

// VenueChipClasses returns tailwind classes for a venue-name chip.
// Stable per venue name (hash-based) so the same venue always paints
// the same color across the page.
func VenueChipClasses(name string) string {
	palette := []string{
		"bg-indigo-100 text-indigo-800",
		"bg-orange-100 text-orange-800",
		"bg-lime-100 text-lime-800",
		"bg-pink-100 text-pink-800",
		"bg-sky-100 text-sky-800",
		"bg-amber-100 text-amber-800",
	}
	if name == "" {
		return "bg-gray-100 text-gray-700"
	}
	sum := 0
	for _, r := range strings.ToLower(name) {
		sum = (sum*31 + int(r)) & 0x7fffffff
	}
	return palette[sum%len(palette)]
}
