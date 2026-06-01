package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// ProposalInput is the data needed to create a Proposal DB row from a form
// submission.
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

// SpeakerConfInput is the data needed to upsert a SpeakerConf DB row.
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

// ConfTalkInput is the data needed to create a ConfTalk DB row at accept time.
type ConfTalkInput struct {
	ConfTag    string
	ProposalID string
}

// SpeakerConfFields is the editable subset of a SpeakerConf row written
// from the dashboard editor. Speaker / conf / talk relations stay put.
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

func CreateProposal(ctx *config.AppContext, in ProposalInput) (string, error) {
	if UsePostgresBackend(ctx) {
		return createProposalPostgres(ctx, in)
	}
	return createProposalNotion(ctx.Notion, in)
}

func UpsertSpeakerConf(ctx *config.AppContext, in SpeakerConfInput) (string, error) {
	if UsePostgresBackend(ctx) {
		return "", unsupportedPostgresBackend("UpsertSpeakerConf")
	}
	return upsertSpeakerConfNotion(ctx, in)
}

func CreateConfTalk(ctx *config.AppContext, in ConfTalkInput) (string, error) {
	if UsePostgresBackend(ctx) {
		return createConfTalkPostgres(ctx, in)
	}
	return createConfTalkNotion(ctx.Notion, in)
}

func UpdateConfTalkSchedule(ctx *config.AppContext, confTalkID, venue string, start, end time.Time) error {
	if UsePostgresBackend(ctx) {
		return updateConfTalkSchedulePostgres(ctx, confTalkID, venue, start, end)
	}
	return updateConfTalkScheduleNotion(ctx, confTalkID, venue, start, end)
}

func DeleteConfTalk(ctx *config.AppContext, confTalkID string) error {
	if UsePostgresBackend(ctx) {
		return deleteConfTalkPostgres(ctx, confTalkID)
	}
	return deleteConfTalkNotion(ctx, confTalkID)
}

func UpdateSpeakerConf(ctx *config.AppContext, speakerConfID string, in SpeakerConfFields) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("UpdateSpeakerConf")
	}
	return updateSpeakerConfNotion(ctx, speakerConfID, in)
}

func ConfTalkSetSocialCard(ctx *config.AppContext, confTalkID, path string) error {
	if UsePostgresBackend(ctx) {
		return confTalkSetSocialCardPostgres(ctx, confTalkID, path)
	}
	return confTalkSetSocialCardNotion(ctx.Notion, confTalkID, path)
}

func ConfTalkSetClipart(ctx *config.AppContext, confTalkID, filename string) error {
	if UsePostgresBackend(ctx) {
		return confTalkSetClipartPostgres(ctx, confTalkID, filename)
	}
	return confTalkSetClipartNotion(ctx.Notion, confTalkID, filename)
}

func UpdateProposal(ctx *config.AppContext, proposalID string, in ProposalInput) error {
	if UsePostgresBackend(ctx) {
		return updateProposalPostgres(ctx, proposalID, in)
	}
	return updateProposalNotion(ctx, proposalID, in)
}

func UpdateProposalStatus(ctx *config.AppContext, proposalID, status string) error {
	if UsePostgresBackend(ctx) {
		return updateProposalStatusPostgres(ctx, proposalID, status)
	}
	return updateProposalStatusNotion(ctx, proposalID, status)
}

func RemoveProposalFromSpeakerConf(ctx *config.AppContext, speakerConfID, proposalID string) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("RemoveProposalFromSpeakerConf")
	}
	return removeProposalFromSpeakerConfNotion(ctx, speakerConfID, proposalID)
}

func SetProposalInviteToken(ctx *config.AppContext, proposalID, token string) error {
	if UsePostgresBackend(ctx) {
		return setProposalInviteTokenPostgres(ctx, proposalID, token)
	}
	return setProposalInviteTokenNotion(ctx, proposalID, token)
}

func SetSpeakerConfInvitedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("SetSpeakerConfInvitedAt")
	}
	return setSpeakerConfInvitedAtNotion(ctx, speakerConfID, when)
}

func SetSpeakerConfViewedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("SetSpeakerConfViewedAt")
	}
	return setSpeakerConfViewedAtNotion(ctx, speakerConfID, when)
}

func SetSpeakerConfAcceptedAt(ctx *config.AppContext, speakerConfID string, when time.Time) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("SetSpeakerConfAcceptedAt")
	}
	return setSpeakerConfAcceptedAtNotion(ctx, speakerConfID, when)
}

func AddSpeakerConfToProposal(ctx *config.AppContext, proposalID, speakerConfID string) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("AddSpeakerConfToProposal")
	}
	return addSpeakerConfToProposalNotion(ctx, proposalID, speakerConfID)
}

// GetProposal loads a single Proposal page by ID. Reads from the in-memory
// cache when warm; only hits the selected backend on a cold miss.
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
	return fetchProposalByIDNotion(ctx, id)
}

// ListProposals fetches every Proposal page. Callers filter by conf in memory,
// matching the existing pattern used for talk apps.
func ListProposals(ctx *config.AppContext) ([]*types.Proposal, error) {
	if UsePostgresBackend(ctx) {
		return listProposalsPostgres(ctx)
	}
	return ListProposalsNotion(ctx)
}

func ListProposalsOnly(n *types.Notion) ([]*types.Proposal, error) {
	return ListProposalsOnlyNotion(n)
}
