package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type workShiftImportRow struct {
	ref            string
	name           string
	maxVols        int
	typeRef        string
	confRef        string
	shiftStart     interface{}
	shiftEnd       interface{}
	priority       int
	calNotif       string
	assigneeRefs   []string
	shiftLeaderRef string
}

func listWorkShiftImportRows(n *types.Notion) ([]*workShiftImportRow, error) {
	var out []*workShiftImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.ShiftDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseWorkShiftImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseWorkShiftImportRow(ref string, props map[string]notion.PropertyValue) *workShiftImportRow {
	shiftTime := props["ShiftTime"]
	name := titleText(props["Name"])
	if name == "" {
		name = richText(props["Name"])
	}
	return &workShiftImportRow{
		ref:            ref,
		name:           name,
		maxVols:        int(props["MaxVols"].Number),
		typeRef:        relationID(props["TypeRef"]),
		confRef:        relationID(props["ConfRef"]),
		shiftStart:     nullableTimePtr(dateStart(shiftTime)),
		shiftEnd:       nullableTimePtr(dateEnd(shiftTime)),
		priority:       int(props["Priority"].Number),
		calNotif:       richText(props["CalNotif"]),
		assigneeRefs:   relationIDs(props["Assignees"]),
		shiftLeaderRef: relationID(props["ShiftLeader"]),
	}
}

func validateWorkShiftRows(rows []*workShiftImportRow, confTagByRef, jobTypeTagByRef map[string]string, volunteerRefs map[string]bool) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.name) == "" {
			return fmt.Errorf("work shift %q has empty Name", row.ref)
		}
		if confTagByRef[row.confRef] == "" {
			return fmt.Errorf("work shift %q has unresolved ConfRef %q", row.ref, row.confRef)
		}
		if row.typeRef != "" && jobTypeTagByRef[row.typeRef] == "" {
			return fmt.Errorf("work shift %q has unresolved TypeRef %q", row.ref, row.typeRef)
		}
		for _, volunteerRef := range row.assigneeRefs {
			if !volunteerRefs[volunteerRef] {
				return fmt.Errorf("work shift %q has unresolved Assignees ref %q", row.ref, volunteerRef)
			}
		}
		if row.shiftLeaderRef != "" && !volunteerRefs[row.shiftLeaderRef] {
			return fmt.Errorf("work shift %q has unresolved ShiftLeader ref %q", row.ref, row.shiftLeaderRef)
		}
	}
	return nil
}

func importWorkShiftRows(ctx context.Context, pool *pgxpool.Pool, rows []*workShiftImportRow, confTagByRef, jobTypeTagByRef, volunteerIDsByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		confTag := confTagByRef[row.confRef]
		jobTypeTag := jobTypeTagByRef[row.typeRef]

		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO work_shifts (
				conference_id, job_type_id, name, max_vols, shift_start,
				shift_end, priority, cal_notif
			)
			SELECT c.id, jt.id, $3, $4, $5,
				$6, $7, $8
			FROM conferences c
			LEFT JOIN job_types jt ON jt.tag = $2
			WHERE c.tag = $1
			RETURNING id::text
		`, confTag, nullableString(jobTypeTag), strings.TrimSpace(row.name), row.maxVols,
			row.shiftStart, row.shiftEnd, row.priority, row.calNotif).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert work shift %q: %w", row.ref, err)
		}

		for _, volunteerRef := range row.assigneeRefs {
			volunteerID := volunteerIDsByRef[volunteerRef]
			if volunteerID == "" {
				return fmt.Errorf("work shift %q has unresolved imported assignee %q", row.ref, volunteerRef)
			}
			if err := insertWorkShiftVolunteer(ctx, pool, id, volunteerID, "assignee"); err != nil {
				return fmt.Errorf("insert work shift assignee %q/%q: %w", row.ref, volunteerRef, err)
			}
		}
		if row.shiftLeaderRef != "" {
			volunteerID := volunteerIDsByRef[row.shiftLeaderRef]
			if volunteerID == "" {
				return fmt.Errorf("work shift %q has unresolved imported leader %q", row.ref, row.shiftLeaderRef)
			}
			if err := insertWorkShiftVolunteer(ctx, pool, id, volunteerID, "leader"); err != nil {
				return fmt.Errorf("insert work shift leader %q/%q: %w", row.ref, row.shiftLeaderRef, err)
			}
		}
	}
	return nil
}

func insertWorkShiftVolunteer(ctx context.Context, pool *pgxpool.Pool, shiftID, volunteerID, role string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO work_shifts_volunteers (shift_id, volunteer_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, shiftID, volunteerID, role)
	return err
}

func validateWorkShifts(ctx context.Context, pool *pgxpool.Pool, rows []*workShiftImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_shifts`).Scan(&count); err != nil {
		return fmt.Errorf("count work shifts: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres work shift count %d is less than Notion count %d", count, len(rows))
	}

	expectedLinks := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		expectedLinks += len(row.assigneeRefs)
		if row.shiftLeaderRef != "" {
			expectedLinks++
		}
	}
	var linkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_shifts_volunteers`).Scan(&linkCount); err != nil {
		return fmt.Errorf("count work shift volunteer links: %w", err)
	}
	if linkCount < expectedLinks {
		return fmt.Errorf("postgres work shift volunteer link count %d is less than Notion relation count %d", linkCount, expectedLinks)
	}
	return nil
}

func volunteerRefsByRef(rows []*volunteerImportRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row == nil || row.ref == "" {
			continue
		}
		out[row.ref] = true
	}
	return out
}
