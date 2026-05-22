package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateProposalRows(proposals []*types.Proposal) error {
	for _, proposal := range proposals {
		if proposal == nil {
			continue
		}
		if strings.TrimSpace(proposal.Title) == "" {
			return fmt.Errorf("proposal with empty title")
		}
	}
	return nil
}

func importProposalsRows(ctx context.Context, pool *pgxpool.Pool, proposals []*types.Proposal) (map[string]string, error) {
	idsByRef := make(map[string]string, len(proposals))
	for _, proposal := range proposals {
		if proposal == nil {
			continue
		}

		confTag := ""
		if proposal.ScheduleFor != nil {
			confTag = proposal.ScheduleFor.Tag
		}

		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO proposals (
				conference_id, title, description, setup, comments, talk_type,
				status, desired_duration_min, avail_duration_min, invite_token
			)
			SELECT c.id, $2, $3, $4, $5, $6, $7, $8, $9, $10
			FROM (SELECT $1::text AS tag) input
			LEFT JOIN conferences c ON c.tag = input.tag
			RETURNING id::text
		`, nullableString(confTag), strings.TrimSpace(proposal.Title), proposal.Description, proposal.Setup,
			proposal.Comments, proposal.TalkType, proposal.Status, proposal.DesiredDuration,
			proposal.AvailDuration, proposal.InviteToken).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert proposal %q: %w", proposal.Title, err)
		}
		if proposal.ID != "" {
			idsByRef[proposal.ID] = id
		}
	}
	return idsByRef, nil
}

func proposalByRef(proposals []*types.Proposal) map[string]*types.Proposal {
	out := make(map[string]*types.Proposal, len(proposals))
	for _, proposal := range proposals {
		if proposal == nil || proposal.ID == "" {
			continue
		}
		out[proposal.ID] = proposal
	}
	return out
}

func validateProposals(ctx context.Context, pool *pgxpool.Pool, proposals []*types.Proposal) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM proposals`).Scan(&count); err != nil {
		return fmt.Errorf("count proposals: %w", err)
	}
	if count < len(proposals) {
		return fmt.Errorf("postgres proposal count %d is less than Notion count %d", count, len(proposals))
	}

	for _, proposal := range proposals {
		if proposal == nil || strings.TrimSpace(proposal.Title) == "" {
			continue
		}
		confTag := ""
		if proposal.ScheduleFor != nil {
			confTag = proposal.ScheduleFor.Tag
		}
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM proposals p
				LEFT JOIN conferences c ON c.id = p.conference_id
				WHERE p.title = $1
					AND p.status = $2
					AND ($3 = '' OR c.tag = $3)
			)
		`, strings.TrimSpace(proposal.Title), proposal.Status, confTag).Scan(&exists); err != nil {
			return fmt.Errorf("validate proposal %q: %w", proposal.Title, err)
		}
		if !exists {
			return fmt.Errorf("missing proposal %q in Postgres", proposal.Title)
		}
	}
	return nil
}
