package getters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgtype"
)

func listWorkShiftsPostgres(ctx *config.AppContext) ([]*types.WorkShift, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}

	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}
	confByID := make(map[string]*types.Conf, len(confs))
	for _, conf := range confs {
		if conf != nil {
			confByID[conf.Ref] = conf
		}
	}

	jobTypes, err := FetchJobsCached(ctx)
	if err != nil {
		return nil, err
	}
	jobByID := make(map[string]*types.JobType, len(jobTypes))
	for _, job := range jobTypes {
		if job != nil {
			jobByID[job.Ref] = job
		}
	}

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT work_shifts.id::text, work_shifts.conference_id::text,
			coalesce(work_shifts.job_type_id::text, ''), work_shifts.name,
			work_shifts.max_vols, work_shifts.shift_start, work_shifts.shift_end,
			work_shifts.priority, work_shifts.cal_notif,
			coalesce(array_agg(work_shifts_volunteers.volunteer_id::text)
				FILTER (WHERE work_shifts_volunteers.role = 'assignee'), '{}'),
			coalesce(max(work_shifts_volunteers.volunteer_id::text)
				FILTER (WHERE work_shifts_volunteers.role = 'leader'), '')
		FROM work_shifts
		LEFT JOIN work_shifts_volunteers ON work_shifts_volunteers.shift_id = work_shifts.id
		GROUP BY work_shifts.id
		ORDER BY work_shifts.shift_start NULLS LAST, work_shifts.priority, work_shifts.name
	`)
	if err != nil {
		return nil, fmt.Errorf("query work shifts: %w", err)
	}
	defer rows.Close()

	var out []*types.WorkShift
	for rows.Next() {
		var shift types.WorkShift
		var confID string
		var jobTypeID string
		var maxVols, priority int64
		var shiftStart pgtype.Timestamptz
		var shiftEnd pgtype.Timestamptz
		err := rows.Scan(
			&shift.Ref,
			&confID,
			&jobTypeID,
			&shift.Name,
			&maxVols,
			&shiftStart,
			&shiftEnd,
			&priority,
			&shift.CalNotif,
			&shift.AssigneesRef,
			&shift.ShiftLeaderRef,
		)
		if err != nil {
			return nil, fmt.Errorf("scan work shift: %w", err)
		}

		shift.MaxVols = uint(maxVols)
		shift.Priority = uint(priority)
		shift.Conf = confByID[confID]
		shift.Type = jobByID[jobTypeID]
		if shiftStart.Valid {
			shift.ShiftTime = &types.Times{Start: shiftStart.Time}
			if shiftEnd.Valid {
				shift.ShiftTime.End = &shiftEnd.Time
			}
		}
		out = append(out, &shift)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work shifts: %w", err)
	}
	return out, nil
}

func shiftUpdateCalNotifPostgres(ctx *config.AppContext, shiftID string, calnotif string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE work_shifts
		SET cal_notif = $2
		WHERE id = $1
	`, shiftID, calnotif)
	if err != nil {
		return fmt.Errorf("update work shift %s cal notif: %w", shiftID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("work shift %s not found", shiftID)
	}
	for _, shift := range shifts {
		if shift != nil && shift.Ref == shiftID {
			shift.CalNotif = calnotif
			break
		}
	}
	return nil
}

