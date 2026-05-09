package handlers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

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

	for _, ci := range infos {
		d := dayFor(ci.Day)
		d.Rows = append(d.Rows, rowsFromConfInfo(ci)...)
	}
	for _, t := range talks {
		if t == nil || t.Sched == nil {
			continue
		}
		idx := dayIndexFor(conf, t.Sched.Start)
		dayFor(idx).Rows = append(dayFor(idx).Rows, rowFromTalk(t))
	}
	for _, s := range shifts {
		if s == nil || s.ShiftTime == nil {
			continue
		}
		idx := dayIndexFor(conf, s.ShiftTime.Start)
		dayFor(idx).Rows = append(dayFor(idx).Rows, rowFromShift(s, volByRef))
	}

	days := make([]*RunOfShowDay, 0, len(dayByIdx))
	for _, d := range dayByIdx {
		sort.SliceStable(d.Rows, func(i, j int) bool {
			return d.Rows[i].Start.Before(d.Rows[j].Start)
		})
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Idx < days[j].Idx })

	page := &RunOfShowPage{
		Conf:         conf,
		Days:         days,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/run_of_show.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/run-of-show render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// rowsFromConfInfo emits timeline rows for the per-day strip events.
// Doors specifically gets two rows (open + close) when End is set —
// it reads more naturally than a ranged "8 AM – 8 PM Doors". Meals
// stay as a single ranged row.
func rowsFromConfInfo(ci *types.ConfInfo) []*RunOfShowRow {
	var rows []*RunOfShowRow
	if ci == nil {
		return rows
	}
	if ci.Doors != nil {
		rows = append(rows, &RunOfShowRow{
			Start: ci.Doors.Start,
			Kind:  "info",
			What:  "Doors open",
		})
		if ci.Doors.End != nil {
			rows = append(rows, &RunOfShowRow{
				Start: *ci.Doors.End,
				Kind:  "info",
				What:  "Doors close",
			})
		}
	}
	if ci.Breakfast != nil {
		rows = append(rows, &RunOfShowRow{
			Start: ci.Breakfast.Start,
			End:   ci.Breakfast.End,
			Kind:  "info",
			What:  "Breakfast",
		})
	}
	if ci.Coffee != nil {
		rows = append(rows, &RunOfShowRow{
			Start: ci.Coffee.Start,
			End:   ci.Coffee.End,
			Kind:  "info",
			What:  "Coffee",
		})
	}
	if ci.Lunch != nil {
		rows = append(rows, &RunOfShowRow{
			Start: ci.Lunch.Start,
			End:   ci.Lunch.End,
			Kind:  "info",
			What:  "Lunch",
		})
	}
	return rows
}

// rowFromTalk turns a Talk into a Run-of-Show row. Speaker names are
// joined with ", " in the order the talk's Speakers slice carries.
func rowFromTalk(t *types.Talk) *RunOfShowRow {
	row := &RunOfShowRow{
		Start: t.Sched.Start,
		End:   t.Sched.End,
		Kind:  "talk",
		What:  t.Name,
		Where: t.Venue,
	}
	names := make([]string, 0, len(t.Speakers))
	seen := map[string]bool{}
	for _, sp := range t.Speakers {
		if sp == nil || sp.Name == "" || seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		names = append(names, sp.Name)
	}
	row.Who = strings.Join(names, ", ")
	return row
}

// rowFromShift turns a WorkShift into a Run-of-Show row. The Who
// column lists every assigned volunteer's name, with the shift leader
// rendered first and tagged " (lead)" so the responsible person is
// obvious. Unresolved volunteer refs (cache miss) are silently
// skipped — the shift still shows up, just with a shorter Who list.
func rowFromShift(s *types.WorkShift, volByRef map[string]*types.Volunteer) *RunOfShowRow {
	row := &RunOfShowRow{
		Start: s.ShiftTime.Start,
		End:   s.ShiftTime.End,
		Kind:  "shift",
		What:  shiftLabel(s),
	}

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
	row.Who = strings.Join(who, ", ")
	return row
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
