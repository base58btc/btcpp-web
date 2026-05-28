package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func getSpeakers(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting speakers...")
	if UsePostgresBackend(ctx) {
		cacheSpeakers, err = listSpeakersPostgres(ctx)
	} else {
		cacheSpeakers, err = ListSpeakersNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching speakers %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d speakers!", len(cacheSpeakers))
		writeCache("speakers", cacheSpeakers)
		ctx.Infos.Printf("there are %d callbacks", len(onSpeakersRefresh))
		for _, cb := range onSpeakersRefresh {
			cb(ctx, cacheSpeakers)
		}
	}
}

/* This may return nil */
func FetchSpeakersCached(ctx *config.AppContext) ([]*types.Speaker, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if cacheSpeakers == nil || lastSpeakerFetch.Before(deadline) {
		/* Set last fetch to now even if there's errors */
		lastSpeakerFetch = time.Now()
		queueRefresh(JobSpeakers)
	}

	return cacheSpeakers, nil
}

func ListSpeakers(n *types.Notion) ([]*types.Speaker, error) {
	return ListSpeakersNotion(n)
}
