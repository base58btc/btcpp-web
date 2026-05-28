package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listProposalsPostgres(ctx *config.AppContext) ([]*types.Proposal, error) {
	return queryProposalsPostgres(ctx, "")
}

func getProposalPostgres(ctx *config.AppContext, id string) (*types.Proposal, error) {
	proposals, err := queryProposalsPostgres(ctx, "WHERE proposals.id::text = $1", id)
	if err != nil {
		return nil, err
	}
	if len(proposals) == 0 {
		return nil, fmt.Errorf("proposal %s not found", id)
	}
	return proposals[0], nil
}

func queryProposalsPostgres(ctx *config.AppContext, where string, args ...interface{}) ([]*types.Proposal, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT proposals.id::text, proposals.title, proposals.description,
			proposals.setup, proposals.comments, proposals.talk_type,
			proposals.status, proposals.desired_duration_min,
			proposals.avail_duration_min, proposals.invite_token,
			coalesce(proposals.conference_id::text, '')
		FROM proposals
		`+where+`
		ORDER BY proposals.created_at DESC, proposals.title
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query proposals: %w", err)
	}
	defer rows.Close()

	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}
	confByID := make(map[string]*types.Conf, len(confs))
	for _, conf := range confs {
		if conf != nil {
			confByID[conf.Ref] = conf
		}
	}

	var out []*types.Proposal
	byID := map[string]*types.Proposal{}
	ids := []string{}
	for rows.Next() {
		var proposal types.Proposal
		var confID string
		if err := rows.Scan(
			&proposal.ID,
			&proposal.Title,
			&proposal.Description,
			&proposal.Setup,
			&proposal.Comments,
			&proposal.TalkType,
			&proposal.Status,
			&proposal.DesiredDuration,
			&proposal.AvailDuration,
			&proposal.InviteToken,
			&confID,
		); err != nil {
			return nil, fmt.Errorf("scan proposal: %w", err)
		}
		proposal.ScheduleFor = confByID[confID]
		out = append(out, &proposal)
		byID[proposal.ID] = &proposal
		ids = append(ids, proposal.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proposals: %w", err)
	}
	if err := hydrateProposalSpeakerConfRefsPostgres(ctx, ids, byID); err != nil {
		return nil, err
	}
	return out, nil
}

func hydrateProposalSpeakerConfRefsPostgres(ctx *config.AppContext, ids []string, byID map[string]*types.Proposal) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT proposal_id::text, speaker_conf_id::text
		FROM proposals_speaker_confs
		WHERE proposal_id::text = ANY($1::text[])
	`, ids)
	if err != nil {
		return fmt.Errorf("query proposal speaker-conf links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var proposalID string
		var speakerConfID string
		if err := rows.Scan(&proposalID, &speakerConfID); err != nil {
			return fmt.Errorf("scan proposal speaker-conf link: %w", err)
		}
		if proposal := byID[proposalID]; proposal != nil {
			proposal.SpeakerConfRefs = append(proposal.SpeakerConfRefs, speakerConfID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate proposal speaker-conf links: %w", err)
	}
	return nil
}
