package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func listConfInfosPostgres(ctx *config.AppContext, confTag string) ([]*types.ConfInfo, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	sql := `
		SELECT cd.id::text, c.tag, cd.day_number, c.start_date, c.timezone,
			cd.doors_start, cd.doors_end, cd.breakfast_start, cd.breakfast_end,
			cd.lunch_start, cd.lunch_end, cd.coffee_start, cd.coffee_end,
			cd.venues
		FROM conference_days cd
		JOIN conferences c ON c.id = cd.conference_id
	`
	args := []any{}
	if confTag != "" {
		sql += " WHERE c.tag = $1"
		args = append(args, confTag)
	}
	sql += " ORDER BY c.tag, cd.day_number"

	rows, err := ctx.DB.Query(context.Background(), sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query conference days: %w", err)
	}
	defer rows.Close()

	var out []*types.ConfInfo
	for rows.Next() {
		var ci types.ConfInfo
		var confStart pgtype.Timestamptz
		var timezoneName string
		var doorsStart, doorsEnd pgtype.Time
		var breakfastStart, breakfastEnd pgtype.Time
		var lunchStart, lunchEnd pgtype.Time
		var coffeeStart, coffeeEnd pgtype.Time
		err := rows.Scan(
			&ci.ID,
			&ci.ConfTag,
			&ci.Day,
			&confStart,
			&timezoneName,
			&doorsStart,
			&doorsEnd,
			&breakfastStart,
			&breakfastEnd,
			&lunchStart,
			&lunchEnd,
			&coffeeStart,
			&coffeeEnd,
			&ci.Venues,
		)
		if err != nil {
			return nil, fmt.Errorf("scan conference day: %w", err)
		}
		anchor := confDayAnchor(confStart, timezoneName, ci.Day)
		ci.Doors = timesFromPgTime(anchor, doorsStart, doorsEnd)
		ci.Breakfast = timesFromPgTime(anchor, breakfastStart, breakfastEnd)
		ci.Lunch = timesFromPgTime(anchor, lunchStart, lunchEnd)
		ci.Coffee = timesFromPgTime(anchor, coffeeStart, coffeeEnd)
		out = append(out, &ci)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conference days: %w", err)
	}
	return out, nil
}

func confDayAnchor(confStart pgtype.Timestamptz, timezoneName string, day int) time.Time {
	loc := time.UTC
	if timezoneName != "" {
		if loaded, err := time.LoadLocation(timezoneName); err == nil {
			loc = loaded
		}
	}
	start := time.Now().In(loc)
	if confStart.Valid {
		start = confStart.Time.In(loc)
	}
	if day < 1 {
		day = 1
	}
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, day-1)
}

func timesFromPgTime(anchor time.Time, start pgtype.Time, end pgtype.Time) *types.Times {
	if !start.Valid {
		return nil
	}
	startTime := pgTimeOnDay(anchor, start)
	if !end.Valid {
		return &types.Times{Start: startTime}
	}
	endTime := pgTimeOnDay(anchor, end)
	return &types.Times{Start: startTime, End: &endTime}
}

func pgTimeOnDay(anchor time.Time, value pgtype.Time) time.Time {
	duration := time.Duration(value.Microseconds) * time.Microsecond
	return anchor.Add(duration)
}
