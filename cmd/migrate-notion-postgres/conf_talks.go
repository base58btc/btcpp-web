package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type confTalkImportRow struct {
	ref             string
	confTag         string
	proposalRef     string
	clipart         string
	scheduledStart  *time.Time
	scheduledEnd    *time.Time
	productionNotes string
	venue           string
	section         string
	calNotif        string
	socialCard      string
}

func listConfTalkImportRows(n *types.Notion) ([]*confTalkImportRow, error) {
	var out []*confTalkImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ConfTalkDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseConfTalkImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseConfTalkImportRow(ref string, props map[string]notion.PropertyValue) *confTalkImportRow {
	start, end := dateRange(props["TalkTime"])
	return &confTalkImportRow{
		ref:             ref,
		confTag:         selectName(props["Event"]),
		proposalRef:     relationID(props["proposal"]),
		clipart:         textValue(props["Clipart"]),
		scheduledStart:  start,
		scheduledEnd:    end,
		productionNotes: richText(props["ProductionNotes"]),
		venue:           selectName(props["Venue"]),
		section:         selectOrText(props["Section"]),
		calNotif:        richText(props["CalNotif"]),
		socialCard:      richText(props["SocialCard"]),
	}
}

func validateConfTalkRows(rows []*confTalkImportRow, confTagByRef map[string]string, proposalsByRef map[string]*types.Proposal) error {
	confTags := make(map[string]bool, len(confTagByRef))
	for _, tag := range confTagByRef {
		if tag != "" {
			confTags[tag] = true
		}
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.confTag) == "" {
			return fmt.Errorf("conf talk %q has empty Event", row.ref)
		}
		if !confTags[row.confTag] {
			return fmt.Errorf("conf talk %q has unresolved Event %q", row.ref, row.confTag)
		}
		if row.proposalRef != "" && proposalsByRef[row.proposalRef] == nil {
			return fmt.Errorf("conf talk %q has unresolved proposal ref", row.ref)
		}
	}
	return nil
}

func importConfTalkRows(ctx context.Context, pool *pgxpool.Pool, rows []*confTalkImportRow, proposalIDsByRef map[string]string) (map[string]string, error) {
	idsByRef := make(map[string]string, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		proposalID := ""
		if row.proposalRef != "" {
			proposalID = proposalIDsByRef[row.proposalRef]
			if proposalID == "" {
				return nil, fmt.Errorf("conf talk %q has unresolved imported proposal", row.ref)
			}
		}
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO conf_talks (
				conference_id, proposal_id, clipart_path, scheduled_start, scheduled_end,
				production_notes, venue, section, cal_notif, social_card_path
			)
			SELECT c.id, $2, $3, $4, $5, $6, $7, $8, $9, $10
			FROM conferences c
			WHERE c.tag = $1
			ON CONFLICT (proposal_id) DO UPDATE SET
				conference_id = EXCLUDED.conference_id,
				clipart_path = EXCLUDED.clipart_path,
				scheduled_start = EXCLUDED.scheduled_start,
				scheduled_end = EXCLUDED.scheduled_end,
				production_notes = EXCLUDED.production_notes,
				venue = EXCLUDED.venue,
				section = EXCLUDED.section,
				cal_notif = EXCLUDED.cal_notif,
				social_card_path = EXCLUDED.social_card_path
			RETURNING id::text
		`, row.confTag, nullableString(proposalID), row.clipart, nullableTimePtr(row.scheduledStart), nullableTimePtr(row.scheduledEnd),
			row.productionNotes, row.venue, row.section, row.calNotif, row.socialCard).Scan(&id); err != nil {
			return nil, fmt.Errorf("insert conf talk %q: %w", row.ref, err)
		}
		if row.ref != "" {
			idsByRef[row.ref] = id
		}
	}
	return idsByRef, nil
}

func validateConfTalks(ctx context.Context, pool *pgxpool.Pool, rows []*confTalkImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM conf_talks`).Scan(&count); err != nil {
		return fmt.Errorf("count conf talks: %w", err)
	}
	expectedRows := dedupedConfTalkCount(rows)
	if count < expectedRows {
		return fmt.Errorf("postgres conf talk count %d is less than deduped Notion count %d", count, expectedRows)
	}
	return nil
}

func dedupedConfTalkCount(rows []*confTalkImportRow) int {
	proposalRefs := make(map[string]bool)
	count := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.proposalRef == "" {
			count++
			continue
		}
		if !proposalRefs[row.proposalRef] {
			proposalRefs[row.proposalRef] = true
			count++
		}
	}
	return count
}

func dateRange(prop notion.PropertyValue) (*time.Time, *time.Time) {
	if prop.Date == nil {
		return nil, nil
	}
	return &prop.Date.Start, prop.Date.End
}

func textValue(prop notion.PropertyValue) string {
	if value := richText(prop); value != "" {
		return value
	}
	return titleText(prop)
}

func selectOrText(prop notion.PropertyValue) string {
	if value := selectName(prop); value != "" {
		return value
	}
	return richText(prop)
}
