package getters

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

var cacheSpeakers []*types.Speaker
var lastSpeakerFetch time.Time
var confs []*types.Conf
var lastConfsFetch time.Time
var talks []*types.Talk
var lastTalksFetch time.Time
var discounts []*types.DiscountCode
var lastDiscountFetch time.Time
var hotels []*types.Hotel
var lastHotelFetch time.Time

var jobs []*types.JobType
var lastJobTypeFetch time.Time

type (
	JobType int
)

const (
	JobSpeakers JobType = iota + 1
	JobConfs
	JobTalks
	JobDiscounts
	JobHotels
	JobJobs
)

var taskChan chan JobType = make(chan JobType)

func StartWorkPool(ctx *config.AppContext) {
	// FIXME: I don't think go-notion is threadsafe lmao
	numWorkers := 1

	// Start the worker pool
	for i := 0; i < numWorkers; i++ {
		go workers(ctx, i, taskChan)
	}
}

func CloseWorkPool() {
	close(taskChan)
}

func WaitFetch(ctx *config.AppContext) {
	runJob(ctx, JobConfs)
	lastConfsFetch = time.Now()

	runJob(ctx, JobSpeakers)
	lastSpeakerFetch = time.Now()

	runJob(ctx, JobTalks)
	lastTalksFetch = time.Now()

	runJob(ctx, JobDiscounts)
	lastDiscountFetch = time.Now()

	runJob(ctx, JobHotels)
	lastHotelFetch = time.Now()

	runJob(ctx, JobJobs)
	lastJobTypeFetch = time.Now()

	ctx.Infos.Printf("wait fetch loaded!")
	ctx.Infos.Printf("has confs?? %t", confs != nil)
	ctx.Infos.Printf("has talks?? %t", talks != nil)
	ctx.Infos.Printf("has speakers?? %t", cacheSpeakers != nil)
	ctx.Infos.Printf("has hotels?? %t", hotels != nil)
	ctx.Infos.Printf("has jobs?? %t", jobs != nil)
}

func runJob(ctx *config.AppContext, job JobType) {
	switch job {
	case JobConfs:
		getConfs(ctx)
	case JobSpeakers:
		getSpeakers(ctx)
	case JobTalks:
		getTalks(ctx)
	case JobDiscounts:
		getDiscounts(ctx)
	case JobHotels:
		getHotels(ctx)
        case JobJobs:
		getJobs(ctx)
	}
}

func workers(ctx *config.AppContext, id int, c chan JobType) {
	for job := range c {
		ctx.Infos.Printf("%d starting job type %d", id, job)
		runJob(ctx, job)
		ctx.Infos.Printf("%d finished job type %d", id, job)
	}
}

func getConfs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting confs...")
	confs, err = ListConferences(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching confs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d confs!", len(confs))
	}
}

func FetchConfsCached(ctx *config.AppContext) ([]*types.Conf, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if confs == nil || lastConfsFetch.Before(deadline) {
		lastConfsFetch = time.Now()
		taskChan <- JobConfs
	}

	return confs, nil
}

func getSpeakers(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting speakers...")
	cacheSpeakers, err = ListSpeakers(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching speakers %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d speakers!", len(cacheSpeakers))
	}
}

/* This may return nil */
func FetchSpeakersCached(ctx *config.AppContext) ([]*types.Speaker, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if cacheSpeakers == nil || lastSpeakerFetch.Before(deadline) {
		/* Set last fetch to now even if there's errors */
		lastSpeakerFetch = time.Now()
		taskChan <- JobSpeakers
	}

	return cacheSpeakers, nil
}

func getTalks(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting talks...")
	talks, err = listTalks(ctx, cacheSpeakers)

	if err != nil {
		ctx.Err.Printf("error fetching talks %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d talks!", len(talks))
	}
}

/* This may return nil */
func FetchTalksCached(ctx *config.AppContext) ([]*types.Talk, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if talks == nil || lastTalksFetch.Before(deadline) {
		/* Set last fetch to now even if fails */
		lastTalksFetch = time.Now()
		taskChan <- JobTalks
	}

	return talks, nil
}

