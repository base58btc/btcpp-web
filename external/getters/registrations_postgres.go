package getters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func checkInPostgres(ctx *config.AppContext, ticket string) (string, bool, error) {
	if ctx == nil || ctx.DB == nil {
		return "", false, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	var ticketType string
	var checkedInAt pgtype.Timestamptz
	var revoked bool
	err := ctx.DB.QueryRow(context.Background(), `
		SELECT type, checked_in_at, revoked
		FROM registrations
		WHERE ref_id = $1
	`, ticket).Scan(&ticketType, &checkedInAt, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", true, fmt.Errorf("Ticket not found")
	}
	if err != nil {
		return "", false, fmt.Errorf("query registration: %w", err)
	}
	if revoked {
		return "", true, fmt.Errorf("Ticket was revoked")
	}
	if checkedInAt.Valid {
		return "", true, fmt.Errorf("Already checked in")
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE registrations
		SET checked_in_at = now()
		WHERE ref_id = $1 AND checked_in_at IS NULL AND revoked = false
	`, ticket)
	if err != nil {
		return "", false, fmt.Errorf("check in registration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", true, fmt.Errorf("Already checked in")
	}
	return ticketType, true, nil
}

func soldTixCountPostgres(ctx *config.AppContext, confRef string) (uint, error) {
	if ctx == nil || ctx.DB == nil {
		return 0, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	var count int64
	err := ctx.DB.QueryRow(context.Background(), `
		SELECT count(*)
		FROM registrations
		WHERE conference_id = $1::uuid
	`, confRef).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count registrations: %w", err)
	}
	return uint(count), nil
}

func fetchRegistrationsPostgres(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	return queryRegistrationsPostgres(ctx, "conf", confRef)
}

func listRegistrationsByEmailPostgres(ctx *config.AppContext, email string) ([]*types.Registration, error) {
	if email == "" {
		return nil, nil
	}
	return queryRegistrationsPostgres(ctx, "email", email)
}

func queryRegistrationsPostgres(ctx *config.AppContext, filter string, value string) ([]*types.Registration, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	sql := `
		SELECT r.ref_id, coalesce(r.conference_id::text, ''), r.type,
			r.email::text, r.item_bought, coalesce(r.amount_paid, 0),
			r.currency, r.revoked
		FROM registrations r
	`
	args := []any{}
	switch filter {
	case "conf":
		if value != "" {
			sql += " WHERE r.conference_id = $1::uuid"
			args = append(args, value)
		}
	case "email":
		sql += " WHERE r.email = $1"
		args = append(args, value)
	default:
		return nil, fmt.Errorf("unknown registrations filter %q", filter)
	}
	sql += " ORDER BY r.registered_at DESC NULLS LAST, r.ref_id"

	rows, err := ctx.DB.Query(context.Background(), sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query registrations: %w", err)
	}
	defer rows.Close()

	var out []*types.Registration
	for rows.Next() {
		var registration types.Registration
		err := rows.Scan(
			&registration.RefID,
			&registration.ConfRef,
			&registration.Type,
			&registration.Email,
			&registration.ItemBought,
			&registration.Amount,
			&registration.Currency,
			&registration.Revoked,
		)
		if err != nil {
			return nil, fmt.Errorf("scan registration: %w", err)
		}
		out = append(out, &registration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registrations: %w", err)
	}
	return out, nil
}

func addTicketsPostgres(ctx *config.AppContext, entry *types.Entry, src string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if entry == nil {
		return fmt.Errorf("AddTickets: entry is nil")
	}
	email := strings.TrimSpace(entry.Email)
	if email == "" {
		return fmt.Errorf("AddTickets: entry email is required")
	}
	if strings.TrimSpace(entry.ConfRef) == "" {
		return fmt.Errorf("AddTickets: entry conference ref is required")
	}

	for i, item := range entry.Items {
		refID := types.UniqueID(entry.Email, entry.ID, int32(i))
		amountPaid := float64(item.Total) / 100
		_, err := ctx.DB.Exec(context.Background(), `
			INSERT INTO registrations (
				ref_id, checkout_id, conference_id, discount_id, type, email,
				item_bought, amount_paid, currency, platform, registered_at, revoked
			)
			VALUES (
				$1, $2, $3::uuid,
				NULLIF($4, '')::uuid,
				$5, $6, $7, $8, $9, $10, $11, false
			)
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
				revoked = false
		`, refID, entry.ID, entry.ConfRef, entry.DiscountRef, item.Type, email,
			item.Desc, amountPaid, entry.Currency, src, entry.Created)
		if err != nil {
			return fmt.Errorf("upsert registration %q: %w", refID, err)
		}
	}
	return nil
}

func revokeTicketPostgres(ctx *config.AppContext, lookupID string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE registrations
		SET revoked = true
		WHERE checkout_id = $1
	`, lookupID)
	if err != nil {
		return fmt.Errorf("revoke ticket %q: %w", lookupID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("ticket lookup %s not found", lookupID)
	}
	return nil
}
