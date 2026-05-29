package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	notion "github.com/niftynei/go-notion"
)

// ProposalInput is the data needed to create a Proposal DB row from a form
// submission. The Notion DB's title-typed property is "Title" — written
// directly from in.Title.
type ProposalInput struct {
	Title           string
	Description     string
	Setup           string
	Comments        string
	TalkType        string // talk / workshop / panel / keynote / hackathon
	DesiredDuration int
	AvailDuration   int
	ScheduleForTag  string // Conf tag, written to the ScheduleFor select
	Status          string // initial value: "Applied"
}

// SpeakerConfInput is the data needed to upsert a SpeakerConf DB row. The
// DB's title-typed property is "ComingFrom" — written directly from
// in.ComingFrom. The `talk` relation is multi-valued: one SpeakerConf row
// covers every proposal a given speaker is delivering at a given conf.
type SpeakerConfInput struct {
	SpeakerID      string
	ConfTag        string // conf the new ProposalID is scheduled for
	ProposalID     string // proposal to attach to this speaker's row at this conf
	OrgID          string // Orgs page ID, written to the "org" relation
	Company        string // free-text affiliation captured from the form
	OrgPhoto       string // bare filename in Spaces sponsors/ (e.g. "abc123.svg")
	ComingFrom     string
	Availability   []string
	RecordOK       string
	Visa           string
	FirstEvent     bool
	OtherEventTags []string // Conf tags, written as a multi_select
	DinnerRSVP     bool
	Sponsor        bool
}

