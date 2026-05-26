package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type conferenceDayImportRow struct {
	ref            string
	confTag        string
	dayNumber      int
	doorsStart     interface{}
	doorsEnd       interface{}
	breakfastStart interface{}
	breakfastEnd   interface{}
	lunchStart     interface{}
	lunchEnd       interface{}
	coffeeStart    interface{}
	coffeeEnd      interface{}
	venues         []string
}

func listConferenceDayImportRows(n *types.Notion) ([]*conferenceDayImportRow, error) {
	var out []*conferenceDayImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ConfInfoDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseConferenceDayImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseConferenceDayImportRow(ref string, props map[string]notion.PropertyValue) *conferenceDayImportRow {
	confTag := selectName(props["Conf"])
	if confTag == "" {
		confTag = richText(props["Conf"])
	}
	doorsStart, doorsEnd := clockRange(richText(props["Doors"]))
	breakfastStart, breakfastEnd := clockRange(richText(props["Breakfast"]))
	lunchStart, lunchEnd := clockRange(richText(props["Lunch"]))
	coffeeStart, coffeeEnd := clockRange(richText(props["Coffee"]))
	return &conferenceDayImportRow{
		ref:            ref,
		confTag:        confTag,
		dayNumber:      int(props["Day"].Number),
		doorsStart:     doorsStart,
		doorsEnd:       doorsEnd,
		breakfastStart: breakfastStart,
		breakfastEnd:   breakfastEnd,
		lunchStart:     lunchStart,
		lunchEnd:       lunchEnd,
		coffeeStart:    coffeeStart,
		coffeeEnd:      coffeeEnd,
		venues:         multiSelectNames(props["Venues"]),
	}
}

func validateConferenceDayRows(rows []*conferenceDayImportRow, confTagByRef map[string]string) error {
	knownConfTags := make(map[string]bool, len(confTagByRef))
	for _, tag := range confTagByRef {
		if tag != "" {
			knownConfTags[tag] = true
		}
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.confTag) == "" {
			return fmt.Errorf("conference day %q has empty Conf", row.ref)
		}
		if !knownConfTags[row.confTag] {
			return fmt.Errorf("conference day %q has unresolved Conf tag %q", row.ref, row.confTag)
		}
		if row.dayNumber < 1 {
			return fmt.Errorf("conference day %q has invalid Day %d", row.ref, row.dayNumber)
		}
	}
	return nil
}

func importConferenceDayRows(ctx context.Context, pool *pgxpool.Pool, rows []*conferenceDayImportRow) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO conference_days (
				conference_id, day_number, doors_start, doors_end,
				breakfast_start, breakfast_end, lunch_start, lunch_end,
				coffee_start, coffee_end, venues
			)
			SELECT c.id, $2, $3, $4,
				$5, $6, $7, $8,
				$9, $10, $11
			FROM conferences c
			WHERE c.tag = $1
			ON CONFLICT (conference_id, day_number) DO UPDATE SET
				doors_start = EXCLUDED.doors_start,
				doors_end = EXCLUDED.doors_end,
				breakfast_start = EXCLUDED.breakfast_start,
				breakfast_end = EXCLUDED.breakfast_end,
				lunch_start = EXCLUDED.lunch_start,
				lunch_end = EXCLUDED.lunch_end,
				coffee_start = EXCLUDED.coffee_start,
				coffee_end = EXCLUDED.coffee_end,
				venues = EXCLUDED.venues,
				updated_at = now()
		`, strings.TrimSpace(row.confTag), row.dayNumber, row.doorsStart, row.doorsEnd,
			row.breakfastStart, row.breakfastEnd, row.lunchStart, row.lunchEnd,
			row.coffeeStart, row.coffeeEnd, row.venues); err != nil {
			return fmt.Errorf("upsert conference day %q: %w", row.ref, err)
		}
	}
	return nil
}

func validateConferenceDays(ctx context.Context, pool *pgxpool.Pool, rows []*conferenceDayImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM conference_days`).Scan(&count); err != nil {
		return fmt.Errorf("count conference days: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres conference day count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}

func clockRange(raw string) (interface{}, interface{}) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.SplitN(raw, ",", 2)
	start := clockValue(parts[0])
	if len(parts) < 2 {
		return start, nil
	}
	return start, clockValue(parts[1])
}

func clockValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return nil
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return nil
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return nil
	}
	return fmt.Sprintf("%02d:%02d:00", hour, minute)
}
