// migrate-speakerconfs collapses the legacy SpeakerProposal-per-talk rows
// into one SpeakerConf per (Speaker, Conf), now that the table's `talk`
// relation has been switched to multi.
//
// For each group of >1 rows sharing (speakerID, conf-of-linked-proposal):
//
//   - Sort deterministically by page ID; first becomes canonical.
//   - Merge fields with first-nonempty:
//       strings, selects: first non-empty
//       multi-selects (Avails, OtherEvents): first non-empty list
//       booleans (FirstEvent, DinnerRSVP, Sponsor): first true
//       relation (org): first non-empty
//   - `talk` relation = union of every row's linked proposals (the whole
//     point of the rename).
//   - Update canonical with the merged values.
//   - Archive the other rows via a direct PATCH /v1/pages/{id} body
//     {"archived": true} — go-notion's UpdatePageProperties only sends
//     properties so we hit the API directly.
//
// Idempotent via ./migrate-speakerconfs-state.json. Re-running skips groups
// already processed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	configFile = "config.toml"
	stateFile  = "migrate-speakerconfs-state.json"

	notionAPI     = "https://api.notion.com/v1"
	notionVersion = "2022-06-28"
)

type cfgFile struct {
	Notion struct {
		Token             string `toml:"token"`
		ProposalDb        string `toml:"proposaldb"`
		SpeakerConfDb string `toml:"speakerconfdb"`
	} `toml:"notion"`
}

type spRow struct {
	ID          string
	SpeakerID   string
	ProposalID  string // single (pre-multi schema)
	OrgID       string
	Company     string
	OrgPhoto    string
	ComingFrom  string
	Avails      []string
	RecordOK    string
	Visa        string
	FirstEvent  bool
	DinnerRSVP  bool
	Sponsor     bool
	OtherEvents []string
}

type doneGroup struct {
	Canonical string   `json:"canonical"`
	Merged    int      `json:"merged"`
	Archived  []string `json:"archived"`
}

