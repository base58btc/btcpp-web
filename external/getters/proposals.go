package getters

import (
	"context"

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

// SpeakerProposalInput is the data needed to create a SpeakerProposal DB row.
// The DB's title-typed property is "ComingFrom" — written directly from
// in.ComingFrom.
type SpeakerProposalInput struct {
	SpeakerID      string
	ProposalID     string
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

func CreateSpeakerProposal(n *types.Notion, in SpeakerProposalInput) (string, error) {
	vals := map[string]*notion.PropertyValue{
		"ComingFrom": titleValue(in.ComingFrom),
		"FirstEvent": checkboxValue(in.FirstEvent),
		"DinnerRSVP": checkboxValue(in.DinnerRSVP),
		"Sponsor":    checkboxValue(in.Sponsor),
	}
	if in.SpeakerID != "" {
		vals["speaker"] = relationValue([]string{in.SpeakerID})
	}
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
	parent := notion.NewDatabaseParent(n.Config.SpeakerProposalDb)
	page, err := n.Client.CreatePage(context.Background(), parent, vals)
	if err != nil {
		return "", err
	}
	return page.ID, nil
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
		vals["Conf"] = selectValue(in.ConfTag)
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

// ListSpeakerProposals fetches every SpeakerProposal page, resolving Speaker
// and Proposal relations via the supplied maps. Pass nil for either map to
// leave that side unresolved.
func ListSpeakerProposals(ctx *config.AppContext, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerProposal, error) {
	n := ctx.Notion
	var out []*types.SpeakerProposal
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakerProposalDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseSpeakerProposal(ctx, page.ID, page.Properties, speakerMap, proposalMap))
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
		return talkFromConfTalk(ct, nil, nil), nil
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
	sps, err := ListSpeakerProposals(ctx, speakerMap, proposalMap)
	if err != nil {
		return nil, err
	}
	var linked []*types.SpeakerProposal
	for _, sp := range sps {
		if sp.Proposal != nil && sp.Proposal.ID == proposalID {
			linked = append(linked, sp)
		}
	}
	return talkFromConfTalk(ct, proposal, linked), nil
}

// talkFromConfTalk is the single denormalization point shared between the
// list-by-conf and get-by-id loaders.
func talkFromConfTalk(ct *types.ConfTalk, proposal *types.Proposal, sps []*types.SpeakerProposal) *types.Talk {
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
	}
	for _, sp := range sps {
		if sp.Speaker == nil {
			continue
		}
		view := *sp.Speaker
		view.Company = sp.Company
		view.OrgLogo = sp.OrgPhoto
		talk.Speakers = append(talk.Speakers, &view)
	}
	return talk
}

// LoadTalksFromConfTalks returns Talk-shaped values populated from the new
// ConfTalk → Proposal → SpeakerProposal[] → Speaker[] chain for a given conf
// tag. Each Speaker's Company and OrgPhoto come from the SpeakerProposal that
// links them to the Proposal — Speaker.Company is no longer authoritative.
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

	sps, err := ListSpeakerProposals(ctx, speakerMap, proposalMap)
	if err != nil {
		return nil, err
	}
	// Group SpeakerProposals by Proposal.ID so we can find the speakers (and
	// per-application Company/OrgPhoto) for any given Proposal.
	spsByProposal := make(map[string][]*types.SpeakerProposal, len(proposalMap))
	for _, sp := range sps {
		if sp.Proposal == nil {
			continue
		}
		spsByProposal[sp.Proposal.ID] = append(spsByProposal[sp.Proposal.ID], sp)
	}

	talks := make([]*types.Talk, 0, len(confTalks))
	for _, ct := range confTalks {
		var linked []*types.SpeakerProposal
		if ct.Proposal != nil {
			linked = spsByProposal[ct.Proposal.ID]
		}
		talks = append(talks, talkFromConfTalk(ct, ct.Proposal, linked))
	}
	return talks, nil
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
