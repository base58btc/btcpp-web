package getters

import (
	"context"
	"errors"
	"fmt"

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
