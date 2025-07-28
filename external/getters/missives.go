package getters

import (
	"context"
	"fmt"
	"strings"
	"btcpp-web/internal/types"
	"btcpp-web/internal/mtypes"
	"github.com/niftynei/go-notion"
	"time"
)

func parseSubs(options *[]*notion.SelectOption) []*mtypes.Subscription {
	var subs []*mtypes.Subscription

	if options == nil {
		return subs
	}

	for _, opt := range *options {
		subs = append(subs, &mtypes.Subscription{
			Name: opt.Name,
			ID:   opt.ID,
		})
	}
	return subs
}

func parseOptsToList(field string, props map[string]notion.PropertyValue) []string {
	var list []string
	opts := props[field].MultiSelect

	if opts == nil {
		return list
	}

	for _, opt := range *opts {
		list = append(list, opt.Name)
	}

	return list
}

func parseLetter(pageID string, props map[string]notion.PropertyValue) *mtypes.Letter {
	letter := &mtypes.Letter{
		PageID:     pageID,
		UID:        parseUniqueID("ID", props),
		Title:      parseRichText("Title", props),
		Newsletters: parseOptsToList("Newsletter", props),
		Markdown:   parseRichText("Markdown", props),
		SendAt:     parseRichText("SendAt", props),
	}

	expiry := props["Expiry"].Date
	if expiry != nil {
		letter.Expiry = &expiry.Start
	}
	sentat := props["SentAt"].Date
	if sentat != nil {
		letter.SentAt = &sentat.Start
	}
	return letter
}

func FindSubscriber(n *types.Notion, email string) (*mtypes.Subscriber, error) {
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.NewsletterDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "Email",
				Text: &notion.TextFilterCondition{
					Equals: email,
				},
			},
		})

	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, nil
	}

	sub := &mtypes.Subscriber{
		Pages: make([]string, len(pages)),
	}

	for i, page := range pages {
		sub.Pages[i] = page.ID
		sub.Email = parseRichText("Email", page.Properties)
		sub.Subs = parseSubs(page.Properties["Subs"].MultiSelect)
	}
	return sub, err
}

func ListSubscribersFor(n *types.Notion, newsletters []string) ([]*mtypes.Subscriber, error) {
	hasMore := true
	nextCursor := ""
	var subs []*mtypes.Subscriber
	var orfilters []*notion.Filter
	var andfilters []*notion.Filter
	var filter *notion.Filter

	for _, nl := range newsletters {
		if strings.HasPrefix(nl, "!") {
			filter := &notion.Filter{
					Property: "Subs",
					MultiSelect: &notion.MultiSelectFilterCondition{
						/* Get rid of ! */
						DoesNotContain: nl[1:],
					},
				}
			andfilters = append(andfilters, filter)
		} else {
			filter = &notion.Filter{
					Property: "Subs",
					MultiSelect: &notion.MultiSelectFilterCondition{
						Contains: nl,
					},
				}
			orfilters = append(orfilters, filter)
		}
	}

	if len(orfilters) == 0 {
		return nil, fmt.Errorf("Must have at least 1 !!newsletter %v", newsletters)
	}

	/* or: [ orfilters... ] */
	filter = &notion.Filter{
		Or: orfilters,
	}
	if len(andfilters) != 0 {
		/* and: [ andfilters..., { or: [ orfilters...] } ] */
		andfilters = append(andfilters, filter)
		filter = &notion.Filter{
			And: andfilters,
		}
	}

	for hasMore {

		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.NewsletterDb, notion.QueryDatabaseParam {
				StartCursor: nextCursor,
				Filter: filter,
			})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			sub := &mtypes.Subscriber{
				Email: parseRichText("Email", page.Properties),
				Subs:  parseSubs(page.Properties["Subs"].MultiSelect),
			}
			subs = append(subs, sub)
		}
	}

	return subs, nil
}

func ListSubscribers(n *types.Notion, newsletter string) ([]*mtypes.Subscriber, error) {
	letters := []string { newsletter }
	return ListSubscribersFor(n, letters)
}

func NewSubscriber(n *types.Notion, email, newsletter string) (*mtypes.Subscriber, error) {
	nls := []string{ newsletter }
	return NewSubscriberList(n, email, nls)
}