func CreateProposal(n *types.Notion, in ProposalInput) (string, error) {
	vals := map[string]*notion.PropertyValue{
		"Title": titleValue(in.Title),
	}
	if in.Description != "" {
		vals["Desc"] = richTextValue(in.Description)
	}
	if in.Setup != "" {
		vals["Setup"] = richTextValue(in.Setup)
	}
	if in.Comments != "" {
		vals["Comments"] = richTextValue(in.Comments)
	}
	if in.TalkType != "" {
		vals["TalkType"] = selectValue(in.TalkType)
	}
	if in.Status != "" {
		vals["Status"] = selectValue(in.Status)
	}
	if in.DesiredDuration > 0 {
		vals["DesiredDuration"] = numberValue(float64(in.DesiredDuration))
	}
	if in.AvailDuration > 0 {
		vals["AvailDuration"] = numberValue(float64(in.AvailDuration))
	}
	if in.ScheduleForTag != "" {
		vals["ScheduleFor"] = selectValue(in.ScheduleForTag)
	}
	parent := notion.NewDatabaseParent(n.Config.ProposalDb)
	page, err := n.Client.CreatePage(context.Background(), parent, vals)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

// UpsertSpeakerConf finds the SpeakerConf row for (in.SpeakerID, in.ConfTag)
// and appends in.ProposalID to its `talk` multi-relation, or creates a new
// row if none exists. Returns the SpeakerConf page ID.
//
// Per-application fields (ComingFrom, Avails, etc.) are written only on
// CREATE — they're not overwritten on append, since they belong to the
// (speaker, conf) pair as a whole, not the individual proposal being added.
func UpsertSpeakerConf(ctx *config.AppContext, in SpeakerConfInput) (string, error) {
	n := ctx.Notion
	if in.SpeakerID == "" {
		return "", fmt.Errorf("UpsertSpeakerConf: SpeakerID required")
	}

	existingID, existingTalkIDs, err := findSpeakerConfForConf(ctx, in.SpeakerID, in.ConfTag)
	if err != nil {
		return "", fmt.Errorf("find speaker conf: %w", err)
	}
	if existingID != "" {
		if in.ProposalID != "" && !containsString(existingTalkIDs, in.ProposalID) {
			existingTalkIDs = append(existingTalkIDs, in.ProposalID)
			_, err := n.Client.UpdatePageProperties(context.Background(), existingID,
				map[string]*notion.PropertyValue{
					"talk": relationValue(existingTalkIDs),
				})
			if err != nil {
				return "", fmt.Errorf("append talk to %s: %w", existingID, err)
			}
			// Append the proposal pointer to the cached SpeakerConf so
			// the dashboard sees it on the very next request rather
			// than waiting for the next cache refresh.
			if cached := FetchSpeakerConfByID(existingID); cached != nil {
				if p, _ := GetProposal(ctx, in.ProposalID); p != nil {
					cached.Proposals = append(cached.Proposals, p)
				}
			}
		}
		return existingID, nil
	}

	vals := map[string]*notion.PropertyValue{
		"FirstEvent": checkboxValue(in.FirstEvent),
		"DinnerRSVP": checkboxValue(in.DinnerRSVP),
		"Sponsor":    checkboxValue(in.Sponsor),
	}
	// ComingFrom is the page Title — Notion rejects writes that include
	// the property but leave its `title` array empty/undefined. Skip the
	// key entirely on empty input (the page just lands untitled, which
	// the apply form / dashboard editor will populate later).
	if in.ComingFrom != "" {
		vals["ComingFrom"] = titleValue(in.ComingFrom)
	}
	vals["speaker"] = relationValue([]string{in.SpeakerID})
	if in.ProposalID != "" {
		vals["talk"] = relationValue([]string{in.ProposalID})
	}
	if in.OrgID != "" {
		vals["org"] = relationValue([]string{in.OrgID})
	}
	if in.Company != "" {
		vals["Company"] = richTextValue(in.Company)
	}
	if in.OrgPhoto != "" {
		vals["OrgPhoto"] = richTextValue(in.OrgPhoto)
	}
	if len(in.Availability) > 0 {
		vals["Avails"] = multiSelectValue(in.Availability)
	}
	if in.RecordOK != "" {
		vals["RecordOK"] = selectValue(in.RecordOK)
	}
	if in.Visa != "" {
		vals["Visa"] = selectValue(in.Visa)
	}
	if len(in.OtherEventTags) > 0 {
		vals["OtherEvents"] = multiSelectValue(in.OtherEventTags)
	}
	parent := notion.NewDatabaseParent(n.Config.SpeakerConfDb)
	page, err := n.Client.CreatePage(context.Background(), parent, vals)
	if err != nil {
		return "", err
	}
	// Eagerly insert into the warm cache so dashboard reads immediately
	// after invite-flow submits don't return "no SpeakerConfs for this
	// email" until the next periodic refresh tick.
	sc := &types.SpeakerConf{
		ID:           page.ID,
		ComingFrom:   in.ComingFrom,
		Speaker:      CacheSpeakerByID(in.SpeakerID),
		Availability: in.Availability,
		RecordOK:     in.RecordOK,
		Visa:         in.Visa,
		FirstEvent:   in.FirstEvent,
		DinnerRSVP:   in.DinnerRSVP,
		Sponsor:      in.Sponsor,
		Company:      in.Company,
		OrgPhoto:     in.OrgPhoto,
	}
	if in.ProposalID != "" {
		if p, _ := GetProposal(ctx, in.ProposalID); p != nil {
			sc.Proposals = append(sc.Proposals, p)
		}
	}
	CacheSpeakerConfInsert(sc)
	return page.ID, nil
}

// findSpeakerConfForConf queries SpeakerConfDb for rows whose speaker
// relation contains speakerID, then for each candidate checks whether any
// proposal it links via `talk` has ScheduleFor == confTag. Returns the
// matching page ID + its existing talk relation IDs, or empty when no match.
func findSpeakerConfForConf(ctx *config.AppContext, speakerID, confTag string) (string, []string, error) {
	if confTag == "" {
		return "", nil, nil
	}
	n := ctx.Notion
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.SpeakerConfDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "speaker",
				Relation: &notion.RelationFilterCondition{
					Contains: speakerID,
				},
			},
		})
	if err != nil {
		return "", nil, err
	}
	for _, pg := range pages {
		var talkIDs []string
		for _, ref := range pg.Properties["talk"].Relation {
			if ref != nil && ref.ID != "" {
				talkIDs = append(talkIDs, ref.ID)
			}
		}
		for _, pid := range talkIDs {
			p, err := GetProposal(ctx, pid)
			if err != nil {
				continue
			}
			if p.ScheduleFor != nil && p.ScheduleFor.Tag == confTag {
				return pg.ID, talkIDs, nil
			}
		}
	}
	return "", nil, nil
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

