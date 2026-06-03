package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type (

	/* Configs for the app! */
	EnvConfig struct {
		Port              string
		Prod              bool
		MailerSecret      string
		MailerJob         int
		MailOff           bool
		MailEndpoint      string
		StripeKey         string
		StripeEndpointSec string
		RegistryPin       string
		LogFile           string
		Notion            NotionConfig
		SendGrid          SendGridConfig
		OpenNode          OpenNodeConfig
		Host              string
		LocalExternal     string
		HMACSecret        string
		HMACKey           [32]byte
		DatabaseURL       string
		DataBackend       string
		BufferAPI         string
		Spaces            SpacesConfig
		CacheTTLSec       int
		NotionRequestLogs bool
		YouTube           YouTubeConfig
		Recordings        RecordingsConfig
	}

	// YouTubeConfig holds the OAuth client + redirect that backs the
	// admin recordings uploader. Empty fields → uploader is disabled
	// (the admin page surfaces a "set env vars" warning rather than
	// crashing on a nil oauth2.Config).
	YouTubeConfig struct {
		ClientID     string
		ClientSecret string
		RedirectURL  string
	}

	// RecordingsConfig controls the scheduled recording publisher. The
	// dashboard remains available when this is disabled; only the
	// background autopublisher and browser automation are gated here.
	RecordingsConfig struct {
		AutopublishEnabled bool
		PollSec            int
		NotifyEmail        string
		EncryptionKey      string
		YouTubeTokenObject string
		X                  XUploaderConfig
	}

	// XUploaderConfig holds the x.com browser automation settings.
	// Chrome profiles are encrypted into Spaces so App Platform's
	// ephemeral filesystem does not wipe login state on deploy.
	XUploaderConfig struct {
		Enabled        bool
		ProfileObject  string
		Headed         bool
		LoginUsername  string
		LoginPassword  string
		PostTimeoutSec int
		AuthWaitSec    int
	}

	Conf struct {
		Ref       string
		UID       uint64
		Tag       string
		Active    bool
		Desc      string
		Tagline   string
		DateDesc  string
		StartDate time.Time
		EndDate   time.Time
		Location  string
		Venue     string
		// VenueMap is a Google Maps / OpenStreetMap link to the
		// venue's location; rendered in the ticket email.
		VenueMap string
		// VenueWebsite is the venue's official site; rendered in
		// the ticket email so attendees can read about it directly.
		VenueWebsite string
		// DoorsOpen is the human-readable check-in time string —
		// not a Notion field, populated just-in-time by the ticket
		// email sender before template render so the body can do
		// {{ .Conf.DoorsOpen }} without round-tripping ConfInfo.
		DoorsOpen string
		// HasAgenda is a derived boolean — true when at least one of
		// the conf's talks has Status == "Scheduled". Populated at
		// request time (shallow-copy + set in RenderConf / RenderTalks),
		// never stored in Notion. Drives both the nav-bar "agenda" /
		// "talks" links and the per-conf-template agenda section.
		HasAgenda     bool
		ShowHackathon bool
		HasSatellites bool
		Tickets       []*ConfTicket
		TixSold       uint
		OGFlavor      string
		Emoji         string
		// Timezone is the IANA name of the conference venue's local
		// time (e.g. "Europe/Vienna", "America/Toronto"). Read from
		// the Notion ConfsDb "Timezone" field. Empty when the field
		// hasn't been filled in for this conf yet — callers should
		// fall back via Conf.Loc() rather than reading TZ directly.
		Timezone string
		// TZ is the parsed *time.Location of Timezone, or nil when
		// Timezone is empty / unparseable. Populated once at parseConf
		// time so hot paths don't pay LoadLocation per request.
		TZ *time.Location

		// CountdownStart / CountdownEnd are the conf's doors-open-
		// day-1 and doors-close-last-day timestamps, populated at
		// conf-page render time from ConfInfo (with a fallback to
		// StartDate / EndDate). Drives the countdown widget in
		// conf_nav. Not stored in Notion — recomputed each render.
		CountdownStart *time.Time
		CountdownEnd   *time.Time

		// OrientCalNotif persists the volunteer-orientation
		// invite state for this conf: a "UID:Sequence:Hashbytes"
		// triple in the same shape as ConfTalk.CalNotif. Stored
		// at the conf level rather than per-vol because the
		// orientation is a single broadcast event — every vol
		// gets the same time / venue, and a SEQUENCE bump
		// propagates to all of their calendars at once.
		OrientCalNotif string
	}

	// ConfInfo is the per-day schedule strip for a conference: when
	// doors open, when meals are served, when the coffee break is.
	// One row per conference-day; resolved against Conf.StartDate so
	// the times carry the conf's timezone.
	//
	// The Notion-side row identifies its conf by Tag string (e.g.
	// "atx25") rather than a relation, so ConfTag holds the tag and
	// resolution is a Tag → *Conf lookup at parse time.
	ConfInfo struct {
		ID        string
		ConfTag   string
		Day       int // 1-indexed day-of-conference (Day 1 = StartDate)
		Doors     *Times
		Breakfast *Times
		Lunch     *Times
		Coffee    *Times
		// Venues is the multiselect on the Notion ConfInfo row
		// — the rooms/stages a talk can be scheduled into for
		// this day. Drives the columns of the schedule grid.
		Venues []string
	}

	ConfTicket struct {
		ID         string
		ConfRef    string
		Tier       string
		Local      uint
		BTC        uint
		USD        uint
		Expires    *Times
		Max        uint
		Currency   string
		Symbol     string
		PostSymbol string
	}
	ConfTickets []*ConfTicket

	TixForm struct {
		Email         string
		Subscribe     bool
		Count         uint
		DiscountPrice uint
		Discount      string
		// AffiliateCode is the silent referral that hitches a
		// ride through the form when the visitor came in via a
		// /{tag}?code= link tied to a `%0` affiliate code.
		// On POST the visible Discount wins — typing a different
		// code drops the silent affiliate's credit.
		AffiliateCode string
		DiscountRef   string
		HMAC          string
		PaymentMethod string // "btc" or "fiat"
	}

	DiscountCode struct {
		Ref       string
		CodeName  string
		Discount  string // raw expression (e.g. "%50", "$10:50", "=25:70")
		ConfRef   []string
		UsesCount uint // current usage count from Notion
		// AffiliateEmail is set when the code is owned by a
		// dashboard self-service affiliate. Webhooks read this
		// to decide whether to record an AffiliateUsage row.
		AffiliateEmail string
		// Parsed from Discount expression:
		DiscType   rune       // '%', '$', or '='
		Amount     uint       // the number value
		MaxUses    uint       // from :N modifier, 0 = unlimited
		ExtraQty   uint       // from +N modifier (BOGO), 0 = not BOGO
		ValidFrom  *time.Time // from @ modifier, nil = no start restriction
		ValidUntil *time.Time // from < or @ modifier, nil = no end restriction
	}

	// AffiliateUsage is one row in the AffiliateUsageDb — appended
	// once per successful checkout that consumed an affiliate
	// code. Aggregated by AffiliateEmail to produce the dashboard
	// stats (tickets sold, $ saved, $ earned). CodeName + ConfTag
	// are stored as plain strings rather than Notion relations so
	// the queries are straightforward and rows survive code
	// renames.
	AffiliateUsage struct {
		ID             string
		CodeName       string
		AffiliateEmail string
		ConfTag        string
		SavedSats      int64
		EarnedSats     int64
		TicketsCount   uint
		Created        *time.Time
	}

	Speaker struct {
		ID            string
		Name          string
		Photo         string
		Email         string
		Signal        string
		Phone         string
		Telegram      string
		Twitter       Twitter
		Nostr         string
		Github        string
		Instagram     string
		LinkedIn      string
		Website       string
		Company       string
		OrgLogo       string
		AvailToHire   bool
		LookingToHire bool
		TShirt        string
		// Roles drives admin-panel access. Each entry is the raw
		// multi-select tag from the Speakers DB Roles column —
		// e.g. "vienna-admin", "global-volcoord". Parsed by the
		// auth package; an empty slice means no admin access.
		Roles []string
	}
	Speakers []*Speaker

	Proposal struct {
		ID              string
		Name            string
		Title           string
		Description     string
		Setup           string
		Comments        string
		TalkType        string
		Status          string
		DesiredDuration int
		AvailDuration   int
		ScheduleFor     *Conf
		// SpeakerConfRefs is the raw page-ID list from the
		// "speakers" multi-relation on Proposal. Resolution into
		// *SpeakerConf objects happens at the consumer layer.
		SpeakerConfRefs []string
		Speakers        []*SpeakerConf

		// InviteToken authenticates the public co-speaker invite
		// link. Empty means "no active invitation"; admins clear
		// or rotate the field in Notion to revoke an outstanding
		// share link.
		InviteToken string

		// Optional attachments populated by the dashboard enricher
		// for the talk-card render. Nil unless explicitly fetched.
		ConfTalk  *ConfTalk
		Recording *Recording
	}

	SpeakerConf struct {
		ID         string
		ComingFrom string
		Speaker    *Speaker
		// Proposals is the multi-relation `talk` on SpeakerConf —
		// every proposal this speaker is delivering at this conf.
		Proposals    []*Proposal
		Availability []string
		RecordOK     string
		Visa         string
		FirstEvent   bool
		DinnerRSVP   bool
		Sponsor      bool
		Company      string
		OrgPhoto     string
		OtherEvents  []*Conf
		// Invite-flow audit trail: set by the admin "Invite a
		// Speaker" flow and the magic-link landing page.
		// InvitedAt is when the admin sent the invite, ViewedAt
		// is the first time the speaker opened the magic link,
		// AcceptedAt is when they clicked Accept.
		InvitedAt  *time.Time
		ViewedAt   *time.Time
		AcceptedAt *time.Time
	}

	ConfTalk struct {
		ID              string
		Conf            *Conf
		Proposal        *Proposal
		Clipart         string
		Sched           *Times
		ProductionNotes string
		Venue           string
		Section         string
		CalNotif        string
		SocialCard      string
	}

	// Recording is a row in RecordingsDb — one per ConfTalk that has
	// a YouTube link (and eventually other recording metadata).
	//
	// FileURI is the Spaces object key for the source video (rich_text
	// column "FileURI" on Notion). Populated by the admin before the
	// longform-upload tool can publish to YouTube / X.
	//
	// XLink is the X.com (Twitter) post URL — written back when the
	// admin posts the recording to X. Empty until then.
	Recording struct {
		ID         string
		ConfTalkID string
		TalkName   string
		YTLink     string
		XLink      string
		XReplyLink string
		FileURI    string
		PublishAt  *time.Time
	}

	SocialPost struct {
		ID               string
		Ref              string
		Text             string
		PostedTo         string
		Kind             string
		Status           string
		RecordingID      string
		ConfTalkID       string
		URL              string
		ReplyURL         string
		Error            string
		ErrorFingerprint string
		ScheduledAt      *time.Time
		PostedAt         *time.Time
		NotifiedAt       *time.Time
	}

	Talk struct {
		ID          string
		Name        string
		Description string
		Clipart     string
		Sched       *Times
		TimeDesc    string
		Duration    string
		Type        string
		Venue       string
		Event       string
		Section     string
		Speakers    []*Speaker
		CalNotif    string
		TalkCardURL string
		// Status mirrors the underlying Proposal.Status so the conf
		// page's speaker list can filter to Accepted-only without
		// re-fetching proposals. Empty when this Talk wasn't sourced
		// from a Proposal (defensive).
		Status string
		// YTLink is the YouTube URL when a Recording row exists for
		// this talk's ConfTalk. Drives the "Watch" badge on the
		// agenda + /talks pages.
		YTLink string
	}

	Session struct {
		Name      string
		Speakers  []*Speaker
		TalkPhoto string
		Sched     *Times
		StartTime string
		Len       string
		Type      string
		Venue     string
		AnchorTag string
		ConfTag   string
		YTLink    string // populated when a Recording row exists for this talk
	}

	Ticket struct {
		ID  string
		Pdf []byte
	}

	Times struct {
		Start time.Time
		End   *time.Time
	}

	Registration struct {
		RefID      string
		ConfRef    string
		Type       string
		Email      string
		ItemBought string
		// Amount is the buyer-paid price in main units (dollars,
		// euros, …) — Notion stores the AddTickets-written number
		// already pre-divided by 100. Currency is the ISO code as
		// chosen at checkout. Both can be zero / blank for legacy
		// rows that pre-date the field.
		Amount   float64
		Currency string
		// Revoked is the Notion "Revoked" checkbox. When true, the
		// ticket is voided (refund / chargeback / admin reversal)
		// and should be hidden from the buyer's dashboard. Stays in
		// the cache so admin-side reporting / staffing decisions
		// can still see it.
		Revoked bool
	}

	Item struct {
		Total int64
		Desc  string
		Type  string
	}

	Entry struct {
		ID          string
		ConfRef     string
		Total       int64
		Currency    string
		Created     time.Time
		Email       string
		Items       []Item
		DiscountRef string
	}

	ShirtSize string

	DayTime int

	Hotel struct {
		ID      string
		ConfRef string
		Name    string
		URL     string
		// Img is the Spaces object path (e.g.
		// "atx25/hotels/abc123.jpg") — no domain. Templates
		// resolve via {{ spacesURL .Img }}. Hotels with empty
		// Img are skipped at render time.
		Img  string
		Type string
		Desc string
		// Order is the display rank within a conf (smaller =
		// earlier). Edited from /{conf}/admin/hotels.
		Order int
	}

	VolInfo struct {
		Ref         string
		ConfRef     string
		OrientLink  string
		OrientTimes *Times
		Notes       string
	}

	Volunteer struct {
		Ref           string
		Name          string
		Email         string
		Phone         string
		Signal        string
		Availability  []string
		ContactAt     string
		Comments      string
		DiscoveredVia string
		ScheduleFor   []*Conf
		OtherEvents   []*Conf
		WorkYes       []*JobType
		WorkNo        []*JobType
		FirstEvent    bool
		Hometown      string
		Twitter       Twitter
		Nostr         string
		Shirt         string
		WorkShifts    []*WorkShift
		Captcha       int
		Subscribe     bool
		Status        string
		CreatedAt     *time.Time
	}

	WorkShift struct {
		Ref     string
		Name    string
		MaxVols uint
		// TODO: change to Volunteers?
		AssigneesRef   []string
		ShiftLeaderRef string
		Type           *JobType
		Conf           *Conf
		ShiftTime      *Times
		Priority       uint
		CalNotif       string
	}

	JobType struct {
		Ref          string
		Tag          string
		DisplayOrder int
		Title        string
		Tooltip      string
		LongDesc     string
		Show         bool
	}

	TalkApp struct {
		Ref           string
		Status        string
		Name          string
		Phone         string
		Email         string
		Signal        string
		Telegram      string
		ContactAt     string
		Hometown      string
		Twitter       Twitter
		Nostr         string
		Github        string
		Website       string
		Visa          string
		Pic           string
		NormPhoto     string
		Org           string
		Sponsor       bool
		OrgTwitter    Twitter
		OrgNostr      string
		OrgSite       string
		OrgLogo       string
		TalkTitle     string
		Description   string
		PresType      string
		Recording     string
		Setup         string
		TalkSetup     bool
		DinnerRSVP    bool
		Availability  []string
		DiscoveredVia string
		Shirt         string
		ScheduleFor   *Conf
		OtherEvents   []*Conf
		Comments      string
		FirstEvent    bool
		Subscribe     bool
		Captcha       int
	}
)

