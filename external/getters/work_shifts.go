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
