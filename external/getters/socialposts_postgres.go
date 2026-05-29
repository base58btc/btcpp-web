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

func listPostedRefsPostgres(ctx *config.AppContext, conf *types.Conf) (map[string]bool, error) {
	posts, err := listSocialPostsPostgres(ctx)
	if err != nil {
		return nil, err
	}
	posted := make(map[string]bool)
	for _, post := range posts {
		if post == nil || post.Ref == "" || !socialPostSuppressesRef(post) {
			continue
		}
		if conf != nil && !strings.Contains(post.Ref, conf.Tag) {
			continue
		}
		posted[post.Ref] = true
	}
	return posted, nil
}

func recordSocialPostPostgres(ctx *config.AppContext, ref, text, platform string, postedAt time.Time) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	var id string
	err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO social_posts (ref, text, posted_to, posted_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, ref, text, platform, postedAt).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert social post: %w", err)
	}
	cacheSocialPost(&types.SocialPost{
		ID:       id,
		Ref:      ref,
		Text:     text,
		PostedTo: platform,
		PostedAt: &postedAt,
	})
	return nil
}

func listSocialPostsPostgres(ctx *config.AppContext) ([]*types.SocialPost, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, ref, text, posted_to, kind, status,
			coalesce(recording_id::text, ''), coalesce(conf_talk_id::text, ''),
			url, reply_url, error, error_fingerprint,
			scheduled_at, posted_at, notified_at
		FROM social_posts
		ORDER BY created_at DESC, id
	`)
	if err != nil {
		return nil, fmt.Errorf("query social posts: %w", err)
	}
	defer rows.Close()

	var out []*types.SocialPost
	for rows.Next() {
		post, err := scanSocialPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate social posts: %w", err)
	}
	return out, nil
}

func upsertSocialPostPostgres(ctx *config.AppContext, up SocialPostUpdate) (*types.SocialPost, error) {
	existing, err := findSocialPostByRefPostgres(ctx, up.Ref)
	if err != nil {
		return nil, err
	}
	updated := applySocialPostUpdate(existing, up)
	if updated.Ref == "" {
		updated.Ref = up.Ref
	}
	if existing == nil {
		return insertSocialPostPostgres(ctx, updated)
	}
	return updateSocialPostPostgres(ctx, updated)
}

func findSocialPostByRefPostgres(ctx *config.AppContext, ref string) (*types.SocialPost, error) {
	if cached := findCachedSocialPostByRef(ref); cached != nil {
		return cached, nil
	}
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	row := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text, ref, text, posted_to, kind, status,
			coalesce(recording_id::text, ''), coalesce(conf_talk_id::text, ''),
			url, reply_url, error, error_fingerprint,
			scheduled_at, posted_at, notified_at
		FROM social_posts
		WHERE ref = $1
		ORDER BY created_at DESC, id
		LIMIT 1
	`, ref)
	post, err := scanSocialPost(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cacheSocialPost(post)
	return post, nil
}

func insertSocialPostPostgres(ctx *config.AppContext, post *types.SocialPost) (*types.SocialPost, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO social_posts (
			ref, text, posted_to, kind, status, recording_id, conf_talk_id,
			url, reply_url, error, error_fingerprint, scheduled_at, posted_at, notified_at
		)
		VALUES (
			$1, $2, $3, $4, $5, nullif($6, '')::uuid, nullif($7, '')::uuid,
			$8, $9, $10, $11, $12, $13, $14
		)
		RETURNING id::text
	`, post.Ref, post.Text, post.PostedTo, post.Kind, post.Status, post.RecordingID, post.ConfTalkID,
		post.URL, post.ReplyURL, post.Error, post.ErrorFingerprint, post.ScheduledAt, post.PostedAt, post.NotifiedAt).Scan(&post.ID)
	if err != nil {
		return nil, fmt.Errorf("insert social post %s: %w", post.Ref, err)
	}
	cacheSocialPost(post)
	return post, nil
}

func updateSocialPostPostgres(ctx *config.AppContext, post *types.SocialPost) (*types.SocialPost, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	tag, err := ctx.DB.Exec(context.Background(), `
		UPDATE social_posts
		SET ref = $2,
			text = $3,
			posted_to = $4,
			kind = $5,
			status = $6,
			recording_id = nullif($7, '')::uuid,
			conf_talk_id = nullif($8, '')::uuid,
			url = $9,
			reply_url = $10,
			error = $11,
			error_fingerprint = $12,
			scheduled_at = $13,
			posted_at = $14,
			notified_at = $15
		WHERE id = $1::uuid
	`, post.ID, post.Ref, post.Text, post.PostedTo, post.Kind, post.Status, post.RecordingID, post.ConfTalkID,
		post.URL, post.ReplyURL, post.Error, post.ErrorFingerprint, post.ScheduledAt, post.PostedAt, post.NotifiedAt)
	if err != nil {
		return nil, fmt.Errorf("update social post %s: %w", post.Ref, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("social post %s not found", post.ID)
	}
	cacheSocialPost(post)
	return post, nil
}

type socialPostScanner interface {
	Scan(dest ...any) error
}

func scanSocialPost(row socialPostScanner) (*types.SocialPost, error) {
	var post types.SocialPost
	var scheduledAt pgtype.Timestamptz
	var postedAt pgtype.Timestamptz
	var notifiedAt pgtype.Timestamptz
	err := row.Scan(
		&post.ID,
		&post.Ref,
		&post.Text,
		&post.PostedTo,
		&post.Kind,
		&post.Status,
		&post.RecordingID,
		&post.ConfTalkID,
		&post.URL,
		&post.ReplyURL,
		&post.Error,
		&post.ErrorFingerprint,
		&scheduledAt,
		&postedAt,
		&notifiedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan social post: %w", err)
	}
	if scheduledAt.Valid {
		post.ScheduledAt = &scheduledAt.Time
	}
	if postedAt.Valid {
		post.PostedAt = &postedAt.Time
	}
	if notifiedAt.Valid {
		post.NotifiedAt = &notifiedAt.Time
	}
	return &post, nil
}
