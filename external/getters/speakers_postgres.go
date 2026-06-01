package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listSpeakersPostgres(ctx *config.AppContext) ([]*types.Speaker, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, name, coalesce(email::text, ''), norm_photo_path, phone,
			signal, telegram, twitter_handle, nostr, github_url, instagram,
			linkedin, website_url, company, org_logo_path, avail_to_hire,
			looking_to_hire, tshirt
		FROM people
		ORDER BY lower(name), id
	`)
	if err != nil {
		return nil, fmt.Errorf("query people: %w", err)
	}
	defer rows.Close()

	var speakers []*types.Speaker
	speakerByID := map[string]*types.Speaker{}
	for rows.Next() {
		var speaker types.Speaker
		var twitter string
		err := rows.Scan(
			&speaker.ID,
			&speaker.Name,
			&speaker.Email,
			&speaker.Photo,
			&speaker.Phone,
			&speaker.Signal,
			&speaker.Telegram,
			&twitter,
			&speaker.Nostr,
			&speaker.Github,
			&speaker.Instagram,
			&speaker.LinkedIn,
			&speaker.Website,
			&speaker.Company,
			&speaker.OrgLogo,
			&speaker.AvailToHire,
			&speaker.LookingToHire,
			&speaker.TShirt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan person: %w", err)
		}
		speaker.Twitter = types.ParseTwitter(twitter)
		speakers = append(speakers, &speaker)
		speakerByID[speaker.ID] = &speaker
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate people: %w", err)
	}

	if err := loadSpeakerRolesPostgres(ctx, speakerByID); err != nil {
		return nil, err
	}
	return speakers, nil
}

func getSpeakersByEmailPostgres(ctx *config.AppContext, email string) ([]*types.Speaker, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil
	}
	return querySpeakersPostgres(ctx, "WHERE email = $1", email)
}

func fetchSpeakerByIDPostgres(ctx *config.AppContext, speakerID string) (*types.Speaker, error) {
	speakerID = strings.TrimSpace(speakerID)
	if speakerID == "" {
		return nil, nil
	}
	speakers, err := querySpeakersPostgres(ctx, "WHERE id::text = $1", speakerID)
	if err != nil {
		return nil, err
	}
	if len(speakers) == 0 {
		return nil, nil
	}
	return speakers[0], nil
}

func querySpeakersPostgres(ctx *config.AppContext, where string, args ...interface{}) ([]*types.Speaker, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, name, coalesce(email::text, ''), norm_photo_path, phone,
			signal, telegram, twitter_handle, nostr, github_url, instagram,
			linkedin, website_url, company, org_logo_path, avail_to_hire,
			looking_to_hire, tshirt
		FROM people
		`+where+`
		ORDER BY lower(name), id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query people: %w", err)
	}
	defer rows.Close()

	var speakers []*types.Speaker
	speakerByID := map[string]*types.Speaker{}
	for rows.Next() {
		var speaker types.Speaker
		var twitter string
		err := rows.Scan(
			&speaker.ID,
			&speaker.Name,
			&speaker.Email,
			&speaker.Photo,
			&speaker.Phone,
			&speaker.Signal,
			&speaker.Telegram,
			&twitter,
			&speaker.Nostr,
			&speaker.Github,
			&speaker.Instagram,
			&speaker.LinkedIn,
			&speaker.Website,
			&speaker.Company,
			&speaker.OrgLogo,
			&speaker.AvailToHire,
			&speaker.LookingToHire,
			&speaker.TShirt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan person: %w", err)
		}
		speaker.Twitter = types.ParseTwitter(twitter)
		speakers = append(speakers, &speaker)
		speakerByID[speaker.ID] = &speaker
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate people: %w", err)
	}
	if err := loadSpeakerRolesPostgres(ctx, speakerByID); err != nil {
		return nil, err
	}
	return speakers, nil
}

func createSpeakerPostgres(ctx *config.AppContext, in SpeakerInput) (string, error) {
	if ctx == nil || ctx.DB == nil {
		return "", fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	in = normalizeSpeakerInput(in)
	var id string
	err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO people (
			name, email, norm_photo_path, phone, signal, telegram, twitter_handle,
			nostr, github_url, instagram, linkedin, website_url, company,
			org_logo_path, avail_to_hire, looking_to_hire, tshirt
		)
		VALUES ($1, NULLIF($2, '')::citext, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17)
		RETURNING id::text
	`, in.Name, in.Email, in.Photo, in.Phone, in.Signal, in.Telegram, in.Twitter,
		in.Nostr, in.Github, in.Instagram, in.LinkedIn, in.Website, in.Company,
		in.OrgLogo, in.AvailToHire, in.LookingToHire, in.TShirt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create person: %w", err)
	}
	CacheSpeakerInsert(speakerFromInput(id, in))
	return id, nil
}