func createShiftPostgres(ctx *config.AppContext, conf *types.Conf, jobType *types.JobType, name string, start, end time.Time, maxVols, priority uint) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if conf == nil || conf.Ref == "" {
		return fmt.Errorf("CreateShift: conf is nil or has empty ref")
	}
	var jobTypeID any
	if jobType != nil {
		if jobType.Ref == "" {
			return fmt.Errorf("CreateShift: job type has empty ref")
		}
		jobTypeID = jobType.Ref
	}

	var shiftStart any
	if !start.IsZero() {
		shiftStart = start
	}
	var shiftEnd any
	if !end.IsZero() {
		shiftEnd = end
	}

	_, err := ctx.DB.Exec(context.Background(), `
		INSERT INTO work_shifts (
			conference_id, job_type_id, name, max_vols, shift_start, shift_end, priority
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, conf.Ref, jobTypeID, name, int64(maxVols), shiftStart, shiftEnd, int64(priority))
	if err != nil {
		return fmt.Errorf("create work shift %q: %w", name, err)
	}
	invalidateShiftCache()
	return nil
}

func updateShiftTimesPostgres(ctx *config.AppContext, shiftRef string, start, end time.Time) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if start.IsZero() {
		return fmt.Errorf("UpdateShiftTimes: start required")
	}
	var shiftEnd any
	if !end.IsZero() {
		shiftEnd = end
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE work_shifts
		SET shift_start = $2, shift_end = $3
		WHERE id = $1
	`, shiftRef, start, shiftEnd)
	if err != nil {
		return fmt.Errorf("update work shift %s times: %w", shiftRef, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("work shift %s not found", shiftRef)
	}
	refreshShiftCache(ctx, "UpdateShiftTimes")
	return nil
}

func updateShiftPostgres(ctx *config.AppContext, shiftRef, name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}

	args := []any{shiftRef, name, int64(maxVols), int64(priority)}
	sets := []string{"name = $2", "max_vols = $3", "priority = $4"}
	if jobType != nil {
		if jobType.Ref == "" {
			return fmt.Errorf("UpdateShift: job type has empty ref")
		}
		args = append(args, jobType.Ref)
		sets = append(sets, fmt.Sprintf("job_type_id = $%d", len(args)))
	}
	if !start.IsZero() {
		args = append(args, start)
		sets = append(sets, fmt.Sprintf("shift_start = $%d", len(args)))
		args = append(args, nullableShiftTime(end))
		sets = append(sets, fmt.Sprintf("shift_end = $%d", len(args)))
	}

	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE work_shifts
		SET `+strings.Join(sets, ", ")+`
		WHERE id = $1
	`, args...)
	if err != nil {
		return fmt.Errorf("update work shift %s: %w", shiftRef, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("work shift %s not found", shiftRef)
	}
	invalidateShiftCache()
	return nil
}

func assignVolunteerToShiftPostgres(ctx *config.AppContext, volRef, shiftRef string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	_, err := ctx.DB.Exec(context.Background(), `
		INSERT INTO work_shifts_volunteers (shift_id, volunteer_id, role)
		VALUES ($1, $2, 'assignee')
		ON CONFLICT DO NOTHING
	`, shiftRef, volRef)
	if err != nil {
		return fmt.Errorf("assign volunteer %s to shift %s: %w", volRef, shiftRef, err)
	}
	patchShiftAssigneeCache(shiftRef, func(refs []string) []string {
		for _, ref := range refs {
			if ref == volRef {
				return refs
			}
		}
		return append(refs, volRef)
	})
	return nil
}

func removeVolunteerFromShiftPostgres(ctx *config.AppContext, volRef, shiftRef string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	_, err := ctx.DB.Exec(context.Background(), `
		DELETE FROM work_shifts_volunteers
		WHERE shift_id = $1 AND volunteer_id = $2 AND role = 'assignee'
	`, shiftRef, volRef)
	if err != nil {
		return fmt.Errorf("remove volunteer %s from shift %s: %w", volRef, shiftRef, err)
	}
	patchShiftAssigneeCache(shiftRef, func(refs []string) []string {
		next := make([]string, 0, len(refs))
		for _, ref := range refs {
			if ref != volRef {
				next = append(next, ref)
			}
		}
		return next
	})
	return nil
}

func nullableShiftTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func patchShiftAssigneeCache(shiftRef string, patch func([]string) []string) {
	for _, shift := range shifts {
		if shift != nil && shift.Ref == shiftRef {
			shift.AssigneesRef = patch(shift.AssigneesRef)
			return
		}
	}
}