const (
	Morning DayTime = iota
	Afternoon
	Evening
)

// Placeholder strings stamped onto a freshly admin-invited proposal
// before the speaker has filled the form. The magic-link form uses
// PlaceholderTitlePrefix to detect "this proposal still needs talk
// content" and unhide the title/description/setup/length fields that
// are otherwise hidden in InviteMode.
const (
	PlaceholderTitlePrefix = "TBD ("
	PlaceholderDescription = "Description to come"
)

var daytimenames = map[DayTime]string{
	Morning:   "01morning",
	Afternoon: "02afternoon",
	Evening:   "03evening",
}

func (dt DayTime) String() string {
	return daytimenames[dt]
}

var DayTimeChars = map[string]DayTime{
	"+": Morning,
	"=": Afternoon,
	"-": Evening,
}

// Desc is a template-friendly alias for the Description field —
// matches the Notion column name (which is "Desc") and lets letter
// templates use the same name they see in the schema.
func (p *Proposal) Desc() string {
	if p == nil {
		return ""
	}
	return p.Description
}

func (t *Talk) AnchorTag() string {
	if len(t.Clipart) <= 4 {
		return t.Clipart
	}
	return t.Clipart[:len(t.Clipart)-4]
}

// AnchorTag returns the same value as Talk.AnchorTag would for
// this conftalk's Clipart filename — used by dashboard / agenda
// templates that have the ConfTalk handy but not the derived
// Talk struct.
func (ct *ConfTalk) AnchorTag() string {
	if ct == nil || len(ct.Clipart) <= 4 {
		if ct == nil {
			return ""
		}
		return ct.Clipart
	}
	return ct.Clipart[:len(ct.Clipart)-4]
}

