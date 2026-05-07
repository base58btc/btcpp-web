package handlers

import (
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// SponsorTier captures everything the sponsor-section template needs
// to know about a single tier on a conf page: canonical Level (the
// stored value), display Label (themed-per-conf — e.g. "Satoshi
// Level Sponsors"), the rendered list of sponsorships in display
// order, and the layout hints. Each tier renders in its own
// flex-wrap container so partial rows + single-sponsor tiers
// center naturally rather than left-aligning into an empty cell.
type SponsorTier struct {
	Level    string
	Label    string
	Sponsors []*types.Sponsorship
	// HeightClass is the Tailwind max-height utility (e.g. "max-h-32")
	// applied to each <img> in this tier. Higher tier = taller logo.
	HeightClass string
	// FullRow signals that each tile in the tier should occupy a
	// full row by itself (one sponsor per row). True for the most
	// prominent tiers — Diamond, Title, Workshop. Other tiers wrap
	// at 2 / 3 / 4 per row depending on viewport.
	FullRow bool
}

// tierConfig stores the per-tier render hints. Order in this slice
// is the on-page render order — Diamond (the headline tier) at the
// top, Community at the bottom. Heights tuned from floripa26's
// hand-styled layout; canonical Level names map onto themed labels
// like "Satoshi Level Sponsors" via the per-row Label field.
var tierConfig = []struct {
	Level   string
	Height  string
	FullRow bool
}{
	// Headline + Diamond are the same render tier; both are at the
	// top of the page and equally large. Stored as separate Level
	// values so admins can keep "Headline Sponsors" sections
	// visually grouped without splitting on Diamond. Order in this
	// slice is on-page render order — Headline first, then
	// everything else.
	{"Headline", "max-h-36", true},
	{"Diamond", "max-h-36", true},
	{"Title", "max-h-32", true},
	{"Gold", "max-h-24", false},
	{"Workshop", "max-h-24", true},
	{"Hackathon", "max-h-24", false},
	{"Silver", "max-h-20", false},
	{"Bronze", "max-h-16", false},
	{"Networking", "max-h-20", false},
	{"Media", "max-h-24", false},
	{"Community", "max-h-20", false},
}

// SponsorTiersForConf groups every Sponsorship attached to confRef
// into SponsorTier buckets, ordered by the tierConfig list above.
// Buckets are keyed by (Level, Label) — multiple labels at the same
// canonical level (rare, but possible if a conf had e.g. "Satoshi
// Level" and "Headline" both → Diamond) get their own section.
//
// Returns nil when confRef is empty or the conf has no sponsorships.
// Any sponsorship with a Level not in tierConfig falls into a
// trailing implicit "other" bucket so we never silently drop rows.
func SponsorTiersForConf(ctx *config.AppContext, confRef string) []*SponsorTier {
	if confRef == "" {
		return nil
	}
	all, err := getters.FetchSponsorshipsForConfCached(ctx, confRef)
	if err != nil {
		ctx.Err.Printf("SponsorTiersForConf %s: %s", confRef, err)
		return nil
	}
	return groupSponsorTiers(all)
}

// groupSponsorTiers is the pure-function core of SponsorTiersForConf
// — separated so it's testable without a Notion client. Buckets by
// (Level, Label) and orders by tierConfig.
func groupSponsorTiers(all []*types.Sponsorship) []*SponsorTier {
	if len(all) == 0 {
		return nil
	}
	type key struct{ Level, Label string }
	buckets := map[key]*SponsorTier{}
	for _, sp := range all {
		if sp == nil || sp.Org == nil {
			continue
		}
		// Only Paid / Committed / Confirmed sponsorships render on
		// the public page. Anything else — including blank Status —
		// is treated as not-yet-public and gets hidden until the
		// admin moves it forward.
		if !visibleSponsorStatus(sp.Status) {
			continue
		}
		// Normalize the stored Level so admin-typed variants
		// ("Headline Sponsors" vs "Headline", lowercase, trailing
		// " Level", etc.) all resolve to the canonical tierConfig
		// entry. Falls back to the raw value when nothing matches
		// — that row sinks to the bottom of the page.
		level := normalizeLevel(sp.Level)
		if level == "" {
			level = sp.Level
		}
		label := sp.Label
		if label == "" {
			label = defaultLabelForLevel(level)
		}
		k := key{Level: level, Label: label}
		t, ok := buckets[k]
		if !ok {
			t = &SponsorTier{
				Level:       level,
				Label:       label,
				HeightClass: heightForLevel(level),
				FullRow:     fullRowForLevel(level),
			}
			buckets[k] = t
		}
		t.Sponsors = append(t.Sponsors, sp)
	}

	out := make([]*SponsorTier, 0, len(buckets))
	for _, t := range buckets {
		// Within a tier, sort by Org name for stable rendering.
		sort.SliceStable(t.Sponsors, func(i, j int) bool {
			ni := ""
			nj := ""
			if t.Sponsors[i].Org != nil {
				ni = strings.ToLower(t.Sponsors[i].Org.Name)
			}
			if t.Sponsors[j].Org != nil {
				nj = strings.ToLower(t.Sponsors[j].Org.Name)
			}
			return ni < nj
		})
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return tierRank(out[i].Level) < tierRank(out[j].Level)
	})
	return out
}