func updateSpeakerPostgres(ctx *config.AppContext, speakerID string, up SpeakerUpdate) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	up = normalizeSpeakerUpdate(up)
	_, err := ctx.DB.Exec(context.Background(), `
		UPDATE people
		SET norm_photo_path = CASE WHEN $2 <> '' THEN $2 ELSE norm_photo_path END,
			phone = CASE WHEN $3 <> '' THEN $3 ELSE phone END,
			signal = CASE WHEN $4 <> '' THEN $4 ELSE signal END,
			telegram = CASE WHEN $5 <> '' THEN $5 ELSE telegram END,
			twitter_handle = CASE WHEN $6 <> '' THEN $6 ELSE twitter_handle END,
			nostr = CASE WHEN $7 <> '' THEN $7 ELSE nostr END,
			github_url = CASE WHEN $8 <> '' THEN $8 ELSE github_url END,
			instagram = CASE WHEN $9 <> '' THEN $9 ELSE instagram END,
			linkedin = CASE WHEN $10 <> '' THEN $10 ELSE linkedin END,
			website_url = CASE WHEN $11 <> '' THEN $11 ELSE website_url END,
			company = CASE WHEN $12 <> '' THEN $12 ELSE company END,
			org_logo_path = CASE WHEN $13 <> '' THEN $13 ELSE org_logo_path END,
			tshirt = CASE WHEN $14 <> '' THEN $14 ELSE tshirt END
		WHERE id = $1::uuid
	`, speakerID, up.Photo, up.Phone, up.Signal, up.Telegram, up.Twitter,
		up.Nostr, up.Github, up.Instagram, up.LinkedIn, up.Website, up.Company,
		up.OrgLogo, up.TShirt)
	if err != nil {
		return fmt.Errorf("update person %s: %w", speakerID, err)
	}
	patchCachedSpeaker(speakerID, up)
	return nil
}

func updateSpeakerRolesPostgres(ctx *config.AppContext, speakerID string, roles []string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	tx, err := ctx.DB.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("begin speaker role update: %w", err)
	}
	defer tx.Rollback(context.Background())

	if _, err := tx.Exec(context.Background(), `DELETE FROM people_roles WHERE person_id = $1::uuid`, speakerID); err != nil {
		return fmt.Errorf("delete speaker roles: %w", err)
	}
	for _, role := range roles {
		scope, position, ok := splitRoleScopePosition(role)
		if !ok {
			continue
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO people_roles (person_id, scope, position)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT DO NOTHING
		`, speakerID, scope, position); err != nil {
			return fmt.Errorf("insert speaker role %q: %w", role, err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("commit speaker role update: %w", err)
	}
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == speakerID {
			s.Roles = append(s.Roles[:0], roles...)
			break
		}
	}
	return nil
}

func splitRoleScopePosition(role string) (string, string, bool) {
	role = strings.TrimSpace(role)
	parts := strings.SplitN(role, "-", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	scope := strings.TrimSpace(parts[0])
	position := strings.TrimSpace(parts[1])
	return scope, position, scope != "" && position != ""
}

func loadSpeakerRolesPostgres(ctx *config.AppContext, speakerByID map[string]*types.Speaker) error {
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT person_id::text, scope, position
		FROM people_roles
		ORDER BY scope, position
	`)
	if err != nil {
		return fmt.Errorf("query people roles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var personID string
		var scope string
		var position string
		if err := rows.Scan(&personID, &scope, &position); err != nil {
			return fmt.Errorf("scan people role: %w", err)
		}
		if speaker := speakerByID[personID]; speaker != nil {
			speaker.Roles = append(speaker.Roles, scope+"-"+position)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate people roles: %w", err)
	}
	return nil
}
