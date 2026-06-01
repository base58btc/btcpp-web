package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listOrgsPostgres(ctx *config.AppContext) ([]*types.Org, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, name, tagline, logo_light_url, logo_dark_url,
			coalesce(email::text, ''), website_url, github_url, twitter_handle,
			nostr, matrix, linkedin_url, instagram_url, youtube_url, hiring, notes
		FROM organizations
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query organizations: %w", err)
	}
	defer rows.Close()

	var out []*types.Org
	for rows.Next() {
		var org types.Org
		var twitterHandle string
		if err := rows.Scan(
			&org.Ref,
			&org.Name,
			&org.Tagline,
			&org.LogoLight,
			&org.LogoDark,
			&org.Email,
			&org.Website,
			&org.Github,
			&twitterHandle,
			&org.Nostr,
			&org.Matrix,
			&org.LinkedIn,
			&org.Instagram,
			&org.Youtube,
			&org.Hiring,
			&org.Notes,
		); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		org.Twitter = types.ParseTwitter(twitterHandle)
		out = append(out, &org)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organizations: %w", err)
	}
	return out, nil
}

func findOrgPostgres(ctx *config.AppContext, website, name string) (*types.Org, error) {
	wantSite := normalizeWebsite(website)
	wantName := normalizeName(name)
	if wantSite == "" && wantName == "" {
		return nil, nil
	}
	orgs, err := listOrgsPostgres(ctx)
	if err != nil {
		return nil, err
	}
	if wantSite != "" {
		for _, org := range orgs {
			if org != nil && normalizeWebsite(org.Website) == wantSite {
				return org, nil
			}
		}
	}
	if wantName != "" {
		for _, org := range orgs {
			if org != nil && normalizeName(org.Name) == wantName {
				return org, nil
			}
		}
	}
	return nil, nil
}

func listSponsorshipsPostgres(ctx *config.AppContext, confRef string) ([]*types.Sponsorship, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}

	args := []interface{}{}
	where := "WHERE sponsorships.archived_at IS NULL"
	if confRef != "" {
		args = append(args, confRef)
		where += `
			AND EXISTS (
				SELECT 1
				FROM sponsorships_conferences sc
				WHERE sc.sponsorship_id = sponsorships.id
					AND sc.conference_id::text = $1
			)`
	}

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT sponsorships.id::text, sponsorships.name,
			coalesce(sponsorships.organization_id::text, ''), sponsorships.level,
			sponsorships.label, sponsorships.status, sponsorships.is_vendor,
			sponsorships.notes
		FROM sponsorships
		`+where+`
		ORDER BY sponsorships.level, sponsorships.name
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query sponsorships: %w", err)
	}
	defer rows.Close()

	orgs, err := FetchOrgsCached(ctx)
	if err != nil {
		return nil, err
	}
	orgByID := make(map[string]*types.Org, len(orgs))
	for _, org := range orgs {
		if org != nil {
			orgByID[org.Ref] = org
		}
	}

	var out []*types.Sponsorship
	ids := []string{}
	byID := map[string]*types.Sponsorship{}
	for rows.Next() {
		var sp types.Sponsorship
		var orgID string
		if err := rows.Scan(
			&sp.Ref,
			&sp.Name,
			&orgID,
			&sp.Level,
			&sp.Label,
			&sp.Status,
			&sp.IsVendor,
			&sp.Notes,
		); err != nil {
			return nil, fmt.Errorf("scan sponsorship: %w", err)
		}
		sp.Org = orgByID[orgID]
		out = append(out, &sp)
		ids = append(ids, sp.Ref)
		byID[sp.Ref] = &sp
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sponsorships: %w", err)
	}
	if err := hydrateSponsorshipConfsPostgres(ctx, ids, byID); err != nil {
		return nil, err
	}
	return out, nil
}

func hydrateSponsorshipConfsPostgres(ctx *config.AppContext, ids []string, byID map[string]*types.Sponsorship) error {
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
		SELECT sponsorship_id::text, conference_id::text
		FROM sponsorships_conferences
		WHERE sponsorship_id::text = ANY($1::text[])
	`, ids)
	if err != nil {
		return fmt.Errorf("query sponsorship conference links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sponsorshipID string
		var confID string
		if err := rows.Scan(&sponsorshipID, &confID); err != nil {
			return fmt.Errorf("scan sponsorship conference link: %w", err)
		}
		sp := byID[sponsorshipID]
		conf := confByID[confID]
		if sp != nil && conf != nil {
			sp.Confs = append(sp.Confs, conf)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sponsorship conference links: %w", err)
	}
	return nil
}
