package handlers

import (
	"sort"
	"time"

	"btcpp-web/internal/types"
)

// AgendaDay is one day's slice of the rendered agenda. Talks come
// pre-sorted (Sched.Start, then Venue) and pre-bucketed against the
// day's ConfInfo break times so templates only render — no logic.
//
// Bucketing rules:
//
//   - Morning   = talks whose Start is before ConfInfo.Lunch.Start.
//   - Afternoon = talks between Lunch.Start and ConfInfo.Coffee.Start.
//   - Evening   = talks at or after Coffee.Start.
//
// When ConfInfo for the day is nil (or break times are missing),
// everything collapses into Morning + All — the template falls back
// to the chrono-only past-conf rendering.
type AgendaDay struct {
	Idx       int              // 1-based day index (Day 1 = conf.StartDate)
	Date      time.Time        // for the "Sat, Nov 15th" day header
	Info      *types.ConfInfo  // doors / breakfast / lunch / coffee — nil ok
	Morning   []*types.Session // before Lunch (or all-day if no Lunch info)
	Afternoon []*types.Session // between Lunch and Coffee
	Evening   []*types.Session // at/after Coffee
	All       []*types.Session // every session this day in chrono order
}

// buildAgendaDays groups the conf's talks by day and buckets them
// against the per-day ConfInfo break times. Days with no talks are
// dropped — a 4-day conf with talks only on days 1/2/4 yields 3
// AgendaDays, not 4.
//
// infosByDay is indexed by 1-based Day matching ConfInfo.Day; days
// without an entry leave AgendaDay.Info nil.
func buildAgendaDays(conf *types.Conf, talks []*types.Talk, infosByDay map[int]*types.ConfInfo) []*AgendaDay {
	if conf == nil {
		return nil
	}
	loc := conf.Loc()
	startDate := dayStart(conf.StartDate, loc)

	byDay := make(map[int][]*types.Talk)
	for _, t := range talks {
		if t == nil || t.Sched == nil {
			continue
		}
		idx := dayIndex(startDate, t.Sched.Start, loc)
		if idx < 1 {
			continue
		}
		byDay[idx] = append(byDay[idx], t)
	}

	idxs := make([]int, 0, len(byDay))
	for i := range byDay {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	out := make([]*AgendaDay, 0, len(idxs))
	for _, idx := range idxs {
		dayTalks := byDay[idx]
		sortTalksForAgenda(dayTalks)
		sessions := make([]*types.Session, 0, len(dayTalks))
		for _, t := range dayTalks {
			sessions = append(sessions, talkToSession(t, conf))
		}

		ad := &AgendaDay{
			Idx:  idx,
			Date: startDate.AddDate(0, 0, idx-1),
			Info: infosByDay[idx],
			All:  sessions,
		}
		bucketByBreaks(ad, sessions)
		out = append(out, ad)
	}
	return out
}

// dayStart returns the midnight at the start of t in loc. We compare
// by date alone when computing day indices, so the time-of-day on
// conf.StartDate doesn't matter (some confs may store noon, some 09:00,
// etc).
func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// dayIndex returns the 1-based day index of when within the conf:
// Day 1 = conf start date, Day N = N-1 calendar days later.
func dayIndex(confStart, when time.Time, loc *time.Location) int {
	w := dayStart(when, loc)
	return int(w.Sub(confStart).Hours()/24) + 1
}

// sortTalksForAgenda orders talks by Sched.Start, then by venue rank
// (one < two < three < four < anything-else < empty). Talks with no
// Sched have already been filtered upstream.
func sortTalksForAgenda(talks []*types.Talk) {
	sort.SliceStable(talks, func(i, j int) bool {
		ti, tj := talks[i].Sched.Start, talks[j].Sched.Start
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return venueRank(talks[i].Venue) < venueRank(talks[j].Venue)
	})
}

// venueRank gives the canonical display order. Unknown venues sort
// last (alphabetically among themselves via SliceStable on the caller).
func venueRank(v string) int {
	switch v {
	case "one":
		return 1
	case "two":
		return 2
	case "three":
		return 3
	case "four":
		return 4
	case "":
		return 1000
	default:
		return 100
	}
}

// bucketByBreaks splits sessions into Morning/Afternoon/Evening using
// the day's Lunch and Coffee start times. Missing Lunch → no
// Morning/Afternoon split (everything before Coffee is Morning).
// Missing Coffee → no Afternoon/Evening split. Both missing → all
// sessions land in Morning so the desktop bucketed view still has a
// single grid to render.
//
// Comparison is wall-clock minute-of-day, not absolute time: ConfInfo
// times come back anchored in conf.StartDate's tz (often the conf
// admin's local tz, e.g. CDT), while talk Sched.Start carries the
// venue's tz (e.g. CEST). Both are conceptually "venue local time" so
// we normalize by clock minutes and ignore offset.
func bucketByBreaks(ad *AgendaDay, sessions []*types.Session) {
	lunchMin, hasLunch := -1, false
	coffeeMin, hasCoffee := -1, false
	if ad.Info != nil {
		if ad.Info.Lunch != nil {
			lunchMin = ad.Info.Lunch.Start.Hour()*60 + ad.Info.Lunch.Start.Minute()
			hasLunch = true
		}
		if ad.Info.Coffee != nil {
			coffeeMin = ad.Info.Coffee.Start.Hour()*60 + ad.Info.Coffee.Start.Minute()
			hasCoffee = true
		}
	}
	for _, s := range sessions {
		if s.Sched == nil {
			ad.Morning = append(ad.Morning, s)
			continue
		}
		startMin := s.Sched.Start.Hour()*60 + s.Sched.Start.Minute()
		switch {
		case hasCoffee && startMin >= coffeeMin:
			ad.Evening = append(ad.Evening, s)
		case hasLunch && startMin >= lunchMin:
			ad.Afternoon = append(ad.Afternoon, s)
		default:
			ad.Morning = append(ad.Morning, s)
		}
	}
}

// confInfosByDay flattens a tag-keyed map (the dashboard's existing
// map[tag][]*ConfInfo) into a Day → *ConfInfo map for one conf. Used
// by RenderConf to feed buildAgendaDays.
func confInfosByDay(infos []*types.ConfInfo) map[int]*types.ConfInfo {
	out := make(map[int]*types.ConfInfo, len(infos))
	for _, ci := range infos {
		if ci != nil && ci.Day > 0 {
			out[ci.Day] = ci
		}
	}
	return out
}

// computeCountdownBounds returns the (doors-open-day-1, doors-close-
// last-day) timestamps that drive the countdown widget on conf pages.
// Prefers ConfInfo.Doors when available (Day 1 for start, the highest
// Day with a Doors row for end); falls back to Conf.StartDate /
// Conf.EndDate when no ConfInfo is available, snapping to a 9:00 -
// 18:00 default window so the countdown still shows something useful.
// Either return value can be nil if even the fallback isn't set.
func computeCountdownBounds(conf *types.Conf, infosByDay map[int]*types.ConfInfo) (*time.Time, *time.Time) {
	if conf == nil {
		return nil, nil
	}
	loc := conf.Loc()
	var start, end *time.Time

	if infosByDay != nil {
		if ci, ok := infosByDay[1]; ok && ci != nil && ci.Doors != nil {
			t := ci.Doors.Start.In(loc)
			start = &t
		}
		// Find the highest Day index that has a Doors.End set.
		hi := -1
		for d := range infosByDay {
			if d > hi {
				if ci := infosByDay[d]; ci != nil && ci.Doors != nil && ci.Doors.End != nil {
					hi = d
				}
			}
		}
		if hi > 0 {
			t := infosByDay[hi].Doors.End.In(loc)
			end = &t
		}
	}

	// Fallbacks for confs without ConfInfo doors filled in: 9 AM
	// of StartDate's calendar day for open, 18:00 of EndDate's day
	// for close. Approximate but better than nothing.
	if start == nil && !conf.StartDate.IsZero() {
		sd := conf.StartDate.In(loc)
		t := time.Date(sd.Year(), sd.Month(), sd.Day(), 9, 0, 0, 0, loc)
		start = &t
	}
	if end == nil && !conf.EndDate.IsZero() {
		ed := conf.EndDate.In(loc)
		t := time.Date(ed.Year(), ed.Month(), ed.Day(), 18, 0, 0, 0, loc)
		end = &t
	}
	return start, end
}
