package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateOrganizationKeys(orgs []*types.Org) error {
	seen := make(map[string]struct{}, len(orgs))
	for _, org := range orgs {
		if org == nil {
			continue
		}
		name := strings.TrimSpace(org.Name)
		if name == "" {
			return fmt.Errorf("organization with empty name")
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate organization name %q", org.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func importOrganizations(ctx context.Context, pool *pgxpool.Pool, orgs []*types.Org) (map[string]string, error) {
	idsByRef := make(map[string]string, len(orgs))
	for _, org := range orgs {
		if org == nil {
			continue
		}
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO organizations (
				name, tagline, logo_light_url, logo_dark_url, email, website_url,
				linkedin_url, instagram_url, youtube_url, github_url, twitter_handle,
				nostr, matrix, hiring, notes
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11,
				$12, $13, $14, $15
			)
			ON CONFLICT (lower(name)) DO UPDATE SET
				tagline = EXCLUDED.tagline,
				logo_light_url = EXCLUDED.logo_light_url,
				logo_dark_url = EXCLUDED.logo_dark_url,
				email = EXCLUDED.email,
				website_url = EXCLUDED.website_url,
				linkedin_url = EXCLUDED.linkedin_url,
				instagram_url = EXCLUDED.instagram_url,
				youtube_url = EXCLUDED.youtube_url,
				github_url = EXCLUDED.github_url,
				twitter_handle = EXCLUDED.twitter_handle,
				nostr = EXCLUDED.nostr,
				matrix = EXCLUDED.matrix,
				hiring = EXCLUDED.hiring,
				notes = EXCLUDED.notes,
				updated_at = now()
			RETURNING id::text
		`, strings.TrimSpace(org.Name), org.Tagline, org.LogoLight, org.LogoDark, nullableString(org.Email), org.Website,
			org.LinkedIn, org.Instagram, org.Youtube, org.Github, org.Twitter.Handle,
			org.Nostr, org.Matrix, org.Hiring, org.Notes).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("upsert organization %q: %w", org.Name, err)
		}
		if org.Ref != "" {
			idsByRef[org.Ref] = id
		}
	}
	return idsByRef, nil
}

func validateOrganizations(ctx context.Context, pool *pgxpool.Pool, orgs []*types.Org) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&count); err != nil {
		return fmt.Errorf("count organizations: %w", err)
	}
	if count < len(orgs) {
		return fmt.Errorf("postgres organization count %d is less than Notion count %d", count, len(orgs))
	}
	for _, org := range orgs {
		if org == nil || strings.TrimSpace(org.Name) == "" {
			continue
		}
		name := strings.TrimSpace(org.Name)
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE lower(name) = lower($1))`, name).Scan(&exists); err != nil {
			return fmt.Errorf("validate organization %q: %w", name, err)
		}
		if !exists {
			return fmt.Errorf("missing organization %q in Postgres", name)
		}
	}
	return nil
}

func orgRefByRef(orgs []*types.Org) map[string]*types.Org {
	out := make(map[string]*types.Org, len(orgs))
	for _, org := range orgs {
		if org == nil || org.Ref == "" {
			continue
		}
		out[org.Ref] = org
	}
	return out
}
