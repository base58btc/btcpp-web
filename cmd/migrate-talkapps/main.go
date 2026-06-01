// migrate-talkapps is a one-shot CLI that ports rows from the legacy TalkApp
// Notion DB onto the new Speaker / Org / Proposal / SpeakerProposal model.
//
// Behavior:
//   - Speaker is upserted by email (1 match → reuse, 0 → create, >1 → fail).
//   - Org is upserted via getters.FindOrg (by website then name); a new Org
//     is only created when the form supplied data beyond name+logo
//     (OrgSite / OrgTwitter / OrgNostr).
//   - Proposal is created with the ORIGINAL TalkApp Status preserved.
//   - SpeakerProposal links Speaker + Proposal + Org and carries Company,
//     OrgPhoto, Availability, Hometown, etc.
//   - ConfTalks are NOT created. Even Accepted proposals are migrated to
//     Status="Accepted" without a ConfTalk row — admins promote later.
//
// State is recorded in ./migrate-talkapps-state.json after each successful
// row so the tool is safe to resume after a crash. Already-migrated rows are
// skipped on re-run.
//
// Usage:
//
//	cd /path/to/btcpp-web   # config.toml must be in cwd
//	go run ./cmd/migrate-talkapps [-dry-run]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	configFile = "config.toml"
	stateFile  = "migrate-talkapps-state.json"
)

// migrateConfig captures only the [notion] section keys this tool needs.
// Field names match the lowercase TOML keys via tags.
type migrateConfig struct {
	Notion struct {
		Token         string `toml:"token"`
		ConfsDb       string `toml:"confsdb"`
		ConfsTixDb    string `toml:"confstixdb"`
		SpeakersDb    string `toml:"speakersdb"`
		OrgDb         string `toml:"orgdb"`
		TalkAppDb     string `toml:"talkappdb"`
		ProposalDb    string `toml:"proposaldb"`
		SpeakerConfDb string `toml:"speakerconfdb"`
	} `toml:"notion"`
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
}

type migrationState struct {
	Completed map[string]migrationResult `json:"completed"`
}

type migrationResult struct {
	SpeakerID         string `json:"speaker_id"`
	OrgID             string `json:"org_id,omitempty"`
	ProposalID        string `json:"proposal_id"`
	SpeakerProposalID string `json:"speaker_proposal_id"`
	OriginalStatus    string `json:"original_status"`
}

