// swap-speakerconf-title coordinates the two-step rotation that makes
// SpeakerName the SpeakerConfDb's title property and demotes ComingFrom
// to a rich_text column.
//
// The Notion API forbids changing a property's title<->non-title type,
// so the actual schema swap has to happen in the Notion UI between the
// two modes of this CLI:
//
//	# 1. Snapshot the current ComingFrom (title) values to disk.
//	go run ./cmd/swap-speakerconf-title -mode snapshot -out comingfrom.json
//
//	# 2. In the Notion UI, do this in order:
//	#    a. Rename the title property "ComingFrom" → "SpeakerName".
//	#       (the title type stays put, but it now answers to a new name)
//	#    b. Delete the old rich_text "SpeakerName" column.
//	#       (its data is already in the speakers DB; we'll repopulate)
//	#    c. Add a new rich_text column named "ComingFrom".
//
//	# 3. Repopulate: writes speaker name into the new title SpeakerName
//	#    and pours every snapshot value into the new rich_text ComingFrom.
//	go run ./cmd/swap-speakerconf-title -mode restore -in comingfrom.json [-dry-run]
//
// Restore is idempotent: rows where title-SpeakerName already equals the
// speaker's Name get skipped. -force overwrites them anyway.
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

	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const configFile = "config.toml"

type cfgFile struct {
	Notion struct {
		Token         string `toml:"token"`
		SpeakersDb    string `toml:"speakersdb"`
		SpeakerConfDb string `toml:"speakerconfdb"`
	} `toml:"notion"`
}

func main() {
	mode := flag.String("mode", "", "snapshot | restore")
	out := flag.String("out", "comingfrom.json", "snapshot: JSON path to write")
	in := flag.String("in", "comingfrom.json", "restore: JSON path to read")
	dryRun := flag.Bool("dry-run", false, "restore: log what we'd write but don't hit Notion")
	force := flag.Bool("force", false, "restore: overwrite SpeakerName even when already populated")
	flag.Parse()

	cfg := loadCfg()
	n := &types.Notion{Config: &types.NotionConfig{Token: cfg.Notion.Token}}
	n.Setup(cfg.Notion.Token)

	switch *mode {
	case "snapshot":
		runSnapshot(n.Client, cfg.Notion.SpeakerConfDb, *out)
	case "restore":
		runRestore(n.Client, cfg.Notion.SpeakerConfDb, cfg.Notion.SpeakersDb, *in, *dryRun, *force)
	default:
		log.Fatalf("usage: -mode snapshot|restore  (got %q)", *mode)
	}
}

// ──────────────────────────────── SNAPSHOT ────────────────────────────

// runSnapshot walks the SpeakerConfDb and writes a {pageID: comingFromText}
// JSON map to disk. Reads the title-typed ComingFrom values directly so we
// keep the data through the schema swap.
func runSnapshot(client notion.API, speakerConfDb, outPath string) {
	// Defend against running snapshot AFTER the UI swap was done — at
	// that point ComingFrom is rich_text, not title, and title-text
	// reads off the page would be empty / wrong.
	prop, err := lookupProperty(client, speakerConfDb, "ComingFrom")
	if err != nil {
		log.Fatalf("retrieve schema: %s", err)
	}
	if prop != "title" {
		log.Fatalf("snapshot expects ComingFrom to be the title property (got %q) — "+
			"are you running this AFTER the UI swap? You don't need to.", prop)
	}

	out := map[string]string{}
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(),
			speakerConfDb, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			log.Fatalf("query speakerconf: %s", err)
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out[pg.ID] = titleText(pg.Properties["ComingFrom"])
		}
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %s", err)
	}
	if err := os.WriteFile(outPath, body, 0644); err != nil {
		log.Fatalf("write %s: %s", outPath, err)
	}

	nonEmpty := 0
	for _, v := range out {
		if v != "" {
			nonEmpty++
		}
	}
	log.Printf("snapshot: %d rows total (%d non-empty ComingFrom) → %s", len(out), nonEmpty, outPath)
}

// ──────────────────────────────── RESTORE ─────────────────────────────