func getDiscounts(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting discounts...")
	discounts, err = ListDiscounts(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching discounts %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d discounts!", len(discounts))
	}
}

/* This may return nil */
func FetchDiscountsCached(ctx *config.AppContext) ([]*types.DiscountCode, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if discounts == nil || lastDiscountFetch.Before(deadline) {
		/* Set last fetch to now even if there's errors */
		lastDiscountFetch = time.Now()
		taskChan <- JobDiscounts
	}

	return discounts, nil
}

func getHotels(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting hotels...")
	hotels, err = ListHotels(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching hotels %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d hotels!", len(hotels))
	}
}

/* This may return nil */
func FetchHotelsCached(ctx *config.AppContext) ([]*types.Hotel, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if hotels == nil || lastHotelFetch.Before(deadline) {
		lastHotelFetch = time.Now()
		taskChan <- JobHotels
	}

	return hotels, nil
}

func getJobs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting jobs...")
	jobs, err = ListJobs(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching jobs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d jobs!", len(jobs))
	}
}

/* This may return nil */
func FetchJobsCached(ctx *config.AppContext) ([]*types.JobType, error) {
	now := time.Now()
	deadline := now.Add(time.Duration(-5) * time.Minute)
	if jobs == nil || lastHotelFetch.Before(deadline) {
		lastHotelFetch = time.Now()
		taskChan <- JobJobs
	}

	return jobs, nil
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
			token := &types.AuthToken{
				Token:     parseRichText("Token", page.Properties),
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

func listTalks(ctx *config.AppContext, speakers []*types.Speaker) ([]*types.Talk, error) {
	var talks []*types.Talk
	n := ctx.Notion

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

func TalkUpdateCalNotif(n *types.Notion, talkID string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), talkID,
		map[string]*notion.PropertyValue{
			"CalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	return err
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

func GetTalk(ctx *config.AppContext, talkID string) (*types.Talk, error) {
	talks, err := FetchTalksCached(ctx)
	if err != nil {
		return nil, err
	}
	for _, talk := range talks {
		if talk.ID == talkID {
			return talk, nil
		}
	}
	return nil, fmt.Errorf("Talk %s not found", talkID)
}

func ListHotels(n *types.Notion) ([]*types.Hotel, error) {
	var hotels []*types.Hotel

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.HotelsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			hotel := parseHotel(page.ID, page.Properties)
			hotels = append(hotels, hotel)
		}
	}

	return hotels, nil
}

func ListJobs(n *types.Notion) ([]*types.JobType, error) {
	var jobs []*types.JobType

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.JobTypeDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			job := parseJobType(page.ID, page.Properties)
			jobs = append(jobs, job)
		}
	}

	return jobs, nil
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

	discountTix := float64(100-discount.PercentOff) * float64(tixPrice) / float64(100)

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

func SoldTixCached(ctx *config.AppContext, conf *types.Conf) uint {
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

func fetchRegistrations(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
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

func ticketMatch(tickets []string, rez *types.Registration) bool {
	for _, tix := range tickets {
		if strings.Contains(rez.ItemBought, tix) {
			return true
		}
	}

	return false
}

func checkActive(ctx *config.AppContext, confRef string) bool {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("couldn't fetch confs?? %s", err)
		return false
	}

	for _, conf := range confs {
		if confRef == conf.Ref {
			return conf.Active
		}
	}

	return false
}

func FetchRegistrationsConf(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	return fetchRegistrations(ctx, confRef)
}

func FetchBtcppRegistrations(ctx *config.AppContext, activeOnly bool) ([]*types.Registration, error) {
	var btcppres []*types.Registration
	rezzies, err := fetchRegistrations(ctx, "")

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

func RegisterVolunteer(n *types.Notion, vol *types.Volunteer) (error) {
	parent := notion.NewDatabaseParent(n.Config.VolunteerDb)

        // multiselect
        availability := make([]*notion.SelectOption, len(vol.Availability))
        for i, av := range vol.Availability {
                availability[i] = &notion.SelectOption{
                        Name: av,
                }
        }

        // relation
        workYes := make([]*notion.ObjectReference, len(vol.WorkYes))
        for i, wy := range vol.WorkYes {
                workYes[i] = &notion.ObjectReference{
                        Object: notion.ObjectPage,
                        ID: wy.Ref,
                }
        }
        workNo := make([]*notion.ObjectReference, len(vol.WorkNo))
        for i, wn := range vol.WorkNo {
                workNo[i] = &notion.ObjectReference{
                        Object: notion.ObjectPage,
                        ID: wn.Ref,
                }
        }
        otherEvents := make([]*notion.ObjectReference, len(vol.OtherEvents))
        for i, oe := range vol.OtherEvents {
                otherEvents[i] = &notion.ObjectReference{
                        Object: notion.ObjectPage,
                        ID: oe.Ref,
                }
        }

        vals := map[string]*notion.PropertyValue{
                "Name": notion.NewTitlePropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                Text: &notion.Text{Content: vol.Name}},
                        }...),
                "Email": notion.NewEmailPropertyValue(vol.Email),
                "Phone": notion.NewPhoneNumberPropertyValue(vol.Phone),
                "Availability":  &notion.PropertyValue {
                        Type: notion.PropertyMultiSelect,
                        MultiSelect: &availability,
                },
                "Signal": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: vol.Signal}},
                        }...),
                "ContactAt": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: vol.ContactAt}},
                        }...),
                "DiscoveredVia": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: vol.DiscoveredVia}},
                        }...),
                "ScheduleFor": notion.NewRelationPropertyValue(
                        []*notion.ObjectReference{{ID: vol.ScheduleFor[0].Ref}}...,
                ),
                "FirstEvent": {
                        Type: notion.PropertyCheckbox,
                        Checkbox: &vol.FirstEvent,
                },
                "Hometown": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: vol.Hometown}},
                        }...),
                "Shirt": {
                        Type: notion.PropertySelect,
                        Select: &notion.SelectOption{
                                Name: vol.Shirt,
                        },
                },

        }

        if len(vol.WorkYes) != 0 {
                vals["WorkYes"] = &notion.PropertyValue{
                        Type: notion.PropertyRelation,
                        Relation: workYes,
                }
        }

        if len(vol.WorkNo) != 0 {
                vals["WorkNo"] = &notion.PropertyValue{
                        Type: notion.PropertyRelation,
                        Relation: workNo,
                }
        }

        if len(vol.OtherEvents) != 0 {
                vals["OtherEvents"] = &notion.PropertyValue{
                        Type: notion.PropertyRelation,
                        Relation: otherEvents,
                }
        }

        if vol.Twitter != "" {
                vals["Twitter"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: vol.Twitter}},
                        }...)
        }

        if vol.Nostr != "" {
                vals["npub"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: vol.Nostr}},
                        }...)
        }

        if vol.Comments != "" {
                vals["Comments"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: vol.Comments}},
                        }...)
        }

        _, err := n.Client.CreatePage(context.Background(), parent, vals)

	return err
}

