package getters

import (
	"context"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

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