// ConfTalkInput is the data needed to create a ConfTalk DB row at accept
// time. The other ConfTalk fields (Clipart, ProductionNotes, TalkTime, Venue,
// SocialCard) are admin-filled when the talk is scheduled.
type ConfTalkInput struct {
	ConfTag    string
	ProposalID string
}

func CreateConfTalk(n *types.Notion, in ConfTalkInput) (string, error) {
	vals := map[string]*notion.PropertyValue{}
	if in.ConfTag != "" {
		vals["Event"] = selectValue(in.ConfTag)
	}
	if in.ProposalID != "" {
		vals["proposal"] = relationValue([]string{in.ProposalID})
	}
	parent := notion.NewDatabaseParent(n.Config.ConfTalkDb)
	page, err := n.Client.CreatePage(context.Background(), parent, vals)
	if err != nil {
		return "", err
	}

	// Eagerly insert the new row into the warm cache. Without this,
	// a follow-up FetchConfTalkByProposal (which reads the map
	// directly with no staleness check) returns nil, so the next
	// schedule drop creates a duplicate ConfTalk instead of
	// updating the existing one.
	ct := &types.ConfTalk{ID: page.ID}
	if in.ProposalID != "" {
		proposalCacheMu.RLock()
		ct.Proposal = proposalByID[in.ProposalID]
		proposalCacheMu.RUnlock()
	}
	confTalkCacheMu.Lock()
	cacheConfTalks = append(cacheConfTalks, ct)
	if confTalkByProposal == nil {
		confTalkByProposal = make(map[string]*types.ConfTalk)
	}
	if in.ProposalID != "" {
		confTalkByProposal[in.ProposalID] = ct
	}
	lastConfTalkFetch = time.Time{}
	confTalkCacheMu.Unlock()

	return page.ID, nil
}

// UpdateConfTalkSchedule writes the TalkTime (date range) + Venue
// (select) on a ConfTalk row. Used by the schedule tool when a talk
// gets dragged onto / repositioned within the grid.
//
// `start` and `end` should already be in the conf's timezone. Notion's
// API encodes them as ISO 8601 with offset; matching consumers (e.g.
// the agenda renderer) compare wall-clock minutes anyway, so the
// stored offset is informational.
//
// Patches the cached ConfTalk in place so the next page render picks
// up the new schedule without waiting for a periodic refresh.
func UpdateConfTalkSchedule(ctx *config.AppContext, confTalkID, venue string, start, end time.Time) error {
	endCopy := end
	props := map[string]*notion.PropertyValue{
		"TalkTime": notion.NewDatePropertyValue(&notion.Date{
			Start: start,
			End:   &endCopy,
		}),
	}
	if venue != "" {
		props["Venue"] = selectValue(venue)
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), confTalkID, props)
	if err != nil {
		return fmt.Errorf("update conftalk %s schedule: %w", confTalkID, err)
	}

	confTalkCacheMu.Lock()
	for _, ct := range cacheConfTalks {
		if ct == nil || ct.ID != confTalkID {
			continue
		}
		ct.Sched = &types.Times{Start: start, End: &endCopy}
		ct.Venue = venue
	}
	lastConfTalkFetch = time.Time{}
	confTalkCacheMu.Unlock()
	return nil
}

// DeleteConfTalk archives the ConfTalk page in Notion (Notion's "delete"
// is a soft archive — the row is hidden from queries but recoverable
// from the page trash for 30 days). Used when a talk is dragged off the
// schedule grid.
//
// Goes via raw HTTP PATCH because the go-notion library doesn't expose
// the `archived` flag on its UpdatePageProperties wrapper. Mirrors the
// pattern already used by clearRelationProperty.
func DeleteConfTalk(ctx *config.AppContext, confTalkID string) error {
	body, err := json.Marshal(map[string]interface{}{"archived": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH",
		"https://api.notion.com/v1/pages/"+confTalkID,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Notion.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion archive %s: %v", confTalkID, errResp)
	}

	// Eagerly drop the entry from the warm cache so the next render
	// doesn't keep showing the just-deleted ConfTalk.
	confTalkCacheMu.Lock()
	for proposalID, ct := range confTalkByProposal {
		if ct != nil && ct.ID == confTalkID {
			delete(confTalkByProposal, proposalID)
		}
	}
	out := cacheConfTalks[:0]
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID != confTalkID {
			out = append(out, ct)
		}
	}
	cacheConfTalks = out
	lastConfTalkFetch = time.Time{}
	confTalkCacheMu.Unlock()
	return nil
}

