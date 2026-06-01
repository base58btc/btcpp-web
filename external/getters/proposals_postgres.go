package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5"
)

func createProposalPostgres(ctx *config.AppContext, in ProposalInput) (string, error) {
	if ctx == nil || ctx.DB == nil {
		return "", fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	confID, err := proposalConferenceIDPostgres(ctx, in.ScheduleForTag)
	if err != nil {
		return "", err
	}
	var proposalID string
	err = ctx.DB.QueryRow(context.Background(), `
		INSERT INTO proposals (
			conference_id, title, description, setup, comments, talk_type,
			status, desired_duration_min, avail_duration_min
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
		RETURNING id::text
	`, confID, strings.TrimSpace(in.Title), in.Description, in.Setup, in.Comments,
		in.TalkType, in.Status, in.DesiredDuration, in.AvailDuration).Scan(&proposalID)
	if err != nil {
		return "", fmt.Errorf("insert proposal %q: %w", in.Title, err)
	}
	InvalidateProposalsCache()
	return proposalID, nil
}

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

func updateProposalPostgres(ctx *config.AppContext, proposalID string, in ProposalInput) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if strings.TrimSpace(proposalID) == "" {
		return fmt.Errorf("UpdateProposal: empty proposalID")
	}

	setParts := []string{}
	args := []interface{}{}
	addSet := func(column string, value interface{}) {
		args = append(args, value)
		setParts = append(setParts, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if in.Title != "" {
		addSet("title", strings.TrimSpace(in.Title))
	}
	if in.Description != "" {
		addSet("description", in.Description)
	}
	if in.Setup != "" {
		addSet("setup", in.Setup)
	}
	if in.Comments != "" {
		addSet("comments", in.Comments)
	}
	if in.TalkType != "" {
		addSet("talk_type", in.TalkType)
	}
	if in.DesiredDuration > 0 {
		addSet("desired_duration_min", in.DesiredDuration)
	}
	if in.AvailDuration > 0 {
		addSet("avail_duration_min", in.AvailDuration)
	}
	if len(setParts) == 0 {
		return nil
	}

	args = append(args, proposalID)
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE proposals
		SET `+strings.Join(setParts, ", ")+`
		WHERE id = $`+fmt.Sprint(len(args))+`
	`, args...)
	if err != nil {
		return fmt.Errorf("update proposal %s: %w", proposalID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("proposal %s not found", proposalID)
	}
	InvalidateProposalsCache()
	return nil
}

func updateProposalStatusPostgres(ctx *config.AppContext, proposalID, status string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE proposals
		SET status = $2
		WHERE id = $1
	`, proposalID, status)
	if err != nil {
		return fmt.Errorf("update proposal status %s: %w", proposalID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("proposal %s not found", proposalID)
	}
	InvalidateProposalsCache()
	InvalidateSpeakerConfsCache()
	proposalCacheMu.Lock()
	if p := proposalByID[proposalID]; p != nil {
		p.Status = status
	}
	proposalCacheMu.Unlock()
	patchTalksStatusForProposal(proposalID, status)
	return nil
}

func setProposalInviteTokenPostgres(ctx *config.AppContext, proposalID, token string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if token == "" {
		return fmt.Errorf("SetProposalInviteToken: empty token")
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE proposals
		SET invite_token = $2
		WHERE id = $1
	`, proposalID, token)
	if err != nil {
		return fmt.Errorf("set invite token on %s: %w", proposalID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("proposal %s not found", proposalID)
	}
	InvalidateProposalsCache()
	return nil
}

func proposalConferenceIDPostgres(ctx *config.AppContext, tag string) (*string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, nil
	}
	var id string
	err := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text
		FROM conferences
		WHERE tag = $1
		LIMIT 1
	`, tag).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("conference %q not found", tag)
		}
		return nil, fmt.Errorf("query conference %q: %w", tag, err)
	}
	return &id, nil
}