// runRestore writes the post-swap state onto every SpeakerConfDb row:
//   - title (now named SpeakerName) ← speaker.Name from SpeakersDb
//   - rich_text ComingFrom ← snapshot value from JSON
//
// Both writes happen in one UpdatePageProperties call per row so a row
// is either fully migrated or untouched.
func runRestore(client notion.API, speakerConfDb, speakersDb, inPath string, dry, force bool) {
	// Sanity-check the schema actually got swapped.
	if t, err := lookupProperty(client, speakerConfDb, "SpeakerName"); err != nil {
		log.Fatalf("retrieve schema: %s", err)
	} else if t != "title" {
		log.Fatalf("expected SpeakerName to be the title property (got %q) — "+
			"the UI swap step (rename ComingFrom→SpeakerName) doesn't look done yet.", t)
	}
	if t, err := lookupProperty(client, speakerConfDb, "ComingFrom"); err != nil {
		log.Fatalf("retrieve schema: %s", err)
	} else if t != "rich_text" {
		log.Fatalf("expected ComingFrom to be rich_text (got %q) — "+
			"the UI step (add new rich_text ComingFrom column) doesn't look done yet.", t)
	}

	body, err := os.ReadFile(inPath)
	if err != nil {
		log.Fatalf("read %s: %s", inPath, err)
	}
	snapshot := map[string]string{}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		log.Fatalf("unmarshal %s: %s", inPath, err)
	}
	log.Printf("loaded snapshot: %d rows", len(snapshot))

	nameByID, err := loadSpeakerNames(client, speakersDb)
	if err != nil {
		log.Fatalf("load speakers: %s", err)
	}
	log.Printf("loaded %d speakers", len(nameByID))

	var (
		written, skippedDone, skippedNoSpeaker, skippedNoName, missingSnapshot, failed int
	)
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(),
			speakerConfDb, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			log.Fatalf("query speakerconf: %s", err)
		}
		cur = next
		more = hm

		for _, pg := range pages {
			speakerID := firstRelationID(pg.Properties["speaker"])
			if speakerID == "" {
				skippedNoSpeaker++
				continue
			}
			name, ok := nameByID[speakerID]
			if !ok || name == "" {
				log.Printf("  no name for speaker %s on conf row %s", speakerID, pg.ID)
				skippedNoName++
				continue
			}

			currentTitle := titleText(pg.Properties["SpeakerName"])
			if currentTitle == name && !force {
				skippedDone++
				continue
			}

			comingFrom, hadSnapshot := snapshot[pg.ID]
			if !hadSnapshot {
				// Row was added after the snapshot was taken. Still
				// safe to write SpeakerName (we have it from the
				// speakers DB) — leave ComingFrom untouched.
				missingSnapshot++
			}

			if dry {
				log.Printf("  dry: %s ← title=%q comingfrom=%q", pg.ID, name, comingFrom)
				written++
				continue
			}

			props := map[string]*notion.PropertyValue{
				"SpeakerName": titleProp(name),
			}
			// Skip ComingFrom entirely when the value is empty — go-
			// notion's NewRichTextPropertyValue() with no args
			// marshals to `{"type":"rich_text"}` with no body and
			// Notion rejects it ("ComingFrom.name should be defined").
			// Leaving the property out leaves it empty, same end state.
			if hadSnapshot && comingFrom != "" {
				props["ComingFrom"] = richTextProp(comingFrom)
			}
			if _, err := client.UpdatePageProperties(context.Background(), pg.ID, props); err != nil {
				log.Printf("  FAILED %s: %s", pg.ID, err)
				failed++
				continue
			}
			written++
			// Polite rate-limit pause (Notion: 3 req/sec).
			time.Sleep(350 * time.Millisecond)
		}
	}

	log.Printf("done: written=%d skipped-already-done=%d skipped-no-speaker=%d skipped-no-name=%d missing-snapshot=%d failed=%d",
		written, skippedDone, skippedNoSpeaker, skippedNoName, missingSnapshot, failed)
}

// ──────────────────────────────── HELPERS ────────────────────────────

func loadCfg() *cfgFile {
	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.SpeakersDb == "" || c.Notion.SpeakerConfDb == "" {
		log.Fatalf("missing notion.token / speakersdb / speakerconfdb in %s", configFile)
	}
	return &c
}

func lookupProperty(client notion.API, dbID, name string) (string, error) {
	db, err := client.RetrieveDatabase(context.Background(), dbID)
	if err != nil {
		return "", err
	}
	prop, ok := db.Properties[name]
	if !ok {
		return "", fmt.Errorf("property %q not found", name)
	}
	return string(prop.Type), nil
}

func loadSpeakerNames(client notion.API, dbID string) (map[string]string, error) {
	out := map[string]string{}
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(), dbID,
			notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out[pg.ID] = titleText(pg.Properties["Name"])
		}
	}
	return out, nil
}

func firstRelationID(p notion.PropertyValue) string {
	if len(p.Relation) == 0 || p.Relation[0] == nil {
		return ""
	}
	return p.Relation[0].ID
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
