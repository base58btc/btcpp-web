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
	if err := validateConfig(env, needDB); err != nil {
		log.Fatal(err)
	}

	notion := &types.Notion{Config: &env.Notion}
	notion.Setup(env.Notion.Token)

	confs, err := getters.ListConferencesOnly(notion)
	if err != nil {
		log.Fatalf("fetch conferences from Notion: %s", err)
	}
	log.Printf("fetched %d conferences from Notion", len(confs))

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
	} else {
		if err := importConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("upserted %d conferences into Postgres", len(confs))
	}

	if opts.validate {
		if err := validateConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("validated conferences count and required tags")
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.configPath, "config", "config.toml", "config.toml path; env-only when the default file is absent")
	flag.StringVar(&opts.databaseURL, "database-url", "", "Postgres connection string; defaults to config DatabaseURL or DATABASE_URL")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "fetch and print planned imports without writing Postgres")
	flag.BoolVar(&opts.reset, "reset", false, "truncate imported tables before writing")
	flag.BoolVar(&opts.validate, "validate", false, "compare imported conference rows against Notion")
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
	return &env, nil
}

func validateConfig(env *types.EnvConfig, needDB bool) error {
	var missing []string
	if strings.TrimSpace(env.Notion.Token) == "" {
		missing = append(missing, "NOTION_TOKEN")
	}
	if strings.TrimSpace(env.Notion.ConfsDb) == "" {
		missing = append(missing, "NOTION_CONFS_DB")
	}
	if needDB && strings.TrimSpace(env.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
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

func dateString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
