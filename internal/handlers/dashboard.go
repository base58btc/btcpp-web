package handlers

import (
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// Dashboard is the magic-link-authed self-service page combining a speaker's
// talk applications + their volunteer shift signups.
//
// GET without valid HMAC → renders the email-entry form.
// GET with valid HMAC → loads SpeakerConfs + VolunteerApps for the email.
// POST with email → emails a magic link, redirects home.
func Dashboard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		dec := newFormDecoder()
		var form EmailForm
		if err := dec.Decode(&form, r.PostForm); err != nil {
			ctx.Err.Printf("/dashboard form decode failed: %s", err)
			w.Write([]byte(helpers.ErrVolApp("Unable to send you email link.")))
			return
		}
		if _, err := emails.OnlyForLogin(ctx, form.Email); err != nil {
			http.Error(w, "Unable to send login link via email", http.StatusInternalServerError)
			ctx.Err.Printf("/dashboard onlyforlogin failed: %s", err)
			return
		}
		http.Redirect(w, r, "/dashboard?flash="+url.QueryEscape("Check your inbox — we sent you a login link."), http.StatusSeeOther)
		return
	}

	email, encodedHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		ctx.Infos.Printf("/dashboard HMAC validation failed: %s", err)
		renderDashboardLogin(w, r, ctx)
		return
	}
	encodedEmail := r.URL.Query().Get("em")

	dashStart := time.Now()
	defer func() {
		ctx.Infos.Printf("/dashboard total: %s", time.Since(dashStart))
	}()

	// Top-level fan-out: speakerconfs + volunteer apps + user's tickets
	// are all independent.
	var (
		speakers     []*types.Speaker
		speakerConfs []*types.SpeakerConf
		scErr        error
		volapps      []*types.Volunteer
		volErr       error
		regs         []*types.Registration
		regErr       error
	)
	t1 := time.Now()
	var topWg sync.WaitGroup
	topWg.Add(3)
	var scDur, volDur, regDur time.Duration
	go func() {
		defer topWg.Done()
		s := time.Now()
		speakers, speakerConfs, scErr = getters.GetSpeakerConfsByEmail(ctx, email)
		scDur = time.Since(s)
	}()
	go func() {
		defer topWg.Done()
		s := time.Now()
		volapps, volErr = getters.ListVolunteerApps(ctx, email)
		volDur = time.Since(s)
	}()
	go func() {
		defer topWg.Done()
		s := time.Now()
		regs, regErr = getters.ListRegistrationsByEmail(ctx, email)
		regDur = time.Since(s)
	}()
	topWg.Wait()
	ctx.Infos.Printf("/dashboard fetch wall=%s (sc=%s vol=%s reg=%s) → speakers=%d speakerConfs=%d volapps=%d regs=%d",
		time.Since(t1), scDur, volDur, regDur, len(speakers), len(speakerConfs), len(volapps), len(regs))
	if regErr != nil {
		ctx.Err.Printf("/dashboard listregs failed (continuing): %s", regErr)
	}

	if scErr != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/dashboard speakerconfs failed: %s", scErr)
		return
	}
	if volErr != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/dashboard listvolunteerapps failed: %s", volErr)
		return
	}

	// Volunteer-side: VolInfo + per-vol shifts can all run in parallel.
	var volInfosByConf map[string]*types.VolInfo
	if len(volapps) > 0 {
		t2 := time.Now()
		var volInfoErr error
		var volInfoDur time.Duration
		var volWg sync.WaitGroup
		volWg.Add(1)
		go func() {
			defer volWg.Done()
			s := time.Now()
			volInfosByConf, volInfoErr = getters.GetVolInfoMap(ctx)
			volInfoDur = time.Since(s)
		}()
		shiftDurs := make([]time.Duration, len(volapps))
		for i, vol := range volapps {
			if len(vol.ScheduleFor) == 0 {
				continue
			}
			volWg.Add(1)
			go func(i int, vol *types.Volunteer) {
				defer volWg.Done()
				s := time.Now()
				confTag := vol.ScheduleFor[0].Tag
				confShifts, err := getters.GetShiftsForConf(ctx, confTag)
				if err != nil {
					ctx.Err.Printf("/dashboard get shifts for %s failed: %s", confTag, err)
					return
				}
				vol.WorkShifts = getSelectedShifts(vol, confShifts)
				shiftDurs[i] = time.Since(s)
			}(i, vol)
		}
		volWg.Wait()
		var maxShift time.Duration
		for _, d := range shiftDurs {
			if d > maxShift {
				maxShift = d
			}
		}
		ctx.Infos.Printf("/dashboard fetch (vol) wall=%s (volinfo=%s slowest-shift=%s of %d)",
			time.Since(t2), volInfoDur, maxShift, len(volapps))
		if volInfoErr != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/dashboard getvolinfomap failed: %s", volInfoErr)
			return
		}
	}

	name, hometown := dashboardIdentity(speakers, speakerConfs, volapps)
	var photo string
	if len(speakers) > 0 {
		photo = speakers[0].Photo
	}
	stats := calcDashboardStats(speakerConfs, volapps)

	tConfs := time.Now()
	confs := listConfs(w, ctx)
	ctx.Infos.Printf("/dashboard listConfs: %s", time.Since(tConfs))

	t3 := time.Now()
	enrichDashboardProposals(ctx, speakerConfs)
	ctx.Infos.Printf("/dashboard enrich proposals: %s", time.Since(t3))

	activeSC, pastSC := splitSpeakerConfsByEnded(speakerConfs)
	activeVol, pastVol := splitVolAppsByEnded(volapps)
	ctx.Infos.Printf("/dashboard split → activeSC=%d pastSC=%d activeVol=%d pastVol=%d",
		len(activeSC), len(pastSC), len(activeVol), len(pastVol))
	eligible := eligibleApplyConfs(confs, speakerConfs)
	buyable := buyableConfs(confs)
	tickets := upcomingTickets(regs, confs)

	activeBlocks, pastBlocks := buildEventBlocks(speakerConfs, volapps, tickets, regs, confs, volInfosByConf)
	// Discover sections at the bottom of the page list confs the user
	// has *no* existing relationship with. Anything already showing as
	// an event block is filtered out so we don't list it twice.
	eligible = excludeConfsInBlocks(eligible, activeBlocks)
	buyable = excludeConfsInBlocks(buyable, activeBlocks)
	ctx.Infos.Printf("/dashboard blocks → active=%d past=%d eligible=%d buyable=%d",
		len(activeBlocks), len(pastBlocks), len(eligible), len(buyable))

	tRender := time.Now()
	err = ctx.TemplateCache.ExecuteTemplate(w, "dashboard.tmpl", &DashboardPage{
		Name:             name,
		Hometown:         hometown,
		Photo:            photo,
		Email:            encodedEmail,
		HMAC:             encodedHMAC,
		SpeakerConfs:     activeSC,
		PastSpeakerConfs: pastSC,
		VolApps:          activeVol,
		PastVolApps:      pastVol,
		VolInfos:         volInfosByConf,
		Stats:            stats,
		Confs:            confs,
		EligibleConfs:    eligible,
		BuyableConfs:     buyable,
		Tickets:          tickets,
		ActiveBlocks:     activeBlocks,
		PastBlocks:       pastBlocks,
		FlashMessage:     r.URL.Query().Get("flash"),
		Year:             helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/dashboard ExecuteTemplate failed: %s", err)
		return
	}
	ctx.Infos.Printf("/dashboard render: %s", time.Since(tRender))
}

