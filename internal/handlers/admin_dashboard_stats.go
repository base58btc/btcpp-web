package handlers

import (
	"sort"
	"strings"
	"sync"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// OrganizerStats is the headline-numbers panel rendered on the
// per-conf admin dashboard. Each block is best-effort: if a Notion
// query fails the field stays at its zero value (logged once)
// rather than blanking the whole panel.
type OrganizerStats struct {
	TicketsSold         int
	RevenueByCurrency   []CurrencyTotal // sorted by amount desc
	SpeakersConfirmed   int
	PendingApplications int
	VolunteersScheduled int
	SponsorsPaid        int
	SponsorsInProgress  int
	SponsorsCommitted   int
	// AffiliateTickets is the count of ticket-sales credited to
	// any affiliate code for the conf.
	AffiliateTickets int
	TopAffiliates    []*AffiliateRow // up to 3, sorted by EarnedSats desc
}

// CurrencyTotal is one row in the revenue-by-currency breakdown.
// Amount is in main units (dollars / euros / ...) — already
// pre-divided by 100 by the AddTickets writer.
type CurrencyTotal struct {
	Currency string
	Amount   float64
}

// AffiliateRow is one entry in the top-3 affiliates table.
type AffiliateRow struct {
	Email       string
	EarnedSats  int64
	TicketsSold int
}

const organizerStatsCacheTTL = time.Minute

type organizerStatsCacheEntry struct {
	stats      *OrganizerStats
	fetchedAt  time.Time
	refreshing bool
}

var (
	organizerStatsCacheMu sync.Mutex
	organizerStatsCache   = map[string]*organizerStatsCacheEntry{}
)

// loadOrganizerStatsCached returns a fresh-enough stats snapshot when
// one is available and kicks off a background refresh on cache misses
// or stale entries. The dashboard should never wait on the slow
// Notion queries needed for this panel.
func loadOrganizerStatsCached(ctx *config.AppContext, conf *types.Conf, proposals []*types.Proposal, pendingCount int) *OrganizerStats {
	if conf == nil {
		return nil
	}
	key := conf.Ref
	if key == "" {
		key = conf.Tag
	}

	now := time.Now()
	organizerStatsCacheMu.Lock()
	entry := organizerStatsCache[key]
	if entry != nil && entry.stats != nil && now.Sub(entry.fetchedAt) < organizerStatsCacheTTL {
		stats := entry.stats
		organizerStatsCacheMu.Unlock()
		return stats
	}
	if entry == nil {
		entry = &organizerStatsCacheEntry{}
		organizerStatsCache[key] = entry
	}
	stats := entry.stats
	if !entry.refreshing {
		entry.refreshing = true
		go refreshOrganizerStats(ctx, key, conf, proposals, pendingCount)
	}
	organizerStatsCacheMu.Unlock()
	return stats
}

func refreshOrganizerStats(ctx *config.AppContext, key string, conf *types.Conf, proposals []*types.Proposal, pendingCount int) {
	start := time.Now()
	stats := loadOrganizerStats(ctx, conf, proposals, pendingCount)

	organizerStatsCacheMu.Lock()
	entry := organizerStatsCache[key]
	if entry == nil {
		entry = &organizerStatsCacheEntry{}
		organizerStatsCache[key] = entry
	}
	entry.stats = stats
	entry.fetchedAt = time.Now()
	entry.refreshing = false
	organizerStatsCacheMu.Unlock()

	if ctx.Infos != nil {
		ctx.Infos.Printf("/%s/admin stats refresh: %s", conf.Tag, time.Since(start).Round(time.Millisecond))
	}
}

// loadOrganizerStats gathers everything for the panel. Synchronous
// and intentionally called only by the background cache refresher:
// it does live Notion reads for registrations and affiliate usage,
// plus cached reads for shifts and sponsorships. On a busy conf the
// live PurchasesDb scan usually dominates.
//
// pendingCount is passed in because the caller already has it from
// splitProposalsByPending; saves re-loading the proposals slice.
// Pass -1 to mean "stats panel is being shown to a non-admin who
// didn't trigger the pending count" — we'll re-derive locally.
func loadOrganizerStats(ctx *config.AppContext, conf *types.Conf, proposals []*types.Proposal, pendingCount int) *OrganizerStats {
	stats := &OrganizerStats{
		PendingApplications: pendingCount,
	}

	// Tickets sold + revenue by currency. Each PurchasesDb row is
	// one ticket; Amount is the buyer-paid main-unit value.
	if regs, err := getters.FetchRegistrations(ctx, conf.Ref); err == nil {
		stats.TicketsSold = len(regs)
		byCurrency := map[string]float64{}
		for _, r := range regs {
			if r == nil {
				continue
			}
			cur := strings.ToUpper(strings.TrimSpace(r.Currency))
			if cur == "" || r.Amount <= 0 {
				continue
			}
			byCurrency[cur] += r.Amount
		}
		for cur, amt := range byCurrency {
			stats.RevenueByCurrency = append(stats.RevenueByCurrency,
				CurrencyTotal{Currency: cur, Amount: amt})
		}
		sort.Slice(stats.RevenueByCurrency, func(i, j int) bool {
			return stats.RevenueByCurrency[i].Amount > stats.RevenueByCurrency[j].Amount
		})
	} else {
		ctx.Err.Printf("/%s/admin stats: registrations: %s", conf.Tag, err)
	}

	// Speakers confirmed = proposals in either of the two
	// "we're running this talk" states: Accepted (program
	// locked, schedule still draft) or Scheduled (cal invite
	// has gone out).
	if proposals == nil {
		proposals = loadConfProposals(ctx, conf)
	}
	for _, p := range proposals {
		if p != nil && (p.Status == StatusAccepted || p.Status == StatusScheduled) {
			stats.SpeakersConfirmed++
		}
	}
	if pendingCount < 0 {
		// Re-derive when the caller skipped the split.
		pending, _ := splitProposalsByPending(proposals)
		stats.PendingApplications = len(pending)
	}

	// Volunteers scheduled = unique volunteer page IDs across all
	// shifts for the conf (assignees + shift leaders).
	if shifts, err := getters.GetShiftsForConf(ctx, conf.Tag); err == nil {
		unique := map[string]bool{}
		for _, s := range shifts {
			if s == nil {
				continue
			}
			for _, ref := range s.AssigneesRef {
				if ref != "" {
					unique[ref] = true
				}
			}
			if s.ShiftLeaderRef != "" {
				unique[s.ShiftLeaderRef] = true
			}
		}
		stats.VolunteersScheduled = len(unique)
	} else {
		ctx.Err.Printf("/%s/admin stats: shifts: %s", conf.Tag, err)
	}

	// Sponsorship buckets. visibleSponsorStatus only counts Paid +
	// Committed for public render; here we surface the full funnel
	// (Paid / InProgress / Committed) so organizers can see the
	// pipeline at a glance. Other statuses (Pending, Invoiced, …)
	// don't fit any of the three buckets and are silently skipped.
	if sps, err := getters.FetchSponsorshipsForConfCached(ctx, conf.Ref); err == nil {
		for _, sp := range sps {
			if sp == nil {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(sp.Status)) {
			case "paid":
				stats.SponsorsPaid++
			case "inprogress", "in progress":
				stats.SponsorsInProgress++
			case "committed":
				stats.SponsorsCommitted++
			}
		}
	} else {
		ctx.Err.Printf("/%s/admin stats: sponsorships: %s", conf.Tag, err)
	}

	// Affiliate aggregation: count tickets across all rows; group
	// by email; surface the top 3 earners.
	if rows, err := getters.QueryAffiliateUsageByConf(ctx, conf.Tag); err == nil {
		type acc struct {
			earned  int64
			tickets int
		}
		byEmail := map[string]*acc{}
		for _, r := range rows {
			if r == nil {
				continue
			}
			stats.AffiliateTickets += int(r.TicketsCount)
			if r.AffiliateEmail == "" {
				continue
			}
			a, ok := byEmail[r.AffiliateEmail]
			if !ok {
				a = &acc{}
				byEmail[r.AffiliateEmail] = a
			}
			a.earned += r.EarnedSats
			a.tickets += int(r.TicketsCount)
		}
		all := make([]*AffiliateRow, 0, len(byEmail))
		for em, a := range byEmail {
			all = append(all, &AffiliateRow{
				Email: em, EarnedSats: a.earned, TicketsSold: a.tickets,
			})
		}
		sort.Slice(all, func(i, j int) bool {
			return all[i].EarnedSats > all[j].EarnedSats
		})
		if len(all) > 3 {
			all = all[:3]
		}
		stats.TopAffiliates = all
	} else {
		ctx.Err.Printf("/%s/admin stats: affiliates: %s", conf.Tag, err)
	}

	return stats
}
