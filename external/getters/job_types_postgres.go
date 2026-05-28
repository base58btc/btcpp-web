package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listJobsPostgres(ctx *config.AppContext) ([]*types.JobType, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, tag, display_order, title, tooltip, long_desc, show
		FROM job_types
		ORDER BY display_order, title
	`)
	if err != nil {
		return nil, fmt.Errorf("query job types: %w", err)
	}
	defer rows.Close()

	var jobs []*types.JobType
	for rows.Next() {
		var job types.JobType
		err := rows.Scan(
			&job.Ref,
			&job.Tag,
			&job.DisplayOrder,
			&job.Title,
			&job.Tooltip,
			&job.LongDesc,
			&job.Show,
		)
		if err != nil {
			return nil, fmt.Errorf("scan job type: %w", err)
		}
		jobs = append(jobs, &job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job types: %w", err)
	}
	return jobs, nil
}
