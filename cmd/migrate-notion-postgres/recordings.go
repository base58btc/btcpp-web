package main

import (
	"context"
	"fmt"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type recordingImportRow struct {
	ref         string
	confTalkRef string
	talkName    string
	youtubeURL  string
	xURL        string
	xReplyURL   string
	fileURI     string
	publishAt   interface{}
}

func listRecordingImportRows(n *types.Notion) ([]*recordingImportRow, error) {
	var out []*recordingImportRow
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
			out = append(out, parseRecordingImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseRecordingImportRow(ref string, props map[string]notion.PropertyValue) *recordingImportRow {
	return &recordingImportRow{
		ref:         ref,
		confTalkRef: relationID(props["talk"]),
		talkName:    richText(props["TalkName"]),
		youtubeURL:  props["YTLink"].URL,
		xURL:        props["XLink"].URL,
		xReplyURL:   props["XReplyLink"].URL,
		fileURI:     richText(props["FileURI"]),
		publishAt:   nullableTimePtr(dateStart(props["PublishAt"])),
	}
}

func validateRecordingRows(rows []*recordingImportRow, confTalkRefs map[string]bool) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.confTalkRef == "" {
			return fmt.Errorf("recording %q has empty talk relation", row.ref)
		}
		if !confTalkRefs[row.confTalkRef] {
			return fmt.Errorf("recording %q has unresolved conf talk ref", row.ref)
		}
	}
	return nil
}

func confTalkRefsByRef(rows []*confTalkImportRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row == nil || row.ref == "" {
			continue
		}
		out[row.ref] = true
	}
	return out
}

func importRecordingRows(ctx context.Context, pool *pgxpool.Pool, rows []*recordingImportRow, confTalkIDsByRef map[string]string) (map[string]string, error) {
	idsByRef := make(map[string]string, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		confTalkID := confTalkIDsByRef[row.confTalkRef]
		if confTalkID == "" {
			return nil, fmt.Errorf("recording %q has unresolved imported conf talk", row.ref)
		}
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO recordings (
				conf_talk_id, talk_name, youtube_url, x_url, x_reply_url, file_uri, publish_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7
			)
			ON CONFLICT (conf_talk_id) DO UPDATE SET
				talk_name = EXCLUDED.talk_name,
				youtube_url = EXCLUDED.youtube_url,
				x_url = EXCLUDED.x_url,
				x_reply_url = EXCLUDED.x_reply_url,
				file_uri = EXCLUDED.file_uri,
				publish_at = EXCLUDED.publish_at
			RETURNING id::text
		`, confTalkID, row.talkName, row.youtubeURL, row.xURL, row.xReplyURL, row.fileURI, row.publishAt).Scan(&id); err != nil {
			return nil, fmt.Errorf("insert recording %q: %w", row.ref, err)
		}
		if row.ref != "" {
			idsByRef[row.ref] = id
		}
	}
	return idsByRef, nil
}

func validateRecordings(ctx context.Context, pool *pgxpool.Pool, rows []*recordingImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM recordings`).Scan(&count); err != nil {
		return fmt.Errorf("count recordings: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres recording count %d is less than Notion count %d", count, len(rows))
	}
	return nil
}
