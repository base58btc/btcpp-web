package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func listConferencesPostgres(ctx *config.AppContext) ([]*types.Conf, error) {
	confs, err := listConferencesOnlyPostgres(ctx)
	if err != nil {
		return nil, err
	}

	tickets, err := listConfTicketsPostgres(ctx)
	if err != nil {
		return nil, err
	}

	confByID := make(map[string]*types.Conf, len(confs))
	for _, conf := range confs {
		if conf != nil {
			confByID[conf.Ref] = conf
		}
	}
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		if conf := confByID[ticket.ConfRef]; conf != nil {
			conf.Tickets = append(conf.Tickets, ticket)
		}
	}
	return confs, nil
}

func listConferencesOnlyPostgres(ctx *config.AppContext) ([]*types.Conf, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, tag, public_uid, active, description, og_flavor, emoji,
			tagline, date_desc, start_date, end_date, timezone, location, venue,
			venue_map_url, venue_website_url, show_hackathon, has_satellites,
			orient_cal_notif
		FROM conferences
		ORDER BY start_date NULLS LAST, tag
	`)
	if err != nil {
		return nil, fmt.Errorf("query conferences: %w", err)
	}
	defer rows.Close()

	var confs []*types.Conf
	for rows.Next() {
		var conf types.Conf
		var publicUID *int64
		var startDate pgtype.Timestamptz
		var endDate pgtype.Timestamptz
		err := rows.Scan(
			&conf.Ref,
			&conf.Tag,
			&publicUID,
			&conf.Active,
			&conf.Desc,
			&conf.OGFlavor,
			&conf.Emoji,
			&conf.Tagline,
			&conf.DateDesc,
			&startDate,
			&endDate,
			&conf.Timezone,
			&conf.Location,
			&conf.Venue,
			&conf.VenueMap,
			&conf.VenueWebsite,
			&conf.ShowHackathon,
			&conf.HasSatellites,
			&conf.OrientCalNotif,
		)
		if err != nil {
			return nil, fmt.Errorf("scan conference: %w", err)
		}
		if publicUID != nil {
			conf.UID = uint64(*publicUID)
		}
		if startDate.Valid {
			conf.StartDate = startDate.Time
		}
		if endDate.Valid {
			conf.EndDate = endDate.Time
		}
		if conf.Timezone != "" {
			if loc, err := time.LoadLocation(conf.Timezone); err == nil {
				conf.TZ = loc
			}
		}
		confs = append(confs, &conf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conferences: %w", err)
	}
	return confs, nil
}

func listConfTicketsPostgres(ctx *config.AppContext) ([]*types.ConfTicket, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, conference_id::text, tier, local_price, btc_price, usd_price,
			expires_start, expires_end, max_count, currency, symbol, post_symbol
		FROM conference_tickets
		ORDER BY expires_start NULLS LAST, tier
	`)
	if err != nil {
		return nil, fmt.Errorf("query conference tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*types.ConfTicket
	for rows.Next() {
		var ticket types.ConfTicket
		var expiresStart pgtype.Timestamptz
		var expiresEnd pgtype.Timestamptz
		var localPrice, btcPrice, usdPrice, maxCount int64
		err := rows.Scan(
			&ticket.ID,
			&ticket.ConfRef,
			&ticket.Tier,
			&localPrice,
			&btcPrice,
			&usdPrice,
			&expiresStart,
			&expiresEnd,
			&maxCount,
			&ticket.Currency,
			&ticket.Symbol,
			&ticket.PostSymbol,
		)
		if err != nil {
			return nil, fmt.Errorf("scan conference ticket: %w", err)
		}
		ticket.Local = uint(localPrice)
		ticket.BTC = uint(btcPrice)
		ticket.USD = uint(usdPrice)
		ticket.Max = uint(maxCount)
		ticket.Expires = &types.Times{}
		if expiresStart.Valid {
			ticket.Expires.Start = expiresStart.Time
		}
		if expiresEnd.Valid {
			ticket.Expires.End = &expiresEnd.Time
		}
		tickets = append(tickets, &ticket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conference tickets: %w", err)
	}
	return tickets, nil
}
