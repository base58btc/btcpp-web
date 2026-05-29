package getters

import (
	"context"

	"btcpp-web/internal/config"
	"github.com/niftynei/go-notion"
)

func siteStatsAttendeesNotion(ctx *config.AppContext) (int, error) {
	if ctx == nil || ctx.Notion == nil || ctx.Notion.Config.PurchasesDb == "" {
		return 0, nil
	}
	count := 0
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := ctx.Notion.Client.QueryDatabase(context.Background(),
			ctx.Notion.Config.PurchasesDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return count, err
		}
		nextCursor = next
		hasMore = more
		count += len(pages)
	}
	return count, nil
}
