package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

var cacheSpeakers []*types.Speaker
var lastSpeakerFetch time.Time
var confs []*types.Conf
var lastConfsFetch time.Time
var talks []*types.Talk
var lastTalksFetch time.Time
var discounts []*types.DiscountCode
var lastDiscountFetch time.Time
var hotels []*types.Hotel
var lastHotelFetch time.Time

var jobs []*types.JobType
var lastJobTypeFetch time.Time

var shifts []*types.WorkShift
var lastShiftFetch time.Time

var orgs []*types.Org
var lastOrgFetch time.Time

// New-DB caches for the dashboard hot path. Each is paired with a by-ID map
// so handlers can do O(1) lookups instead of point reads against Notion.
// Maps are rebuilt fully on every refresh (cheap) and protected by a single
// mutex per kind because the slice may be replaced atomically.
var (
	cacheProposals    []*types.Proposal
	proposalByID      map[string]*types.Proposal
	lastProposalFetch time.Time
	proposalCacheMu   sync.RWMutex

	cacheSpeakerConfs    []*types.SpeakerConf
	speakerConfByID      map[string]*types.SpeakerConf
	speakerConfBySpkID   map[string][]*types.SpeakerConf // indexed by Speaker.ID
	lastSpeakerConfFetch time.Time
	speakerConfCacheMu   sync.RWMutex

	cacheConfTalks     []*types.ConfTalk
	confTalkByProposal map[string]*types.ConfTalk
	lastConfTalkFetch  time.Time
	confTalkCacheMu    sync.RWMutex

	cacheRecordings     []*types.Recording
	recordingByConfTalk map[string]*types.Recording
	lastRecordingFetch  time.Time
	recordingCacheMu    sync.RWMutex

	// Site-wide aggregate stats for the about page. Recomputed from the
	// other warm caches + one paginated PurchasesDb scan; refreshed via
	// the worker pool on TTL.
	siteStats          SiteStatsValues
	lastSiteStatsFetch time.Time
	siteStatsMu        sync.RWMutex
)

// SiteStatsValues holds the raw counts behind the about-page numbers.
// Format-for-display is left to callers.
type SiteStatsValues struct {
	PastConfs int // count of confs where EndDate is in the past
	PastTalks int // count of Accepted ConfTalks at past confs
	Attendees int // total rows in PurchasesDb (rough total attendees)
}

type (
	JobType int
)

const (
	JobSpeakers JobType = iota + 1
	JobConfs
	JobTalks
	JobDiscounts
	JobHotels
	JobJobs
	JobShifts
	JobOrgs
	JobProposals
	JobSpeakerConfs
	JobConfTalks
	JobRecordings
	JobSiteStats
)

// Buffered so concurrent FetchXxxCached callers don't block each other when
// the worker is busy. queueRefresh drops sends that would overflow the
// buffer — the cache stays stale for one more TTL cycle, but no handler
// freezes.
var taskChan chan JobType = make(chan JobType, 32)

// queueRefresh attempts to enqueue a cache-refresh job without blocking.
// Returns true if queued, false if the buffer was full (in which case
// another caller's refresh is already in flight or queued).
func queueRefresh(j JobType) bool {
	select {
	case taskChan <- j:
		return true
	default:
		return false
	}
}

var cacheTTL time.Duration

type TalksCallback func(ctx *config.AppContext, talks []*types.Talk)
type SpeakersCallback func(ctx *config.AppContext, speakers []*types.Speaker)

var onTalksRefresh []TalksCallback
var onSpeakersRefresh []SpeakersCallback

func OnTalksRefresh(cb TalksCallback) {
	onTalksRefresh = append(onTalksRefresh, cb)
}

func OnSpeakersRefresh(cb SpeakersCallback) {
	onSpeakersRefresh = append(onSpeakersRefresh, cb)
}

func StartWorkPool(ctx *config.AppContext) {
	// FIXME: I don't think go-notion is threadsafe lmao
	numWorkers := 1

	// Start the worker pool
	for i := 0; i < numWorkers; i++ {
		go workers(ctx, i, taskChan)
	}
}

func CloseWorkPool() {
	close(taskChan)
}

func loadFromCache() bool {
	loaded := true
	if !readCache("confs", &confs) {
		loaded = false
	}
	if !readCache("speakers", &cacheSpeakers) {
		loaded = false
	}
	if !readCache("talks", &talks) {
		loaded = false
	}
	if !readCache("discounts", &discounts) {
		loaded = false
	}
	if !readCache("hotels", &hotels) {
		loaded = false
	}
	if !readCache("jobs", &jobs) {
		loaded = false
	}
	if !readCache("shifts", &shifts) {
		loaded = false
	}
	if !readCache("orgs", &orgs) {
		loaded = false
	}

	if loaded {
		now := time.Now()
		lastConfsFetch = now
		lastSpeakerFetch = now
		lastTalksFetch = now
		lastDiscountFetch = now
		lastHotelFetch = now
		lastJobTypeFetch = now
		lastShiftFetch = now
		lastOrgFetch = now
		return true
	}

	return false
}

// diskCacheBootstrapped tracks whether we've already attempted to bootstrap
// from the on-disk cache. The disk cache is a startup convenience only — once
// the process has booted (whether bootstrap succeeded or fell through to
// Notion), every subsequent WaitFetch goes straight to Notion. This makes
// /conf-reload always re-fetch live data instead of returning stale rows.
var diskCacheBootstrapped = false

func WaitFetch(ctx *config.AppContext) {
	cacheTTL = time.Duration(ctx.Env.CacheTTLSec) * time.Second

	if !ctx.InProduction {
		EnableDiskCache()
	}

	// Try the disk cache for legacy types (confs / speakers / talks /
	// discounts / hotels / jobs / shifts / orgs) on the first boot. The
	// new-DB caches (proposals / speakerconfs / conftalks / recordings)
	// have no disk persistence and are always fetched fresh below.
	loadedFromDisk := false
	if !diskCacheBootstrapped && diskCacheEnabled && loadFromCache() {
		ctx.Infos.Printf("Loaded legacy data from disk cache!")
		loadedFromDisk = true
	}
	diskCacheBootstrapped = true

	if !loadedFromDisk {
		ctx.Infos.Printf("Fetching legacy types from Notion...")
		// Phase 1: independent legacy types
		var wg sync.WaitGroup
		wg.Add(6)
		go func() { defer wg.Done(); runJob(ctx, JobConfs); lastConfsFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobSpeakers); lastSpeakerFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobDiscounts); lastDiscountFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobHotels); lastHotelFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobJobs); lastJobTypeFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobOrgs); lastOrgFetch = time.Now() }()
		wg.Wait()

		// Phase 2 (legacy): depends on Speakers / Confs from phase 1
		wg.Add(2)
		go func() { defer wg.Done(); runJob(ctx, JobTalks); lastTalksFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobShifts); lastShiftFetch = time.Now() }()
		wg.Wait()
	}

	// New-DB types — always fetched, even when the legacy disk cache hit,
	// since they have no on-disk copy and the dashboard depends on them.
	ctx.Infos.Printf("Fetching new-DB types from Notion...")

	// Phase A: independent. Proposals + Recordings have no inter-deps.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); runJob(ctx, JobProposals); lastProposalFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobRecordings); lastRecordingFetch = time.Now() }()
	wg.Wait()

	// Phase B: ConfTalks need Proposals (resolves ct.Proposal pointer);
	// SpeakerConfs need Proposals + Speakers. Both can now run in parallel.
	wg.Add(2)
	go func() { defer wg.Done(); runJob(ctx, JobConfTalks); lastConfTalkFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobSpeakerConfs); lastSpeakerConfFetch = time.Now() }()
	wg.Wait()

	// Site stats run async — they're only used by the about page so
	// they can warm up after WaitFetch returns. Don't block boot on the
	// PurchasesDb scan, which can be 30+ paginated calls.
	go func() {
		runJob(ctx, JobSiteStats)
		lastSiteStatsFetch = time.Now()
	}()
}

