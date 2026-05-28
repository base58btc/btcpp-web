package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func recordAffiliateUsagePostgres(ctx *config.AppContext, in AffiliateUsageInput) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	_, err := ctx.DB.Exec(context.Background(), `
		INSERT INTO affiliate_usages (
			discount_id, conference_id, code_name_snapshot, affiliate_email,
			saved_sats, earned_sats, tickets_count
		)
		VALUES (
			(SELECT id FROM discounts WHERE code_name = $1 LIMIT 1),
			(SELECT id FROM conferences WHERE tag = $2 LIMIT 1),
			$1, $3, $4, $5, $6
		)
	`, in.CodeName, in.ConfTag, in.AffiliateEmail, in.SavedSats, in.EarnedSats, int(in.TicketsCount))
	if err != nil {
		return fmt.Errorf("insert affiliate usage: %w", err)
	}
	return nil
}

func listAffiliateUsagePostgres(ctx *config.AppContext) ([]*types.AffiliateUsage, error) {
	return queryAffiliateUsagePostgres(ctx, "", "")
}

func queryAffiliateUsageByEmailPostgres(ctx *config.AppContext, email string) ([]*types.AffiliateUsage, error) {
	if email == "" {
		return nil, nil
	}
	return queryAffiliateUsagePostgres(ctx, "email", email)
}

func queryAffiliateUsageByConfPostgres(ctx *config.AppContext, confTag string) ([]*types.AffiliateUsage, error) {
	if confTag == "" {
		return nil, nil
	}
	return queryAffiliateUsagePostgres(ctx, "conf", confTag)
}

func queryAffiliateUsagePostgres(ctx *config.AppContext, filter string, value string) ([]*types.AffiliateUsage, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	sql := `
		SELECT au.id::text, au.code_name_snapshot::text, au.affiliate_email::text,
			coalesce(c.tag, ''), au.saved_sats, au.earned_sats,
			au.tickets_count, au.created_at
		FROM affiliate_usages au
		LEFT JOIN conferences c ON c.id = au.conference_id
	`
	args := []any{}
	switch filter {
	case "email":
		sql += " WHERE au.affiliate_email = $1"
		args = append(args, value)
	case "conf":
		sql += " WHERE c.tag = $1"
		args = append(args, value)
	case "":
	default:
		return nil, fmt.Errorf("unknown affiliate usage filter %q", filter)
	}
	sql += " ORDER BY au.created_at DESC, au.id"

	rows, err := ctx.DB.Query(context.Background(), sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query affiliate usages: %w", err)
	}
	defer rows.Close()

	var out []*types.AffiliateUsage
	for rows.Next() {
		var usage types.AffiliateUsage
		var ticketsCount int
		var created time.Time
		err := rows.Scan(
			&usage.ID,
			&usage.CodeName,
			&usage.AffiliateEmail,
			&usage.ConfTag,
			&usage.SavedSats,
			&usage.EarnedSats,
			&ticketsCount,
			&created,
		)
		if err != nil {
			return nil, fmt.Errorf("scan affiliate usage: %w", err)
		}
		usage.TicketsCount = uint(ticketsCount)
		usage.Created = &created
		out = append(out, &usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate affiliate usages: %w", err)
	}
	return out, nil
}

func updateAffiliateUsageSatsPostgres(ctx *config.AppContext, usageID string, savedSats, earnedSats int64) error {
	if usageID == "" {
		return fmt.Errorf("UpdateAffiliateUsageSats: usageID is required")
	}
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE affiliate_usages
		SET saved_sats = $2, earned_sats = $3
		WHERE id = $1
	`, usageID, savedSats, earnedSats)
	if err != nil {
		return fmt.Errorf("update affiliate usage sats: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("affiliate usage %s not found", usageID)
	}
	return nil
}
