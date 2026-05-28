package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func getConfs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting confs...")
	if UsePostgresBackend(ctx) {
		confs, err = listConferencesPostgres(ctx)
	} else {
		confs, err = ListConferencesNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching confs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d confs!", len(confs))
		writeCache("confs", confs)
	}
}

func FetchConfsCached(ctx *config.AppContext) ([]*types.Conf, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if confs == nil || lastConfsFetch.Before(deadline) {
		lastConfsFetch = time.Now()
		queueRefresh(JobConfs)
	}

	return confs, nil
}

func ListConfTickets(n *types.Notion) ([]*types.ConfTicket, error) {
	return ListConfTicketsNotion(n)
}

func ListConferences(n *types.Notion) ([]*types.Conf, error) {
	return ListConferencesNotion(n)
}

func ListConferencesOnly(n *types.Notion) ([]*types.Conf, error) {
	return ListConferencesOnlyNotion(n)
}
