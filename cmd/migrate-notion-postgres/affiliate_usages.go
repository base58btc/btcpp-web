package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type affiliateUsageImportRow struct {
	ref            string
	codeName       string
	affiliateEmail string
	confTag        string
	savedSats      int64
	earnedSats     int64
	ticketsCount   int
	createdAt      interface{}
}

func listAffiliateUsageImportRows(n *types.Notion) ([]*affiliateUsageImportRow, error) {
	var out []*affiliateUsageImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.AffiliateUsageDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseAffiliateUsageImportRow(page.ID, page.Properties, page.CreatedTime))
		}
	}
	return out, nil
}

func parseAffiliateUsageImportRow(ref string, props map[string]notion.PropertyValue, createdAt interface{}) *affiliateUsageImportRow {
	return &affiliateUsageImportRow{
		ref:            ref,
		codeName:       richText(props["DiscountCode"]),
		affiliateEmail: emailOrRichText(props["AffiliateEmail"]),
		confTag:        selectName(props["Conference"]),
		savedSats:      int64(props["SavedSats"].Number),
		earnedSats:     int64(props["EarnedSats"].Number),
		ticketsCount:   int(props["TicketsCount"].Number),
		createdAt:      createdAt,
	}
}

func validateAffiliateUsageRows(rows []*affiliateUsageImportRow, confTagByRef map[string]string) error {
	knownConfTags := make(map[string]bool, len(confTagByRef))
	for _, tag := range confTagByRef {
		if tag != "" {
			knownConfTags[tag] = true
		}
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.codeName) == "" {
			return fmt.Errorf("affiliate usage %q has empty DiscountCode", row.ref)
		}
		if strings.TrimSpace(row.affiliateEmail) == "" {
			return fmt.Errorf("affiliate usage %q has empty AffiliateEmail", row.ref)
		}
		if strings.TrimSpace(row.confTag) != "" && !knownConfTags[row.confTag] {
			return fmt.Errorf("affiliate usage %q has unresolved Conference tag %q", row.ref, row.confTag)
		}
		if row.ticketsCount < 0 {
			return fmt.Errorf("affiliate usage %q has negative TicketsCount", row.ref)
		}
	}
	return nil
}

func importAffiliateUsageRows(ctx context.Context, pool *pgxpool.Pool, rows []*affiliateUsageImportRow) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO affiliate_usages (
				discount_id, conference_id, code_name_snapshot, affiliate_email,
				saved_sats, earned_sats, tickets_count, created_at
			)
			SELECT d.id, c.id, $1, $2, $3, $4, $5, $6
			FROM (SELECT $1::citext AS code_name, $7::text AS conf_tag) input
			LEFT JOIN discounts d ON d.code_name = input.code_name
			LEFT JOIN conferences c ON c.tag = input.conf_tag
		`, strings.TrimSpace(row.codeName), strings.TrimSpace(row.affiliateEmail),
			row.savedSats, row.earnedSats, row.ticketsCount, row.createdAt, nullableString(row.confTag)); err != nil {
			return fmt.Errorf("insert affiliate usage %q: %w", row.ref, err)
		}
	}
	return nil
}

func validateAffiliateUsages(ctx context.Context, pool *pgxpool.Pool, rows []*affiliateUsageImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM affiliate_usages`).Scan(&count); err != nil {
		return fmt.Errorf("count affiliate usages: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres affiliate usage count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}

func emailOrRichText(prop notion.PropertyValue) string {
	if prop.Email != "" {
		return prop.Email
	}
	return richText(prop)
}
