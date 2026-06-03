package handlers

import (
	"time"

	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"
)

type Day struct {
	Morning   []types.SessionTime
	Afternoon []types.SessionTime
	Evening   []types.SessionTime

	Idx int
}

func (d *Day) Venues() []string {
	venhash := make(map[string]string)

	all := make([]types.SessionTime, 0)
	all = append(all, d.Morning...)
	all = append(all, d.Afternoon...)
	all = append(all, d.Evening...)

	for _, list := range all {
		for _, sesh := range list {
			venhash[sesh.Venue] = ""
		}
	}

	venues := make([]string, len(venhash))
	i := 0
	for k, _ := range venhash {
		venues[i] = k
		i++
	}

	return venues
}

type ConfPage struct {
	Conf          *types.Conf
	Hotels        []*types.Hotel
	Tix           *types.ConfTicket
	MaxTix        *types.ConfTicket
	Sold          uint
	TixLeft       uint
	Talks         []*types.Talk
	EventSpeakers []*types.Speaker
	Buckets       map[string]types.SessionTime
	Days          []*Day

	// AgendaDays drives the post-section-letter agenda rendering: per-day
	// bucketed talks + ConfInfo time strip. Empty when the conf has no
	// scheduled talks yet.
	AgendaDays []*AgendaDay

	// ScheduledSessions is AgendaDays' .All flattened into a single
	// chrono-ordered slice — used by the JSON-LD Event subEvent[]
	// emission so the template can comma-separate without nested-
	// range gymnastics. Nil when no talks are Scheduled yet.
	ScheduledSessions []*types.Session

	Year uint
}

type SuccessPage struct {
	Conf *types.Conf
	Year uint
}

type TixFormPage struct {
	Conf          *types.Conf
	Tix           *types.ConfTicket
	TixSlug       string
	Count         uint
	TixPrice      uint
	Discount      string
	DiscountPrice uint
	DiscountRef   string
	// AffiliateCode is the silent (`%0`) referral code stashed
	// from a /{tag}?code= visit. Carried through the form
	// as a hidden input — the visible Discount field stays empty
	// for silent codes, but the affiliate still gets credit on
	// successful checkout. Buyer typing a code into Discount
	// overrides this on POST.
	AffiliateCode string
	HMAC          string
	Err           string
	Year          uint
	PaymentMethod string // "btc" or "fiat"
}

type SchedulePage struct {
	Talks []*types.Talk
	s     []types.TalkTime
}

type CheckInPage struct {
	NeedsPin   bool
	TicketType string
	Msg        string
	Year       uint
}

type VolunteerPage struct {
	Confs     []*types.Conf
	Conf      *types.Conf
	YesJobs   []types.CheckItem
	NoJobs    []types.CheckItem
	ConfItems []types.CheckItem
	DaysList  []types.CheckItem
	// Prefill is populated when the apply form is opened from
	// the /dashboard "Sign up to volunteer" CTA. The dashboard
	// passes the user's HMAC-verified email and we look up the
	// Speakers row to pre-fill Name / Phone / Email / Signal /
	// Twitter / Nostr — saves the speaker re-typing what we
	// already know about them. Nil when the form is opened
	// anonymously from the public conf page.
	Prefill *types.Speaker
	// PrefillHometown comes from SpeakerConf.ComingFrom for
	// this conf when one exists — the speaker has already
	// told us where they're traveling from for this event.
	// Empty when there's no SpeakerConf or the field's blank;
	// template treats that the same as "no pre-fill".
	PrefillHometown string
	Year            uint
}

