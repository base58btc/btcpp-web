package getters

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/mtypes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func findSubscriberPostgres(ctx *config.AppContext, email string) (*mtypes.Subscriber, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil
	}

	var subscriberID string
	var storedEmail string
	err := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text, email
		FROM subscribers
		WHERE email = $1
	`, email).Scan(&subscriberID, &storedEmail)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query subscriber %q: %w", email, err)
	}

	subs, err := subscriberSubscriptionsPostgres(ctx, subscriberID)
	if err != nil {
		return nil, err
	}
	return &mtypes.Subscriber{
		Email: storedEmail,
		Subs:  subs,
		Pages: []string{subscriberID},
	}, nil
}

func listSubscribersForPostgres(ctx *config.AppContext, newsletters []string) ([]*mtypes.Subscriber, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	include, exclude := splitNewsletterFilters(newsletters)
	if len(include) == 0 {
		return nil, fmt.Errorf("Must have at least 1 !!newsletter %v", newsletters)
	}

	rows, err := ctx.DB.Query(context.Background(), `
		SELECT s.id::text, s.email
		FROM subscribers s
		WHERE EXISTS (
			SELECT 1
			FROM subscriber_subscriptions ss
			WHERE ss.subscriber_id = s.id
				AND ss.name = ANY($1::text[])
		)
		AND NOT EXISTS (
			SELECT 1
			FROM subscriber_subscriptions ss
			WHERE ss.subscriber_id = s.id
				AND ss.name = ANY($2::text[])
		)
		ORDER BY s.email
	`, include, exclude)
	if err != nil {
		return nil, fmt.Errorf("query subscribers: %w", err)
	}
	defer rows.Close()

	return scanSubscribersPostgres(ctx, rows)
}

func isSubscribedToPostgres(ctx *config.AppContext, email, newsletter string) (bool, error) {
	if ctx == nil || ctx.DB == nil {
		return false, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(newsletter) == "" {
		return false, nil
	}

	var exists bool
	err := ctx.DB.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM subscribers s
			JOIN subscriber_subscriptions ss ON ss.subscriber_id = s.id
			WHERE s.email = $1
				AND ss.name = $2
		)
	`, strings.TrimSpace(email), strings.TrimSpace(newsletter)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query subscription %q/%q: %w", email, newsletter, err)
	}
	return exists, nil
}

func listSubscribersPostgres(ctx *config.AppContext, newsletter string) ([]*mtypes.Subscriber, error) {
	return listSubscribersForPostgres(ctx, []string{newsletter})
}

func newSubscriberPostgres(ctx *config.AppContext, email, newsletter string) (*mtypes.Subscriber, error) {
	return newSubscriberListPostgres(ctx, email, []string{newsletter})
}

func newSubscriberListPostgres(ctx *config.AppContext, email string, newsletters []string) (*mtypes.Subscriber, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("subscriber email is empty")
	}

	var subscriberID string
	if err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO subscribers (email)
		VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id::text
	`, email).Scan(&subscriberID); err != nil {
		return nil, fmt.Errorf("insert subscriber %q: %w", email, err)
	}

	sub := &mtypes.Subscriber{Email: email, Pages: []string{subscriberID}}
	sub.AddSublist(newsletters)
	if err := updateSubsPostgres(ctx, sub); err != nil {
		return nil, err
	}
	return findSubscriberPostgres(ctx, email)
}

func subscribeEmailListPostgres(ctx *config.AppContext, email string, newsletters []string) (*mtypes.Subscriber, error) {
	subscriber, err := findSubscriberPostgres(ctx, email)
	if err != nil {
		return nil, err
	}
	if subscriber == nil {
		return newSubscriberListPostgres(ctx, email, newsletters)
	}
	for _, nl := range newsletters {
		subscriber.AddSubscription(nl)
	}
	if err := updateSubsPostgres(ctx, subscriber); err != nil {
		return nil, err
	}
	return findSubscriberPostgres(ctx, email)
}

func subscribeEmailPostgres(ctx *config.AppContext, email, newsletter string) (*mtypes.Subscriber, error) {
	return subscribeEmailListPostgres(ctx, email, []string{newsletter})
}

func updateSubsPostgres(ctx *config.AppContext, sub *mtypes.Subscriber) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if sub == nil {
		return fmt.Errorf("subscriber is nil")
	}

	subscriberID, err := subscriberIDPostgres(ctx, sub)
	if err != nil {
		return err
	}
	tx, err := ctx.DB.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("begin subscriber update: %w", err)
	}
	defer tx.Rollback(context.Background())

	if _, err := tx.Exec(context.Background(), `DELETE FROM subscriber_subscriptions WHERE subscriber_id = $1`, subscriberID); err != nil {
		return fmt.Errorf("clear subscriber subscriptions %q: %w", sub.Email, err)
	}
	for _, subscription := range sub.Subs {
		if subscription == nil || strings.TrimSpace(subscription.Name) == "" {
			continue
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO subscriber_subscriptions (subscriber_id, name)
			VALUES ($1, $2)
			ON CONFLICT (subscriber_id, name) DO NOTHING
		`, subscriberID, strings.TrimSpace(subscription.Name)); err != nil {
			return fmt.Errorf("insert subscriber subscription %q/%q: %w", sub.Email, subscription.Name, err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("commit subscriber update: %w", err)
	}
	return nil
}

