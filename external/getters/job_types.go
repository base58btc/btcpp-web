package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func getJobs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting jobs...")
	if UsePostgresBackend(ctx) {
		jobs, err = listJobsPostgres(ctx)
	} else {
		jobs, err = ListJobsNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching jobs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d jobs!", len(jobs))
		writeCache("jobs", jobs)
	}
}

/* This may return nil */
func FetchJobsCached(ctx *config.AppContext) ([]*types.JobType, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if jobs == nil || lastJobTypeFetch.Before(deadline) {
		lastJobTypeFetch = time.Now()
		queueRefresh(JobJobs)
	}

	return jobs, nil
}

func ListJobs(n *types.Notion) ([]*types.JobType, error) {
	return ListJobsNotion(n)
}
