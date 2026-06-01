// migrate-talks ports rows from the legacy Talks DB onto the new
// Proposal / SpeakerProposal / ConfTalk model so the Talks DB can be
// retired and ConfTalk becomes the sole source of accepted talks.
//
// Behavior per Talks-DB row:
//
//   - Skip if the talk's Event tag isn't in the Confs DB.
//   - Match an existing Proposal by (lower(trim(Title)), Event tag).
//   - If matched: fill-only update Description / TalkType / Duration on
//     the existing Proposal, then for each speaker in the Talk's
//     `speakers` relation either reuse the existing SpeakerProposal or
//     create a new one (multi-speaker talks get one SP per speaker).
//   - If not matched: create a fresh Proposal + one SpeakerProposal per
//     speaker. Status is hard-coded to "Accepted".
//   - Push Talk.Recording to every linked SpeakerProposal (fill-only on
//     existing, set on new).
//   - Always create a ConfTalk linking the Proposal to the Conf, with
//     Clipart, TalkTime, Venue, SocialCard (← TalkCardURL), CalNotif.
//
// Idempotent via ./migrate-talks-state.json — re-runs skip rows already
// processed in a previous successful pass.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	configFile = "config.toml"
	stateFile  = "migrate-talks-state.json"
)

type cfgFile struct {
	Notion struct {
		Token         string `toml:"token"`
		TalksDb       string `toml:"talksdb"`
		SpeakersDb    string `toml:"speakersdb"`
		ConfsDb       string `toml:"confsdb"`
		ConfsTixDb    string `toml:"confstixdb"`
		OrgDb         string `toml:"orgdb"`
		ProposalDb    string `toml:"proposaldb"`
		SpeakerConfDb string `toml:"speakerconfdb"`
		ConfTalkDb    string `toml:"conftalkdb"`
	} `toml:"notion"`
}

type doneRow struct {
	ProposalID         string   `json:"proposal_id"`
	SpeakerProposalIDs []string `json:"speaker_proposal_ids"`
	ConfTalkID         string   `json:"conf_talk_id"`
	ProposalReused     bool     `json:"proposal_reused"`
}

type runState struct {
	Completed map[string]doneRow `json:"completed"`
}

