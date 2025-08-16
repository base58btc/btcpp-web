package missives

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"
)

type SubToken struct {
	Time       time.Time
	Email      string
	Newsletter string
}

func ParseSubscribeToken(sec []byte, token string) (*SubToken, error) {
	parts := strings.Split(token, "-")
	if len(parts) != 4 {
		return nil, fmt.Errorf("Invalid token format %s", token)
	}

	emailB, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	subB, err := hex.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	timeB, err := hex.DecodeString(parts[3])
	if err != nil {
		return nil, err
	}
	timestamp := binary.LittleEndian.Uint64(timeB)
	hash, _ := helpers.GetSubscribeToken(sec, string(emailB), string(subB), timestamp)
	if hash != parts[0] {
		return nil, fmt.Errorf("Invalid token %s", token)
	}

	return &SubToken{
		Time:       time.Unix(0, int64(timestamp)),
		Email:      string(emailB),
		Newsletter: string(subB),
	}, nil
}

func scheduleMissives(ctx *config.AppContext, subscribers []*mtypes.Subscriber, letters []*mtypes.Letter) error {
	subonly, sendable, skipped := 0, 0, 0
	for _, letter := range letters {
		_, status, err := scheduleMissive(ctx, subscribers, letter, false)
		if err != nil {
			return err
		}

		switch status {
		case mtypes.SubOnly:
			subonly += 1
		case mtypes.Skipped:
			skipped += 1
		case mtypes.Sendable:
			sendable += 1
		}
	}

	ctx.Infos.Printf("Attempted to send %d; skipped %d 'subonly' %d sent %d", len(letters), skipped, subonly, sendable)

	return nil
}

func scheduleMissive(ctx *config.AppContext, subscribers []*mtypes.Subscriber, letter *mtypes.Letter, isPreview bool) ([]byte, mtypes.SendStatus, error) {

	if !letter.Sendable() {
		return nil, mtypes.Skipped, nil
	}

	if letter.AtSubOnly() {
		return nil, mtypes.SubOnly, nil
	}

	sendAt, err := letter.CalcSendAt()
	if err != nil {
		return nil, mtypes.ErrO, err
	}

	if isPreview {
		sendAt = time.Now()
		letter.Title += "-" + strconv.Itoa(int(sendAt.UTC().Unix()))
	}

	subssent := 0
	var htmlBody []byte
	for _, sub := range subscribers {
		if !sub.IsSubscribed(letter) && !isPreview {
			continue
		}

		var err error
		htmlBody, err = emails.SendNewsletterMissive(ctx, sub, letter, sendAt, isPreview)
		if err != nil {
			/* FIXME: do something less hacky for collisions
			(like returning a specific error code)
			*/
			if !strings.Contains(err.Error(), "scheduled.idem_key") {
				return nil, mtypes.ErrO, err
			}
		}
		subssent += 1
	}

	ctx.Infos.Printf("Sent %d emails (%s)", subssent, letter.Title)

	/* Update SentAt field! */
	if letter.SetSentAt() && !isPreview {
		now := time.Now()
		err = getters.MarkLetterSent(ctx.Notion, letter, now)
		if err != nil {
			return nil, mtypes.ErrO, err
		}
	}

	return htmlBody, mtypes.Sendable, nil
}

func NewSubscriberMissives(ctx *config.AppContext, subscriber *mtypes.Subscriber, newsletter string) error {

	letters, err := getters.GetLetters(ctx.Notion, newsletter)
	if err != nil {
		return err
	}

	for _, letter := range letters {
		if !letter.Sendable() {
			continue
		}

		sendAt, err := letter.CalcSendAt()
		if err != nil {
			return err
		}

		_, err = emails.SendNewsletterMissive(ctx, subscriber, letter, sendAt, false)
		if err != nil {
			/* FIXME: do something less hacky for collisions */
			if !strings.Contains(err.Error(), "scheduled.idem_key") {
				return err
			}
		}
	}

	return nil
}

func NewTicketSub(ctx *config.AppContext, email, conf, tixtype string, gensub bool) error {
	/* Add to subscriber list */
	newsletters := make([]string, 3)
	newsletters[0] = conf
	newsletters[1] = tixtype
	newsletters[2] = conf + "-" + tixtype
	if gensub {
		newsletters = append(newsletters, "newsletter")
	}

	sub, err := getters.SubscribeEmailList(ctx.Notion, email, newsletters)
	if err != nil {
		return err
	}

	/* Schedule + send any mails for them */
	for _, nl := range newsletters {
		err = NewSubscriberMissives(ctx, sub, nl)
		if err != nil {
			return err
		}
	}
	return nil

}