// TypeLongDesc returns this shift's JobType.LongDesc when set, "" otherwise.
// Lets templates pull a Description for cal-invite UI without inline nil
// checks against the optional *JobType pointer.
func (s *WorkShift) TypeLongDesc() string {
	if s == nil || s.Type == nil {
		return ""
	}
	return s.Type.LongDesc
}

// AnchorOrID returns AnchorTag when the conftalk has a clipart, and
// the raw page ID otherwise. Lets the "Add to calendar" download URL
// resolve before an admin has uploaded a clipart — important on the
// dashboard where a Scheduled talk should always offer the cal-invite
// fallback, clipart or not. The TalkPublicICS handler matches on
// either form.
func (ct *ConfTalk) AnchorOrID() string {
	if ct == nil {
		return ""
	}
	if a := ct.AnchorTag(); a != "" {
		return a
	}
	return ct.ID
}

func (t *Talk) ClipartAvif() string {
	name := strings.TrimSuffix(t.Clipart, filepath.Ext(t.Clipart))
	return name + ".avif"
}

func (c *ConfTalk) ClipartAvif() string {
	if c == nil || c.Clipart == "" {
		return ""
	}
	name := strings.TrimSuffix(c.Clipart, filepath.Ext(c.Clipart))
	return name + ".avif"
}