// oldTalk is a lightweight read model for legacy Talks-DB rows. Mirrors the
// fields the migration cares about and nothing else.
type oldTalk struct {
	ID          string
	Title       string
	Description string
	TalkType    string
	Event       string
	Clipart     string
	CalNotif    string
	Recording   string
	Venue       string
	TalkCardURL string
	Sched       *types.Times
	Speakers    []*types.Speaker
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview without writing")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	for k, v := range map[string]string{
		"notion.token":         c.Notion.Token,
		"notion.talksdb":       c.Notion.TalksDb,
		"notion.speakersdb":    c.Notion.SpeakersDb,
		"notion.confsdb":       c.Notion.ConfsDb,
		"notion.confstixdb":    c.Notion.ConfsTixDb,
		"notion.proposaldb":    c.Notion.ProposalDb,
		"notion.speakerconfdb": c.Notion.SpeakerConfDb,
		"notion.conftalkdb":    c.Notion.ConfTalkDb,
	} {
		if v == "" {
			log.Fatalf("missing %s in %s", k, configFile)
		}
	}

	nc := &types.NotionConfig{
		Token:         c.Notion.Token,
		SpeakersDb:    c.Notion.SpeakersDb,
		ConfsDb:       c.Notion.ConfsDb,
		ConfsTixDb:    c.Notion.ConfsTixDb,
		OrgDb:         c.Notion.OrgDb,
		ProposalDb:    c.Notion.ProposalDb,
		SpeakerConfDb: c.Notion.SpeakerConfDb,
		ConfTalkDb:    c.Notion.ConfTalkDb,
	}
	n := &types.Notion{Config: nc}
	n.Setup(c.Notion.Token)

	// Several getters (ListProposals, parseProposal → lookupConfByTag) walk
	// the cached `confs` slice and refresh it via the worker pool when
	// stale. We need to spin that pool up and synchronously warm the cache
	// before any parser runs, otherwise FetchConfsCached deadlocks sending
	// to taskChan with no consumer.
	appCtx := &config.AppContext{
		Env: &types.EnvConfig{
			Notion:      *nc,
			CacheTTLSec: 300,
		},
		Notion:       n,
		InProduction: true, // skip the disk-cache bootstrap
		Err:          log.New(os.Stderr, "ERR ", log.LstdFlags),
		Infos:        log.New(os.Stdout, "INFO ", log.LstdFlags),
	}
	getters.StartWorkPool(appCtx)
	defer getters.CloseWorkPool()
	getters.WaitFetch(appCtx)

	confs, err := getters.ListConferences(n)
	if err != nil {
		log.Fatalf("list confs: %s", err)
	}
	validTags := make(map[string]bool, len(confs))
	for _, cf := range confs {
		validTags[cf.Tag] = true
	}
	log.Printf("loaded %d confs (tags: %d)", len(confs), len(validTags))

	speakers, err := getters.ListSpeakers(n)
	if err != nil {
		log.Fatalf("list speakers: %s", err)
	}
	speakerMap := make(map[string]*types.Speaker, len(speakers))
	for _, sp := range speakers {
		speakerMap[sp.ID] = sp
	}
	log.Printf("loaded %d speakers", len(speakers))

	talks, err := loadTalks(n, c.Notion.TalksDb, speakerMap)
	if err != nil {
		log.Fatalf("load talks: %s", err)
	}
	log.Printf("loaded %d talks", len(talks))

	proposals, err := getters.ListProposals(appCtx)
	if err != nil {
		log.Fatalf("list proposals: %s", err)
	}
	proposalMap := make(map[string]*types.Proposal, len(proposals))
	// proposalKey: "lower(title)\x00conf.Tag" → *Proposal
	proposalByKey := make(map[string]*types.Proposal, len(proposals))
	for _, p := range proposals {
		proposalMap[p.ID] = p
		if p.ScheduleFor != nil && p.ScheduleFor.Tag != "" && p.Title != "" {
			proposalByKey[normTitle(p.Title)+"\x00"+p.ScheduleFor.Tag] = p
		}
	}
	log.Printf("loaded %d existing proposals", len(proposals))

	sps, err := getters.ListSpeakerConfs(appCtx, speakerMap, proposalMap)
	if err != nil {
		log.Fatalf("list speaker confs: %s", err)
	}
	// spByKey: "proposalID\x00speakerID" → *SpeakerConf. After the rename
	// each SpeakerConf can hold multiple proposals in `talk`, so we index
	// every (proposal, speaker) pair.
	spByKey := make(map[string]*types.SpeakerConf, len(sps))
	for _, sp := range sps {
		if sp.Speaker == nil {
			continue
		}
		for _, p := range sp.Proposals {
			if p == nil {
				continue
			}
			spByKey[p.ID+"\x00"+sp.Speaker.ID] = sp
		}
	}
	log.Printf("loaded %d existing speaker confs", len(sps))

	state := loadState()

	migrated, skipped, skippedConf, failed := 0, 0, 0, 0
	for _, t := range talks {
		if _, has := state.Completed[t.ID]; has {
			skipped++
			continue
		}
		if !validTags[t.Event] {
			log.Printf("- skip %s (event %q not in confs)", t.ID, t.Event)
			skippedConf++
			continue
		}
		log.Printf("→ %s | %s | %s (speakers=%d)", t.ID, t.Event, t.Title, len(t.Speakers))
		if *dryRun {
			migrated++
			continue
		}

		res, err := migrateTalk(n, appCtx, t, proposalByKey, spByKey)
		if err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		state.Completed[t.ID] = res
		saveState(state)
		log.Printf("  ✓ proposal=%s (reused=%v) sps=%v conftalk=%s",
			res.ProposalID, res.ProposalReused, res.SpeakerProposalIDs, res.ConfTalkID)
		migrated++
	}

	log.Printf("done: migrated=%d skipped-cached=%d skipped-bad-conf=%d failed=%d total=%d",
		migrated, skipped, skippedConf, failed, len(talks))
}