func NewSubscriberList(n *types.Notion, email string, newsletters []string) (*mtypes.Subscriber, error) {

	opts := make([]*notion.SelectOption, len(newsletters))
	for i, nl := range newsletters {
		opts[i] = &notion.SelectOption{
			Name: nl,
		}
	}

	parent := notion.NewDatabaseParent(n.Config.NewsletterDb)
	props := map[string]*notion.PropertyValue{
		"Email": notion.NewTitlePropertyValue(
			[]*notion.RichText{
				{
					Type: notion.RichTextText,
					Text: &notion.Text{Content: email},
				},
			}...),
		"Subs": notion.NewMultiSelectPropertyValue(opts...),
	}

	page, err := n.Client.CreatePage(context.Background(), parent, props)
	if err != nil {
		return nil, err
	}
	subscriber := &mtypes.Subscriber{
		Pages: []string{page.ID},
		Email: email,
	}
	subscriber.AddSublist(newsletters)
	return subscriber, nil
}

func SubscribeEmailList(n *types.Notion, email string, newsletters []string) (*mtypes.Subscriber, error) {
	subscriber, err := FindSubscriber(n, email)
	if err != nil {
		return nil, err
	}

	if subscriber == nil {
		return NewSubscriberList(n, email, newsletters)
	}

	for _, nl := range newsletters {
		subscriber.AddSubscription(nl)
	}
	err = UpdateSubs(n, subscriber)

	return subscriber, err
}

func SubscribeEmail(n *types.Notion, email, newsletter string) (*mtypes.Subscriber, error) {
	subscriber, err := FindSubscriber(n, email)
	if err != nil {
		return nil, err
	}

	if subscriber == nil {
		return NewSubscriber(n, email, newsletter)
	}

	subscriber.AddSubscription(newsletter)
	err = UpdateSubs(n, subscriber)

	return subscriber, err
}

func makeSubList(sub *mtypes.Subscriber) []*notion.SelectOption {
	subList := make([]*notion.SelectOption, len(sub.Subs))
	for i, subscription := range sub.Subs {
		subList[i] = &notion.SelectOption{
			Name: subscription.Name,
			ID:   subscription.ID,
		}
	}
	return subList
}

func UpdateSubs(n *types.Notion, sub *mtypes.Subscriber) error {
	subList := makeSubList(sub)

	for _, pageID := range sub.Pages {
		_, err := n.Client.UpdatePageProperties(context.Background(), pageID,
			map[string]*notion.PropertyValue{
				"Subs": notion.NewMultiSelectPropertyValue(subList...),
			})
		if err != nil {
			return err
		}
	}

	return nil
}

func GetLetter(n *types.Notion, uniqueID uint64) (*mtypes.Letter, error) {
	var err error
	var pages []*notion.Page
	pages, _, _, err = n.Client.QueryDatabase(context.Background(),
		n.Config.MissivesDb, notion.QueryDatabaseParam{
			Filter:      &notion.Filter{
				Property: "ID",
				ID: &notion.UniqueIDFilterCondition{
					Equals: float64(uniqueID),
				},
			},
		})
	if err != nil {
		return nil, err
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("Couldn't find missive with UID#%d", uniqueID)
	}

	letter := parseLetter(pages[0].ID, pages[0].Properties)
	return letter, nil
}

func GetLetters(n *types.Notion, newsletter string) ([]*mtypes.Letter, error) {
	hasMore := true
	nextCursor := ""
	var letters []*mtypes.Letter
	var filter *notion.Filter

	/* "all" keyword sends everything */
	if newsletter != "all" {
		filter = &notion.Filter{
			Property: "Newsletter",
			MultiSelect: &notion.MultiSelectFilterCondition{
				Contains: newsletter,
			},
		}
	}

	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.MissivesDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
				Filter:      filter,
			})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			letter := parseLetter(page.ID, page.Properties)
			letters = append(letters, letter)
		}
	}

	return letters, nil
}

func MarkLetterSent(n *types.Notion, letter *mtypes.Letter, sentAt time.Time) error {

	_, err := n.Client.UpdatePageProperties(context.Background(), letter.PageID,
		map[string]*notion.PropertyValue{
			"SentAt": notion.NewDatePropertyValue(&notion.Date{
				Start: sentAt,
			}),
		})
	return err
}

