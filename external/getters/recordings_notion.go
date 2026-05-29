package getters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	notion "github.com/niftynei/go-notion"
)

// ListRecordings fetches every row in RecordingsDb. Used by the warm-cache
// bootstrap; callers should normally read from cacheRecordings instead.
func ListRecordingsNotion(ctx *config.AppContext) ([]*types.Recording, error) {
	n := ctx.Notion
	if n.Config.RecordingsDb == "" {
		return nil, nil
	}
	var out []*types.Recording
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.RecordingsDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseRecording(page.ID, page.Properties))
		}
	}
	return out, nil
}

func getRecordingByConfTalkNotion(ctx *config.AppContext, confTalkID string) (*types.Recording, error) {
	n := ctx.Notion
	if n.Config.RecordingsDb == "" {
		return nil, nil
	}
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.RecordingsDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "talk",
				Relation: &notion.RelationFilterCondition{Contains: confTalkID},
			},
		})
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, nil
	}
	return parseRecording(pages[0].ID, pages[0].Properties), nil
}

func updateRecordingYTLinkNotion(ctx *config.AppContext, recordingID, ytLink string) error {
	n := ctx.Notion
	_, err := n.Client.UpdatePageProperties(context.Background(), recordingID,
		map[string]*notion.PropertyValue{
			"YTLink": notion.NewURLPropertyValue(ytLink),
		})
	if err != nil {
		return fmt.Errorf("notion update YTLink: %w", err)
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.YTLink = ytLink
	})
	return nil
}

func updateRecordingXLinkNotion(ctx *config.AppContext, recordingID, xLink string) error {
	n := ctx.Notion
	_, err := n.Client.UpdatePageProperties(context.Background(), recordingID,
		map[string]*notion.PropertyValue{
			"XLink": notion.NewURLPropertyValue(xLink),
		})
	if err != nil {
		return fmt.Errorf("notion update XLink: %w", err)
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.XLink = xLink
	})
	return nil
}

func updateRecordingPublishAtNotion(ctx *config.AppContext, recordingID string, publishAt *time.Time) error {
	dateValue := interface{}(nil)
	if publishAt != nil {
		dateValue = map[string]interface{}{
			"start": publishAt.UTC().Format(time.RFC3339),
		}
	}
	body := map[string]interface{}{
		"properties": map[string]interface{}{
			"PublishAt": map[string]interface{}{
				"date": dateValue,
			},
		},
	}
	if err := notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+recordingID, body); err != nil {
		return fmt.Errorf("notion update PublishAt: %w", err)
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		if publishAt == nil {
			r.PublishAt = nil
		} else {
			when := *publishAt
			r.PublishAt = &when
		}
	})
	return nil
}

func updateRecordingFileURINotion(ctx *config.AppContext, recordingID, fileURI string) error {
	if strings.TrimSpace(fileURI) == "" {
		return fmt.Errorf("FileURI is required")
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), recordingID,
		map[string]*notion.PropertyValue{
			"FileURI": richTextValue(fileURI),
		})
	if err != nil {
		return fmt.Errorf("notion update FileURI: %w", err)
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.FileURI = fileURI
	})
	return nil
}

func updateRecordingPublishingNotion(ctx *config.AppContext, recordingID string, up RecordingPublishingUpdate) error {
	props := make(map[string]*notion.PropertyValue)
	if up.YTLink != nil {
		props["YTLink"] = notion.NewURLPropertyValue(*up.YTLink)
	}
	if up.XLink != nil {
		props["XLink"] = notion.NewURLPropertyValue(*up.XLink)
	}
	if up.XReplyLink != nil {
		props["XReplyLink"] = notion.NewURLPropertyValue(*up.XReplyLink)
	}
	if len(props) == 0 {
		return nil
	}
	if _, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), recordingID, props); err != nil {
		return fmt.Errorf("notion update recording publishing fields: %w", err)
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		if up.YTLink != nil {
			r.YTLink = *up.YTLink
		}
		if up.XLink != nil {
			r.XLink = *up.XLink
		}
		if up.XReplyLink != nil {
			r.XReplyLink = *up.XReplyLink
		}
	})
	return nil
}
