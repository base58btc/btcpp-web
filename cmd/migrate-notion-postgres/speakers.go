package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateSpeakerRows(speakers []*types.Speaker) error {
	for _, speaker := range speakers {
		if speaker == nil {
			continue
		}
		if strings.TrimSpace(speaker.Name) == "" {
			return fmt.Errorf("speaker with empty name")
		}
		for _, role := range speaker.Roles {
			if _, _, ok := splitSpeakerRole(role); !ok {
				return fmt.Errorf("speaker %q has invalid role %q", speaker.Name, role)
			}
		}
	}
	return nil
}

func importSpeakersRows(ctx context.Context, pool *pgxpool.Pool, speakers []*types.Speaker) (map[string]string, error) {
	idsByRef := make(map[string]string, len(speakers))
	for _, speaker := range speakers {
		if speaker == nil {
			continue
		}

		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO speakers (
				name, email, norm_photo_path, phone, signal, telegram,
				twitter_handle, nostr, github_url, instagram, linkedin,
				website_url, company, org_logo_path, avail_to_hire,
				looking_to_hire, tshirt
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11,
				$12, $13, $14, $15,
				$16, $17
			)
			RETURNING id::text
		`, strings.TrimSpace(speaker.Name), nullableString(strings.TrimSpace(speaker.Email)), speaker.Photo,
			speaker.Phone, speaker.Signal, speaker.Telegram, speaker.Twitter.Handle, speaker.Nostr,
			speaker.Github, speaker.Instagram, speaker.LinkedIn, speaker.Website, speaker.Company,
			speaker.OrgLogo, speaker.AvailToHire, speaker.LookingToHire, speaker.TShirt).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert speaker %q: %w", speaker.Name, err)
		}
		if speaker.ID != "" {
			idsByRef[speaker.ID] = id
		}

		for _, role := range speaker.Roles {
			scope, position, ok := splitSpeakerRole(role)
			if !ok {
				return nil, fmt.Errorf("speaker %q has invalid role %q", speaker.Name, role)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO speaker_roles (speaker_id, scope, position)
				VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING
			`, id, scope, position); err != nil {
				return nil, fmt.Errorf("insert speaker role %q/%q: %w", speaker.Name, role, err)
			}
		}
	}
	return idsByRef, nil
}

func validateSpeakers(ctx context.Context, pool *pgxpool.Pool, speakers []*types.Speaker) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speakers`).Scan(&count); err != nil {
		return fmt.Errorf("count speakers: %w", err)
	}
	if count < len(speakers) {
		return fmt.Errorf("postgres speaker count %d is less than Notion count %d", count, len(speakers))
	}

	expectedRoles := 0
	for _, speaker := range speakers {
		if speaker != nil {
			expectedRoles += len(speaker.Roles)
		}
	}
	var roleCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speaker_roles`).Scan(&roleCount); err != nil {
		return fmt.Errorf("count speaker roles: %w", err)
	}
	if roleCount < expectedRoles {
		return fmt.Errorf("postgres speaker role count %d is less than Notion role count %d", roleCount, expectedRoles)
	}

	return nil
}

func splitSpeakerRole(role string) (string, string, bool) {
	role = strings.TrimSpace(role)
	idx := strings.LastIndex(role, "-")
	if idx <= 0 || idx == len(role)-1 {
		return "", "", false
	}
	scope := strings.TrimSpace(role[:idx])
	position := strings.TrimSpace(role[idx+1:])
	return scope, position, scope != "" && position != ""
}
