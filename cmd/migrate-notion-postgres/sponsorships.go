package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateSponsorshipKeys(sponsorships []*types.Sponsorship, orgsByRef map[string]*types.Org, confTagByRef map[string]string) error {
	for _, sponsorship := range sponsorships {
		if sponsorship == nil {
			continue
		}
		if sponsorship.Org != nil && sponsorship.Org.Ref != "" {
			if orgsByRef[sponsorship.Org.Ref] == nil {
				return fmt.Errorf("sponsorship %q has unresolved organization ref", sponsorship.Name)
			}
		}
		for _, conf := range sponsorship.Confs {
			if conf == nil || conf.Ref == "" {
				continue
			}
			if confTagByRef[conf.Ref] == "" {
				return fmt.Errorf("sponsorship %q has unresolved conference ref", sponsorship.Name)
			}
		}
	}
	return nil
}

func importSponsorships(ctx context.Context, pool *pgxpool.Pool, sponsorships []*types.Sponsorship, orgIDsByRef map[string]string, orgsByRef map[string]*types.Org, confTagByRef map[string]string) error {
	for _, sponsorship := range sponsorships {
		if sponsorship == nil {
			continue
		}
		orgID := ""
		if sponsorship.Org != nil {
			orgID = orgIDsByRef[sponsorship.Org.Ref]
		}

		var sponsorshipID string
		name := sponsorshipName(sponsorship, orgsByRef, confTagByRef)
		err := pool.QueryRow(ctx, `
			INSERT INTO sponsorships (
				organization_id, name, level, label, status, is_vendor, notes
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7
			)
			RETURNING id::text
		`, nullableString(orgID), name, sponsorship.Level, sponsorship.Label, sponsorship.Status, sponsorship.IsVendor, sponsorship.Notes).Scan(&sponsorshipID)
		if err != nil {
			return fmt.Errorf("insert sponsorship %q: %w", name, err)
		}

		for _, conf := range sponsorship.Confs {
			if conf == nil {
				continue
			}
			confTag := confTagByRef[conf.Ref]
			if confTag == "" {
				return fmt.Errorf("sponsorship %q has unresolved conference ref", name)
			}
			_, err := pool.Exec(ctx, `
				INSERT INTO sponsorship_conferences (sponsorship_id, conference_id)
				SELECT $1, id
				FROM conferences
				WHERE tag = $2
				ON CONFLICT DO NOTHING
			`, sponsorshipID, confTag)
			if err != nil {
				return fmt.Errorf("upsert sponsorship conference %q/%q: %w", name, confTag, err)
			}
		}
	}
	return nil
}

func validateSponsorships(ctx context.Context, pool *pgxpool.Pool, sponsorships []*types.Sponsorship, orgsByRef map[string]*types.Org, confTagByRef map[string]string) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sponsorships`).Scan(&count); err != nil {
		return fmt.Errorf("count sponsorships: %w", err)
	}
	if count < len(sponsorships) {
		return fmt.Errorf("postgres sponsorship count %d is less than Notion count %d", count, len(sponsorships))
	}
	expectedLinks := 0
	for _, sponsorship := range sponsorships {
		if sponsorship != nil {
			expectedLinks += len(sponsorship.Confs)
		}
	}
	var linkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sponsorship_conferences`).Scan(&linkCount); err != nil {
		return fmt.Errorf("count sponsorship conference links: %w", err)
	}
	if linkCount < expectedLinks {
		return fmt.Errorf("postgres sponsorship conference link count %d is less than Notion relation count %d", linkCount, expectedLinks)
	}
	for _, sponsorship := range sponsorships {
		if sponsorship == nil {
			continue
		}
		for _, conf := range sponsorship.Confs {
			if conf == nil || conf.Ref == "" {
				continue
			}
			confTag := confTagByRef[conf.Ref]
			var linked bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM sponsorship_conferences sc
					JOIN sponsorships s ON s.id = sc.sponsorship_id
					JOIN conferences c ON c.id = sc.conference_id
					WHERE s.organization_id IS NOT DISTINCT FROM $1::uuid
						AND s.level = $2
						AND s.label = $3
						AND s.status = $4
						AND s.is_vendor = $5
						AND c.tag = $6
				)
			`, sponsorshipOrgID(ctx, pool, sponsorship, orgsByRef), sponsorship.Level, sponsorship.Label, sponsorship.Status, sponsorship.IsVendor, confTag).Scan(&linked); err != nil {
				return fmt.Errorf("validate sponsorship conference %q/%q: %w", sponsorshipName(sponsorship, orgsByRef, confTagByRef), confTag, err)
			}
			if !linked {
				return fmt.Errorf("missing sponsorship conference link %q/%q in Postgres", sponsorshipName(sponsorship, orgsByRef, confTagByRef), confTag)
			}
		}
	}
	return nil
}

func sponsorshipConfTags(sponsorship *types.Sponsorship, confTagByRef map[string]string) []string {
	if sponsorship == nil {
		return nil
	}
	out := make([]string, 0, len(sponsorship.Confs))
	for _, conf := range sponsorship.Confs {
		if conf == nil {
			continue
		}
		if tag := confTagByRef[conf.Ref]; tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func sponsorshipName(sponsorship *types.Sponsorship, orgsByRef map[string]*types.Org, confTagByRef map[string]string) string {
	if sponsorship == nil {
		return ""
	}
	base := strings.TrimSpace(sponsorship.Name)
	if base == "" {
		base = sponsorshipOrgName(sponsorship, orgsByRef)
	}
	if base == "" && sponsorship.Org != nil {
		base = strings.TrimSpace(sponsorship.Org.Ref)
	}

	parts := []string{base}
	parts = append(parts, sponsorshipConfTags(sponsorship, confTagByRef)...)
	return strings.Join(nonEmptyStrings(parts), " - ")
}

func sponsorshipOrgID(ctx context.Context, pool *pgxpool.Pool, sponsorship *types.Sponsorship, orgsByRef map[string]*types.Org) interface{} {
	if sponsorship == nil || sponsorship.Org == nil {
		return nil
	}
	org := orgsByRef[sponsorship.Org.Ref]
	if org == nil || strings.TrimSpace(org.Name) == "" {
		return nil
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE lower(name) = lower($1)`, org.Name).Scan(&id); err != nil {
		return nil
	}
	return id
}

func sponsorshipOrgName(sponsorship *types.Sponsorship, orgsByRef map[string]*types.Org) string {
	if sponsorship == nil || sponsorship.Org == nil {
		return ""
	}
	org := orgsByRef[sponsorship.Org.Ref]
	if org == nil {
		return ""
	}
	return org.Name
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