func renderDashboardLogin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_login.tmpl", &DashboardPage{
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/dashboard render login failed: %s", err)
	}
}

// dashboardIdentity picks a name + hometown to greet the user with. Prefers
// the Speaker record (its Name is curated) and falls back to the first
// volunteer app. Hometown lives on SpeakerConf (ComingFrom) for speakers
// and directly on Volunteer for shift workers.
func dashboardIdentity(speakers []*types.Speaker, speakerConfs []*types.SpeakerConf, volapps []*types.Volunteer) (string, string) {
	if len(speakers) > 0 && speakers[0].Name != "" {
		name := speakers[0].Name
		hometown := ""
		for _, sc := range speakerConfs {
			if sc.ComingFrom != "" {
				hometown = sc.ComingFrom
				break
			}
		}
		if hometown == "" && len(volapps) > 0 {
			hometown = volapps[0].Hometown
		}
		return name, hometown
	}
	if len(volapps) > 0 {
		return volapps[0].Name, volapps[0].Hometown
	}
	return "there", ""
}

// enrichDashboardProposals walks every proposal across the user's
// SpeakerConfs and attaches the data needed by the talk card:
//
//   - proposal.Speakers: full SpeakerConf+Speaker for every speaker on the
//     proposal (so we can render avatars).
//   - proposal.ConfTalk: the ConfTalk row for accepted proposals (Clipart).
//   - proposal.Recording: the Recording row when one exists (YT link).
//
// Two-phase to keep everything parallel: first fan-out fetch every unique
// co-speaker's SpeakerConf+Speaker, then fan-out per-proposal enrich
// (ConfTalk → Recording is a serial chain within each proposal goroutine).
//
// Best-effort — individual fetches that fail just leave the field nil. The
// dashboard renders without that piece rather than 500ing.
func enrichDashboardProposals(ctx *config.AppContext, speakerConfs []*types.SpeakerConf) {
	scCache := make(map[string]*types.SpeakerConf)
	// Seed the cache with the user's own SpeakerConfs (their Speaker is
	// already resolved by GetSpeakerConfsByEmail) so we don't re-fetch.
	for _, sc := range speakerConfs {
		if sc != nil {
			scCache[sc.ID] = sc
		}
	}

	// Walk proposals once to collect unique work items + which proposals
	// to enrich. Avoids enriching the same proposal twice when shared
	// across the user's SpeakerConfs (rare, but cheap to defend).
	uniqueRefs := make(map[string]struct{})
	seenProp := make(map[string]bool)
	var proposals []*types.Proposal
	for _, sc := range speakerConfs {
		for _, p := range sc.Proposals {
			if p == nil || seenProp[p.ID] {
				continue
			}
			seenProp[p.ID] = true
			proposals = append(proposals, p)
			for _, ref := range p.SpeakerConfRefs {
				if _, ok := scCache[ref]; ok {
					continue
				}
				uniqueRefs[ref] = struct{}{}
			}
		}
	}

	// Phase 1: parallel-fetch every unique co-speaker SpeakerConf.
	t1 := time.Now()
	if len(uniqueRefs) > 0 {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for ref := range uniqueRefs {
			wg.Add(1)
			go func(ref string) {
				defer wg.Done()
				sc, err := getters.FetchSpeakerConfWithSpeaker(ctx, ref)
				if err != nil {
					ctx.Err.Printf("enrich: fetch sc %s: %s", ref, err)
					return
				}
				mu.Lock()
				scCache[ref] = sc
				mu.Unlock()
			}(ref)
		}
		wg.Wait()
	}
	ctx.Infos.Printf("enrich phase1 (%d co-speaker scs): %s", len(uniqueRefs), time.Since(t1))

	// Phase 2: parallel-enrich each proposal. Cache is now read-only —
	// each goroutine attaches its own ConfTalk + Recording chain.
	t2 := time.Now()
	var wg sync.WaitGroup
	for _, p := range proposals {
		wg.Add(1)
		go func(p *types.Proposal) {
			defer wg.Done()
			enrichProposal(ctx, p, scCache)
		}(p)
	}
	wg.Wait()
	ctx.Infos.Printf("enrich phase2 (%d proposals): %s", len(proposals), time.Since(t2))
}

