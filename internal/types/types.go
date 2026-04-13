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
		Google            GoogleConfig
		OpenNode          OpenNodeConfig
		Host              string
		LocalExternal     string
		HMACSecret        string
		HMACKey           [32]byte
	}

	GoogleConfig struct {
		Key    string
		Config string
	}

	Conf struct {
		Ref           string
		UID           uint64
		Tag           string
		Active        bool
		Desc          string
		Tagline       string
		DateDesc      string
		StartDate     time.Time
		EndDate       time.Time
		Location      string
		Venue         string
		ShowAgenda    bool
		ShowTalks     bool
		ShowHackathon bool
		HasSatellites bool
		Tickets       []*ConfTicket
		TixSold       uint
		OGFlavor      string
		Emoji         string
	}

	ConfTicket struct {
		ID       string
		ConfRef  string
		Tier     string
		Local    uint
		BTC      uint
		USD      uint
		Expires  *Times
		Max      uint
		Currency string
		Symbol   string
		PostSymbol   string
	}
	ConfTickets []*ConfTicket

	TixForm struct {
		Email         string
		Subscribe     bool
		Count         uint
		DiscountPrice uint
		Discount      string
		DiscountRef   string
		HMAC          string
	}

	DiscountCode struct {
		Ref        string
		CodeName   string
                Discount   string
		PercentOff uint
		ConfRef    []string
	}

	Speaker struct {
		ID       string
		Name     string
		Email    string
		Photo    string
		Twitter  string
		Github   string
		Website  string
		Nostr    string
		Company  string
		OrgPhoto string
	}
	Speakers []*Speaker

	Talk struct {
		ID          string
		Name        string
		Description string
		Clipart     string
		Sched       *Times
		TimeDesc    string
		Duration    string
		DayTag      string
		Type        string
		Venue       string
		Event       string
		AnchorTag   string
		Section     string
		Speakers    []*Speaker
		CalNotif    string
	}

	Session struct {
		Name      string
		Speakers  []*Speaker
		TalkPhoto string
		Sched     *Times
		StartTime string
		Len       string
		DayTag    string
		Type      string
		Venue     string
		AnchorTag string
		ConfTag   string
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

	AuthToken struct {
		CreatedAt *time.Time
		Token     string
	}
	AuthTokens []*AuthToken

	DayTime int

	Hotel struct {
		ID       string
		ConfRef  string
		Name     string
		URL      string
		PhotoURL string
		Type     string
		Desc     string
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
                Twitter       string
                Nostr         string
                Shirt         string
                WorkShifts    []*WorkShift
                Captcha       int
                Subscribe     bool
                Status        string
                CreatedAt     *time.Time
        }

        WorkShift struct {
                Ref string
                Name string
                MaxVols uint
                // TODO: change to Volunteers?
                AssigneesRef []string
                ShiftLeaderRef string
                Type *JobType
                Conf *Conf
                ShiftTime *Times
                Priority uint
        }

        JobType struct {
                Ref      string
                Tag      string
                DisplayOrder int
                Title    string 
                Tooltip  string
                LongDesc string
                Show     bool
        }

        TalkApp struct {
                Ref           string
                Name          string
                Phone         string
                Email         string
                Signal        string
                Telegram      string
                ContactAt     string
                Hometown      string
                Twitter       string
                Nostr         string
                Github        string
                Website       string
                Visa          string
                Pic           string
                Org           string
                Sponsor       bool
                OrgTwitter    string
                OrgNostr      string
                OrgSite       string
                OrgLogo       string
                TalkTitle     string
                Description   string
                PresType      string
                TalkSetup     bool
                DinnerRSVP    bool
                Availability  []string
                DiscoveredVia string
                Shirt         string
                ScheduleFor   []*Conf
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

func (t *Talk) ClipartAvif() string {
	name := strings.TrimSuffix(t.Clipart, filepath.Ext(t.Clipart))
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

func (t *Times) Day() string {
	return t.Start.Format("Monday")
}

func (t *Times) FmtRange() string {
        start := t.Desc() 
        end := ""
        if t.End != nil {
                end = t.End.Format("- 3:04pm")
        }
        return start + end
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

func (c *Conf) InFuture() bool {
        return c.StartDate.After(time.Now())
}

func (c *Conf) WithinTwoWeeks() bool {
        return time.Until(c.StartDate) <= 12 * 24 * time.Hour
}

func (c *Conf) DateBeforeStart(daysbefore int) string {
        start := c.StartDate.AddDate(0, 0, daysbefore * -1)
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
                        ItemID: prefix + d.Format("01/02/2006"),
                        ItemDesc: d.Format("Mon. Jan 2, 2006"),
                        Checked: true,
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

func (vol *Volunteer) RegisID() (string) {
        conf := vol.ScheduleFor[0]
        return RegistrationHash("volreg", conf.Ref, vol.Email)
}

func (vol *Volunteer) TicketRef() (string) {
        tixID := vol.RegisID()
	return UniqueID(vol.Email, tixID, int32(0))
}

func (vol *Volunteer) ParseAvailability(prefix string, form url.Values) (error) {
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

func (talkapp *TalkApp) ParseAvailability(prefix string, form url.Values) (error) {
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

/* Functions to sort AuthTokens */
func (at AuthTokens) Len() int {
	return len(at)
}

func (at AuthTokens) Swap(i, j int) {
	at[i], at[j] = at[j], at[i]
}

/* I want most recent first */
func (at AuthTokens) Less(i, j int) bool {
	/* Sort by time */
	if at[i].CreatedAt == nil && at[j].CreatedAt == nil {
		return false
	} else if at[j].CreatedAt == nil {
		return true
	} else if at[i].CreatedAt == nil {
		return false
	}

	return (*at[j].CreatedAt).Before(*at[i].CreatedAt)
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
	indx := strings.LastIndex(s.Twitter, "/")
	if indx == -1 {
		return ""
	}
	handle := s.Twitter[indx+1:]
	if strings.HasPrefix(handle, "@") {
		return handle[1:]
	}
	return handle
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