type SpeakerPage struct {
	Confs            []*types.Conf
	Conf             *types.Conf
	ConfItems        []types.CheckItem
	DaysList         []types.CheckItem
	DueDate          string
	RSVPFor          string
	PresentationType []types.CheckItem
	RecordingOptions []types.CheckItem

	// Set when the apply form is rendered after a magic-link login. The
	// template hides Speaker-personal fields (Name, Phone, Email, etc.)
	// and submits them as hidden inputs from this Speaker's data.
	KnownSpeaker           *types.Speaker
	HMAC                   string // base64-encoded
	Email                  string // base64-encoded
	IsNewsletterSubscriber bool   // hides the newsletter opt-in checkbox
	IsReturningAttendee    bool   // hides the "first bitcoin++" checkbox

	// InviteMode flips the form into co-speaker-invite mode: the
	// talk-content fields (Title / Description / Setup / PresType /
	// Captcha / OtherEvents) are hidden and the form posts to
	// /invite-speaker/{Proposal.ID}?t={InviteToken} instead of
	// /talk/{Conf.Tag}.
	InviteMode  bool
	InviteToken string
	Proposal    *types.Proposal
	// KnownSpeakerConf is the existing SpeakerConf for the
	// recipient at this conf — used to pre-fill ComingFrom / Visa
	// / Availability / Company / etc. so the speaker only has to
	// confirm or tweak rather than re-enter them.
	KnownSpeakerConf *types.SpeakerConf
	// EditTalkContent overrides InviteMode's content-hiding when
	// true — used for the admin "Invite a Speaker" flow where the
	// freshly-invited primary speaker still needs to fill in
	// title/description/length. Detected via a placeholder-string
	// check on the Proposal's Title.
	EditTalkContent bool
	// IsInvited relabels the form's submit button to "Accept
	// invitation" (with green styling) when the proposal is in
	// Invited status. The submit target stays /invite-speaker/{id};
	// the POST handler runs the accept pipeline inline after a
	// successful save, so saving and accepting are one click.
	IsInvited bool

	Year uint
}

type ApplicationStats struct {
	Applied     int
	Pending     int
	Accepted    int
	TotalShifts int
}

type VolShiftPage struct {
	Name     string
	Hometown string
	Email    string
	HMAC     string
	VolApps  []*types.Volunteer
	Stats    *ApplicationStats
	Confs    []*types.Conf
	VolInfos map[string]*types.VolInfo
	Year     uint
}

type DashboardPage struct {
	Name     string
	Hometown string
	Photo    string // Speaker.Photo filename, empty if none
	Email    string // base64-encoded
	HMAC     string // base64-encoded

	// Speaker is the user's row in the Speakers DB (looked up by
	// email). Nil means the user is volunteer- or ticket-only and
	// hasn't added themselves to the speakers DB yet — the
	// dashboard renders a "Create speaker profile" CTA in that
	// case.
	Speaker *types.Speaker

	// Speaker side, split by whether the linked conf has ended.
	SpeakerConfs     []*types.SpeakerConf
	PastSpeakerConfs []*types.SpeakerConf

	// Volunteer side, same split.
	VolApps     []*types.Volunteer
	PastVolApps []*types.Volunteer
	VolInfos    map[string]*types.VolInfo

	Stats *DashboardStats
	Confs []*types.Conf

	// Confs the user could apply to speak at — Active, applications still
	// open, no existing SpeakerConf for this user.
	EligibleConfs []*types.Conf

	// Active upcoming confs the user could buy a ticket for. Shown as
	// a "Buy a ticket" section on the dashboard.
	BuyableConfs []*types.Conf

	// DiscoverConfs is the unified list of upcoming Active confs the
	// user has no existing relationship with — drives the per-event
	// discover cards (hero image + Get ticket / Apply to speak /
	// Apply to volunteer CTAs).
	DiscoverConfs []*types.Conf

	// Tickets the user has purchased for upcoming/active confs, with
	// their Conf resolved for header rendering. Past tickets are
	// omitted (no point downloading a PDF for a conf that's over).
	Tickets []*UserTicket

	// ActiveBlocks is the per-event view: one entry for each conf
	// the user has any kind of relationship with (speaker, volunteer,
	// or ticket-holder). Replaces the old activity-typed sections —
	// talks / volunteer / tickets are nested inside each block.
	ActiveBlocks []*EventBlock
	PastBlocks   []*EventBlock

	// HasUpcomingTalk / HasUpcomingVol gate the per-channel "Need
	// help?" block in the footer. True when at least one
	// ActiveBlock has a SpeakerConf / VolApp respectively.
	HasUpcomingTalk bool
	HasUpcomingVol  bool

	FlashMessage string
	// FlashError is the parallel red-banner message — used when
	// a redirect bounces the user with an error rather than a
	// success notice. Populated from ?error= on the URL.
	FlashError string

	// IsGlobalAdmin gates the role-management panel — only a
	// global-admin can edit other speakers' Roles. Other admin
	// surfaces (the per-conf Admin button on conf cards) are
	// gated by EventBlock.AdminRole, not this flag.
	IsGlobalAdmin bool

	// HasAnyTicket gates the affiliate section — visitors who
	// haven't bought (or been issued) a ticket don't see the
	// mint-a-code affordance. AffiliateCode is the user's live
	// code (nil when none); AffiliateStats sums redemptions.
	HasAnyTicket   bool
	AffiliateCode  *types.DiscountCode
	AffiliateStats *AffiliateStats
	// BaseURI is the absolute site root used to build full
	// affiliate share URLs the user can copy from per-event
	// cards (e.g. https://btcpp.dev/vienna?code=NIFTY10).
	BaseURI string

	Year uint
}

