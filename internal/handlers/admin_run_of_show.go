package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// venuePalette is the cycle of hex colors assigned to venues for the
// Where-column text on the run-of-show. Tailwind 700-shade equivalents
// so the colors stay legible on the white table background and
// reasonably distinct under a B&W printer if anyone prints in mono.
// Picked by sorted-index of the conf's venue list, so a given conf
// always gets the same mapping across renders.
var venuePalette = []string{
	"#4338ca", // indigo-700
	"#047857", // emerald-700
	"#be123c", // rose-700
	"#b45309", // amber-700
	"#0e7490", // cyan-700
}

// venueLabels maps the raw Notion venue tags (the multi-select values
// stored on ConfInfo.Venues / ConfTalk.Venue) to friendly display
// labels per conference. Anything not in this map renders as the raw
// tag — admins can keep entering Notion-friendly slugs while the
// run-of-show shows the human-readable name.
var venueLabels = map[string]map[string]string{
	"vienna": {
		"one": "Main Stage",
		"two": "Talks Stage",
	},
}

// venueLabel resolves a raw venue tag to its display label for a
// given conf, falling back to the raw tag when no mapping is set.
func venueLabel(confTag, raw string) string {
	if raw == "" {
		return ""
	}
	if m, ok := venueLabels[confTag]; ok {
		if l, ok := m[raw]; ok {
			return l
		}
	}
	return raw
}

