package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type socialPostImportRow struct {
	ref              string
	socialRef        string
	text             string
	postedTo         string
	kind             string
	status           string
	recordingRef     string
	confTalkRef      string
	url              string
	replyURL         string
	errorText        string
	errorFingerprint string
	scheduledAt      interface{}
	postedAt         interface{}
	notifiedAt       interface{}
}

func listSocialPostImportRows(n *types.Notion) ([]*socialPostImportRow, error) {
	var out []*socialPostImportRow
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
			out = append(out, parseSocialPostImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseSocialPostImportRow(ref string, props map[string]notion.PropertyValue) *socialPostImportRow {
	return &socialPostImportRow{
		ref:              ref,
		socialRef:        textValue(props["Ref"]),
		text:             richText(props["Text"]),
		postedTo:         selectOrText(props["PostedTo"]),
		kind:             selectOrText(props["Kind"]),
		status:           selectOrText(props["Status"]),
		recordingRef:     relationID(props["Recording"]),
		confTalkRef:      relationID(props["ConfTalk"]),
		url:              props["URL"].URL,
		replyURL:         props["ReplyURL"].URL,
		errorText:        richText(props["Error"]),
		errorFingerprint: richText(props["ErrorFingerprint"]),
		scheduledAt:      nullableTimePtr(dateStart(props["ScheduledAt"])),
		postedAt:         nullableTimePtr(dateStart(props["PostedAt"])),
		notifiedAt:       nullableTimePtr(dateStart(props["NotifiedAt"])),
	}
}

func validateSocialPostRows(rows []*socialPostImportRow, recordingRefs map[string]bool, confTalkRefs map[string]bool) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.socialRef) == "" {
			return fmt.Errorf("social post %q has empty Ref", row.ref)
		}
		if row.recordingRef != "" && !recordingRefs[row.recordingRef] {
			return fmt.Errorf("social post %q has unresolved Recording ref", row.ref)
		}
		if row.confTalkRef != "" && !confTalkRefs[row.confTalkRef] {
			return fmt.Errorf("social post %q has unresolved ConfTalk ref", row.ref)
		}
	}
	return nil
}

func importSocialPostRows(ctx context.Context, pool *pgxpool.Pool, rows []*socialPostImportRow, recordingIDsByRef, confTalkIDsByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		recordingID := ""
		if row.recordingRef != "" {
			recordingID = recordingIDsByRef[row.recordingRef]
			if recordingID == "" {
				return fmt.Errorf("social post %q has unresolved imported recording", row.ref)
			}
		}
		confTalkID := ""
		if row.confTalkRef != "" {
			confTalkID = confTalkIDsByRef[row.confTalkRef]
			if confTalkID == "" {
				return fmt.Errorf("social post %q has unresolved imported conf talk", row.ref)
			}
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO social_posts (
				ref, text, posted_to, kind, status, recording_id, conf_talk_id,
				url, reply_url, error, error_fingerprint, scheduled_at, posted_at, notified_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7,
				$8, $9, $10, $11, $12, $13, $14
			)
		`, strings.TrimSpace(row.socialRef), row.text, row.postedTo, row.kind, row.status,
			nullableString(recordingID), nullableString(confTalkID), row.url, row.replyURL,
			row.errorText, row.errorFingerprint, row.scheduledAt, row.postedAt, row.notifiedAt); err != nil {
			return fmt.Errorf("insert social post %q: %w", row.ref, err)
		}
	}
	return nil
}

func recordingRefsByRef(rows []*recordingImportRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row == nil || row.ref == "" {
			continue
		}
		out[row.ref] = true
	}
	return out
}

func validateSocialPosts(ctx context.Context, pool *pgxpool.Pool, rows []*socialPostImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM social_posts`).Scan(&count); err != nil {
		return fmt.Errorf("count social posts: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres social post count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}
