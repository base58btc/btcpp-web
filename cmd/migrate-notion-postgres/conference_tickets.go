package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateTicketKeys(tickets []*types.ConfTicket, confTagByRef map[string]string) error {
	seen := make(map[string]struct{}, len(tickets))
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		confTag := confTagByRef[ticket.ConfRef]
		if confTag == "" {
			return fmt.Errorf("conference ticket %q has unresolved conference ref", ticket.Tier)
		}
		if strings.TrimSpace(ticket.Tier) == "" {
			return fmt.Errorf("conference ticket for %q has empty tier", confTag)
		}
		key := strings.ToLower(confTag) + "\x00" + ticketKey(ticket)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate conference ticket key %q for %q", ticketKey(ticket), confTag)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func importConferenceTickets(ctx context.Context, pool *pgxpool.Pool, tickets []*types.ConfTicket, confTagByRef map[string]string) error {
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		confTag := confTagByRef[ticket.ConfRef]
		if confTag == "" {
			return fmt.Errorf("conference ticket %q has unresolved conference ref", ticket.Tier)
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO conference_tickets (
				conference_id, ticket_key, tier, local_price, btc_price, usd_price,
				expires_start, expires_end, max_count, currency, symbol, post_symbol
			)
			SELECT id, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
			FROM conferences
			WHERE tag = $1
			ON CONFLICT (conference_id, ticket_key) DO UPDATE SET
				tier = EXCLUDED.tier,
				local_price = EXCLUDED.local_price,
				btc_price = EXCLUDED.btc_price,
				usd_price = EXCLUDED.usd_price,
				expires_start = EXCLUDED.expires_start,
				expires_end = EXCLUDED.expires_end,
				max_count = EXCLUDED.max_count,
				currency = EXCLUDED.currency,
				symbol = EXCLUDED.symbol,
				post_symbol = EXCLUDED.post_symbol,
				updated_at = now()
		`, confTag, ticketKey(ticket), ticket.Tier, int64(ticket.Local), int64(ticket.BTC), int64(ticket.USD),
			nullableTimesStart(ticket.Expires), nullableTimesEnd(ticket.Expires), int64(ticket.Max),
			ticket.Currency, ticket.Symbol, ticket.PostSymbol)
		if err != nil {
			return fmt.Errorf("upsert conference ticket %q/%q: %w", confTag, ticket.Tier, err)
		}
	}
	return nil
}

func validateConferenceTickets(ctx context.Context, pool *pgxpool.Pool, tickets []*types.ConfTicket, confTagByRef map[string]string) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM conference_tickets`).Scan(&count); err != nil {
		return fmt.Errorf("count conference tickets: %w", err)
	}
	if count < len(tickets) {
		return fmt.Errorf("postgres conference ticket count %d is less than Notion count %d", count, len(tickets))
	}
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		confTag := confTagByRef[ticket.ConfRef]
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM conference_tickets ct
				JOIN conferences c ON c.id = ct.conference_id
				WHERE c.tag = $1 AND ct.ticket_key = $2
			)
		`, confTag, ticketKey(ticket)).Scan(&exists); err != nil {
			return fmt.Errorf("validate conference ticket %q/%q: %w", confTag, ticket.Tier, err)
		}
		if !exists {
			return fmt.Errorf("missing conference ticket %q/%q in Postgres", confTag, ticket.Tier)
		}
	}
	return nil
}

func ticketKey(ticket *types.ConfTicket) string {
	if ticket == nil {
		return ""
	}
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(ticket.Tier)),
		timesKey(ticket.Expires),
		fmt.Sprintf("local:%d", ticket.Local),
		fmt.Sprintf("btc:%d", ticket.BTC),
		fmt.Sprintf("usd:%d", ticket.USD),
		fmt.Sprintf("max:%d", ticket.Max),
		strings.ToLower(strings.TrimSpace(ticket.Currency)),
		strings.TrimSpace(ticket.Symbol),
		strings.TrimSpace(ticket.PostSymbol),
	}, "|")
}

func timesKey(times *types.Times) string {
	if times == nil {
		return ""
	}
	start := ""
	if !times.Start.IsZero() {
		start = times.Start.UTC().Format(time.RFC3339Nano)
	}
	end := ""
	if times.End != nil && !times.End.IsZero() {
		end = times.End.UTC().Format(time.RFC3339Nano)
	}
	return start + "/" + end
}