func (s *Session) TalkAvif() string {
	name := strings.TrimSuffix(s.TalkPhoto, filepath.Ext(s.TalkPhoto))
	return name + ".avif"
}

func (s *Session) BeginsAt() string {
	return s.Sched.Start.Format("15:04")
}

func (env *EnvConfig) GetDomain() string {
	if env.Port != "" && !env.Prod {
		return fmt.Sprintf("%s:%s", env.Host, env.Port)
	}

	return env.Host
}

func (env *EnvConfig) GetURI() string {
	if env.Prod {
		return fmt.Sprintf("https://%s", env.GetDomain())
	}

	if env.LocalExternal != "" {
		return env.LocalExternal
	}

	return fmt.Sprintf("http://%s", env.GetDomain())
}

/* Silly thing to return a value for a venue, for ordering */
func (t *Talk) VenueValue() int {
	switch t.Venue {
	case "p2pkh":
		return 0
	case "p2wsh":
		return 1
	case "multisig":
		return 2
	case "p2tr":
		return 3
	case "p2sh-p2wpkh":
		return 4
	case "one":
		return 0
	case "two":
		return 1
	case "three":
		return 2
	case "four":
		return 3
	}

	return 5
}

func NameVenue(v string) string {
	switch v {
	case "p2pkh":
		return "Main Stage"
	case "p2wsh":
		return "Talking Stage"
	case "multisig":
		return "Workshops"
	case "p2tr":
		return "Workshops 2"
	case "p2sh-p2wpkh":
		return "Talking two"
	case "one":
		return "Main Stage"
	case "two":
		return "Talks Stage"
	case "three":
		return "Workshops Stage"
	case "four":
		return "Lounge Stage"
	}

	return "Not Listed Yet"
}