// visibleSponsorStatus returns true for Sponsorship.Status values
// that should appear on the public conf page. Paid / Committed /
// Confirmed are all "the deal is locked in" states; anything else
// (Pending, Invoiced, blank, …) stays hidden.
func visibleSponsorStatus(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "paid", "committed", "confirmed":
		return true
	}
	return false
}

// normalizeLevel makes the Notion Level value tolerate small admin
// variations: case differences ("Diamond" vs "diamond"), trailing
// " Sponsor" / " Sponsors" / " Level" suffixes that admins
// sometimes type into the select. Returns the matching canonical
// Level name from tierConfig, or "" when no match.
func normalizeLevel(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	for _, suf := range []string{" sponsors", " sponsor", " level"} {
		s = strings.TrimSuffix(s, suf)
		s = strings.TrimSpace(s)
	}
	for _, c := range tierConfig {
		if strings.EqualFold(c.Level, s) {
			return c.Level
		}
	}
	return ""
}

// tierRank returns the position of a Level in tierConfig — used as
// the on-page sort key. Unknown levels rank after every known tier.
func tierRank(level string) int {
	for i, c := range tierConfig {
		if c.Level == level {
			return i
		}
	}
	return len(tierConfig)
}

func heightForLevel(level string) string {
	for _, c := range tierConfig {
		if c.Level == level {
			return c.Height
		}
	}
	return "max-h-16"
}

func fullRowForLevel(level string) bool {
	for _, c := range tierConfig {
		if c.Level == level {
			return c.FullRow
		}
	}
	return false
}

// defaultLabelForLevel is the fallback heading when a Sponsorship
// row didn't supply its own Label. "Sponsors" for the catch-all
// Bronze tier; otherwise "{Level} Sponsors".
func defaultLabelForLevel(level string) string {
	if level == "" || level == "Bronze" {
		return "Sponsors"
	}
	return level + " Sponsors"
}

// SponsorBannerForConf returns the subset of sponsorships shown in
// the marquee scrolling banner near the top of the conf page —
// Title, Diamond, and Workshop tiers only. Sorted by tier rank, then
// alphabetically by org name within each tier so the banner order is
// stable across renders.
func SponsorBannerForConf(ctx *config.AppContext, confRef string) []*types.Sponsorship {
	tiers := SponsorTiersForConf(ctx, confRef)
	keep := map[string]bool{
		"Headline": true,
		"Diamond":  true,
		"Title":    true,
		"Workshop": true,
	}
	var out []*types.Sponsorship
	for _, t := range tiers {
		if !keep[t.Level] {
			continue
		}
		out = append(out, t.Sponsors...)
	}
	return out
}
