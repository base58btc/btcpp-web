package emails

import (
        "bytes"
        "fmt"
	"html/template"
        "time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"
)

type (
        VolLogin struct {
                Email        string
                VolShiftLink string
        }

        VolSignup struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                Email        string
                VolShiftLink string
        }

        VolWaitlist struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                Email        string
                VolShiftLink string
        }

        VolShifts struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        VolCustom struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        VolCancel struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolShiftLink string
        }

        VolApp struct {
                Name         string
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        SpeakerCustom struct {
                Speaker *types.Speaker
                Conf    *types.Conf
                Talks   []*types.Talk
                Email   string
        }

        // OnlyForProposal is the data shape passed to the per-proposal
        // status emails (talkinvited / talkconfirmed / talkdeclined /
        // talkwaitlisted / talkrejected). The template can reach all
        // co-speakers via .SpeakerConfs / .Speakers; .Speaker / .Email
        // identify the recipient of *this particular* render so the
        // letter can address them by name.
        //
        // TalkConfirmLink is the magic-link URL the recipient clicks
        // to one-click-accept the talk. DashboardLink is their general
        // self-service URL. Both are speaker-scoped magic links — the
        // HMAC encodes the recipient's email so each speaker on a
        // multi-speaker proposal gets their own pair of links.
        OnlyForProposal struct {
                Proposal        *types.Proposal
                SpeakerConfs    []*types.SpeakerConf
                Speakers        []*types.Speaker
                Conf            *types.Conf
                Speaker         *types.Speaker
                Email           string
                TalkConfirmLink string
                DashboardLink   string
        }
)


func makeJobKeyRep(email string, letter *mtypes.Letter) string {
	jobhash := helpers.MakeJobHash(email, letter.UID, letter.Title)
	return fmt.Sprintf("%s-%s-%d", letter.Missive(), jobhash, time.Now().Unix())
}


// OnlyForLogin sends a magic-link email pointing at /dashboard. Reuses the
// existing "vollogin" Notion letter (its template field is named
// VolShiftLink for historical reasons; the URL it produces is now the
// unified dashboard).
//
// Outside production, the link is also written to the info log so devs can
// grab it without waiting for a real email. NEVER do this in prod — anyone
// with log access could log in as anyone.
func OnlyForLogin(ctx *config.AppContext, email string) ([]byte, error) {
        onlyFor := "vollogin"
        link := helpers.EmailLink(ctx, email, "/dashboard")
        if !ctx.InProduction {
                ctx.Infos.Printf("[dev] dashboard login link for %s: %s", email, link)
        }
        tmplData := &VolLogin{
                Email:        email,
                VolShiftLink: link,
	}

        return execOnlyFor(ctx, email, onlyFor, tmplData)
}

