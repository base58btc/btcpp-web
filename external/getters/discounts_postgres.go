package getters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5"
)

func createDiscountPostgres(ctx *config.AppContext, in DiscountInput) (string, error) {
	if in.CodeName == "" {
		return "", fmt.Errorf("CreateDiscount: CodeName is required")
	}
	if in.DiscountExpr == "" {
		return "", fmt.Errorf("CreateDiscount: DiscountExpr is required")
	}
	if in.ConfRef == "" {
		return "", fmt.Errorf("CreateDiscount: ConfRef is required")
	}
	return insertDiscountPostgres(ctx, in.CodeName, in.DiscountExpr, in.AffiliateEmail, []string{in.ConfRef})
}

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

func updateDiscountPostgres(ctx *config.AppContext, discountID string, in DiscountInput) error {
	if discountID == "" {
		return fmt.Errorf("UpdateDiscount: discountID is required")
	}
	if in.CodeName == "" {
		return fmt.Errorf("UpdateDiscount: CodeName is required")
	}
	if in.DiscountExpr == "" {
		return fmt.Errorf("UpdateDiscount: DiscountExpr is required")
	}
	if in.ConfRef == "" {
		return fmt.Errorf("UpdateDiscount: ConfRef is required")
	}
	return updateDiscountRowPostgres(ctx, discountID, in.CodeName, in.DiscountExpr, &in.AffiliateEmail, []string{in.ConfRef})
}

func archiveDiscountPostgres(ctx *config.AppContext, discountID string) error {
	if discountID == "" {
		return fmt.Errorf("ArchiveDiscount: discountID is required")
	}
	return archiveDiscountRowPostgres(ctx, discountID)
}

func insertDiscountPostgres(ctx *config.AppContext, codeName, discountExpr, affiliateEmail string, confRefs []string) (string, error) {
	if ctx == nil || ctx.DB == nil {
		return "", fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	discount, err := discountForWrite(codeName, discountExpr, affiliateEmail)
	if err != nil {
		return "", err
	}

	tx, err := ctx.DB.Begin(context.Background())
	if err != nil {
		return "", fmt.Errorf("begin discount insert: %w", err)
	}
	defer tx.Rollback(context.Background())

	discType := string(discount.DiscType)
	var discountID string
	err = tx.QueryRow(context.Background(), `
		INSERT INTO discounts (
			code_name, discount_expr, affiliate_email, disc_type, amount,
			max_uses, extra_qty, valid_from, valid_until
		) VALUES (
			$1, $2, NULLIF($3, '')::citext, $4, $5, $6, $7, $8, $9
		)
		RETURNING id::text
	`, discount.CodeName, discount.Discount, discount.AffiliateEmail, discType,
		nullableUintPtr(discount.Amount), nullableUintPtr(discount.MaxUses),
		int(discount.ExtraQty), discount.ValidFrom, discount.ValidUntil).Scan(&discountID)
	if err != nil {
		return "", fmt.Errorf("insert discount %q: %w", discount.CodeName, err)
	}
	if err := replaceDiscountConferenceLinksPostgres(tx, discountID, confRefs); err != nil {
		return "", err
	}
	if err := tx.Commit(context.Background()); err != nil {
		return "", fmt.Errorf("commit discount insert: %w", err)
	}

	discount.Ref = discountID
	discount.ConfRef = append([]string(nil), confRefs...)
	if discounts != nil {
		discounts = append(discounts, discount)
	}
	queueRefresh(JobDiscounts)
	return discountID, nil
}

func updateDiscountRowPostgres(ctx *config.AppContext, discountID, codeName, discountExpr string, affiliateEmail *string, confRefs []string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	email := ""
	if affiliateEmail != nil {
		email = *affiliateEmail
	}
	discount, err := discountForWrite(codeName, discountExpr, email)
	if err != nil {
		return err
	}

	tx, err := ctx.DB.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("begin discount update: %w", err)
	}
	defer tx.Rollback(context.Background())

	discType := string(discount.DiscType)
	var rowsAffected int64
	if affiliateEmail == nil {
		commandTag, execErr := tx.Exec(context.Background(), `
			UPDATE discounts
			SET code_name = $2,
				discount_expr = $3,
				disc_type = $4,
				amount = $5,
				max_uses = $6,
				extra_qty = $7,
				valid_from = $8,
				valid_until = $9
			WHERE id = $1
		`, discountID, discount.CodeName, discount.Discount, discType,
			nullableUintPtr(discount.Amount), nullableUintPtr(discount.MaxUses),
			int(discount.ExtraQty), discount.ValidFrom, discount.ValidUntil)
		err = execErr
		rowsAffected = commandTag.RowsAffected()
	} else {
		commandTag, execErr := tx.Exec(context.Background(), `
			UPDATE discounts
			SET code_name = $2,
				discount_expr = $3,
				affiliate_email = NULLIF($4, '')::citext,
				disc_type = $5,
				amount = $6,
				max_uses = $7,
				extra_qty = $8,
				valid_from = $9,
				valid_until = $10
			WHERE id = $1
		`, discountID, discount.CodeName, discount.Discount, discount.AffiliateEmail,
			discType, nullableUintPtr(discount.Amount), nullableUintPtr(discount.MaxUses),
			int(discount.ExtraQty), discount.ValidFrom, discount.ValidUntil)
		err = execErr
		rowsAffected = commandTag.RowsAffected()
	}
	if err != nil {
		return fmt.Errorf("update discount %s: %w", discountID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("discount %s not found", discountID)
	}
	if err := replaceDiscountConferenceLinksPostgres(tx, discountID, confRefs); err != nil {
		return err
	}
	if err := tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("commit discount update: %w", err)
	}
	patchDiscountCacheAfterUpdate(discountID, discount, confRefs, affiliateEmail)
	queueRefresh(JobDiscounts)
	return nil
}

