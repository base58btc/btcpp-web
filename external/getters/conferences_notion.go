package getters

import (
	"context"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func ListConfTicketsNotion(n *types.Notion) ([]*types.ConfTicket, error) {
	var confTix []*types.ConfTicket

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsTixDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			tix := parseConfTicket(page.ID, page.Properties)
			confTix = append(confTix, tix)
		}
	}

	return confTix, nil
}

/* Grabs the conferences + their tickets buckets */
func ListConferencesNotion(n *types.Notion) ([]*types.Conf, error) {
	var confs []*types.Conf

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			conf := parseConf(page.ID, page.Properties)
			confs = append(confs, conf)
		}
	}

	confTix, err := ListConfTicketsNotion(n)
	if err != nil {
		return nil, err
	}

	for _, tix := range confTix {
		for _, conf := range confs {
			if conf.Ref == tix.ConfRef {
				conf.Tickets = append(conf.Tickets, tix)
				break
			}
		}
	}

	return confs, nil
}

func ListConferencesOnlyNotion(n *types.Notion) ([]*types.Conf, error) {
	var confs []*types.Conf

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			conf := parseConf(page.ID, page.Properties)
			confs = append(confs, conf)
		}
	}

	return confs, nil
}

// ConfUpdateOrientCalNotif stamps the orientation-invite state triple
// ("UID:Sequence:Hashbytes") on a conf row's OrientCalNotif rich_text column.
func ConfUpdateOrientCalNotif(n *types.Notion, confRef string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confRef,
		map[string]*notion.PropertyValue{
			"OrientCalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	if err != nil {
		return err
	}
	for _, c := range confs {
		if c != nil && c.Ref == confRef {
			c.OrientCalNotif = calnotif
			break
		}
	}
	return nil
}

func ListConfInfosNotion(ctx *config.AppContext, confTag string) ([]*types.ConfInfo, error) {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}
	confByTag := make(map[string]*types.Conf, len(confs))
	for _, c := range confs {
		if c != nil && c.Tag != "" {
			confByTag[c.Tag] = c
		}
	}

	n := ctx.Notion
	db := ctx.Env.Notion.ConfInfoDb
	if db == "" {
		return nil, nil
	}

	var out []*types.ConfInfo
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
		})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			ci := parseConfInfo(page.ID, page.Properties, confByTag)
			if confTag != "" && ci.ConfTag != confTag {
				continue
			}
			out = append(out, ci)
		}
	}
	return out, nil
}
