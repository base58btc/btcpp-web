package types

type (
	TalkTime    []*Talk
	SessionTime []*Session
	ConfList    []*Conf
        JobsList    []*JobType
        WorkShifts  []*WorkShift
        Volunteers  []*Volunteer
)

func (p TalkTime) Len() int {
	return len(p)
}

func (p TalkTime) Less(i, j int) bool {
	if p[i].Sched == nil {
		return true
	}
	if p[j].Sched == nil {
		return false
	}

	/* Sort by time first */
	if p[i].Sched.Start != p[j].Sched.Start {
		return p[i].Sched.Start.Before(p[j].Sched.Start)
	}

	/* Then we sort by room */
	return p[i].VenueValue() < p[j].VenueValue()
}

func (p TalkTime) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

/* Sort confs */
func (c ConfList) Len() int {
	return len(c)
}

func (c ConfList) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c ConfList) Less(i, j int) bool {
	/* Sort by start date */
	return c[i].StartDate.Before(c[j].StartDate)
}

/* Sort job types for display */
func (jl JobsList) Len() int {
	return len(jl)
}

func (jl JobsList) Swap(i, j int) {
	jl[i], jl[j] = jl[j], jl[i]
}

func (jl JobsList) Less(i, j int) bool {
	/* Sort by start date */
	return jl[i].DisplayOrder < jl[j].DisplayOrder
}

/* Sort job types for display */
func (ws WorkShifts) Len() int {
	return len(ws)
}

func (ws WorkShifts) Swap(i, j int) {
	ws[i], ws[j] = ws[j], ws[i]
}

func (ws WorkShifts) Less(i, j int) bool {
	/* Sort by priority */
	return ws[i].Priority < ws[j].Priority
}

/* Sort volunteers for shifts */
func (vs Volunteers) Len() int {
	return len(vs)
}

func (vs Volunteers) Swap(i, j int) {
	vs[i], vs[j] = vs[j], vs[i]
}

func (vs Volunteers) Less(i, j int) bool {
        v1 := vs[i]
        v2 := vs[j]

        /* Not their first btc++ gets priority */
        if v1.FirstEvent != v2.FirstEvent {
                return v2.FirstEvent
        }
        
        /* Preference: wants to do that job type */
        // TODO: how to insert job type into sort?
        //if v1.WillWork(x) != v2.WillWork(x) {
        //       return v1.WillWork(x)
        //}

        /* Already assigned work? */
        if len(v1.WorkShifts) != len(v2.WorkShifts) {
                /* Preference for volunteer with more jobs */
                return len(v1.WorkShifts) > len(v2.WorkShifts)
        }
        
        /* Date of Application: oldest first */
        // FIXME: not currently parsing
	return len(v1.Availability) < len(v2.Availability)
}