// enrichProposal attaches Speakers (from the prebuilt cache), ConfTalk,
// and Recording to a single proposal. Safe to call concurrently across
// proposals — only the proposal's own fields are mutated and scCache is
// read-only at this point.
func enrichProposal(ctx *config.AppContext, p *types.Proposal, scCache map[string]*types.SpeakerConf) {
	p.Speakers = nil
	for _, refID := range p.SpeakerConfRefs {
		if sc := scCache[refID]; sc != nil {
			p.Speakers = append(p.Speakers, sc)
		}
	}

	if p.Status != StatusAccepted {
		return
	}
	ct, err := getters.GetConfTalkByProposal(ctx, p.ID)
	if err != nil {
		ctx.Err.Printf("enrich proposal %s: conftalk: %s", p.ID, err)
		return
	}
	p.ConfTalk = ct
	if ct == nil {
		return
	}
	rec, err := getters.GetRecordingByConfTalk(ctx, ct.ID)
	if err != nil {
		ctx.Err.Printf("enrich proposal %s: recording: %s", p.ID, err)
		return
	}
	p.Recording = rec
}

// buildEventBlocks consolidates the user's per-event relationships
// (speaker conf, volunteer app, tickets) into one EventBlock per conf
// they touch. Returns separate active / past slices so the template
// can render past confs in a collapsed bucket.
//
// Sort order within each slice is by conf StartDate ascending — the
// nearest upcoming conf appears first in active; oldest first in past.
//
// A conf can appear in either slice but never both. If a conf has no
// relationship at all (the user never applied / volunteered / bought)
// it doesn't get a block; those confs surface via EligibleConfs /
// BuyableConfs in the discover section instead.
func buildEventBlocks(
        speakerConfs []*types.SpeakerConf,
        volApps []*types.Volunteer,
        tickets []*UserTicket,
        regs []*types.Registration,
        confs []*types.Conf,
        volInfos map[string]*types.VolInfo,
) (active, past []*EventBlock) {
        byTag := make(map[string]*EventBlock)
        confByTag := make(map[string]*types.Conf, len(confs))
        for _, c := range confs {
                if c != nil {
                        confByTag[c.Tag] = c
                }
        }

        block := func(conf *types.Conf) *EventBlock {
                if conf == nil {
                        return nil
                }
                if eb, ok := byTag[conf.Tag]; ok {
                        return eb
                }
                eb := &EventBlock{
                        Conf:   conf,
                        CanBuy: conf.Active && conf.InFuture(),
                }
                byTag[conf.Tag] = eb
                return eb
        }

        for _, sc := range speakerConfs {
                conf := speakerConfConf(sc)
                if eb := block(conf); eb != nil {
                        eb.SpeakerConf = sc
                }
        }

        for _, vol := range volApps {
                if len(vol.ScheduleFor) == 0 {
                        continue
                }
                conf := vol.ScheduleFor[0]
                if eb := block(conf); eb != nil {
                        eb.VolApp = vol
                        if vi, ok := volInfos[conf.Tag]; ok {
                                eb.VolInfo = vi
                        }
                }
        }

        // Tickets are scoped via the resolved Conf on each UserTicket.
        // upcomingTickets already filtered out past confs, so to also
        // surface tickets for ended confs in the past block we walk
        // the raw regs slice independently.
        for _, t := range tickets {
                if eb := block(t.Conf); eb != nil {
                        eb.Tickets = append(eb.Tickets, t.Reg)
                }
        }
        for _, r := range regs {
                if r == nil || r.RefID == "" {
                        continue
                }
                conf := confByRef(confByTag, r.ConfRef)
                if conf == nil {
                        continue
                }
                eb := block(conf)
                if eb == nil {
                        continue
                }
                // Avoid double-add when upcomingTickets already covered it.
                if !containsTicket(eb.Tickets, r) {
                        eb.Tickets = append(eb.Tickets, r)
                }
        }

        for _, eb := range byTag {
                if eb.Conf != nil && eb.Conf.HasEnded() {
                        past = append(past, eb)
                } else {
                        active = append(active, eb)
                }
        }
        sort.Slice(active, func(i, j int) bool {
                return active[i].Conf.StartDate.Before(active[j].Conf.StartDate)
        })
        sort.Slice(past, func(i, j int) bool {
                return past[i].Conf.StartDate.After(past[j].Conf.StartDate)
        })
        return active, past
}

