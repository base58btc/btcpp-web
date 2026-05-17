package handlers

import (
	"sync"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

const (
	adminDashboardProposalTTL  = time.Minute
	adminDashboardCountdownTTL = 10 * time.Minute
)

type adminDashboardProposalSnapshot struct {
	pendingCount    int
	decisionedCount int
	proposals       []*types.Proposal
	fetchedAt       time.Time
	refreshing      bool
}

type adminDashboardCountdownSnapshot struct {
	start      *time.Time
	end        *time.Time
	fetchedAt  time.Time
	refreshing bool
}

var (
	adminDashboardProposalMu    sync.Mutex
	adminDashboardProposalCache = map[string]*adminDashboardProposalSnapshot{}

	adminDashboardCountdownMu    sync.Mutex
	adminDashboardCountdownCache = map[string]*adminDashboardCountdownSnapshot{}
)

func adminDashboardCacheKey(conf *types.Conf) string {
	if conf == nil {
		return ""
	}
	if conf.Ref != "" {
		return conf.Ref
	}
	return conf.Tag
}

func loadAdminDashboardProposalSnapshotCached(ctx *config.AppContext, conf *types.Conf) (pendingCount int, decisionedCount int, proposals []*types.Proposal, ok bool) {
	key := adminDashboardCacheKey(conf)
	if key == "" {
		return 0, 0, nil, false
	}

	now := time.Now()
	adminDashboardProposalMu.Lock()
	entry := adminDashboardProposalCache[key]
	if entry != nil && now.Sub(entry.fetchedAt) < adminDashboardProposalTTL {
		pendingCount = entry.pendingCount
		decisionedCount = entry.decisionedCount
		proposals = entry.proposals
		adminDashboardProposalMu.Unlock()
		return pendingCount, decisionedCount, proposals, true
	}
	if entry == nil {
		entry = &adminDashboardProposalSnapshot{}
		adminDashboardProposalCache[key] = entry
	}
	pendingCount = entry.pendingCount
	decisionedCount = entry.decisionedCount
	proposals = entry.proposals
	ok = !entry.fetchedAt.IsZero()
	if !entry.refreshing {
		entry.refreshing = true
		go refreshAdminDashboardProposalSnapshot(ctx, key, conf)
	}
	adminDashboardProposalMu.Unlock()
	return pendingCount, decisionedCount, proposals, ok
}

func refreshAdminDashboardProposalSnapshot(ctx *config.AppContext, key string, conf *types.Conf) {
	start := time.Now()
	proposals := loadConfProposals(ctx, conf)
	pending, decisioned := splitProposalsByPending(proposals)

	adminDashboardProposalMu.Lock()
	entry := adminDashboardProposalCache[key]
	if entry == nil {
		entry = &adminDashboardProposalSnapshot{}
		adminDashboardProposalCache[key] = entry
	}
	entry.pendingCount = len(pending)
	entry.decisionedCount = len(decisioned)
	entry.proposals = proposals
	entry.fetchedAt = time.Now()
	entry.refreshing = false
	adminDashboardProposalMu.Unlock()

	if ctx.Infos != nil {
		ctx.Infos.Printf("/%s/admin proposal counts refresh: %s", conf.Tag, time.Since(start).Round(time.Millisecond))
	}
}

func loadAdminDashboardCountdownCached(ctx *config.AppContext, conf *types.Conf) (start *time.Time, end *time.Time, ok bool) {
	key := adminDashboardCacheKey(conf)
	if key == "" {
		return nil, nil, false
	}

	now := time.Now()
	adminDashboardCountdownMu.Lock()
	entry := adminDashboardCountdownCache[key]
	if entry != nil && now.Sub(entry.fetchedAt) < adminDashboardCountdownTTL {
		start = entry.start
		end = entry.end
		adminDashboardCountdownMu.Unlock()
		return start, end, true
	}
	if entry == nil {
		entry = &adminDashboardCountdownSnapshot{}
		adminDashboardCountdownCache[key] = entry
	}
	start = entry.start
	end = entry.end
	ok = !entry.fetchedAt.IsZero()
	if !entry.refreshing {
		entry.refreshing = true
		go refreshAdminDashboardCountdown(ctx, key, conf)
	}
	adminDashboardCountdownMu.Unlock()

	if start == nil && end == nil {
		confCopy := *conf
		start, end = computeCountdownBounds(&confCopy, nil)
	}
	return start, end, ok
}

func refreshAdminDashboardCountdown(ctx *config.AppContext, key string, conf *types.Conf) {
	started := time.Now()
	confCopy := *conf
	var infosByDay map[int]*types.ConfInfo
	if cis, err := getters.ListConfInfos(ctx, conf.Tag); err == nil {
		infosByDay = confInfosByDay(cis)
	} else if ctx.Err != nil {
		ctx.Err.Printf("/%s/admin countdown refresh: %s", conf.Tag, err)
	}
	start, end := computeCountdownBounds(&confCopy, infosByDay)

	adminDashboardCountdownMu.Lock()
	entry := adminDashboardCountdownCache[key]
	if entry == nil {
		entry = &adminDashboardCountdownSnapshot{}
		adminDashboardCountdownCache[key] = entry
	}
	entry.start = start
	entry.end = end
	entry.fetchedAt = time.Now()
	entry.refreshing = false
	adminDashboardCountdownMu.Unlock()

	if ctx.Infos != nil {
		ctx.Infos.Printf("/%s/admin countdown refresh: %s", conf.Tag, time.Since(started).Round(time.Millisecond))
	}
}