func runJob(ctx *config.AppContext, job JobType) {
	switch job {
	case JobConfs:
		getConfs(ctx)
	case JobSpeakers:
		getSpeakers(ctx)
	case JobTalks:
		getTalks(ctx)
	case JobDiscounts:
		getDiscounts(ctx)
	case JobHotels:
		getHotels(ctx)
	case JobJobs:
		getJobs(ctx)
	case JobShifts:
		getShifts(ctx)
	case JobOrgs:
		getOrgs(ctx)
	case JobProposals:
		getProposals(ctx)
	case JobSpeakerConfs:
		getSpeakerConfs(ctx)
	case JobConfTalks:
		getConfTalks(ctx)
	case JobRecordings:
		getRecordings(ctx)
	case JobSiteStats:
		getSiteStats(ctx)
	}
}

func workers(ctx *config.AppContext, id int, c chan JobType) {
	for job := range c {
		ctx.Infos.Printf("%d starting job type %d", id, job)
		runJob(ctx, job)
		ctx.Infos.Printf("%d finished job type %d", id, job)
	}
}

// InvalidateProposalsCache forces the next FetchProposalsCached / Fetch
// ProposalByID to refresh from Notion. Call after a write that changed
// proposal data (status flip, edit, etc.).
func InvalidateProposalsCache() {
	proposalCacheMu.Lock()
	lastProposalFetch = time.Time{}
	proposalCacheMu.Unlock()
}

// getSpeakerConfs refreshes the SpeakerConf cache. Depends on Speakers and
// Proposals being already cached so parseSpeakerConf can resolve relations.
func getSpeakerConfs(ctx *config.AppContext) {
	ctx.Infos.Printf("getting speaker confs...")
	// Build the per-call resolution maps from the existing caches.
	speakerMap := make(map[string]*types.Speaker, len(cacheSpeakers))
	for _, s := range cacheSpeakers {
		if s != nil {
			speakerMap[s.ID] = s
		}
	}
	proposalCacheMu.RLock()
	pmap := make(map[string]*types.Proposal, len(proposalByID))
	for k, v := range proposalByID {
		pmap[k] = v
	}
	proposalCacheMu.RUnlock()

	scs, err := ListSpeakerConfs(ctx, speakerMap, pmap)
	if err != nil {
		ctx.Err.Printf("error fetching speakerconfs %s", err)
		return
	}
	idx := make(map[string]*types.SpeakerConf, len(scs))
	bySpk := make(map[string][]*types.SpeakerConf)
	for _, sc := range scs {
		if sc == nil {
			continue
		}
		idx[sc.ID] = sc
		if sc.Speaker != nil {
			bySpk[sc.Speaker.ID] = append(bySpk[sc.Speaker.ID], sc)
		}
	}
	speakerConfCacheMu.Lock()
	cacheSpeakerConfs = scs
	speakerConfByID = idx
	speakerConfBySpkID = bySpk
	speakerConfCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d speaker confs!", len(scs))
}

// FetchSpeakerConfsForSpeaker returns the cached SpeakerConf rows linked to
// speakerID. Triggers an async refresh on TTL expiry but returns stale
// data immediately rather than blocking.
func FetchSpeakerConfsForSpeaker(ctx *config.AppContext, speakerID string) []*types.SpeakerConf {
	deadline := time.Now().Add(-cacheTTL)
	speakerConfCacheMu.RLock()
	stale := cacheSpeakerConfs == nil || lastSpeakerConfFetch.Before(deadline)
	out := append([]*types.SpeakerConf(nil), speakerConfBySpkID[speakerID]...)
	speakerConfCacheMu.RUnlock()
	if stale {
		lastSpeakerConfFetch = time.Now()
		queueRefresh(JobSpeakerConfs)
	}
	return out
}

// FetchSpeakerConfByID returns the cached SpeakerConf for the given page
// ID, or nil if not in cache.
func FetchSpeakerConfByID(id string) *types.SpeakerConf {
	speakerConfCacheMu.RLock()
	defer speakerConfCacheMu.RUnlock()
	return speakerConfByID[id]
}

func InvalidateSpeakerConfsCache() {
	speakerConfCacheMu.Lock()
	lastSpeakerConfFetch = time.Time{}
	speakerConfCacheMu.Unlock()
}

// CacheSpeakerInsert appends a Speaker to the in-memory speakers cache.
// Used after CreateSpeaker so the just-written row is findable by
// GetSpeakersByEmail without waiting for the periodic refresh tick —
// which can otherwise leave a freshly-invited co-speaker staring at an
// empty dashboard.
//
// No mutex on cacheSpeakers — readers (e.g. GetSpeakersByEmail) snapshot
// the slice via a single assignment. We follow the same pattern by
// rewriting the slice header atomically with append.
func CacheSpeakerInsert(s *types.Speaker) {
	if s == nil {
		return
	}
	cacheSpeakers = append(cacheSpeakers, s)
}

