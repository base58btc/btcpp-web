package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"btcpp-web/external/spaces"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	confTag       = "vienna"
	targetPrefix  = "vienna/recordings/edits/talks/"
	sourceBaseURL = "https://files.sonicsustenance.com/vienna/"
)

var socialZipSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

type cfgFile struct {
	Notion struct {
		Token        string `toml:"token"`
		RecordingsDb string `toml:"recordingsdb"`
	} `toml:"notion"`
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
}

type sourceFile struct {
	Name       string
	SourcePath string
	Day        int
	Time       string
	Venue      string
}

type migrationPlan struct {
	SourceURL       string `json:"source_url"`
	SourcePath      string `json:"source_path,omitempty"`
	SourceKey       string `json:"source_key,omitempty"`
	SourceName      string `json:"source_name"`
	TalkID          string `json:"talk_id"`
	TalkName        string `json:"talk_name"`
	TargetKey       string `json:"target_key"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	RecordingID     string `json:"recording_id,omitempty"`
	ExistingFileURI string `json:"existing_file_uri,omitempty"`
}

type migrationState struct {
	Completed map[string]migrationResult `json:"completed"`
}

type migrationResult struct {
	TargetKey   string `json:"target_key"`
	RecordingID string `json:"recording_id"`
	CompletedAt string `json:"completed_at"`
}

func main() {
	configFile := flag.String("config", "config.toml", "Path to TOML config file")
	talksCache := flag.String("talks-cache", "_cache/talks.json", "Path to talks cache JSON")
	confsCache := flag.String("confs-cache", "_cache/confs.json", "Path to confs cache JSON")
	stateFile := flag.String("state", "migrate-vienna-recordings-state.json", "Path to resumable migration state JSON")
	sourceSet := flag.String("source-set", "main", "Source file set to migrate: main or talks")
	existingPrefix := flag.String("existing-prefix", "", "Move existing Spaces objects from this prefix instead of downloading from Sonic Sustenance")
	dryRun := flag.Bool("dry-run", false, "Preview mappings without uploading or writing Notion")
	flag.Parse()

	cfg := loadCfg(*configFile)
	talks := loadViennaTalks(*talksCache)
	conf := loadConf(*confsCache)
	plans := buildPlans(conf, talks, *sourceSet)
	state := loadState(*stateFile)

	n := &types.Notion{Config: &types.NotionConfig{
		Token:        cfg.Notion.Token,
		RecordingsDb: cfg.Notion.RecordingsDb,
	}}
	n.Setup(cfg.Notion.Token)

	spaces.Init(types.SpacesConfig{
		Endpoint: cfg.Spaces.Endpoint,
		Region:   cfg.Spaces.Region,
		Bucket:   cfg.Spaces.Bucket,
		Key:      cfg.Spaces.Key,
		Secret:   cfg.Spaces.Secret,
	})
	if !spaces.IsConfigured() {
		log.Fatalf("spaces not configured (check [spaces] in %s)", *configFile)
	}

	for i := range plans {
		if strings.TrimSpace(*existingPrefix) != "" {
			plans[i].SourceKey = strings.TrimRight(strings.TrimSpace(*existingPrefix), "/") + "/" + strings.TrimLeft(sourcePath(plans[i].SourceName, plans[i].SourcePath), "/")
		} else {
			size, err := fetchSourceSize(plans[i].SourceURL)
			if err != nil {
				log.Fatalf("probe source size for %s: %s", plans[i].SourceName, err)
			}
			plans[i].SizeBytes = size
		}

		rec, err := findRecordingByConfTalk(n.Client, cfg.Notion.RecordingsDb, plans[i].TalkID)
		if err != nil {
			log.Fatalf("find recording for %s: %s", plans[i].TalkID, err)
		}
		if rec != nil {
			plans[i].RecordingID = rec.ID
			plans[i].ExistingFileURI = rec.FileURI
		}
	}

	if *dryRun {
		printPlans(plans)
		return
	}

	totalBytes := int64(0)
	for _, plan := range plans {
		if done, ok := state.Completed[plan.SourceName]; ok && done.TargetKey == plan.TargetKey && done.RecordingID != "" {
			totalBytes += plan.SizeBytes
		}
	}

	var created, updated, skipped int
	runStarted := time.Now()
	for idx, plan := range plans {
		if plan.SourceKey == "" && state.Completed != nil {
			if done, ok := state.Completed[plan.SourceName]; ok && done.TargetKey == plan.TargetKey && done.RecordingID != "" {
				log.Printf("skip %s -> %s (already completed in state)", plan.SourceName, plan.TargetKey)
				skipped++
				continue
			}
		}

		recID := plan.RecordingID
		if recID != "" && strings.TrimSpace(plan.ExistingFileURI) != "" && normalizeKey(plan.ExistingFileURI) != plan.TargetKey {
			log.Fatalf("recording %s for talk %s already has FileURI %q, refusing to overwrite with %q", recID, plan.TalkID, plan.ExistingFileURI, plan.TargetKey)
		}

		if plan.SourceKey != "" && !spaces.Exists(plan.SourceKey) && !spaces.Exists(plan.TargetKey) {
			log.Fatalf("source object %s does not exist and target %s is not already present", plan.SourceKey, plan.TargetKey)
		}

		if !spaces.Exists(plan.TargetKey) {
			if plan.SourceKey != "" {
				if err := spaces.MovePublic(plan.SourceKey, plan.TargetKey); err != nil {
					log.Fatalf("move %s -> %s: %s", plan.SourceKey, plan.TargetKey, err)
				}
				log.Printf("moved %s -> %s", plan.SourceKey, plan.TargetKey)
			} else {
				progress := newTransferProgress(idx+1, len(plans), plan.SourceName, plan.SizeBytes, totalBytes, totalPlanBytes(plans), runStarted)
				if err := uploadSource(plan.SourceURL, plan.TargetKey, progress); err != nil {
					log.Fatalf("upload %s -> %s: %s", plan.SourceURL, plan.TargetKey, err)
				}
				totalBytes += plan.SizeBytes
				log.Printf("uploaded %s -> %s", plan.SourceName, plan.TargetKey)
			}
		} else {
			totalBytes += plan.SizeBytes
			log.Printf("exists %s", plan.TargetKey)
			if plan.SourceKey != "" && spaces.Exists(plan.SourceKey) {
				if err := spaces.Delete(plan.SourceKey); err != nil {
					log.Fatalf("delete duplicate source %s: %s", plan.SourceKey, err)
				}
				log.Printf("deleted duplicate source %s", plan.SourceKey)
			}
		}

		if recID == "" {
			id, err := createRecording(n.Client, cfg.Notion.RecordingsDb, plan.TalkID, plan.TalkName, plan.TargetKey)
			if err != nil {
				log.Fatalf("create recording for %s: %s", plan.TalkID, err)
			}
			recID = id
			created++
			log.Printf("created recording %s for %s", recID, plan.TalkName)
		} else if strings.TrimSpace(plan.ExistingFileURI) == "" {
			if err := updateRecordingFileURI(n.Client, recID, plan.TargetKey); err != nil {
				log.Fatalf("update recording %s FileURI: %s", recID, err)
			}
			updated++
			log.Printf("updated recording %s FileURI -> %s", recID, plan.TargetKey)
		} else {
			log.Printf("recording %s already linked to %s", recID, plan.TargetKey)
		}

		state.Completed[plan.SourceName] = migrationResult{
			TargetKey:   plan.TargetKey,
			RecordingID: recID,
			CompletedAt: time.Now().UTC().Format(time.RFC3339),
		}
		saveState(*stateFile, state)
	}

	log.Printf("done: created=%d updated=%d skipped=%d total=%d", created, updated, skipped, len(plans))
}

func totalPlanBytes(plans []migrationPlan) int64 {
	var total int64
	for _, plan := range plans {
		total += plan.SizeBytes
	}
	return total
}

func loadCfg(configFile string) cfgFile {
	var cfg cfgFile
	if _, err := toml.DecodeFile(configFile, &cfg); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if cfg.Notion.Token == "" || cfg.Notion.RecordingsDb == "" {
		log.Fatalf("missing notion.token / notion.recordingsdb in %s", configFile)
	}
	return cfg
}

func loadViennaTalks(talksCache string) []*types.Talk {
	var talks []*types.Talk
	if err := readJSONFile(talksCache, &talks); err != nil {
		log.Fatalf("read %s: %s", talksCache, err)
	}
	out := make([]*types.Talk, 0)
	for _, talk := range talks {
		if talk == nil || talk.Event != confTag || talk.Sched == nil {
			continue
		}
		out = append(out, talk)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Sched.Start.Before(out[j].Sched.Start)
	})
	return out
}

func loadConf(confsCache string) *types.Conf {
	var confs []*types.Conf
	if err := readJSONFile(confsCache, &confs); err != nil {
		log.Fatalf("read %s: %s", confsCache, err)
	}
	for _, conf := range confs {
		if conf != nil && conf.Tag == confTag {
			return conf
		}
	}
	log.Fatalf("conference %q not found in %s", confTag, confsCache)
	return nil
}

func readJSONFile(name string, dst interface{}) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(dst)
}

func buildPlans(conf *types.Conf, talks []*types.Talk, sourceSet string) []migrationPlan {
	sources := sourceFiles(sourceSet)
	talkBySlot := map[string]*types.Talk{}
	for _, talk := range talks {
		slot := fmt.Sprintf("%02d-%s-%s", confDay(conf, talk.Sched.Start), talk.Venue, talk.Sched.Start.In(confLoc(conf)).Format("1504"))
		if talkBySlot[slot] != nil {
			log.Fatalf("duplicate main-stage talk slot %s", slot)
		}
		talkBySlot[slot] = talk
	}

	plans := make([]migrationPlan, 0, len(sources))
	for _, src := range sources {
		venue := strings.TrimSpace(src.Venue)
		if venue == "" {
			venue = "one"
		}
		slot := fmt.Sprintf("%02d-%s-%s", src.Day, venue, src.Time)
		talk := talkBySlot[slot]
		if talk == nil {
			log.Fatalf("no talk found for source %s at slot %s", src.Name, slot)
		}
		base := schedulePrefix(conf, talk) + slug(talk.Name)
		sourceRel := src.SourcePath
		if sourceRel == "" {
			sourceRel = src.Name
		}
		plans = append(plans, migrationPlan{
			SourceURL:  sourceBaseURL + sourceRel,
			SourcePath: sourceRel,
			SourceName: src.Name,
			TalkID:     talk.ID,
			TalkName:   talk.Name,
			TargetKey:  targetPrefix + base + strings.ToLower(path.Ext(src.Name)),
		})
	}
	return plans
}

func sourceFiles(sourceSet string) []sourceFile {
	switch strings.ToLower(strings.TrimSpace(sourceSet)) {
	case "", "main":
		return []sourceFile{
			{Name: "Y26E3R1D1S01-Kickoff.mov", Day: 1, Time: "1015"},
			{Name: "Y26E3R1D1S02-Renee.mov", Day: 1, Time: "1030"},
			{Name: "Y26E3R1D1S03-Knut.mov", Day: 1, Time: "1100"},
			{Name: "Y26E3R1D1S04-Luke.mov", Day: 1, Time: "1300"},
			{Name: "Y26E3R1D1S05-ArkadePanel.mov", Day: 1, Time: "1330"},
			{Name: "Y26E3R1D1S06-Anita.mov", Day: 1, Time: "1400"},
			{Name: "Y26E3R1D1S07-Thomas.mov", Day: 1, Time: "1430"},
			{Name: "Y26E3R1D1S08-Jos.mov", Day: 1, Time: "1545"},
			{Name: "Y26E3R1D1S09-PanelEconomicsBitcoin.mov", Day: 1, Time: "1630"},
			{Name: "Y26E3R1D2S01-Matthew.mov", Day: 2, Time: "1015"},
			{Name: "Y26E3R1D2S02-Max.mov", Day: 2, Time: "1045"},
			{Name: "Y26E3R1D2S03-Hubertus.mov", Day: 2, Time: "1300"},
			{Name: "Y26E3R1D2S04-Ben.mov", Day: 2, Time: "1345"},
			{Name: "Y26E3R1D2S05-Rahim.mov", Day: 2, Time: "1415"},
			// Explicit day-2 override: "Panel" is the 15:45 Austrian School
			// panel, while the unlabeled "Video" file is parked as Closing
			// until we have better metadata for it.
			{Name: "Y26E3R1D2S07-Panel.mov", Day: 2, Time: "1545"},
			{Name: "Y26E3R1D2S06-Video.mov", Day: 2, Time: "1630"},
		}
	case "talks":
		return []sourceFile{
			{Name: "Y26E3R2D1S01-Michael.mov", SourcePath: "day1/Y26E3R2D1S01-Michael.mov", Day: 1, Time: "1300", Venue: "two"},
			{Name: "Y26E3R2D1S02-Marek.mov", SourcePath: "day1/Y26E3R2D1S02-Marek.mov", Day: 1, Time: "1330", Venue: "two"},
			{Name: "Y26E3R2D1S03-BlueMatt.mov", SourcePath: "day1/Y26E3R2D1S03-BlueMatt.mov", Day: 1, Time: "1400", Venue: "two"},
			{Name: "Y26E3R2D1S04-Rusty.mov", SourcePath: "day1/Y26E3R2D1S04-Rusty.mov", Day: 1, Time: "1430", Venue: "two"},
			{Name: "Y26E3R2D1S05-Jungly.mov", SourcePath: "day1/Y26E3R2D1S05-Jungly.mov", Day: 1, Time: "1545", Venue: "two"},
			{Name: "Y26E3R2D2S01-Antonie.mov", SourcePath: "day2/Y26E3R2D2S01-Antonie.mov", Day: 2, Time: "1015", Venue: "two"},
			{Name: "Y26E3R2D2S02-Alexander.mov", SourcePath: "day2/Y26E3R2D2S02-Alexander.mov", Day: 2, Time: "1045", Venue: "two"},
			{Name: "Y26E3R2D2S03-Steven.mov", SourcePath: "day2/Y26E3R2D2S03-Steven.mov", Day: 2, Time: "1300", Venue: "two"},
			{Name: "Y26E3R2D2S04-Matous.mov", SourcePath: "day2/Y26E3R2D2S04-Matous.mov", Day: 2, Time: "1345", Venue: "two"},
			{Name: "Y26E3R2D2S05-Talip.mov", SourcePath: "day2/Y26E3R2D2S05-Talip.mov", Day: 2, Time: "1415", Venue: "two"},
		}
	default:
		log.Fatalf("unknown source set %q", sourceSet)
		return nil
	}
}

func sourcePath(name, rel string) string {
	if strings.TrimSpace(rel) != "" {
		return rel
	}
	return name
}

func confDay(conf *types.Conf, when time.Time) int {
	loc := confLoc(conf)
	start := when.In(loc)
	dayStart := time.Date(conf.StartDate.In(loc).Year(), conf.StartDate.In(loc).Month(), conf.StartDate.In(loc).Day(), 0, 0, 0, 0, loc)
	target := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	return int(target.Sub(dayStart).Hours()/24) + 1
}

func schedulePrefix(conf *types.Conf, talk *types.Talk) string {
	loc := confLoc(conf)
	start := talk.Sched.Start.In(loc)
	return fmt.Sprintf("%02d%s%s_", confDay(conf, talk.Sched.Start), stageSlug(talk.Venue), start.Format("1504"))
}

func confLoc(conf *types.Conf) *time.Location {
	if conf != nil && strings.TrimSpace(conf.Timezone) != "" {
		if loc, err := time.LoadLocation(conf.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

func stageSlug(venue string) string {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case "one", "main", "main stage":
		return "main"
	case "two", "talks", "talks stage":
		return "talks"
	case "three", "workshop", "workshops", "workshops stage":
		return "workshop"
	case "four", "lounge", "lounge stage":
		return "lounge"
	default:
		return slug(venue)
	}
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = socialZipSlugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func fetchSourceSize(srcURL string) (int64, error) {
	req, err := http.NewRequest(http.MethodHead, srcURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("head: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength, nil
	}
	return 0, nil
}

func uploadSource(srcURL, targetKey string, progress *transferProgress) error {
	resp, err := http.Get(srcURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = "video/quicktime"
	}
	size := resp.ContentLength
	if size < 0 {
		size = 0
	}
	if progress != nil && size > 0 && progress.fileTotal == 0 {
		progress.fileTotal = size
	}
	body := io.Reader(resp.Body)
	if progress != nil {
		body = io.TeeReader(resp.Body, progress)
		progress.start()
		defer progress.finish()
	}
	_, err = spaces.UploadStream(targetKey, body, ct, size)
	return err
}

type transferProgress struct {
	fileIndex    int
	fileCount    int
	fileName     string
	fileTotal    int64
	fileDone     int64
	overallDone  int64
	overallTotal int64
	runStarted   time.Time
	lastLog      time.Time
}

func newTransferProgress(fileIndex, fileCount int, fileName string, fileTotal, overallDone, overallTotal int64, runStarted time.Time) *transferProgress {
	return &transferProgress{
		fileIndex:    fileIndex,
		fileCount:    fileCount,
		fileName:     fileName,
		fileTotal:    fileTotal,
		overallDone:  overallDone,
		overallTotal: overallTotal,
		runStarted:   runStarted,
	}
}

func (p *transferProgress) start() {
	p.lastLog = time.Now()
	log.Printf("[%d/%d] starting %s (%s)", p.fileIndex, p.fileCount, p.fileName, formatBytes(p.fileTotal))
}

func (p *transferProgress) Write(b []byte) (int, error) {
	n := len(b)
	p.fileDone += int64(n)
	now := time.Now()
	if now.Sub(p.lastLog) >= 5*time.Second {
		p.logStatus(now)
		p.lastLog = now
	}
	return n, nil
}

func (p *transferProgress) finish() {
	p.logStatus(time.Now())
}

func (p *transferProgress) logStatus(now time.Time) {
	filePct := pct(p.fileDone, p.fileTotal)
	overallDone := p.overallDone + p.fileDone
	overallPct := pct(overallDone, p.overallTotal)
	elapsed := now.Sub(p.runStarted)
	rate := float64(overallDone)
	if elapsed > 0 {
		rate = rate / elapsed.Seconds()
	}
	eta := ""
	if rate > 0 && p.overallTotal > overallDone {
		eta = fmt.Sprintf(" eta=%s", (time.Duration(float64(p.overallTotal-overallDone)/rate) * time.Second).Round(time.Second))
	}
	log.Printf(
		"[%d/%d] %s file=%s/%s (%.1f%%) overall=%s/%s (%.1f%%) rate=%s/s%s",
		p.fileIndex,
		p.fileCount,
		p.fileName,
		formatBytes(p.fileDone),
		formatBytes(p.fileTotal),
		filePct,
		formatBytes(overallDone),
		formatBytes(p.overallTotal),
		overallPct,
		formatBytes(int64(rate)),
		eta,
	)
}

func pct(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(done) * 100 / float64(total)
}

func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	u := 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if u == 0 {
		return fmt.Sprintf("%d %s", n, units[u])
	}
	return fmt.Sprintf("%.1f %s", v, units[u])
}

func findRecordingByConfTalk(client notion.API, recordingsDb, confTalkID string) (*types.Recording, error) {
	pages, _, _, err := client.QueryDatabase(context.Background(), recordingsDb, notion.QueryDatabaseParam{
		Filter: &notion.Filter{
			Property: "talk",
			Relation: &notion.RelationFilterCondition{Contains: confTalkID},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, nil
	}
	return parseRecording(pages[0].ID, pages[0].Properties), nil
}

func createRecording(client notion.API, recordingsDb, confTalkID, talkName, fileURI string) (string, error) {
	vals := map[string]*notion.PropertyValue{
		"talk": {
			Type: notion.PropertyRelation,
			Relation: []*notion.ObjectReference{
				{Object: notion.ObjectPage, ID: confTalkID},
			},
		},
		"FileURI": richTextValue(fileURI),
	}
	if talkName != "" {
		vals["TalkName"] = notion.NewTitlePropertyValue(
			&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: talkName}},
		)
	}
	page, err := client.CreatePage(context.Background(), notion.NewDatabaseParent(recordingsDb), vals)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

func updateRecordingFileURI(client notion.API, recordingID, fileURI string) error {
	_, err := client.UpdatePageProperties(context.Background(), recordingID, map[string]*notion.PropertyValue{
		"FileURI": richTextValue(fileURI),
	})
	return err
}

func richTextValue(s string) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type: notion.PropertyRichText,
		RichText: []*notion.RichText{{
			Type: notion.RichTextText,
			Text: &notion.Text{Content: s},
		}},
	}
}

func parseRecording(pageID string, props map[string]notion.PropertyValue) *types.Recording {
	rec := &types.Recording{
		ID:         pageID,
		TalkName:   propText(props["TalkName"]),
		YTLink:     props["YTLink"].URL,
		XLink:      props["XLink"].URL,
		XReplyLink: props["XReplyLink"].URL,
		FileURI:    propText(props["FileURI"]),
	}
	for _, ref := range props["talk"].Relation {
		if ref != nil && ref.ID != "" {
			rec.ConfTalkID = ref.ID
			break
		}
	}
	return rec
}

func propText(p notion.PropertyValue) string {
	var sb strings.Builder
	for _, t := range p.Title {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	for _, t := range p.RichText {
		if t != nil && t.Text != nil {
			sb.WriteString(t.Text.Content)
		}
	}
	return strings.TrimSpace(sb.String())
}

func normalizeKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil {
			return strings.TrimPrefix(u.Path, "/")
		}
	}
	return s
}

func loadState(stateFile string) *migrationState {
	state := &migrationState{Completed: map[string]migrationResult{}}
	if err := readJSONFile(stateFile, state); err != nil {
		return state
	}
	if state.Completed == nil {
		state.Completed = map[string]migrationResult{}
	}
	return state
}

func saveState(stateFile string, state *migrationState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Fatalf("marshal state: %s", err)
	}
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		log.Fatalf("write %s: %s", stateFile, err)
	}
}

func printPlans(plans []migrationPlan) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plans); err != nil {
		log.Fatalf("write plan: %s", err)
	}
}