type runState struct {
	Completed map[string]doneGroup `json:"completed"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview without writing or archiving")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.ProposalDb == "" || c.Notion.SpeakerConfDb == "" {
		log.Fatalf("missing notion.token / proposaldb / speakerproposaldb in %s", configFile)
	}

	n := &types.Notion{Config: &types.NotionConfig{Token: c.Notion.Token}}
	n.Setup(c.Notion.Token)

	confByProposal, err := loadProposalConfs(n.Client, c.Notion.ProposalDb)
	if err != nil {
		log.Fatalf("load proposals: %s", err)
	}
	log.Printf("indexed %d proposals (with ScheduleFor → conf)", len(confByProposal))

	rows, err := loadSpeakerConfs(n.Client, c.Notion.SpeakerConfDb)
	if err != nil {
		log.Fatalf("load speaker confs: %s", err)
	}
	log.Printf("loaded %d speaker conf rows", len(rows))

	// Group by (speaker, conf-tag-of-proposal)
	groups := map[string][]*spRow{}
	missingProposal := 0
	for _, r := range rows {
		if r.SpeakerID == "" || r.ProposalID == "" {
			missingProposal++
			continue
		}
		conf, ok := confByProposal[r.ProposalID]
		if !ok || conf == "" {
			missingProposal++
			continue
		}
		key := r.SpeakerID + "\x00" + conf
		groups[key] = append(groups[key], r)
	}
	log.Printf("grouped: %d unique (speaker, conf) pairs (skipped %d rows with missing proposal/conf)", len(groups), missingProposal)

	state := loadState()
	merged, kept, failed, skippedDone := 0, 0, 0, 0

	for key, rs := range groups {
		if _, done := state.Completed[key]; done {
			skippedDone++
			continue
		}
		if len(rs) < 2 {
			kept++
			continue
		}

		sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
		canonical := rs[0]
		merger := mergeFromRows(rs)
		log.Printf("→ speaker=%s conf=%s | %d rows → canonical=%s, merging %d into talk relation",
			canonical.SpeakerID, strings.Split(key, "\x00")[1], len(rs), canonical.ID, len(merger.TalkIDs))

		if *dryRun {
			merged++
			continue
		}

		if err := updateCanonical(n.Client, canonical.ID, merger); err != nil {
			log.Printf("  FAILED canonical update %s: %s", canonical.ID, err)
			failed++
			continue
		}

		var archivedIDs []string
		archiveOK := true
		for _, dup := range rs[1:] {
			if err := archivePage(c.Notion.Token, dup.ID); err != nil {
				log.Printf("  FAILED archive %s: %s", dup.ID, err)
				archiveOK = false
				continue
			}
			archivedIDs = append(archivedIDs, dup.ID)
		}
		if !archiveOK {
			failed++
			continue
		}
		state.Completed[key] = doneGroup{
			Canonical: canonical.ID,
			Merged:    len(rs),
			Archived:  archivedIDs,
		}
		saveState(state)
		merged++
	}

	log.Printf("done: merged-groups=%d kept-singletons=%d skipped-cached=%d failed=%d total-groups=%d archived=%d",
		merged, kept, skippedDone, failed, len(groups), countArchived(state))
}

func countArchived(st *runState) int {
	n := 0
	for _, g := range st.Completed {
		n += len(g.Archived)
	}
	return n
}

// mergeOut is the merged value set we'll write onto the canonical row.
type mergeOut struct {
	TalkIDs     []string
	Company     string
	OrgPhoto    string
	OrgID       string
	ComingFrom  string
	Avails      []string
	RecordOK    string
	Visa        string
	FirstEvent  bool
	DinnerRSVP  bool
	Sponsor     bool
	OtherEvents []string
}

func mergeFromRows(rows []*spRow) mergeOut {
	out := mergeOut{}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.ProposalID != "" && !seen[r.ProposalID] {
			seen[r.ProposalID] = true
			out.TalkIDs = append(out.TalkIDs, r.ProposalID)
		}
		if out.Company == "" {
			out.Company = r.Company
		}
		if out.OrgPhoto == "" {
			out.OrgPhoto = r.OrgPhoto
		}
		if out.OrgID == "" {
			out.OrgID = r.OrgID
		}
		if out.ComingFrom == "" {
			out.ComingFrom = r.ComingFrom
		}
		if len(out.Avails) == 0 {
			out.Avails = r.Avails
		}
		if out.RecordOK == "" {
			out.RecordOK = r.RecordOK
		}
		if out.Visa == "" {
			out.Visa = r.Visa
		}
		if !out.FirstEvent {
			out.FirstEvent = r.FirstEvent
		}
		if !out.DinnerRSVP {
			out.DinnerRSVP = r.DinnerRSVP
		}
		if !out.Sponsor {
			out.Sponsor = r.Sponsor
		}
		if len(out.OtherEvents) == 0 {
			out.OtherEvents = r.OtherEvents
		}
	}
	return out
}

// updateCanonical writes merged values onto the canonical SpeakerConf row.
// Notably, `talk` becomes a multi-relation containing the union from the
// whole group.
func updateCanonical(client notion.API, pageID string, m mergeOut) error {
	props := map[string]*notion.PropertyValue{
		"talk": relationProp(m.TalkIDs),
	}
	if m.Company != "" {
		props["Company"] = richTextProp(m.Company)
	}
	if m.OrgPhoto != "" {
		props["OrgPhoto"] = richTextProp(m.OrgPhoto)
	}
	if m.OrgID != "" {
		props["org"] = relationProp([]string{m.OrgID})
	}
	if m.ComingFrom != "" {
		props["ComingFrom"] = titleProp(m.ComingFrom)
	}
	if len(m.Avails) > 0 {
		props["Avails"] = multiSelectProp(m.Avails)
	}
	if m.RecordOK != "" {
		props["RecordOK"] = selectProp(m.RecordOK)
	}
	if m.Visa != "" {
		props["Visa"] = selectProp(m.Visa)
	}
	props["FirstEvent"] = checkboxProp(m.FirstEvent)
	props["DinnerRSVP"] = checkboxProp(m.DinnerRSVP)
	props["Sponsor"] = checkboxProp(m.Sponsor)
	if len(m.OtherEvents) > 0 {
		props["OtherEvents"] = multiSelectProp(m.OtherEvents)
	}

	_, err := client.UpdatePageProperties(context.Background(), pageID, props)
	return err
}

// archivePage moves a Notion page to the trash. The go-notion lib's
// UpdatePageProperties only patches the `properties` field, so we hit
// /v1/pages/{id} directly with {"archived": true}.
func archivePage(token, pageID string) error {
	body := strings.NewReader(`{"archived": true}`)
	req, err := http.NewRequest(http.MethodPatch, notionAPI+"/pages/"+pageID, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	// Tiny pause to be polite to Notion's rate limit (3 req/sec).
	time.Sleep(350 * time.Millisecond)
	return nil
}

// loadProposalConfs returns proposalID → ScheduleFor.Tag for every Proposal.
func loadProposalConfs(client notion.API, dbID string) (map[string]string, error) {
	out := map[string]string{}
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(), dbID, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			tag := selectName(pg.Properties["ScheduleFor"])
			out[pg.ID] = tag
		}
	}
	return out, nil
}

func loadSpeakerConfs(client notion.API, dbID string) ([]*spRow, error) {
	var out []*spRow
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(), dbID, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out = append(out, &spRow{
				ID:          pg.ID,
				SpeakerID:   firstRelationID(pg.Properties["speaker"]),
				ProposalID:  firstRelationID(pg.Properties["talk"]),
				OrgID:       firstRelationID(pg.Properties["org"]),
				Company:     richText(pg.Properties["Company"]),
				OrgPhoto:    richText(pg.Properties["OrgPhoto"]),
				ComingFrom:  titleText(pg.Properties["ComingFrom"]),
				Avails:      selectListNames(pg.Properties["Avails"]),
				RecordOK:    selectName(pg.Properties["RecordOK"]),
				Visa:        selectName(pg.Properties["Visa"]),
				FirstEvent:  boolVal(pg.Properties["FirstEvent"].Checkbox),
				DinnerRSVP:  boolVal(pg.Properties["DinnerRSVP"].Checkbox),
				Sponsor:     boolVal(pg.Properties["Sponsor"].Checkbox),
				OtherEvents: selectListNames(pg.Properties["OtherEvents"]),
			})
		}
	}
	return out, nil
}

// --- shared parse / property helpers ---

func firstRelationID(p notion.PropertyValue) string {
	if len(p.Relation) == 0 || p.Relation[0] == nil {
		return ""
	}
	return p.Relation[0].ID
}

func richText(p notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range p.RichText {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	return sb.String()
}

func titleText(p notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range p.Title {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
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

func selectListNames(p notion.PropertyValue) []string {
	if p.MultiSelect == nil {
		return nil
	}
	out := make([]string, 0, len(*p.MultiSelect))
	for _, opt := range *p.MultiSelect {
		if opt != nil {
			out = append(out, opt.Name)
		}
	}
	return out
}

func boolVal(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func relationProp(ids []string) *notion.PropertyValue {
	refs := make([]*notion.ObjectReference, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		refs = append(refs, &notion.ObjectReference{Object: notion.ObjectPage, ID: id})
	}
	return &notion.PropertyValue{Type: notion.PropertyRelation, Relation: refs}
}

func richTextProp(s string) *notion.PropertyValue {
	if s == "" {
		return notion.NewRichTextPropertyValue()
	}
	return notion.NewRichTextPropertyValue(&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: s}})
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

func multiSelectProp(values []string) *notion.PropertyValue {
	opts := make([]*notion.SelectOption, len(values))
	for i, v := range values {
		opts[i] = &notion.SelectOption{Name: v}
	}
	return &notion.PropertyValue{Type: notion.PropertyMultiSelect, MultiSelect: &opts}
}

func checkboxProp(b bool) *notion.PropertyValue {
	bb := b
	return &notion.PropertyValue{Type: notion.PropertyCheckbox, Checkbox: &bb}
}

// --- state file ---

func loadState() *runState {
	st := &runState{Completed: map[string]doneGroup{}}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, st)
	if st.Completed == nil {
		st.Completed = map[string]doneGroup{}
	}
	log.Printf("resuming from %s (%d groups already done)", stateFile, len(st.Completed))
	return st
}

func saveState(st *runState) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(stateFile, data, 0644)
}
