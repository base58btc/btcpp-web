package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// MakeWorkShifts bulk-creates the canonical roster of volunteer
// shifts for a conference based on its ConfInfo per-day strip:
//
//   * Setup crew (day before Day 1, 10:00 → 14:00, 6 people).
//   * Each event day:
//       - Check-in AM: 30m before doors open → +4h, 3 people.
//       - Check-in PM: 30m before AM check-in ends → +4h, 3 people.
//       - Per venue × {Showrunner, A/V Monitor}:
//           AM: doors open → 13:30, 1 person each.
//           PM: 13:00 → doors close − 1h, 1 person each.
//   * Teardown crew on the last day (doors close − 2h → doors
//     close, 6 people).
//
// All times anchor in conf.Loc() so the resulting Notion rows show
// the right local wall-clock regardless of the server's tz.
//
// Best-effort across rows — a single CreateShift failure logs and
// the rest still get attempted. Returns the count of shifts
// successfully written and the first error (if any) so the caller
// can surface a "created N (with errors)" flash.
//
// Not idempotent: re-running creates duplicates. Coords should
// delete existing shifts before re-running, or accept duplicates
// that they edit / remove individually.
func MakeWorkShifts(ctx *config.AppContext, conf *types.Conf) (int, error) {
	if conf == nil {
		return 0, fmt.Errorf("MakeWorkShifts: nil conf")
	}
	infos, err := getters.ListConfInfos(ctx, conf.Tag)
	if err != nil {
		return 0, fmt.Errorf("list confinfos: %w", err)
	}
	if len(infos) == 0 {
		return 0, fmt.Errorf("no ConfInfo rows for %s — set up the per-day strip first", conf.Tag)
	}
	// Sort by Day ascending. Filter out Day < 1 (orphan rows).
	clean := make([]*types.ConfInfo, 0, len(infos))
	for _, ci := range infos {
		if ci != nil && ci.Day >= 1 {
			clean = append(clean, ci)
		}
	}
	if len(clean) == 0 {
		return 0, fmt.Errorf("no Day≥1 ConfInfo rows for %s", conf.Tag)
	}
	sortConfInfosByDay(clean)

	jobs, err := getters.FetchJobsCached(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch jobs: %w", err)
	}
	jobByTag := func(tag string) *types.JobType {
		for _, j := range jobs {
			if j != nil && strings.EqualFold(j.Tag, tag) {
				return j
			}
		}
		return nil
	}
	showrunner := jobByTag("showrunner")
	avdesk := jobByTag("avdesk")
	checkin := jobByTag("checkin")
	setup := jobByTag("setup")
	teardown := jobByTag("teardown")

	loc := conf.Loc()
	var firstErr error
	created := 0
	create := func(jt *types.JobType, label, name string, start, end time.Time, max, prio uint) {
		if jt == nil {
			ctx.Err.Printf("MakeWorkShifts %s: %s job type not configured (skip)", conf.Tag, label)
			return
		}
		if err := getters.CreateShift(ctx, conf, jt, name, start, end, max, prio); err != nil {
			ctx.Err.Printf("MakeWorkShifts %s create %q: %s", conf.Tag, name, err)
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		created++
	}

	// Setup crew the day before Day 1: 10:00 → 14:00, 6 people.
	day1 := dayDateFor(conf, clean[0].Day)
	dayBefore := day1.AddDate(0, 0, -1)
	setupStart := time.Date(dayBefore.Year(), dayBefore.Month(), dayBefore.Day(), 10, 0, 0, 0, loc)
	setupEnd := time.Date(dayBefore.Year(), dayBefore.Month(), dayBefore.Day(), 14, 0, 0, 0, loc)
	create(setup, "setup", "Setup crew", setupStart, setupEnd, 6, 0)

	for i, ci := range clean {
		if ci.Doors == nil {
			ctx.Err.Printf("MakeWorkShifts %s: Day %d has no Doors set (skip per-day shifts)", conf.Tag, ci.Day)
			continue
		}
		doorsOpen := ci.Doors.Start.In(loc)
		var doorsClose time.Time
		hasClose := false
		if ci.Doors.End != nil {
			doorsClose = ci.Doors.End.In(loc)
			hasClose = true
		}

		// Day-of setup on Day 1: 1h before doors open → +4h, 6
		// people. Catches the venue-touch tasks that can only
		// happen morning-of (signage, A/V check, registration
		// table layout) — separate from the day-before setup
		// crew which handles bigger move-in work.
		if i == 0 {
			dayOfStart := doorsOpen.Add(-1 * time.Hour)
			dayOfEnd := dayOfStart.Add(4 * time.Hour)
			create(setup, "setup",
				"Setup, Day of",
				dayOfStart, dayOfEnd, 6, 0)
		}

		// Check-in AM/PM — per day, not per venue.
		amStart := doorsOpen.Add(-30 * time.Minute)
		amEnd := amStart.Add(4 * time.Hour)
		pmStart := amEnd.Add(-30 * time.Minute)
		pmEnd := pmStart.Add(4 * time.Hour)
		create(checkin, "checkin",
			fmt.Sprintf("Check-in — AM (day %d)", ci.Day),
			amStart, amEnd, 3, 1)
		create(checkin, "checkin",
			fmt.Sprintf("Check-in — PM (day %d)", ci.Day),
			pmStart, pmEnd, 3, 1)

		// Per-venue × {Showrunner, A/V Monitor} × {AM, PM}.
		// AM: doors open → 13:30. PM: 13:00 → doors close − 1h
		// (skipped when Doors has no End).
		amVenueEnd := timeOnDay(doorsOpen, 13, 30, loc)
		pmVenueStart := timeOnDay(doorsOpen, 13, 0, loc)
		var pmVenueEnd time.Time
		if hasClose {
			pmVenueEnd = doorsClose.Add(-1 * time.Hour)
		}
		for _, venue := range ci.Venues {
			venueLabel := venueLabelOrTag(conf, venue)
			create(showrunner, "showrunner",
				fmt.Sprintf("Showrunner — %s (AM, day %d)", venueLabel, ci.Day),
				doorsOpen, amVenueEnd, 1, 2)
			create(avdesk, "avdesk",
				fmt.Sprintf("A/V Monitor — %s (AM, day %d)", venueLabel, ci.Day),
				doorsOpen, amVenueEnd, 1, 2)
			if hasClose {
				create(showrunner, "showrunner",
					fmt.Sprintf("Showrunner — %s (PM, day %d)", venueLabel, ci.Day),
					pmVenueStart, pmVenueEnd, 1, 2)
				create(avdesk, "avdesk",
					fmt.Sprintf("A/V Monitor — %s (PM, day %d)", venueLabel, ci.Day),
					pmVenueStart, pmVenueEnd, 1, 2)
			}
		}

		// Teardown crew on the last event day: doors close − 2h →
		// doors close, 6 people.
		if i == len(clean)-1 && hasClose {
			tdStart := doorsClose.Add(-2 * time.Hour)
			create(teardown, "teardown",
				"Teardown crew",
				tdStart, doorsClose, 6, 0)
		}
	}

	return created, firstErr
}

// venueLabelOrTag prefers the conf-specific venue display label
// (the same map run-of-show uses — vienna's "one" → "Main Stage")
// so generated shift names read naturally on the gantt instead of
// the bare slug. Falls back to the raw tag when no mapping exists.
func venueLabelOrTag(conf *types.Conf, raw string) string {
	if l := venueLabel(conf.Tag, raw); l != "" {
		return l
	}
	return raw
}

// timeOnDay returns a time on `anchor`'s calendar day in loc, with
// the given hour/minute. Used for fixed-time shifts (e.g. "13:30 on
// the day doors open").
func timeOnDay(anchor time.Time, hour, min int, loc *time.Location) time.Time {
	a := anchor.In(loc)
	return time.Date(a.Year(), a.Month(), a.Day(), hour, min, 0, 0, loc)
}

func sortConfInfosByDay(cis []*types.ConfInfo) {
	for i := 1; i < len(cis); i++ {
		j := i
		for j > 0 && cis[j-1].Day > cis[j].Day {
			cis[j-1], cis[j] = cis[j], cis[j-1]
			j--
		}
	}
}

// VolAdminGenWorkShifts is the POST endpoint behind the "Gen
// workshifts" button on the volcoord shifts page. Calls
// MakeWorkShifts and bounces back to /volcoord/shifts with a flash
// summarizing the result.
func VolAdminGenWorkShifts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	created, makeErr := MakeWorkShifts(ctx, conf)
	flash := fmt.Sprintf("Created %d shifts.", created)
	if makeErr != nil {
		flash = fmt.Sprintf("Created %d shifts (with errors: %s).", created, makeErr)
	}
	http.Redirect(w, r,
		fmt.Sprintf("/%s/volcoord/shifts?flash=%s", conf.Tag, url.QueryEscape(flash)),
		http.StatusSeeOther)
}
