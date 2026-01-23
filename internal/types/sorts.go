package types

type (
	TalkTime    []*Talk
	SessionTime []*Session
	ConfList    []*Conf
        JobsList    []*JobType
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
