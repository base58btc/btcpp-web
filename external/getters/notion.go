package getters

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/base58btc/btcpp-web/internal/config"
	"github.com/base58btc/btcpp-web/internal/types"
	"github.com/sorcererxw/go-notion"
	"sort"
	"strings"
	"time"
)

var cacheSpeakers []*types.Speaker
var lastSpeakerFetch time.Time
var talks []*types.Talk
var lastTalksFetch time.Time
var discounts []*types.DiscountCode
var lastDiscountFetch time.Time


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

func parseDiscount(pageID string, props map[string]notion.PropertyValue) *types.DiscountCode {
	discount := &types.DiscountCode{
		Ref:            pageID,
		CodeName:	parseRichText("CodeName", props),
		PercentOff:     uint(props["PercentOff"].Number),
	}

	for _, confRef := range props["Conference"].Relation {
		discount.ConfRef = append(discount.ConfRef, confRef.ID)
	}

	return discount
}

func parseSpeaker(pageID string, props map[string]notion.PropertyValue) *types.Speaker {
	var twitter string
	parseTwitter := parseRichText("Twitter", props)
	if strings.Contains(parseTwitter, "http") {
		twitter = parseTwitter
	} else if parseTwitter != "" {
		twitter = fmt.Sprintf("https://twitter.com/%s", parseTwitter)
	}

	speaker := &types.Speaker{
		ID:          pageID,
		Name:        parseRichText("Name", props),
		Photo:       parseRichText("NormPhoto", props),
		OrgPhoto:    parseRichText("OrgPhoto", props),
		Website:     props["Website"].URL,
		Github:      props["Github"].URL,
		Twitter:     twitter,
		Nostr:       parseRichText("npub", props),
		Company:     parseRichText("Company", props),
	}

	return speaker
}

func parseTalk(pageID string, props map[string]notion.PropertyValue, speakers []*types.Speaker) *types.Talk {

	var sched *types.Times
	talktimes := props["Talk Time"].Date
	if talktimes != nil {
		sched = &types.Times{
			Start: talktimes.Start,
			End:   talktimes.End,
		}
	}

	talk := &types.Talk{
		ID:          pageID,
		Name:        parseRichText("Talk Name", props),
		Clipart:     parseRichText("Clipart", props),
		Description: parseRichText("Description", props),
		Sched:       sched,
	}

	/* Find all speakers for this talk */
	if speakers != nil {
		for _, speakerRel := range props["speakers"].Relation {
			for _, speaker := range speakers {
				if speakerRel.ID == speaker.ID {
					talk.Speakers = append(talk.Speakers, speaker)
				}
			}
		}
	}

	if len(talk.Clipart) > 4 {
		talk.AnchorTag = talk.Clipart[:len(talk.Clipart)-4]
	}

	if props["Venue"].Select != nil {
		talk.Venue = props["Venue"].Select.Name
	}

	if props["Event"].Select != nil {
		talk.Event = props["Event"].Select.Name
	}

	if sched != nil {
		talk.TimeDesc = sched.Desc()
		talk.DayTag = sched.Day()
	}
	if props["Talk Type"].Select != nil {
		talk.Type = props["Talk Type"].Select.Name
	}

	if props["Section"].Select != nil {
		talk.Section = props["Section"].Select.Name
	}

	return talk
}

func parseConf(pageID string, props map[string]notion.PropertyValue) *types.Conf {
	conf := &types.Conf{
		Ref:           pageID,
		Tag:           parseRichText("Name", props),
		Active:        props["Active"].Checkbox,
		Desc:          parseRichText("Desc", props),
		DateDesc:      parseRichText("DateDesc", props),
		Venue:         parseRichText("Venue", props),
		ShowAgenda:    props["Show Agenda"].Checkbox,
		ShowHackathon:    props["Show Hacks"].Checkbox,
		ShowTalks:     props["Show Talks"].Checkbox,
		HasSatellites: props["Has Satellites"].Checkbox,
	}

	if props["Color"].Select != nil {
		conf.Color = props["Color"].Select.Name
	}

	return conf
}

func parseConfTicket(pageID string, props map[string]notion.PropertyValue) *types.ConfTicket {
	ticket := &types.ConfTicket{
		ID:    pageID,
		Tier:  parseRichText("Tier", props),
		Local: uint(props["Local"].Number),
		BTC:   uint(props["BTC"].Number),
		USD:   uint(props["USD"].Number),
		Max:   uint(props["Max"].Number),
		Currency: parseRichText("Currency", props),
		Symbol: parseRichText("Symbol", props),
	}

	if len(props["Conf"].Relation) > 0 {
		ticket.ConfRef = props["Conf"].Relation[0].ID
	}

	if props["Expires"].Date != nil {
		ticket.Expires = &types.Times{
			Start: props["Expires"].Date.Start,
		}
	}

	return ticket
}