// AffiliateStats are the dashboard headline numbers for the
// affiliate section. SavedSats is what redeemers paid less than
// list (the buyer's total discount, BTC-denominated). EarnedSats
// is the affiliate's commission, computed as the slack between a
// fixed 20% ceiling and the buyer's actual savings.
type AffiliateStats struct {
	TicketsSold int
	SavedSats   int64
	EarnedSats  int64
}

// OrganizerDashboardPage drives /admin/{tag}/ — the per-event
// organizer landing. Tiles are conditionally rendered against the
// IsConf* flags so a staff user lands on a slimmer page than an
// admin (no review/applicants/sponsors/social/email).
type OrganizerDashboardPage struct {
	Conf              *types.Conf
	PendingCount      int
	DecisionedCount   int
	ReviewCountsReady bool
	FlashMessage      string
	// Stats drives the headline-numbers panel under the
	// hero image (tickets sold, revenue, sponsors, etc).
	// nil when the load was skipped or all queries blanked.
	Stats *OrganizerStats
	// IsGlobalAdmin gates a couple of tiles (e.g. speaker gifts
	// CSV) whose destination is global-admin only.
	IsGlobalAdmin bool
	// IsConfAdmin: full per-conf admin tier — sees every tile.
	IsConfAdmin bool
	// IsConfVolcoord: surface the volcoord tile. admin implies
	// volcoord at the same scope, so admins always have this.
	IsConfVolcoord bool
	Year           uint
}

// RunOfShowPage drives /{conf}/admin/run-of-show — a per-day timeline
// table interleaving ConfInfo events (doors, coffee, lunch),
// volunteer shifts, and conference talks. Days are emitted in
// chronological order; only days with at least one row appear.
type RunOfShowPage struct {
	Conf *types.Conf
	Days []*RunOfShowDay
	// Venues is the deduped list of talk venues across the conf,
	// alphabetized by display Label. Drives the per-venue
	// visibility checkboxes at the top of the page.
	Venues       []VenueOption
	FlashMessage string
	Year         uint
}

// VenueOption pairs a raw venue tag (the Notion select value used
// on ConfTalk.Venue) with its human-readable display label and a
// hex color for the run-of-show Where column. Colors cycle through
// a small palette so different venues are visually distinct on the
// printed timeline (e.g. "Main Stage" vs "Talks Stage").
type VenueOption struct {
	Tag   string
	Label string
	Color string
}

// AdminClipartsPage drives /{conf}/admin/cliparts — a per-talk table
// for uploading clipart images. Each row exposes the talk title,
// current Clipart filename (if any), a suggested filename derived
// from the title (`{conftag}_<keyword>`), and an upload form.
type AdminClipartsPage struct {
	Conf         *types.Conf
	Rows         []*ClipartRow
	FlashMessage string
	ErrorMessage string
	Year         uint
}

// ClipartRow carries a single talk's clipart-upload row. ProposalID
// is the source-of-truth identifier — ConfTalks are lazy-created on
// first upload, so Accepted proposals that haven't been placed on
// the schedule grid yet still get a row here. Status (Accepted /
// Scheduled) is rendered as a pill so the admin can prioritise.
type ClipartRow struct {
	ProposalID     string
	TalkTitle      string
	Status         string
	CurrentClipart string // bare filename ("vienna_bitcoin.png") or empty
	SuggestedName  string // `{conftag}_<keyword>` minus extension
	ClipartURL     string // Spaces URL of CurrentClipart, "" when empty
}

// RunOfShowDay groups every row falling on a single calendar day in
// the conf's timezone. Idx is the conf-relative day index (Day 1 =
// Conf.StartDate); zero or negative for setup days that fall before
// the first event day.
type RunOfShowDay struct {
	Idx  int
	Date time.Time
	Rows []*RunOfShowRow
}

