package getters

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/base58btc/btcpp-web/internal/types"
	"github.com/base58btc/btcpp-web/internal/config"
	"github.com/sorcererxw/go-notion"
	"strings"
	"time"
)

func parseRichText(key string, props map[string]notion.PropertyValue) string {
	val, ok := props[key]
	if !ok {
		/* FIXME: log err? */
		return ""
	}
	if len(val.RichText) == 0 {
		if len(val.Title) != 0 {
			return val.Title[0].Text.Content
		}
		/* FIXME: log err? */
		return ""
	}

	return val.RichText[0].Text.Content
}

func fileGetURL(file *notion.File) string {
	if file.Internal != nil {
		return file.Internal.URL
	}
	if file.External != nil {
		return file.External.URL
	}
	return ""
}

func parseSpeaker(pageID string, props map[string]notion.PropertyValue) *types.Speaker {
	speaker := &types.Speaker{
		Name:         parseRichText("Name", props),
		Desc:      parseRichText("Desc", props),
		Org:      parseRichText("Org", props),
		Photo:      parseRichText("Photo", props),
		Github:      parseRichText("Github", props),
		Twitter:      parseRichText("Twitter", props),
	}

	return speaker
}

func parseTalk(pageID string, props map[string]notion.PropertyValue) *types.Talk {

	var photoURL string
	if len(props["NormPhoto"].Files) > 0 {
		file := props["NormPhoto"].Files[0]
		photoURL = fileGetURL(file)
	} else {
		photoURL = ""
	}

	var twitter string
	parseTwitter := parseRichText("Twitter", props)
	if strings.Contains(parseTwitter, "http") {
		twitter = parseTwitter
	} else {
		twitter = fmt.Sprintf("https://twitter.com/%s", parseTwitter)
	}

	var sched *types.Times
	talktimes := props["Talk Time"].Date
	if talktimes != nil {
		sched = &types.Times{
			Start: talktimes.Start,
			End: talktimes.End,
		}
	}

	talk := &types.Talk{
		ID:           pageID,
		Name:         parseRichText("Talk Name", props),
		Clipart:      parseRichText("Clipart", props),
		Email:        props["Speaker Email"].Email,
		Description:  parseRichText("Description", props),
		Setup:        parseRichText("Setup", props),
		Photo:        photoURL,
		Website:      props["Website"].URL,
		Twitter:      twitter,
		BadgeName:    parseRichText("Badge Name", props),
		Company:      parseRichText("Company", props),
		Sched:        sched,
	}

	if len(talk.Clipart) > 4 {
		talk.AnchorTag = talk.Clipart[:len(talk.Clipart) - 4]
	}

	if props["Venue"].Select != nil {
		talk.Venue = props["Venue"].Select.Name
	}

	if sched != nil {
		talk.TimeDesc = sched.Desc()
		talk.DayTag = sched.Day()
	}
	if props["TalkType"].Select != nil {
		talk.Type = props["TalkType"].Select.Name
	}

	return talk
}

func ListBerlinSpeakers(n *types.Notion) ([]*types.Speaker, error) {
	var speakers []*types.Speaker

	hasMore := true;
	nextCursor := "";
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.BerlinTalksDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
		})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			speaker := parseSpeaker(page.ID, page.Properties)
			speakers = append(speakers, speaker)
		}
	}

	return speakers, nil
}

func CheckIn(n *types.Notion, ticket string) (string, bool, error) {
	/* Make sure that the ticket is in the Purchases table and 
	is *NOT* already checked in */
	pages, _, _, _:= n.Client.QueryDatabase(context.Background(), n.Config.PurchasesDb,
		notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "RefID",
				Text: &notion.TextFilterCondition{
					Equals: ticket,
				},
			},
		})

	if len(pages) != 1 {
		return "", true, fmt.Errorf("Ticket not found")
	}

	page := pages[0]
	if len(page.Properties["Checked In"].RichText) == 0 {
		/* Update to checked in at time.now() */
		now := time.Now()
		_, err := n.Client.UpdatePageProperties(context.Background(), page.ID,
			map[string]*notion.PropertyValue{
				"Checked In": notion.NewRichTextPropertyValue(
					[]*notion.RichText{
						{Type: notion.RichTextText,
							Text: &notion.Text{Content: now.Format(time.RFC3339) }},
					}...),
			})

		/* I need to know what role this is, so I can flash it! */
		var ticket_type string
		if page.Properties["Type"].Select != nil {
			ticket_type = page.Properties["Type"].Select.Name
		}
		return ticket_type, err == nil, err
	}



	return "", true, fmt.Errorf("Already checked in")
}

