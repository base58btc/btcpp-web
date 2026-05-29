package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func listConfTalksPostgres(ctx *config.AppContext, proposalMap map[string]*types.Proposal) ([]*types.ConfTalk, error) {
	return queryConfTalksPostgres(ctx, "", nil, proposalMap)
}

func getConfTalkByProposalPostgres(ctx *config.AppContext, proposalID string) (*types.ConfTalk, error) {
	rows, err := queryConfTalksPostgres(ctx, "WHERE proposal_id::text = $1", []interface{}{proposalID}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func loadTalkFromConfTalkPostgres(ctx *config.AppContext, confTalkID string) (*types.Talk, error) {
	rows, err := queryConfTalksPostgres(ctx, "WHERE conf_talks.id::text = $1", []interface{}{confTalkID}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("conf talk %s not found", confTalkID)
	}
	ct := rows[0]
	if ct.Proposal == nil {
		return talkFromConfTalk(ct, nil), nil
	}

	proposalMap := map[string]*types.Proposal{ct.Proposal.ID: ct.Proposal}
	sps, err := listSpeakerConfsPostgres(ctx, nil, proposalMap)
	if err != nil {
		return nil, err
	}
	speakerConfMap := make(map[string]*types.SpeakerConf, len(sps))
	for _, sc := range sps {
		speakerConfMap[sc.ID] = sc
	}
	resolveProposalSpeakers(ct.Proposal, speakerConfMap)
	return talkFromConfTalk(ct, ct.Proposal), nil
}

func queryConfTalksPostgres(ctx *config.AppContext, where string, args []interface{}, proposalMap map[string]*types.Proposal) ([]*types.ConfTalk, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if proposalMap == nil {
		proposals, err := listProposalsPostgres(ctx)
		if err != nil {
			return nil, err
		}
		proposalMap = make(map[string]*types.Proposal, len(proposals))
		for _, proposal := range proposals {
			if proposal != nil {
				proposalMap[proposal.ID] = proposal
			}
		}
	}

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

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, conference_id::text, coalesce(proposal_id::text, ''),
			clipart_path, scheduled_start, scheduled_end, production_notes,
			venue, section, cal_notif, social_card_path
		FROM conf_talks
		`+where+`
		ORDER BY scheduled_start NULLS LAST, id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query conf talks: %w", err)
	}
	defer rows.Close()

	var out []*types.ConfTalk
	for rows.Next() {
		var ct types.ConfTalk
		var confID string
		var proposalID string
		var scheduledStart pgtype.Timestamptz
		var scheduledEnd pgtype.Timestamptz
		if err := rows.Scan(
			&ct.ID,
			&confID,
			&proposalID,
			&ct.Clipart,
			&scheduledStart,
			&scheduledEnd,
			&ct.ProductionNotes,
			&ct.Venue,
			&ct.Section,
			&ct.CalNotif,
			&ct.SocialCard,
		); err != nil {
			return nil, fmt.Errorf("scan conf talk: %w", err)
		}
		ct.Conf = confByID[confID]
		ct.Proposal = proposalMap[proposalID]
		if scheduledStart.Valid {
			ct.Sched = &types.Times{Start: scheduledStart.Time}
			if scheduledEnd.Valid {
				ct.Sched.End = &scheduledEnd.Time
			}
		}
		out = append(out, &ct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conf talks: %w", err)
	}
	return out, nil
}
