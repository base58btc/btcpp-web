package getters

import (
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// getSpeakerConfs refreshes the SpeakerConf cache. Depends on Speakers and
// Proposals being already cached so backend readers can resolve relations.
func getSpeakerConfs(ctx *config.AppContext) {
	ctx.Infos.Printf("getting speaker confs...")

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

func ListSpeakerConfs(ctx *config.AppContext, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerConf, error) {
	if UsePostgresBackend(ctx) {
		return listSpeakerConfsPostgres(ctx, speakerMap, proposalMap)
	}
	return ListSpeakerConfsNotion(ctx, speakerMap, proposalMap)
}

// FetchSpeakerConfsForSpeaker returns the cached SpeakerConf rows linked to
// speakerID. Triggers an async refresh on TTL expiry but returns stale data
// immediately rather than blocking.
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

// FetchSpeakerConfByID returns the cached SpeakerConf for the given ID, or nil
// if not in cache.
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

// CacheSpeakerConfInsert adds a fresh SpeakerConf to the in-memory caches
// (slice + by-ID + by-speakerID indexes) so the next
// FetchSpeakerConfsForSpeaker call sees it. Used after a successful
// UpsertSpeakerConf create.
//
// Idempotent on ID: calling twice with the same row replaces the previous entry
// rather than duplicating it.
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
		list := speakerConfBySpkID[sc.Speaker.ID]
		for _, e := range list {
			if e != nil && e.ID == sc.ID {
				return
			}
		}
		speakerConfBySpkID[sc.Speaker.ID] = append(list, sc)
	}
}

// GetSpeakerConfsByEmail looks up Speaker(s) by email and returns every
// SpeakerConf row linked to those speakers, fully resolved.
func GetSpeakerConfsByEmail(ctx *config.AppContext, email string) ([]*types.Speaker, []*types.SpeakerConf, error) {
	if email == "" {
		return nil, nil, nil
	}
	if UsePostgresBackend(ctx) {
		return getSpeakerConfsByEmailPostgres(ctx, email)
	}
	speakers, err := GetSpeakersByEmail(ctx, email)
	if err != nil {
		return nil, nil, fmt.Errorf("speakers by email: %w", err)
	}
	if len(speakers) == 0 {
		return nil, nil, nil
	}

	var allConfs []*types.SpeakerConf
	for _, sp := range speakers {
		allConfs = append(allConfs, FetchSpeakerConfsForSpeaker(ctx, sp.ID)...)
	}
	return speakers, allConfs, nil
}

// FetchSpeakerConfWithSpeaker reads a SpeakerConf by ID with its speaker
// relation resolved. Cache-first; backend-specific fallback only runs on misses.
func FetchSpeakerConfWithSpeaker(ctx *config.AppContext, speakerConfID string) (*types.SpeakerConf, error) {
	if sc := FetchSpeakerConfByID(speakerConfID); sc != nil {
		return sc, nil
	}
	if UsePostgresBackend(ctx) {
		return fetchSpeakerConfWithSpeakerPostgres(ctx, speakerConfID)
	}
	return fetchSpeakerConfWithSpeakerNotion(ctx, speakerConfID)
}
