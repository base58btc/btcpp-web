// backfill-sponsorships is a hybrid two-step CLI that migrates the
// hardcoded sponsor blocks in templates/conf/*.tmpl into Sponsorship
// rows in Notion.
//
// Mode 1: scan.
//
//	go run ./cmd/backfill-sponsorships -mode scan -out sponsors.tsv
//
// Walks every templates/conf/*.tmpl, finds the <section id="sponsors">
// block, and emits a TSV row per <a href><img></a> tile with:
//
//	conf_tag  tier_label_raw  tier_canonical  org_name  org_website  image_path  template_line
//
// `tier_canonical` is best-effort heuristic (Satoshi → Title, Finney →
// Diamond, etc.); blank when the surrounding <h3> didn't match a known
// pattern. Eyeball the file, fix anything wrong, then run mode 2.
//
// Mode 2: upload.
//
//	go run ./cmd/backfill-sponsorships -mode upload -in sponsors.tsv [-dry-run]
//
// Reads the (edited) TSV. For each row: looks up the Org by website
// (falling back to name match), refuses to insert if no Org exists,
// and otherwise calls getters.RegisterSponsorship with the conf
// relation + canonical Level. Status defaults to "Committed". Writes
// progress to stderr; safe to re-run because the upload is idempotent
// per (conf, org) — duplicates get skipped.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const configFile = "config.toml"

// canonicalTier maps the raw <h3> header text from the legacy
// templates onto our canonical Level vocabulary. Anything not in this
// map gets surfaced in the TSV with a blank tier so the human review
// step can fill it in. Match is a simple case-insensitive substring
// over the header text after emoji + decoration are stripped.
var canonicalTier = []struct {
	needle string // lowercased substring to match in the header
	tier   string
}{
	// Themed labels first (long substrings, more specific).
	// User-confirmed mapping: Satoshi=Diamond, Finney=Gold, Wuille=Silver.
	{"satoshi level", "Diamond"},
	{"finney level", "Gold"},
	{"wuille level", "Silver"},
	{"vip dinner", "Gold"}, // matches "VIP Dinner Party" + "VIP Dinner Sponsor"
	{"headline sponsor", "Diamond"},
	{"pool party", "Workshop"}, // atx25 themed = workshop tier
	{"networking partner", "Networking"},
	{"media partner", "Media"},
	{"community partner", "Community"},
	{"hackathon sponsor", "Hackathon"},
	{"workshop sponsor", "Workshop"},
	// Canonical-name fallbacks. Some confs already use these directly.
	{"title sponsor", "Title"},
	{"diamond", "Diamond"},
	{"gold", "Gold"},
	{"silver", "Silver"},
	{"bronze", "Bronze"},
	{"workshop", "Workshop"},
	{"hackathon", "Hackathon"},
	// Generic standalone "Party" header → Diamond. Order matters: must
	// come after "Pool Party" / "VIP Dinner Party" so those win first.
	{"party", "Diamond"},
}

// genericTierFallback is the catch-all for headers that didn't match
// any of the canonical/themed needles above (the bare "Sponsors"
// heading some confs use, or templates with no headers at all like
// berlin23). The user wants these displayed as the generic "Sponsors"
// section at the bottom of the page — Bronze in canonical-tier
// terms.
const (
	genericTierFallback  = "Bronze"
	genericLabelFallback = "Sponsors"
)

// tsvRow is the row shape we serialize to / read from sponsors.tsv.
type tsvRow struct {
	ConfTag       string
	TierLabelRaw  string
	TierCanonical string
	OrgName       string
	OrgWebsite    string
	ImagePath     string
	TemplateLine  int
}

const tsvHeader = "conf_tag\ttier_label_raw\ttier_canonical\torg_name\torg_website\timage_path\ttemplate_line"

