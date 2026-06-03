package main

import (
	"log"
	"os"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

type cfgFile struct {
	Port string `toml:"port"`
	Host string `toml:"host"`
	Notion struct {
		Token            string `toml:"token"`
		EmailDb          string `toml:"emaildb"`
		PurchasesDb      string `toml:"purchasesdb"`
		SpeakersDb       string `toml:"speakersdb"`
		ConfsDb          string `toml:"confsdb"`
		ConfsTixDb       string `toml:"confstixdb"`
		DiscountsDb      string `toml:"discountsdb"`
		NewsletterDb     string `toml:"newsletterdb"`
		MissivesDb       string `toml:"missivesdb"`
		HotelsDb         string `toml:"hotelsdb"`
		VolunteerDb      string `toml:"volunteerdb"`
		JobTypeDb        string `toml:"jobtypedb"`
		ProposalDb       string `toml:"proposaldb"`
		SpeakerConfDb    string `toml:"speakerconfdb"`
		ConfTalkDb       string `toml:"conftalkdb"`
		RecordingsDb     string `toml:"recordingsdb"`
		ConfInfoDb       string `toml:"confinfodb"`
		VolInfoDb        string `toml:"volinfodb"`
		ShiftDb          string `toml:"shiftdb"`
		OrgDb            string `toml:"orgdb"`
		SponsorshipsDb   string `toml:"sponsorshipsdb"`
		SocialPostsDb    string `toml:"socialpostsdb"`
		AffiliateUsageDb string `toml:"affiliateusagedb"`
	} `toml:"notion"`
}

func main() {
	configFile := "config.prod"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" {
		log.Fatalf("missing notion.token in %s", configFile)
	}

	nc := types.NotionConfig{
		Token:            c.Notion.Token,
		EmailDb:          c.Notion.EmailDb,
		PurchasesDb:      c.Notion.PurchasesDb,
		SpeakersDb:       c.Notion.SpeakersDb,
		ConfsDb:          c.Notion.ConfsDb,
		ConfsTixDb:       c.Notion.ConfsTixDb,
		DiscountsDb:      c.Notion.DiscountsDb,
		NewsletterDb:     c.Notion.NewsletterDb,
		MissivesDb:       c.Notion.MissivesDb,
		HotelsDb:         c.Notion.HotelsDb,
		VolunteerDb:      c.Notion.VolunteerDb,
		JobTypeDb:        c.Notion.JobTypeDb,
		ProposalDb:       c.Notion.ProposalDb,
		SpeakerConfDb:    c.Notion.SpeakerConfDb,
		ConfTalkDb:       c.Notion.ConfTalkDb,
		RecordingsDb:     c.Notion.RecordingsDb,
		ConfInfoDb:       c.Notion.ConfInfoDb,
		VolInfoDb:        c.Notion.VolInfoDb,
		ShiftDb:          c.Notion.ShiftDb,
		OrgDb:            c.Notion.OrgDb,
		SponsorshipsDb:   c.Notion.SponsorshipsDb,
		SocialPostsDb:    c.Notion.SocialPostsDb,
		AffiliateUsageDb: c.Notion.AffiliateUsageDb,
	}

	n := &types.Notion{Config: &nc}
	n.Setup(c.Notion.Token)

	// Force a fresh legacy fetch instead of bootstrapping from stale disk cache.
	if err := os.RemoveAll("_cache"); err != nil {
		log.Fatalf("clear _cache: %s", err)
	}

	appCtx := &config.AppContext{
		Env: &types.EnvConfig{
			Notion:      nc,
			CacheTTLSec: 300,
			Host:        c.Host,
			Port:        c.Port,
			Prod:        false,
		},
		Notion:       n,
		InProduction: false,
		Err:          log.New(os.Stderr, "ERR ", log.LstdFlags),
		Infos:        log.New(os.Stdout, "INFO ", log.LstdFlags),
	}

	getters.StartWorkPool(appCtx)
	defer getters.CloseWorkPool()
	getters.WaitFetch(appCtx)

	log.Printf("cache refresh complete using %s", configFile)
}
