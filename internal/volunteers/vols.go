package volunteers

import (
	"sort"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

const targetShiftsPerVol = 3

// shiftsByPriority sorts shifts by descending Priority (higher first), with
// nil ShiftTime sinking to the bottom.
type shiftsByPriority []*types.WorkShift

func (s shiftsByPriority) Len() int      { return len(s) }
func (s shiftsByPriority) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s shiftsByPriority) Less(i, j int) bool {
	return s[i].Priority > s[j].Priority
}

// volsByShiftCount sorts volunteers ascending by number of currently assigned
// shifts, so volunteers with the most shifts get filled first.
// If two volunteers have an equal number of shifts assigned, the volunteer
// that registered first gets priority.
type volsByShiftCount []*types.Volunteer

func (v volsByShiftCount) Len() int      { return len(v) }
func (v volsByShiftCount) Swap(i, j int) { v[i], v[j] = v[j], v[i] }
func (v volsByShiftCount) Less(i, j int) bool {
        if len(v[i].WorkShifts) == len(v[j].WorkShifts) {
                return v[i].CreatedAt.Before(*v[j].CreatedAt)
        }
	return len(v[i].WorkShifts) > len(v[j].WorkShifts)
}

func findEligibleVols(shift *types.WorkShift, vols []*types.Volunteer, relaxType bool) []*types.Volunteer {
	okvols := make([]*types.Volunteer, 0)

	for _, vol := range vols {
		/* Available on that day */
		if !vol.AvailableOn(shift) {
			continue
		}

		/* Is compatible with job */
		if !relaxType && shift.Type != nil && vol.WillNotWork(shift.Type) {
			continue
		}

		/* is not scheduled for target+ shifts already */
		if len(vol.WorkShifts) >= targetShiftsPerVol {
			continue
		}

		/* is not scheduled for an overlapping shift */
		if shift.Intersects(vol.WorkShifts) {
			continue
		}

		okvols = append(okvols, vol)
	}

	return okvols
}

// AssignFunc is called to persist a shift assignment. The real implementation
// calls getters.AssignVolunteerToShift; tests can substitute a no-op or spy.
type AssignFunc func(volRef, shiftRef string) error

func processShifts(shift *types.WorkShift, eligible []*types.Volunteer, toAssign int, assign AssignFunc) (int, error) {
        for _, vol := range eligible {
                if toAssign == 0 {
                        break
                }
                err := assign(vol.Ref, shift.Ref)
                if err != nil {
                        return toAssign, err
                }
                vol.WorkShifts = append(vol.WorkShifts, shift)
                toAssign--
        }

        return toAssign, nil
}

// assignShiftsCore is the pure algorithmic core. It's separated from the
// Notion-dependent wrapper so it can be unit-tested with a fake AssignFunc.
func assignShiftsCore(vols []*types.Volunteer, shifts []*types.WorkShift, assign AssignFunc) error {
	sort.Sort(shiftsByPriority(shifts))

	for _, shift := range shifts {
		toAssign := int(shift.MaxVols) - len(shift.AssigneesRef)
		if toAssign <= 0 {
			continue
		}

		eligible := findEligibleVols(shift, vols, false)
		sort.Sort(volsByShiftCount(eligible))
                toAssign, _ = processShifts(shift, eligible, toAssign, assign)

                if toAssign <= 0 {
                        continue
                }

                /* relax requirements and try again */
		eligible = findEligibleVols(shift, vols, true)
		sort.Sort(volsByShiftCount(eligible))
                toAssign, _ = processShifts(shift, eligible, toAssign, assign)
	}

	return nil
}

// AssignShifts greedily assigns volunteers to shifts. Shifts are processed in
// descending Priority order. For each shift with open spots, eligible
// volunteers are picked starting with those who have the most shifts so far.
// Each assignment is persisted via getters.AssignVolunteerToShift and the
// in-memory vol.WorkShifts list is updated.
func AssignShifts(ctx *config.AppContext, vols []*types.Volunteer, shifts []*types.WorkShift) error {
	return assignShiftsCore(vols, shifts, func(volRef, shiftRef string) error {
		return getters.AssignVolunteerToShift(ctx, volRef, shiftRef)
	})
}
