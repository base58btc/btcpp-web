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
	// URI is the absolute site root (e.g. "https://btcpp.dev")
	// present on every OnlyFor data struct so letter templates can
	// reference assets like
	// ![]({{ .URI }}/static/img/{{ .Conf.Tag }}/leading.png).
	// Populated by each Send* helper from ctx.Env.GetURI().

	VolLogin struct {
		Email        string
		VolShiftLink string
		URI          string
	}

	VolSignup struct {
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		Email        string
		VolShiftLink string
		URI          string
	}

	VolWaitlist struct {
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		Email        string
		VolShiftLink string
		URI          string
	}

	VolShifts struct {
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		VolInfo      *types.VolInfo
		Email        string
		VolShiftLink string
		URI          string
	}

	VolCustom struct {
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		VolInfo      *types.VolInfo
		DiscountCode *types.DiscountCode
		Email        string
		VolShiftLink string
		URI          string
	}

	VolCancel struct {
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		VolShiftLink string
		URI          string
	}

	VolApp struct {
		Name         string
		Volunteer    *types.Volunteer
		Conf         *types.Conf
		VolInfo      *types.VolInfo
		Email        string
		VolShiftLink string
		URI          string
	}

	SpeakerCustom struct {
		Speaker *types.Speaker
		Conf    *types.Conf
		Talks   []*types.Talk
		Email   string
		URI     string
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
		// MagicLink is the InviteToken-gated /invite-speaker URL
		// — populated only on talkinvited-direct (the admin-
		// originated invite flow). Empty for the regular review
		// letters (talkinvited / talkconfirmed / etc.).
		MagicLink string
		// Note is a free-text personal message from the admin
		// who originated the invitation, surfaced in the
		// talkinvited-direct letter so the recipient sees a
		// human voice ("hey jane, would love to have you
		// back, here's the form…"). Empty for letters fired
		// by automated status changes (talkconfirmed, etc.).
		Note string
		URI  string
	}
)

func makeJobKeyRep(email string, letter *mtypes.Letter) string {
	jobhash := helpers.MakeJobHash(email, letter.UID, letter.Title)
	return fmt.Sprintf("%s-%s-%d", letter.Missive(), jobhash, time.Now().Unix())
}

// makeJobKeyDedupe is the no-timestamp variant for letters where we
// WANT the mailer-side dedupe to actually bite. Currently used only by
// the ticket letter (SendOnlyForTicket): the cron re-runs every job
// every tick and a fresh time.Now().Unix() in the key would punch
// straight through the mailer's idempotency layer, double-delivering
// every ticket on every restart. The standard makeJobKeyRep keeps its
// timestamp for letters where re-firing IS the intent.
func makeJobKeyDedupe(email string, letter *mtypes.Letter) string {
	jobhash := helpers.MakeJobHash(email, letter.UID, letter.Title)
	return fmt.Sprintf("%s-%s", letter.Missive(), jobhash)
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
	return OnlyForLoginLink(ctx, email, helpers.EmailLink(ctx, email, "/dashboard"))
}

// OnlyForLoginLink sends the "vollogin" magic-link letter with a
// caller-provided URL. Used by the generic /login flow to bake a
// `next` redirect into the link via auth.MagicLink.
func OnlyForLoginLink(ctx *config.AppContext, email, link string) ([]byte, error) {
	onlyFor := "vollogin"
	if !ctx.InProduction {
		ctx.Infos.Printf("[dev] login link for %s: %s", email, link)
	}
	tmplData := &VolLogin{
		Email:        email,
		VolShiftLink: link,
		URI:          ctx.Env.GetURI(),
	}

	return execOnlyFor(ctx, email, onlyFor, tmplData)
}

