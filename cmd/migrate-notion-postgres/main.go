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
	configPath   string
	databaseURL  string
	dryRun       bool
	reset        bool
	validate     bool
	skipTickets  bool
	skipSponsors bool
	skipSpeakers bool
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
	importSponsors := !opts.skipSponsors
	importSpeakers := !opts.skipSpeakers
	if err := validateConfig(env, needDB, importTickets, importSponsors, importSpeakers); err != nil {
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

	var orgs []*types.Org
	var sponsorships []*types.Sponsorship
	if importSponsors {
		orgs, err = getters.ListOrgs(notion)
		if err != nil {
			log.Fatalf("fetch organizations from Notion: %s", err)
		}
		if err := validateOrganizationKeys(orgs); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d organizations from Notion", len(orgs))

		sponsorships, err = getters.ListSponsorshipsOnly(notion)
		if err != nil {
			log.Fatalf("fetch sponsorships from Notion: %s", err)
		}
		if err := validateSponsorshipKeys(sponsorships, orgRefByRef(orgs), confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d sponsorships from Notion", len(sponsorships))
	}

	var speakers []*types.Speaker
	if importSpeakers {
		speakers, err = getters.ListSpeakers(notion)
		if err != nil {
			log.Fatalf("fetch speakers from Notion: %s", err)
		}
		if err := validateSpeakerRows(speakers); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d speakers from Notion", len(speakers))
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
		for _, org := range orgs {
			log.Printf("dry-run organization name=%q website=%q", org.Name, org.Website)
		}
		for _, sponsorship := range sponsorships {
			confTags := sponsorshipConfTags(sponsorship, confTagByRef)
			orgName := sponsorshipOrgName(sponsorship, orgRefByRef(orgs))
			log.Printf("dry-run sponsorship name=%q org=%q confs=%q level=%q status=%q vendor=%t", sponsorship.Name, orgName, strings.Join(confTags, ","), sponsorship.Level, sponsorship.Status, sponsorship.IsVendor)
		}
		for _, speaker := range speakers {
			log.Printf("dry-run speaker name=%q email=%q roles=%q", speaker.Name, speaker.Email, strings.Join(speaker.Roles, ","))
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
		if importSponsors {
			orgIDsByRef, err := importOrganizations(ctx, pool, orgs)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d organizations into Postgres", len(orgs))
			if err := importSponsorships(ctx, pool, sponsorships, orgIDsByRef, orgRefByRef(orgs), confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d sponsorships into Postgres", len(sponsorships))
		}
		if importSpeakers {
			if _, err := importSpeakersRows(ctx, pool, speakers); err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d speakers into Postgres", len(speakers))
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
		if importSponsors {
			if err := validateOrganizations(ctx, pool, orgs); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated organization count and required names")
			if err := validateSponsorships(ctx, pool, sponsorships, orgRefByRef(orgs), confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated sponsorship count and conference links")
		}
		if importSpeakers {
			if err := validateSpeakers(ctx, pool, speakers); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated speaker count and roles")
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
	flag.BoolVar(&opts.skipSponsors, "skip-sponsors", false, "skip importing organizations and sponsorships")
	flag.BoolVar(&opts.skipSpeakers, "skip-speakers", false, "skip importing speakers and speaker roles")
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
	if v := os.Getenv("NOTION_ORGS_DB"); v != "" {
		env.Notion.OrgDb = v
	}
	if v := os.Getenv("NOTION_SPONSORSHIPS_DB"); v != "" {
		env.Notion.SponsorshipsDb = v
	}
	if v := os.Getenv("NOTION_SPEAKERS_DB"); v != "" {
		env.Notion.SpeakersDb = v
	}
	return &env, nil
}

func validateConfig(env *types.EnvConfig, needDB, importTickets, importSponsors, importSpeakers bool) error {
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
	if importSponsors && strings.TrimSpace(env.Notion.OrgDb) == "" {
		missing = append(missing, "NOTION_ORGS_DB")
	}
	if importSponsors && strings.TrimSpace(env.Notion.SponsorshipsDb) == "" {
		missing = append(missing, "NOTION_SPONSORSHIPS_DB")
	}
	if importSpeakers && strings.TrimSpace(env.Notion.SpeakersDb) == "" {
		missing = append(missing, "NOTION_SPEAKERS_DB")
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
	_, err := pool.Exec(ctx, `TRUNCATE conferences, organizations, sponsorships, people CASCADE`)
	return err
}
