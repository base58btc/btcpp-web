// backfill-speakername copies Speaker.Name onto every SpeakerConf row's
// new SpeakerName property.
//
//	go run ./cmd/backfill-speakername [-dry-run] [-force]
//
// At startup the CLI hits RetrieveDatabase on the SpeakerConfDb to find
// out whether SpeakerName is rich_text or title typed, then writes the
// right shape. Idempotent by default — rows with a non-empty SpeakerName
// are left alone unless -force is passed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	dryRun := flag.Bool("dry-run", false, "Log what we'd write but don't hit Notion")
	force := flag.Bool("force", false, "Overwrite SpeakerName even when already populated")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.SpeakersDb == "" || c.Notion.SpeakerConfDb == "" {
		log.Fatalf("missing notion.token / speakersdb / speakerconfdb in %s", configFile)
	}

	n := &types.Notion{Config: &types.NotionConfig{Token: c.Notion.Token}}
	n.Setup(c.Notion.Token)

	// Detect the SpeakerName property type once. If it's neither
	// rich_text nor title, bail loudly — we don't have a write shape
	// for select/etc and the user almost certainly didn't intend it.
	propType, err := detectSpeakerNameType(n.Client, c.Notion.SpeakerConfDb)
	if err != nil {
		log.Fatalf("retrieve SpeakerConfDb schema: %s", err)
	}
	if propType != "rich_text" && propType != "title" {
		log.Fatalf("SpeakerName property is type %q — only rich_text or title are supported", propType)
	}
	log.Printf("SpeakerName property type: %s", propType)

	// Build speaker page ID → Name map. One paginated query.
	nameByID, err := loadSpeakerNames(n.Client, c.Notion.SpeakersDb)
	if err != nil {
		log.Fatalf("load speakers: %s", err)
	}
	log.Printf("loaded %d speakers", len(nameByID))

	// Walk every SpeakerConf row.
	var (
		written, skippedFilled, skippedNoSpeaker, skippedNoName, failed int
	)
	cur := ""
	more := true
	for more {
		pages, next, hm, err := n.Client.QueryDatabase(context.Background(),
			c.Notion.SpeakerConfDb, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			log.Fatalf("query speakerconf: %s", err)
		}
		cur = next
		more = hm

		for _, pg := range pages {
			// Existing SpeakerName — skip if already populated, unless -force.
			currentName := readSpeakerName(pg.Properties["SpeakerName"], propType)
			if currentName != "" && !*force {
				skippedFilled++
				continue
			}
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
			if name == currentName {
				skippedFilled++
				continue
			}
			if *dryRun {
				log.Printf("  dry: %s ← %q", pg.ID, name)
				written++
				continue
			}
			if err := writeSpeakerName(n.Client, pg.ID, name, propType); err != nil {
				log.Printf("  FAILED %s: %s", pg.ID, err)
				failed++
				continue
			}
			written++
			// Polite rate-limit pause (Notion: 3 req/sec).
			time.Sleep(350 * time.Millisecond)
		}
	}

	log.Printf("done: written=%d skipped-filled=%d skipped-no-speaker=%d skipped-no-name=%d failed=%d",
		written, skippedFilled, skippedNoSpeaker, skippedNoName, failed)
}

// detectSpeakerNameType returns the Notion property type ("rich_text" /
// "title" / etc.) of the SpeakerName column on the SpeakerConfDb.
func detectSpeakerNameType(client notion.API, dbID string) (string, error) {
	db, err := client.RetrieveDatabase(context.Background(), dbID)
	if err != nil {
		return "", err
	}
	prop, ok := db.Properties["SpeakerName"]
	if !ok {
		return "", fmt.Errorf("SpeakerName column not found on SpeakerConfDb")
	}
	return string(prop.Type), nil
}

// loadSpeakerNames returns speaker page-ID → Name across the whole
// Speakers DB. Name is the Notion title property — joined across rich-
// text fragments in case the title was edited mid-page.
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

// readSpeakerName returns the current value of the SpeakerName property
// in plain text, regardless of whether it's title- or rich_text-typed.
func readSpeakerName(p notion.PropertyValue, propType string) string {
	switch propType {
	case "title":
		return titleText(p)
	case "rich_text":
		return richText(p)
	}
	return ""
}

// writeSpeakerName patches one SpeakerConf page's SpeakerName property
// with the given value, in the right shape for the column type.
func writeSpeakerName(client notion.API, pageID, name, propType string) error {
	var pv *notion.PropertyValue
	switch propType {
	case "title":
		pv = titleProp(name)
	case "rich_text":
		pv = richTextProp(name)
	default:
		return fmt.Errorf("unsupported SpeakerName type %q", propType)
	}
	_, err := client.UpdatePageProperties(context.Background(), pageID,
		map[string]*notion.PropertyValue{"SpeakerName": pv})
	return err
}

// --- shared parse / property helpers (mirrored from migrate-speakerconfs) ---

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
