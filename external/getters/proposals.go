package getters

import (
	"context"
	"fmt"

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
	InvalidateConfTalksCache()
	return page.ID, nil
}

// GetProposal loads a single Proposal page by ID. Reads from the in-memory
// cache when warm; only hits Notion on a cold miss.
func GetProposal(ctx *config.AppContext, proposalID string) (*types.Proposal, error) {
	return FetchProposalByID(ctx, proposalID)
}

// ListProposals fetches every Proposal page. Callers filter by conf in memory,
// matching the existing pattern used for talk apps.
func ListProposals(ctx *config.AppContext) ([]*types.Proposal, error) {
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

// GetSpeakerConfsByEmail looks up Speaker(s) by email and returns every
// SpeakerConf row linked to those speakers, fully resolved.
//
// Cache-driven on the hot path — when caches are warm this does zero
// Notion calls and a couple of map lookups. Falls back to a live query
// only when the cache hasn't been populated yet (e.g. just after boot
// before WaitFetch finishes).
func GetSpeakerConfsByEmail(ctx *config.AppContext, email string) ([]*types.Speaker, []*types.SpeakerConf, error) {
	if email == "" {
		return nil, nil, nil
	}
	speakers, err := GetSpeakersByEmail(ctx.Notion, email)
	if err != nil {
		return nil, nil, fmt.Errorf("speakers by email: %w", err)
	}
	if len(speakers) == 0 {
		return nil, nil, nil
	}

	var allConfs []*types.SpeakerConf
	for _, sp := range speakers {
		allConfs = append(allConfs, FetchSpeakerConfsForSpeaker(ctx, sp.ID)...)
	}
	return speakers, allConfs, nil
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

// FetchSpeakerConfWithSpeaker reads a SpeakerConf by ID with its `speaker`
// relation resolved. Cache-first — only the first request after boot (or
// after invalidation) actually hits Notion.
func FetchSpeakerConfWithSpeaker(ctx *config.AppContext, speakerConfID string) (*types.SpeakerConf, error) {
	if sc := FetchSpeakerConfByID(speakerConfID); sc != nil {
		return sc, nil
	}
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), speakerConfID)
	if err != nil {
		return nil, fmt.Errorf("retrieve speakerconf %s: %w", speakerConfID, err)
	}
	speakerID := parseRef(page.Properties, "speaker")
	if speakerID == "" {
		return nil, nil
	}
	spPage, err := ctx.Notion.Client.RetrievePage(context.Background(), speakerID)
	if err != nil {
		return nil, fmt.Errorf("retrieve speaker %s: %w", speakerID, err)
	}
	speaker := parseSpeaker(spPage.ID, spPage.Properties)
	speakerMap := map[string]*types.Speaker{speakerID: speaker}
	return parseSpeakerConf(ctx, page.ID, page.Properties, speakerMap, nil), nil
}


// ListSpeakerConfs fetches every SpeakerConf page, resolving Speaker and
// Proposals (multi-relation `talk`) via the supplied maps. Pass nil for
// either map to leave that side unresolved.
func ListSpeakerConfs(ctx *config.AppContext, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerConf, error) {
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

// ListConfTalks fetches every ConfTalk page, resolving the Proposal relation
// via proposalMap. Pass nil for proposalMap to leave the Proposal unresolved.
// Callers filter by conf in memory.
func ListConfTalks(ctx *config.AppContext, proposalMap map[string]*types.Proposal) ([]*types.ConfTalk, error) {
	n := ctx.Notion
	var out []*types.ConfTalk
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ConfTalkDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseConfTalk(ctx, page.ID, page.Properties, proposalMap))
		}
	}
	return out, nil
}

