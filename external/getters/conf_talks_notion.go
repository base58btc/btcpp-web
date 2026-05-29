package getters

import (
	"context"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

// listConfTalksNotion fetches every ConfTalk page, resolving the Proposal
// relation via proposalMap. Pass nil for proposalMap to leave it unresolved.
func listConfTalksNotion(ctx *config.AppContext, proposalMap map[string]*types.Proposal) ([]*types.ConfTalk, error) {
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

func getConfTalkByProposalNotion(ctx *config.AppContext, proposalID string) (*types.ConfTalk, error) {
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

func loadTalkFromConfTalkNotion(ctx *config.AppContext, confTalkID string) (*types.Talk, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), confTalkID)
	if err != nil {
		return nil, err
	}
	ct := parseConfTalk(ctx, page.ID, page.Properties, nil)

	proposalID := parseRef(page.Properties, "proposal")
	if proposalID == "" {
		return talkFromConfTalk(ct, nil), nil
	}
	proposalPage, err := ctx.Notion.Client.RetrievePage(context.Background(), proposalID)
	if err != nil {
		return nil, err
	}
	proposal := parseProposal(ctx, proposalPage.ID, proposalPage.Properties)

	speakers, err := ListSpeakersNotion(ctx.Notion)
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
