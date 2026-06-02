package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func getShifts(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting shifts...")
	if UsePostgresBackend(ctx) {
		shifts, err = listWorkShiftsPostgres(ctx)
	} else {
		shifts, err = ListWorkShiftsNotion(ctx)
	}

	if err != nil {
		ctx.Err.Printf("error fetching shifts %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d shifts!", len(shifts))
		writeCache("shifts", shifts)
	}
}

/* This may return nil */
func FetchShiftsCached(ctx *config.AppContext) ([]*types.WorkShift, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if shifts == nil || lastShiftFetch.Before(deadline) {
		lastShiftFetch = time.Now()
		queueRefresh(JobShifts)
	}

	return shifts, nil
}

func ListWorkShifts(ctx *config.AppContext) ([]*types.WorkShift, error) {
	if UsePostgresBackend(ctx) {
		return listWorkShiftsPostgres(ctx)
	}
	return ListWorkShiftsNotion(ctx)
}

func GetShiftsForConf(ctx *config.AppContext, confTag string) ([]*types.WorkShift, error) {
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return nil, err
	}

	var filtered []*types.WorkShift
	for _, shift := range allShifts {
		if shift.Conf != nil && shift.Conf.Tag == confTag {
			filtered = append(filtered, shift)
		}
	}
	return filtered, nil
}

func ShiftUpdateCalNotif(ctx *config.AppContext, shiftID string, calnotif string) error {
	if UsePostgresBackend(ctx) {
		return shiftUpdateCalNotifPostgres(ctx, shiftID, calnotif)
	}
	return shiftUpdateCalNotifNotion(ctx.Notion, shiftID, calnotif)
}

func CreateShift(ctx *config.AppContext, conf *types.Conf, jobType *types.JobType, name string, start, end time.Time, maxVols, priority uint) error {
	if UsePostgresBackend(ctx) {
		return createShiftPostgres(ctx, conf, jobType, name, start, end, maxVols, priority)
	}
	return CreateShiftNotion(ctx, conf, jobType, name, start, end, maxVols, priority)
}

func UpdateShiftTimes(ctx *config.AppContext, shiftRef string, start, end time.Time) error {
	if UsePostgresBackend(ctx) {
		return updateShiftTimesPostgres(ctx, shiftRef, start, end)
	}
	return UpdateShiftTimesNotion(ctx, shiftRef, start, end)
}

func UpdateShift(ctx *config.AppContext, shiftRef, name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) error {
	if UsePostgresBackend(ctx) {
		return updateShiftPostgres(ctx, shiftRef, name, jobType, start, end, maxVols, priority)
	}
	return UpdateShiftNotion(ctx, shiftRef, name, jobType, start, end, maxVols, priority)
}

func AssignVolunteerToShift(ctx *config.AppContext, volRef, shiftRef string) error {
	if UsePostgresBackend(ctx) {
		return assignVolunteerToShiftPostgres(ctx, volRef, shiftRef)
	}
	return AssignVolunteerToShiftNotion(ctx, volRef, shiftRef)
}

func RemoveVolunteerFromShift(ctx *config.AppContext, volRef, shiftRef string) error {
	if UsePostgresBackend(ctx) {
		return removeVolunteerFromShiftPostgres(ctx, volRef, shiftRef)
	}
	return RemoveVolunteerFromShiftNotion(ctx, volRef, shiftRef)
}

// invalidateShiftCache forces the next FetchShiftsCached call to refetch.
func invalidateShiftCache() {
	shifts = nil
	lastShiftFetch = time.Time{}
}

func refreshShiftCache(ctx *config.AppContext, caller string) {
	fresh, err := ListWorkShifts(ctx)
	if err == nil {
		shifts = fresh
		lastShiftFetch = time.Now()
		writeCache("shifts", shifts)
		return
	}
	ctx.Err.Printf("%s: cache reload (continuing): %s", caller, err)
}
