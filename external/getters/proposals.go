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
		}
		return existingID, nil
	}

	vals := map[string]*notion.PropertyValue{
		"ComingFrom": titleValue(in.ComingFrom),
		"FirstEvent": checkboxValue(in.FirstEvent),
		"DinnerRSVP": checkboxValue(in.DinnerRSVP),
		"Sponsor":    checkboxValue(in.Sponsor),
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
	return page.ID, nil
}

// GetProposal loads a single Proposal page by ID.
func GetProposal(ctx *config.AppContext, proposalID string) (*types.Proposal, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), proposalID)
	if err != nil {
		return nil, err
	}
	return parseProposal(ctx, page.ID, page.Properties), nil
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

// UpdateProposalStatus mirrors UpdateTalkAppStatus for the new Proposal DB.
// Used by the Accept/Invite/Decline admin actions (future work).
func UpdateProposalStatus(ctx *config.AppContext, proposalID, status string) error {
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), proposalID,
		map[string]*notion.PropertyValue{
			"Status": selectValue(status),
		})
	return err
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
