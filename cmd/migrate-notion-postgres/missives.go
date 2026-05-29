package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type missiveImportRow struct {
	ref         string
	publicUID   uint64
	title       string
	newsletters []string
	onlyFor     string
	markdown    string
	sendAtExpr  string
	sentAt      interface{}
	expiry      interface{}
}

func listMissiveImportRows(n *types.Notion) ([]*missiveImportRow, error) {
	var out []*missiveImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.MissivesDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseMissiveImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseMissiveImportRow(ref string, props map[string]notion.PropertyValue) *missiveImportRow {
	return &missiveImportRow{
		ref:         ref,
		publicUID:   uniqueID(props["ID"]),
		title:       titleText(props["Title"]),
		newsletters: multiSelectNames(props["Newsletter"]),
		onlyFor:     selectName(props["OnlyFor"]),
		markdown:    richText(props["Markdown"]),
		sendAtExpr:  richText(props["SendAt"]),
		sentAt:      nullableTimePtr(dateStart(props["SentAt"])),
		expiry:      nullableTimePtr(dateStart(props["Expiry"])),
	}
}

func validateMissiveRows(rows []*missiveImportRow) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.title) == "" {
			return fmt.Errorf("missive %q has empty Title", row.ref)
		}
	}
	return nil
}

func importMissiveRows(ctx context.Context, pool *pgxpool.Pool, rows []*missiveImportRow) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO missives (
				public_uid, title, newsletters, only_for, markdown, send_at_expr, sent_at, expiry
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8
			)
			ON CONFLICT (public_uid) DO UPDATE SET
				title = EXCLUDED.title,
				newsletters = EXCLUDED.newsletters,
				only_for = EXCLUDED.only_for,
				markdown = EXCLUDED.markdown,
				send_at_expr = EXCLUDED.send_at_expr,
				sent_at = EXCLUDED.sent_at,
				expiry = EXCLUDED.expiry
		`, nullableUID(row.publicUID), strings.TrimSpace(row.title), row.newsletters, row.onlyFor, row.markdown, row.sendAtExpr, row.sentAt, row.expiry); err != nil {
			return fmt.Errorf("insert missive %q: %w", row.ref, err)
		}
	}
	return nil
}

func validateMissives(ctx context.Context, pool *pgxpool.Pool, rows []*missiveImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM missives`).Scan(&count); err != nil {
		return fmt.Errorf("count missives: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres missive count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}