func FetchTokens(n *types.Notion) (types.AuthTokens, error) {
	var tokens types.AuthTokens

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.TokenDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			token := &types.AuthToken {
				Token: parseRichText("Token", page.Properties),
				CreatedAt: page.Properties["CreatedAt"].CreatedTime,
			}
			tokens = append(tokens, token)
		}
	}

	return tokens, nil
}

func MostRecentToken(n *types.Notion) (*types.AuthToken, error) {
	tokens, err := FetchTokens(n)
	if err != nil {
		return nil, err 
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	sort.Sort(tokens)
	return tokens[0], nil
}

func SaveAuthToken(n *types.Notion, token string) error {
	parent := notion.NewDatabaseParent(n.Config.TokenDb)

	vals := map[string]*notion.PropertyValue{
		"Token": notion.NewTitlePropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: token}},
			}...),
	}

	_, err := n.Client.CreatePage(context.Background(), parent, vals)
	return err
}

func ListConfTickets(n *types.Notion) ([]*types.ConfTicket, error) {
	var confTix []*types.ConfTicket

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsTixDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			tix := parseConfTicket(page.ID, page.Properties)
			confTix = append(confTix, tix)
		}
	}

	return confTix, nil
}

/* Grabs the conferences + their tickets buckets */
func ListConferences(n *types.Notion) ([]*types.Conf, error) {
	var confs []*types.Conf

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ConfsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			conf := parseConf(page.ID, page.Properties)
			confs = append(confs, conf)
		}
	}

	confTix, err := ListConfTickets(n)
	if err != nil {
		return nil, err
	}

	/* Add conf tixs to confs */
	for _, tix := range confTix {
		for _, conf := range confs {
			if conf.Ref == tix.ConfRef {
				conf.Tickets = append(conf.Tickets, tix)
				break
			}
		}
	}

	return confs, nil
}

func GetTalks(ctx *config.AppContext) {
	var err error
	talks, err = ListTalks(ctx)
	/* Set last fetch to now even if there's errors */
	lastTalksFetch = time.Now()

	if err != nil {
		ctx.Err.Printf("error fetching talks %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d talks!", len(talks))
	}
}

func ListTalks(ctx *config.AppContext) ([]*types.Talk, error) {
	var talks []*types.Talk
	n := ctx.Notion

	speakers, err := FetchSpeakersCached(ctx)
	if err != nil {
		return nil, err
	}

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.TalksDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			talk := parseTalk(page.ID, page.Properties, speakers)
			talks = append(talks, talk)
		}
	}

	return talks, nil
}


/* This may return nil */
func FetchSpeakersCached(ctx *config.AppContext) ([]*types.Speaker, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if cacheSpeakers == nil || lastSpeakerFetch.Before(deadline) {
		go GetSpeakers(ctx)
	}

	return cacheSpeakers, nil
}

func GetSpeakers(ctx *config.AppContext) {
	var err error
	cacheSpeakers, err = ListSpeakers(ctx.Notion)
	/* Set last fetch to now even if there's errors */
	lastSpeakerFetch = time.Now()

	if err != nil {
		ctx.Err.Printf("error fetching speakers %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d speakers!", len(cacheSpeakers))
	}
}

func ListSpeakers(n *types.Notion) ([]*types.Speaker, error) {
	var speakers []*types.Speaker

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakersDb, notion.QueryDatabaseParam{
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

/* This may return nil */
func FetchTalksCached(ctx *config.AppContext) ([]*types.Talk, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if talks == nil || lastTalksFetch.Before(deadline) {
		go GetTalks(ctx)
	}

	return talks, nil
}

func GetTalksFor(ctx *config.AppContext, event string) ([]*types.Talk, error) {
	talks, err := FetchTalksCached(ctx)
	if err != nil {
		return nil, err
	}
	var filtered []*types.Talk
	for _, talk := range talks {
		if talk.Event == event {
			filtered = append(filtered, talk)
		}
	}
	return filtered, nil
}

func ListDiscounts(n *types.Notion) ([]*types.DiscountCode, error) {
	var discounts []*types.DiscountCode

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.DiscountsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			discount := parseDiscount(page.ID, page.Properties)
			discounts = append(discounts, discount)
		}
	}

	return discounts, nil
}

