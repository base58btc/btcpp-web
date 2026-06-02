package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func registerVolunteerPostgres(ctx *config.AppContext, vol *types.Volunteer) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if vol == nil {
		return fmt.Errorf("RegisterVolunteer: volunteer is nil")
	}
	normalizeVolunteerInput(vol)
	if len(vol.ScheduleFor) == 0 || vol.ScheduleFor[0] == nil || vol.ScheduleFor[0].Ref == "" {
		return fmt.Errorf("RegisterVolunteer: ScheduleFor required")
	}

	status := vol.Status
	if status == "" {
		status = "Applied"
	}

	tx, err := ctx.DB.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("begin volunteer registration: %w", err)
	}
	defer tx.Rollback(context.Background())

	var volunteerID string
	err = tx.QueryRow(context.Background(), `
		INSERT INTO volunteers (
			name, email, phone, signal, availability, contact_at, comments,
			discovered_via, first_event, hometown, twitter_handle, nostr,
			shirt, status, captcha, subscribe
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
		RETURNING id::text
	`, vol.Name, vol.Email, vol.Phone, vol.Signal, vol.Availability,
		vol.ContactAt, vol.Comments, vol.DiscoveredVia, vol.FirstEvent,
		vol.Hometown, vol.Twitter.Handle, vol.Nostr, vol.Shirt, status,
		vol.Captcha, vol.Subscribe).Scan(&volunteerID)
	if err != nil {
		return fmt.Errorf("insert volunteer %q: %w", vol.Email, err)
	}

	if err := insertVolunteerConferenceLinksPostgres(tx, volunteerID, vol.ScheduleFor, "schedule_for"); err != nil {
		return err
	}
	if err := insertVolunteerConferenceLinksPostgres(tx, volunteerID, vol.OtherEvents, "other_event"); err != nil {
		return err
	}
	if err := insertVolunteerJobLinksPostgres(tx, volunteerID, vol.WorkYes, "yes"); err != nil {
		return err
	}
	if err := insertVolunteerJobLinksPostgres(tx, volunteerID, vol.WorkNo, "no"); err != nil {
		return err
	}

	if err := tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("commit volunteer registration: %w", err)
	}
	vol.Ref = volunteerID
	vol.Status = status
	return nil
}

func getVolInfosPostgres(ctx *config.AppContext, confRef string) ([]*types.VolInfo, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}

	args := []interface{}{}
	where := ""
	if confRef != "" {
		args = append(args, confRef)
		where = "WHERE volunteer_info.conference_id::text = $1"
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT volunteer_info.id::text, volunteer_info.conference_id::text,
			volunteer_info.orient_link_url, volunteer_info.orient_start,
			volunteer_info.orient_end, volunteer_info.notes
		FROM volunteer_info
		`+where+`
		ORDER BY volunteer_info.created_at
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query volunteer info: %w", err)
	}
	defer rows.Close()

	var out []*types.VolInfo
	for rows.Next() {
		var info types.VolInfo
		var orientStart pgtype.Timestamptz
		var orientEnd pgtype.Timestamptz
		if err := rows.Scan(&info.Ref, &info.ConfRef, &info.OrientLink, &orientStart, &orientEnd, &info.Notes); err != nil {
			return nil, fmt.Errorf("scan volunteer info: %w", err)
		}
		if orientStart.Valid {
			info.OrientTimes = &types.Times{Start: orientStart.Time}
			if orientEnd.Valid {
				info.OrientTimes.End = &orientEnd.Time
			}
		}
		out = append(out, &info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate volunteer info: %w", err)
	}
	return out, nil
}

func listVolunteerAppsPostgres(ctx *config.AppContext, email string) ([]*types.Volunteer, error) {
	where := ""
	args := []interface{}{}
	if strings.TrimSpace(email) != "" {
		args = append(args, email)
		where = "WHERE email = $1"
	}
	return listVolunteersPostgres(ctx, where, args...)
}

func fetchVolunteerPostgres(ctx *config.AppContext, volRef string) (*types.Volunteer, error) {
	vols, err := listVolunteersPostgres(ctx, "WHERE id::text = $1", volRef)
	if err != nil {
		return nil, err
	}
	if len(vols) == 0 {
		return nil, fmt.Errorf("volunteer %s not found", volRef)
	}
	return vols[0], nil
}

func listVolunteersForConfPostgres(ctx *config.AppContext, confRef string) ([]*types.Volunteer, error) {
	return listVolunteersPostgres(ctx, `
		WHERE id IN (
			SELECT volunteer_id
			FROM volunteers_conferences
			WHERE conference_id::text = $1 AND kind = 'schedule_for'
		)
	`, confRef)
}

