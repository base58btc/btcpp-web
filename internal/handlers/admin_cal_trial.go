package handlers

import (
	"fmt"
	"net/http"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/types"
)

// TrialCalInvite is a dev-only smoke endpoint for the self-hosted
// ICS calendar pipeline. Generates a synthetic talk against the
// requested conf, renders an ICS, and emails it to the recipient
// with Reply-To: speak@btcpp.dev. Doesn't touch any Notion row,
// so it's safe to fire repeatedly during development.
//
// Usage:
//
//	GET /trial-cal-invite?conf=atx25&email=you@example.com
//
// Defaults to conf=atx25 + the maintainer's inbox so a bare hit
// still works. Gated on the global-admin role + non-prod build —
// production never registers the route.
func TrialCalInvite(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	if ctx.Env.Prod {
		http.Error(w, "trial-cal-invite is dev-only", http.StatusForbidden)
		return
	}

	confTag := r.URL.Query().Get("conf")
	if confTag == "" {
		confTag = "atx25"
	}
	email := r.URL.Query().Get("email")
	if email == "" {
		email = "niftynei@gmail.com"
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		http.Error(w, "load confs: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "unknown conf: "+confTag, http.StatusNotFound)
		return
	}

	// Synthetic talk — we don't read or write any Notion row. The
	// time is "tomorrow at 14:00 in conf's local time"; venue is
	// "one" so it resolves to "Main Stage" via MapVenue.
	loc := conf.Loc()
	startLocal := time.Now().In(loc).Add(24 * time.Hour)
	startLocal = time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(),
		14, 0, 0, 0, loc)
	end := startLocal.Add(30 * time.Minute)
	endPtr := end

	proposal := &types.Proposal{
		ID:          "trial-talk",
		Title:       "[trial] Test ICS calendar invite",
		Description: "Sanity check for the self-hosted calendar pipeline. Safe to ignore.",
	}
	talk := &types.ConfTalk{
		ID:    "trial-conftalk",
		Conf:  conf,
		Sched: &types.Times{Start: startLocal, End: &endPtr},
		Venue: "one",
	}

	uid := ics.NewUID("talk", "trial-"+confTag)
	hash := ics.ContentHash(startLocal, end, conf.Tag, proposal.Title)
	event := ics.BuildTalkEvent(talk, proposal, conf,
		ics.Attendee{Email: email, Name: "Trial Recipient"},
		ics.MethodRequest,
		uid,
		0, // first send → seq=0
		conf.Venue,
	)
	icsBytes := ics.Render(event)

	// Build the Mail by hand instead of going through ExecLetter so
	// the smoke route doesn't depend on a Notion letter existing
	// for the cal-invite tags yet.
	body := fmt.Sprintf("Trial calendar invite for %s\n\n"+
		"Title: %s\nWhen: %s (%s)\nVenue: %s\nUID: %s\nHash: %s\n",
		conf.Desc, proposal.Title,
		startLocal.Format("Mon Jan 2 3:04 PM"),
		conf.Timezone, ics.MapVenue(talk.Venue), uid, hash)
	html, _ := emails.BuildHTMLEmail(ctx, []byte(body))

	mail := &emails.Mail{
		JobKey:  fmt.Sprintf("trial-cal-%d", time.Now().UTC().Unix()),
		Email:   email,
		Title:   fmt.Sprintf("[trial] %s calendar invite", conf.Desc),
		SendAt:  time.Now(),
		ReplyTo: ics.ReplyToTalk,
		TextBody: []byte(body),
		HTMLBody: html,
		Files: []*emails.EmailFile{{
			Bytes:       icsBytes,
			ContentType: "text/calendar; method=REQUEST; charset=utf-8",
			Name:        "invite.ics",
		}},
	}
	if err := emails.ComposeAndSendMail(ctx, mail); err != nil {
		http.Error(w, "send: "+err.Error(), http.StatusInternalServerError)
		ctx.Err.Printf("/trial-cal-invite send: %s", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "queued trial cal invite\n")
	fmt.Fprintf(w, "  conf:   %s (%s)\n", conf.Desc, conf.Tag)
	fmt.Fprintf(w, "  to:     %s\n", email)
	fmt.Fprintf(w, "  start:  %s\n", startLocal.Format(time.RFC1123))
	fmt.Fprintf(w, "  uid:    %s\n", uid)
	fmt.Fprintf(w, "  hash:   %s\n", hash)
	fmt.Fprintf(w, "  ics size: %d bytes\n\n--- ICS ---\n%s\n", len(icsBytes), icsBytes)
}