func main() {
	mode := flag.String("mode", "", "scan | upload")
	out := flag.String("out", "sponsors.tsv", "scan: TSV path to write")
	in := flag.String("in", "sponsors.tsv", "upload: TSV path to read")
	dryRun := flag.Bool("dry-run", false, "upload: log what we'd write but don't hit Notion")
	flag.Parse()

	switch *mode {
	case "scan":
		runScan(*out)
	case "upload":
		runUpload(*in, *dryRun)
	default:
		log.Fatalf("usage: -mode scan|upload  (got %q)", *mode)
	}
}

// ──────────────────────────────── SCAN ────────────────────────────────

func runScan(outPath string) {
	matches, err := filepath.Glob("templates/conf/*.tmpl")
	if err != nil {
		log.Fatalf("glob: %s", err)
	}
	if len(matches) == 0 {
		log.Fatal("no templates/conf/*.tmpl found — run from the btcpp-web repo root")
	}
	sort.Strings(matches)

	f, err := os.Create(outPath)
	if err != nil {
		log.Fatalf("create %s: %s", outPath, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	fmt.Fprintln(w, tsvHeader)

	var total int
	for _, path := range matches {
		tag := strings.TrimSuffix(filepath.Base(path), ".tmpl")
		// Skip the pre-Notion templates that don't represent live confs.
		// Their sponsors aren't in our DB anyway.
		if tag == "atx22" || tag == "cdmx22" || tag == "atx23" {
			continue
		}
		rows := scanTemplate(path, tag)
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
				r.ConfTag, r.TierLabelRaw, r.TierCanonical, r.OrgName, r.OrgWebsite, r.ImagePath, r.TemplateLine)
			total++
		}
	}
	log.Printf("scan: %d sponsor rows written → %s", total, outPath)
}

// scanTemplate parses one conf template, finds its sponsors section,
// and emits one tsvRow per <a><img> tile. Tier label cycles forward
// every time we hit an <h3> inside the section.
//
// Parsing is regex-based — the templates have stable enough markup
// that a real HTML parse is overkill, and Go templates' {{ }} bits
// would confuse goquery anyway.
func scanTemplate(path, tag string) []tsvRow {
	body, err := os.ReadFile(path)
	if err != nil {
		log.Printf("read %s: %s — skipping", path, err)
		return nil
	}
	src := string(body)

	startIdx := strings.Index(src, `<section id="sponsors"`)
	if startIdx < 0 {
		return nil
	}
	endIdx := strings.Index(src[startIdx:], `</section>`)
	if endIdx < 0 {
		return nil
	}
	section := src[startIdx : startIdx+endIdx]

	// Pre-compute line offsets so we can report the original template
	// line number for each tile (handy when reviewing the TSV).
	lineOf := func(offset int) int {
		return strings.Count(src[:startIdx+offset], "\n") + 1
	}

	var (
		rows         []tsvRow
		currentLabel string
		currentTier  string
	)

	tokenRE := regexp.MustCompile(`(?s)<h3[^>]*>(.*?)</h3>|<a\s+href="([^"]+)"[^>]*>\s*<img[^>]*\salt="([^"]*)"[^>]*\ssrc="([^"]+)"|<a\s+href="([^"]+)"[^>]*>\s*<img[^>]*\ssrc="([^"]+)"[^>]*\salt="([^"]*)"`)
	for _, m := range tokenRE.FindAllStringSubmatchIndex(section, -1) {
		// h3 group: m[2]:m[3]
		if m[2] >= 0 {
			currentLabel = cleanHeader(section[m[2]:m[3]])
			currentTier = inferTier(currentLabel)
			continue
		}
		var href, alt, srcPath string
		switch {
		case m[4] >= 0:
			href = section[m[4]:m[5]]
			alt = section[m[6]:m[7]]
			srcPath = section[m[8]:m[9]]
		case m[10] >= 0:
			href = section[m[10]:m[11]]
			srcPath = section[m[12]:m[13]]
			alt = section[m[14]:m[15]]
		}
		if href == "" {
			continue
		}
		// Fall back to the generic Bronze "Sponsors" tier when we
		// couldn't infer one from the surrounding header. Keeps the
		// TSV fully populated so the upload pass doesn't skip rows
		// just because the source template had no <h3> divider.
		tier := currentTier
		label := currentLabel
		if tier == "" {
			tier = genericTierFallback
			if label == "" {
				label = genericLabelFallback
			}
		}
		rows = append(rows, tsvRow{
			ConfTag:       tag,
			TierLabelRaw:  label,
			TierCanonical: tier,
			OrgName:       strings.TrimSpace(alt),
			OrgWebsite:    strings.TrimSpace(href),
			ImagePath:     strings.TrimSpace(srcPath),
			TemplateLine:  lineOf(m[0]),
		})
	}
	return rows
}