// GetProposal loads a single Proposal page by ID. Reads from the in-memory
// cache when warm; only hits Notion on a cold miss.
func GetProposal(ctx *config.AppContext, proposalID string) (*types.Proposal, error) {
	return FetchProposalByID(ctx, proposalID)
}

// getProposals refreshes the in-memory Proposal cache + by-ID map.
func getProposals(ctx *config.AppContext) {
	ctx.Infos.Printf("getting proposals...")
	props, err := ListProposals(ctx)
	if err != nil {
		ctx.Err.Printf("error fetching proposals %s", err)
		return
	}
	idx := make(map[string]*types.Proposal, len(props))
	for _, p := range props {
		if p != nil {
			idx[p.ID] = p
		}
	}
	proposalCacheMu.Lock()
	cacheProposals = props
	proposalByID = idx
	proposalCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d proposals!", len(props))
}

// FetchProposalsCached returns the cached proposal slice. May trigger an
// async refresh if the TTL has elapsed; the returned data may be stale by
// up to one refresh cycle.
func FetchProposalsCached(ctx *config.AppContext) ([]*types.Proposal, error) {
	deadline := time.Now().Add(-cacheTTL)
	proposalCacheMu.RLock()
	stale := cacheProposals == nil || lastProposalFetch.Before(deadline)
	out := cacheProposals
	proposalCacheMu.RUnlock()
	if stale {
		lastProposalFetch = time.Now()
		queueRefresh(JobProposals)
	}
	return out, nil
}

// FetchProposalByID is the hot-path lookup used by per-proposal handlers
// (GetProposal, dashboard auth, etc.). O(1) map read; falls back to a direct
// backend read only if the cache is empty.
func FetchProposalByID(ctx *config.AppContext, id string) (*types.Proposal, error) {
	proposalCacheMu.RLock()
	p := proposalByID[id]
	proposalCacheMu.RUnlock()
	if p != nil {
		return p, nil
	}
	if UsePostgresBackend(ctx) {
		return getProposalPostgres(ctx, id)
	}
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return parseProposal(ctx, page.ID, page.Properties), nil
}

// ListProposals fetches every Proposal page. Callers filter by conf in memory,
// matching the existing pattern used for talk apps.
func ListProposals(ctx *config.AppContext) ([]*types.Proposal, error) {
	if UsePostgresBackend(ctx) {
		return listProposalsPostgres(ctx)
	}
	return ListProposalsNotion(ctx)
}

func ListProposalsNotion(ctx *config.AppContext) ([]*types.Proposal, error) {
	n := ctx.Notion
	var out []*types.Proposal
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ProposalDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseProposal(ctx, page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseProposalOnly(pageID string, props map[string]notion.PropertyValue) *types.Proposal {
	prop := &types.Proposal{
		ID:              pageID,
		Title:           parseRichText("Title", props),
		Description:     parseRichText("Desc", props),
		Setup:           parseRichText("Setup", props),
		Comments:        parseRichText("Comments", props),
		TalkType:        parseSelect("TalkType", props),
		Status:          parseSelect("Status", props),
		DesiredDuration: int(props["DesiredDuration"].Number),
		AvailDuration:   int(props["AvailDuration"].Number),
		InviteToken:     parseRichText("InviteToken", props),
	}
	if tag := parseSelect("ScheduleFor", props); tag != "" {
		prop.ScheduleFor = &types.Conf{Tag: tag}
	}
	for _, ref := range props["speakers"].Relation {
		if ref != nil && ref.ID != "" {
			prop.SpeakerConfRefs = append(prop.SpeakerConfRefs, ref.ID)
		}
	}
	return prop
}

func ListProposalsOnly(n *types.Notion) ([]*types.Proposal, error) {
	return ListProposalsOnlyNotion(n)
}

func ListProposalsOnlyNotion(n *types.Notion) ([]*types.Proposal, error) {
	var out []*types.Proposal
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ProposalDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseProposalOnly(page.ID, page.Properties))
		}
	}
	return out, nil
}

