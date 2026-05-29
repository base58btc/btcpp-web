package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type subscriberImportRow struct {
	ref   string
	email string
	subs  []string
}

func listSubscriberImportRows(n *types.Notion) ([]*subscriberImportRow, error) {
	var out []*subscriberImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.NewsletterDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseSubscriberImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseSubscriberImportRow(ref string, props map[string]notion.PropertyValue) *subscriberImportRow {
	return &subscriberImportRow{
		ref:   ref,
		email: titleText(props["Email"]),
		subs:  multiSelectNames(props["Subs"]),
	}
}

func validateSubscriberRows(rows []*subscriberImportRow) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.email) == "" {
			return fmt.Errorf("subscriber %q has empty Email", row.ref)
		}
	}
	return nil
}

func importSubscriberRows(ctx context.Context, pool *pgxpool.Pool, rows []*subscriberImportRow) error {
	for _, row := range mergeSubscriberRows(rows) {
		if row == nil {
			continue
		}
		var subscriberID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO subscribers (email)
			VALUES ($1)
			ON CONFLICT (email) DO UPDATE SET
				email = EXCLUDED.email
			RETURNING id
		`, strings.TrimSpace(row.email)).Scan(&subscriberID); err != nil {
			return fmt.Errorf("insert subscriber %q: %w", row.ref, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM subscriber_subscriptions WHERE subscriber_id = $1`, subscriberID); err != nil {
			return fmt.Errorf("clear subscriber subscriptions %q: %w", row.ref, err)
		}
		for _, sub := range row.subs {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO subscriber_subscriptions (subscriber_id, name)
				VALUES ($1, $2)
				ON CONFLICT (subscriber_id, name) DO NOTHING
			`, subscriberID, sub); err != nil {
				return fmt.Errorf("insert subscriber subscription %q/%q: %w", row.ref, sub, err)
			}
		}
	}
	return nil
}

func validateSubscribers(ctx context.Context, pool *pgxpool.Pool, rows []*subscriberImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM subscribers`).Scan(&count); err != nil {
		return fmt.Errorf("count subscribers: %w", err)
	}
	expected := len(mergeSubscriberRows(rows))
	if count < expected {
		return fmt.Errorf("postgres subscriber count %d is less than Notion unique email count %d", count, expected)
	}
	return nil
}

func mergeSubscriberRows(rows []*subscriberImportRow) []*subscriberImportRow {
	byEmail := make(map[string]*subscriberImportRow, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		email := strings.TrimSpace(row.email)
		if email == "" {
			continue
		}
		key := strings.ToLower(email)
		merged := byEmail[key]
		if merged == nil {
			merged = &subscriberImportRow{ref: row.ref, email: email}
			byEmail[key] = merged
			order = append(order, key)
		}
		seen := make(map[string]bool, len(merged.subs))
		for _, sub := range merged.subs {
			seen[sub] = true
		}
		for _, sub := range row.subs {
			sub = strings.TrimSpace(sub)
			if sub == "" || seen[sub] {
				continue
			}
			merged.subs = append(merged.subs, sub)
			seen[sub] = true
		}
	}
	out := make([]*subscriberImportRow, 0, len(order))
	for _, key := range order {
		out = append(out, byEmail[key])
	}
	return out
}
