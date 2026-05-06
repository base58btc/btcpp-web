package handlers

import (
	"sort"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// NavConfList drives the dynamic events flyout in the main nav. The
// template ranges over Upcoming first (next conf at the top) and Past
// after that (most-recent-first), so the same struct serves both
// desktop and mobile.
type NavConfList struct {
	Upcoming []*types.Conf
	Past     []*types.Conf
}

// buildNavConfList pulls the warm conf cache, drops inactive rows, and
// splits by HasEnded(). Sort order is "next event soonest" for
// upcoming and "most recently ended" for past so the freshest items
// land at the top of each list.
func buildNavConfList(ctx *config.AppContext) NavConfList {
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("navConfs: %s", err)
		return NavConfList{}
	}
	// Tags hardcoded at the bottom of the Past flyout as YouTube
	// playlist links — exclude them from the dynamic list so they
	// don't render twice if a row exists in Notion.
	hardcodedPast := map[string]bool{"atx22": true, "cdmx22": true}

	var upcoming, past []*types.Conf
	for _, c := range confs {
		if c == nil {
			continue
		}
		if hardcodedPast[c.Tag] {
			continue
		}
		// Past confs ignore the Active flag — once an event has ended,
		// the row is typically flipped to inactive in Notion, but we
		// still want it on the public archive list. Upcoming confs
		// keep the Active gate so unpublished drafts don't leak.
		if c.HasEnded() {
			past = append(past, c)
		} else if c.Active {
			upcoming = append(upcoming, c)
		}
	}
	sort.Slice(upcoming, func(i, j int) bool {
		return upcoming[i].StartDate.Before(upcoming[j].StartDate)
	})
	sort.Slice(past, func(i, j int) bool {
		return past[i].StartDate.After(past[j].StartDate)
	})
	return NavConfList{Upcoming: upcoming, Past: past}
}