func migrateTalk(n *types.Notion, ctx *config.AppContext, t *oldTalk,
	proposalByKey map[string]*types.Proposal, spByKey map[string]*types.SpeakerConf,
) (doneRow, error) {
	res := doneRow{}

	dur := durationMinutes(t.Sched)
	talkType := strings.ToLower(strings.TrimSpace(t.TalkType))

	// Find or create the Proposal.
	key := normTitle(t.Title) + "\x00" + t.Event
	existing := proposalByKey[key]
	var proposalID string

	if existing != nil {
		proposalID = existing.ID
		res.ProposalReused = true
		// Fill-only update on Proposal: Description, TalkType, Duration.
		// Status is force-set to Accepted (Talks DB rows are all accepted).
		props := map[string]*notion.PropertyValue{}
		if existing.Description == "" && t.Description != "" {
			props["Desc"] = richTextProp(t.Description)
		}
		if existing.TalkType == "" && talkType != "" {
			props["TalkType"] = selectProp(talkType)
		}
		if existing.DesiredDuration == 0 && dur > 0 {
			props["DesiredDuration"] = numberProp(float64(dur))
		}
		if existing.AvailDuration == 0 && dur > 0 {
			props["AvailDuration"] = numberProp(float64(dur))
		}
		if existing.Status != "Accepted" {
			props["Status"] = selectProp("Accepted")
		}
		if len(props) > 0 {
			if _, err := n.Client.UpdatePageProperties(context.Background(), proposalID, props); err != nil {
				return res, fmt.Errorf("update proposal %s: %w", proposalID, err)
			}
		}
	} else {
		// Create new Proposal.
		id, err := getters.CreateProposal(ctx, getters.ProposalInput{
			Title:           t.Title,
			Description:     t.Description,
			TalkType:        talkType,
			DesiredDuration: dur,
			AvailDuration:   dur,
			ScheduleForTag:  t.Event,
			Status:          "Accepted",
		})
		if err != nil {
			return res, fmt.Errorf("create proposal: %w", err)
		}
		proposalID = id
	}
	res.ProposalID = proposalID

	// One SpeakerProposal per linked Speaker. Reuse if (proposal, speaker)
	// already exists (in which case fill-only RecordOK from Talks.Recording);
	// else create with defaults.
	for _, sp := range t.Speakers {
		if sp == nil {
			continue
		}
		spKey := proposalID + "\x00" + sp.ID
		if existingSP, ok := spByKey[spKey]; ok {
			res.SpeakerProposalIDs = append(res.SpeakerProposalIDs, existingSP.ID)
			if existingSP.RecordOK == "" && t.Recording != "" {
				_, err := n.Client.UpdatePageProperties(context.Background(), existingSP.ID,
					map[string]*notion.PropertyValue{
						"RecordOK": selectProp(t.Recording),
					})
				if err != nil {
					return res, fmt.Errorf("update speaker proposal %s: %w", existingSP.ID, err)
				}
			}
			continue
		}
		newSPID, err := getters.UpsertSpeakerConf(ctx, getters.SpeakerConfInput{
			SpeakerID:  sp.ID,
			ConfTag:    t.Event,
			ProposalID: proposalID,
			ComingFrom: "unknown",
			RecordOK:   t.Recording,
		})
		if err != nil {
			return res, fmt.Errorf("upsert speaker conf for %s: %w", sp.ID, err)
		}
		res.SpeakerProposalIDs = append(res.SpeakerProposalIDs, newSPID)
	}

	// ConfTalk — always create one. The CreateConfTalk getter only writes
	// Conf + proposal; the rest (Clipart, TalkTime, Venue, SocialCard,
	// CalNotif) we patch in directly to keep the live submit-flow getter
	// minimal.
	confTalkID, err := getters.CreateConfTalk(ctx, getters.ConfTalkInput{
		ConfTag:    t.Event,
		ProposalID: proposalID,
	})
	if err != nil {
		return res, fmt.Errorf("create conf talk: %w", err)
	}
	res.ConfTalkID = confTalkID

	patch := map[string]*notion.PropertyValue{}
	if t.Clipart != "" {
		// Clipart is the title-typed property on ConfTalk.
		patch["Clipart"] = titleProp(t.Clipart)
	}
	if t.Sched != nil {
		patch["TalkTime"] = dateProp(t.Sched)
	}
	if t.Venue != "" {
		patch["Venue"] = selectProp(t.Venue)
	}
	if t.TalkCardURL != "" {
		patch["SocialCard"] = richTextProp(t.TalkCardURL)
	}
	if t.CalNotif != "" {
		patch["CalNotif"] = richTextProp(t.CalNotif)
	}
	if len(patch) > 0 {
		if _, err := n.Client.UpdatePageProperties(context.Background(), confTalkID, patch); err != nil {
			return res, fmt.Errorf("patch conf talk %s: %w", confTalkID, err)
		}
	}

	return res, nil
}

