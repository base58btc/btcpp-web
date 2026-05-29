package getters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func listRecordingsPostgres(ctx *config.AppContext) ([]*types.Recording, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, conf_talk_id::text, talk_name, youtube_url, x_url,
			x_reply_url, file_uri, publish_at
		FROM recordings
		ORDER BY publish_at NULLS LAST, talk_name, id
	`)
	if err != nil {
		return nil, fmt.Errorf("query recordings: %w", err)
	}
	defer rows.Close()

	var out []*types.Recording
	for rows.Next() {
		rec, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recordings: %w", err)
	}
	return out, nil
}

func getRecordingByConfTalkPostgres(ctx *config.AppContext, confTalkID string) (*types.Recording, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	row := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text, conf_talk_id::text, talk_name, youtube_url, x_url,
			x_reply_url, file_uri, publish_at
		FROM recordings
		WHERE conf_talk_id = $1::uuid
	`, confTalkID)
	rec, err := scanRecording(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

type recordingScanner interface {
	Scan(dest ...any) error
}

func scanRecording(row recordingScanner) (*types.Recording, error) {
	var rec types.Recording
	var publishAt pgtype.Timestamptz
	err := row.Scan(
		&rec.ID,
		&rec.ConfTalkID,
		&rec.TalkName,
		&rec.YTLink,
		&rec.XLink,
		&rec.XReplyLink,
		&rec.FileURI,
		&publishAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan recording: %w", err)
	}
	if publishAt.Valid {
		rec.PublishAt = &publishAt.Time
	}
	return &rec, nil
}

func updateRecordingYTLinkPostgres(ctx *config.AppContext, recordingID, ytLink string) error {
	if err := updateRecordingColumnPostgres(ctx, recordingID, "youtube_url", ytLink); err != nil {
		return err
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.YTLink = ytLink
	})
	return nil
}

func updateRecordingXLinkPostgres(ctx *config.AppContext, recordingID, xLink string) error {
	if err := updateRecordingColumnPostgres(ctx, recordingID, "x_url", xLink); err != nil {
		return err
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.XLink = xLink
	})
	return nil
}

func updateRecordingFileURIPostgres(ctx *config.AppContext, recordingID, fileURI string) error {
	if strings.TrimSpace(fileURI) == "" {
		return fmt.Errorf("FileURI is required")
	}
	if err := updateRecordingColumnPostgres(ctx, recordingID, "file_uri", fileURI); err != nil {
		return err
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		r.FileURI = fileURI
	})
	return nil
}

func updateRecordingPublishAtPostgres(ctx *config.AppContext, recordingID string, publishAt *time.Time) error {
	if err := updateRecordingColumnPostgres(ctx, recordingID, "publish_at", publishAt); err != nil {
		return err
	}
	patchRecordingCache(recordingID, func(r *types.Recording) {
		if publishAt == nil {
			r.PublishAt = nil
			return
		}
		when := *publishAt
		r.PublishAt = &when
	})
	return nil
}

func updateRecordingPublishingPostgres(ctx *config.AppContext, recordingID string, up RecordingPublishingUpdate) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	setParts := []string{}
	args := []any{recordingID}
	if up.YTLink != nil {
		args = append(args, *up.YTLink)
		setParts = append(setParts, fmt.Sprintf("youtube_url = $%d", len(args)))
	}
	if up.XLink != nil {
		args = append(args, *up.XLink)
		setParts = append(setParts, fmt.Sprintf("x_url = $%d", len(args)))
	}
	if up.XReplyLink != nil {
		args = append(args, *up.XReplyLink)
		setParts = append(setParts, fmt.Sprintf("x_reply_url = $%d", len(args)))
	}
	if len(setParts) == 0 {
		return nil
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE recordings
		SET `+strings.Join(setParts, ", ")+`
		WHERE id = $1::uuid
	`, args...)
	if err != nil {
		return fmt.Errorf("update recording publishing fields: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recording %s not found", recordingID)
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

func updateRecordingColumnPostgres(ctx *config.AppContext, recordingID string, column string, value any) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if !validRecordingColumn(column) {
		return fmt.Errorf("invalid recording column %q", column)
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE recordings
		SET `+pgx.Identifier{column}.Sanitize()+` = $2
		WHERE id = $1::uuid
	`, recordingID, value)
	if err != nil {
		return fmt.Errorf("update recording %s: %w", column, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recording %s not found", recordingID)
	}
	return nil
}

func validRecordingColumn(column string) bool {
	switch column {
	case "youtube_url", "x_url", "file_uri", "publish_at":
		return true
	default:
		return false
	}
}