func (t *Talk) VenueName() string {
	return NameVenue(t.Venue)
}

func (t *Times) Desc() string {
	// Sat. Apr 29, 2020 @ 10a
	return t.Start.Format("Mon. Jan 2, 2006 @ 3:04 pm")
}

func (t *Times) DateDesc() string {
	// Apr 29, 2020
	return t.Start.Format("Jan 2, 2006")
}

func (t *Times) StartTime() string {
	// 10 am
	return fmt.Sprintf("%s - %s", t.Start.Format("3:04 pm"), t.End.Format("3:04 pm"))
}

// HourRange formats the start (and end, when set) as "3:04 pm" or
// "3:04 pm - 4:00 pm". Safe to call on a *Times whose End is nil — the
// Doors / Breakfast columns of ConfInfo can legitimately be a single
// instant rather than a range.
func (t *Times) HourRange() string {
	s := t.Start.Format("3:04 pm")
	if t.End == nil {
		return s
	}
	return s + " - " + t.End.Format("3:04 pm")
}

func (t *Times) Day() string {
	return t.Start.Format("Monday")
}

func (t *Times) FmtRange() string {
	start := t.Desc()
	end := ""
	if t.End != nil {
		end = t.End.Format("- 3:04pm")
	}
	tz, _ := t.Start.Zone()
	return start + end + " " + tz
}