// LoadTalkFromConfTalk returns a single Talk-shaped value built from the
// ConfTalk identified by confTalkID — used by the media-render route handlers
// that previously looked up a Talk by Talks-DB page ID. Same denormalization
// rules as LoadTalksFromConfTalks.
func LoadTalkFromConfTalk(ctx *config.AppContext, confTalkID string) (*types.Talk, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), confTalkID)
	if err != nil {
		return nil, err
	}
	ct := parseConfTalk(ctx, page.ID, page.Properties, nil)

	// Resolve the proposal by ID off the page directly.
	proposalID := parseRef(page.Properties, "proposal")
	if proposalID == "" {
		return talkFromConfTalk(ct, nil), nil
	}
	proposal, err := GetProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}

	speakers, err := ListSpeakers(ctx.Notion)
	if err != nil {
		return nil, err
	}
	speakerMap := make(map[string]*types.Speaker, len(speakers))
	for _, sp := range speakers {
		speakerMap[sp.ID] = sp
	}
	proposalMap := map[string]*types.Proposal{proposalID: proposal}
	sps, err := ListSpeakerConfs(ctx, speakerMap, proposalMap)
	if err != nil {
		return nil, err
	}
	speakerConfMap := make(map[string]*types.SpeakerConf, len(sps))
	for _, sc := range sps {
		speakerConfMap[sc.ID] = sc
	}
	resolveProposalSpeakers(proposal, speakerConfMap)
	return talkFromConfTalk(ct, proposal), nil
}

// resolveProposalSpeakers fills in Proposal.Speakers from SpeakerConfRefs
// using the supplied speakerConfMap. Unknown refs are silently skipped.
func resolveProposalSpeakers(p *types.Proposal, speakerConfMap map[string]*types.SpeakerConf) {
	if p == nil {
		return
	}
	p.Speakers = p.Speakers[:0]
	for _, ref := range p.SpeakerConfRefs {
		if sc, ok := speakerConfMap[ref]; ok {
			p.Speakers = append(p.Speakers, sc)
		}
	}
}

// talkFromConfTalk denormalizes a (ConfTalk, Proposal) pair plus the
// proposal's resolved Speakers list into the legacy *types.Talk shape used
// by templates / mediagen / social. Each speaker is a copy with Company /
// OrgLogo overlaid from the per-conf SpeakerConf data.
func talkFromConfTalk(ct *types.ConfTalk, proposal *types.Proposal) *types.Talk {
	talk := &types.Talk{
		ID:          ct.ID,
		Clipart:     ct.Clipart,
		Sched:       ct.Sched,
		Venue:       ct.Venue,
		Section:     ct.Section,
		CalNotif:    ct.CalNotif,
		TalkCardURL: ct.SocialCard,
	}
	if ct.Conf != nil {
		talk.Event = ct.Conf.Tag
	}
	if talk.Sched != nil {
		talk.TimeDesc = talk.Sched.Desc()
	}
	if proposal != nil {
		talk.Name = proposal.Title
		talk.Description = proposal.Description
		talk.Type = proposal.TalkType
		for _, sc := range proposal.Speakers {
			if sc == nil || sc.Speaker == nil {
				continue
			}
			view := *sc.Speaker
			view.Company = sc.Company
			view.OrgLogo = sc.OrgPhoto
			talk.Speakers = append(talk.Speakers, &view)
		}
	}
	return talk
}

