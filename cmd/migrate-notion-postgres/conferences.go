package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func importConferences(ctx context.Context, pool *pgxpool.Pool, confs []*types.Conf) error {
	for _, conf := range confs {
		if conf == nil {
			continue
		}
		if strings.TrimSpace(conf.Tag) == "" {
			return fmt.Errorf("conference with empty tag")
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO conferences (
				tag, public_uid, active, description, og_flavor, emoji, tagline,
				date_desc, start_date, end_date, timezone, location, venue,
				venue_map_url, venue_website_url, show_hackathon, has_satellites,
				orient_cal_notif
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7,
				$8, $9, $10, $11, $12, $13,
				$14, $15, $16, $17, $18
			)
			ON CONFLICT (tag) DO UPDATE SET
				public_uid = EXCLUDED.public_uid,
				active = EXCLUDED.active,
				description = EXCLUDED.description,
				og_flavor = EXCLUDED.og_flavor,
				emoji = EXCLUDED.emoji,
				tagline = EXCLUDED.tagline,
				date_desc = EXCLUDED.date_desc,
				start_date = EXCLUDED.start_date,
				end_date = EXCLUDED.end_date,
				timezone = EXCLUDED.timezone,
				location = EXCLUDED.location,
				venue = EXCLUDED.venue,
				venue_map_url = EXCLUDED.venue_map_url,
				venue_website_url = EXCLUDED.venue_website_url,
				show_hackathon = EXCLUDED.show_hackathon,
				has_satellites = EXCLUDED.has_satellites,
				orient_cal_notif = EXCLUDED.orient_cal_notif,
				updated_at = now()
		`, conf.Tag, nullableUID(conf.UID), conf.Active, conf.Desc, conf.OGFlavor, conf.Emoji,
			conf.Tagline, conf.DateDesc, nullableDate(conf.StartDate), nullableDate(conf.EndDate),
			conf.Timezone, conf.Location, conf.Venue, conf.VenueMap, conf.VenueWebsite,
			conf.ShowHackathon, conf.HasSatellites, conf.OrientCalNotif)
		if err != nil {
			return fmt.Errorf("upsert conference %q: %w", conf.Tag, err)
		}
	}
	return nil
}

func validateConferences(ctx context.Context, pool *pgxpool.Pool, confs []*types.Conf) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM conferences`).Scan(&count); err != nil {
		return fmt.Errorf("count conferences: %w", err)
	}
	if count < len(confs) {
		return fmt.Errorf("postgres conference count %d is less than Notion count %d", count, len(confs))
	}
	for _, conf := range confs {
		if conf == nil || conf.Tag == "" {
			continue
		}
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM conferences WHERE tag = $1)`, conf.Tag).Scan(&exists); err != nil {
			return fmt.Errorf("validate conference %q: %w", conf.Tag, err)
		}
		if !exists {
			return fmt.Errorf("missing conference %q in Postgres", conf.Tag)
		}
	}
	return nil
}