func (t *Times) LenStr() string {
	if t.End == nil {
		return ""
	}
	dur := t.End.Sub(t.Start)
	d := dur.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute

	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

func datesBetween(start, end time.Time) []time.Time {
	var dates []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d)
	}
	return dates
}

// Loc returns the conference's local timezone. Prefers the explicit
// Timezone field (parsed into TZ at parseConf time); falls back to
// StartDate.Location() when the conf row hasn't been migrated to
// have a Timezone yet — same behavior as before this field existed.
// Always non-nil so callers can use it directly with time.Date.
func (c *Conf) Loc() *time.Location {
	if c.TZ != nil {
		return c.TZ
	}
	return c.StartDate.Location()
}

func (c *Conf) InFuture() bool {
	return c.StartDate.After(time.Now())
}

func (c *Conf) WithinTwoWeeks() bool {
	return time.Until(c.StartDate) <= 12*24*time.Hour
}

// HasEnded reports whether the conf is over (EndDate is in the past).
// Used by the dashboard to fold past confs into a collapsed section.
func (c *Conf) HasEnded() bool {
	if c.EndDate.IsZero() {
		return false
	}
	return c.EndDate.Before(time.Now())
}

// CanInvite reports whether co-speaker invites are still meaningful for
// this conf: the conf is Active and at least 4 days out from start. Inside
// that window the schedule is locked and adding speakers creates more
// problems than it solves.
func (c *Conf) CanInvite() bool {
	return c.Active && time.Until(c.StartDate) > 4*24*time.Hour
}

