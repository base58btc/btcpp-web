package types

import (
	"fmt"
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
		Key string
	}

	Conf struct {
		Ref           string
		Tag           string
		Active        bool
		Desc          string
		DateDesc      string
		Venue         string
		ShowAgenda    bool
		ShowTalks     bool
		ShowHackathon bool
		HasSatellites bool
		Color         string
		Tickets       []*ConfTicket
		TixSold       uint
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
	}
	ConfTickets []*ConfTicket

	TixForm struct {
		Email         string
		Count         uint
		DiscountPrice uint
		Discount      string
		DiscountRef   string
		HMAC          string
	}

	DiscountCode struct {
		Ref        string
		CodeName   string
		PercentOff uint
		ConfRef    []string
	}

	Speaker struct {
		ID       string
		Name     string
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
)

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

func (c *Conf) GetColor() string {
	if c.Color == "" {
		return "indigo-600"
	}
	return c.Color
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
