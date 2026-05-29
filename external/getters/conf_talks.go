package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// getConfTalks refreshes the ConfTalk cache. Depends on Proposals being cached
// so backend readers can attach linked Proposal pointers.
func getConfTalks(ctx *config.AppContext) {
	ctx.Infos.Printf("getting conftalks...")
	proposalCacheMu.RLock()
	pmap := make(map[string]*types.Proposal, len(proposalByID))
	for k, v := range proposalByID {
		pmap[k] = v
	}
	proposalCacheMu.RUnlock()

	cts, err := ListConfTalks(ctx, pmap)
	if err != nil {
		ctx.Err.Printf("error fetching conftalks %s", err)
		return
	}
	byProp := make(map[string]*types.ConfTalk, len(cts))
	for _, ct := range cts {
		if ct != nil && ct.Proposal != nil {
			byProp[ct.Proposal.ID] = ct
		}
	}
	confTalkCacheMu.Lock()
	cacheConfTalks = cts
	confTalkByProposal = byProp
	confTalkCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d conftalks!", len(cts))
}

func ListConfTalks(ctx *config.AppContext, proposalMap map[string]*types.Proposal) ([]*types.ConfTalk, error) {
	if UsePostgresBackend(ctx) {
		return listConfTalksPostgres(ctx, proposalMap)
	}
	return listConfTalksNotion(ctx, proposalMap)
}

// FetchConfTalkByProposal returns the cached ConfTalk for proposalID, or nil if
// no ConfTalk exists yet (or if the cache is empty).
func FetchConfTalkByProposal(proposalID string) *types.ConfTalk {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	return confTalkByProposal[proposalID]
}

// FetchConfTalkByID walks the warm cache for a ConfTalk with the given ID.
func FetchConfTalkByID(id string) *types.ConfTalk {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID == id {
			return ct
		}
	}
	return nil
}

func InvalidateConfTalksCache() {
	confTalkCacheMu.Lock()
	lastConfTalkFetch = time.Time{}
	confTalkCacheMu.Unlock()
}

func cacheConfTalksWarm() bool {
	confTalkCacheMu.RLock()
	defer confTalkCacheMu.RUnlock()
	return cacheConfTalks != nil
}

// GetConfTalkByProposal looks up the ConfTalk linked to a proposal. Cache-first:
// when the ConfTalks cache is warm, a missing entry is authoritative.
func GetConfTalkByProposal(ctx *config.AppContext, proposalID string) (*types.ConfTalk, error) {
	if ct := FetchConfTalkByProposal(proposalID); ct != nil {
		return ct, nil
	}
	if cacheConfTalksWarm() {
		return nil, nil
	}
	if UsePostgresBackend(ctx) {
		return getConfTalkByProposalPostgres(ctx, proposalID)
	}
	return getConfTalkByProposalNotion(ctx, proposalID)
}

// LoadTalkFromConfTalk returns a single Talk-shaped value built from the
// ConfTalk identified by confTalkID.
func LoadTalkFromConfTalk(ctx *config.AppContext, confTalkID string) (*types.Talk, error) {
	if UsePostgresBackend(ctx) {
		return loadTalkFromConfTalkPostgres(ctx, confTalkID)
	}
	return loadTalkFromConfTalkNotion(ctx, confTalkID)
}

// resolveProposalSpeakers fills in Proposal.Speakers from SpeakerConfRefs using
// the supplied speakerConfMap. Unknown refs are silently skipped.
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

// talkFromConfTalk denormalizes a (ConfTalk, Proposal) pair plus the proposal's
// resolved Speakers list into the legacy *types.Talk shape used by templates,
// media generation, and social publishing.
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
	if rec := FetchRecordingByConfTalk(ct.ID); rec != nil {
		talk.YTLink = rec.YTLink
	}
	if proposal != nil {
		talk.Name = proposal.Title
		talk.Description = proposal.Description
		talk.Type = proposal.TalkType
		talk.Status = proposal.Status
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
// ConfTalk -> Proposal -> speakers chain for a given conf tag. Pass an empty
// string to load talks for every conf.
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

	speakers, err := listSpeakersForBackend(ctx)
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

	for _, p := range proposalMap {
		resolveProposalSpeakers(p, speakerConfMap)
	}

	talks := make([]*types.Talk, 0, len(confTalks))
	for _, ct := range confTalks {
		talks = append(talks, talkFromConfTalk(ct, ct.Proposal))
	}
	return talks, nil
}

func listSpeakersForBackend(ctx *config.AppContext) ([]*types.Speaker, error) {
	if UsePostgresBackend(ctx) {
		return listSpeakersPostgres(ctx)
	}
	return ListSpeakersNotion(ctx.Notion)
}
