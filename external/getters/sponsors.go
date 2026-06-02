package getters

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func getOrgs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting orgs...")
	if UsePostgresBackend(ctx) {
		orgs, err = listOrgsPostgres(ctx)
	} else {
		orgs, err = ListOrgsNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching orgs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d orgs!", len(orgs))
		writeCache("orgs", orgs)
	}
}

/* This may return nil */
func FetchOrgsCached(ctx *config.AppContext) ([]*types.Org, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if orgs == nil || lastOrgFetch.Before(deadline) {
		lastOrgFetch = time.Now()
		queueRefresh(JobOrgs)
	}

	return orgs, nil
}

func ListOrgs(ctx *config.AppContext) ([]*types.Org, error) {
	if UsePostgresBackend(ctx) {
		return listOrgsPostgres(ctx)
	}
	return ListOrgsNotion(ctx.Notion)
}

// orgListCache memoizes ListOrgs for the autocomplete endpoint, which can
// fire several times per second as the user types. The TTL is short
// enough that admin-side org additions show up promptly.
var (
	orgListCacheMu  sync.Mutex
	orgListCached   []*types.Org
	orgListCachedAt time.Time
)

const orgListCacheTTL = 5 * time.Minute

func listOrgsCached(ctx *config.AppContext) ([]*types.Org, error) {
	orgListCacheMu.Lock()
	if orgListCached != nil && time.Since(orgListCachedAt) < orgListCacheTTL {
		out := orgListCached
		orgListCacheMu.Unlock()
		return out, nil
	}
	orgListCacheMu.Unlock()

	orgs, err := ListOrgs(ctx)
	if err != nil {
		return nil, err
	}
	orgListCacheMu.Lock()
	orgListCached = orgs
	orgListCachedAt = time.Now()
	orgListCacheMu.Unlock()
	return orgs, nil
}

// SearchOrgsByName returns up to limit orgs whose name contains q
// (case-insensitive substring). Used by the autocomplete on the speaker
// info editor. Backed by listOrgsCached so rapid keystrokes don't hammer
// Notion.
func SearchOrgsByName(ctx *config.AppContext, q string, limit int) ([]*types.Org, error) {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return nil, nil
	}
	orgs, err := listOrgsCached(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*types.Org, 0, limit)
	for _, o := range orgs {
		if o == nil || o.Name == "" {
			continue
		}
		if strings.Contains(strings.ToLower(o.Name), q) {
			out = append(out, o)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func GetOrg(ctx *config.AppContext, ref string) (*types.Org, error) {
	if UsePostgresBackend(ctx) {
		orgs, err := listOrgsPostgres(ctx)
		if err != nil {
			return nil, err
		}
		for _, org := range orgs {
			if org != nil && org.Ref == ref {
				return org, nil
			}
		}
		return nil, fmt.Errorf("org %s not found", ref)
	}
	return GetOrgNotion(ctx.Notion, ref)
}

// sponsorshipsCache memoizes ListSponsorships across requests so the
// public conf page doesn't re-query Notion on every hit. We fetch the
// full Sponsorships DB once, bucket by conf.Ref, and serve from the
// in-memory map until TTL. TTL is short enough that admin-side
// sponsor edits land within a few minutes.
var (
	sponsorshipsCacheMu   sync.Mutex
	sponsorshipsByConf    map[string][]*types.Sponsorship
	sponsorshipsFetchedAt time.Time
)

const sponsorshipsCacheTTL = 5 * time.Minute

// FetchSponsorshipsForConfCached returns the Sponsorship rows for a
// given conf.Ref, served from a 5-min memoized cache. The first call
// (or first call after the TTL) fetches every Sponsorship row from
// Notion and buckets by conf; subsequent calls within TTL hit the
// in-memory map.
func FetchSponsorshipsForConfCached(ctx *config.AppContext, confRef string) ([]*types.Sponsorship, error) {
	sponsorshipsCacheMu.Lock()
	if sponsorshipsByConf != nil && time.Since(sponsorshipsFetchedAt) < sponsorshipsCacheTTL {
		out := sponsorshipsByConf[confRef]
		sponsorshipsCacheMu.Unlock()
		return out, nil
	}
	sponsorshipsCacheMu.Unlock()

	all, err := ListSponsorships(ctx, "")
	if err != nil {
		return nil, err
	}
	byConf := map[string][]*types.Sponsorship{}
	for _, sp := range all {
		if sp == nil {
			continue
		}
		for _, c := range sp.Confs {
			if c == nil {
				continue
			}
			byConf[c.Ref] = append(byConf[c.Ref], sp)
		}
	}
	sponsorshipsCacheMu.Lock()
	sponsorshipsByConf = byConf
	sponsorshipsFetchedAt = time.Now()
	sponsorshipsCacheMu.Unlock()
	return byConf[confRef], nil
}

// InvalidateSponsorshipsCache forces the next FetchSponsorshipsForConfCached
// call to refresh from Notion. Wire this into any admin-side write
// path that mutates Sponsorships (RegisterSponsorship,
// UpdateSponsorshipStatus, etc.) so admin edits show up promptly.
func InvalidateSponsorshipsCache() {
	sponsorshipsCacheMu.Lock()
	sponsorshipsFetchedAt = time.Time{}
	sponsorshipsCacheMu.Unlock()
}

func ListSponsorships(ctx *config.AppContext, confRef string) ([]*types.Sponsorship, error) {
	if UsePostgresBackend(ctx) {
		return listSponsorshipsPostgres(ctx, confRef)
	}
	return ListSponsorshipsNotion(ctx, confRef)
}

func ListSponsorshipsOnly(n *types.Notion) ([]*types.Sponsorship, error) {
	return ListSponsorshipsOnlyNotion(n)
}

// OrgUpdate is a sparse fill-only update for an existing Org row. Empty
// values are skipped.
type OrgUpdate struct {
	Website   string
	Twitter   string // bare handle
	Nostr     string
	Github    string
	LogoLight string // full Spaces URL
	LogoDark  string
}

func RegisterOrg(ctx *config.AppContext, org *types.Org) (string, error) {
	if UsePostgresBackend(ctx) {
		return registerOrgPostgres(ctx, org)
	}
	return RegisterOrgNotion(ctx.Notion, org)
}

func UpdateOrg(ctx *config.AppContext, orgID string, up OrgUpdate) error {
	if UsePostgresBackend(ctx) {
		return updateOrgPostgres(ctx, orgID, up)
	}
	return UpdateOrgNotion(ctx.Notion, orgID, up)
}

func UpdateOrgDetails(ctx *config.AppContext, org *types.Org) error {
	if UsePostgresBackend(ctx) {
		return updateOrgDetailsPostgres(ctx, org)
	}
	return UpdateOrgDetailsNotion(ctx.Notion, org)
}

func FindOrg(ctx *config.AppContext, website, name string) (*types.Org, error) {
	if UsePostgresBackend(ctx) {
		return findOrgPostgres(ctx, website, name)
	}
	return FindOrgNotion(ctx.Notion, website, name)
}

func RegisterSponsorship(ctx *config.AppContext, sp *types.Sponsorship) error {
	if UsePostgresBackend(ctx) {
		return registerSponsorshipPostgres(ctx, sp)
	}
	return RegisterSponsorshipNotion(ctx.Notion, sp)
}

func UpdateSponsorshipStatus(ctx *config.AppContext, ref string, status string) error {
	if UsePostgresBackend(ctx) {
		return updateSponsorshipStatusPostgres(ctx, ref, status)
	}
	return UpdateSponsorshipStatusNotion(ctx.Notion, ref, status)
}
