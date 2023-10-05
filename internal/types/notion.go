package types

import (
	"github.com/sorcererxw/go-notion"
)

type (
	NotionConfig struct {
		Token       string
		EmailDb     string
		PurchasesDb string
		TalksDb     string
	}

	Notion struct {
		Config *NotionConfig
		Client notion.API
	}
)

func (n *Notion) Setup(token string) {
	client := notion.NewClient(notion.Settings{Token: token})
	n.Client = client
}
