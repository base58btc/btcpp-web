// refresh-cards regenerates social-media card PNGs in Spaces for talks +
// speakers, sourced from ConfTalk → Proposal → SpeakerConf → Speaker.
//
// Filters (combine to AND-narrow):
//
//	-conf <tag>     Only talks where Talk.Event == tag.
//	-speaker <q>    Only talks where ANY speaker's name matches q
//	                (case-insensitive substring).
//
// With no flags, every talk in every conf is refreshed. Idempotent — already-
// generated cards short-circuit via the in-memory hash cache (loaded at
// startup) and a Spaces.Exists check.
//
// Usage:
//
//	go run ./cmd/refresh-cards                            # everything
//	go run ./cmd/refresh-cards -conf berlin26             # one conf
//	go run ./cmd/refresh-cards -speaker neigut            # one speaker
//	go run ./cmd/refresh-cards -conf berlin26 -speaker neigut
//	go run ./cmd/refresh-cards -conf berlin26 -sponsors-only -force
package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/handlers"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

const configFile = "config.toml"

type cfgFile struct {
	Port   string `toml:"port"`
	Host   string `toml:"host"`
	Notion struct {
		Token          string `toml:"token"`
		ConfsDb        string `toml:"confsdb"`
		ConfsTixDb     string `toml:"confstixdb"`
		SpeakersDb     string `toml:"speakersdb"`
		OrgDb          string `toml:"orgdb"`
		ProposalDb     string `toml:"proposaldb"`
		SpeakerConfDb  string `toml:"speakerconfdb"`
		ConfTalkDb     string `toml:"conftalkdb"`
		SponsorshipsDb string `toml:"sponsorshipsdb"`
	} `toml:"notion"`
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
}

