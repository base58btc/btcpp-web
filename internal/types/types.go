package types

import (
	"strings"
	"time"
	"fmt"
)

type (

	/* Configs for the app! */
	EnvConfig struct {
		Port     string
		Prod     bool
		MailerSecret string
		MailerJob int
		MailOff   bool
		StripeKey string
		StripeEndpointSec string
		OpenNodeKey string
		RegistryPin string
		LogFile  string
		Notion   NotionConfig
		SendGrid SendGridConfig
		Google   GoogleConfig
		Host string
		Tickets []string
	}

	GoogleConfig struct {
		Key string
	}

	Speaker struct {
		Name string
		Desc string
		Org string
		Photo string
		Github string
		Twitter string
	}

	Talk struct {
		ID string
		Name string
		Email string
		Description string
		Clipart string
		Setup string
		Photo string
		Website string
		Twitter string
		BadgeName string
		Company string
		Sched   *Times
		TimeDesc string
		Duration string
		DayTag  string
		Type    string
		Venue   string
		AnchorTag string
	}

	Ticket struct {
		ID string
		Pdf []byte
	}

	Times struct {
		Start time.Time
		End   *time.Time
	}

	Registration struct {
		RefID string
		Type string
		Email string
		ItemBought string
	}

	Item struct {
		Total    int64
		Desc     string
	}

	Entry struct {
		ID       string
		Total    int64
		Currency string
		Created  time.Time
		Email    string
		Items    []Item
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
	}

	return 5
}

func (t *Times) Desc() string {
	// Sat. Apr 29, 2020 @ 10a
	return t.Start.Format("Mon. Jan 2, 2006 @ 3:04 pm")
}

func (t *Times) StartTime() string {
	// 10 am
	return fmt.Sprintf("%s - %s", t.Start.Format("3:04 pm"), t.End.Format("3:04 pm"))
}

func (t *Times) Day() string {
	return t.Start.Format("Monday")
}

func (t *Times) LenStr() string {
	if t.End == nil{
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
