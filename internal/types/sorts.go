package types

type (
	TalkTime []*Talk
	SessionTime []*Session
	ConfList []*Conf
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
	/* Sort by UID ?? */
	return c[i].UID < c[j].UID
}