// cleanHeader strips emoji decoration and tag noise from an <h3>
// header so it survives as something a human reviewer can read.
func cleanHeader(raw string) string {
	out := raw
	// Drop nested <span> tags.
	out = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(out, "")
	out = strings.TrimSpace(out)
	// Strip leading/trailing emoji clusters — they all look like \p{So} runs
	// in the templates. Don't be precious about edge cases; the field is
	// for human eyes.
	out = strings.Trim(out, "🔶💎🪙👙🛜📡📺🏡🛠️ ")
	return strings.TrimSpace(out)
}

func inferTier(label string) string {
	low := strings.ToLower(label)
	for _, m := range canonicalTier {
		if strings.Contains(low, m.needle) {
			return m.tier
		}
	}
	return ""
}

// ──────────────────────────────── UPLOAD ──────────────────────────────

func runUpload(inPath string, dry bool) {
	cfg := loadCfg()
	n := &types.Notion{Config: cfg}
	n.Setup(cfg.Token)

	confs, err := getters.ListConferences(n)
	if err != nil {
		log.Fatalf("list confs: %s", err)
	}
	confByTag := map[string]*types.Conf{}
	for _, c := range confs {
		if c != nil {
			confByTag[c.Tag] = c
		}
	}

	orgs, err := getters.ListOrgsNotion(n)
	if err != nil {
		log.Fatalf("list orgs: %s", err)
	}

	// Existing sponsorships keyed by (confRef, orgRef) so re-runs
	// (and overlaps with manually-entered Notion rows) are
	// idempotent. Bypass the production parseSponsorship path —
	// it walks the FetchConfsCached / FetchOrgsCached pipelines
	// which only populate via background cache jobs the CLI
	// doesn't run. Just query the Sponsorships DB raw and pull
	// the "event" + "org" relation page IDs.
	existing, npairs, err := preflightExistingPairs(n)
	if err != nil {
		log.Fatalf("preflight list sponsorships: %s", err)
	}
	log.Printf("preflight: %d existing (conf, org) pairs in Notion", npairs)

	f, err := os.Open(inPath)
	if err != nil {
		log.Fatalf("open %s: %s", inPath, err)
	}
	defer f.Close()

	r := bufio.NewReader(f)
	header, err := r.ReadString('\n')
	if err != nil {
		log.Fatalf("read header: %s", err)
	}
	if strings.TrimSpace(header) != tsvHeader {
		log.Fatalf("unexpected header — got %q", strings.TrimSpace(header))
	}

	var written, skipped, missing int
	for {
		line, err := r.ReadString('\n')
		if line == "" && err == io.EOF {
			break
		}
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) < 6 {
			continue
		}
		row := tsvRow{
			ConfTag:       fields[0],
			TierLabelRaw:  fields[1],
			TierCanonical: fields[2],
			OrgName:       fields[3],
			OrgWebsite:    fields[4],
			ImagePath:     fields[5],
		}
		if row.TierCanonical == "" {
			log.Printf("skip (no tier): %s · %s", row.ConfTag, row.OrgName)
			skipped++
			continue
		}
		conf, ok := confByTag[row.ConfTag]
		if !ok {
			log.Printf("skip (unknown conf %s): %s", row.ConfTag, row.OrgName)
			missing++
			continue
		}
		org := matchOrg(orgs, row.OrgName, row.OrgWebsite)
		if org == nil {
			log.Printf("skip (no org match for %q / %q at %s)", row.OrgName, row.OrgWebsite, row.ConfTag)
			missing++
			continue
		}
		key := conf.Ref + "|" + org.Ref
		if existing[key] {
			skipped++
			continue
		}
		existing[key] = true
		sp := &types.Sponsorship{
			Org:    org,
			Confs:  []*types.Conf{conf},
			Level:  row.TierCanonical,
			Label:  row.TierLabelRaw,
			Status: "Committed",
		}
		if dry {
			log.Printf("dry: would register %s @ %s · %s", org.Name, conf.Tag, row.TierCanonical)
			written++
			continue
		}
		if err := getters.RegisterSponsorshipNotion(n, sp); err != nil {
			log.Printf("register %s @ %s: %s", org.Name, conf.Tag, err)
			continue
		}
		log.Printf("ok: %s @ %s · %s", org.Name, conf.Tag, row.TierCanonical)
		written++
	}
	log.Printf("upload: written=%d skipped=%d missing=%d", written, skipped, missing)
}

