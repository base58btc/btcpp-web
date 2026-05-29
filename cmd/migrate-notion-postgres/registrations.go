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

type registrationImportRow struct {
	ref              string
	refID            string
	checkoutID       string
	confRef          string
	discountRef      string
	registrationType string
	email            string
	itemBought       string
	amountPaid       float64
	currency         string
	platform         string
	registeredAt     interface{}
	checkedInAt      interface{}
	revoked          bool
}

func listRegistrationImportRows(n *types.Notion) ([]*registrationImportRow, error) {
	var out []*registrationImportRow
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
			out = append(out, parseRegistrationImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseRegistrationImportRow(ref string, props map[string]notion.PropertyValue) *registrationImportRow {
	return &registrationImportRow{
		ref:              ref,
		refID:            textValue(props["RefID"]),
		checkoutID:       richText(props["Lookup ID"]),
		confRef:          relationID(props["conf"]),
		discountRef:      relationID(props["discount"]),
		registrationType: selectName(props["Type"]),
		email:            props["Email"].Email,
		itemBought:       richText(props["Item Bought"]),
		amountPaid:       props["Amount Paid"].Number,
		currency:         selectName(props["Currency"]),
		platform:         selectName(props["Platform"]),
		registeredAt:     nullableTimePtr(parseTextTimestamp(richText(props["Timestamp"]))),
		checkedInAt:      nullableTimePtr(parseTextTimestamp(richText(props["Checked In"]))),
		revoked:          checkbox(props["Revoked"].Checkbox),
	}
}

func validateRegistrationRows(rows []*registrationImportRow, confTagByRef map[string]string, discountRefs map[string]bool) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.refID) == "" {
			return fmt.Errorf("registration %q has empty RefID", row.ref)
		}
		if strings.TrimSpace(row.email) == "" {
			return fmt.Errorf("registration %q has empty Email", row.ref)
		}
		if row.confRef != "" && confTagByRef[row.confRef] == "" {
			return fmt.Errorf("registration %q has unresolved conf ref", row.ref)
		}
		if row.discountRef != "" && !discountRefs[row.discountRef] {
			return fmt.Errorf("registration %q has unresolved discount ref", row.ref)
		}
	}
	return nil
}

func importRegistrationRows(ctx context.Context, pool *pgxpool.Pool, rows []*registrationImportRow, confTagByRef map[string]string, discountIDsByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		confTag := confTagByRef[row.confRef]
		discountID := ""
		if row.discountRef != "" {
			discountID = discountIDsByRef[row.discountRef]
			if discountID == "" {
				return fmt.Errorf("registration %q has unresolved imported discount", row.ref)
			}
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO registrations (
				ref_id, checkout_id, conference_id, discount_id, type, email,
				item_bought, amount_paid, currency, platform, registered_at, checked_in_at, revoked
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
				registered_at = EXCLUDED.registered_at,
				checked_in_at = EXCLUDED.checked_in_at,
				revoked = EXCLUDED.revoked
		`, strings.TrimSpace(row.refID), row.checkoutID, nullableString(confTag), nullableString(discountID),
			row.registrationType, strings.TrimSpace(row.email), row.itemBought, nullableFloat64(row.amountPaid),
			row.currency, row.platform, row.registeredAt, row.checkedInAt, row.revoked); err != nil {
			return fmt.Errorf("insert registration %q: %w", row.ref, err)
		}
	}
	return nil
}

func validateRegistrations(ctx context.Context, pool *pgxpool.Pool, rows []*registrationImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM registrations`).Scan(&count); err != nil {
		return fmt.Errorf("count registrations: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres registration count %d is less than Notion count %d", count, len(rows))
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