func OnlyForVolWaitlist(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
	onlyFor := "volwaitlist"
	tmplData := &VolWaitlist{
		Email:        vol.Email,
		Conf:         conf,
		Volunteer:    vol,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
	}

	return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolSignup(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
	onlyFor := "volsignup"
	tmplData := &VolSignup{
		Email:        vol.Email,
		Conf:         conf,
		Volunteer:    vol,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
	}

	return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolApp(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo) ([]byte, error) {
	onlyFor := "volapp"
	tmplData := &VolApp{
		Name:         vol.Name,
		Volunteer:    vol,
		Conf:         conf,
		VolInfo:      volinfo,
		Email:        vol.Email,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
	}

	return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolCancel(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
	onlyFor := "volcancel"
	tmplData := &VolCancel{
		Volunteer:    vol,
		Conf:         conf,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
	}

	return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolShift(ctx *config.AppContext, volinfo *types.VolInfo, vol *types.Volunteer) ([]byte, error) {
	onlyFor := "volshifts"
	tmplData := &VolShifts{
		Volunteer:    vol,
		Conf:         vol.ScheduleFor[0],
		VolInfo:      volinfo,
		Email:        vol.Email,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
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
// talkwaitlisted, talkrejected, talkinvited-direct, talkselfdecline.
//
// note is a free-text personal message attached to the rendered data
// shape (.Note) — currently used only by the admin invite-speaker
// flow's talkinvited-direct letter so the organizer can include a
// human nudge. Pass "" when no note applies.
func SendOnlyForProposal(ctx *config.AppContext, onlyFor string, proposal *types.Proposal, conf *types.Conf, note string) error {
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
			MagicLink:       helpers.InviteLink(ctx, proposal.ID, proposal.InviteToken),
			Note:            note,
			URI:             ctx.Env.GetURI(),
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
	return SendCustomToVolWithDiscount(ctx, vol, conf, volinfo, nil, title, markdown)
}

func SendCustomToVolWithDiscount(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo, discount *types.DiscountCode, title, markdown string) ([]byte, error) {
	tmplData := &VolCustom{
		Volunteer:    vol,
		Conf:         conf,
		VolInfo:      volinfo,
		DiscountCode: discount,
		Email:        vol.Email,
		VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
		URI:          ctx.Env.GetURI(),
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
		URI   string
	}{
		Conf:  conf,
		Email: reg.Email,
		URI:   ctx.Env.GetURI(),
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
		URI      string
	}{
		Proposal: proposal,
		Speaker:  speaker,
		Conf:     conf,
		Email:    speaker.Email,
		URI:      ctx.Env.GetURI(),
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
		URI:     ctx.Env.GetURI(),
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

// OnlyForTicket is the data shape passed to the "ticket" OnlyFor
// letter — the per-recipient ticket-receipt email that fires from
// the registration mailer. The PDF attachment is built upstream and
// attached at send time, not via the template.
type OnlyForTicket struct {
	Conf  *types.Conf
	Email string
	// URI is the site root (e.g. "https://btcpp.dev"). Used to
	// build the absolute leading-image URL that the email body
	// markdowns reference at the top of the message.
	URI string
	// DayCount is the conference duration written out in
	// English ("two", "three", …) so the email body reads
	// naturally — populated by SendOnlyForTicket.
	DayCount      string
	DashboardLink string
}

// SendOnlyForTicket renders the "ticket" OnlyFor letter against
// (conf, email) and dispatches it with `pdf` attached. Replaces the
// per-conf templates/emails/{tag}.tmpl path that's been used for
// every conference's ticket receipt to date — same delivery
// pipeline (`ComposeAndSendMail` → mailer service), just with a
// single Notion-stored letter instead of N hand-written templates.
//
// Pre-fills `conf.DoorsOpen` from `DoorsOpenDesc(ctx, conf)` so the
// template body can reference {{ .Conf.DoorsOpen }} directly without
// the caller round-tripping ConfInfo.
//
// `ticketID` is folded into the JobKey so the remote mailer's
// idempotency layer dedupes per (recipient, ticket) and the cron
// can re-run on a restart without double-sending.
//
// `uriOverride` lets test paths render with a different absolute
// URL than the running app's GetURI() — e.g. a local dev box
// emitting test ticket emails wants images sourced from
// https://btcpp.dev so they actually render in the recipient's
// inbox. Pass "" to use ctx.Env.GetURI() (the normal cron path).
func SendOnlyForTicket(ctx *config.AppContext, conf *types.Conf, email string, pdf []byte, ticketID, uriOverride string) error {
	if conf == nil {
		return fmt.Errorf("SendOnlyForTicket: nil conf")
	}
	if email == "" {
		return fmt.Errorf("SendOnlyForTicket: empty email")
	}

	// Mutate a SHALLOW COPY so DoorsOpen lives only on the
	// template-data version — caching the cached *Conf with this
	// value would leak per-render state.
	confCopy := *conf
	confCopy.DoorsOpen = DoorsOpenDesc(ctx, conf)

	uri := uriOverride
	if uri == "" {
		uri = ctx.Env.GetURI()
	}

	data := &OnlyForTicket{
		Conf:          &confCopy,
		Email:         email,
		URI:           uri,
		DayCount:      englishCount(confDayCount(conf)),
		DashboardLink: helpers.EmailLink(ctx, email, "/dashboard"),
	}

	letter, err := getters.GetLetterFor(ctx, "ticket")
	if err != nil {
		return fmt.Errorf("load ticket letter: %w", err)
	}

	var buf bytes.Buffer
	if err := missiveTemplate(ctx, letter).Execute(&buf, data); err != nil {
		return fmt.Errorf("render ticket letter: %w", err)
	}
	title := templatizeTitle(letter.Title, data)
	if title == "" {
		title = fmt.Sprintf("[%s] Your Conference Pass is Here!", conf.Desc)
	}

	htmlBody, err := BuildHTMLEmail(ctx, buf.Bytes())
	if err != nil {
		return fmt.Errorf("build html: %w", err)
	}

	// Attachment file naming mirrors the legacy SendTickets
	// path (btcpp_{tag}_ticket_{shortid}.pdf) so existing
	// archives + email-rule filters keep matching.
	short := ticketID
	if len(short) > 6 {
		short = short[:6]
	}
	attachName := fmt.Sprintf("btcpp_%s_ticket_%s.pdf", conf.Tag, short)

	// Per-recipient idempotency key: same (email, ticket) pair
	// collapses on the mailer side regardless of restart noise.
	// Uses the no-timestamp variant so the mailer's dedupe layer
	// actually catches re-runs of the same (email, ticket) pair.
	jobKey := makeJobKeyDedupe(email, letter) + "-" + short

	mail := &Mail{
		JobKey:   jobKey,
		Missive:  letter.Missive(),
		Email:    email,
		Title:    title,
		SendAt:   time.Now(),
		HTMLBody: htmlBody,
		TextBody: buf.Bytes(),
		Files: []*EmailFile{
			{PDF: pdf, Name: attachName},
		},
	}
	ctx.Infos.Printf("Sending ticket (%s)%s to %s", jobKey, title, email)
	return ComposeAndSendMail(ctx, mail)
}

// DoorsOpenDesc returns the human-readable check-in time for the
// conf, sourced from ConfInfo[Day=1].Doors.Start. Falls back to
// "9:00am" when no Day-1 ConfInfo row exists yet (or its Doors.Start
// is zero-valued) so the email body still reads sensibly during
// the early-conf-setup window.
func DoorsOpenDesc(ctx *config.AppContext, conf *types.Conf) string {
	const fallback = "9:00am"
	if conf == nil || conf.Tag == "" {
		return fallback
	}
	infos, err := getters.ListConfInfos(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("DoorsOpenDesc %s: list confinfos: %s", conf.Tag, err)
		return fallback
	}
	for _, ci := range infos {
		if ci != nil && ci.Day == 1 && ci.Doors != nil && !ci.Doors.Start.IsZero() {
			// 8:00 AM-style 12h clock; lowercase am/pm
			// so it reads casually in the email body.
			s := ci.Doors.Start.Format("3:04pm")
			return s
		}
	}
	return fallback
}

// confDayCount returns the inclusive day count between StartDate
// and EndDate. Defaults to 1 when EndDate isn't set so the email
// body never reads "for zero days".
func confDayCount(conf *types.Conf) int {
	if conf == nil || conf.StartDate.IsZero() {
		return 1
	}
	end := conf.EndDate
	if end.IsZero() {
		end = conf.StartDate
	}
	days := int(end.Sub(conf.StartDate).Hours()/24) + 1
	if days < 1 {
		return 1
	}
	return days
}

// englishCount returns small integers spelled out as English words —
// "one" for 1, "two" for 2, etc. Up to ten covers every plausible
// conference duration. Anything past ten falls back to digits.
func englishCount(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return fmt.Sprintf("%d", n)
}

func execOnlyFor(ctx *config.AppContext, email, onlyFor string, tmplData interface{}) ([]byte, error) {
	letter, err := getters.GetLetterFor(ctx, onlyFor)
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
	return sendOnlyForWithExtras(ctx, email, letter, title, content, SendOpts{})
}

// SendOpts carries optional extras for an OnlyFor send: a Reply-To
// override (so cal-invite emails route replies to speak@ /
// volunteer@ without changing the envelope From) and zero or more
// attachments. Empty fields are no-ops; callers that don't need
// either pass a zero-value SendOpts.
type SendOpts struct {
	ReplyTo string // RFC-5322 mailbox; "Display Name <addr@dom>" syntax accepted
	Files   []*EmailFile
}

// sendOnlyForWithExtras is the variant of sendOnlyFor that honors
// SendOpts. The original sendOnlyFor is now a thin wrapper around
// this so existing call sites stay unchanged.
func sendOnlyForWithExtras(ctx *config.AppContext, email string, letter *mtypes.Letter, title string, content bytes.Buffer, opts SendOpts) ([]byte, error) {
	mail := &Mail{
		JobKey:   makeJobKeyRep(email, letter),
		Missive:  letter.Missive(),
		Email:    email,
		Title:    title,
		SendAt:   time.Now(),
		TextBody: content.Bytes(),
		ReplyTo:  opts.ReplyTo,
		Files:    opts.Files,
	}

	var err error
	mail.HTMLBody, err = BuildHTMLEmail(ctx, content.Bytes())
	if err != nil {
		return nil, err
	}

	ctx.Infos.Printf("Sending (%s)%s to %s at %s", mail.JobKey, title, email, mail.SendAt)

	return mail.HTMLBody, ComposeAndSendMail(ctx, mail)
}

// ExecLetter renders a Notion letter against tmplData and sends it
// to a single recipient with optional Reply-To + attachments.
// Public surface used by the cal-invite dispatch (and the
// /trial-cal-invite smoke route) to fire one-off ICS-attached
// emails outside the proposal-fan-out path.
func ExecLetter(ctx *config.AppContext, email, onlyFor string, tmplData interface{}, opts SendOpts) ([]byte, error) {
	letter, err := getters.GetLetterFor(ctx, onlyFor)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err = missiveTemplate(ctx, letter).Execute(&buf, tmplData); err != nil {
		return nil, err
	}
	title := templatizeTitle(letter.Title, tmplData)
	return sendOnlyForWithExtras(ctx, email, letter, title, buf, opts)
}
