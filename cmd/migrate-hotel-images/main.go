// migrate-hotel-images is a one-shot CLI that copies every Hotel
// row's Notion-hosted PhotoURL onto DigitalOcean Spaces and writes
// the resulting object path back into the row's `Img` rich_text
// field.
//
// Why: Notion-hosted file URLs are time-limited presigned S3 links —
// they expire after ~1 hour, which means the conf-page hotel
// thumbnails break a few times a day. Mirroring to Spaces and
// reading from the bare-path Img field gets us stable URLs that
// serve from our own bucket.
//
// Behavior:
//   - Lists every row in HotelsDb.
//   - Skips rows where Img is already populated (idempotent).
//   - For rows with a PhotoURL Files prop, downloads the file,
//     hashes the bytes, uploads to hotels/{shortID}{ext} in Spaces
//     (idempotent via spaces.Exists), and writes the bare path
//     ("hotels/abc123.jpg") to the row's Img rich_text field.
//
// Usage:
//
//	cd /path/to/btcpp-web
//	go run ./cmd/migrate-hotel-images [-dry-run]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"btcpp-web/external/spaces"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const configFile = "config.toml"

func main() {
	dry := flag.Bool("dry-run", false, "log what would be uploaded but don't write to Spaces or Notion")
	flag.Parse()

	notionCfg, spacesCfg := loadCfg()
	n := &types.Notion{Config: notionCfg}
	n.Setup(notionCfg.Token)
	spaces.Init(spacesCfg)
	if !spaces.IsConfigured() {
		log.Fatal("spaces config missing — fill in [spaces] in config.toml")
	}

	confTags, err := loadConfTags(n, notionCfg.ConfsDb)
	if err != nil {
		log.Fatalf("list confs: %s", err)
	}
	log.Printf("loaded %d confs (ref → tag)", len(confTags))

	pages, err := listHotelPages(n, notionCfg.HotelsDb)
	if err != nil {
		log.Fatalf("list hotels: %s", err)
	}
	log.Printf("found %d hotel rows", len(pages))

	var migrated, skipped, failed int
	for _, p := range pages {
		props := p.Properties
		name := propText(props["Name"].Title)
		existingImg := propText(props["Img"].RichText)
		if existingImg != "" {
			log.Printf("skip %s — Img already set to %s", name, existingImg)
			skipped++
			continue
		}
		fileURL := fileGetURL(props["PhotoURL"].Files)
		if fileURL == "" {
			log.Printf("skip %s — no PhotoURL", name)
			skipped++
			continue
		}
		// Resolve the conf relation → conf.Tag so the Spaces key
		// lives under {conftag}/hotels/. Skip when there's no
		// linked conf — the row has nowhere to render anyway.
		var confTag string
		for _, ref := range props["conf"].Relation {
			if ref != nil && confTags[ref.ID] != "" {
				confTag = confTags[ref.ID]
				break
			}
		}
		if confTag == "" {
			log.Printf("skip %s — no conf tag (conf relation empty or unknown)", name)
			skipped++
			continue
		}

		path, err := mirrorImage(fileURL, confTag, *dry)
		if err != nil {
			log.Printf("fail %s: %s", name, err)
			failed++
			continue
		}
		if *dry {
			log.Printf("dry: would mirror %s → %s + set Img on row", name, path)
			migrated++
			continue
		}
		if err := setHotelImg(n.Client, p.ID, path); err != nil {
			log.Printf("fail %s: write Img: %s", name, err)
			failed++
			continue
		}
		log.Printf("ok %s → %s", name, path)
		migrated++
	}
	log.Printf("done: migrated=%d skipped=%d failed=%d", migrated, skipped, failed)
}