// CacheSpeakerConfInsert adds a fresh SpeakerConf to the in-memory
// caches (slice + by-ID + by-speakerID indexes) so the next
// FetchSpeakerConfsForSpeaker call sees it. Used after a successful
// UpsertSpeakerConf create.
//
// Idempotent on ID — calling twice with the same row replaces the
// previous entry rather than duplicating it.
func CacheSpeakerConfInsert(sc *types.SpeakerConf) {
	if sc == nil {
		return
	}
	speakerConfCacheMu.Lock()
	defer speakerConfCacheMu.Unlock()

	if speakerConfByID == nil {
		speakerConfByID = make(map[string]*types.SpeakerConf)
	}
	if speakerConfBySpkID == nil {
		speakerConfBySpkID = make(map[string][]*types.SpeakerConf)
	}

	if existing, ok := speakerConfByID[sc.ID]; ok {
		// Replace the existing pointer in the by-spkID slice too. Look
		// up the speaker via the existing entry — the new one might
		// have a different (or nil) Speaker pointer if the caller
		// constructed it sparsely.
		if existing.Speaker != nil {
			list := speakerConfBySpkID[existing.Speaker.ID]
			for i, e := range list {
				if e != nil && e.ID == sc.ID {
					list[i] = sc
					break
				}
			}
		}
	} else {
		cacheSpeakerConfs = append(cacheSpeakerConfs, sc)
	}
	speakerConfByID[sc.ID] = sc
	if sc.Speaker != nil {
		// Avoid duplicate entry in the by-spkID list when this is a
		// replace path that's already linked.
		list := speakerConfBySpkID[sc.Speaker.ID]
		seen := false
		for _, e := range list {
			if e != nil && e.ID == sc.ID {
				seen = true
				break
			}
		}
		if !seen {
			speakerConfBySpkID[sc.Speaker.ID] = append(list, sc)
		}
	}
}

// CacheSpeakerByID looks up a Speaker pointer in the warm cache for
// callers that need to attach it to a fresh SpeakerConf before
// inserting. Returns nil when the cache doesn't have it (e.g. the
// speaker was created in another process and we haven't refreshed
// since).
func CacheSpeakerByID(id string) *types.Speaker {
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == id {
			return s
		}
	}
	return nil
}

// getConfTalks refreshes the ConfTalk cache. Depends on Proposals being
// cached so parseConfTalk can attach the linked Proposal pointer.
func getConfTalks(ctx *config.AppContext) {
	ctx.Infos.Printf("getting conftalks...")
	proposalCacheMu.RLock()
	pmap := make(map[string]*types.Proposal, len(proposalByID))
	for k, v := range proposalByID {
		pmap[k] = v
	}
	proposalCacheMu.RUnlock()

	cts, err := ListConfTalks(ctx, pmap)
	if err != nil {
		ctx.Err.Printf("error fetching conftalks %s", err)
		return
	}
	byProp := make(map[string]*types.ConfTalk, len(cts))
	for _, ct := range cts {
		if ct != nil && ct.Proposal != nil {
			byProp[ct.Proposal.ID] = ct
		}
	}
	confTalkCacheMu.Lock()
	cacheConfTalks = cts
	confTalkByProposal = byProp
	confTalkCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d conftalks!", len(cts))
}

// FetchConfTalkByProposal returns the cached ConfTalk for proposalID, or
// nil if no ConfTalk exists yet (or if the cache is empty).
func FetchConfTalkByProposal(proposalID string) *types.ConfTalk {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	return confTalkByProposal[proposalID]
}

// FetchConfTalkByID walks the warm cache for a ConfTalk with the given
// page ID. Linear scan because the cache isn't indexed by ID today —
// fine for admin-list page sizes (low hundreds).
func FetchConfTalkByID(id string) *types.ConfTalk {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID == id {
			return ct
		}
	}
	return nil
}

func InvalidateConfTalksCache() {
	confTalkCacheMu.Lock()
	lastConfTalkFetch = time.Time{}
	confTalkCacheMu.Unlock()
}

// getRecordings refreshes the Recording cache + by-ConfTalk index.
func getRecordings(ctx *config.AppContext) {
	ctx.Infos.Printf("getting recordings...")
	recs, err := ListRecordings(ctx)
	if err != nil {
		ctx.Err.Printf("error fetching recordings %s", err)
		return
	}
	byCT := make(map[string]*types.Recording, len(recs))
	for _, r := range recs {
		if r != nil && r.ConfTalkID != "" {
			byCT[r.ConfTalkID] = r
		}
	}
	recordingCacheMu.Lock()
	cacheRecordings = recs
	recordingByConfTalk = byCT
	recordingCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d recordings!", len(recs))
}

// FetchRecordingByConfTalk returns the cached Recording linked to
// confTalkID, or nil if none.
func FetchRecordingByConfTalk(confTalkID string) *types.Recording {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	return recordingByConfTalk[confTalkID]
}

// FetchYTLinkForTalk bridges the legacy Talks-DB renderer (which uses
// Talk.ID = Talks-DB page ID) to the new Recording cache (keyed by
// ConfTalk.ID). Until GetTalksFor is fully replaced by
// LoadTalksFromConfTalks, this lookup matches on the (conf tag, talk
// title) pair which is unique within a conf — both ConfTalks and
// Recordings carry the title.
//
// Returns "" when no matching ConfTalk or no Recording exists.
func FetchYTLinkForTalk(confTag, name string) string {
	if confTag == "" || name == "" {
		return ""
	}
	confTalkCacheMu.RLock()
	var matchID string
	for _, ct := range cacheConfTalks {
		if ct == nil || ct.Conf == nil || ct.Proposal == nil {
			continue
		}
		if ct.Conf.Tag == confTag && ct.Proposal.Title == name {
			matchID = ct.ID
			break
		}
	}
	confTalkCacheMu.RUnlock()
	if matchID == "" {
		return ""
	}
	if rec := FetchRecordingByConfTalk(matchID); rec != nil {
		return rec.YTLink
	}
	return ""
}

func cacheRecordingsWarm() bool {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	return cacheRecordings != nil
}

func cacheConfTalksWarm() bool {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	return cacheConfTalks != nil
}

func InvalidateRecordingsCache() {
	recordingCacheMu.Lock()
	lastRecordingFetch = time.Time{}
	recordingCacheMu.Unlock()
}

// getSiteStats recomputes the about-page aggregate counters from the
// already-warm Confs / ConfTalks caches + one paginated PurchasesDb
// scan. Idempotent and safe to run on a TTL refresh.
func getSiteStats(ctx *config.AppContext) {
	ctx.Infos.Printf("getting site stats...")
	var s SiteStatsValues

	// Past confs from the in-memory cache.
	for _, c := range confs {
		if c != nil && c.HasEnded() {
			s.PastConfs++
		}
	}

	// Accepted talks at past confs — read the ConfTalks cache.
	confTalkCacheMu.RLock()
	for _, ct := range cacheConfTalks {
		if ct == nil || ct.Conf == nil || !ct.Conf.HasEnded() {
			continue
		}
		if ct.Proposal != nil && ct.Proposal.Status == "Accepted" {
			s.PastTalks++
		}
	}
	confTalkCacheMu.RUnlock()

	// Attendees: total rows in PurchasesDb. Slight over-count (test rows,
	// upcoming-conf purchases) but rounded down to the nearest 50 in
	// display, so the imprecision is invisible.
	if db := ctx.Notion.Config.PurchasesDb; db != "" {
		hasMore := true
		nextCursor := ""
		for hasMore {
			pages, next, more, err := ctx.Notion.Client.QueryDatabase(context.Background(),
				db, notion.QueryDatabaseParam{StartCursor: nextCursor})
			if err != nil {
				ctx.Err.Printf("site stats purchases scan: %s", err)
				break
			}
			nextCursor = next
			hasMore = more
			s.Attendees += len(pages)
		}
	}

	siteStatsMu.Lock()
	siteStats = s
	siteStatsMu.Unlock()
	ctx.Infos.Printf("Loaded site stats: confs=%d talks=%d attendees=%d",
		s.PastConfs, s.PastTalks, s.Attendees)
}

