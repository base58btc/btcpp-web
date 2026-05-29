package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func listSpeakerConfsPostgres(ctx *config.AppContext, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerConf, error) {
	return querySpeakerConfsPostgres(ctx, "", nil, speakerMap, proposalMap)
}

func fetchSpeakerConfWithSpeakerPostgres(ctx *config.AppContext, speakerConfID string) (*types.SpeakerConf, error) {
	scs, err := querySpeakerConfsPostgres(ctx, "WHERE speaker_confs.id::text = $1", []interface{}{speakerConfID}, nil, nil)
	if err != nil {
		return nil, err
	}
	if len(scs) == 0 {
		return nil, nil
	}
	return scs[0], nil
}

func getSpeakerConfsByEmailPostgres(ctx *config.AppContext, email string) ([]*types.Speaker, []*types.SpeakerConf, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil, nil
	}
	if ctx == nil || ctx.DB == nil {
		return nil, nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}

	speakers, err := listSpeakersPostgres(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("speakers by email: %w", err)
	}
	speakerMap := make(map[string]*types.Speaker)
	ids := []string{}
	for _, sp := range speakers {
		if sp == nil || !strings.EqualFold(strings.TrimSpace(sp.Email), email) {
			continue
		}
		speakerMap[sp.ID] = sp
		ids = append(ids, sp.ID)
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}

	proposals, err := listProposalsPostgres(ctx)
	if err != nil {
		return nil, nil, err
	}
	proposalMap := make(map[string]*types.Proposal, len(proposals))
	for _, proposal := range proposals {
		if proposal != nil {
			proposalMap[proposal.ID] = proposal
		}
	}

	scs, err := querySpeakerConfsPostgres(ctx, "WHERE speaker_confs.speaker_id::text = ANY($1::text[])", []interface{}{ids}, speakerMap, proposalMap)
	if err != nil {
		return nil, nil, err
	}
	outSpeakers := make([]*types.Speaker, 0, len(ids))
	for _, id := range ids {
		outSpeakers = append(outSpeakers, speakerMap[id])
	}
	return outSpeakers, scs, nil
}

func querySpeakerConfsPostgres(ctx *config.AppContext, where string, args []interface{}, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) ([]*types.SpeakerConf, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if speakerMap == nil {
		speakers, err := listSpeakersPostgres(ctx)
		if err != nil {
			return nil, err
		}
		speakerMap = make(map[string]*types.Speaker, len(speakers))
		for _, speaker := range speakers {
			if speaker != nil {
				speakerMap[speaker.ID] = speaker
			}
		}
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

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, speaker_id::text, coming_from, availability, record_ok,
			visa, first_event, dinner_rsvp, sponsor, company, org_photo_path,
			invited_at, viewed_at, accepted_at
		FROM speaker_confs
		`+where+`
		ORDER BY created_at DESC, id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query speaker confs: %w", err)
	}
	defer rows.Close()

	var out []*types.SpeakerConf
	byID := map[string]*types.SpeakerConf{}
	ids := []string{}
	for rows.Next() {
		var sc types.SpeakerConf
		var speakerID string
		var invitedAt pgtype.Timestamptz
		var viewedAt pgtype.Timestamptz
		var acceptedAt pgtype.Timestamptz
		if err := rows.Scan(
			&sc.ID,
			&speakerID,
			&sc.ComingFrom,
			&sc.Availability,
			&sc.RecordOK,
			&sc.Visa,
			&sc.FirstEvent,
			&sc.DinnerRSVP,
			&sc.Sponsor,
			&sc.Company,
			&sc.OrgPhoto,
			&invitedAt,
			&viewedAt,
			&acceptedAt,
		); err != nil {
			return nil, fmt.Errorf("scan speaker conf: %w", err)
		}
		sc.Speaker = speakerMap[speakerID]
		if invitedAt.Valid {
			sc.InvitedAt = &invitedAt.Time
		}
		if viewedAt.Valid {
			sc.ViewedAt = &viewedAt.Time
		}
		if acceptedAt.Valid {
			sc.AcceptedAt = &acceptedAt.Time
		}
		out = append(out, &sc)
		byID[sc.ID] = &sc
		ids = append(ids, sc.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate speaker confs: %w", err)
	}

	if err := hydrateSpeakerConfProposalsPostgres(ctx, ids, byID, proposalMap); err != nil {
		return nil, err
	}
	if err := hydrateSpeakerConfOtherEventsPostgres(ctx, ids, byID); err != nil {
		return nil, err
	}
	return out, nil
}

func hydrateSpeakerConfProposalsPostgres(ctx *config.AppContext, ids []string, byID map[string]*types.SpeakerConf, proposalMap map[string]*types.Proposal) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT speaker_conf_id::text, proposal_id::text
		FROM proposals_speaker_confs
		WHERE speaker_conf_id::text = ANY($1::text[])
	`, ids)
	if err != nil {
		return fmt.Errorf("query speaker-conf proposal links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var speakerConfID string
		var proposalID string
		if err := rows.Scan(&speakerConfID, &proposalID); err != nil {
			return fmt.Errorf("scan speaker-conf proposal link: %w", err)
		}
		if sc := byID[speakerConfID]; sc != nil {
			if proposal := proposalMap[proposalID]; proposal != nil {
				sc.Proposals = append(sc.Proposals, proposal)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate speaker-conf proposal links: %w", err)
	}
	return nil
}

func hydrateSpeakerConfOtherEventsPostgres(ctx *config.AppContext, ids []string, byID map[string]*types.SpeakerConf) error {
	if len(ids) == 0 {
		return nil
	}
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return err
	}
	confByID := make(map[string]*types.Conf, len(confs))
	for _, conf := range confs {
		if conf != nil {
			confByID[conf.Ref] = conf
		}
	}

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT speaker_conf_id::text, conference_id::text
		FROM speaker_confs_conferences
		WHERE speaker_conf_id::text = ANY($1::text[])
	`, ids)
	if err != nil {
		return fmt.Errorf("query speaker-conf other events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var speakerConfID string
		var confID string
		if err := rows.Scan(&speakerConfID, &confID); err != nil {
			return fmt.Errorf("scan speaker-conf other event: %w", err)
		}
		if sc := byID[speakerConfID]; sc != nil {
			if conf := confByID[confID]; conf != nil {
				sc.OtherEvents = append(sc.OtherEvents, conf)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate speaker-conf other events: %w", err)
	}
	return nil
}
