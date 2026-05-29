package getters

import (
	"sync"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
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

	siteStats          SiteStatsValues
	lastSiteStatsFetch time.Time
	siteStatsMu        sync.RWMutex
)

type JobType int

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
// buffer; the cache stays stale for one more TTL cycle, but no handler freezes.
var taskChan chan JobType = make(chan JobType, 32)

// queueRefresh attempts to enqueue a cache-refresh job without blocking.
// Returns true if queued, false if the buffer was full.
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
// from the on-disk cache. The disk cache is a startup convenience only; once
// the process has booted, every subsequent WaitFetch goes straight to live
// data. This makes /conf-reload always re-fetch live rows.
var diskCacheBootstrapped = false

func WaitFetch(ctx *config.AppContext) {
	cacheTTL = time.Duration(ctx.Env.CacheTTLSec) * time.Second

	if !ctx.InProduction {
		EnableDiskCache()
	}

	loadedFromDisk := false
	if !diskCacheBootstrapped && diskCacheEnabled && loadFromCache() {
		ctx.Infos.Printf("Loaded legacy data from disk cache!")
		loadedFromDisk = true
	}
	diskCacheBootstrapped = true

	if !loadedFromDisk {
		ctx.Infos.Printf("Fetching legacy types...")
		var wg sync.WaitGroup
		wg.Add(6)
		go func() { defer wg.Done(); runJob(ctx, JobConfs); lastConfsFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobSpeakers); lastSpeakerFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobDiscounts); lastDiscountFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobHotels); lastHotelFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobJobs); lastJobTypeFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobOrgs); lastOrgFetch = time.Now() }()
		wg.Wait()

		wg.Add(2)
		go func() { defer wg.Done(); runJob(ctx, JobTalks); lastTalksFetch = time.Now() }()
		go func() { defer wg.Done(); runJob(ctx, JobShifts); lastShiftFetch = time.Now() }()
		wg.Wait()
	}

	ctx.Infos.Printf("Fetching new-DB types...")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); runJob(ctx, JobProposals); lastProposalFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobRecordings); lastRecordingFetch = time.Now() }()
	wg.Wait()

	wg.Add(2)
	go func() { defer wg.Done(); runJob(ctx, JobConfTalks); lastConfTalkFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobSpeakerConfs); lastSpeakerConfFetch = time.Now() }()
	wg.Wait()

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

// InvalidateProposalsCache forces the next FetchProposalsCached /
// FetchProposalByID to refresh. Call after a write that changed proposal data.
func InvalidateProposalsCache() {
	proposalCacheMu.Lock()
	lastProposalFetch = time.Time{}
	proposalCacheMu.Unlock()
}

// CacheSpeakerInsert appends a Speaker to the in-memory speakers cache. Used
// after CreateSpeaker so the just-written row is findable without waiting for
// the periodic refresh tick.
func CacheSpeakerInsert(s *types.Speaker) {
	if s == nil {
		return
	}
	cacheSpeakers = append(cacheSpeakers, s)
}

// CacheSpeakerByID looks up a Speaker pointer in the warm cache for callers
// that need to attach it to a fresh SpeakerConf before inserting.
func CacheSpeakerByID(id string) *types.Speaker {
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == id {
			return s
		}
	}
	return nil
}

// CacheStats reports the current row counts in each warm cache. Used by the
// /api/cache-stats debug endpoint to verify bootstrap completed.
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
