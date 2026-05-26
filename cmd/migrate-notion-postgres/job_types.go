package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateJobTypeRows(jobTypes []*types.JobType) error {
	for _, jobType := range jobTypes {
		if jobType == nil {
			continue
		}
		if strings.TrimSpace(jobType.Tag) == "" {
			return fmt.Errorf("job type %q has empty Tag", jobType.Ref)
		}
		if strings.TrimSpace(jobType.Title) == "" {
			return fmt.Errorf("job type %q has empty Title", jobType.Ref)
		}
	}
	return nil
}

func importJobTypeRows(ctx context.Context, pool *pgxpool.Pool, jobTypes []*types.JobType) error {
	for _, jobType := range jobTypes {
		if jobType == nil {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_types (
				tag, display_order, title, tooltip, long_desc, show
			) VALUES (
				$1, $2, $3, $4, $5, $6
			)
			ON CONFLICT (tag) DO UPDATE SET
				display_order = EXCLUDED.display_order,
				title = EXCLUDED.title,
				tooltip = EXCLUDED.tooltip,
				long_desc = EXCLUDED.long_desc,
				show = EXCLUDED.show,
				updated_at = now()
		`, strings.TrimSpace(jobType.Tag), jobType.DisplayOrder, strings.TrimSpace(jobType.Title),
			jobType.Tooltip, jobType.LongDesc, jobType.Show); err != nil {
			return fmt.Errorf("upsert job type %q: %w", jobType.Ref, err)
		}
	}
	return nil
}

func validateJobTypes(ctx context.Context, pool *pgxpool.Pool, jobTypes []*types.JobType) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_types`).Scan(&count); err != nil {
		return fmt.Errorf("count job types: %w", err)
	}
	if count < len(jobTypes) {
		return fmt.Errorf("postgres job type count %d is less than Notion count %d", count, len(jobTypes))
	}
	return nil
}