func listVolunteersPostgres(ctx *config.AppContext, where string, args ...interface{}) ([]*types.Volunteer, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, name, email::text, phone, signal, availability, contact_at,
			comments, discovered_via, first_event, hometown, twitter_handle, nostr,
			shirt, status, captcha, subscribe, created_at
		FROM volunteers
		`+where+`
		ORDER BY created_at DESC, name
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query volunteers: %w", err)
	}
	defer rows.Close()

	var out []*types.Volunteer
	for rows.Next() {
		var vol types.Volunteer
		var twitterHandle string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(
			&vol.Ref,
			&vol.Name,
			&vol.Email,
			&vol.Phone,
			&vol.Signal,
			&vol.Availability,
			&vol.ContactAt,
			&vol.Comments,
			&vol.DiscoveredVia,
			&vol.FirstEvent,
			&vol.Hometown,
			&twitterHandle,
			&vol.Nostr,
			&vol.Shirt,
			&vol.Status,
			&vol.Captcha,
			&vol.Subscribe,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan volunteer: %w", err)
		}
		vol.Twitter = types.ParseTwitter(twitterHandle)
		if createdAt.Valid {
			vol.CreatedAt = &createdAt.Time
		}
		out = append(out, &vol)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate volunteers: %w", err)
	}
	if err := hydrateVolunteerRelationsPostgres(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func hydrateVolunteerRelationsPostgres(ctx *config.AppContext, vols []*types.Volunteer) error {
	if len(vols) == 0 {
		return nil
	}
	volByID := make(map[string]*types.Volunteer, len(vols))
	ids := make([]string, 0, len(vols))
	for _, vol := range vols {
		if vol == nil {
			continue
		}
		volByID[vol.Ref] = vol
		ids = append(ids, vol.Ref)
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
	if err := hydrateVolunteerConferenceRelationsPostgres(ctx, ids, volByID, confByID); err != nil {
		return err
	}

	jobs, err := FetchJobsCached(ctx)
	if err != nil {
		return err
	}
	jobByID := make(map[string]*types.JobType, len(jobs))
	for _, job := range jobs {
		if job != nil {
			jobByID[job.Ref] = job
		}
	}
	return hydrateVolunteerJobRelationsPostgres(ctx, ids, volByID, jobByID)
}

func hydrateVolunteerConferenceRelationsPostgres(ctx *config.AppContext, ids []string, volByID map[string]*types.Volunteer, confByID map[string]*types.Conf) error {
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT volunteer_id::text, conference_id::text, kind
		FROM volunteers_conferences
		WHERE volunteer_id::text = ANY($1::text[])
		ORDER BY kind
	`, ids)
	if err != nil {
		return fmt.Errorf("query volunteer conference links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var volunteerID string
		var confID string
		var kind string
		if err := rows.Scan(&volunteerID, &confID, &kind); err != nil {
			return fmt.Errorf("scan volunteer conference link: %w", err)
		}
		vol := volByID[volunteerID]
		conf := confByID[confID]
		if vol == nil || conf == nil {
			continue
		}
		switch kind {
		case "schedule_for":
			vol.ScheduleFor = append(vol.ScheduleFor, conf)
		case "other_event":
			vol.OtherEvents = append(vol.OtherEvents, conf)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate volunteer conference links: %w", err)
	}
	return nil
}

func insertVolunteerConferenceLinksPostgres(tx pgx.Tx, volunteerID string, confs []*types.Conf, kind string) error {
	for _, conf := range confs {
		if conf == nil || strings.TrimSpace(conf.Ref) == "" {
			continue
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO volunteers_conferences (volunteer_id, conference_id, kind)
			VALUES ($1, $2, $3)
			ON CONFLICT (volunteer_id, conference_id, kind) DO NOTHING
		`, volunteerID, conf.Ref, kind); err != nil {
			return fmt.Errorf("insert volunteer conference link %s/%s/%s: %w", volunteerID, conf.Ref, kind, err)
		}
	}
	return nil
}

func insertVolunteerJobLinksPostgres(tx pgx.Tx, volunteerID string, jobs []*types.JobType, preference string) error {
	for _, job := range jobs {
		if job == nil || strings.TrimSpace(job.Ref) == "" {
			continue
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO volunteers_job_types (volunteer_id, job_type_id, preference)
			VALUES ($1, $2, $3)
			ON CONFLICT (volunteer_id, job_type_id, preference) DO NOTHING
		`, volunteerID, job.Ref, preference); err != nil {
			return fmt.Errorf("insert volunteer job link %s/%s/%s: %w", volunteerID, job.Ref, preference, err)
		}
	}
	return nil
}

func hydrateVolunteerJobRelationsPostgres(ctx *config.AppContext, ids []string, volByID map[string]*types.Volunteer, jobByID map[string]*types.JobType) error {
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT volunteer_id::text, job_type_id::text, preference
		FROM volunteers_job_types
		WHERE volunteer_id::text = ANY($1::text[])
		ORDER BY preference
	`, ids)
	if err != nil {
		return fmt.Errorf("query volunteer job links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var volunteerID string
		var jobID string
		var preference string
		if err := rows.Scan(&volunteerID, &jobID, &preference); err != nil {
			return fmt.Errorf("scan volunteer job link: %w", err)
		}
		vol := volByID[volunteerID]
		job := jobByID[jobID]
		if vol == nil || job == nil {
			continue
		}
		switch preference {
		case "yes":
			vol.WorkYes = append(vol.WorkYes, job)
		case "no":
			vol.WorkNo = append(vol.WorkNo, job)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate volunteer job links: %w", err)
	}
	return nil
}
