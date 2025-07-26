package types

import (
	"github.com/niftynei/go-notion"
)

type (
	NotionConfig struct {
		Token       string
		EmailDb     string
		PurchasesDb string
		TalksDb     string
		SpeakersDb  string
		ConfsDb     string
		ConfsTixDb  string
		DiscountsDb string

		NewsletterDb string
		MissivesDb   string
	}

	Notion struct {
		Config *NotionConfig
		Client notion.API
	}
)

func (n *Notion) Setup(token string) {
	client := notion.NewClient(notion.Settings{
		Token: token,
	})
	n.Client = client
}