// FetchSiteStats returns the cached about-page counters and queues a
// background refresh on TTL expiry.
func FetchSiteStats(ctx *config.AppContext) SiteStatsValues {
	siteStatsMu.RLock()
	s := siteStats
	stale := lastSiteStatsFetch.Before(time.Now().Add(-cacheTTL))
	siteStatsMu.RUnlock()
	if stale {
		lastSiteStatsFetch = time.Now()
		queueRefresh(JobSiteStats)
	}
	return s
}

// CacheStats reports the current row counts in each warm cache. Used by
// the /api/cache-stats debug endpoint to verify bootstrap completed.
func CacheStats() map[string]int {
	proposalCacheMu.RLock()
	nProp := len(proposalByID)
	proposalCacheMu.RUnlock()
	speakerConfCacheMu.RLock()
	nSC := len(speakerConfByID)
	nBySpk := len(speakerConfBySpkID)
	speakerConfCacheMu.RUnlock()
	confTalkCacheMu.RLock()
	nCT := len(confTalkByProposal)
	confTalkCacheMu.RUnlock()
	recordingCacheMu.RLock()
	nRec := len(recordingByConfTalk)
	recordingCacheMu.RUnlock()
	return map[string]int{
		"proposals":              nProp,
		"speakerconfs":           nSC,
		"speakerconfs_by_spkid":  nBySpk,
		"conftalks_by_proposal":  nCT,
		"recordings_by_conftalk": nRec,
		"confs":                  len(confs),
		"speakers":               len(cacheSpeakers),
		"orgs":                   len(orgs),
		"notion_calls_total":     int(types.NotionCallCount()),
	}
}

func getTalks(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting talks...")
	talks, err = listTalks(ctx, cacheSpeakers)

	if err != nil {
		ctx.Err.Printf("error fetching talks %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d talks!", len(talks))
		writeCache("talks", talks)
		for _, cb := range onTalksRefresh {
			cb(ctx, talks)
		}
	}
}

/* This may return nil */
func FetchTalksCached(ctx *config.AppContext) ([]*types.Talk, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if talks == nil || lastTalksFetch.Before(deadline) {
		/* Set last fetch to now even if fails */
		lastTalksFetch = time.Now()
		queueRefresh(JobTalks)
	}

	return talks, nil
}

// InvalidateTalksCache forces the next FetchTalksCached call to
// queue a refresh, even within the TTL window. The talks slice is
// derived from ConfTalks via listTalks, so callers that mutate a
// ConfTalk row (e.g. clipart upload) need to invalidate THIS cache
// too — InvalidateConfTalksCache only busts the lower-level
// confTalk index. Without this, GET /admin/cliparts after an upload
// returns the stale derived slice and the new clipart doesn't show.
func InvalidateTalksCache() {
	lastTalksFetch = time.Time{}
}

// patchTalksStatusForProposal eagerly updates Talk.Status for every
// cached Talk whose underlying ConfTalk belongs to the given
// proposal. Talk is a denormalized snapshot — talk.Status is copied
// from proposal.Status at listTalks time — so a proposal-status flip
// (Accepted → Scheduled via Send Cal Invites) leaves the derived
// talks slice stale until the next refresh tick. Without this,
// Conf.HasAgenda / agenda visibility lag behind by a refresh
// interval.
func patchTalksStatusForProposal(proposalID, status string) {
	confTalkCacheMu.RLock()
	ctIDs := make(map[string]bool)
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.Proposal != nil && ct.Proposal.ID == proposalID {
			ctIDs[ct.ID] = true
		}
	}
	confTalkCacheMu.RUnlock()
	if len(ctIDs) == 0 {
		return
	}
	for _, t := range talks {
		if t != nil && ctIDs[t.ID] {
			t.Status = status
		}
	}
}

func ListConfTicketsNotion(n *types.Notion) ([]*types.ConfTicket, error) {
	var confTix []*types.ConfTicket

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsTixDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			tix := parseConfTicket(page.ID, page.Properties)
			confTix = append(confTix, tix)
		}
	}

	return confTix, nil
}

/* Grabs the conferences + their tickets buckets */
func ListConferencesNotion(n *types.Notion) ([]*types.Conf, error) {
	var confs []*types.Conf

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			conf := parseConf(page.ID, page.Properties)
			confs = append(confs, conf)
		}
	}

	confTix, err := ListConfTicketsNotion(n)
	if err != nil {
		return nil, err
	}

	/* Add conf tixs to confs */
	for _, tix := range confTix {
		for _, conf := range confs {
			if conf.Ref == tix.ConfRef {
				conf.Tickets = append(conf.Tickets, tix)
				break
			}
		}
	}

	return confs, nil
}

func ListConferencesOnlyNotion(n *types.Notion) ([]*types.Conf, error) {
	var confs []*types.Conf

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			conf := parseConf(page.ID, page.Properties)
			confs = append(confs, conf)
		}
	}

	return confs, nil
}

// listTalks loads every Talk-shaped row across all confs, sourced from the
// ConfTalk → Proposal → SpeakerConf[] → Speaker[] chain. Talk.ID is the
// ConfTalk page ID (the new canonical talk identifier).
//
// The `speakers` param is unused — kept on the signature to match the cache
// job runner; SpeakerConf joins handle speaker resolution internally.
func listTalks(ctx *config.AppContext, _ []*types.Speaker) ([]*types.Talk, error) {
	talks, err := LoadTalksFromConfTalks(ctx, "")
	if err != nil {
		return nil, err
	}
	ctx.Infos.Printf("listTalks: loaded %d talks from conf talks", len(talks))
	return talks, nil
}

func TalkUpdateCalNotif(n *types.Notion, talkID string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), talkID,
		map[string]*notion.PropertyValue{
			"CalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	if err != nil {
		return err
	}
	// Patch the warm caches in place so the next page-render
	// sees the new CalNotif without waiting on a refresh tick.
	// confTalkByProposal and cacheConfTalks share pointers, so a
	// single mutation reaches both readers.
	confTalkCacheMu.Lock()
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID == talkID {
			ct.CalNotif = calnotif
			break
		}
	}
	confTalkCacheMu.Unlock()
	return nil
}