// SpeakerConfFields is the editable subset of a SpeakerConf row written
// from the dashboard editor. Speaker / conf / talk relations stay put — the
// editor only touches per-attendance fields.
type SpeakerConfFields struct {
	Company      string
	OrgID        string // Org page ID picked via autocomplete; empty = leave existing
	OrgPhoto     string // bare filename in Spaces sponsors/; empty = leave existing
	ComingFrom   string
	Availability []string
	RecordOK     string
	Visa         string
	FirstEvent   bool
	DinnerRSVP   bool
	Sponsor      bool
}

// UpdateSpeakerConf writes the editable subset to a SpeakerConf row.
// All fields are written every time — the form always submits them all,
// so partial-update semantics aren't needed here. OrgPhoto is the
// exception: empty means "no new upload, keep the existing filename".
func UpdateSpeakerConf(ctx *config.AppContext, speakerConfID string, in SpeakerConfFields) error {
	vals := map[string]*notion.PropertyValue{
		"FirstEvent": checkboxValue(in.FirstEvent),
		"DinnerRSVP": checkboxValue(in.DinnerRSVP),
		"Sponsor":    checkboxValue(in.Sponsor),
	}
	// Title (ComingFrom) and rich_text (Company) properties must be
	// omitted when empty — Notion rejects writes that include the key
	// with no type-specific body. To clear an existing value, write
	// it from the dashboard form which always submits a value.
	if in.ComingFrom != "" {
		vals["ComingFrom"] = titleValue(in.ComingFrom)
	}
	if in.Company != "" {
		vals["Company"] = richTextValue(in.Company)
	}
	if in.RecordOK != "" {
		vals["RecordOK"] = selectValue(in.RecordOK)
	}
	if in.Visa != "" {
		vals["Visa"] = selectValue(in.Visa)
	}
	if in.Availability != nil {
		vals["Avails"] = multiSelectValue(in.Availability)
	}
	if in.OrgPhoto != "" {
		vals["OrgPhoto"] = richTextValue(in.OrgPhoto)
	}
	if in.OrgID != "" {
		vals["org"] = relationValue([]string{in.OrgID})
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), speakerConfID, vals)
	if err == nil {
		InvalidateSpeakerConfsCache()
	}
	return err
}

// ListSpeakerConfs fetches every SpeakerConf page, resolving Speaker and
// Proposals (multi-relation `talk`) via the supplied maps. Pass nil for
// either map to leave that side unresolved.
func ListSpeakerConfsNotion(ctx *config.AppContext, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerConf, error) {
	n := ctx.Notion
	var out []*types.SpeakerConf
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakerConfDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseSpeakerConf(ctx, page.ID, page.Properties, speakerMap, proposalMap))
		}
	}
	return out, nil
}

// ConfTalkSetSocialCard writes the storage path of a generated talk-card
// PNG onto ConfTalk.SocialCard (rich_text). The value stored is the path
// portion only — e.g. "/riga/talks/abc-1080p.png" — not the full Spaces
// public URL, so the rendering side can compose the host as it sees fit.
func ConfTalkSetSocialCard(n *types.Notion, confTalkID, path string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confTalkID,
		map[string]*notion.PropertyValue{
			"SocialCard": richTextValue(path),
		})
	return err
}

