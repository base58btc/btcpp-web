package getters

import (
	"context"
	"fmt"

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
