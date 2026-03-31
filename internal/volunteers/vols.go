package volunteers

import (
	"sort"
)

func findEligibleVols(shift *types.WorkShift, vols []*types.Volunteer) []*types.Volunteer {
        okvols := make([]*types.Volunteer, 0) 

        for _, vol := range vols {
                /* Available on that day */
                if !vol.AvailableOn(shift) {
                        continue
                }

                /* Is compatible with job */
                if vol.WillNotWork(shift.Type) {
                        continue
                }
        
                /* is not scheduled for 3+ shifts already */
                if len(vol.WorkShifts) >= 3 {
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

func AssignShifts(vols []*types.Volunteer, shifts *types.WorkShift) (error) {
        /* Sort shifts by priority */
        sort.Sort(shifts)

        for _, shift := range shifts {
                toAssign := shift.MaxVols - len(shift.AssigneesRef)
                if toAssign <= 0 {
                        continue
                }
                eligible := findEligibleVols(vols)
                sort.Sort(eligible)
                for _, vol := range eligible {
                        shift.AssigneesRef = append(shift.AssigneesRef, vol.Ref)           
                        toAssign -= 1
                        if toAssign == 0 {
                                continue
                        }
                }
        }
                
        return nil
}