type talkApp struct {
	ID             string
	Status         string
	Email          string
	Name           string
	Phone          string
	Signal         string
	Telegram       string
	Hometown       string
	Twitter        string
	Nostr          string
	Github         string
	Website        string
	Instagram      string
	Visa           string
	NormPhoto      string
	Org            string
	OrgTwitter     string
	OrgNostr       string
	OrgSite        string // legacy alias for OrgWebsite
	OrgWebsite     string
	OrgGithub      string
	OrgLogo        string // legacy: Notion-hosted file URL
	PicURL         string // legacy speaker photo: Notion-hosted file URL
	Sponsor        bool
	TalkTitle      string
	Description    string
	PresType       string
	Recording      string
	Setup          string
	Comments       string
	Shirt          string
	Availability   []string
	ScheduleForIDs []string
	OtherEventIDs  []string
	FirstEvent     bool
	DinnerRSVP     bool
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview migration without writing any new Notion rows")
	backfillPhotos := flag.Bool("backfill-photos", false, "One-shot: scan Speakers DB for rows with empty NormPhoto, look up TalkApps by email, fill from any matching TalkApp.NormPhoto")
	backfillSPCompanies := flag.Bool("backfill-sp-companies", false, "One-shot: for SpeakerProposals with empty Company, fill from linked Speaker.Company; if Company is set and org relation is empty, link to Orgs DB row matching that name")
	flag.Parse()

	var mc migrateConfig
	if _, err := toml.DecodeFile(configFile, &mc); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	mustVal(mc.Notion.Token, "notion.token")
	mustVal(mc.Notion.ConfsDb, "notion.confsdb")
	mustVal(mc.Notion.ConfsTixDb, "notion.confstixdb")
	mustVal(mc.Notion.SpeakersDb, "notion.speakersdb")
	mustVal(mc.Notion.OrgDb, "notion.orgdb")
	mustVal(mc.Notion.TalkAppDb, "notion.talkappdb")
	mustVal(mc.Notion.ProposalDb, "notion.proposaldb")
	mustVal(mc.Notion.SpeakerConfDb, "notion.speakerconfdb")

	talkAppDB := mc.Notion.TalkAppDb
	cfg := &types.NotionConfig{
		Token:         mc.Notion.Token,
		ConfsDb:       mc.Notion.ConfsDb,
		ConfsTixDb:    mc.Notion.ConfsTixDb,
		SpeakersDb:    mc.Notion.SpeakersDb,
		OrgDb:         mc.Notion.OrgDb,
		ProposalDb:    mc.Notion.ProposalDb,
		SpeakerConfDb: mc.Notion.SpeakerConfDb,
	}
	n := &types.Notion{Config: cfg}
	n.Setup(cfg.Token)

	spaces.Init(types.SpacesConfig{
		Endpoint: mc.Spaces.Endpoint,
		Region:   mc.Spaces.Region,
		Bucket:   mc.Spaces.Bucket,
		Key:      mc.Spaces.Key,
		Secret:   mc.Spaces.Secret,
	})
	if !spaces.IsConfigured() {
		log.Fatal("spaces is not configured (check [spaces] in config.toml)")
	}

	if *backfillPhotos {
		if err := runPhotoBackfill(n, talkAppDB, *dryRun); err != nil {
			log.Fatalf("backfill: %s", err)
		}
		return
	}

	if *backfillSPCompanies {
		if err := runSPCompanyBackfill(n, *dryRun); err != nil {
			log.Fatalf("backfill: %s", err)
		}
		return
	}

	confs, err := getters.ListConferences(n)
	if err != nil {
		log.Fatalf("list confs: %s", err)
	}
	confTagByID := make(map[string]string, len(confs))
	for _, c := range confs {
		confTagByID[c.Ref] = c.Tag
	}
	log.Printf("loaded %d confs", len(confs))

	apps, err := listTalkApps(n.Client, talkAppDB)
	if err != nil {
		log.Fatalf("list talk apps: %s", err)
	}
	withNorm := 0
	for _, a := range apps {
		if a.NormPhoto != "" {
			withNorm++
		}
	}
	log.Printf("loaded %d talk apps (%d with NormPhoto, %d without)", len(apps), withNorm, len(apps)-withNorm)

	canonical, dupes := dedup(apps, confTagByID)
	log.Printf("dedup: %d canonical / %d duplicates skipped", len(canonical), len(dupes))
	for _, d := range dupes {
		log.Printf("  dup: %s | %s | %q | confs=%v", d.ID, d.Email, d.TalkTitle, confTagsOf(d.ScheduleForIDs, confTagByID))
	}

	state := loadState()
	migrated, skipped, failed := 0, 0, 0

	for _, ta := range canonical {
		if done, ok := state.Completed[ta.ID]; ok {
			// Re-run fill-only updates against the existing Speaker / Org
			// rows. Idempotent: empty fields stay empty, non-empty fields
			// aren't overwritten. Catches cases where an earlier pass
			// created the row without a field that's now in the TalkApp
			// (e.g., NormPhoto, OrgGithub).
			if err := backfillSpeakerFromTalkApp(n, ta, done.SpeakerID); err != nil {
				log.Printf("  speaker backfill %s failed: %s", ta.ID, err)
			}
			if done.OrgID != "" {
				if err := backfillOrgFromTalkApp(n, ta, done.OrgID); err != nil {
					log.Printf("  org backfill %s failed: %s", ta.ID, err)
				}
			}
			skipped++
			continue
		}
		log.Printf("→ %s | %s | status=%q | %s", ta.ID, ta.Email, ta.Status, ta.TalkTitle)
		if *dryRun {
			migrated++
			continue
		}

		res, err := migrate(n, ta, confTagByID)
		if err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		state.Completed[ta.ID] = res
		saveState(state)
		log.Printf("  ✓ speaker=%s proposal=%s sp=%s org=%s",
			res.SpeakerID, res.ProposalID, res.SpeakerProposalID, res.OrgID)
		migrated++
	}

	log.Printf("done: migrated=%d skipped=%d failed=%d canonical=%d (loaded=%d dupes=%d)",
		migrated, skipped, failed, len(canonical), len(apps), len(dupes))
}