func RegisterTalkApp(n *types.Notion, tapp *types.TalkApp) (error) {
	parent := notion.NewDatabaseParent(n.Config.TalkAppDb)

        // multiselect
        availability := make([]*notion.SelectOption, len(tapp.Availability))
        for i, av := range tapp.Availability {
                availability[i] = &notion.SelectOption{
                        Name: av,
                }
        }

        // relation
        otherEvents := make([]*notion.ObjectReference, len(tapp.OtherEvents))
        for i, oe := range tapp.OtherEvents {
                otherEvents[i] = &notion.ObjectReference{
                        Object: notion.ObjectPage,
                        ID: oe.Ref,
                }
        }

        vals := map[string]*notion.PropertyValue{
                "Name": notion.NewTitlePropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                Text: &notion.Text{Content: tapp.Name}},
                        }...),
                "Phone": notion.NewPhoneNumberPropertyValue(tapp.Phone),
                "Email": notion.NewEmailPropertyValue(tapp.Email),
                "Signal": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.Signal}},
                        }...),
                "ContactAt": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.ContactAt}},
                        }...),
                "Hometown": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.Hometown}},
                        }...),

                "Github": notion.NewURLPropertyValue(tapp.Github),
                "Visa": {
                        Type: notion.PropertySelect,
                        Select: &notion.SelectOption{
                                Name: tapp.Visa,
                        },
                },

                "Pic": notion.NewFilesPropertyValue(
                        []*notion.File{
                                {
                                        Name: "speaker",
                                        Type: "file_upload",
                                        Upload: &notion.UploadFile{
                                                ID: tapp.Pic,
                                        },
                                },
                        }...),

                "Org": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.Org}},
                        }...),
                "Sponsor": {
                        Type: notion.PropertyCheckbox,
                        Checkbox: &tapp.Sponsor,
                },
                "TalkTitle": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.TalkTitle}},
                        }...),
                "Description": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.Description}},
                        }...),
                "PresType": {
                        Type: notion.PropertySelect,
                        Select: &notion.SelectOption{
                                Name: tapp.PresType,
                        },
                },
                "TalkSetup": {
                        Type: notion.PropertyCheckbox,
                        Checkbox: &tapp.TalkSetup,
                },
                "DinnerRSVP": {
                        Type: notion.PropertyCheckbox,
                        Checkbox: &tapp.DinnerRSVP,
                },
                "Availability":  &notion.PropertyValue {
                        Type: notion.PropertyMultiSelect,
                        MultiSelect: &availability,
                },

                "DiscoveredVia": notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                        Text: &notion.Text{Content: tapp.DiscoveredVia}},
                        }...),
                "Shirt": {
                        Type: notion.PropertySelect,
                        Select: &notion.SelectOption{
                                Name: tapp.Shirt,
                        },
                },
                "ScheduleFor": notion.NewRelationPropertyValue(
                        []*notion.ObjectReference{{ID: tapp.ScheduleFor[0].Ref}}...,
                ),

                "FirstEvent": {
                        Type: notion.PropertyCheckbox,
                        Checkbox: &tapp.FirstEvent,
                },

        }

        if tapp.Telegram != "" {
                vals["Telegram"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.Telegram}},
                        }...)
        }

        if tapp.Twitter != "" {
                vals["Twitter"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.Twitter}},
                        }...)
        }

        if tapp.Nostr != "" {
                vals["npub"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.Nostr}},
                        }...)
        }

        if tapp.Website!= "" {
                vals["Website"] = notion.NewURLPropertyValue(tapp.Website)
        }

        if tapp.OrgTwitter != "" {
                vals["OrgTwitter"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.OrgTwitter}},
                        }...)
        }

        if tapp.OrgNostr != "" {
                vals["OrgNostr"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.OrgNostr}},
                        }...)
        }

        if tapp.OrgSite!= "" {
                vals["OrgSite"] = notion.NewURLPropertyValue(tapp.OrgSite)
        }

        if tapp.OrgLogo != "" {
                vals["OrgLogo"] = notion.NewFilesPropertyValue(
                        []*notion.File{
                                {
                                        Name: "orglogospeaker",
                                        Type: "file_upload",
                                        Upload: &notion.UploadFile{
                                                ID: tapp.OrgLogo,
                                        },
                                },
                        }...)
        }

        if len(tapp.OtherEvents) != 0 {
                vals["OtherEvents"] = &notion.PropertyValue{
                        Type: notion.PropertyRelation,
                        Relation: otherEvents,
                }
        }

        if tapp.Comments != "" {
                vals["Comments"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.Comments}},
                        }...)
        }

        _, err := n.Client.CreatePage(context.Background(), parent, vals)

	return err
}

func UploadFile(n *types.Notion, data []byte) (string, error) {
        upload, err := n.Client.CreateFileUpload(context.Background())
        if err != nil {
                return "", err
        }

        result, err := n.Client.UploadFile(context.Background(), upload, data)
        if err != nil {
                return "", err
        }

        if result.Status != notion.FileStatusUploaded {
                return "", fmt.Errorf("Unable to upload file. %v", result)
        }

        return result.ID, nil
}