// ConfTalkSetClipart writes a clipart filename onto ConfTalk.Clipart.
// The Notion column is title-typed (not rich_text — parseRichText
// reads it back fine because go-notion exposes both shapes
// uniformly, but writes have to use the matching type or Notion
// rejects with "Clipart is expected to be title").
//
// Used by the admin clipart-upload UI after the bytes land in
// Spaces — templates reference Clipart as just the filename
// ("vienna_bitcoin.png") and compose the URL via the talkClipart
// template func.
func ConfTalkSetClipart(n *types.Notion, confTalkID, filename string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confTalkID,
		map[string]*notion.PropertyValue{
			"Clipart": titleValue(filename),
		})
	if err != nil {
		return err
	}
	// Patch the warm ConfTalk caches in place. Without this, the
	// admin redirect right after a clipart upload re-renders from
	// stale cache (FetchConfTalkByProposal reads the map directly;
	// InvalidateConfTalksCache only resets the staleness timer, it
	// doesn't synchronously refetch). Same shape as the patch
	// TalkUpdateCalNotif does for CalNotif writes.
	confTalkCacheMu.Lock()
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID == confTalkID {
			ct.Clipart = filename
			break
		}
	}
	confTalkCacheMu.Unlock()
	return nil
}

// UpdateProposal applies a partial update to a Proposal page — only fields
// set on `in` are written. Used by the speaker-side proposal editor on the
// dashboard.
func UpdateProposal(ctx *config.AppContext, proposalID string, in ProposalInput) error {
	vals := map[string]*notion.PropertyValue{}
	if in.Title != "" {
		vals["Title"] = titleValue(in.Title)
	}
	if in.Description != "" {
		vals["Desc"] = richTextValue(in.Description)
	}
	if in.Setup != "" {
		vals["Setup"] = richTextValue(in.Setup)
	}
	if in.Comments != "" {
		vals["Comments"] = richTextValue(in.Comments)
	}
	if in.TalkType != "" {
		vals["TalkType"] = selectValue(in.TalkType)
	}
	if in.DesiredDuration > 0 {
		vals["DesiredDuration"] = numberValue(float64(in.DesiredDuration))
	}
	if in.AvailDuration > 0 {
		vals["AvailDuration"] = numberValue(float64(in.AvailDuration))
	}
	if len(vals) == 0 {
		return nil
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), proposalID, vals)
	if err != nil {
		return err
	}
	// Drop the warm cache so subsequent reads (the dashboard, the
	// admin views) see the updated content fields without waiting
	// for the periodic refresh.
	InvalidateProposalsCache()
	return nil
}

func selectProperty(name string) *notion.PropertyValue {
	return notion.NewSelectPropertyValue(&notion.SelectOption{Name: name})
}

// UpdateProposalStatus mirrors UpdateTalkAppStatus for the new Proposal DB.
// Used by the dashboard withdraw / accept-invite flows.
//
// After a successful write we also patch the cached *Proposal in place
// — the cache invalidation only resets the staleness timer, so without
// the eager mutation the dashboard's immediate redirect-back would
// still render the old status until the next periodic refresh tick.
// We also patch the derived `talks` slice so conf-page reads of
// Talk.Status (e.g. Conf.HasAgenda) see the flip without waiting on
// the async talks-cache rebuild.
func UpdateProposalStatus(ctx *config.AppContext, proposalID, status string) error {
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), proposalID,
		map[string]*notion.PropertyValue{
			"Status": selectValue(status),
		})
	if err == nil {
		InvalidateProposalsCache()
		InvalidateSpeakerConfsCache()
		proposalCacheMu.Lock()
		if p := proposalByID[proposalID]; p != nil {
			p.Status = status
		}
		proposalCacheMu.Unlock()
		patchTalksStatusForProposal(proposalID, status)
	}
	return err
}