func getLetterPostgres(ctx *config.AppContext, uniqueID uint64) (*mtypes.Letter, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	row := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text, public_uid, title, newsletters, only_for, markdown,
			send_at_expr, sent_at, expiry
		FROM missives
		WHERE public_uid = $1
	`, uniqueID)
	letter, err := scanLetterPostgres(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("Couldn't find missive with UID#%d", uniqueID)
		}
		return nil, fmt.Errorf("query missive %d: %w", uniqueID, err)
	}
	return letter, nil
}

func getLetterForPostgres(ctx *config.AppContext, onlyfor string) (*mtypes.Letter, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	row := ctx.DB.QueryRow(context.Background(), `
		SELECT id::text, public_uid, title, newsletters, only_for, markdown,
			send_at_expr, sent_at, expiry
		FROM missives
		WHERE only_for = $1
		ORDER BY public_uid DESC NULLS LAST, created_at DESC
		LIMIT 1
	`, onlyfor)
	letter, err := scanLetterPostgres(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("Couldn't find missive OnlyFor %s", onlyfor)
		}
		return nil, fmt.Errorf("query missive only_for %q: %w", onlyfor, err)
	}
	return letter, nil
}

func getLettersPostgres(ctx *config.AppContext, newsletter string) ([]*mtypes.Letter, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	query := `
		SELECT id::text, public_uid, title, newsletters, only_for, markdown,
			send_at_expr, sent_at, expiry
		FROM missives
		WHERE $1 = 'all' OR $1 = ANY(newsletters)
		ORDER BY public_uid NULLS LAST, created_at
	`
	rows, err := ctx.DB.Query(context.Background(), query, newsletter)
	if err != nil {
		return nil, fmt.Errorf("query missives: %w", err)
	}
	defer rows.Close()

	var letters []*mtypes.Letter
	for rows.Next() {
		letter, err := scanLetterPostgres(rows)
		if err != nil {
			return nil, err
		}
		if newsletter != "all" && !letter.HasNewsletter(newsletter) {
			continue
		}
		letters = append(letters, letter)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missives: %w", err)
	}
	return letters, nil
}

func listOnlyForLettersPostgres(ctx *config.AppContext) ([]*mtypes.Letter, error) {
	return listLettersByOnlyForPostgres(ctx, `only_for <> ''`)
}

func listTemplatedLettersPostgres(ctx *config.AppContext) ([]*mtypes.Letter, error) {
	return listLettersByOnlyForPostgres(ctx, `only_for = '`+mtypes.OnlyForTemplated+`'`)
}

func createTemplatedMissivePostgres(ctx *config.AppContext, in MissiveInput) (*mtypes.Letter, error) {
	in.OnlyFor = mtypes.OnlyForTemplated
	return insertMissivePostgres(ctx, in)
}

func updateTemplatedMissivePostgres(ctx *config.AppContext, pageID string, in MissiveInput) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	in.OnlyFor = mtypes.OnlyForTemplated
	_, err := ctx.DB.Exec(context.Background(), `
		UPDATE missives
		SET title = $2,
			markdown = $3,
			send_at_expr = $4,
			newsletters = $5,
			only_for = $6,
			expiry = $7
		WHERE id = $1
	`, pageID, in.Title, in.Markdown, in.SendAt, in.Newsletters, in.OnlyFor, in.Expiry)
	if err != nil {
		return fmt.Errorf("update templated missive %q: %w", pageID, err)
	}
	return nil
}

func createMissivePostgres(ctx *config.AppContext, title, markdown, sendAt string, newsletters []string) error {
	_, err := insertMissivePostgres(ctx, MissiveInput{
		Title:       title,
		Markdown:    markdown,
		SendAt:      sendAt,
		Newsletters: newsletters,
	})
	return err
}

func markLetterSentPostgres(ctx *config.AppContext, letter *mtypes.Letter, sentAt time.Time) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if letter == nil {
		return fmt.Errorf("letter is nil")
	}
	_, err := ctx.DB.Exec(context.Background(), `
		UPDATE missives
		SET sent_at = $2
		WHERE id = $1
	`, letter.PageID, sentAt)
	if err != nil {
		return fmt.Errorf("mark missive sent %q: %w", letter.PageID, err)
	}
	return nil
}

type letterScanner interface {
	Scan(dest ...any) error
}

func scanLetterPostgres(row letterScanner) (*mtypes.Letter, error) {
	var letter mtypes.Letter
	var publicUID *int64
	var sentAt pgtype.Timestamptz
	var expiry pgtype.Timestamptz
	err := row.Scan(
		&letter.PageID,
		&publicUID,
		&letter.Title,
		&letter.Newsletters,
		&letter.OnlyFor,
		&letter.Markdown,
		&letter.SendAt,
		&sentAt,
		&expiry,
	)
	if err != nil {
		return nil, err
	}
	if publicUID != nil {
		letter.UID = uint64(*publicUID)
	}
	if sentAt.Valid {
		letter.SentAt = &sentAt.Time
	}
	if expiry.Valid {
		letter.Expiry = &expiry.Time
	}
	return &letter, nil
}

func subscriberSubscriptionsPostgres(ctx *config.AppContext, subscriberID string) ([]*mtypes.Subscription, error) {
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT name
		FROM subscriber_subscriptions
		WHERE subscriber_id = $1
		ORDER BY name
	`, subscriberID)
	if err != nil {
		return nil, fmt.Errorf("query subscriber subscriptions %q: %w", subscriberID, err)
	}
	defer rows.Close()

	var subs []*mtypes.Subscription
	for rows.Next() {
		var sub mtypes.Subscription
		if err := rows.Scan(&sub.Name); err != nil {
			return nil, fmt.Errorf("scan subscriber subscription: %w", err)
		}
		subs = append(subs, &sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriber subscriptions: %w", err)
	}
	return subs, nil
}