// RunOfShowRow is one timeline row. Kind drives row styling
// ("info" / "shift" / "talk"). Ranged ConfInfo events (Breakfast /
// Coffee / Lunch / Doors) and volunteer shifts produce TWO rows —
// a start row with full content and an "End:" row at the close time
// — so a chronological timeline shows both moments at their actual
// positions. Talks emit a SINGLE row with their duration baked into
// What ("Title (30m)") rather than a separate end row, since talks
// pack densely on the page.
type RunOfShowRow struct {
	Start    time.Time
	Kind     string
	What     string
	Who      string
	Where    string // human-readable label (post-venueLabel translation)
	VenueTag string // raw venue tag for per-venue visibility toggle
}

// ReviewProposalPage drives /admin/{tag}/review — the
// walkthrough page for individual proposal decisions. Current is nil
// when the queue is empty (template renders an empty state).
type ReviewProposalPage struct {
	Conf         *types.Conf
	Current      *types.Proposal
	Speakers     []*types.SpeakerConf // resolved for Current
	Total        int                  // total pending in the queue
	Index        int                  // 1-based position of Current
	NextID       string               // pre-computed "advance to next" ID, "" at end
	Actions      []reviewAction
	FlashMessage string
	Year         uint
}

// AdminInviteSpeakerPage drives /admin/{tag}/invite-speaker —
// the form an organizer uses to originate a speaker invitation.
// PresentationTypes is the talk-length enum reused from the public
// apply form; AttachableProposals is the conf's current Invited /
// Accepted set, surfaced as the "attach to existing proposal" picker.
type AdminInviteSpeakerPage struct {
	Conf                *types.Conf
	PresentationTypes   []types.CheckItem
	AttachableProposals []*types.Proposal
	FormError           string
	// Pre-fill values when re-rendering after a validation error so
	// the organizer doesn't lose what they typed.
	Form struct {
		Name             string
		Email            string
		SpeakerID        string
		AttachProposalID string
		TalkType         string
		Note             string
	}
	Year uint
}

// AdminInviteSpeakerSentPage is the post-submit confirmation rendered
// after a successful invite. Shows the magic link the admin can copy
// + a CTA to invite another speaker.
type AdminInviteSpeakerSentPage struct {
	Conf               *types.Conf
	Speaker            *types.Speaker
	Proposal           *types.Proposal
	MagicLink          string
	AttachedToExisting bool
	Year               uint
}

// AdminSchedulePage drives /admin/{tag}/schedule — the drag-and-drop
// schedule editor.
type AdminSchedulePage struct {
	Conf         *types.Conf
	Days         []*ScheduleDay
	Unscheduled  []*ScheduleProposal
	PxPerMin     int // vertical scale for the grid + sidebar
	SnapMin      int // drop-position rounding step (e.g. 5min)
	FlashMessage string
	Year         uint
}

// ScheduleDay is one day's grid: venue columns, time bounds, and the
// ScheduleProposals already placed in each venue.
type ScheduleDay struct {
	Idx       int
	Date      time.Time
	Info      *types.ConfInfo
	Venues    []string
	OpensMin  int // minute-of-day, top of grid
	ClosesMin int // minute-of-day, bottom of grid
	HeightPx  int
	// Placed is venue → talks placed in that venue for this day.
	Placed map[string][]*ScheduleProposal
	// Breaks are time bands (lunch / coffee) where talks can't be
	// scheduled. Rendered as a striped overlay over every venue
	// column; the place / resize handlers reject overlapping
	// placements server-side.
	Breaks []*ScheduleBreak
}

// ScheduleBreak is one no-go time band on a day's grid.
type ScheduleBreak struct {
	Label    string
	StartMin int
	EndMin   int
	TopPx    int
	HeightPx int
}

