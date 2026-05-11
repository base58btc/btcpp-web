package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

// TalkPublicICS serves a downloadable .ics file for a single
// scheduled talk so anyone browsing the conf agenda can add the
// session to their personal calendar. Distinct from the per-
// speaker invite (METHOD:REQUEST, addressed to a specific email):
// this is METHOD:PUBLISH with no ATTENDEE list — a one-way "here's
// an event, opt in if you want to" feed.
//
// UID is namespaced under "agenda-" so a recipient who's also a
// speaker doesn't end up with duplicate calendar entries (the
// speaker invite already populated their calendar under a
// different UID).
//
// Path: GET /{conf}/talk/{anchor}/calendar.ics
//
// Returns 404 when the conf or talk isn't found OR when the
// talk's scheduled end is already in the past — back-dated
// downloads aren't useful and would just clutter calendars.
func TalkPublicICS(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	vars := mux.Vars(r)
	confTag := vars["conf"]
	anchor := vars["anchor"]
	if confTag == "" || anchor == "" {
		http.NotFound(w, r)
		return
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/%s/talk/%s/calendar.ics confs: %s", confTag, anchor, err)
		http.Error(w, "Unable to load conference", http.StatusInternalServerError)
		return
	}
	var conf *types.Conf
	for _, c := range confs {
		if c != nil && c.Tag == confTag {
			conf = c
			break
		}
	}
	if conf == nil {
		http.NotFound(w, r)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/talk/%s/calendar.ics talks: %s", confTag, anchor, err)
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		return
	}

	// Match on the clipart-derived AnchorTag first (pretty URL),
	// fall back to the raw ConfTalk ID so the dashboard's "Add to
	// calendar" download keeps working for Scheduled talks that
	// haven't had a clipart uploaded yet.
	var talk *types.Talk
	for _, t := range talks {
		if t == nil || t.Sched == nil {
			continue
		}
		if t.AnchorTag() == anchor || t.ID == anchor {
			talk = t
			break
		}
	}
	if talk == nil || talk.Sched.End == nil {
		http.NotFound(w, r)
		return
	}
	// Skip past talks — calendar entries in the past aren't
	// useful and inviting people to add stale events would
	// just pollute their calendars.
	if talk.Sched.End.Before(time.Now()) {
		http.NotFound(w, r)
		return
	}

	speakerNames := make([]string, 0, len(talk.Speakers))
	for _, sp := range talk.Speakers {
		if sp != nil && sp.Name != "" {
			speakerNames = append(speakerNames, sp.Name)
		}
	}
	desc := talk.Description
	if len(speakerNames) > 0 {
		prefix := "Speaker: "
		if len(speakerNames) > 1 {
			prefix = "Speakers: "
		}
		desc = prefix + strings.Join(speakerNames, ", ") + "\n\n" + desc
	}

	location := ics.MapVenue(talk.Venue)
	if location == "" {
		location = conf.Venue
	}

	event := ics.Event{
		Method:        ics.MethodPublish,
		UID:           ics.NewUID("agenda", conf.Tag+"-"+anchor),
		Sequence:      0,
		Status:        ics.StatusConfirmed,
		Summary:       talk.Name,
		Description:   desc,
		Location:      location,
		Start:         talk.Sched.Start,
		End:           *talk.Sched.End,
		TZ:            conf.Loc(),
		Organizer:     ics.ReplyToTalk,
		OrganizerName: ics.ReplyToTalkName,
		// Intentionally no Attendees — METHOD:PUBLISH is a
		// public feed, no "addressed to" semantics.
	}
	icsBytes := ics.Render(event)

	filename := fmt.Sprintf("%s-%s.ics", conf.Tag, anchor)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(icsBytes)
}
