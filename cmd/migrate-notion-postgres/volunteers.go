package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type volunteerImportRow struct {
	ref             string
	name            string
	email           string
	phone           string
	signal          string
	availability    []string
	contactAt       string
	comments        string
	discoveredVia   string
	scheduleForRefs []string
	otherEventRefs  []string
	workYesRefs     []string
	workNoRefs      []string
	firstEvent      bool
	hometown        string
	twitterHandle   string
	nostr           string
	shirt           string
	status          string
	createdAt       interface{}
}

func listVolunteerImportRows(n *types.Notion) ([]*volunteerImportRow, error) {
	var out []*volunteerImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.VolunteerDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseVolunteerImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseVolunteerImportRow(ref string, props map[string]notion.PropertyValue) *volunteerImportRow {
	return &volunteerImportRow{
		ref:             ref,
		name:            titleText(props["Name"]),
		email:           props["Email"].Email,
		phone:           props["Phone"].PhoneNumber,
		signal:          richText(props["Signal"]),
		availability:    multiSelectNames(props["Availability"]),
		contactAt:       richText(props["ContactAt"]),
		comments:        richText(props["Comments"]),
		discoveredVia:   richText(props["DiscoveredVia"]),
		scheduleForRefs: relationIDs(props["ScheduleFor"]),
		otherEventRefs:  relationIDs(props["OtherEvents"]),
		workYesRefs:     relationIDs(props["WorkYes"]),
		workNoRefs:      relationIDs(props["WorkNo"]),
		firstEvent:      checkbox(props["FirstEvent"].Checkbox),
		hometown:        richText(props["Hometown"]),
		twitterHandle:   types.ParseTwitter(richText(props["Twitter"])).Handle,
		nostr:           richText(props["npub"]),
		shirt:           selectName(props["Shirt"]),
		status:          selectName(props["Status"]),
		createdAt:       nullableTimePtr(dateStart(props["created"])),
	}
}

func validateVolunteerRows(rows []*volunteerImportRow, confTagByRef map[string]string, jobTypeTagByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.name) == "" {
			return fmt.Errorf("volunteer %q has empty Name", row.ref)
		}
		if strings.TrimSpace(row.email) == "" {
			return fmt.Errorf("volunteer %q has empty Email", row.ref)
		}
		for _, confRef := range append(append([]string{}, row.scheduleForRefs...), row.otherEventRefs...) {
			if confTagByRef[confRef] == "" {
				return fmt.Errorf("volunteer %q has unresolved conference ref %q", row.ref, confRef)
			}
		}
		for _, jobRef := range append(append([]string{}, row.workYesRefs...), row.workNoRefs...) {
			if jobTypeTagByRef[jobRef] == "" {
				return fmt.Errorf("volunteer %q has unresolved job type ref %q", row.ref, jobRef)
			}
		}
	}
	return nil
}

func importVolunteerRows(ctx context.Context, pool *pgxpool.Pool, rows []*volunteerImportRow, confTagByRef map[string]string, jobTypeTagByRef map[string]string) (map[string]string, error) {
	idsByRef := make(map[string]string, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}

		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO volunteers (
				name, email, phone, signal, availability, contact_at,
				comments, discovered_via, first_event, hometown, twitter_handle,
				nostr, shirt, status, created_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11,
				$12, $13, $14, COALESCE($15, now())
			)
			RETURNING id::text
		`, strings.TrimSpace(row.name), strings.TrimSpace(row.email), row.phone, row.signal,
			row.availability, row.contactAt, row.comments, row.discoveredVia, row.firstEvent,
			row.hometown, row.twitterHandle, row.nostr, row.shirt, row.status, row.createdAt).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("insert volunteer %q: %w", row.ref, err)
		}
		if row.ref != "" {
			idsByRef[row.ref] = id
		}

		if err := importVolunteerConferenceLinks(ctx, pool, id, row.scheduleForRefs, "schedule_for", confTagByRef); err != nil {
			return nil, fmt.Errorf("insert volunteer conference links %q: %w", row.ref, err)
		}
		if err := importVolunteerConferenceLinks(ctx, pool, id, row.otherEventRefs, "other_event", confTagByRef); err != nil {
			return nil, fmt.Errorf("insert volunteer other-event links %q: %w", row.ref, err)
		}
		if err := importVolunteerJobTypeLinks(ctx, pool, id, row.workYesRefs, "yes", jobTypeTagByRef); err != nil {
			return nil, fmt.Errorf("insert volunteer yes job links %q: %w", row.ref, err)
		}
		if err := importVolunteerJobTypeLinks(ctx, pool, id, row.workNoRefs, "no", jobTypeTagByRef); err != nil {
			return nil, fmt.Errorf("insert volunteer no job links %q: %w", row.ref, err)
		}
	}
	return idsByRef, nil
}

func importVolunteerConferenceLinks(ctx context.Context, pool *pgxpool.Pool, volunteerID string, confRefs []string, kind string, confTagByRef map[string]string) error {
	for _, confRef := range confRefs {
		confTag := confTagByRef[confRef]
		if confTag == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO volunteers_conferences (volunteer_id, conference_id, kind)
			SELECT $1, id, $3
			FROM conferences
			WHERE tag = $2
			ON CONFLICT DO NOTHING
		`, volunteerID, confTag, kind); err != nil {
			return err
		}
	}
	return nil
}

func importVolunteerJobTypeLinks(ctx context.Context, pool *pgxpool.Pool, volunteerID string, jobRefs []string, preference string, jobTypeTagByRef map[string]string) error {
	for _, jobRef := range jobRefs {
		jobTypeTag := jobTypeTagByRef[jobRef]
		if jobTypeTag == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO volunteers_job_types (volunteer_id, job_type_id, preference)
			SELECT $1, id, $3
			FROM job_types
			WHERE tag = $2
			ON CONFLICT DO NOTHING
		`, volunteerID, jobTypeTag, preference); err != nil {
			return err
		}
	}
	return nil
}

func validateVolunteers(ctx context.Context, pool *pgxpool.Pool, rows []*volunteerImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteers`).Scan(&count); err != nil {
		return fmt.Errorf("count volunteers: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres volunteer count %d is less than Notion count %d", count, len(rows))
	}

	expectedConfLinks := 0
	expectedJobLinks := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		expectedConfLinks += len(row.scheduleForRefs) + len(row.otherEventRefs)
		expectedJobLinks += len(row.workYesRefs) + len(row.workNoRefs)
	}

	var confLinks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteers_conferences`).Scan(&confLinks); err != nil {
		return fmt.Errorf("count volunteer conference links: %w", err)
	}
	if confLinks < expectedConfLinks {
		return fmt.Errorf("postgres volunteer conference link count %d is less than Notion relation count %d", confLinks, expectedConfLinks)
	}

	var jobLinks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteers_job_types`).Scan(&jobLinks); err != nil {
		return fmt.Errorf("count volunteer job type links: %w", err)
	}
	if jobLinks < expectedJobLinks {
		return fmt.Errorf("postgres volunteer job type link count %d is less than Notion relation count %d", jobLinks, expectedJobLinks)
	}
	return nil
}

func jobTypeTagByRef(jobTypes []*types.JobType) map[string]string {
	out := make(map[string]string, len(jobTypes))
	for _, jobType := range jobTypes {
		if jobType == nil || jobType.Ref == "" || jobType.Tag == "" {
			continue
		}
		out[jobType.Ref] = jobType.Tag
	}
	return out
}