// runSPCompanyBackfill scans every SpeakerProposal row and, for rows with an
// empty Company, fills it from the linked Speaker's Company. After that, for
// rows whose `org` relation is empty AND that have a Company (either
// pre-existing or just-backfilled), looks up the Orgs DB by name and links
// the relation if a match is found.
//
// Idempotent. Pure fill: never overwrites a non-empty Company or an existing
// org link.
func runSPCompanyBackfill(n *types.Notion, dryRun bool) error {
	speakers, err := getters.ListSpeakers(n)
	if err != nil {
		return fmt.Errorf("list speakers: %w", err)
	}
	speakerByID := make(map[string]*types.Speaker, len(speakers))
	for _, sp := range speakers {
		speakerByID[sp.ID] = sp
	}

	orgs, err := getters.ListOrgs(n)
	if err != nil {
		return fmt.Errorf("list orgs: %w", err)
	}
	orgByName := make(map[string]*types.Org, len(orgs))
	for _, o := range orgs {
		key := strings.ToLower(strings.TrimSpace(o.Name))
		if key == "" {
			continue
		}
		if _, dup := orgByName[key]; dup {
			continue // first wins
		}
		orgByName[key] = o
	}
	log.Printf("indexed %d speakers, %d orgs by name", len(speakers), len(orgByName))

	rows, err := listSpeakerProposalsRaw(n)
	if err != nil {
		return fmt.Errorf("list speaker proposals: %w", err)
	}
	log.Printf("scanning %d speaker proposals", len(rows))

	companyFilled, orgLinked, alreadyOK, missingSpeaker, failed := 0, 0, 0, 0, 0
	for _, row := range rows {
		// Determine the canonical company for this SP. Prefer existing
		// SP.Company; else fall back to Speaker.Company.
		companyToMatch := row.Company
		var fillCompany string
		if row.Company == "" && row.SpeakerID != "" {
			sp := speakerByID[row.SpeakerID]
			if sp == nil {
				missingSpeaker++
				continue
			}
			if sp.Company != "" {
				fillCompany = sp.Company
				companyToMatch = sp.Company
			}
		}

		// Org-link decision: only when SP currently has none and we have a
		// company name to match against.
		var fillOrgID string
		if row.OrgID == "" && companyToMatch != "" {
			key := strings.ToLower(strings.TrimSpace(companyToMatch))
			if org, ok := orgByName[key]; ok {
				fillOrgID = org.Ref
			}
		}

		if fillCompany == "" && fillOrgID == "" {
			alreadyOK++
			continue
		}

		props := map[string]*notion.PropertyValue{}
		if fillCompany != "" {
			props["Company"] = richTextProp(fillCompany)
		}
		if fillOrgID != "" {
			props["org"] = relationProp([]string{fillOrgID})
		}

		log.Printf("→ %s | company=%q org=%q", row.ID, fillCompany, fillOrgID)
		if dryRun {
			if fillCompany != "" {
				companyFilled++
			}
			if fillOrgID != "" {
				orgLinked++
			}
			continue
		}
		if _, err := n.Client.UpdatePageProperties(context.Background(), row.ID, props); err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		if fillCompany != "" {
			companyFilled++
		}
		if fillOrgID != "" {
			orgLinked++
		}
	}
	log.Printf("done: company-filled=%d org-linked=%d already-ok=%d missing-speaker=%d failed=%d total=%d",
		companyFilled, orgLinked, alreadyOK, missingSpeaker, failed, len(rows))
	return nil
}

type spRow struct {
	ID        string
	Company   string
	SpeakerID string
	OrgID     string
}

func listSpeakerProposalsRaw(n *types.Notion) ([]*spRow, error) {
	var out []*spRow
	cur := ""
	more := true
	for more {
		pages, next, hm, err := n.Client.QueryDatabase(context.Background(), n.Config.SpeakerConfDb, notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			out = append(out, &spRow{
				ID:        pg.ID,
				Company:   richText(pg.Properties["Company"]),
				SpeakerID: firstRelationID(pg.Properties["speaker"]),
				OrgID:     firstRelationID(pg.Properties["org"]),
			})
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

func richTextProp(content string) *notion.PropertyValue {
	if content == "" {
		return notion.NewRichTextPropertyValue()
	}
	return notion.NewRichTextPropertyValue(&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: content}})
}

func relationProp(ids []string) *notion.PropertyValue {
	refs := make([]*notion.ObjectReference, len(ids))
	for i, id := range ids {
		refs[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: id}
	}
	return &notion.PropertyValue{Type: notion.PropertyRelation, Relation: refs}
}

// photoSource captures whichever photo data a TalkApp has for an email:
// either a content-hashed NormPhoto (already in Spaces) or a raw Pic file URL
// from the legacy Notion files column (needs processing).
type photoSource struct {
	NormPhoto string // bare basename like "abc123.jpg"; "" if not set
	PicURL    string // Notion presigned URL; "" if no Pic file
}

// runPhotoBackfill scans every Speaker and, for any with an empty NormPhoto,
// looks up matching TalkApps by email. Two paths:
//
//  1. TalkApp.NormPhoto present → write avif400-form filename onto Speaker.
//  2. Else TalkApp.Pic file present → download bytes, hash, upload original
//     + 800/400 AVIF derivatives to Spaces, write the avif400 filename onto
//     Speaker. Mirrors the live mirrorPicToSpaces pipeline.
//
// Idempotent on both paths via spaces.Exists.
func runPhotoBackfill(n *types.Notion, talkAppDB string, dryRun bool) error {
	appCtx := appContextForNotion(n)
	speakers, err := getters.ListSpeakers(n)
	if err != nil {
		return fmt.Errorf("list speakers: %w", err)
	}
	apps, err := listTalkApps(n.Client, talkAppDB)
	if err != nil {
		return fmt.Errorf("list talk apps: %w", err)
	}

	// email → photoSource. First TalkApp with NormPhoto wins; otherwise we
	// take the first one with a Pic.
	srcByEmail := map[string]photoSource{}
	for _, ta := range apps {
		if ta.Email == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ta.Email))
		cur := srcByEmail[key]
		if cur.NormPhoto != "" {
			continue // best-source already recorded
		}
		if ta.NormPhoto != "" {
			cur.NormPhoto = ta.NormPhoto
		} else if cur.PicURL == "" && ta.PicURL != "" {
			cur.PicURL = ta.PicURL
		}
		srcByEmail[key] = cur
	}
	withNorm, withPic := 0, 0
	for _, s := range srcByEmail {
		if s.NormPhoto != "" {
			withNorm++
		} else if s.PicURL != "" {
			withPic++
		}
	}
	log.Printf("indexed %d emails (%d via NormPhoto, %d via Pic)", len(srcByEmail), withNorm, withPic)
	log.Printf("scanning %d speakers", len(speakers))

	filled, alreadySet, noEmail, noMatch, failed := 0, 0, 0, 0, 0
	for _, sp := range speakers {
		if sp.Photo != "" {
			alreadySet++
			continue
		}
		if sp.Email == "" {
			noEmail++
			continue
		}
		src, ok := srcByEmail[strings.ToLower(strings.TrimSpace(sp.Email))]
		if !ok || (src.NormPhoto == "" && src.PicURL == "") {
			noMatch++
			continue
		}

		var newPhoto string
		if src.NormPhoto != "" {
			newPhoto = avif400(src.NormPhoto)
			log.Printf("→ %s (%s) ← %s (NormPhoto)", sp.ID, sp.Email, newPhoto)
		} else {
			log.Printf("→ %s (%s) ← processing Pic %s", sp.ID, sp.Email, src.PicURL)
			if dryRun {
				filled++
				continue
			}
			// uploadSpeakerPicToSpaces returns the Speaker.Photo value
			// directly (avif400 form on success, original filename on
			// libaom failure).
			ph, err := uploadSpeakerPicToSpaces(src.PicURL)
			if err != nil {
				log.Printf("  FAILED: %s", err)
				failed++
				continue
			}
			newPhoto = ph
		}
		if dryRun {
			filled++
			continue
		}
		if err := getters.UpdateSpeaker(appCtx, sp.ID, getters.SpeakerUpdate{Photo: newPhoto}); err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		filled++
	}
	log.Printf("done: filled=%d already-set=%d no-email=%d no-talkapp-match=%d failed=%d total=%d",
		filled, alreadySet, noEmail, noMatch, failed, len(speakers))
	return nil
}

