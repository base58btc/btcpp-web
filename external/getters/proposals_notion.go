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

func createProposalNotion(n *types.Notion, in ProposalInput) (string, error) {
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
func upsertSpeakerConfNotion(ctx *config.AppContext, in SpeakerConfInput) (string, error) {
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

func createConfTalkNotion(n *types.Notion, in ConfTalkInput) (string, error) {
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

func updateConfTalkScheduleNotion(ctx *config.AppContext, confTalkID, venue string, start, end time.Time) error {
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

func deleteConfTalkNotion(ctx *config.AppContext, confTalkID string) error {
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

func fetchProposalByIDNotion(ctx *config.AppContext, id string) (*types.Proposal, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return parseProposal(ctx, page.ID, page.Properties), nil
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

func updateSpeakerConfNotion(ctx *config.AppContext, speakerConfID string, in SpeakerConfFields) error {
	vals := map[string]*notion.PropertyValue{
		"FirstEvent": checkboxValue(in.FirstEvent),
		"DinnerRSVP": checkboxValue(in.DinnerRSVP),
		"Sponsor":    checkboxValue(in.Sponsor),
	}
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

func confTalkSetSocialCardNotion(n *types.Notion, confTalkID, path string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confTalkID,
		map[string]*notion.PropertyValue{
			"SocialCard": richTextValue(path),
		})
	return err
}

func confTalkSetClipartNotion(n *types.Notion, confTalkID, filename string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confTalkID,
		map[string]*notion.PropertyValue{
			"Clipart": titleValue(filename),
		})
	if err != nil {
		return err
	}
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

func updateProposalNotion(ctx *config.AppContext, proposalID string, in ProposalInput) error {
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
	InvalidateProposalsCache()
	return nil
}

func updateProposalStatusNotion(ctx *config.AppContext, proposalID, status string) error {
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

func removeProposalFromSpeakerConfNotion(ctx *config.AppContext, speakerConfID, proposalID string) error {
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
	if cached := FetchSpeakerConfByID(speakerConfID); cached != nil {
		out := cached.Proposals[:0]
		for _, p := range cached.Proposals {
			if p != nil && p.ID != proposalID {
				out = append(out, p)
			}
		}
		cached.Proposals = out
	}
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

func setProposalInviteTokenNotion(ctx *config.AppContext, proposalID, token string) error {
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

func setSpeakerConfInvitedAtNotion(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "InvitedAt", when, false)
}

func setSpeakerConfViewedAtNotion(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "ViewedAt", when, true)
}

func setSpeakerConfAcceptedAtNotion(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	return setSpeakerConfDate(ctx, speakerConfID, "AcceptedAt", when, true)
}

func addSpeakerConfToProposalNotion(ctx *config.AppContext, proposalID, speakerConfID string) error {
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
