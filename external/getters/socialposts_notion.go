package getters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func listPostedRefsNotion(ctx *config.AppContext, conf *types.Conf) (map[string]bool, error) {
	posted := make(map[string]bool)
	n := ctx.Notion

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
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SocialPostsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
				Filter:      filter,
			})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			post := parseSocialPost(page)
			if post.Ref != "" && socialPostSuppressesRef(post) {
				posted[post.Ref] = true
			}
		}
	}

	return posted, nil
}

func recordSocialPostNotion(ctx *config.AppContext, ref, text, platform string, postedAt time.Time) error {
	n := ctx.Notion
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

	page, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(n.Config.SocialPostsDb), props)
	if err == nil {
		cacheSocialPost(&types.SocialPost{
			ID:       page.ID,
			Ref:      ref,
			Text:     text,
			PostedTo: platform,
			PostedAt: &postedAt,
		})
	}
	return err
}

func listSocialPostsNotion(ctx *config.AppContext) ([]*types.SocialPost, error) {
	n := ctx.Notion
	if n.Config.SocialPostsDb == "" {
		return nil, nil
	}
	var out []*types.SocialPost
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SocialPostsDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseSocialPost(page))
		}
	}
	return out, nil
}

func upsertSocialPostNotion(ctx *config.AppContext, up SocialPostUpdate) (*types.SocialPost, error) {
	if ctx.Notion.Config.SocialPostsDb == "" {
		return nil, fmt.Errorf("SocialPostsDb not configured")
	}
	existing, err := findSocialPostByRefNotion(ctx, up.Ref)
	if err != nil {
		return nil, err
	}
	props := socialPostUpdateProps(up, existing == nil)
	if len(props) == 0 {
		return existing, nil
	}
	if existing != nil {
		if _, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), existing.ID, props); err != nil {
			return nil, fmt.Errorf("notion update social post %s: %w", up.Ref, err)
		}
		updated := applySocialPostUpdate(existing, up)
		cacheSocialPost(updated)
		return updated, nil
	}
	page, err := ctx.Notion.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(ctx.Notion.Config.SocialPostsDb), props)
	if err != nil {
		return nil, fmt.Errorf("notion create social post %s: %w", up.Ref, err)
	}
	created := applySocialPostUpdate(&types.SocialPost{ID: page.ID}, up)
	cacheSocialPost(created)
	return created, nil
}

func findSocialPostByRefNotion(ctx *config.AppContext, ref string) (*types.SocialPost, error) {
	if cached := findCachedSocialPostByRef(ref); cached != nil {
		return cached, nil
	}
	pages, _, _, err := ctx.Notion.Client.QueryDatabase(context.Background(),
		ctx.Notion.Config.SocialPostsDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "Ref",
				Text:     &notion.TextFilterCondition{Equals: ref},
			},
		})
	if err != nil {
		return nil, fmt.Errorf("notion find social post %s: %w", ref, err)
	}
	if len(pages) == 0 {
		return nil, nil
	}
	post := parseSocialPost(pages[0])
	cacheSocialPost(post)
	return post, nil
}

func socialPostUpdateProps(up SocialPostUpdate, includeRef bool) map[string]*notion.PropertyValue {
	props := map[string]*notion.PropertyValue{}
	if includeRef {
		props["Ref"] = titleValue(up.Ref)
	}
	if up.Text != nil && *up.Text != "" {
		props["Text"] = richTextValue(*up.Text)
	}
	if up.PostedTo != "" {
		props["PostedTo"] = selectValue(up.PostedTo)
	}
	if up.Kind != "" {
		props["Kind"] = selectValue(up.Kind)
	}
	if up.RecordingID != "" {
		props["Recording"] = relationValue([]string{up.RecordingID})
	}
	if up.ConfTalkID != "" {
		props["ConfTalk"] = relationValue([]string{up.ConfTalkID})
	}
	if up.Status != nil && *up.Status != "" {
		props["Status"] = selectValue(*up.Status)
	}
	if up.URL != nil && *up.URL != "" {
		props["URL"] = notion.NewURLPropertyValue(*up.URL)
	}
	if up.ReplyURL != nil && *up.ReplyURL != "" {
		props["ReplyURL"] = notion.NewURLPropertyValue(*up.ReplyURL)
	}
	if up.Error != nil {
		props["Error"] = clearableRichTextValue(*up.Error)
	}
	if up.ErrorFingerprint != nil {
		props["ErrorFingerprint"] = clearableRichTextValue(*up.ErrorFingerprint)
	}
	if up.ScheduledAt != nil {
		props["ScheduledAt"] = notion.NewDatePropertyValue(&notion.Date{Start: *up.ScheduledAt})
	}
	if up.PostedAt != nil {
		props["PostedAt"] = notion.NewDatePropertyValue(&notion.Date{Start: *up.PostedAt})
	}
	if up.NotifiedAt != nil {
		props["NotifiedAt"] = notion.NewDatePropertyValue(&notion.Date{Start: *up.NotifiedAt})
	}
	return props
}

func parseSocialPost(page *notion.Page) *types.SocialPost {
	props := page.Properties
	return &types.SocialPost{
		ID:               page.ID,
		Ref:              parseRichText("Ref", props),
		Text:             parseRichText("Text", props),
		PostedTo:         parseSelectOrText("PostedTo", props),
		Kind:             parseSelectOrText("Kind", props),
		Status:           parseSelectOrText("Status", props),
		RecordingID:      parseRef(props, "Recording"),
		ConfTalkID:       parseRef(props, "ConfTalk"),
		URL:              props["URL"].URL,
		ReplyURL:         props["ReplyURL"].URL,
		Error:            parseRichText("Error", props),
		ErrorFingerprint: parseRichText("ErrorFingerprint", props),
		ScheduledAt:      parseDate("ScheduledAt", props),
		PostedAt:         parseDate("PostedAt", props),
		NotifiedAt:       parseDate("NotifiedAt", props),
	}
}

func clearableRichTextValue(content string) *notion.PropertyValue {
	content = strings.TrimSpace(content)
	if content == "" {
		content = " "
	}
	return richTextValue(content)
}
