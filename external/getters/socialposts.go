package getters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

const (
	SocialPostKindRecording = "recording"
)

type SocialPostUpdate struct {
	Ref              string
	Text             *string
	PostedTo         string
	Kind             string
	RecordingID      string
	ConfTalkID       string
	Status           *string
	URL              *string
	ReplyURL         *string
	Error            *string
	ErrorFingerprint *string
	ScheduledAt      *time.Time
	PostedAt         *time.Time
	NotifiedAt       *time.Time
}

var (
	socialPostCacheMu   sync.RWMutex
	cacheSocialPosts    []*types.SocialPost
	socialPostByRef     map[string]*types.SocialPost
	lastSocialPostFetch time.Time
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
				Filter:      filter,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			post := parseSocialPost(page)
			if post.Ref != "" && socialPostSuppressesRef(post) {
				posted[post.Ref] = true
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

func FetchSocialPostsCached(ctx *config.AppContext) ([]*types.SocialPost, error) {
	ttl := cacheTTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	socialPostCacheMu.RLock()
	if cacheSocialPosts != nil && time.Since(lastSocialPostFetch) < ttl {
		out := append([]*types.SocialPost(nil), cacheSocialPosts...)
		socialPostCacheMu.RUnlock()
		return out, nil
	}
	socialPostCacheMu.RUnlock()

	posts, err := ListSocialPosts(ctx)
	if err != nil {
		return nil, err
	}
	replaceSocialPostCache(posts)
	return append([]*types.SocialPost(nil), posts...), nil
}

func FetchSocialPostByRef(ref string) *types.SocialPost {
	socialPostCacheMu.RLock()
	defer socialPostCacheMu.RUnlock()
	post := socialPostByRef[ref]
	if post == nil {
		return nil
	}
	cp := *post
	return &cp
}

func ListSocialPosts(ctx *config.AppContext) ([]*types.SocialPost, error) {
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

func UpsertSocialPost(ctx *config.AppContext, up SocialPostUpdate) (*types.SocialPost, error) {
	if strings.TrimSpace(up.Ref) == "" {
		return nil, fmt.Errorf("social post ref required")
	}
	if ctx.Notion.Config.SocialPostsDb == "" {
		return nil, fmt.Errorf("SocialPostsDb not configured")
	}
	existing, err := findSocialPostByRef(ctx, up.Ref)
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

func findSocialPostByRef(ctx *config.AppContext, ref string) (*types.SocialPost, error) {
	if cached := FetchSocialPostByRef(ref); cached != nil {
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
	if up.Error != nil && *up.Error != "" {
		props["Error"] = richTextValue(*up.Error)
	}
	if up.ErrorFingerprint != nil && *up.ErrorFingerprint != "" {
		props["ErrorFingerprint"] = richTextValue(*up.ErrorFingerprint)
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

func applySocialPostUpdate(post *types.SocialPost, up SocialPostUpdate) *types.SocialPost {
	if post == nil {
		post = &types.SocialPost{}
	}
	cp := *post
	if up.Ref != "" {
		cp.Ref = up.Ref
	}
	if up.Text != nil && *up.Text != "" {
		cp.Text = *up.Text
	}
	if up.PostedTo != "" {
		cp.PostedTo = up.PostedTo
	}
	if up.Kind != "" {
		cp.Kind = up.Kind
	}
	if up.RecordingID != "" {
		cp.RecordingID = up.RecordingID
	}
	if up.ConfTalkID != "" {
		cp.ConfTalkID = up.ConfTalkID
	}
	if up.Status != nil && *up.Status != "" {
		cp.Status = *up.Status
	}
	if up.URL != nil && *up.URL != "" {
		cp.URL = *up.URL
	}
	if up.ReplyURL != nil && *up.ReplyURL != "" {
		cp.ReplyURL = *up.ReplyURL
	}
	if up.Error != nil && *up.Error != "" {
		cp.Error = *up.Error
	}
	if up.ErrorFingerprint != nil && *up.ErrorFingerprint != "" {
		cp.ErrorFingerprint = *up.ErrorFingerprint
	}
	if up.ScheduledAt != nil {
		when := *up.ScheduledAt
		cp.ScheduledAt = &when
	}
	if up.PostedAt != nil {
		when := *up.PostedAt
		cp.PostedAt = &when
	}
	if up.NotifiedAt != nil {
		when := *up.NotifiedAt
		cp.NotifiedAt = &when
	}
	return &cp
}

func socialPostSuppressesRef(post *types.SocialPost) bool {
	status := strings.TrimSpace(strings.ToLower(post.Status))
	if status == "" {
		return true
	}
	switch status {
	case "queued", "posted", "uploaded", "published", "succeeded", "success":
		return true
	default:
		return false
	}
}

func replaceSocialPostCache(posts []*types.SocialPost) {
	byRef := make(map[string]*types.SocialPost, len(posts))
	for _, post := range posts {
		if post != nil && post.Ref != "" {
			byRef[post.Ref] = post
		}
	}
	socialPostCacheMu.Lock()
	cacheSocialPosts = posts
	socialPostByRef = byRef
	lastSocialPostFetch = time.Now()
	socialPostCacheMu.Unlock()
}

func cacheSocialPost(post *types.SocialPost) {
	if post == nil || post.Ref == "" {
		return
	}
	socialPostCacheMu.Lock()
	defer socialPostCacheMu.Unlock()
	if socialPostByRef == nil {
		socialPostByRef = map[string]*types.SocialPost{}
	}
	socialPostByRef[post.Ref] = post
	for i, existing := range cacheSocialPosts {
		if existing != nil && existing.Ref == post.Ref {
			cacheSocialPosts[i] = post
			lastSocialPostFetch = time.Now()
			return
		}
	}
	cacheSocialPosts = append(cacheSocialPosts, post)
	lastSocialPostFetch = time.Now()
}