func OnlyForVolWaitlist(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volwaitlist"
        tmplData := &VolWaitlist{
                Email: vol.Email,
                Conf: conf,
                Volunteer: vol,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolSignup(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volsignup"
        tmplData := &VolSignup{
                Email: vol.Email,
                Conf: conf,
                Volunteer: vol,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolApp(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo) ([]byte, error) {
        onlyFor := "volapp"
        tmplData := &VolApp{
                Name:         vol.Name,
                Conf:         conf,
                VolInfo:      volinfo,
                Email:        vol.Email,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolCancel(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volcancel"
        tmplData := &VolCancel{
                Volunteer:  vol,
                Conf:  conf,
                VolShiftLink:   helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolShift(ctx *config.AppContext, volinfo *types.VolInfo, vol *types.Volunteer) ([]byte, error) {
        onlyFor := "volshifts"
        tmplData := &VolShifts{
                Volunteer:      vol,
                Conf:           vol.ScheduleFor[0],
                VolInfo:        volinfo,
                Email:          vol.Email,
                VolShiftLink:   helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

// SendOnlyForProposal fans out one rendered onlyfor letter per speaker
// attached to the proposal. Each render gets the same proposal/conf/
// peers data, with .Speaker / .Email swapped to the current recipient
// so the template can address them by name.
//
// Best-effort across recipients: a failed send for one speaker is
// logged via ctx.Err and skipped; siblings still get their email. The
// returned error is non-nil only when *every* send failed (or the
// proposal had no speakers at all to send to).
//
// onlyFor is one of: talkinvited, talkconfirmed, talkdeclined,
// talkwaitlisted, talkrejected.
func SendOnlyForProposal(ctx *config.AppContext, onlyFor string, proposal *types.Proposal, conf *types.Conf) error {
        if proposal == nil {
                return fmt.Errorf("SendOnlyForProposal: nil proposal")
        }

        // Resolve all SpeakerConfs (and their Speakers) for the proposal.
        // proposal.Speakers is populated by the dashboard enricher but
        // may be empty in admin paths — fall back to FetchSpeakerConfByID
        // on the cached lookup.
        scs := make([]*types.SpeakerConf, 0, len(proposal.SpeakerConfRefs))
        speakers := make([]*types.Speaker, 0, len(proposal.SpeakerConfRefs))
        for _, ref := range proposal.SpeakerConfRefs {
                sc := getters.FetchSpeakerConfByID(ref)
                if sc == nil {
                        ctx.Err.Printf("SendOnlyForProposal: SpeakerConf %s not in cache — skip", ref)
                        continue
                }
                scs = append(scs, sc)
                if sc.Speaker != nil {
                        speakers = append(speakers, sc.Speaker)
                }
        }
        if len(speakers) == 0 {
                return fmt.Errorf("SendOnlyForProposal: no speakers resolved for proposal %s", proposal.ID)
        }

        sentAny := false
        var firstErr error
        for _, sp := range speakers {
                if sp == nil || sp.Email == "" {
                        continue
                }
                data := &OnlyForProposal{
                        Proposal:        proposal,
                        SpeakerConfs:    scs,
                        Speakers:        speakers,
                        Conf:            conf,
                        Speaker:         sp,
                        Email:           sp.Email,
                        TalkConfirmLink: helpers.EmailLink(ctx, sp.Email, "/dashboard/talks/"+proposal.ID+"/confirm"),
                        DashboardLink:   helpers.EmailLink(ctx, sp.Email, "/dashboard"),
                }
                if _, err := execOnlyFor(ctx, sp.Email, onlyFor, data); err != nil {
                        ctx.Err.Printf("SendOnlyForProposal %s → %s: %s", onlyFor, sp.Email, err)
                        if firstErr == nil {
                                firstErr = err
                        }
                        continue
                }
                sentAny = true
        }
        if !sentAny {
                return firstErr
        }
        return nil
}

func templatizeTitle(title string, tmplData interface{}) string {
        var tt bytes.Buffer
        Create := func(name, t string) *template.Template {
                return template.Must(template.New(name).Parse(t))
        }
        titletemp := Create("tt", title)
        titletemp.Execute(&tt, &tmplData)
        return tt.String()
}

// SendCustomToVol renders a custom markdown body and title to a single
// volunteer using the VolShifts data shape. The markdown is parsed as a Go
// template so admins can use {{ .Volunteer.Name }} etc. in the body and title.
func SendCustomToVol(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo, title, markdown string) ([]byte, error) {
        tmplData := &VolCustom{
                Volunteer:    vol,
                Conf:         conf,
                VolInfo:      volinfo,
                Email:        vol.Email,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
        }

        // Build an in-memory Letter so we can reuse the existing renderer pipeline.
        // The UID is used to cache parsed templates by hash; using time.Now() means
        // each send gets its own template entry which is what we want for one-off custom mails.
        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, vol.Email, letter, renderedTitle, buf)
}

func SendCustomToAttendee(ctx *config.AppContext, reg *types.Registration, conf *types.Conf, title, markdown string) ([]byte, error) {
        tmplData := &struct {
                Conf  *types.Conf
                Email string
        }{
                Conf:  conf,
                Email: reg.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, reg.Email, letter, renderedTitle, buf)
}

func SendCustomToProposalSpeaker(ctx *config.AppContext, proposal *types.Proposal, speaker *types.Speaker, conf *types.Conf, title, markdown string) ([]byte, error) {
        tmplData := &struct {
                Proposal *types.Proposal
                Speaker  *types.Speaker
                Conf     *types.Conf
                Email    string
        }{
                Proposal: proposal,
                Speaker:  speaker,
                Conf:     conf,
                Email:    speaker.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, speaker.Email, letter, renderedTitle, buf)
}

func SendCustomToSpeaker(ctx *config.AppContext, speaker *types.Speaker, conf *types.Conf, talks []*types.Talk, title, markdown string) ([]byte, error) {
        tmplData := &SpeakerCustom{
                Speaker: speaker,
                Conf:    conf,
                Talks:   talks,
                Email:   speaker.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, speaker.Email, letter, renderedTitle, buf)
}

func execOnlyFor(ctx *config.AppContext, email, onlyFor string, tmplData interface{}) ([]byte, error) {
        letter, err := getters.GetLetterFor(ctx.Notion, onlyFor)
        if err != nil {
                return nil, err
        }

        /* Execute template for this type */
	var buf bytes.Buffer
	err = missiveTemplate(ctx, letter).Execute(&buf, tmplData)

	if err != nil {
		return nil, err
	}

        /* Also parse/pull the letter title! */
        title := templatizeTitle(letter.Title, tmplData)

        return sendOnlyFor(ctx, email, letter, title, buf)
}

func sendOnlyFor(ctx *config.AppContext, email string, letter *mtypes.Letter, title string, content bytes.Buffer) ([]byte, error) {
	mail := &Mail{
		JobKey:   makeJobKeyRep(email, letter),
		Missive:  letter.Missive(),
		Email:    email,
		Title:    title,
		SendAt:   time.Now(),
		TextBody: content.Bytes(),
	}

        var err error
	mail.HTMLBody, err = BuildHTMLEmail(ctx, content.Bytes())
	if err != nil {
		return nil, err
	}

	ctx.Infos.Printf("Sending (%s)%s to %s at %s", mail.JobKey, title, email, mail.SendAt)

	return mail.HTMLBody, ComposeAndSendMail(ctx, mail)
}