func ShiftUpdateCalNotif(n *types.Notion, shiftID string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), shiftID,
		map[string]*notion.PropertyValue{
			"CalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	if err != nil {
		return err
	}
	// Mirror TalkUpdateCalNotif: patch the warm shift cache so a
	// subsequent ListWorkShifts read in the same process sees
	// the new CalNotif without waiting on a refresh tick. The
	// `shifts` slice is unprotected (matches the existing pattern
	// in invalidateShiftCache + the FetchShiftsCached refresh
	// path); a parallel-write race here is no worse than what
	// already exists upstream.
	for _, s := range shifts {
		if s != nil && s.Ref == shiftID {
			s.CalNotif = calnotif
			break
		}
	}
	return nil
}

// ConfUpdateOrientCalNotif stamps the orientation-invite state
// triple ("UID:Sequence:Hashbytes") on a conf row's
// OrientCalNotif rich_text column. Mirrors TalkUpdateCalNotif /
// ShiftUpdateCalNotif's in-place cache patch so the next render
// reads the fresh value without waiting on a cache refresh tick.
func ConfUpdateOrientCalNotif(n *types.Notion, confRef string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confRef,
		map[string]*notion.PropertyValue{
			"OrientCalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	if err != nil {
		return err
	}
	// Patch the warm conf cache. confs[] holds pointers; the
	// same pointer is what FetchConfsCached returns, so a
	// single mutation reaches every reader.
	for _, c := range confs {
		if c != nil && c.Ref == confRef {
			c.OrientCalNotif = calnotif
			break
		}
	}
	return nil
}

func ListSpeakersNotion(n *types.Notion) ([]*types.Speaker, error) {
	var speakers []*types.Speaker

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakersDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			speaker := parseSpeaker(page.ID, page.Properties)
			speakers = append(speakers, speaker)
		}
	}

	return speakers, nil
}

func GetTalksFor(ctx *config.AppContext, event string) ([]*types.Talk, error) {
	talks, err := FetchTalksCached(ctx)
	if err != nil {
		return nil, err
	}
	var filtered []*types.Talk
	for _, talk := range talks {
		if talk.Event == event {
			filtered = append(filtered, talk)
		}
	}
	return filtered, nil
}

func GetTalk(ctx *config.AppContext, talkID string) (*types.Talk, error) {
	talks, err := FetchTalksCached(ctx)
	if err != nil {
		return nil, err
	}
	for _, talk := range talks {
		if talk.ID == talkID {
			return talk, nil
		}
	}
	return nil, fmt.Errorf("Talk %s not found", talkID)
}

func ListHotelsNotion(n *types.Notion) ([]*types.Hotel, error) {
	var hotels []*types.Hotel

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.HotelsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			hotel := parseHotel(page.ID, page.Properties)
			hotels = append(hotels, hotel)
		}
	}

	return hotels, nil
}

func ListJobsNotion(n *types.Notion) ([]*types.JobType, error) {
	var jobs []*types.JobType

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.JobTypeDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			job := parseJobType(page.ID, page.Properties)
			jobs = append(jobs, job)
		}
	}

	return jobs, nil
}

func ListWorkShiftsNotion(ctx *config.AppContext) ([]*types.WorkShift, error) {
	var shiftList []*types.WorkShift
	n := ctx.Notion

	jobtypes, err := FetchJobsCached(ctx)
	if err != nil {
		return nil, err
	}

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ShiftDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			shift := parseWorkShift(ctx, page.ID, page.Properties, jobtypes)
			shiftList = append(shiftList, shift)
		}
	}

	return shiftList, nil
}

// invalidateShiftCache forces the next FetchShiftsCached call to refetch.
func invalidateShiftCache() {
	shifts = nil
}

// buildShiftPropertiesJSON constructs the Notion `properties` payload for a
// shift page. We build this by hand (rather than using go-notion's
// PropertyValue/CreatePage) because the library marks every value field as
// json:omitempty, which silently drops zero-value Numbers (e.g. Priority=0)
// and produces an invalid Notion request.
func buildShiftPropertiesJSON(name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) map[string]interface{} {
	props := map[string]interface{}{
		"Name": map[string]interface{}{
			"title": []map[string]interface{}{
				{"text": map[string]interface{}{"content": name}},
			},
		},
		"MaxVols":  map[string]interface{}{"number": maxVols},
		"Priority": map[string]interface{}{"number": priority},
	}

	if !start.IsZero() {
		date := map[string]interface{}{
			"start": start.Format(time.RFC3339),
		}
		if !end.IsZero() {
			date["end"] = end.Format(time.RFC3339)
		}
		props["ShiftTime"] = map[string]interface{}{"date": date}
	}

	if jobType != nil {
		props["TypeRef"] = map[string]interface{}{
			"relation": []map[string]interface{}{{"id": jobType.Ref}},
		}
	}

	return props
}

// notionPagePost sends a JSON request directly to Notion's pages API. method
// is "POST" for create, "PATCH" for update. urlPath is appended to the v1/pages
// base. Returns the parsed JSON response or an error.
func notionPagePost(token, method, urlPath string, body map[string]interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, "https://api.notion.com/v1/pages"+urlPath, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion API error: %v", errResp)
	}
	return nil
}

// CreateShift creates a new WorkShift page in the Notion ShiftDb. ShiftTime
// must have a non-nil End. Bypasses go-notion's CreatePage to avoid the
// omitempty zero-value bug for Number properties.
func CreateShift(ctx *config.AppContext, conf *types.Conf, jobType *types.JobType, name string, start, end time.Time, maxVols, priority uint) error {
	if conf == nil || conf.Ref == "" {
		return fmt.Errorf("CreateShift: conf is nil or has empty ref")
	}

	props := buildShiftPropertiesJSON(name, jobType, start, end, maxVols, priority)
	props["ConfRef"] = map[string]interface{}{
		"relation": []map[string]interface{}{{"id": conf.Ref}},
	}

	body := map[string]interface{}{
		"parent": map[string]interface{}{
			"database_id": ctx.Notion.Config.ShiftDb,
		},
		"properties": props,
	}

	err := notionPagePost(ctx.Notion.Config.Token, "POST", "", body)
	if err != nil {
		return err
	}

	invalidateShiftCache()
	return nil
}

