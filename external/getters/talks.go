package getters

import (
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

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

// InvalidateTalksCache forces the next FetchTalksCached call to queue a
// refresh, even within the TTL window. The talks slice is derived from
// ConfTalks via listTalks, so callers that mutate a ConfTalk row need to
// invalidate this cache too.
func InvalidateTalksCache() {
	lastTalksFetch = time.Time{}
}

// patchTalksStatusForProposal eagerly updates Talk.Status for every cached
// Talk whose underlying ConfTalk belongs to the given proposal. Talk is a
// denormalized snapshot, so proposal-status flips can otherwise leave the
// derived talks slice stale until the next refresh tick.
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

// listTalks loads every Talk-shaped row across all confs, sourced from the
// ConfTalk -> Proposal -> SpeakerConf[] -> Speaker[] chain. Talk.ID is the
// ConfTalk page ID in Notion and the conf_talks.id value in Postgres.
//
// The speakers param is unused. It is kept on the signature to match the cache
// job runner; SpeakerConf joins handle speaker resolution internally.
func listTalks(ctx *config.AppContext, _ []*types.Speaker) ([]*types.Talk, error) {
	talks, err := LoadTalksFromConfTalks(ctx, "")
	if err != nil {
		return nil, err
	}
	ctx.Infos.Printf("listTalks: loaded %d talks from conf talks", len(talks))
	return talks, nil
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