func main() {
	confTag := flag.String("conf", "", "Restrict to talks where Talk.Event matches this conf tag")
	speakerQ := flag.String("speaker", "", "Restrict to talks containing a speaker whose name matches (case-insensitive substring)")
	orgQ := flag.String("org", "", "Restrict sponsor refresh to orgs whose name matches (case-insensitive substring). Implies sponsor-only.")
	sponsorsOnly := flag.Bool("sponsors-only", false, "Refresh only sponsor cards for -conf. Can be combined with -org and -force.")
	force := flag.Bool("force", false, "Bypass hash + Spaces.Exists short-circuits — always re-render and re-upload. Useful when a logo's bytes changed but the filename didn't.")
	activeOnly := flag.Bool("active", false, "Refresh talks/speakers/sponsors for every Active+future conf. Mutually exclusive with -conf.")
	flag.Parse()
	if *activeOnly && (*confTag != "" || *speakerQ != "" || *orgQ != "" || *sponsorsOnly) {
		log.Fatalf("-active is incompatible with -conf / -speaker / -org / -sponsors-only")
	}
	if *orgQ != "" && *confTag == "" {
		log.Fatalf("-org requires -conf")
	}
	if *sponsorsOnly && *confTag == "" {
		log.Fatalf("-sponsors-only requires -conf")
	}
	if *sponsorsOnly && *speakerQ != "" {
		log.Fatalf("-sponsors-only is incompatible with -speaker")
	}

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	for k, v := range map[string]string{
		"notion.token":         c.Notion.Token,
		"notion.confsdb":       c.Notion.ConfsDb,
		"notion.confstixdb":    c.Notion.ConfsTixDb,
		"notion.speakersdb":    c.Notion.SpeakersDb,
		"notion.proposaldb":    c.Notion.ProposalDb,
		"notion.speakerconfdb": c.Notion.SpeakerConfDb,
		"notion.conftalkdb":    c.Notion.ConfTalkDb,
	} {
		if v == "" {
			log.Fatalf("missing %s in %s", k, configFile)
		}
	}

	nc := &types.NotionConfig{
		Token:          c.Notion.Token,
		ConfsDb:        c.Notion.ConfsDb,
		ConfsTixDb:     c.Notion.ConfsTixDb,
		SpeakersDb:     c.Notion.SpeakersDb,
		OrgDb:          c.Notion.OrgDb,
		ProposalDb:     c.Notion.ProposalDb,
		SpeakerConfDb:  c.Notion.SpeakerConfDb,
		ConfTalkDb:     c.Notion.ConfTalkDb,
		SponsorshipsDb: c.Notion.SponsorshipsDb,
	}
	n := &types.Notion{Config: nc}
	n.Setup(c.Notion.Token)

	if c.Host == "" || c.Port == "" {
		log.Fatalf("missing host/port in %s — Chrome needs a reachable URL to render card templates. Run `make dev-run` first.", configFile)
	}
	appCtx := &config.AppContext{
		Env: &types.EnvConfig{
			Notion:      *nc,
			CacheTTLSec: 300,
			Host:        c.Host,
			Port:        c.Port,
			Prod:        false, // local: GetURI builds http://host:port
		},
		Notion:       n,
		InProduction: true, // skip disk cache bootstrap; doesn't affect GetURI
		Err:          log.New(os.Stderr, "ERR ", log.LstdFlags),
		Infos:        log.New(os.Stdout, "INFO ", log.LstdFlags),
	}

	// Confs cache must be warm for parseProposal/parseConfTalk to resolve
	// tag → *Conf via lookupConfByTag.
	getters.StartWorkPool(appCtx)
	defer getters.CloseWorkPool()
	getters.WaitFetch(appCtx)

	spaces.Init(types.SpacesConfig{
		Endpoint: c.Spaces.Endpoint,
		Region:   c.Spaces.Region,
		Bucket:   c.Spaces.Bucket,
		Key:      c.Spaces.Key,
		Secret:   c.Spaces.Secret,
	})
	if !spaces.IsConfigured() {
		log.Fatal("spaces is not configured (check [spaces] in config.toml)")
	}

	// Pull the existing hash index so unchanged cards short-circuit.
	handlers.PreloadCardHashes(appCtx)

	if *activeOnly {
		confs, _ := getters.FetchConfsCached(appCtx)
		var refreshed int
		for _, c := range confs {
			if c == nil || !c.Active || !c.InFuture() {
				continue
			}
			confTalks, err := getters.LoadTalksFromConfTalks(appCtx, c.Tag)
			if err != nil {
				log.Printf("load talks %s: %s — skipping", c.Tag, err)
				continue
			}
			log.Printf("→ %s: %d talks", c.Tag, len(confTalks))
			handlers.RefreshTalkCardsForceOpt(appCtx, confTalks, *force)
			handlers.RefreshSponsorCardsForConfOpt(appCtx, c, "", *force)
			refreshed++
		}
		log.Printf("refresh complete (%d active conf(s))", refreshed)
		return
	}

	// Sponsor-only mode skips the talk/speaker pass and refreshes
	// matching sponsors for the named conf.
	if *sponsorsOnly || *orgQ != "" {
		confs, _ := getters.FetchConfsCached(appCtx)
		var hit *types.Conf
		for _, c := range confs {
			if c != nil && c.Tag == *confTag {
				hit = c
				break
			}
		}
		if hit == nil {
			log.Fatalf("conf %q not found", *confTag)
		}
		handlers.RefreshSponsorCardsForConfOpt(appCtx, hit, *orgQ, *force)
		log.Println("refresh complete")
		return
	}

	talks, err := getters.LoadTalksFromConfTalks(appCtx, *confTag)
	if err != nil {
		log.Fatalf("load talks: %s", err)
	}

	filtered := filterTalks(talks, *speakerQ)
	log.Printf("loaded %d talks; %d match filters (conf=%q speaker=%q)",
		len(talks), len(filtered), *confTag, *speakerQ)

	if len(filtered) == 0 {
		log.Println("nothing to refresh.")
		return
	}

	handlers.RefreshTalkCardsForceOpt(appCtx, filtered, *force)

	// Sponsor cards aren't tied to a talk; refresh them per-conf when
	// -conf is set. Skipped on a no-flag full sweep because the
	// ambient RefreshSponsorCards (called from the running web app)
	// covers active/future confs already.
	if *confTag != "" && *speakerQ == "" {
		confs, _ := getters.FetchConfsCached(appCtx)
		for _, c := range confs {
			if c != nil && c.Tag == *confTag {
				handlers.RefreshSponsorCardsForConfOpt(appCtx, c, "", *force)
				break
			}
		}
	}

	log.Println("refresh complete")
}

// filterTalks narrows talks by speaker-name substring. Pass "" to skip.
// (Conf filtering is already handled by LoadTalksFromConfTalks via the
// confTag arg.)
func filterTalks(talks []*types.Talk, speakerQ string) []*types.Talk {
	if speakerQ == "" {
		return talks
	}
	q := strings.ToLower(strings.TrimSpace(speakerQ))
	var out []*types.Talk
	for _, t := range talks {
		for _, sp := range t.Speakers {
			if strings.Contains(strings.ToLower(sp.Name), q) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