// UpdateShift updates a WorkShift's mutable fields. Pass nil for jobType to
// skip updating the type. Pass a zero start to skip updating the time. Uses
// direct HTTP PATCH to avoid go-notion's omitempty issues.
// UpdateShiftTimes patches only the ShiftTime property on a shift,
// leaving Name / JobType / MaxVols / Priority / Assignees untouched.
// Used by the gantt drag/resize UI on /volcoord/shifts so a coord
// can move + reshape a shift in place without re-sending fields
// that haven't changed (which would clobber concurrent edits).
//
// After the PATCH succeeds we *synchronously* reload the shifts
// cache rather than calling invalidateShiftCache (which nils the
// slice and queues an async refresh). The drag UI reloads the page
// immediately after the POST returns; an async refresh would race
// the next GET and serve a nil slice — making every shift on the
// page momentarily disappear.
func UpdateShiftTimes(ctx *config.AppContext, shiftRef string, start, end time.Time) error {
	if start.IsZero() {
		return fmt.Errorf("UpdateShiftTimes: start required")
	}
	date := map[string]interface{}{
		"start": start.Format(time.RFC3339),
	}
	if !end.IsZero() {
		date["end"] = end.Format(time.RFC3339)
	}
	body := map[string]interface{}{
		"properties": map[string]interface{}{
			"ShiftTime": map[string]interface{}{"date": date},
		},
	}
	if err := notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+shiftRef, body); err != nil {
		return err
	}
	if fresh, err := ListWorkShifts(ctx); err == nil {
		shifts = fresh
		lastShiftFetch = time.Now()
		writeCache("shifts", shifts)
	} else {
		ctx.Err.Printf("UpdateShiftTimes: cache reload (continuing): %s", err)
	}
	return nil
}

func UpdateShift(ctx *config.AppContext, shiftRef, name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) error {
	props := buildShiftPropertiesJSON(name, jobType, start, end, maxVols, priority)

	body := map[string]interface{}{
		"properties": props,
	}

	err := notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+shiftRef, body)
	if err != nil {
		return err
	}

	invalidateShiftCache()
	return nil
}

func AssignVolunteerToShift(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

	// First get the current shift to get existing assignees
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return err
	}

	var shift *types.WorkShift
	for _, s := range allShifts {
		if s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil {
		return fmt.Errorf("shift %s not found", shiftRef)
	}

	// Check if already assigned
	for _, assignee := range shift.AssigneesRef {
		if assignee == volRef {
			return nil // Already assigned
		}
	}

	// Build new assignees list
	newAssignees := make([]*notion.ObjectReference, len(shift.AssigneesRef)+1)
	for i, ref := range shift.AssigneesRef {
		newAssignees[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     ref,
		}
	}
	newAssignees[len(shift.AssigneesRef)] = &notion.ObjectReference{
		Object: notion.ObjectPage,
		ID:     volRef,
	}

	_, err = n.Client.UpdatePageProperties(context.Background(), shiftRef,
		map[string]*notion.PropertyValue{
			"Assignees": {
				Type:     notion.PropertyRelation,
				Relation: newAssignees,
			},
		})

	if err == nil {
		// Update local cache
		shift.AssigneesRef = append(shift.AssigneesRef, volRef)
	}

	return err
}

func RemoveVolunteerFromShift(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

	// First get the current shift to get existing assignees
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return err
	}

	var shift *types.WorkShift
	for _, s := range allShifts {
		if s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil {
		return fmt.Errorf("shift %s not found", shiftRef)
	}

	// Build new assignees list without the volunteer
	newAssignees := make([]*notion.ObjectReference, 0)
	newAssigneesRef := make([]string, 0)
	for _, ref := range shift.AssigneesRef {
		if ref != volRef {
			newAssignees = append(newAssignees, &notion.ObjectReference{
				Object: notion.ObjectPage,
				ID:     ref,
			})
			newAssigneesRef = append(newAssigneesRef, ref)
		}
	}

	// If relation is empty, use direct HTTP request since go-notion's
	// omitempty causes empty slices to be omitted from JSON
	if len(newAssignees) == 0 {
		err = clearRelationProperty(n.Config.Token, shiftRef, "Assignees")
	} else {
		_, err = n.Client.UpdatePageProperties(context.Background(), shiftRef,
			map[string]*notion.PropertyValue{
				"Assignees": {
					Type:     notion.PropertyRelation,
					Relation: newAssignees,
				},
			})
	}

	if err == nil {
		// Update local cache
		shift.AssigneesRef = newAssigneesRef
	}

	return err
}

// clearRelationProperty makes a direct HTTP request to Notion API to clear a relation
func clearRelationProperty(token, pageID, propertyName string) error {
	payload := map[string]interface{}{
		"properties": map[string]interface{}{
			propertyName: map[string]interface{}{
				"relation": []interface{}{},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", "https://api.notion.com/v1/pages/"+pageID, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion API error: %v", errResp)
	}

	return nil
}

func UpdateVolunteerStatus(ctx *config.AppContext, volRef, status string) error {
	n := ctx.Notion

	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"Status": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: status,
				},
			},
		})

	return err
}

func UpdateVolunteerAvailability(ctx *config.AppContext, volRef string, days []string) error {
	n := ctx.Notion

	availability := make([]*notion.SelectOption, len(days))
	for i, d := range days {
		availability[i] = &notion.SelectOption{Name: d}
	}

	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"Availability": {
				Type:        notion.PropertyMultiSelect,
				MultiSelect: &availability,
			},
		})

	return err
}

func UpdateVolunteerWorkPrefs(ctx *config.AppContext, volRef string, workYesRefs, workNoRefs []string) error {
	n := ctx.Notion

	// WorkYes
	if len(workYesRefs) == 0 {
		err := clearRelationProperty(n.Config.Token, volRef, "WorkYes")
		if err != nil {
			return err
		}
	} else {
		yesRel := make([]*notion.ObjectReference, len(workYesRefs))
		for i, r := range workYesRefs {
			yesRel[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: r}
		}
		_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
			map[string]*notion.PropertyValue{
				"WorkYes": {
					Type:     notion.PropertyRelation,
					Relation: yesRel,
				},
			})
		if err != nil {
			return err
		}
	}

	// WorkNo
	if len(workNoRefs) == 0 {
		return clearRelationProperty(n.Config.Token, volRef, "WorkNo")
	}

	noRel := make([]*notion.ObjectReference, len(workNoRefs))
	for i, r := range workNoRefs {
		noRel[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: r}
	}
	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"WorkNo": {
				Type:     notion.PropertyRelation,
				Relation: noRel,
			},
		})

	return err
}

func ListDiscountsNotion(n *types.Notion) ([]*types.DiscountCode, error) {
	var discounts []*types.DiscountCode

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.DiscountsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			discount := parseDiscount(page.ID, page.Properties)
			discounts = append(discounts, discount)
		}
	}

	return discounts, nil
}

func CheckInNotion(n *types.Notion, ticket string) (string, bool, error) {
	/* Make sure that the ticket is in the Purchases table and
	is *NOT* already checked in */
	pages, _, _, _ := n.Client.QueryDatabase(context.Background(), n.Config.PurchasesDb,
		notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "RefID",
				Text: &notion.TextFilterCondition{
					Equals: ticket,
				},
			},
		})

	if len(pages) == 0 {
		return "", true, fmt.Errorf("Ticket not found")
	}

	page := pages[0]

	revoked := page.Properties["Revoked"].Checkbox
	if revoked != nil && *revoked {
		return "", true, fmt.Errorf("Ticket was revoked")
	}

	if len(page.Properties["Checked In"].RichText) == 0 {
		/* Update to checked in at time.now() */
		now := time.Now()
		_, err := n.Client.UpdatePageProperties(context.Background(), page.ID,
			map[string]*notion.PropertyValue{
				"Checked In": notion.NewRichTextPropertyValue(
					[]*notion.RichText{
						{Type: notion.RichTextText,
							Text: &notion.Text{Content: now.Format(time.RFC3339)}},
					}...),
			})

		/* I need to know what role this is, so I can flash it! */
		var ticket_type string
		if page.Properties["Type"].Select != nil {
			ticket_type = page.Properties["Type"].Select.Name
		}
		return ticket_type, err == nil, err
	}

	return "", true, fmt.Errorf("Already checked in")
}