// RunOfShowAdmin renders /{conf}/admin/run-of-show — a per-day
// timeline table interleaving ConfInfo events (doors, coffee, lunch),
// volunteer shifts, and conference talks. Read-only; no writes.
func RunOfShowAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	infos, err := getters.ListConfInfos(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show list confinfos: %s", conf.Tag, err)
		http.Error(w, "Unable to load run of show", http.StatusInternalServerError)
		return
	}
	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show list talks: %s", conf.Tag, err)
		http.Error(w, "Unable to load run of show", http.StatusInternalServerError)
		return
	}
	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show list shifts: %s", conf.Tag, err)
		http.Error(w, "Unable to load run of show", http.StatusInternalServerError)
		return
	}
	// Resolve volunteer page-IDs → names so the Who column can show
	// readable assignee lists. Best-effort: a list error degrades to
	// empty Who cells rather than failing the page.
	volByRef := map[string]*types.Volunteer{}
	if vols, err := getters.ListVolunteersForConf(ctx, conf.Ref); err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show list volunteers (continuing): %s", conf.Tag, err)
	} else {
		for _, v := range vols {
			if v != nil && v.Ref != "" {
				volByRef[v.Ref] = v
			}
		}
	}

	dayByIdx := map[int]*RunOfShowDay{}
	dayFor := func(idx int) *RunOfShowDay {
		d, ok := dayByIdx[idx]
		if !ok {
			d = &RunOfShowDay{Idx: idx, Date: dayDateFor(conf, idx)}
			dayByIdx[idx] = d
		}
		return d
	}

	// Each entry with a time range produces TWO rows — one at start
	// and one at end — bucketed independently so an overnight shift
	// (start day N, end day N+1) lands on both days correctly.
	//
	// Normalize every row's Start to conf-local tz before bucketing
	// + sorting. parseTimes returns whatever zone Notion stored
	// (typically UTC for datetimes), but parseTimesRange anchors
	// ConfInfo events to conf-local. Without this conversion, a shift
	// end at "17:00 UTC" displays as "5:00 PM" but sorts at the same
	// instant as "09:00 conf-local" — which is exactly the
	// "shift ends in the wrong chronological position" symptom.
	loc := conf.Loc()
	addRows := func(rows []*RunOfShowRow) {
		for _, row := range rows {
			if row == nil {
				continue
			}
			row.Start = row.Start.In(loc)
			idx := dayIndexFor(conf, row.Start)
			dayFor(idx).Rows = append(dayFor(idx).Rows, row)
		}
	}
	for _, ci := range infos {
		addRows(rowsFromConfInfo(ci))
	}
	for _, t := range talks {
		if t == nil || t.Sched == nil {
			continue
		}
		addRows(rowsFromTalk(conf.Tag, t))
	}
	for _, s := range shifts {
		if s == nil || s.ShiftTime == nil {
			continue
		}
		addRows(rowsFromShift(s, volByRef))
	}

	days := make([]*RunOfShowDay, 0, len(dayByIdx))
	for _, d := range dayByIdx {
		sort.SliceStable(d.Rows, func(i, j int) bool {
			return d.Rows[i].Start.Before(d.Rows[j].Start)
		})
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Idx < days[j].Idx })

	// Collect unique non-empty venue tags across every talk row so
	// the template can render a checkbox per venue. Sort by display
	// label for stable, alphabetical UI, then assign each venue a
	// color from a fixed palette by sorted-index — same conf always
	// gets the same color mapping across renders.
	venueSeen := map[string]bool{}
	var venues []VenueOption
	for _, d := range days {
		for _, row := range d.Rows {
			if row.VenueTag == "" || venueSeen[row.VenueTag] {
				continue
			}
			venueSeen[row.VenueTag] = true
			venues = append(venues, VenueOption{
				Tag:   row.VenueTag,
				Label: venueLabel(conf.Tag, row.VenueTag),
			})
		}
	}
	sort.SliceStable(venues, func(i, j int) bool { return venues[i].Label < venues[j].Label })
	for i := range venues {
		venues[i].Color = venuePalette[i%len(venuePalette)]
	}

	page := &RunOfShowPage{
		Conf:         conf,
		Days:         days,
		Venues:       venues,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/run_of_show.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// rangedRows emits a "start" row at t.Start and, when t.End is set
// and is strictly after t.Start, a matching "end" row prefixed with
// "End: " so the timeline shows both moments at their actual times.
// startRow carries the full content (Who / Where); the end row keeps
// only the labelled What so the timeline doesn't repeat speaker /
// venue info on the closing line.
func rangedRows(t *types.Times, kind, label, who, where string) []*RunOfShowRow {
	if t == nil {
		return nil
	}
	rows := []*RunOfShowRow{{
		Start: t.Start,
		Kind:  kind,
		What:  label,
		Who:   who,
		Where: where,
	}}
	if t.End != nil && t.End.After(t.Start) {
		rows = append(rows, &RunOfShowRow{
			Start: *t.End,
			Kind:  kind,
			What:  "End: " + label,
		})
	}
	return rows
}

// rowsFromConfInfo emits timeline rows for the per-day strip events.
// Each event with an End time produces two rows (start + end), placed
// at their respective times so the run-of-show reads chronologically.
func rowsFromConfInfo(ci *types.ConfInfo) []*RunOfShowRow {
	var rows []*RunOfShowRow
	if ci == nil {
		return rows
	}
	// Doors gets a custom pair so the labels read "Doors open" /
	// "Doors close" rather than "Doors" / "End: Doors".
	if ci.Doors != nil {
		rows = append(rows, &RunOfShowRow{
			Start: ci.Doors.Start,
			Kind:  "info",
			What:  "Doors open",
		})
		if ci.Doors.End != nil && ci.Doors.End.After(ci.Doors.Start) {
			rows = append(rows, &RunOfShowRow{
				Start: *ci.Doors.End,
				Kind:  "info",
				What:  "Doors close",
			})
		}
	}
	rows = append(rows, rangedRows(ci.Breakfast, "info", "Breakfast", "", "")...)
	rows = append(rows, rangedRows(ci.Coffee, "info", "Coffee", "", "")...)
	rows = append(rows, rangedRows(ci.Lunch, "info", "Lunch", "", "")...)
	return rows
}

// rowsFromTalk emits a single timeline row for a talk. The talk's
// duration is folded into the label as "Title (30m)" rather than
// emitting a separate "End:" row at the close time — talks fit
// densely on the page and an inline duration reads more cleanly than
// duplicate rows. Where carries the human-readable venue label
// (resolved per confTag); the raw tag rides along on VenueTag for
// the per-venue visibility toggle.
func rowsFromTalk(confTag string, t *types.Talk) []*RunOfShowRow {
	names := make([]string, 0, len(t.Speakers))
	seen := map[string]bool{}
	for _, sp := range t.Speakers {
		if sp == nil || sp.Name == "" || seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		names = append(names, sp.Name)
	}
	label := t.Name
	if t.Sched.End != nil && t.Sched.End.After(t.Sched.Start) {
		durMin := int(t.Sched.End.Sub(t.Sched.Start).Minutes())
		label = fmt.Sprintf("%s (%dm)", t.Name, durMin)
	}
	return []*RunOfShowRow{{
		Start:    t.Sched.Start,
		Kind:     "talk",
		What:     label,
		Who:      strings.Join(names, ", "),
		Where:    venueLabel(confTag, t.Venue),
		VenueTag: t.Venue,
	}}
}

// rowsFromShift emits a start row listing every assigned volunteer
// (leader first, tagged " (lead)") and, when the shift has an End
// time, a closing "End: <label>" row at the shift's end.
func rowsFromShift(s *types.WorkShift, volByRef map[string]*types.Volunteer) []*RunOfShowRow {
	var who []string
	included := map[string]bool{}
	if s.ShiftLeaderRef != "" {
		if v := volByRef[s.ShiftLeaderRef]; v != nil && v.Name != "" {
			who = append(who, v.Name+" (lead)")
			included[s.ShiftLeaderRef] = true
		}
	}
	for _, ref := range s.AssigneesRef {
		if ref == "" || included[ref] {
			continue
		}
		v := volByRef[ref]
		if v == nil || v.Name == "" {
			continue
		}
		who = append(who, v.Name)
		included[ref] = true
	}
	return rangedRows(s.ShiftTime, "shift", shiftLabel(s), strings.Join(who, ", "), "")
}

// shiftLabel produces the "What" string for a volunteer shift row.
// Prefer the JobType title (e.g. "Registration", "Catering"); fall
// back to the shift's own Name; empty-string in last resort. Always
// prefixed with "Volunteer shift: " so the row sorts visually under
// the same prefix on the Run-of-Show table.
func shiftLabel(s *types.WorkShift) string {
	label := ""
	if s.Type != nil && s.Type.Title != "" {
		label = s.Type.Title
	} else if s.Name != "" {
		label = s.Name
	}
	if label == "" {
		return "Volunteer shift"
	}
	return "Volunteer shift: " + label
}

// formatRunOfShowTime returns "9:30 AM" for any time.Time. Wired
// into the template funcMap as `formatTime` (see handlers.go).
func formatRunOfShowTime(t time.Time) string {
	return t.Format("3:04 PM")
}