// confByRef finds a Conf by Notion page-ID (the value stored on
// PurchasesDb rows). Linear scan over the typically-small confs map.
func confByRef(byTag map[string]*types.Conf, ref string) *types.Conf {
        for _, c := range byTag {
                if c != nil && c.Ref == ref {
                        return c
                }
        }
        return nil
}

func containsTicket(list []*types.Registration, r *types.Registration) bool {
        for _, t := range list {
                if t != nil && t.RefID == r.RefID {
                        return true
                }
        }
        return false
}

// excludeConfsInBlocks filters a candidate slice (e.g. EligibleConfs)
// to drop confs that already appear as event blocks — the discovery
// list at the bottom of the dashboard shouldn't repeat events the
// user is already engaged with.
func excludeConfsInBlocks(candidates []*types.Conf, blocks []*EventBlock) []*types.Conf {
        if len(blocks) == 0 {
                return candidates
        }
        seen := make(map[string]bool, len(blocks))
        for _, eb := range blocks {
                if eb != nil && eb.Conf != nil {
                        seen[eb.Conf.Tag] = true
                }
        }
        out := make([]*types.Conf, 0, len(candidates))
        for _, c := range candidates {
                if c == nil || seen[c.Tag] {
                        continue
                }
                out = append(out, c)
        }
        return out
}

