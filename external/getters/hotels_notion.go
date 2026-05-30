package getters

import (
	"context"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func ListHotelsNotion(n *types.Notion) ([]*types.Hotel, error) {
	var hotels []*types.Hotel

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.HotelsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			hotel := parseHotel(page.ID, page.Properties)
			hotels = append(hotels, hotel)
		}
	}

	return hotels, nil
}