// uploadSpeakerPicToSpaces downloads a Notion-hosted speaker photo, content-
// hashes it, uploads the original + 400px and 800px AVIF derivatives to
// Spaces, and returns the value to write to Speaker.NormPhoto. Mirrors the
// live mirrorPicToSpaces pipeline. Idempotent via spaces.Exists.
//
// On success returns "{shortID}-400.avif" (the form Speaker.Photo points at).
// If libaom can't encode the source (some PNGs OOM), falls back to the bare
// "{shortID}{ext}" filename — the site will render the original instead.
func uploadSpeakerPicToSpaces(fileURL string) (string, error) {
	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", fileURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download %s: HTTP %d", fileURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body %s: %w", fileURL, err)
	}
	contentType := resp.Header.Get("Content-Type")
	ext := extFromURL(fileURL)
	shortID := imgproc.ShortID(raw)
	normPhoto := shortID + ext

	origKey := "speakers/" + normPhoto
	if !spaces.Exists(origKey) {
		if _, err := spaces.Upload(origKey, raw, contentType, ""); err != nil {
			return "", fmt.Errorf("upload %s: %w", origKey, err)
		}
	}
	// Try AVIF 400 first — that's what Speaker.Photo references. If it
	// fails (libaom-av1 OOMs on certain PNGs), fall back to the original
	// filename: the site renders the bigger orig instead. 800px is a nice-
	// to-have, never fatal.
	avif400OK := true
	for _, size := range []int{400, 800} {
		key := fmt.Sprintf("speakers/%s-%d.avif", shortID, size)
		if spaces.Exists(key) {
			continue
		}
		avif, err := imgproc.MakeAVIF(raw, size)
		if err != nil {
			log.Printf("    avif %d failed (non-fatal): %s", size, err)
			if size == 400 {
				avif400OK = false
			}
			continue
		}
		if _, err := spaces.Upload(key, avif, "image/avif", ""); err != nil {
			log.Printf("    upload %s failed (non-fatal): %s", key, err)
			if size == 400 {
				avif400OK = false
			}
		}
	}
	if avif400OK {
		return avif400(normPhoto), nil
	}
	log.Printf("    falling back to original (%s) as Speaker.Photo", normPhoto)
	return normPhoto, nil
}

// dedup groups TalkApps by (lowercased+trimmed Name, lowercased+trimmed
// TalkTitle, primary ScheduleFor conf tag) and picks the first one
// encountered as canonical for each group. Two apps with the same
// speaker+title but different conferences are NOT duplicates — they're
// separate applications.
//
// Rows missing Name + TalkTitle are passed through unchanged.
func dedup(apps []*talkApp, confTagByID map[string]string) (canonical, dupes []*talkApp) {
	seen := make(map[string]*talkApp)
	for _, ta := range apps {
		name := strings.ToLower(strings.TrimSpace(ta.Name))
		title := strings.ToLower(strings.TrimSpace(ta.TalkTitle))
		if name == "" && title == "" {
			canonical = append(canonical, ta)
			continue
		}
		var primaryTag string
		if len(ta.ScheduleForIDs) > 0 {
			primaryTag = confTagByID[ta.ScheduleForIDs[0]]
		}
		key := name + "\x00" + title + "\x00" + primaryTag
		if _, ok := seen[key]; ok {
			dupes = append(dupes, ta)
			continue
		}
		seen[key] = ta
		canonical = append(canonical, ta)
	}
	return
}

