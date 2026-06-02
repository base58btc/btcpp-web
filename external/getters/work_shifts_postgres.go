package getters

import (
	"context"
	"fmt"

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
