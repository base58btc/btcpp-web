package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listDiscountsPostgres(ctx *config.AppContext) ([]*types.DiscountCode, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT discounts.id::text, discounts.code_name::text, discounts.discount_expr,
			discounts.uses_count, coalesce(discounts.affiliate_email::text, ''),
			coalesce(conferences.id::text, '')
		FROM discounts
		LEFT JOIN discounts_conferences ON discounts_conferences.discount_id = discounts.id
		LEFT JOIN conferences ON conferences.id = discounts_conferences.conference_id
		WHERE discounts.archived_at IS NULL
		ORDER BY discounts.code_name, conferences.tag
	`)
	if err != nil {
		return nil, fmt.Errorf("query discounts: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]*types.DiscountCode)
	var out []*types.DiscountCode
	for rows.Next() {
		var id string
		var confRef string
		var usesCount int64
		discount := &types.DiscountCode{}
		err := rows.Scan(
			&id,
			&discount.CodeName,
			&discount.Discount,
			&usesCount,
			&discount.AffiliateEmail,
			&confRef,
		)
		if err != nil {
			return nil, fmt.Errorf("scan discount: %w", err)
		}

		existing := byID[id]
		if existing == nil {
			discount.Ref = id
			discount.UsesCount = uint(usesCount)
			_ = discount.ParseDiscountExpr()
			byID[id] = discount
			out = append(out, discount)
			existing = discount
		}
		if confRef != "" {
			existing.ConfRef = append(existing.ConfRef, confRef)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate discounts: %w", err)
	}
	return out, nil
}

func incrementDiscountUsesPostgres(ctx *config.AppContext, discountRef string, addCount uint) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	_, err := ctx.DB.Exec(context.Background(), `
		UPDATE discounts
		SET uses_count = uses_count + $2
		WHERE id = $1
	`, discountRef, int64(addCount))
	if err != nil {
		return fmt.Errorf("increment discount uses: %w", err)
	}
	for _, d := range discounts {
		if d != nil && d.Ref == discountRef {
			d.UsesCount += addCount
			break
		}
	}
	lastDiscountFetch = time.Time{}
	return nil
}