// ScheduleProposal is the per-talk render shape: identity + size +
// (optional) placement coordinates.
//
// DesiredMin is the speaker's stated desired length — read-only on
// the schedule UI; reflects the form they submitted. ActualMin is
// what's currently scheduled, derived from ConfTalk.Sched.End - .Start
// when placed; defaults to DesiredMin in the sidebar so the card has
// a sensible size before its first drop. Resize updates ActualMin
// only.
type ScheduleProposal struct {
	Proposal   *types.Proposal
	Speakers   []*types.SpeakerConf
	ConfTalkID string // "" when not yet placed
	StartMin   int    // minute-of-day, 0 when unscheduled
	DesiredMin int
	ActualMin  int
	TopPx      int // grid Y when placed
	HeightPx   int
	// AvailDays is the intersection of every speaker's Availability
	// (short labels like "Sat", "Sun"). Empty + NoAvail=true means
	// speakers don't share a single day. Empty + NoAvail=false
	// means we couldn't resolve any availability data at all.
	AvailDays []string
	NoAvail   bool
	// HasDrift means the ConfTalk's current content (start /
	// end / title / conf-tag) hashes to something different
	// from the value stamped in CalNotif — i.e. attendees'
	// calendars are out-of-sync with the schedule UI. Drives
	// the orange tint on the card so the admin can spot which
	// talks need a "Send Cal Updates" click.
	// True only for Scheduled proposals (no CalNotif → no
	// baseline to drift from).
	HasDrift bool
}

// EventBlock collects every relationship the dashboard's user has with
// a single conference. Any non-empty field renders its own subsection
// on the dashboard; empty fields are skipped.
type EventBlock struct {
	Conf        *types.Conf
	SpeakerConf *types.SpeakerConf // nil = not a speaker at this conf
	VolApp      *types.Volunteer   // nil = not a volunteer at this conf
	VolInfo     *types.VolInfo     // orientation info (when VolApp != nil)
	Tickets     []*types.Registration
	// CanBuy when the conf is still selling tickets (Active +
	// future). Used to show a "Buy more tickets" / "Get a ticket"
	// CTA inside the block alongside any tickets the user already
	// has.
	CanBuy bool
	// AdminRole is set to "admin" or "volcoord" when the user has
	// a matching role for this conf — drives the "Admin" / "Vol
	// coord" link on the conf card. Empty when the user has no
	// admin relationship with the event.
	AdminRole string
}

type DashboardStats struct {
	TalksApplied  int
	TalksAccepted int
	ShiftsApplied int
	ShiftsBooked  int
}

// UserTicket bundles a Registration with its resolved Conf for
// dashboard rendering.
type UserTicket struct {
	Reg  *types.Registration
	Conf *types.Conf
}

type EditProposalPage struct {
	Proposal   *types.Proposal
	Conf       *types.Conf
	HMAC       string
	Email      string
	Locked     bool
	LockReason string
	TalkTypes  []string
	Durations  []int
	Year       uint
}

// TalkDetailsPage drives the read-only proposal summary view at
// /dashboard/talks/{id}/details — surfaced from the dashboard for
// proposals in a terminal status (TheyDecline / WeDecline / Rejected)
// where editing is no longer applicable but the user may still want
// to look back at what they submitted.
type TalkDetailsPage struct {
	Proposal *types.Proposal
	Conf     *types.Conf
	Speakers []*types.SpeakerConf
	HMAC     string
	Email    string
	Year     uint
}

// InviteCoSpeakerPage is the inviter-side share-a-link page on the
// dashboard. Renders the talk + conf header so the speaker can confirm
// what they're inviting onto, plus a copyable URL.
type InviteCoSpeakerPage struct {
	Proposal  *types.Proposal
	Conf      *types.Conf
	HMAC      string
	Email     string
	InviteURL string
	Year      uint
}

// EditSpeakerPage drives /dashboard/speaker — the single-row Speakers
// editor. Mode is "edit" when the user already has a Speaker record
// (Speaker non-nil) and "create" when they don't yet (volunteer- or
// ticket-only contacts who want to add themselves to the speakers DB).
type EditSpeakerPage struct {
	Speaker      *types.Speaker
	HMAC         string
	Email        string
	EmailPlain   string // not base64 — used as the value for the create-mode email field
	Mode         string // "edit" | "create"
	FlashMessage string
	IsAdmin      bool
	BackURL      string
	FormAction   string
	Year         uint
}

type EditSpeakerConfPage struct {
	SpeakerConf         *types.SpeakerConf
	Conf                *types.Conf
	HMAC                string
	Email               string
	Locked              bool
	LockReason          string
	DaysList            []types.CheckItem
	RecordingOptions    []types.CheckItem
	IsReturningAttendee bool // hides the "first bitcoin++" checkbox
	// RSVPFor is the speakers'-dinner date label ("Mon. Jan 5, 2026"),
	// shown next to the DinnerRSVP toggle so the user knows which day
	// they're agreeing to attend. Set from conf.DaysList()[0].
	RSVPFor    string
	IsAdmin    bool
	BackURL    string
	FormAction string
	Year       uint
}

