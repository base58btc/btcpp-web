package main

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type volunteerInfoImportRow struct {
	ref         string
	confRef     string
	orientLink  string
	orientStart interface{}
	orientEnd   interface{}
	notes       string
}

func listVolunteerInfoImportRows(n *types.Notion) ([]*volunteerInfoImportRow, error) {
	var out []*volunteerInfoImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.VolInfoDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseVolunteerInfoImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseVolunteerInfoImportRow(ref string, props map[string]notion.PropertyValue) *volunteerInfoImportRow {
	orientTimes := props["OrientTimes"]
	return &volunteerInfoImportRow{
		ref:         ref,
		confRef:     relationID(props["conf"]),
		orientLink:  props["OrientLink"].URL,
		orientStart: nullableTimePtr(dateStart(orientTimes)),
		orientEnd:   nullableTimePtr(dateEnd(orientTimes)),
		notes:       richText(props["Notes"]),
	}
}

func validateVolunteerInfoRows(rows []*volunteerInfoImportRow, confTagByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if confTagByRef[row.confRef] == "" {
			return fmt.Errorf("volunteer info %q has unresolved conf ref %q", row.ref, row.confRef)
		}
	}
	return nil
}

func importVolunteerInfoRows(ctx context.Context, pool *pgxpool.Pool, rows []*volunteerInfoImportRow, confTagByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		confTag := confTagByRef[row.confRef]
		if _, err := pool.Exec(ctx, `
			INSERT INTO volunteer_info (
				conference_id, orient_link_url, orient_start, orient_end, notes
			)
			SELECT c.id, $2, $3, $4, $5
			FROM conferences c
			WHERE c.tag = $1
			ON CONFLICT (conference_id) DO UPDATE SET
				orient_link_url = EXCLUDED.orient_link_url,
				orient_start = EXCLUDED.orient_start,
				orient_end = EXCLUDED.orient_end,
				notes = EXCLUDED.notes,
				updated_at = now()
		`, confTag, row.orientLink, row.orientStart, row.orientEnd, row.notes); err != nil {
			return fmt.Errorf("upsert volunteer info %q: %w", row.ref, err)
		}
	}
	return nil
}

func validateVolunteerInfo(ctx context.Context, pool *pgxpool.Pool, rows []*volunteerInfoImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_info`).Scan(&count); err != nil {
		return fmt.Errorf("count volunteer info: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres volunteer info count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}

func dateEnd(prop notion.PropertyValue) *time.Time {
	if prop.Date == nil {
		return nil
	}
	return prop.Date.End
}
