package handlers

import (
	"fmt"
	"sort"
	"strconv"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func talkDays(ctx *config.AppContext, conf *types.Conf, talks types.TalkTime) ([]*Day, error) {
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
				Morning:   make([]types.SessionTime, 0),
				Afternoon: make([]types.SessionTime, 0),
				Evening:   make([]types.SessionTime, 0),
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

func talkToSession(talk *types.Talk, conf *types.Conf) *types.Session {
	sesh := &types.Session{
		Name:      talk.Name,
		Speakers:  talk.Speakers,
		TalkPhoto: talk.Clipart,
		Sched:     talk.Sched,
		Type:      talk.Type,
		Venue:     talk.Venue,
		AnchorTag: talk.AnchorTag,
		ConfTag:   conf.Tag,
	}

	if talk.Sched != nil {
		sesh.Len = talk.Sched.LenStr()
		sesh.StartTime = talk.Sched.StartTime()
		sesh.DayTag = talk.Sched.Day()
	}

	return sesh
}


func bucketTalks(conf *types.Conf, talks types.TalkTime) (map[string]types.SessionTime, error) {
	sort.Sort(talks)

	sessions := make(map[string]types.SessionTime)
	for _, talk := range talks {
		session := talkToSession(talk, conf)
		section, ok := sessions[talk.Section]
		if !ok {
			section = make(types.SessionTime, 0)
		}
		section = append(section, session)
		sessions[talk.Section] = section
	}
	return sessions, nil
}

