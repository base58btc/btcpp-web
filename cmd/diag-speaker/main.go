// diag-speaker walks the dashboard's Speaker → SpeakerConf → Proposal
// lookup chain for one email and prints what it found at each step.
// Use to diagnose "I logged in but my dashboard is empty" reports —
// the output isolates which of the three usual failure modes is in
// play:
//
//  1. No Speaker row at all (admin-created Proposal with no Speakers
//     DB entry).
//  2. Multiple Speaker rows, with the proposal-linked SpeakerConf
//     pointing at the row that DOESN'T carry the login email.
//  3. One Speaker row but no SpeakerConfs reference it (rare —
//     usually means the Proposal's `speakers` relation got cleared).
//
// Usage:
//
//	go run ./cmd/diag-speaker -email hi@anita.onl
//
// Reads config.toml from the cwd; no Notion writes happen.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

type cfgFile struct {
	Notion struct {
		Token         string `toml:"token"`
		ConfsDb       string `toml:"confsdb"`
		SpeakersDb    string `toml:"speakersdb"`
		ProposalDb    string `toml:"proposaldb"`
		SpeakerConfDb string `toml:"speakerconfdb"`
		ConfTalkDb    string `toml:"conftalkdb"`
		OrgDb         string `toml:"orgdb"`
	} `toml:"notion"`
}

func main() {
	email := flag.String("email", "", "email to look up (required)")
	flag.Parse()
	if *email == "" {
		log.Fatal("required: -email")
	}

	var c cfgFile
	if _, err := toml.DecodeFile("config.toml", &c); err != nil {
		log.Fatalf("read config.toml: %s", err)
	}

	nc := &types.NotionConfig{
		Token:         c.Notion.Token,
		ConfsDb:       c.Notion.ConfsDb,
		SpeakersDb:    c.Notion.SpeakersDb,
		ProposalDb:    c.Notion.ProposalDb,
		SpeakerConfDb: c.Notion.SpeakerConfDb,
		ConfTalkDb:    c.Notion.ConfTalkDb,
		OrgDb:         c.Notion.OrgDb,
	}
	n := &types.Notion{Config: nc}
	n.Setup(c.Notion.Token)

	ctx := &config.AppContext{
		Env:    &types.EnvConfig{Notion: *nc, CacheTTLSec: 300},
		Notion: n,
		Infos:  log.New(os.Stdout, "INFO ", log.LstdFlags),
		Err:    log.New(os.Stderr, "ERR  ", log.LstdFlags),
	}

	// Step 1: Speaker pages matching the email. Live Notion query
	// (Equals: email) — case-sensitive on the Notion side.
	speakers, err := getters.GetSpeakersByEmail(n, *email)
	if err != nil {
		log.Fatalf("GetSpeakersByEmail: %s", err)
	}
	fmt.Printf("\n=== Step 1: Speaker rows in Notion matching email=%q (case-sensitive) ===\n", *email)
	fmt.Printf("Found %d row(s).\n", len(speakers))
	for i, s := range speakers {
		fmt.Printf("  [%d] id=%s name=%q email=%q photo=%q company=%q roles=%v\n",
			i, s.ID, s.Name, s.Email, s.Photo, s.Company, s.Roles)
	}
	if len(speakers) == 0 {
		fmt.Println("→ Dashboard would render empty (no Speaker = no name/photo, no SpeakerConfs, no proposals).")
		fmt.Println("→ Likely fix: check the Speakers DB for a different-case email, trailing whitespace,")
		fmt.Println("  or a different domain (e.g. @gmail.com vs @googlemail.com). Or the speaker has")
		fmt.Println("  no Speakers-DB row at all — admin created a Proposal without one.")
		return
	}

	// Step 2: Prime the proposal cache (cheap one-shot), then resolve
	// SpeakerConfs for each Speaker. This mirrors what the dashboard
	// does via GetSpeakerConfsByEmail.
	if _, err := getters.ListProposals(ctx); err != nil {
		log.Fatalf("ListProposals (cache prime): %s", err)
	}

	fmt.Printf("\n=== Step 2: SpeakerConfs per Speaker ===\n")
	totalSCs := 0
	for i, s := range speakers {
		scs := getters.FetchSpeakerConfsForSpeaker(ctx, s.ID)
		totalSCs += len(scs)
		fmt.Printf("Speaker [%d] %s (id=%s) → %d SpeakerConf(s):\n", i, s.Name, s.ID, len(scs))
		for _, sc := range scs {
			fmt.Printf("  - id=%s coming_from=%q company=%q proposals=%d\n",
				sc.ID, sc.ComingFrom, sc.Company, len(sc.Proposals))
			for _, p := range sc.Proposals {
				if p == nil {
					fmt.Println("    proposal: <nil ref>")
					continue
				}
				confTag := ""
				if p.ScheduleFor != nil {
					confTag = p.ScheduleFor.Tag
				}
				fmt.Printf("    proposal: id=%s title=%q status=%q schedule_for=%q\n",
					p.ID, p.Title, p.Status, confTag)
			}
		}
	}

	// Step 3: Cross-check — for every Proposal in the cache, list any
	// whose Speakers slice contains a SpeakerConf whose underlying
	// Speaker email matches. Catches the case where the proposal's
	// SpeakerConf points at a Speaker page that DOESN'T carry the
	// login email (mismatched relation, common when there are
	// duplicate Speakers rows).
	allProposals, _ := getters.ListProposals(ctx)
	fmt.Printf("\n=== Step 3: Proposals globally that contain a SpeakerConf for email=%q ===\n", *email)
	hits := 0
	emailLC := strings.ToLower(strings.TrimSpace(*email))
	for _, p := range allProposals {
		if p == nil {
			continue
		}
		for _, sc := range p.Speakers {
			if sc == nil || sc.Speaker == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(sc.Speaker.Email), emailLC) {
				confTag := ""
				if p.ScheduleFor != nil {
					confTag = p.ScheduleFor.Tag
				}
				fmt.Printf("  proposal id=%s title=%q status=%q schedule_for=%q\n", p.ID, p.Title, p.Status, confTag)
				fmt.Printf("    via SpeakerConf id=%s pointing at Speaker id=%s (email=%q)\n", sc.ID, sc.Speaker.ID, sc.Speaker.Email)
				hits++
			}
		}
	}
	fmt.Printf("Total proposals reachable from this email via SpeakerConf.Speaker.Email: %d\n", hits)

	fmt.Println()
	if totalSCs == 0 && hits > 0 {
		fmt.Println("→ Diagnosis: Step 2 found 0 SpeakerConfs for the email's Speaker page(s), but")
		fmt.Println("  Step 3 found proposals whose SpeakerConf.Speaker.Email matches. That means the")
		fmt.Println("  proposal's SpeakerConf is bound to a DIFFERENT Speaker page than the one(s)")
		fmt.Println("  matched in Step 1. Either merge the duplicate Speakers row, or re-point the")
		fmt.Println("  SpeakerConf's Speaker relation at the email-bearing row.")
	} else if totalSCs == 0 && hits == 0 {
		fmt.Println("→ Diagnosis: no SpeakerConf references this email anywhere. The Speaker row")
		fmt.Println("  exists but no proposal lists them. Admin created the proposal without linking")
		fmt.Println("  them, or the link was cleared.")
	} else if totalSCs > 0 {
		fmt.Println("→ Looks healthy: SpeakerConfs are linked. If the dashboard still renders empty,")
		fmt.Println("  check (a) cache staleness — hit /refresh-confs and retry, (b) Proposal.Status")
		fmt.Println("  values — Applied / InReview don't show on the dashboard talk cards.")
	}
}
