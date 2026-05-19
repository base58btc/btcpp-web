package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/db"
	"btcpp-web/internal/types"
	"github.com/BurntSushi/toml"
	"github.com/jackc/pgx/v5/pgxpool"
)

type options struct {
	configPath  string
	databaseURL string
	dryRun      bool
	reset       bool
	validate    bool
	skipTickets bool
}

func main() {
	log.SetFlags(0)

	opts := parseFlags()
	ctx := context.Background()

	env, err := loadConfig(opts.configPath)
	if err != nil {
		log.Fatal(err)
	}
	if opts.databaseURL != "" {
		env.DatabaseURL = opts.databaseURL
	}
	needDB := !opts.dryRun || opts.validate
	importTickets := !opts.skipTickets
	if err := validateConfig(env, needDB, importTickets); err != nil {
		log.Fatal(err)
	}

	notion := &types.Notion{Config: &env.Notion}
	notion.Setup(env.Notion.Token)

	confs, err := getters.ListConferencesOnly(notion)
	if err != nil {
		log.Fatalf("fetch conferences from Notion: %s", err)
	}
	log.Printf("fetched %d conferences from Notion", len(confs))
	confTagByRef := conferenceTagByRef(confs)

	var tickets []*types.ConfTicket
	if importTickets {
		tickets, err = getters.ListConfTickets(notion)
		if err != nil {
			log.Fatalf("fetch conference tickets from Notion: %s", err)
		}
		if err := validateTicketKeys(tickets, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d conference tickets from Notion", len(tickets))
	}

	var pool *pgxpool.Pool
	if !opts.dryRun || opts.validate {
		pool, err = db.Open(ctx, env.DatabaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
	}

	if opts.reset && !opts.dryRun {
		if err := resetDatabase(ctx, pool); err != nil {
			log.Fatalf("reset postgres tables: %s", err)
		}
		log.Printf("reset postgres tables")
	}

	if opts.dryRun {
		for _, conf := range confs {
			log.Printf("dry-run conference tag=%q uid=%d active=%t start=%s end=%s", conf.Tag, conf.UID, conf.Active, dateString(conf.StartDate), dateString(conf.EndDate))
		}
		for _, ticket := range tickets {
			confTag := confTagByRef[ticket.ConfRef]
			log.Printf("dry-run conference-ticket conf=%q key=%q tier=%q local=%d btc=%d usd=%d max=%d", confTag, ticketKey(ticket), ticket.Tier, ticket.Local, ticket.BTC, ticket.USD, ticket.Max)
		}
	} else {
		if err := importConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("upserted %d conferences into Postgres", len(confs))
		if importTickets {
			if err := importConferenceTickets(ctx, pool, tickets, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d conference tickets into Postgres", len(tickets))
		}
	}

	if opts.validate {
		if err := validateConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("validated conferences count and required tags")
		if importTickets {
			if err := validateConferenceTickets(ctx, pool, tickets, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated conference ticket count and required tiers")
		}
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.configPath, "config", "config.toml", "config.toml path; env-only when the default file is absent")
	flag.StringVar(&opts.databaseURL, "database-url", "", "Postgres connection string; defaults to config DatabaseURL or DATABASE_URL")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "fetch and print planned imports without writing Postgres")
	flag.BoolVar(&opts.reset, "reset", false, "truncate imported tables before writing")
	flag.BoolVar(&opts.validate, "validate", false, "compare imported conference rows against Notion")
	flag.BoolVar(&opts.skipTickets, "skip-tickets", false, "skip importing conference ticket tiers")
	flag.Parse()
	return opts
}

func loadConfig(path string) (*types.EnvConfig, error) {
	var env types.EnvConfig
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &env); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) || path != "config.toml" {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}

	if v := os.Getenv("DATABASE_URL"); v != "" {
		env.DatabaseURL = v
	}
	if v := os.Getenv("NOTION_TOKEN"); v != "" {
		env.Notion.Token = v
	}
	if v := os.Getenv("NOTION_CONFS_DB"); v != "" {
		env.Notion.ConfsDb = v
	}
	if v := os.Getenv("NOTION_CONFSTIX_DB"); v != "" {
		env.Notion.ConfsTixDb = v
	}
	return &env, nil
}

func validateConfig(env *types.EnvConfig, needDB, importTickets bool) error {
	var missing []string
	if strings.TrimSpace(env.Notion.Token) == "" {
		missing = append(missing, "NOTION_TOKEN")
	}
	if strings.TrimSpace(env.Notion.ConfsDb) == "" {
		missing = append(missing, "NOTION_CONFS_DB")
	}
	if importTickets && strings.TrimSpace(env.Notion.ConfsTixDb) == "" {
		missing = append(missing, "NOTION_CONFSTIX_DB")
	}
	if needDB && strings.TrimSpace(env.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func conferenceTagByRef(confs []*types.Conf) map[string]string {
	out := make(map[string]string, len(confs))
	for _, conf := range confs {
		if conf == nil || conf.Ref == "" || conf.Tag == "" {
			continue
		}
		out[conf.Ref] = conf.Tag
	}
	return out
}

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

func resetDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE conferences CASCADE`)
	return err
}

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

func nullableUID(uid uint64) interface{} {
	if uid == 0 {
		return nil
	}
	return int64(uid)
}

func nullableDate(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullableTimesStart(times *types.Times) interface{} {
	if times == nil || times.Start.IsZero() {
		return nil
	}
	return times.Start
}

func nullableTimesEnd(times *types.Times) interface{} {
	if times == nil || times.End == nil || times.End.IsZero() {
		return nil
	}
	return *times.End
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

func dateString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
