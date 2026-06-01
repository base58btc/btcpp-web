package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/mtypes"
)

type MissiveInput struct {
	Title       string
	Markdown    string
	SendAt      string
	Newsletters []string
	OnlyFor     string
	Expiry      *time.Time
}

func FindSubscriber(ctx *config.AppContext, email string) (*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return findSubscriberPostgres(ctx, email)
	}
	return findSubscriberNotion(ctx.Notion, email)
}

func ListSubscribersFor(ctx *config.AppContext, newsletters []string) ([]*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return listSubscribersForPostgres(ctx, newsletters)
	}
	return listSubscribersForNotion(ctx.Notion, newsletters)
}

func IsSubscribedTo(ctx *config.AppContext, email, newsletter string) (bool, error) {
	if UsePostgresBackend(ctx) {
		return isSubscribedToPostgres(ctx, email, newsletter)
	}
	return isSubscribedToNotion(ctx.Notion, email, newsletter)
}

func ListSubscribers(ctx *config.AppContext, newsletter string) ([]*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return listSubscribersPostgres(ctx, newsletter)
	}
	return listSubscribersNotion(ctx.Notion, newsletter)
}

func NewSubscriber(ctx *config.AppContext, email, newsletter string) (*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return newSubscriberPostgres(ctx, email, newsletter)
	}
	return newSubscriberNotion(ctx.Notion, email, newsletter)
}

func NewSubscriberList(ctx *config.AppContext, email string, newsletters []string) (*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return newSubscriberListPostgres(ctx, email, newsletters)
	}
	return newSubscriberListNotion(ctx.Notion, email, newsletters)
}

func SubscribeEmailList(ctx *config.AppContext, email string, newsletters []string) (*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return subscribeEmailListPostgres(ctx, email, newsletters)
	}
	return subscribeEmailListNotion(ctx.Notion, email, newsletters)
}

func SubscribeEmail(ctx *config.AppContext, email, newsletter string) (*mtypes.Subscriber, error) {
	if UsePostgresBackend(ctx) {
		return subscribeEmailPostgres(ctx, email, newsletter)
	}
	return subscribeEmailNotion(ctx.Notion, email, newsletter)
}

func UpdateSubs(ctx *config.AppContext, sub *mtypes.Subscriber) error {
	if UsePostgresBackend(ctx) {
		return updateSubsPostgres(ctx, sub)
	}
	return updateSubsNotion(ctx.Notion, sub)
}

func GetLetter(ctx *config.AppContext, uniqueID uint64) (*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return getLetterPostgres(ctx, uniqueID)
	}
	return getLetterNotion(ctx.Notion, uniqueID)
}

func GetLetterFor(ctx *config.AppContext, onlyfor string) (*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return getLetterForPostgres(ctx, onlyfor)
	}
	return getLetterForNotion(ctx.Notion, onlyfor)
}

func GetLetters(ctx *config.AppContext, newsletter string) ([]*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return getLettersPostgres(ctx, newsletter)
	}
	return getLettersNotion(ctx.Notion, newsletter)
}

func ListOnlyForLetters(ctx *config.AppContext) ([]*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return listOnlyForLettersPostgres(ctx)
	}
	return listOnlyForLettersNotion(ctx.Notion)
}

func ListTemplatedLetters(ctx *config.AppContext) ([]*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return listTemplatedLettersPostgres(ctx)
	}
	return listTemplatedLettersNotion(ctx.Notion)
}

func CreateTemplatedMissive(ctx *config.AppContext, in MissiveInput) (*mtypes.Letter, error) {
	if UsePostgresBackend(ctx) {
		return createTemplatedMissivePostgres(ctx, in)
	}
	return createTemplatedMissiveNotion(ctx.Notion, in)
}

func UpdateTemplatedMissive(ctx *config.AppContext, pageID string, in MissiveInput) error {
	if UsePostgresBackend(ctx) {
		return updateTemplatedMissivePostgres(ctx, pageID, in)
	}
	return updateTemplatedMissiveNotion(ctx.Notion, pageID, in)
}

func CreateMissive(ctx *config.AppContext, title, markdown, sendAt string, newsletters []string) error {
	if UsePostgresBackend(ctx) {
		return createMissivePostgres(ctx, title, markdown, sendAt, newsletters)
	}
	return createMissiveNotion(ctx.Notion, title, markdown, sendAt, newsletters)
}

func MarkLetterSent(ctx *config.AppContext, letter *mtypes.Letter, sentAt time.Time) error {
	if UsePostgresBackend(ctx) {
		return markLetterSentPostgres(ctx, letter, sentAt)
	}
	return markLetterSentNotion(ctx.Notion, letter, sentAt)
}
