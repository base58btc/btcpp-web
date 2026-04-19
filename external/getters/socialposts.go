package getters

import (
	"context"
	"time"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

// ListPostedRefs returns a set of all Ref values that have been posted
func ListPostedRefs(n *types.Notion, conf *types.Conf) (map[string]bool, error) {
	posted := make(map[string]bool)

	var filter *notion.Filter
	if conf != nil {
		filter = &notion.Filter{
			Property: "Ref",
                        Text: &notion.TextFilterCondition{
                                Contains: conf.Tag,
                        },
		}
	}

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.SocialPostsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
                                Filter: filter,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			ref := parseRichText("Ref", page.Properties)
			if ref != "" {
				posted[ref] = true
			}
		}
	}

	return posted, nil
}

func RecordSocialPost(n *types.Notion, ref, text, platform string, postedAt time.Time) error {
	props := map[string]*notion.PropertyValue{
		"Ref":  notion.NewTitlePropertyValue(richText(ref)...),
		"Text": notion.NewRichTextPropertyValue(richText(text)...),
		"PostedTo": {
			Type:   notion.PropertySelect,
			Select: &notion.SelectOption{Name: platform},
		},
		"PostedAt": notion.NewDatePropertyValue(
			&notion.Date{
				Start: postedAt,
			},
		),
	}

	_, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(n.Config.SocialPostsDb), props)
	return err
}
