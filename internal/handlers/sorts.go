package handlers

import (
	"fmt"
	"sort"
	"strconv"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

type (
	talkTime []*types.Talk
	sessionTime []*Session
	confList []*types.Conf
)

func (p talkTime) Len() int {
	return len(p)
}

func (p talkTime) Less(i, j int) bool {
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

func (p talkTime) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

/* Sort confs */
func (c confList) Len() int {
	return len(c)
}

func (c confList) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c confList) Less(i, j int) bool {
	/* Sort by UID ?? */
	return c[i].UID < c[j].UID
}

func talkDays(ctx *config.AppContext, conf *types.Conf, talks talkTime) ([]*Day, error) {
	buckets, err := bucketTalks(conf, talks)
	if err != nil {
		return nil, err
	}
	/* Sort keys alphabetically */
	days := make([]*Day, 0)

	keys := make([]string, len(buckets))
	i := 0
	for k, _ := range buckets {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" {
			ctx.Err.Printf("empty key in set?")
			continue
		}
		v, _ := buckets[k]
		i, err := strconv.Atoi(string(k[0]))
		if err != nil {
			return nil, err
		}
		/* This could go horribly wrong */
		if i > 21 {
			return nil, fmt.Errorf("too many days %d", i)
		}
		for i > len(days) {
			days = append(days, &Day{
				Morning:   make([]sessionTime, 0),
				Afternoon: make([]sessionTime, 0),
				Evening:   make([]sessionTime, 0),
			})
		}

		day := days[i-1]
		switch string(k[len(k)-1]) {
		case "+":
			day.Morning = append(day.Morning, v)
		case "=":
			day.Afternoon = append(day.Afternoon, v)
		case "-":
			day.Evening = append(day.Evening, v)
		}
	}

	return days, nil
}

func bucketTalks(conf *types.Conf, talks talkTime) (map[string]sessionTime, error) {
	sort.Sort(talks)

	sessions := make(map[string]sessionTime)
	for _, talk := range talks {
		session := TalkToSession(talk, conf)
		section, ok := sessions[talk.Section]
		if !ok {
			section = make(sessionTime, 0)
		}
		section = append(section, session)
		sessions[talk.Section] = section
	}
	return sessions, nil
}