// TalksDueDays returns the number of days before StartDate at which talk
// applications close. Most confs use 45; some shorter cycles use 35.
//
// Centralized here so dashboard, the apply form, and any deadline-checking
// code stay in sync.
func (c *Conf) TalksDueDays() int {
	if c.Tag == "nairobi" {
		return 35
	}
	return 45
}

// TalksDueDate returns the absolute time at which talk applications close.
func (c *Conf) TalksDueDate() time.Time {
	return c.StartDate.AddDate(0, 0, -c.TalksDueDays())
}

// TalksOpen reports whether talk applications are currently being accepted
// for this conf — Active and before TalksDueDate.
func (c *Conf) TalksOpen() bool {
	return c.Active && time.Now().Before(c.TalksDueDate())
}

// VolunteerOpen reports whether public volunteer applications should be
// available for this conf.
func (c *Conf) VolunteerOpen() bool {
	if c.Tag == "nairobi" {
		return false
	}
	return c.Active && c.InFuture()
}

// EmojiOrDefault returns the conf's emoji, or a sparkles fallback when
// the field is empty so the dashboard never renders a blank tile.
func (c *Conf) EmojiOrDefault() string {
	if c.Emoji == "" {
		return "✨"
	}
	return c.Emoji
}

func (c *Conf) DateBeforeStart(daysbefore int) string {
	start := c.StartDate.AddDate(0, 0, daysbefore*-1)
	return start.Format("Mon. Jan 2, 2006")
}

func (c *Conf) DaysList(prefix string, addone bool) []CheckItem {
	/* Add an setup day before the event starts */
	delta := 0
	if addone {
		delta = -1
	}
	start := c.StartDate.AddDate(0, 0, delta)

	dates := datesBetween(start, c.EndDate)
	items := make([]CheckItem, len(dates))

	for i, d := range dates {
		items[i] = CheckItem{
			ItemID:   prefix + d.Format("01/02/2006"),
			ItemDesc: d.Format("Mon. Jan 2, 2006"),
			Checked:  true,
		}
	}

	return items
}

func RegistrationHash(prefix, confRef, email string) string {
	h := sha256.New()
	h.Write([]byte(email))
	h.Write([]byte(confRef))
	infohash := hex.EncodeToString(h.Sum(nil)[:18])
	return fmt.Sprintf("btcpp-%s-%s", prefix, infohash)
}

func UniqueID(email string, ref string, counter int32) string {
	// sha256 of ref || email || count (4, le)
	h := sha256.New()
	h.Write([]byte(email))
	h.Write([]byte(ref))

	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(counter))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func (vol *Volunteer) RegisID() string {
	conf := vol.ScheduleFor[0]
	return RegistrationHash("volreg", conf.Ref, vol.Email)
}

// SpeakerRegisID is the deterministic ID for a speaker's
// complimentary ticket at a conf — derived from a "spkreg" hash so a
// second self-confirm or admin Mark Confirmed click upserts the same
// row instead of duplicating tickets.
func SpeakerRegisID(confRef, email string) string {
	return RegistrationHash("spkreg", confRef, email)
}

func (vol *Volunteer) TicketRef() string {
	tixID := vol.RegisID()
	return UniqueID(vol.Email, tixID, int32(0))
}

func (vol *Volunteer) ParseAvailability(prefix string, form url.Values) error {
	if vol.Availability == nil {
		vol.Availability = make([]string, 0)
	}
	for k, _ := range form {
		if strings.HasPrefix(k, prefix) {
			vol.Availability = append(vol.Availability, k[len(prefix):])
		}
	}
	return nil
}

func (talkapp *TalkApp) ParseAvailability(prefix string, form url.Values) error {
	if talkapp.Availability == nil {
		talkapp.Availability = make([]string, 0)
	}
	for k, _ := range form {
		if strings.HasPrefix(k, prefix) {
			talkapp.Availability = append(talkapp.Availability, k[len(prefix):])
		}
	}
	return nil
}