func SoldTixCountNotion(n *types.Notion, confRef string) (uint, error) {
	var regisCount uint

	hasMore := true
	nextCursor := ""
	db := n.Config.PurchasesDb
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db,
			notion.QueryDatabaseParam{
				Filter: &notion.Filter{
					Property: "conf",
					Relation: &notion.RelationFilterCondition{
						Contains: confRef,
					},
				},
				StartCursor: nextCursor,
			})
		if err != nil {
			return 0, err
		}

		regisCount += uint(len(pages))
	}

	return regisCount, nil
}

func FetchRegistrationsNotion(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	var regis []*types.Registration
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.PurchasesDb

	var filter *notion.Filter
	if confRef != "" {
		filter = &notion.Filter{
			Property: "conf",
			Relation: &notion.RelationFilterCondition{
				Contains: confRef,
			},
		}
	}
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
			Filter:      filter,
		})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			r := parseRegistration(page.Properties)
			regis = append(regis, r)
		}
	}

	return regis, nil
}

// ListRegistrationsByEmail returns every PurchasesDb row for this email.
// Used by the dashboard to render "your tickets" and the apply-form
// "returning attendee" check.
func ListRegistrationsByEmailNotion(ctx *config.AppContext, email string) ([]*types.Registration, error) {
	if email == "" {
		return nil, nil
	}
	n := ctx.Notion
	var out []*types.Registration
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.PurchasesDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
				Filter: &notion.Filter{
					Property: "Email",
					Text:     &notion.TextFilterCondition{Equals: email},
				},
			})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseRegistration(page.Properties))
		}
	}
	return out, nil
}

func LookupTicketPages(n *types.Notion, lookupID string) ([]*notion.Page, error) {
	return TicketPages(n, "Lookup ID", lookupID)
}

func RefTicketPages(n *types.Notion, refid string) ([]*notion.Page, error) {
	return TicketPages(n, "RefID", refid)
}

func TicketPages(n *types.Notion, field, uniqID string) ([]*notion.Page, error) {
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.PurchasesDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: field,
				Text: &notion.TextFilterCondition{
					Equals: uniqID,
				},
			},
		})

	return pages, err
}

func ToggleTicketBlock(n *types.Notion, pageID string, block bool) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), pageID,
		map[string]*notion.PropertyValue{
			"Revoked": {
				Type:     notion.PropertyCheckbox,
				Checkbox: &block,
			},
		})
	return err
}

func RevokeTicket(n *types.Notion, lookupID string) error {
	pages, err := LookupTicketPages(n, lookupID)

	for _, page := range pages {
		ToggleTicketBlock(n, page.ID, true)
	}
	return err
}

func AddTickets(n *types.Notion, entry *types.Entry, src string) error {
	parent := notion.NewDatabaseParent(n.Config.PurchasesDb)

	for i, item := range entry.Items {
		uniqID := types.UniqueID(entry.Email, entry.ID, int32(i))

		/* Check for existing ticket already */
		pages, err := RefTicketPages(n, uniqID)
		if err != nil {
			return err
		}
		if len(pages) > 0 {
			/* Set each page to unrevoked */
			for _, page := range pages {
				ToggleTicketBlock(n, page.ID, false)
			}
			continue
		}

		vals := map[string]*notion.PropertyValue{
			"RefID": notion.NewTitlePropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: uniqID}},
				}...),
			"Timestamp": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: entry.Created.Format(time.RFC3339)},
					}}...),
			"Platform": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: src,
				},
			},
			"conf": notion.NewRelationPropertyValue(
				[]*notion.ObjectReference{{ID: entry.ConfRef}}...,
			),
			"Type": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: item.Type,
				},
			},
			// Amount Paid is built below — go-notion's
			// `Number float64 json:omitempty` would elide a
			// zero-value float from the PATCH body, leaving
			// the property with type=number but no `number`
			// sub-field, which Notion 400s on. For free comp
			// tickets we just leave the column unset (Notion
			// treats it as null).
			"Currency": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: entry.Currency,
				},
			},
			"Email": {
				Type:  notion.PropertyEmail,
				Email: entry.Email,
			},
			"Item Bought": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: item.Desc}},
				}...),
			"Lookup ID": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: entry.ID}},
				}...),
		}

		if item.Total > 0 {
			vals["Amount Paid"] = &notion.PropertyValue{
				Type:   notion.PropertyNumber,
				Number: float64(item.Total) / 100,
			}
		}

		if entry.DiscountRef != "" {
			vals["discount"] = notion.NewRelationPropertyValue(
				[]*notion.ObjectReference{{ID: entry.DiscountRef}}...,
			)
		}
		_, err = n.Client.CreatePage(context.Background(), parent, vals)
		if err != nil {
			return err
		}
	}

	return nil
}