func confTagsOf(ids []string, confTagByID map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if tag := confTagByID[id]; tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func mustVal(v, name string) {
	if v == "" {
		log.Fatalf("missing %s in %s", name, configFile)
	}
}

func appContextForNotion(n *types.Notion) *config.AppContext {
	return &config.AppContext{
		Notion: n,
		Err:    log.New(os.Stderr, "ERR ", log.LstdFlags),
		Infos:  log.New(os.Stdout, "INFO ", log.LstdFlags),
	}
}

func migrate(n *types.Notion, ta *talkApp, confTagByID map[string]string) (migrationResult, error) {
	res := migrationResult{OriginalStatus: ta.Status}
	appCtx := appContextForNotion(n)

	if ta.Email == "" {
		return res, errors.New("no email; cannot upsert speaker")
	}
	if len(ta.ScheduleForIDs) == 0 {
		return res, errors.New("no ScheduleFor; nothing to pin proposal to")
	}
	primaryTag, ok := confTagByID[ta.ScheduleForIDs[0]]
	if !ok || primaryTag == "" {
		return res, fmt.Errorf("unknown conf %q (not in Confs DB)", ta.ScheduleForIDs[0])
	}

	// Normalize OrgSite/OrgWebsite — fall back to whichever is non-empty.
	orgWebsite := ta.OrgSite
	if orgWebsite == "" {
		orgWebsite = ta.OrgWebsite
	}

	// Speaker upsert.
	matches, err := getters.GetSpeakersByEmail(appCtx, ta.Email)
	if err != nil {
		return res, fmt.Errorf("find speakers: %w", err)
	}
	if len(matches) > 1 {
		return res, fmt.Errorf("%d speakers match email %s; resolve manually", len(matches), ta.Email)
	}
	var speakerID string
	if len(matches) == 0 {
		speakerID, err = getters.CreateSpeaker(appCtx, getters.SpeakerInput{
			Name:      ta.Name,
			Email:     ta.Email,
			Photo:     avif400(ta.NormPhoto),
			Phone:     ta.Phone,
			Signal:    ta.Signal,
			Telegram:  ta.Telegram,
			Twitter:   ta.Twitter,
			Nostr:     ta.Nostr,
			Github:    ta.Github,
			Website:   ta.Website,
			Instagram: ta.Instagram,
			TShirt:    validShirtCode(ta.Shirt),
		})
		if err != nil {
			return res, fmt.Errorf("create speaker: %w", err)
		}
	} else {
		existing := matches[0]
		speakerID = existing.ID
		// Fill-only update: backfill any blank fields on the existing
		// Speaker from this TalkApp.
		up := buildSpeakerFillUpdate(existing, ta)
		if err := getters.UpdateSpeaker(appCtx, speakerID, up); err != nil {
			return res, fmt.Errorf("update speaker %s: %w", speakerID, err)
		}
	}
	res.SpeakerID = speakerID

	// Backfill org logo first so the public URL is known before the Org
	// upsert (LogoLight wants the full Spaces URL, SpeakerProposal.OrgPhoto
	// wants the bare filename).
	orgPhotoName, err := backfillOrgLogo(ta.OrgLogo)
	if err != nil {
		return res, fmt.Errorf("backfill org logo: %w", err)
	}
	var logoURL string
	if orgPhotoName != "" {
		logoURL = spaces.PublicURL("sponsors/" + orgPhotoName)
	}

	// Org upsert. Created only when the form has data beyond name+logo
	// (one of OrgWebsite / OrgTwitter / OrgNostr / OrgGithub). For matched
	// existing Orgs, fill empty fields from this TalkApp.
	if orgWebsite != "" || ta.Org != "" {
		existing, err := getters.FindOrg(n, orgWebsite, ta.Org)
		if err != nil {
			return res, fmt.Errorf("find org: %w", err)
		}
		if existing != nil {
			res.OrgID = existing.Ref
			up := getters.OrgUpdate{}
			if existing.Website == "" {
				up.Website = orgWebsite
			}
			if existing.Twitter.Handle == "" {
				up.Twitter = ta.OrgTwitter
			}
			if existing.Nostr == "" {
				up.Nostr = ta.OrgNostr
			}
			if existing.Github == "" {
				up.Github = ta.OrgGithub
			}
			if existing.LogoLight == "" {
				up.LogoLight = logoURL
			}
			if err := getters.UpdateOrg(n, existing.Ref, up); err != nil {
				return res, fmt.Errorf("update org %s: %w", existing.Ref, err)
			}
		} else if orgWebsite != "" || ta.OrgTwitter != "" || ta.OrgNostr != "" || ta.OrgGithub != "" {
			orgID, err := getters.RegisterOrg(n, &types.Org{
				Name:      ta.Org,
				Website:   orgWebsite,
				Twitter:   types.Twitter{Handle: ta.OrgTwitter},
				Nostr:     ta.OrgNostr,
				Github:    ta.OrgGithub,
				LogoLight: logoURL,
			})
			if err != nil {
				return res, fmt.Errorf("create org: %w", err)
			}
			res.OrgID = orgID
		}
	}

	// Proposal — preserve original Status.
	dur := durationFromPresType(ta.PresType)
	proposalID, err := getters.CreateProposal(n, getters.ProposalInput{
		Title:           ta.TalkTitle,
		Description:     ta.Description,
		Setup:           ta.Setup,
		Comments:        ta.Comments,
		TalkType:        mapPresTypeToTalkType(ta.PresType),
		DesiredDuration: dur,
		AvailDuration:   dur,
		ScheduleForTag:  primaryTag,
		Status:          ta.Status,
	})
	if err != nil {
		return res, fmt.Errorf("create proposal: %w", err)
	}
	res.ProposalID = proposalID

	// ComingFrom is the title-typed property on SpeakerProposal. Notion
	// requires a non-empty title — default missing Hometown to "unknown".
	comingFrom := ta.Hometown
	if comingFrom == "" {
		comingFrom = "unknown"
	}

	// SpeakerProposal. Trailing ScheduleFor confs + OtherEvents → OtherEvents
	// multi_select on the SpeakerProposal (deduped by tag).
	otherTags := otherEventTags(ta.ScheduleForIDs[1:], ta.OtherEventIDs, confTagByID)
	recording := ta.Recording
	if strings.TrimSpace(recording) == "" {
		recording = "RecordingOK"
	}
	spID, err := getters.UpsertSpeakerConf(appCtx, getters.SpeakerConfInput{
		SpeakerID:      speakerID,
		ConfTag:        primaryTag,
		ProposalID:     proposalID,
		OrgID:          res.OrgID,
		Company:        ta.Org,
		OrgPhoto:       orgPhotoName,
		ComingFrom:     comingFrom,
		Availability:   ta.Availability,
		RecordOK:       recording,
		Visa:           ta.Visa,
		FirstEvent:     ta.FirstEvent,
		OtherEventTags: otherTags,
		DinnerRSVP:     ta.DinnerRSVP,
		Sponsor:        ta.Sponsor,
	})
	if err != nil {
		return res, fmt.Errorf("upsert speaker conf: %w", err)
	}
	res.SpeakerProposalID = spID

	return res, nil
}

// listTalkApps paginates the legacy TalkApp DB and parses each page into
// our minimal local struct. Inlines the fields the migration cares about;
// everything else is dropped.
func listTalkApps(client notion.API, dbID string) ([]*talkApp, error) {
	var out []*talkApp
	hasMore := true
	cursor := ""
	for hasMore {
		pages, next, more, err := client.QueryDatabase(context.Background(), dbID,
			notion.QueryDatabaseParam{StartCursor: cursor})
		if err != nil {
			return nil, err
		}
		cursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseTalkApp(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseTalkApp(id string, props map[string]notion.PropertyValue) *talkApp {
	return &talkApp{
		ID:             id,
		Status:         selectName(props["Status"]),
		Email:          props["Email"].Email,
		Name:           richText(props["Name"]),
		Phone:          props["Phone"].PhoneNumber,
		Signal:         richText(props["Signal"]),
		Telegram:       richText(props["Telegram"]),
		Hometown:       richText(props["Hometown"]),
		Twitter:        richText(props["Twitter"]),
		Nostr:          richText(props["npub"]),
		Github:         props["Github"].URL,
		Website:        props["Website"].URL,
		Instagram:      richText(props["Instagram"]),
		Visa:           selectName(props["Visa"]),
		NormPhoto:      richText(props["NormPhoto"]),
		Org:            richText(props["Org"]),
		OrgTwitter:     richText(props["OrgTwitter"]),
		OrgNostr:       richText(props["OrgNostr"]),
		OrgSite:        props["OrgSite"].URL,
		OrgWebsite:     props["OrgWebsite"].URL,
		OrgGithub:      props["OrgGithub"].URL,
		Sponsor:        boolVal(props["Sponsor"].Checkbox),
		TalkTitle:      richText(props["TalkTitle"]),
		Description:    richText(props["Description"]),
		PresType:       selectName(props["PresType"]),
		Recording:      selectName(props["Recording"]),
		Setup:          richText(props["Setup"]),
		Comments:       richText(props["Comments"]),
		Shirt:          selectName(props["Shirt"]),
		Availability:   selectListNames(props["Avails"]),
		ScheduleForIDs: relationIDs(props["ScheduleFor"]),
		OtherEventIDs:  relationIDs(props["OtherEvents"]),
		FirstEvent:     boolVal(props["FirstEvent"].Checkbox),
		DinnerRSVP:     boolVal(props["DinnerRSVP"].Checkbox),
		// OrgLogo on the legacy TalkApp DB is a Notion files column, not
		// rich_text. Capture the URL here; the migration fetches the bytes
		// and re-uploads to sponsors/{shortID}{ext} in Spaces.
		OrgLogo: notionFileURL(props["OrgLogo"].Files),
		// Pic is the legacy speaker-photo column (Notion files type),
		// captured here so backfill-photos can recover when NormPhoto is
		// blank. Re-uploaded to speakers/{shortID}{ext} + AVIF derivatives.
		PicURL: notionFileURL(props["Pic"].Files),
	}
}

func notionFileURL(files []*notion.File) string {
	if len(files) == 0 || files[0] == nil {
		return ""
	}
	if files[0].Internal != nil {
		return files[0].Internal.URL
	}
	if files[0].External != nil {
		return files[0].External.URL
	}
	return ""
}

func richText(pv notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range pv.RichText {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	if sb.Len() == 0 {
		for _, t := range pv.Title {
			if t != nil && t.Text != nil {
				sb.WriteString(t.Text.Content)
			}
		}
	}
	return sb.String()
}

func selectName(pv notion.PropertyValue) string {
	if pv.Select == nil {
		return ""
	}
	return pv.Select.Name
}

func selectListNames(pv notion.PropertyValue) []string {
	if pv.MultiSelect == nil {
		return nil
	}
	out := make([]string, 0, len(*pv.MultiSelect))
	for _, opt := range *pv.MultiSelect {
		if opt != nil {
			out = append(out, opt.Name)
		}
	}
	return out
}

func relationIDs(pv notion.PropertyValue) []string {
	if len(pv.Relation) == 0 {
		return nil
	}
	out := make([]string, 0, len(pv.Relation))
	for _, r := range pv.Relation {
		if r != nil && r.ID != "" {
			out = append(out, r.ID)
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

// otherEventTags merges the trailing ScheduleFor confs with OtherEvents into
// a deduped tag list (resolving page IDs through confTagByID). Skips IDs
// that aren't recognized.
func otherEventTags(rest, others []string, confTagByID map[string]string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(ids []string) {
		for _, id := range ids {
			tag := confTagByID[id]
			if tag == "" {
				continue
			}
			if _, dup := seen[tag]; dup {
				continue
			}
			seen[tag] = struct{}{}
			out = append(out, tag)
		}
	}
	add(rest)
	add(others)
	return out
}

// backfillOrgFromTalkApp loads an already-migrated Org and applies a fill-only
// update from the TalkApp's data, mirroring the create-time logic. Handles
// the legacy OrgLogo file: downloads it, re-uploads to Spaces if missing,
// and writes the Spaces public URL onto Org.LogoLight when blank.
func backfillOrgFromTalkApp(n *types.Notion, ta *talkApp, orgID string) error {
	org, err := getters.GetOrg(n, orgID)
	if err != nil {
		return fmt.Errorf("load org %s: %w", orgID, err)
	}

	orgWebsite := ta.OrgSite
	if orgWebsite == "" {
		orgWebsite = ta.OrgWebsite
	}

	var logoURL string
	if ta.OrgLogo != "" {
		bareName, err := backfillOrgLogo(ta.OrgLogo)
		if err != nil {
			return fmt.Errorf("backfill org logo: %w", err)
		}
		if bareName != "" {
			logoURL = spaces.PublicURL("sponsors/" + bareName)
		}
	}

	up := getters.OrgUpdate{}
	if org.Website == "" && orgWebsite != "" {
		up.Website = orgWebsite
	}
	if org.Twitter.Handle == "" && ta.OrgTwitter != "" {
		up.Twitter = ta.OrgTwitter
	}
	if org.Nostr == "" && ta.OrgNostr != "" {
		up.Nostr = ta.OrgNostr
	}
	if org.Github == "" && ta.OrgGithub != "" {
		up.Github = ta.OrgGithub
	}
	if org.LogoLight == "" && logoURL != "" {
		up.LogoLight = logoURL
	}
	return getters.UpdateOrg(n, orgID, up)
}

// backfillSpeakerFromTalkApp loads an already-migrated Speaker and applies a
// fill-only update from the TalkApp's data. No-op when nothing's blank.
func backfillSpeakerFromTalkApp(n *types.Notion, ta *talkApp, speakerID string) error {
	appCtx := appContextForNotion(n)
	sp, err := loadSpeakerByID(n, speakerID)
	if err != nil {
		return fmt.Errorf("load speaker %s: %w", speakerID, err)
	}
	up := buildSpeakerFillUpdate(sp, ta)
	if err := getters.UpdateSpeaker(appCtx, speakerID, up); err != nil {
		return fmt.Errorf("update speaker %s: %w", speakerID, err)
	}
	return nil
}

// loadSpeakerByID reads a Speakers DB page and returns the in-memory shape
// the fill-update logic needs. Mirrors parseSpeaker just enough for our
// purposes — we don't have access to the unexported parser.
func loadSpeakerByID(n *types.Notion, speakerID string) (*types.Speaker, error) {
	page, err := n.Client.RetrievePage(context.Background(), speakerID)
	if err != nil {
		return nil, err
	}
	props := page.Properties
	return &types.Speaker{
		ID:        page.ID,
		Photo:     richText(props["NormPhoto"]),
		Phone:     richText(props["Phone"]),
		Signal:    richText(props["Signal"]),
		Telegram:  richText(props["Telegram"]),
		Twitter:   types.Twitter{Handle: richText(props["Twitter"])},
		Nostr:     richText(props["npub"]),
		Github:    props["Github"].URL,
		Website:   props["Website"].URL,
		Instagram: richText(props["Instagram"]),
		TShirt:    selectName(props["TShirt"]),
	}, nil
}

// buildSpeakerFillUpdate returns a fill-only SpeakerUpdate for an existing
// Speaker, copying TalkApp values into any field that's currently blank.
// Mirrors live submit's buildSpeakerUpdateFromForm semantics.
func buildSpeakerFillUpdate(sp *types.Speaker, ta *talkApp) getters.SpeakerUpdate {
	up := getters.SpeakerUpdate{}
	if sp.Photo == "" && ta.NormPhoto != "" {
		up.Photo = avif400(ta.NormPhoto)
	}
	if sp.Phone == "" && ta.Phone != "" {
		up.Phone = ta.Phone
	}
	if sp.Signal == "" && ta.Signal != "" {
		up.Signal = ta.Signal
	}
	if sp.Telegram == "" && ta.Telegram != "" {
		up.Telegram = ta.Telegram
	}
	if sp.Twitter.Handle == "" && ta.Twitter != "" {
		up.Twitter = ta.Twitter
	}
	if sp.Nostr == "" && ta.Nostr != "" {
		up.Nostr = ta.Nostr
	}
	if sp.Github == "" && ta.Github != "" {
		up.Github = ta.Github
	}
	if sp.Website == "" && ta.Website != "" {
		up.Website = ta.Website
	}
	if sp.Instagram == "" && ta.Instagram != "" {
		up.Instagram = ta.Instagram
	}
	if sp.TShirt == "" {
		if v := validShirtCode(ta.Shirt); v != "" {
			up.TShirt = v
		}
	}
	return up
}

// backfillOrgLogo downloads a Notion-hosted file (presigned S3 URL),
// content-hashes the bytes, uploads to sponsors/{shortID}{ext} in Spaces if
// not already there, and returns the bare filename to store on
// SpeakerProposal.OrgPhoto. Returns "" when fileURL is empty.
//
// Idempotent: repeated runs short-circuit via spaces.Exists. Safe to call
// for already-migrated rows because identical bytes hash to the same key.
func backfillOrgLogo(fileURL string) (string, error) {
	if fileURL == "" {
		return "", nil
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", fileURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download %s: HTTP %d", fileURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body %s: %w", fileURL, err)
	}

	ext := extFromURL(fileURL)
	shortID := imgproc.ShortID(raw)
	name := shortID + ext
	key := "sponsors/" + name

	if spaces.Exists(key) {
		return name, nil
	}
	contentType := resp.Header.Get("Content-Type")
	if _, err := spaces.Upload(key, raw, contentType, ""); err != nil {
		return "", fmt.Errorf("upload %s: %w", key, err)
	}
	return name, nil
}

// extFromURL pulls the file extension off a presigned URL's path. Returns
// ".bin" if no extension is found so the file still has a sensible suffix.
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

// avif400 mirrors handlers.avif400Name — the 400px AVIF derivative is what
// we surface on the Speakers DB.
func avif400(normPhoto string) string {
	if normPhoto == "" {
		return ""
	}
	dot := strings.LastIndex(normPhoto, ".")
	if dot < 0 {
		return ""
	}
	return normPhoto[:dot] + "-400.avif"
}

func mapPresTypeToTalkType(presType string) string {
	switch {
	case strings.Contains(presType, "talk"):
		return "talk"
	case strings.Contains(presType, "workshop"):
		return "workshop"
	case strings.Contains(presType, "panel"):
		return "panel"
	case strings.Contains(presType, "keynote"):
		return "keynote"
	case strings.Contains(presType, "hackathon"):
		return "hackathon"
	default:
		return ""
	}
}

func durationFromPresType(presType string) int {
	if presType == "" {
		return 0
	}
	if presType == "lntalk" {
		return 5
	}
	i := 0
	for i < len(presType) && presType[i] >= '0' && presType[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	switch presType[i:] {
	case "talk", "panel", "workshop", "keynote", "hackathon":
	default:
		return 0
	}
	n, err := strconv.Atoi(presType[:i])
	if err != nil {
		return 0
	}
	return n
}

func validShirtCode(shirt string) string {
	switch shirt {
	case "LS", "LM", "LL", "MS", "MM", "ML", "MXL", "MXXL", "MXXXL":
		return shirt
	}
	return ""
}

func loadState() *migrationState {
	state := &migrationState{Completed: map[string]migrationResult{}}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, state); err != nil {
		log.Fatalf("corrupt state file %s: %s", stateFile, err)
	}
	if state.Completed == nil {
		state.Completed = map[string]migrationResult{}
	}
	log.Printf("resuming from %s (%d already done)", stateFile, len(state.Completed))
	return state
}

func saveState(state *migrationState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("WARN: marshal state: %s", err)
		return
	}
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		log.Printf("WARN: write state file: %s", err)
	}
}
