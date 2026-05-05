// migrate-recordings ports the legacy Talks-DB `YTLink` column into the new
// RecordingsDB. One Recording row is created per Talks-DB row that had a
// non-empty YTLink, linked via the `talk` relation to the corresponding
// ConfTalk page (looked up via migrate-talks-state.json from the prior run).
//
// Idempotent via ./migrate-recordings-state.json — re-runs skip TalksDB
// rows already processed.
//
// Required config.toml additions:
//
//	[notion]
//	recordingsdb = "<page id>"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"

	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	configFile         = "config.toml"
	talksStateFile     = "migrate-talks-state.json"
	recordingStateFile = "migrate-recordings-state.json"
)

type cfgFile struct {
	Notion struct {
		Token        string `toml:"token"`
		TalksDb      string `toml:"talksdb"`
		RecordingsDb string `toml:"recordingsdb"`
	} `toml:"notion"`
}

type talksState struct {
	Completed map[string]struct {
		ConfTalkID string `json:"conf_talk_id"`
	} `json:"completed"`
}

type recState struct {
	Completed map[string]struct {
		RecordingID string `json:"recording_id"`
		YTLink      string `json:"yt_link"`
	} `json:"completed"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview without writing")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.TalksDb == "" || c.Notion.RecordingsDb == "" {
		log.Fatalf("missing notion.token / talksdb / recordingsdb in %s", configFile)
	}

	n := &types.Notion{Config: &types.NotionConfig{Token: c.Notion.Token}}
	n.Setup(c.Notion.Token)

	// Load talks-migration state → talksDbID → confTalkID.
	tsRaw, err := os.ReadFile(talksStateFile)
	if err != nil {
		log.Fatalf("read %s: %s", talksStateFile, err)
	}
	var ts talksState
	if err := json.Unmarshal(tsRaw, &ts); err != nil {
		log.Fatalf("parse %s: %s", talksStateFile, err)
	}
	confTalkByTalksID := make(map[string]string, len(ts.Completed))
	for talksID, entry := range ts.Completed {
		if entry.ConfTalkID != "" {
			confTalkByTalksID[talksID] = entry.ConfTalkID
		}
	}
	log.Printf("loaded %d (talksID → confTalkID) mappings", len(confTalkByTalksID))

	// Load YTLink + Talk Name for every Talks DB row.
	talks, err := loadTalkInfo(n.Client, c.Notion.TalksDb)
	if err != nil {
		log.Fatalf("load talks: %s", err)
	}
	withLink := 0
	for _, t := range talks {
		if t.YTLink != "" {
			withLink++
		}
	}
	log.Printf("scanned %d Talks DB rows (%d with non-empty YTLink)", len(talks), withLink)

	state := loadRecState()
	created, skippedDone, skippedNoLink, skippedNoConfTalk, failed := 0, 0, 0, 0, 0

	for talksID, t := range talks {
		if t.YTLink == "" {
			skippedNoLink++
			continue
		}
		if _, ok := state.Completed[talksID]; ok {
			skippedDone++
			continue
		}
		confTalkID, ok := confTalkByTalksID[talksID]
		if !ok {
			// Probably a Talks-DB row whose Event was a `maybe-*` placeholder
			// and got skipped during the talks migration.
			skippedNoConfTalk++
			continue
		}

		log.Printf("→ %s | confTalk=%s | %q | %s", talksID, confTalkID, t.Name, t.YTLink)
		if *dryRun {
			created++
			continue
		}

		recID, err := createRecording(n, c.Notion.RecordingsDb, confTalkID, t.Name, t.YTLink)
		if err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		state.Completed[talksID] = struct {
			RecordingID string `json:"recording_id"`
			YTLink      string `json:"yt_link"`
		}{RecordingID: recID, YTLink: t.YTLink}
		saveRecState(state)
		log.Printf("  ✓ recording=%s", recID)
		created++
	}

	log.Printf("done: created=%d skipped-done=%d skipped-no-link=%d skipped-no-conftalk=%d failed=%d total=%d",
		created, skippedDone, skippedNoLink, skippedNoConfTalk, failed, len(talks))
}

// createRecording writes a row to RecordingsDB with the talk relation set to
// confTalkID, TalkName (title) set to talkName, and YTLink set to ytLink.
// PublishAt is left empty — we don't have a source value for it.
func createRecording(n *types.Notion, recordingsDb, confTalkID, talkName, ytLink string) (string, error) {
	vals := map[string]*notion.PropertyValue{
		"talk": {
			Type: notion.PropertyRelation,
			Relation: []*notion.ObjectReference{
				{Object: notion.ObjectPage, ID: confTalkID},
			},
		},
	}
	if talkName != "" {
		vals["TalkName"] = notion.NewTitlePropertyValue(
			&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: talkName}},
		)
	}
	if ytLink != "" {
		vals["YTLink"] = notion.NewURLPropertyValue(ytLink)
	}
	page, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(recordingsDb), vals)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

type talkInfo struct {
	Name   string
	YTLink string
}

// loadTalkInfo returns a map of talksDbID → {Talk Name, YTLink} for every
// page in the Talks DB. Empty strings when fields are unset.
func loadTalkInfo(client notion.API, talksDb string) (map[string]talkInfo, error) {
	out := map[string]talkInfo{}
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(), talksDb,
			notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out[pg.ID] = talkInfo{
				Name:   titleText(pg.Properties["Talk Name"]),
				YTLink: strings.TrimSpace(pg.Properties["YTLink"].URL),
			}
		}
	}
	return out, nil
}

func titleText(p notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range p.Title {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	if sb.Len() == 0 {
		for _, t := range p.RichText {
			if t != nil && t.Text != nil {
				sb.WriteString(t.Text.Content)
			}
		}
	}
	return sb.String()
}

func loadRecState() *recState {
	state := &recState{Completed: map[string]struct {
		RecordingID string `json:"recording_id"`
		YTLink      string `json:"yt_link"`
	}{}}
	data, err := os.ReadFile(recordingStateFile)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, state); err != nil {
		log.Fatalf("corrupt state %s: %s", recordingStateFile, err)
	}
	if state.Completed == nil {
		state.Completed = map[string]struct {
			RecordingID string `json:"recording_id"`
			YTLink      string `json:"yt_link"`
		}{}
	}
	log.Printf("resuming from %s (%d already done)", recordingStateFile, len(state.Completed))
	return state
}

func saveRecState(state *recState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("WARN: marshal state: %s", err)
		return
	}
	if err := os.WriteFile(recordingStateFile, data, 0644); err != nil {
		log.Printf("WARN: write state: %s", err)
	}
}

