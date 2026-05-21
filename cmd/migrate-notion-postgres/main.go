package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

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

func resetDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE conferences CASCADE`)
	return err
}
