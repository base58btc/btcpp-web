package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func CheckInNotion(n *types.Notion, ticket string) (string, bool, error) {
	pages, _, _, _ := n.Client.QueryDatabase(context.Background(), n.Config.PurchasesDb,
		notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "RefID",
				Text: &notion.TextFilterCondition{
					Equals: ticket,
				},
			},
		})

	if len(pages) == 0 {
		return "", true, fmt.Errorf("Ticket not found")
	}

	page := pages[0]

	revoked := page.Properties["Revoked"].Checkbox
	if revoked != nil && *revoked {
		return "", true, fmt.Errorf("Ticket was revoked")
	}

	if len(page.Properties["Checked In"].RichText) == 0 {
		now := time.Now()
		_, err := n.Client.UpdatePageProperties(context.Background(), page.ID,
			map[string]*notion.PropertyValue{
				"Checked In": notion.NewRichTextPropertyValue(
					[]*notion.RichText{
						{Type: notion.RichTextText,
							Text: &notion.Text{Content: now.Format(time.RFC3339)}},
					}...),
			})

		var ticketType string
		if page.Properties["Type"].Select != nil {
			ticketType = page.Properties["Type"].Select.Name
		}
		return ticketType, err == nil, err
	}

	return "", true, fmt.Errorf("Already checked in")
}

func SoldTixCountNotion(n *types.Notion, confRef string) (uint, error) {
	var regisCount uint

	hasMore := true
	nextCursor := ""
	db := n.Config.PurchasesDb
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db,
			notion.QueryDatabaseParam{
				Filter: &notion.Filter{
					Property: "conf",
					Relation: &notion.RelationFilterCondition{
						Contains: confRef,
					},
				},
				StartCursor: nextCursor,
			})
		if err != nil {
			return 0, err
		}

		regisCount += uint(len(pages))
	}

	return regisCount, nil
}

func FetchRegistrationsNotion(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	var regis []*types.Registration
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.PurchasesDb

	var filter *notion.Filter
	if confRef != "" {
		filter = &notion.Filter{
			Property: "conf",
			Relation: &notion.RelationFilterCondition{
				Contains: confRef,
			},
		}
	}
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
			Filter:      filter,
		})
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			r := parseRegistration(page.Properties)
			regis = append(regis, r)
		}
	}

	return regis, nil
}

// ListRegistrationsByEmail returns every PurchasesDb row for this email.
// Used by the dashboard to render "your tickets" and the apply-form
// "returning attendee" check.
func ListRegistrationsByEmailNotion(ctx *config.AppContext, email string) ([]*types.Registration, error) {
	if email == "" {
		return nil, nil
	}
	n := ctx.Notion
	var out []*types.Registration
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.PurchasesDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
				Filter: &notion.Filter{
					Property: "Email",
					Text:     &notion.TextFilterCondition{Equals: email},
				},
			})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseRegistration(page.Properties))
		}
	}
	return out, nil
}

func LookupTicketPages(n *types.Notion, lookupID string) ([]*notion.Page, error) {
	return TicketPages(n, "Lookup ID", lookupID)
}

func RefTicketPages(n *types.Notion, refid string) ([]*notion.Page, error) {
	return TicketPages(n, "RefID", refid)
}

func TicketPages(n *types.Notion, field, uniqID string) ([]*notion.Page, error) {
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.PurchasesDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: field,
				Text: &notion.TextFilterCondition{
					Equals: uniqID,
				},
			},
		})

	return pages, err
}

func ToggleTicketBlock(n *types.Notion, pageID string, block bool) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), pageID,
		map[string]*notion.PropertyValue{
			"Revoked": {
				Type:     notion.PropertyCheckbox,
				Checkbox: &block,
			},
		})
	return err
}

func RevokeTicket(n *types.Notion, lookupID string) error {
	pages, err := LookupTicketPages(n, lookupID)

	for _, page := range pages {
		ToggleTicketBlock(n, page.ID, true)
	}
	return err
}

func AddTickets(n *types.Notion, entry *types.Entry, src string) error {
	parent := notion.NewDatabaseParent(n.Config.PurchasesDb)

	for i, item := range entry.Items {
		uniqID := types.UniqueID(entry.Email, entry.ID, int32(i))

		pages, err := RefTicketPages(n, uniqID)
		if err != nil {
			return err
		}
		if len(pages) > 0 {
			for _, page := range pages {
				ToggleTicketBlock(n, page.ID, false)
			}
			continue
		}

		vals := map[string]*notion.PropertyValue{
			"RefID": notion.NewTitlePropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: uniqID}},
				}...),
			"Timestamp": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: entry.Created.Format(time.RFC3339)},
					}}...),
			"Platform": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: src,
				},
			},
			"conf": notion.NewRelationPropertyValue(
				[]*notion.ObjectReference{{ID: entry.ConfRef}}...,
			),
			"Type": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: item.Type,
				},
			},
			"Currency": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: entry.Currency,
				},
			},
			"Email": {
				Type:  notion.PropertyEmail,
				Email: entry.Email,
			},
			"Item Bought": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: item.Desc}},
				}...),
			"Lookup ID": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{Type: notion.RichTextText,
						Text: &notion.Text{Content: entry.ID}},
				}...),
		}

		if item.Total > 0 {
			vals["Amount Paid"] = &notion.PropertyValue{
				Type:   notion.PropertyNumber,
				Number: float64(item.Total) / 100,
			}
		}

		if entry.DiscountRef != "" {
			vals["discount"] = notion.NewRelationPropertyValue(
				[]*notion.ObjectReference{{ID: entry.DiscountRef}}...,
			)
		}
		_, err = n.Client.CreatePage(context.Background(), parent, vals)
		if err != nil {
			return err
		}
	}

	return nil
}
