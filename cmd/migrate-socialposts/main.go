// migrate-socialposts rewrites the `Ref` title on every social_posts row
// that embeds an old Talks-DB page ID, replacing it with the corresponding
// ConfTalk page ID. Mapping comes from migrate-talks-state.json.
//
// Examples:
//
//	{conf}-{oldTalkID}                → {conf}-{confTalkID}
//	{conf}-{oldTalkID}-{speakerID}    → {conf}-{confTalkID}-{speakerID}
//	{conf}-{sponsorID}                → unchanged (no match in migration map)
//	{conf}-{confTalkID}               → unchanged (idempotent re-run)
//
// Sponsor and already-rewritten rows are detected by absence-of-match
// against the migration map and silently skipped.
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
	configFile     = "config.toml"
	talksStateFile = "migrate-talks-state.json"
)

type cfgFile struct {
	Notion struct {
		Token          string `toml:"token"`
		SocialPostsDb  string `toml:"socialpostsdb"`
	} `toml:"notion"`
}

type talksState struct {
	Completed map[string]struct {
		ConfTalkID string `json:"conf_talk_id"`
	} `json:"completed"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview without writing")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.SocialPostsDb == "" {
		log.Fatalf("missing notion.token / socialpostsdb in %s", configFile)
	}

	n := &types.Notion{Config: &types.NotionConfig{
		Token:         c.Notion.Token,
		SocialPostsDb: c.Notion.SocialPostsDb,
	}}
	n.Setup(c.Notion.Token)

	stRaw, err := os.ReadFile(talksStateFile)
	if err != nil {
		log.Fatalf("read %s: %s", talksStateFile, err)
	}
	var st talksState
	if err := json.Unmarshal(stRaw, &st); err != nil {
		log.Fatalf("parse %s: %s", talksStateFile, err)
	}
	oldToNew := make(map[string]string, len(st.Completed))
	for oldID, entry := range st.Completed {
		if entry.ConfTalkID != "" {
			oldToNew[oldID] = entry.ConfTalkID
		}
	}
	log.Printf("loaded %d (oldTalkID → confTalkID) mappings", len(oldToNew))

	pages, err := loadAllPages(n.Client, c.Notion.SocialPostsDb)
	if err != nil {
		log.Fatalf("list social_posts: %s", err)
	}
	log.Printf("scanning %d social_posts rows", len(pages))

	rewritten, skipped, failed := 0, 0, 0
	for _, pg := range pages {
		ref := titleText(pg.Properties["Ref"])
		if ref == "" {
			skipped++
			continue
		}
		newRef := rewriteRef(ref, oldToNew)
		if newRef == ref {
			skipped++
			continue
		}

		log.Printf("→ %s | %q → %q", pg.ID, ref, newRef)
		if *dryRun {
			rewritten++
			continue
		}
		if err := updateRef(n, pg.ID, newRef); err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		rewritten++
	}

	log.Printf("done: rewritten=%d skipped=%d failed=%d total=%d",
		rewritten, skipped, failed, len(pages))
}

// rewriteRef finds the first oldTalkID in the map that appears as a
// substring of ref and swaps it for the new ConfTalk ID. Returns the
// original ref if no match.
func rewriteRef(ref string, oldToNew map[string]string) string {
	for oldID, newID := range oldToNew {
		if strings.Contains(ref, oldID) {
			return strings.Replace(ref, oldID, newID, 1)
		}
	}
	return ref
}

func updateRef(n *types.Notion, pageID, ref string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), pageID,
		map[string]*notion.PropertyValue{
			"Ref": notion.NewTitlePropertyValue(
				&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: ref}},
			),
		})
	return err
}

func loadAllPages(client notion.API, dbID string) ([]*notion.Page, error) {
	var out []*notion.Page
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
		out = append(out, pages...)
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