func archiveDiscountRowPostgres(ctx *config.AppContext, discountID string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE discounts
		SET archived_at = now()
		WHERE id = $1
	`, discountID)
	if err != nil {
		return fmt.Errorf("archive discount %s: %w", discountID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("discount %s not found", discountID)
	}
	if discounts != nil {
		filtered := discounts[:0]
		for _, d := range discounts {
			if d == nil || d.Ref != discountID {
				filtered = append(filtered, d)
			}
		}
		discounts = filtered
	}
	queueRefresh(JobDiscounts)
	return nil
}

func replaceDiscountConferenceLinksPostgres(tx pgx.Tx, discountID string, confRefs []string) error {
	if _, err := tx.Exec(context.Background(), `DELETE FROM discounts_conferences WHERE discount_id = $1`, discountID); err != nil {
		return fmt.Errorf("clear discount conference links %s: %w", discountID, err)
	}
	for _, confRef := range confRefs {
		confRef = strings.TrimSpace(confRef)
		if confRef == "" {
			continue
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO discounts_conferences (discount_id, conference_id)
			VALUES ($1, $2)
			ON CONFLICT (discount_id, conference_id) DO NOTHING
		`, discountID, confRef); err != nil {
			return fmt.Errorf("insert discount conference link %s/%s: %w", discountID, confRef, err)
		}
	}
	return nil
}

func discountForWrite(codeName, discountExpr, affiliateEmail string) (*types.DiscountCode, error) {
	discount := &types.DiscountCode{
		CodeName:       strings.TrimSpace(codeName),
		Discount:       strings.TrimSpace(discountExpr),
		AffiliateEmail: strings.TrimSpace(affiliateEmail),
	}
	if discount.CodeName == "" {
		return nil, fmt.Errorf("discount code name is required")
	}
	if err := discount.ParseDiscountExpr(); err != nil {
		return nil, err
	}
	return discount, nil
}

func nullableUintPtr(value uint) *int {
	if value == 0 {
		return nil
	}
	out := int(value)
	return &out
}

func patchDiscountCacheAfterUpdate(discountID string, patch *types.DiscountCode, confRefs []string, affiliateEmail *string) {
	if discounts == nil || patch == nil {
		return
	}
	for _, d := range discounts {
		if d == nil || d.Ref != discountID {
			continue
		}
		d.CodeName = patch.CodeName
		d.Discount = patch.Discount
		d.ConfRef = append([]string(nil), confRefs...)
		if affiliateEmail != nil {
			d.AffiliateEmail = patch.AffiliateEmail
		}
		d.DiscType = 0
		d.Amount = 0
		d.MaxUses = 0
		d.ExtraQty = 0
		d.ValidFrom = nil
		d.ValidUntil = nil
		_ = d.ParseDiscountExpr()
		break
	}
}