// LoadTalksFromConfTalks returns Talk-shaped values populated from the new
// ConfTalk → Proposal → speakers (SpeakerConf[]) → Speaker[] chain for a
// given conf tag. Pass an empty string to load talks for every conf at
// once. Each Speaker's Company and OrgPhoto come from the SpeakerConf
// row — Speaker.Company is no longer authoritative.
//
// Talk.ID is set to ConfTalk.ID so existing card-URL / file-key derivation
// stays stable as Talks-DB usage is wound down.
func LoadTalksFromConfTalks(ctx *config.AppContext, confTag string) ([]*types.Talk, error) {
	proposals, err := ListProposals(ctx)
	if err != nil {
		return nil, err
	}
	proposalMap := make(map[string]*types.Proposal, len(proposals))
	for _, p := range proposals {
		proposalMap[p.ID] = p
	}

	allConfTalks, err := ListConfTalks(ctx, proposalMap)
	if err != nil {
		return nil, err
	}
	confTalks := make([]*types.ConfTalk, 0, len(allConfTalks))
	for _, ct := range allConfTalks {
		if confTag == "" {
			confTalks = append(confTalks, ct)
			continue
		}
		if ct.Conf != nil && ct.Conf.Tag == confTag {
			confTalks = append(confTalks, ct)
		}
	}
	if len(confTalks) == 0 {
		return nil, nil
	}

	speakers, err := ListSpeakers(ctx.Notion)
	if err != nil {
		return nil, err
	}
	speakerMap := make(map[string]*types.Speaker, len(speakers))
	for _, sp := range speakers {
		speakerMap[sp.ID] = sp
	}

	sps, err := ListSpeakerConfs(ctx, speakerMap, proposalMap)
	if err != nil {
		return nil, err
	}
	speakerConfMap := make(map[string]*types.SpeakerConf, len(sps))
	for _, sc := range sps {
		speakerConfMap[sc.ID] = sc
	}

	// Resolve each Proposal.Speakers from its SpeakerConfRefs (the
	// "speakers" multi-relation written by the form-submit / accept flow).
	for _, p := range proposalMap {
		resolveProposalSpeakers(p, speakerConfMap)
	}

	talks := make([]*types.Talk, 0, len(confTalks))
	for _, ct := range confTalks {
		talks = append(talks, talkFromConfTalk(ct, ct.Proposal))
	}
	return talks, nil
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
	return err
}

// ListRecordings fetches every row in RecordingsDb. Used by the warm-cache
// bootstrap; callers should normally read from cacheRecordings instead.
func ListRecordings(ctx *config.AppContext) ([]*types.Recording, error) {
	n := ctx.Notion
	if n.Config.RecordingsDb == "" {
		return nil, nil
	}
	var out []*types.Recording
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.RecordingsDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseRecording(page.ID, page.Properties))
		}
	}
	return out, nil
}

// GetRecordingByConfTalk fetches the Recording row whose `talk` relation
// points at confTalkID. Cache-first — when warm, a missing entry means
// "no recording exists" and we return nil without re-querying Notion.
func GetRecordingByConfTalk(ctx *config.AppContext, confTalkID string) (*types.Recording, error) {
	if r := FetchRecordingByConfTalk(confTalkID); r != nil {
		return r, nil
	}
	if cacheRecordingsWarm() {
		return nil, nil
	}
	n := ctx.Notion
	if n.Config.RecordingsDb == "" {
		return nil, nil
	}
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.RecordingsDb, notion.QueryDatabaseParam{
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

// GetConfTalkByProposal looks up the ConfTalk linked to a proposal via its
// `proposal` relation. Cache-first — when the ConfTalks cache is warm,
// a missing entry is authoritative ("no ConfTalk exists yet") and we
// return nil without falling through to Notion. Live query is reserved
// for the cold-cache path (boot before WaitFetch completes).
func GetConfTalkByProposal(ctx *config.AppContext, proposalID string) (*types.ConfTalk, error) {
	if ct := FetchConfTalkByProposal(proposalID); ct != nil {
		return ct, nil
	}
	if cacheConfTalksWarm() {
		return nil, nil
	}
	n := ctx.Notion
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.ConfTalkDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "proposal",
				Relation: &notion.RelationFilterCondition{Contains: proposalID},
			},
		})
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, nil
	}
	return parseConfTalk(ctx, pages[0].ID, pages[0].Properties, nil), nil
}

// UpdateProposalStatus mirrors UpdateTalkAppStatus for the new Proposal DB.
// Used by the dashboard withdraw / accept-invite flows.
//
// After a successful write we also patch the cached *Proposal in place
// — the cache invalidation only resets the staleness timer, so without
// the eager mutation the dashboard's immediate redirect-back would
// still render the old status until the next periodic refresh tick.
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
	_, err = ctx.Notion.Client.UpdatePageProperties(context.Background(), speakerConfID,
		map[string]*notion.PropertyValue{
			"talk": relationValue(remaining),
		})
	if err != nil {
		return fmt.Errorf("update speakerconf %s: %w", speakerConfID, err)
	}
	InvalidateSpeakerConfsCache()
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