// RemoveProposalFromSpeakerConf drops one proposal from a SpeakerConf's `talk`
// multi-relation. Used when a panelist withdraws — the proposal stays alive
// (other panelists still linked) but this speaker no longer participates.
//
// No-op if the proposal isn't currently in the relation.
func RemoveProposalFromSpeakerConf(ctx *config.AppContext, speakerConfID, proposalID string) error {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), speakerConfID)
	if err != nil {
		return fmt.Errorf("retrieve speakerconf %s: %w", speakerConfID, err)
	}
	var remaining []string
	for _, ref := range page.Properties["talk"].Relation {
		if ref == nil || ref.ID == "" || ref.ID == proposalID {
			continue
		}
		remaining = append(remaining, ref.ID)
	}
	// go-notion's PropertyValue.Relation has `omitempty`, so an
	// empty slice gets elided from the PATCH body and Notion
	// rejects "talk" with no concrete sub-field. When the removal
	// empties the relation entirely, fall back to the raw-HTTP
	// clearRelationProperty path (same workaround
	// UpdateAffiliateCode and the WorkShift Assignees clearing
	// use).
	if len(remaining) == 0 {
		if err := clearRelationProperty(ctx.Notion.Config.Token, speakerConfID, "talk"); err != nil {
			return fmt.Errorf("clear speakerconf %s talk: %w", speakerConfID, err)
		}
	} else {
		_, err = ctx.Notion.Client.UpdatePageProperties(context.Background(), speakerConfID,
			map[string]*notion.PropertyValue{
				"talk": relationValue(remaining),
			})
		if err != nil {
			return fmt.Errorf("update speakerconf %s: %w", speakerConfID, err)
		}
	}
	InvalidateSpeakerConfsCache()
	// Eagerly drop the proposal pointer from the cached SpeakerConf so
	// the dashboard's next render after the redirect doesn't still
	// show the just-removed talk waiting for a periodic refresh.
	if cached := FetchSpeakerConfByID(speakerConfID); cached != nil {
		out := cached.Proposals[:0]
		for _, p := range cached.Proposals {
			if p != nil && p.ID != proposalID {
				out = append(out, p)
			}
		}
		cached.Proposals = out
	}
	// Mirror the drop on the Proposal side. Notion's two-way
	// relation will backfill this eventually, but the admin
	// edit-page redirect renders before that propagates;
	// resolveProposalSpeakers walks Proposal.SpeakerConfRefs and
	// FetchSpeakerConfByID still returns a SpeakerConf row for
	// the removed ref, so the speaker stays visible until the
	// next cache refresh.
	proposalCacheMu.Lock()
	if p := proposalByID[proposalID]; p != nil {
		out := p.SpeakerConfRefs[:0]
		for _, ref := range p.SpeakerConfRefs {
			if ref != speakerConfID {
				out = append(out, ref)
			}
		}
		p.SpeakerConfRefs = out
	}
	proposalCacheMu.Unlock()
	return nil
}

// SetProposalInviteToken writes the InviteToken rich_text field on a
// proposal. Used by the share-a-link invite flow to mint a token on
// first invite, or to rotate one (admin-side, via this endpoint or
// directly in Notion) to revoke any outstanding share links.
//
// Empty token isn't supported here — the go-notion library's omitempty
// drops zero-length rich_text arrays from the JSON, which Notion
// rejects. To clear a token, admins edit the field in Notion's UI.
func SetProposalInviteToken(ctx *config.AppContext, proposalID, token string) error {
	if token == "" {
		return fmt.Errorf("SetProposalInviteToken: empty token (clear via Notion UI)")
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), proposalID,
		map[string]*notion.PropertyValue{
			"InviteToken": richTextValue(token),
		})
	if err != nil {
		return fmt.Errorf("set invite token on %s: %w", proposalID, err)
	}
	InvalidateProposalsCache()
	return nil
}

// setSpeakerConfDate stamps a date property on a SpeakerConf row and
// invalidates the cache. Shared by the three timestamp writers below.
// Returns nil if the row already has a timestamp and onlyIfEmpty is
// true — that's the right semantics for ViewedAt (first-view-wins),
// where repeated visits shouldn't keep updating the field.
func setSpeakerConfDate(ctx *config.AppContext, speakerConfID, field string, when time.Time, onlyIfEmpty bool) error {
	if onlyIfEmpty {
		if sc := FetchSpeakerConfByID(speakerConfID); sc != nil {
			already := scTimestamp(sc, field)
			if already != nil {
				return nil
			}
		}
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), speakerConfID,
		map[string]*notion.PropertyValue{
			field: notion.NewDatePropertyValue(&notion.Date{Start: when}),
		})
	if err != nil {
		return fmt.Errorf("set %s on speakerconf %s: %w", field, speakerConfID, err)
	}
	InvalidateSpeakerConfsCache()
	if cached := FetchSpeakerConfByID(speakerConfID); cached != nil {
		w := when
		switch field {
		case "InvitedAt":
			cached.InvitedAt = &w
		case "ViewedAt":
			cached.ViewedAt = &w
		case "AcceptedAt":
			cached.AcceptedAt = &w
		}
	}
	return nil
}

