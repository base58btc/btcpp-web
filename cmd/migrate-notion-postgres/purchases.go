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

type purchaseImportRow struct {
	ref          string
	refID        string
	checkoutID   string
	confRef      string
	discountRef  string
	purchaseType string
	email        string
	itemBought   string
	amountPaid   float64
	currency     string
	platform     string
	purchasedAt  interface{}
	checkedInAt  interface{}
	revoked      bool
}

func listPurchaseImportRows(n *types.Notion) ([]*purchaseImportRow, error) {
	var out []*purchaseImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.PurchasesDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parsePurchaseImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parsePurchaseImportRow(ref string, props map[string]notion.PropertyValue) *purchaseImportRow {
	return &purchaseImportRow{
		ref:          ref,
		refID:        textValue(props["RefID"]),
		checkoutID:   richText(props["Lookup ID"]),
		confRef:      relationID(props["conf"]),
		discountRef:  relationID(props["discount"]),
		purchaseType: selectName(props["Type"]),
		email:        props["Email"].Email,
		itemBought:   richText(props["Item Bought"]),
		amountPaid:   props["Amount Paid"].Number,
		currency:     selectName(props["Currency"]),
		platform:     selectName(props["Platform"]),
		purchasedAt:  nullableTimePtr(parseTextTimestamp(richText(props["Timestamp"]))),
		checkedInAt:  nullableTimePtr(parseTextTimestamp(richText(props["Checked In"]))),
		revoked:      checkbox(props["Revoked"].Checkbox),
	}
}

func validatePurchaseRows(rows []*purchaseImportRow, confTagByRef map[string]string, discountRefs map[string]bool) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.refID) == "" {
			return fmt.Errorf("purchase %q has empty RefID", row.ref)
		}
		if strings.TrimSpace(row.email) == "" {
			return fmt.Errorf("purchase %q has empty Email", row.ref)
		}
		if row.confRef != "" && confTagByRef[row.confRef] == "" {
			return fmt.Errorf("purchase %q has unresolved conf ref", row.ref)
		}
		if row.discountRef != "" && !discountRefs[row.discountRef] {
			return fmt.Errorf("purchase %q has unresolved discount ref", row.ref)
		}
	}
	return nil
}

func importPurchaseRows(ctx context.Context, pool *pgxpool.Pool, rows []*purchaseImportRow, confTagByRef map[string]string, discountIDsByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		confTag := confTagByRef[row.confRef]
		discountID := ""
		if row.discountRef != "" {
			discountID = discountIDsByRef[row.discountRef]
			if discountID == "" {
				return fmt.Errorf("purchase %q has unresolved imported discount", row.ref)
			}
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO purchases (
				ref_id, checkout_id, conference_id, discount_id, type, email,
				item_bought, amount_paid, currency, platform, purchased_at, checked_in_at, revoked
			)
			SELECT $1, $2, c.id, $4, $5, $6,
				$7, $8, $9, $10, $11, $12, $13
			FROM (SELECT $3::text AS tag) input
			LEFT JOIN conferences c ON c.tag = input.tag
			ON CONFLICT (ref_id) DO UPDATE SET
				checkout_id = EXCLUDED.checkout_id,
				conference_id = EXCLUDED.conference_id,
				discount_id = EXCLUDED.discount_id,
				type = EXCLUDED.type,
				email = EXCLUDED.email,
				item_bought = EXCLUDED.item_bought,
				amount_paid = EXCLUDED.amount_paid,
				currency = EXCLUDED.currency,
				platform = EXCLUDED.platform,
				purchased_at = EXCLUDED.purchased_at,
				checked_in_at = EXCLUDED.checked_in_at,
				revoked = EXCLUDED.revoked
		`, strings.TrimSpace(row.refID), row.checkoutID, nullableString(confTag), nullableString(discountID),
			row.purchaseType, strings.TrimSpace(row.email), row.itemBought, nullableFloat64(row.amountPaid),
			row.currency, row.platform, row.purchasedAt, row.checkedInAt, row.revoked); err != nil {
			return fmt.Errorf("insert purchase %q: %w", row.ref, err)
		}
	}
	return nil
}

func validatePurchases(ctx context.Context, pool *pgxpool.Pool, rows []*purchaseImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM purchases`).Scan(&count); err != nil {
		return fmt.Errorf("count purchases: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres purchase count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}

func parseTextTimestamp(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return &t
	}
	return nil
}