func parseRegistration(props map[string]notion.PropertyValue) *types.Registration {
	regis := &types.Registration{
		RefID: parseRichText("RefID", props),
		Type:  props["Type"].Select.Name,
		Email: props["Email"].Email,
		ItemBought: parseRichText("Item Bought", props),
	}
	return regis
}

func fetchRegistrations(ctx *config.AppContext) ([]*types.Registration, error) {
	var regis []*types.Registration

	hasMore := true;
	nextCursor := "";
	n := ctx.Notion
	db := ctx.Env.Notion.PurchasesDb
	for hasMore {
		var err error
		var pages []*notion.Page
		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
		})
		if err != nil {
			return nil, err
		}

		ctx.Infos.Println("Got back pages:", len(pages))
		ctx.Infos.Println("Has more pages?", hasMore)

		for _, page := range pages {
			r := parseRegistration(page.Properties)
			regis = append(regis, r)
		}
	}

	return regis, nil
}

func ticketMatch(tickets []string, rez *types.Registration) bool {
	for _, tix := range tickets {
		if strings.Contains(rez.ItemBought, tix) {
			return true
		}
	}

	return false
}

func FetchBtcppRegistrations(tickets []string, ctx *config.AppContext) ([]*types.Registration, error) {
	var btcppres []*types.Registration
	rezzies, err := fetchRegistrations(ctx)

	if err != nil {
		return nil, err
	}

	for _, r := range rezzies {
		if r.RefID == "" {
			continue
		}
		if ticketMatch(tickets, r) {
			btcppres = append(btcppres, r)
		}
	}

	return btcppres, nil
}

func UniqueID(email string, ref string, counter int32) string {
	// sha256 of ref || email || count (4, le)
	h := sha256.New()
	h.Write([]byte(email))
	h.Write([]byte(ref))

	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(counter))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func AddTickets(n *types.Notion, entry *types.Entry, src string) error {
	parent := notion.NewDatabaseParent(n.Config.PurchasesDb)

	for i, item := range entry.Items {
		uniqID := UniqueID(entry.Email, entry.ID, int32(i))
		_, err := n.Client.CreatePage(context.Background(),
			parent,
			map[string]*notion.PropertyValue{
				"RefID": notion.NewTitlePropertyValue(
					[]*notion.RichText{
						{Type: notion.RichTextText,
							Text: &notion.Text{Content: uniqID }},
					}...),
				"Timestamp": notion.NewRichTextPropertyValue(
					[]*notion.RichText{
						{Type: notion.RichTextText,
							Text: &notion.Text{Content: entry.Created.Format(time.RFC3339)},
						}}...),
				"Platform": &notion.PropertyValue{
					Type: notion.PropertySelect,
					Select: &notion.SelectOption{
						Name: src,
					},
				},
				"Type": &notion.PropertyValue{
					Type: notion.PropertySelect,
					Select: &notion.SelectOption{
						Name: "genpop",
					},
				},
				"Amount Paid": &notion.PropertyValue{
					Type:   notion.PropertyNumber,
					Number: float64(item.Total) / 100,
				},
				"Currency": &notion.PropertyValue{
					Type: notion.PropertySelect,
					Select: &notion.SelectOption{
						Name: entry.Currency,
					},
				},
				"Email": &notion.PropertyValue{
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
			})
		if err != nil {
			return err
		}
	}

	return nil
}