/* This may return nil */
func FetchDiscountsCached(ctx *config.AppContext) ([]*types.DiscountCode, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if discounts == nil || lastDiscountFetch.Before(deadline) {
		go GetDiscounts(ctx)
	}

	return discounts, nil
}

func GetDiscounts(ctx *config.AppContext) {
	var err error
	discounts, err = ListDiscounts(ctx.Notion)
	/* Set last fetch to now even if there's errors */
	lastDiscountFetch = time.Now()

	if err != nil {
		ctx.Err.Printf("error fetching discounts %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d discounts!", len(discounts))
	}
}

func FindDiscount(ctx *config.AppContext, code string) (*types.DiscountCode, error) {
	discounts, err := FetchDiscountsCached(ctx)
	if err != nil {
		return nil, err
	}

	upcode := strings.ToUpper(code)
	for _, discount := range discounts {
		if strings.ToUpper(discount.CodeName) == upcode {
			return discount, nil
		}
	}
	return nil, nil
}

func CalcDiscount(ctx *config.AppContext, confRef string, code string, tixPrice uint) (uint, *types.DiscountCode, error) {
	discount, err := FindDiscount(ctx, code)

	if err != nil {
		return tixPrice, nil, err
	}

	/* Discount not found! */
	if discount == nil {
		return tixPrice, nil, fmt.Errorf("Discount code \"%s\" not found", code)
	}

	found := false
	for _, discountConfRef := range discount.ConfRef {
		found = found || discountConfRef == confRef
	}

	if !found {
		return tixPrice, nil, fmt.Errorf("%s not a valid code for conference (%s)", code, confRef)
	}

	discountTix := float64(100 - discount.PercentOff) * float64(tixPrice) / float64(100)
	
	tix := uint(discountTix)
	/* Overflows are a thing */
	if tix == 0 || tix > tixPrice {
		tix = 1	
	}
	return tix, discount, nil
}

func CheckIn(n *types.Notion, ticket string) (string, bool, error) {
	/* Make sure that the ticket is in the Purchases table and
	is *NOT* already checked in */
	pages, _, _, _ := n.Client.QueryDatabase(context.Background(), n.Config.PurchasesDb,
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
							Text: &notion.Text{Content: now.Format(time.RFC3339)}},
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
		RefID:      parseRichText("RefID", props),
		Type:       props["Type"].Select.Name,
		Email:      props["Email"].Email,
		ItemBought: parseRichText("Item Bought", props),
	}
	if len(props["conf"].Relation) > 0 {
		regis.ConfRef = props["conf"].Relation[0].ID
	}
	return regis
}

func SoldTixCached(ctx *config.AppContext, conf *types.Conf) (uint) {
	/* update the sold tix cache every time */
	go UpdateSoldTix(ctx, conf)

	return conf.TixSold
}

func UpdateSoldTix(ctx *config.AppContext, conf *types.Conf) {
	soldTixCount, err := SoldTixCount(ctx.Notion, conf.Ref)
	if err != nil {
		ctx.Err.Printf("error fetching sold tix %s %s", conf.Ref, err)
	} else {
		ctx.Infos.Printf("Loaded sold tix count %s %d!", conf.Ref, soldTixCount)
		conf.TixSold = soldTixCount
	}
}

func SoldTixCount(n *types.Notion, confRef string) (uint, error) {
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

func fetchRegistrations(ctx *config.AppContext) ([]*types.Registration, error) {
	var regis []*types.Registration

	hasMore := true
	nextCursor := ""
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

func checkActive(ctx *config.AppContext, confRef string) bool {
	for _, conf := range ctx.Confs {
		if confRef == conf.Ref {
			return conf.Active
		}
	}

	return false
}

func FetchBtcppRegistrations(ctx *config.AppContext, activeOnly bool) ([]*types.Registration, error) {
	var btcppres []*types.Registration
	rezzies, err := fetchRegistrations(ctx)

	if err != nil {
		return nil, err
	}

	for _, r := range rezzies {
		if r.RefID == "" {
			continue
		}

		if activeOnly && !checkActive(ctx, r.ConfRef) {
			continue
		}

		btcppres = append(btcppres, r)
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
			"Amount Paid": {
				Type:   notion.PropertyNumber,
				Number: float64(item.Total) / 100,
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

		if entry.DiscountRef != "" {
			vals["discount"] = notion.NewRelationPropertyValue(
				[]*notion.ObjectReference{{ID: entry.DiscountRef}}...,
			)
		}
		_, err := n.Client.CreatePage(context.Background(), parent, vals)
		if err != nil {
			return err
		}
	}

	return nil
}