func scTimestamp(sc *types.SpeakerConf, field string) *time.Time {
	switch field {
	case "InvitedAt":
		return sc.InvitedAt
	case "ViewedAt":
		return sc.ViewedAt
	case "AcceptedAt":
		return sc.AcceptedAt
	}
	return nil
}

// SetSpeakerConfInvitedAt stamps the moment an admin sent an invite.
// Always overwrites — re-inviting is a real event the audit trail
// should reflect.
func SetSpeakerConfInvitedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "InvitedAt", when, false)
}

// SetSpeakerConfViewedAt stamps the first time the invited speaker
// opened the magic link. First-view-wins so we don't overwrite the
// initial signal on every page reload.
func SetSpeakerConfViewedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "ViewedAt", when, true)
}

// SetSpeakerConfAcceptedAt stamps the moment the speaker hit the
// Accept Invitation button on the magic-link form. First-accept-wins.
func SetSpeakerConfAcceptedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "AcceptedAt", when, true)
}

// AddSpeakerConfToProposal appends speakerConfID to a Proposal's `speakers`
// multi-relation. Idempotent — no-op when the SpeakerConf is already in
// the relation.
//
// Used by the co-speaker invite flow alongside UpsertSpeakerConf, which
// only writes the SpeakerConf → Proposal direction. Notion's two-way
// relations should keep these in sync, but writing both sides
// explicitly defends against schema drift and keeps the in-memory cache
// consistent on the next refresh.
func AddSpeakerConfToProposal(ctx *config.AppContext, proposalID, speakerConfID string) error {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), proposalID)
	if err != nil {
		return fmt.Errorf("retrieve proposal %s: %w", proposalID, err)
	}
	existing := make([]string, 0, len(page.Properties["speakers"].Relation)+1)
	for _, ref := range page.Properties["speakers"].Relation {
		if ref == nil || ref.ID == "" {
			continue
		}
		if ref.ID == speakerConfID {
			return nil
		}
		existing = append(existing, ref.ID)
	}
	existing = append(existing, speakerConfID)
	_, err = ctx.Notion.Client.UpdatePageProperties(context.Background(), proposalID,
		map[string]*notion.PropertyValue{
			"speakers": relationValue(existing),
		})
	if err != nil {
		return fmt.Errorf("update proposal %s: %w", proposalID, err)
	}
	InvalidateProposalsCache()
	// Patch the warm proposal cache in place. Without this, the
	// immediate redirect-back after an admin attach-speaker click
	// re-renders from stale SpeakerConfRefs (the timer-only
	// invalidation doesn't synchronously refetch, and the cached
	// pointer is what GetProposal returns). Same shape as the
	// patch UpdateProposalStatus / ConfTalkSetClipart do.
	proposalCacheMu.Lock()
	if p := proposalByID[proposalID]; p != nil {
		alreadyHas := false
		for _, ref := range p.SpeakerConfRefs {
			if ref == speakerConfID {
				alreadyHas = true
				break
			}
		}
		if !alreadyHas {
			p.SpeakerConfRefs = append(p.SpeakerConfRefs, speakerConfID)
		}
	}
	proposalCacheMu.Unlock()
	return nil
}

// --- internal property-builder helpers shared by accept.go ---

func numberValue(n float64) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type:   notion.PropertyNumber,
		Number: n,
	}
}

func relationValue(ids []string) *notion.PropertyValue {
	refs := make([]*notion.ObjectReference, len(ids))
	for i, id := range ids {
		refs[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: id}
	}
	return &notion.PropertyValue{
		Type:     notion.PropertyRelation,
		Relation: refs,
	}
}