func loadTalks(n *types.Notion, talksDb string, speakerMap map[string]*types.Speaker) ([]*oldTalk, error) {
	var out []*oldTalk
	cur := ""
	more := true
	for more {
		pages, next, hm, err := n.Client.QueryDatabase(context.Background(), talksDb, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out = append(out, parseOldTalk(pg.ID, pg.Properties, speakerMap))
		}
	}
	return out, nil
}

func parseOldTalk(id string, props map[string]notion.PropertyValue, speakerMap map[string]*types.Speaker) *oldTalk {
	t := &oldTalk{
		ID:          id,
		Title:       richText(props["Talk Name"]),
		Description: richText(props["Description"]),
		Clipart:     richText(props["Clipart"]),
		CalNotif:    richText(props["CalNotif"]),
		Recording:   selectName(props["Recording"]),
		Venue:       selectName(props["Venue"]),
		Event:       selectName(props["Event"]),
		TalkType:    selectName(props["Talk Type"]),
		TalkCardURL: props["TalkCardURL"].URL,
		Sched:       parseTimes(props["Talk Time"]),
	}
	for _, ref := range props["speakers"].Relation {
		if ref == nil {
			continue
		}
		if sp, ok := speakerMap[ref.ID]; ok {
			t.Speakers = append(t.Speakers, sp)
		}
	}
	return t
}

func parseTimes(p notion.PropertyValue) *types.Times {
	if p.Date == nil {
		return nil
	}
	return &types.Times{Start: p.Date.Start, End: p.Date.End}
}

func durationMinutes(s *types.Times) int {
	if s == nil || s.End == nil {
		return 0
	}
	d := s.End.Sub(s.Start)
	if d <= 0 {
		return 0
	}
	return int(d.Round(time.Minute) / time.Minute)
}

func normTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func richText(p notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range p.RichText {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	if sb.Len() == 0 {
		for _, t := range p.Title {
			if t != nil && t.Text != nil {
				sb.WriteString(t.Text.Content)
			}
		}
	}
	return sb.String()
}

func selectName(p notion.PropertyValue) string {
	if p.Select == nil {
		return ""
	}
	return p.Select.Name
}

// Property builders (Notion write-side). Mirror getters' richTextValue /
// selectValue / numberValue / titleValue but inlined to avoid pulling
// unexported helpers cross-package.

func richTextProp(s string) *notion.PropertyValue {
	if s == "" {
		return notion.NewRichTextPropertyValue()
	}
	// Same chunking guard as getters.richTextChunks: max 2000 chars per block.
	const lim = 2000
	runes := []rune(s)
	var rts []*notion.RichText
	for len(runes) > lim {
		rts = append(rts, &notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: string(runes[:lim])}})
		runes = runes[lim:]
	}
	if len(runes) > 0 {
		rts = append(rts, &notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: string(runes)}})
	}
	return notion.NewRichTextPropertyValue(rts...)
}

func titleProp(s string) *notion.PropertyValue {
	if s == "" {
		return notion.NewTitlePropertyValue()
	}
	return notion.NewTitlePropertyValue(&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: s}})
}

func selectProp(name string) *notion.PropertyValue {
	return &notion.PropertyValue{Type: notion.PropertySelect, Select: &notion.SelectOption{Name: name}}
}

func numberProp(n float64) *notion.PropertyValue {
	return &notion.PropertyValue{Type: notion.PropertyNumber, Number: n}
}

func dateProp(s *types.Times) *notion.PropertyValue {
	d := &notion.Date{Start: s.Start}
	if s.End != nil {
		d.End = s.End
	}
	return &notion.PropertyValue{Type: notion.PropertyDate, Date: d}
}

func loadState() *runState {
	st := &runState{Completed: map[string]doneRow{}}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil {
		log.Fatalf("corrupt state %s: %s", stateFile, err)
	}
	if st.Completed == nil {
		st.Completed = map[string]doneRow{}
	}
	log.Printf("resuming from %s (%d already done)", stateFile, len(st.Completed))
	return st
}

func saveState(st *runState) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("WARN: marshal state: %s", err)
		return
	}
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		log.Printf("WARN: write state: %s", err)
	}
}
