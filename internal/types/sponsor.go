package types

type (
	Org struct {
		Ref         string
		Name        string
                Tagline     string
		LogoLight   string // URL to light mode logo on Spaces
		LogoDark    string // URL to dark mode logo on Spaces
		Email       string
		Website     string
		LinkedIn    string
		Instagram   string
		Youtube     string
		Github      string
		Twitter     string
		Nostr       string
		Matrix      string
		Hiring      bool
		Notes       string
	}

	Sponsorship struct {
		Ref           string
                Org           *Org
		Confs         []*Conf
		Level         string
		Status        string
		IsVendor      bool
		Notes         string
	}
)