func subscriberIDPostgres(ctx *config.AppContext, sub *mtypes.Subscriber) (string, error) {
	if len(sub.Pages) > 0 && strings.TrimSpace(sub.Pages[0]) != "" {
		return strings.TrimSpace(sub.Pages[0]), nil
	}
	if strings.TrimSpace(sub.Email) == "" {
		return "", fmt.Errorf("subscriber email is empty")
	}
	var subscriberID string
	if err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO subscribers (email)
		VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id::text
	`, strings.TrimSpace(sub.Email)).Scan(&subscriberID); err != nil {
		return "", fmt.Errorf("upsert subscriber %q: %w", sub.Email, err)
	}
	sub.Pages = []string{subscriberID}
	return subscriberID, nil
}

func splitNewsletterFilters(newsletters []string) ([]string, []string) {
	include := make([]string, 0, len(newsletters))
	exclude := make([]string, 0, len(newsletters))
	for _, newsletter := range newsletters {
		newsletter = strings.TrimSpace(newsletter)
		if newsletter == "" {
			continue
		}
		if strings.HasPrefix(newsletter, "!") {
			exclude = append(exclude, strings.TrimPrefix(newsletter, "!"))
			continue
		}
		include = append(include, newsletter)
	}
	return include, exclude
}

func scanSubscribersPostgres(ctx *config.AppContext, rows pgx.Rows) ([]*mtypes.Subscriber, error) {
	var subscribers []*mtypes.Subscriber
	for rows.Next() {
		var subscriberID string
		var email string
		if err := rows.Scan(&subscriberID, &email); err != nil {
			return nil, fmt.Errorf("scan subscriber: %w", err)
		}
		subs, err := subscriberSubscriptionsPostgres(ctx, subscriberID)
		if err != nil {
			return nil, err
		}
		subscribers = append(subscribers, &mtypes.Subscriber{
			Email: email,
			Subs:  subs,
			Pages: []string{subscriberID},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscribers: %w", err)
	}
	return subscribers, nil
}

func listLettersByOnlyForPostgres(ctx *config.AppContext, condition string) ([]*mtypes.Letter, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT id::text, public_uid, title, newsletters, only_for, markdown,
			send_at_expr, sent_at, expiry
		FROM missives
		WHERE `+condition+`
		ORDER BY public_uid NULLS LAST, created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("query only_for missives: %w", err)
	}
	defer rows.Close()

	var letters []*mtypes.Letter
	for rows.Next() {
		letter, err := scanLetterPostgres(rows)
		if err != nil {
			return nil, err
		}
		letters = append(letters, letter)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate only_for missives: %w", err)
	}
	return letters, nil
}

func insertMissivePostgres(ctx *config.AppContext, in MissiveInput) (*mtypes.Letter, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	row := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO missives (public_uid, title, markdown, send_at_expr, newsletters, only_for, expiry)
		VALUES ((SELECT COALESCE(max(public_uid), 0) + 1 FROM missives), $1, $2, $3, $4, $5, $6)
		RETURNING id::text, public_uid, title, newsletters, only_for, markdown,
			send_at_expr, sent_at, expiry
	`, in.Title, in.Markdown, in.SendAt, in.Newsletters, in.OnlyFor, in.Expiry)
	letter, err := scanLetterPostgres(row)
	if err != nil {
		return nil, fmt.Errorf("insert missive %q: %w", in.Title, err)
	}
	return letter, nil
}