type ShiftDisplay struct {
	Shift       *types.WorkShift
	IsAvailable bool   // Vol available on that day
	IsEligible  bool   // Not on WillNotWork list
	IsFull      bool   // No spots left
	IsSelected  bool   // Already assigned
	Conflicts   bool   // Overlaps with selected shift
	CanSelect   bool   // Computed eligibility
	Reason      string // Why can't select
}

type ShiftSignupPage struct {
	Vol            *types.Volunteer
	Conf           *types.Conf
	AvailShifts    map[string][]*ShiftDisplay // Grouped by day
	SelectedShifts []*types.WorkShift
	MinShifts      int
	ShiftProgress  int
	CanSubmit      bool
	ConfRef        string
	Email          string
	HMAC           string
	DaysList       []types.CheckItem
	YesJobs        []types.CheckItem
	NoJobs         []types.CheckItem
	Year           uint
}

type VolAdminPage struct {
	Conf                   *types.Conf
	Volunteers             []*types.Volunteer
	Shifts                 []*types.WorkShift
	VolInfo                *types.VolInfo
	OrientationStartInput  string
	OrientationEndInput    string
	OrientationRecipientCt int
	StatusFilter           string
	Missives               []*mtypes.Letter
	FlashMessage           string
	Year                   uint
	EmailCompose           *EmailComposeData
	DeclineTitle           string
	DeclineBody            string
	Stats                  *VolAdminStats
}

// VolAdminStats are derived shift+volunteer counts shown in the
// dashboard at the top of the admin page. Always computed against
// the *unfiltered* volunteer list so the numbers stay stable as the
// user clicks status-filter chips.
type VolAdminStats struct {
	ShiftsFilled    int // assignment count, summed across all shifts
	ShiftsTotal     int // sum of WorkShift.MaxVols
	ShiftsLeft      int // ShiftsTotal - ShiftsFilled
	UnscheduledVols int // # of vols in Applied or PendingShifts
	VolsNeeded      int // ceil(ShiftsLeft / VolShiftQuota)
}

// VolShiftQuota is the per-volunteer shift-count target used to
// translate "shifts left to fill" into "volunteers still needed."
// Mirrors the auto-assign default (3 shifts per vol).
const VolShiftQuota = 3

type ShiftDayGroup struct {
	Date     string             // "01/02/2006"
	DateDesc string             // "Mon. Jan 2"
	MinHour  int                // earliest start hour across this day's shifts
	MaxHour  int                // latest end hour
	Shifts   []*types.WorkShift // sorted by start time
}

type VolAdminShiftsPage struct {
	Conf     *types.Conf
	Days     []*ShiftDayGroup
	VolMap   map[string]*types.Volunteer // ref → volunteer for assignee resolution
	JobTypes []*types.JobType
	DaysList []types.CheckItem // for shift form day selector
	Year     uint
}

type GiftRow struct {
	Clipart     string
	SpeakerName string
}

type SpeakerRow struct {
	ID      string
	Name    string
	Email   string
	Signal  string
	Photo   string // bare filename in Spaces speakers/, "" if unset
	CardURL string
	// Per-conf info from the matching SpeakerConf row. Empty
	// when the speaker has no SpeakerConf for this conf yet
	// (admin-imported speaker, freshly attached, etc.).
	SpeakerConfID string
	ComingFrom    string
	Company       string
	OrgLogo       string // bare filename in Spaces sponsors/, "" if unset
	// Talks on this conf that the speaker is on. One entry per
	// proposal, with the per-talk status pill source.
	Talks []*SpeakerRowTalk
	// OnlySoftStatuses is true when the speaker has at least
	// one talk AND every one of those talks is in {Waitlisted,
	// Invited}. Drives the admin filter "hide speakers with
	// only soft-status proposals" — useful for narrowing the
	// roster to the confirmed-ish cohort.
	OnlySoftStatuses bool
}

// SpeakerRowTalk is one chip in a speaker's row — the talks they're
// attached to for this conf. ProposalID is the Notion page ID, used
// to link to the admin edit page.
type SpeakerRowTalk struct {
	ProposalID string
	Title      string
	Status     string
}