// mirrorImage downloads the Notion-hosted URL, content-hashes the
// bytes, and uploads to {confTag}/hotels/{shortID}{ext} in Spaces.
// Returns the bare object path. Idempotent on identical content via
// the shortID + spaces.Exists short-circuit.
func mirrorImage(fileURL, confTag string, dryRun bool) (string, error) {
	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	ext := extFromURL(fileURL)
	if ext == ".bin" {
		// fall back to MIME → ext when URL has no useful suffix
		if mime := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(mime, "image/") {
			ext = "." + strings.TrimPrefix(strings.SplitN(mime, ";", 2)[0], "image/")
		}
	}
	shortID := imgproc.ShortID(raw)
	key := confTag + "/hotels/" + shortID + ext
	if dryRun {
		return key, nil
	}
	if spaces.Exists(key) {
		return key, nil
	}
	contentType := resp.Header.Get("Content-Type")
	if _, err := spaces.Upload(key, raw, contentType, ""); err != nil {
		return "", fmt.Errorf("upload %s: %w", key, err)
	}
	return key, nil
}

// setHotelImg writes path to the Hotel row's Img rich_text field.
func setHotelImg(client notion.API, pageID, path string) error {
	_, err := client.UpdatePageProperties(context.Background(), pageID,
		map[string]*notion.PropertyValue{
			"Img": notion.NewRichTextPropertyValue(
				[]*notion.RichText{{
					Type: notion.RichTextText,
					Text: &notion.Text{Content: path},
				}}...),
		})
	return err
}

// loadConfTags returns a map of {conf-page-ID → conf.Tag}. Used to
// resolve the Hotel row's `Conference` relation back to a tag string
// so the Spaces key can live under the right per-conf prefix.
func loadConfTags(n *types.Notion, confsDb string) (map[string]string, error) {
	out := map[string]string{}
	cursor := ""
	hasMore := true
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			confsDb, notion.QueryDatabaseParam{StartCursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			tag := propText(p.Properties["Name"].Title)
			if tag == "" {
				continue
			}
			out[p.ID] = tag
		}
		cursor = next
		hasMore = more
	}
	return out, nil
}

func listHotelPages(n *types.Notion, hotelsDb string) ([]*notion.Page, error) {
	var out []*notion.Page
	cursor := ""
	hasMore := true
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			hotelsDb, notion.QueryDatabaseParam{StartCursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, pages...)
		cursor = next
		hasMore = more
	}
	return out, nil
}

// propText collapses a slice of Notion rich_text segments to a plain
// string — same shape Notion's title and rich_text values share.
func propText(rt []*notion.RichText) string {
	var b strings.Builder
	for _, r := range rt {
		if r != nil && r.Text != nil {
			b.WriteString(r.Text.Content)
		}
	}
	return b.String()
}

func fileGetURL(files []*notion.File) string {
	for _, f := range files {
		if f == nil {
			continue
		}
		if f.External != nil && f.External.URL != "" {
			return f.External.URL
		}
		if f.Internal != nil && f.Internal.URL != "" {
			return f.Internal.URL
		}
	}
	return ""
}

func extFromURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ".bin"
	}
	ext := strings.ToLower(filepath.Ext(parsed.Path))
	if ext == "" {
		return ".bin"
	}
	return ext
}

// ──────────────────────────────── CONFIG ──────────────────────────

type cfgFile struct {
	Notion struct {
		Token    string `toml:"token"`
		ConfsDb  string `toml:"confsdb"`
		HotelsDb string `toml:"hotelsdb"`
	} `toml:"notion"`
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
}

func loadCfg() (*types.NotionConfig, types.SpacesConfig) {
	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	mustVal := func(v, name string) {
		if v == "" {
			log.Fatalf("missing %s in %s", name, configFile)
		}
	}
	mustVal(c.Notion.Token, "notion.token")
	mustVal(c.Notion.ConfsDb, "notion.confsdb")
	mustVal(c.Notion.HotelsDb, "notion.hotelsdb")
	mustVal(c.Spaces.Bucket, "spaces.bucket")
	notionCfg := &types.NotionConfig{
		Token:    c.Notion.Token,
		ConfsDb:  c.Notion.ConfsDb,
		HotelsDb: c.Notion.HotelsDb,
	}
	spacesCfg := types.SpacesConfig{
		Endpoint: c.Spaces.Endpoint,
		Region:   c.Spaces.Region,
		Bucket:   c.Spaces.Bucket,
		Key:      c.Spaces.Key,
		Secret:   c.Spaces.Secret,
	}
	return notionCfg, spacesCfg
}