const (
	Small ShirtSize = "small"
	Med   ShirtSize = "med"
	Large ShirtSize = "large"
	XL    ShirtSize = "xl"
	XXL   ShirtSize = "xxl"
)

func (s ShirtSize) String() string {
	return string(s)
}

var mapEnumShirtSize = func() map[string]ShirtSize {
	m := make(map[string]ShirtSize)
	m[string(Small)] = Small
	m[string(Med)] = Med
	m[string(Large)] = Large
	m[string(XL)] = XL
	m[string(XXL)] = XXL

	return m
}()

func ParseShirtSize(str string) (ShirtSize, bool) {
	ss, ok := mapEnumShirtSize[strings.ToLower(str)]
	return ss, ok
}

/* FIXME: make this nicer?? */
func (c Conf) HasSchedule() bool {
	return c.Tag == "durham" || c.Tag == "berlin25"
}

/* Functions to sort conference tickets */
func (t ConfTickets) Len() int {
	return len(t)
}

func (t ConfTickets) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

func (s ConfTickets) Less(i, j int) bool {
	/* Sort by time first */
	return s[i].Expires.Start.Before(s[j].Expires.Start)
}

/* Functions to sort Speakers */
func (s Speakers) Len() int {
	return len(s)
}

func (s Speakers) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s Speakers) Less(i, j int) bool {
	return strings.ToUpper(s[i].Name) < strings.ToUpper(s[j].Name)
}

func (s *Speaker) TwitterHandle() string {
	return s.Twitter.Handle
}

func (j *JobType) IsWildcard() bool {
	return j.Tag == "wildcard"
}

func (ws *WorkShift) DayOf() string {
	return ws.ShiftTime.Start.Format("01/02/2006")
}

func (ws *WorkShift) DayOfDesc() string {
	return ws.ShiftTime.Start.Format("Mon. Jan 2")
}

func (ws *WorkShift) SpotsAvailable() uint {
	assigned := uint(len(ws.AssigneesRef))
	if assigned >= ws.MaxVols {
		return 0
	}
	return ws.MaxVols - assigned
}

func (ws *WorkShift) IsFull() bool {
	return ws.SpotsAvailable() == 0
}

func (ws *WorkShift) TimeDesc() string {
	if ws.ShiftTime == nil {
		return ""
	}
	start := ws.ShiftTime.Start.Format("3:04pm")
	if ws.ShiftTime.End != nil {
		return fmt.Sprintf("%s - %s", start, ws.ShiftTime.End.Format("3:04pm"))
	}
	return start
}

func (ws *WorkShift) IsAssigned(volRef string) bool {
	for _, ref := range ws.AssigneesRef {
		if ref == volRef {
			return true
		}
	}
	return false
}

func (v *Volunteer) AvailableOn(ws *WorkShift) bool {
	shiftDay := ws.DayOf()
	for _, day := range v.Availability {
		if day == shiftDay {
			return true
		}
	}
	return false
}

func (v *Volunteer) WillWork(job *JobType) bool {
	for _, yjob := range v.WorkYes {
		if yjob.Ref == job.Ref {
			return true
		}
	}
	return false
}

func (v *Volunteer) WillNotWork(job *JobType) bool {
	for _, njob := range v.WorkNo {
		if njob.Ref == job.Ref {
			return true
		}
	}
	return false
}

func (ws *WorkShift) Intersects(shifts []*WorkShift) bool {
	if ws.ShiftTime == nil {
		return false
	}

	for _, shift := range shifts {
		if shift.ShiftTime == nil {
			continue
		}
		/* this shift starts after other ends, ok */
		if shift.ShiftTime.End == nil {
			continue
		}
		if ws.ShiftTime.Start.After(*shift.ShiftTime.End) {
			continue
		}
		if ws.ShiftTime.End == nil {
			continue
		}
		/* other shift starts after other ends, ok */
		if shift.ShiftTime.Start.After(*ws.ShiftTime.End) {
			continue
		}

		return true
	}

	return false
}