// upcomingTickets joins the user's PurchasesDb rows with the confs cache
// and keeps only those whose conf hasn't ended. Past tickets are dropped
// — no value in offering a PDF for a conf that's already over.
func upcomingTickets(regs []*types.Registration, allConfs []*types.Conf) []*UserTicket {
	if len(regs) == 0 {
		return nil
	}
	confByRef := make(map[string]*types.Conf, len(allConfs))
	for _, c := range allConfs {
		if c != nil {
			confByRef[c.Ref] = c
		}
	}
	var out []*UserTicket
	for _, r := range regs {
		if r == nil || r.RefID == "" {
			continue
		}
		c := confByRef[r.ConfRef]
		if c == nil || c.HasEnded() {
			continue
		}
		out = append(out, &UserTicket{Reg: r, Conf: c})
	}
	return out
}

// buyableConfs returns Active confs whose start date is still in the
// future — i.e., the ones a logged-in user can still buy a ticket for.
// We don't filter by existing purchases; the conf page handles "you've
// already got a ticket" UI when the user clicks through.
func buyableConfs(allConfs []*types.Conf) []*types.Conf {
	var out []*types.Conf
	for _, c := range allConfs {
		if c == nil || !c.Active || !c.InFuture() {
			continue
		}
		out = append(out, c)
	}
	return out
}

// eligibleApplyConfs returns confs the user could still apply to speak at:
// Active, applications still open (TalksOpen), and no existing SpeakerConf
// linking them. Used to render the dashboard's "Apply to speak" section.
func eligibleApplyConfs(allConfs []*types.Conf, userSpeakerConfs []*types.SpeakerConf) []*types.Conf {
	applied := make(map[string]bool)
	for _, sc := range userSpeakerConfs {
		if conf := speakerConfConf(sc); conf != nil {
			applied[conf.Tag] = true
		}
	}
	var out []*types.Conf
	for _, c := range allConfs {
		if c == nil || !c.TalksOpen() || applied[c.Tag] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// splitSpeakerConfsByEnded partitions speaker confs by whether their conf
// has ended (per Conf.EndDate). A SpeakerConf with no resolvable conf
// (no proposals or proposals without ScheduleFor) lands in the active
// bucket so it's still visible — better to show too much than to bury it.
func splitSpeakerConfsByEnded(scs []*types.SpeakerConf) (active, past []*types.SpeakerConf) {
	for _, sc := range scs {
		conf := speakerConfConf(sc)
		if conf != nil && conf.HasEnded() {
			past = append(past, sc)
		} else {
			active = append(active, sc)
		}
	}
	return active, past
}

func splitVolAppsByEnded(vols []*types.Volunteer) (active, past []*types.Volunteer) {
	for _, v := range vols {
		if len(v.ScheduleFor) > 0 && v.ScheduleFor[0] != nil && v.ScheduleFor[0].HasEnded() {
			past = append(past, v)
		} else {
			active = append(active, v)
		}
	}
	return active, past
}

// speakerConfConf returns the conf this SpeakerConf belongs to, looking it
// up via the first proposal's ScheduleFor. SpeakerConfs are per-(speaker,
// conf) so all proposals share the same conf — but defensive against the
// no-proposal edge case.
func speakerConfConf(sc *types.SpeakerConf) *types.Conf {
	for _, p := range sc.Proposals {
		if p != nil && p.ScheduleFor != nil {
			return p.ScheduleFor
		}
	}
	return nil
}

// calcDashboardStats counts unique proposals (by ID — a proposal can appear
// under multiple SpeakerConfs in multi-speaker setups) and shift signups.
func calcDashboardStats(speakerConfs []*types.SpeakerConf, volapps []*types.Volunteer) *DashboardStats {
	s := &DashboardStats{}
	seen := make(map[string]bool)
	for _, sc := range speakerConfs {
		for _, p := range sc.Proposals {
			if p == nil || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			s.TalksApplied++
			if p.Status == StatusAccepted {
				s.TalksAccepted++
			}
		}
	}
	for _, v := range volapps {
		s.ShiftsApplied++
		if v.Status == "Scheduled" {
			s.ShiftsBooked++
		}
	}
	return s
}