func RegisterVolunteer(n *types.Notion, vol *types.Volunteer) error {
	normalizeVolunteerInput(vol)
	parent := notion.NewDatabaseParent(n.Config.VolunteerDb)

	// multiselect
	availability := make([]*notion.SelectOption, len(vol.Availability))
	for i, av := range vol.Availability {
		availability[i] = &notion.SelectOption{
			Name: av,
		}
	}

	// relation
	workYes := make([]*notion.ObjectReference, len(vol.WorkYes))
	for i, wy := range vol.WorkYes {
		workYes[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     wy.Ref,
		}
	}
	workNo := make([]*notion.ObjectReference, len(vol.WorkNo))
	for i, wn := range vol.WorkNo {
		workNo[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     wn.Ref,
		}
	}
	otherEvents := make([]*notion.ObjectReference, len(vol.OtherEvents))
	for i, oe := range vol.OtherEvents {
		otherEvents[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     oe.Ref,
		}
	}

	vals := map[string]*notion.PropertyValue{
		"Name": notion.NewTitlePropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Name}},
			}...),
		"Email": notion.NewEmailPropertyValue(vol.Email),
		"Phone": notion.NewPhoneNumberPropertyValue(vol.Phone),
		"Availability": &notion.PropertyValue{
			Type:        notion.PropertyMultiSelect,
			MultiSelect: &availability,
		},
		"Signal": notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Signal}},
			}...),
		"ContactAt": notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.ContactAt}},
			}...),
		"DiscoveredVia": notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.DiscoveredVia}},
			}...),
		"ScheduleFor": notion.NewRelationPropertyValue(
			[]*notion.ObjectReference{{ID: vol.ScheduleFor[0].Ref}}...,
		),
		"FirstEvent": {
			Type:     notion.PropertyCheckbox,
			Checkbox: &vol.FirstEvent,
		},
		"Hometown": notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Hometown}},
			}...),
		"Shirt": {
			Type: notion.PropertySelect,
			Select: &notion.SelectOption{
				Name: vol.Shirt,
			},
		},
		"Status": {
			Type: notion.PropertySelect,
			Select: &notion.SelectOption{
				Name: "Applied",
			},
		},
	}

	if len(vol.WorkYes) != 0 {
		vals["WorkYes"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: workYes,
		}
	}

	if len(vol.WorkNo) != 0 {
		vals["WorkNo"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: workNo,
		}
	}

	if len(vol.OtherEvents) != 0 {
		vals["OtherEvents"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: otherEvents,
		}
	}

	if vol.Twitter.Handle != "" {
		vals["Twitter"] = notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Twitter.Handle}},
			}...)
	}

	if vol.Nostr != "" {
		vals["npub"] = notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Nostr}},
			}...)
	}

	if vol.Comments != "" {
		vals["Comments"] = notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Comments}},
			}...)
	}

	_, err := n.Client.CreatePage(context.Background(), parent, vals)

	return err
}

func normalizeVolunteerInput(vol *types.Volunteer) {
	if vol == nil {
		return
	}
	vol.Name = strings.TrimSpace(vol.Name)
	vol.Email = strings.TrimSpace(vol.Email)
	vol.Phone = strings.TrimSpace(vol.Phone)
	vol.Signal = strings.TrimSpace(vol.Signal)
	vol.ContactAt = strings.TrimSpace(vol.ContactAt)
	vol.Comments = strings.TrimSpace(vol.Comments)
	vol.DiscoveredVia = strings.TrimSpace(vol.DiscoveredVia)
	vol.Hometown = strings.TrimSpace(vol.Hometown)
	vol.Twitter = types.ParseTwitter(vol.Twitter.Handle)
	vol.Nostr = strings.TrimSpace(vol.Nostr)
	vol.Shirt = strings.TrimSpace(vol.Shirt)
}

// ListConfInfos fetches every row in ConfInfoDb, optionally filtered to a
// single conf by Tag. Each row is resolved against the cached confs
// slice so the returned *Times carry the conf's timezone.
//
// The Conf column stores a tag string (not a relation), so the confTag
// filter is applied client-side after fetch — the DB is small enough
// that a full scan is cheaper than maintaining two filter codepaths
// (select vs rich_text).
//
// Rows whose tag can't be matched in the confs cache come back with
// empty time fields rather than dropping out — useful for admin tools
// that want to surface orphan rows.
func ListConfInfos(ctx *config.AppContext, confTag string) ([]*types.ConfInfo, error) {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}
	confByTag := make(map[string]*types.Conf, len(confs))
	for _, c := range confs {
		if c != nil && c.Tag != "" {
			confByTag[c.Tag] = c
		}
	}

	n := ctx.Notion
	db := ctx.Env.Notion.ConfInfoDb
	if db == "" {
		return nil, nil
	}

	var out []*types.ConfInfo
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
		})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			ci := parseConfInfo(page.ID, page.Properties, confByTag)
			if confTag != "" && ci.ConfTag != confTag {
				continue
			}
			out = append(out, ci)
		}
	}
	return out, nil
}

// GetConfInfoMap returns a Tag → []*ConfInfo map, sorted by Day within
// each conf. Convenient for templates that want "the schedule strip for
// this conf" without sifting by tag manually.
func GetConfInfoMap(ctx *config.AppContext) (map[string][]*types.ConfInfo, error) {
	infos, err := ListConfInfos(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]*types.ConfInfo)
	for _, ci := range infos {
		if ci.ConfTag == "" {
			continue
		}
		out[ci.ConfTag] = append(out[ci.ConfTag], ci)
	}
	for tag := range out {
		sort.Slice(out[tag], func(i, j int) bool {
			return out[tag][i].Day < out[tag][j].Day
		})
	}
	return out, nil
}

func GetVolInfosNotion(ctx *config.AppContext, confRef string) ([]*types.VolInfo, error) {
	var vis []*types.VolInfo
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolInfoDb

	var filter *notion.Filter
	if confRef != "" {
		filter = &notion.Filter{
			Property: "conf",
			Relation: &notion.RelationFilterCondition{
				Contains: confRef,
			},
		}
	}
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
			Filter:      filter,
		})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			vi := parseVolInfo(page.ID, page.Properties)
			vis = append(vis, vi)
		}
	}

	return vis, nil
}

func ListVolunteerAppsNotion(ctx *config.AppContext, email string) ([]*types.Volunteer, error) {
	var vols []*types.Volunteer
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolunteerDb

	var filter *notion.Filter
	if email != "" {
		filter = &notion.Filter{
			Property: "Email",
			Text: &notion.TextFilterCondition{
				Equals: email,
			},
		}
	}
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
			Filter:      filter,
		})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			v := parseVolunteer(ctx, page.ID, page.Properties)
			vols = append(vols, v)
		}
	}

	return vols, nil
}

// FetchVolunteer retrieves a single volunteer page directly by ID. This is a
// strongly-consistent read (unlike QueryDatabase, which uses an
// eventually-consistent index), so it should be used after writes when the
// caller needs to render the just-updated state.
func FetchVolunteerNotion(ctx *config.AppContext, volRef string) (*types.Volunteer, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), volRef)
	if err != nil {
		return nil, err
	}
	return parseVolunteer(ctx, page.ID, page.Properties), nil
}

func ListVolunteersForConfNotion(ctx *config.AppContext, confRef string) ([]*types.Volunteer, error) {
	var vols []*types.Volunteer
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolunteerDb

	filter := &notion.Filter{
		Property: "ScheduleFor",
		Relation: &notion.RelationFilterCondition{
			Contains: confRef,
		},
	}
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
			Filter:      filter,
		})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			v := parseVolunteer(ctx, page.ID, page.Properties)
			vols = append(vols, v)
		}
	}

	return vols, nil
}

func UploadFile(n *types.Notion, contentType, filename string, data []byte) (string, error) {
	upload, err := n.Client.CreateFileUpload(context.Background())
	if err != nil {
		return "", err
	}

	upload.Filename = filename
	upload.ContentType = contentType
	result, err := n.Client.UploadFile(context.Background(), upload, data)
	if err != nil {
		return "", err
	}

	if result.Status != notion.FileStatusUploaded {
		return "", fmt.Errorf("Unable to upload file. %v", result)
	}

	return result.ID, nil
}
