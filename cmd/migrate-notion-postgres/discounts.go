package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateDiscountRows(discounts []*types.DiscountCode, confTagByRef map[string]string) error {
	for _, discount := range discounts {
		if discount == nil {
			continue
		}
		if strings.TrimSpace(discount.CodeName) == "" {
			return fmt.Errorf("discount with empty CodeName")
		}
		if strings.TrimSpace(discount.Discount) == "" {
			return fmt.Errorf("discount %q has empty Discount expression", discount.CodeName)
		}
		if discount.DiscType == 0 {
			return fmt.Errorf("discount %q has invalid Discount expression %q", discount.CodeName, discount.Discount)
		}
		for _, confRef := range discount.ConfRef {
			if confTagByRef[confRef] == "" {
				return fmt.Errorf("discount %q has unresolved conference ref", discount.CodeName)
			}
		}
	}
	return nil
}

func importDiscountRows(ctx context.Context, pool *pgxpool.Pool, discounts []*types.DiscountCode, confTagByRef map[string]string) (map[string]string, error) {
	idsByRef := make(map[string]string, len(discounts))
	for _, discount := range discounts {
		if discount == nil {
			continue
		}

		discType := ""
		if discount.DiscType != 0 {
			discType = string(discount.DiscType)
		}

		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO discounts (
				code_name, discount_expr, uses_count, affiliate_email, disc_type,
				amount, max_uses, extra_qty, valid_from, valid_until
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9, $10
			)
			ON CONFLICT (code_name) DO UPDATE SET
				discount_expr = EXCLUDED.discount_expr,
				uses_count = EXCLUDED.uses_count,
				affiliate_email = EXCLUDED.affiliate_email,
				disc_type = EXCLUDED.disc_type,
				amount = EXCLUDED.amount,
				max_uses = EXCLUDED.max_uses,
				extra_qty = EXCLUDED.extra_qty,
				valid_from = EXCLUDED.valid_from,
				valid_until = EXCLUDED.valid_until
			RETURNING id::text
		`, strings.TrimSpace(discount.CodeName), discount.Discount, int(discount.UsesCount),
			nullableString(discount.AffiliateEmail), nullableString(discType),
			nullableUint(discount.Amount), nullableUint(discount.MaxUses), int(discount.ExtraQty),
			nullableTimePtr(discount.ValidFrom), nullableTimePtr(discount.ValidUntil)).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert discount %q: %w", discount.CodeName, err)
		}
		if discount.Ref != "" {
			idsByRef[discount.Ref] = id
		}

		if _, err := pool.Exec(ctx, `DELETE FROM discounts_conferences WHERE discount_id = $1`, id); err != nil {
			return nil, fmt.Errorf("clear discount conference links %q: %w", discount.CodeName, err)
		}
		for _, confRef := range discount.ConfRef {
			confTag := confTagByRef[confRef]
			if strings.TrimSpace(confTag) == "" {
				continue
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO discounts_conferences (discount_id, conference_id)
				SELECT $1, id
				FROM conferences
				WHERE tag = $2
				ON CONFLICT DO NOTHING
			`, id, confTag); err != nil {
				return nil, fmt.Errorf("insert discount conference link %q/%q: %w", discount.CodeName, confTag, err)
			}
		}
	}
	return idsByRef, nil
}

func discountRefsByRef(discounts []*types.DiscountCode) map[string]bool {
	out := make(map[string]bool, len(discounts))
	for _, discount := range discounts {
		if discount == nil || discount.Ref == "" {
			continue
		}
		out[discount.Ref] = true
	}
	return out
}

func validateDiscounts(ctx context.Context, pool *pgxpool.Pool, discounts []*types.DiscountCode) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM discounts`).Scan(&count); err != nil {
		return fmt.Errorf("count discounts: %w", err)
	}
	if count < len(discounts) {
		return fmt.Errorf("postgres discount count %d is less than Notion count %d", count, len(discounts))
	}

	expectedLinks := 0
	for _, discount := range discounts {
		if discount != nil {
			expectedLinks += len(discount.ConfRef)
		}
	}
	var linkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM discounts_conferences`).Scan(&linkCount); err != nil {
		return fmt.Errorf("count discount conference links: %w", err)
	}
	if linkCount < expectedLinks {
		return fmt.Errorf("postgres discount conference link count %d is less than Notion relation count %d", linkCount, expectedLinks)
	}
	return nil
}