type RegistrationsAdminPage struct {
	Conf          *types.Conf
	Registrations []*types.Registration
	FlashMessage  string
	// IsConfAdmin gates the email composer. Staff get the
	// attendee roster (read-only) but no bulk-email power.
	IsConfAdmin  bool
	Year         uint
	EmailCompose *EmailComposeData
}

// ProposalAdminRow is one card on /{conf}/admin/applicants. The
// "Talk Proposals" view shows every proposal scheduled for the
// conf as a labeled card with speakers, venue, talktime, and a
// status-aware calendar-invite button.
type ProposalAdminRow struct {
	Proposal *types.Proposal
	// Speaker is the FIRST resolved speaker on the proposal —
	// kept for back-compat with the existing email-compose
	// template data (which expects a single .Speaker). For
	// display, Speakers is the full list.
	Speaker  *types.Speaker
	Speakers []*types.Speaker
	// ConfTalk is the scheduled-talk row when one exists; nil
	// for proposals that haven't been scheduled yet (or have
	// moved to a terminal-decline status post-schedule).
	ConfTalk *types.ConfTalk
	// Display-only derived fields. VenueLabel is the human-
	// readable stage label resolved via ics.MapVenue. Time
	// labels render in the conf's local timezone. Durations
	// are in minutes.
	VenueLabel         string
	StartLabel         string
	EndLabel           string
	DurationActualMin  int
	DurationDesiredMin int
	// CalState drives the per-card cal-invite button:
	//   ""        — proposal isn't scheduled, no button
	//   "none"    — scheduled but no CalNotif yet ("Send cal invite")
	//   "fresh"   — CalNotif present, hash matches ("Resend cal invite")
	//   "stale"   — CalNotif present, hash differs ("Update cal invite")
	CalState    string
	TalkCardURL string
}

type ProposalAdminPage struct {
	Conf         *types.Conf
	Rows         []*ProposalAdminRow
	FlashMessage string
	Year         uint
	EmailCompose *EmailComposeData
}

type SpeakerAdminPage struct {
	Conf         *types.Conf
	Rows         []*SpeakerRow
	FlashMessage string
	// IsConfAdmin gates the email composer + send-calendar
	// buttons. Staff (read-only) see the speaker roster but
	// can't email or fan out calendar invites.
	IsConfAdmin  bool
	Year         uint
	EmailCompose *EmailComposeData
}

type EmailFieldGroup struct {
	Name    string
	Items   []string
	IsRange bool
}

type EmailComposeData struct {
	Title            string
	Description      string
	TitlePlaceholder string
	BodyPlaceholder  string
	Fields           []EmailFieldGroup
}

type SponsorFormPage struct {
	Confs       []*types.Conf
	ConfItems   []types.CheckItem
	SponsorOpps []types.CheckItem
	Year        uint
}

type SocialSpeakerRow struct {
	ID              string
	TalkID          string
	Name            string
	TwitterHandle   string
	TalkName        string
	SpeakerPhotoURL string
	PhotoURL        string
	InstaPhotoURL   string
	PostText        string
}

type SocialTalkRow struct {
	ID           string
	Name         string
	SpeakerNames string
	PostText     string
	PhotoURL     string
}

type SocialSponsorRow struct {
	Ref      string
	OrgName  string
	Twitter  string
	Level    string
	CardURL  string
	PostText string
}

type SocialAdminPage struct {
	Conf             *types.Conf
	SpeakerRows      []*SocialSpeakerRow
	TalkRows         []*SocialTalkRow
	SponsorRows      []*SocialSponsorRow
	SponsorBatchText string
	FlashMessage     string
	Year             uint
	BufferOK         bool
}

type TalksGiftsPage struct {
	Conf     *types.Conf
	Rows     []*GiftRow
	FilePath string
	Year     uint
}

type VolDetailsPage struct {
	Conf           *types.Conf
	Vol            *types.Volunteer
	AllShifts      []*types.WorkShift
	ShiftDisplays  map[string][]*ShiftDisplay
	SelectedShifts []*types.WorkShift
	DayKeys        []string
	JobTypes       []*types.JobType
	YesJobs        []types.CheckItem
	NoJobs         []types.CheckItem
	DaysList       []types.CheckItem
	Statuses       []string
	Year           uint
}