// preflightExistingPairs queries the Sponsorships DB and returns the
// set of (confRef, orgRef) pairs already represented. Skipping the
// usual parseSponsorship code path because it depends on the conf /
// org caches that only populate via background jobs we don't run in
// a CLI process.
func preflightExistingPairs(n *types.Notion) (map[string]bool, int, error) {
	pairs := map[string]bool{}
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SponsorshipsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})
		if err != nil {
			return nil, 0, err
		}
		nextCursor = next
		hasMore = more
		for _, p := range pages {
			eventRels := p.Properties["event"].Relation
			orgRels := p.Properties["org"].Relation
			if len(eventRels) == 0 || len(orgRels) == 0 {
				continue
			}
			for _, ev := range eventRels {
				for _, og := range orgRels {
					if ev == nil || og == nil {
						continue
					}
					pairs[ev.ID+"|"+og.ID] = true
				}
			}
		}
	}
	return pairs, len(pairs), nil
}

// matchOrg picks the best Org candidate by website (host match) then
// by case-insensitive name. Returns nil when neither finds anything.
// Website match is host-only — query strings, paths, and trailing
// slashes shouldn't sink an otherwise valid hit.
func matchOrg(orgs []*types.Org, name, website string) *types.Org {
	if website != "" {
		host := hostOnly(website)
		for _, o := range orgs {
			if o == nil {
				continue
			}
			if hostOnly(o.Website) == host && host != "" {
				return o
			}
		}
	}
	if name == "" {
		return nil
	}
	low := strings.ToLower(strings.TrimSpace(name))
	for _, o := range orgs {
		if o == nil {
			continue
		}
		if strings.EqualFold(o.Name, name) {
			return o
		}
		if strings.Contains(strings.ToLower(o.Name), low) || strings.Contains(low, strings.ToLower(o.Name)) {
			return o
		}
	}
	return nil
}

func hostOnly(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// ──────────────────────────────── CONFIG ──────────────────────────────

type cfgFile struct {
	Notion struct {
		Token          string `toml:"token"`
		ConfsDb        string `toml:"confsdb"`
		ConfsTixDb     string `toml:"confstixdb"`
		OrgDb          string `toml:"orgdb"`
		SponsorshipsDb string `toml:"sponsorshipsdb"`
	} `toml:"notion"`
}

func loadCfg() *types.NotionConfig {
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
	mustVal(c.Notion.ConfsTixDb, "notion.confstixdb")
	mustVal(c.Notion.OrgDb, "notion.orgdb")
	mustVal(c.Notion.SponsorshipsDb, "notion.sponsorshipsdb")
	return &types.NotionConfig{
		Token:          c.Notion.Token,
		ConfsDb:        c.Notion.ConfsDb,
		ConfsTixDb:     c.Notion.ConfsTixDb,
		OrgDb:          c.Notion.OrgDb,
		SponsorshipsDb: c.Notion.SponsorshipsDb,
	}
}
