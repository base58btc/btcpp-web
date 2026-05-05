package main

import (
	"context"
	"fmt"
	"log"

	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

type C struct {
	Notion struct {
		Token      string `toml:"token"`
		ConfTalkDb string `toml:"conftalkdb"`
	} `toml:"notion"`
}

func main() {
	var c C
	if _, err := toml.DecodeFile("config.toml", &c); err != nil {
		log.Fatal(err)
	}
	n := &types.Notion{}
	n.Setup(c.Notion.Token)

	db, err := n.Client.RetrieveDatabase(context.Background(), c.Notion.ConfTalkDb)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("ConfTalkDb properties:")
	for k, v := range db.Properties {
		fmt.Printf("  %s (type=%s)\n", k, v.Type)
	}
}
