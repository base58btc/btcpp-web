package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func ListConfTicketsNotion(n *types.Notion) ([]*types.ConfTicket, error) {
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
func ListConferencesNotion(n *types.Notion) ([]*types.Conf, error) {
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

	confTix, err := ListConfTicketsNotion(n)
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

func ListConferencesOnlyNotion(n *types.Notion) ([]*types.Conf, error) {
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

	return confs, nil
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
	if err != nil {
		return err
	}
	// Patch the warm caches in place so the next page-render
	// sees the new CalNotif without waiting on a refresh tick.
	// confTalkByProposal and cacheConfTalks share pointers, so a
	// single mutation reaches both readers.
	confTalkCacheMu.Lock()
	for _, ct := range cacheConfTalks {
		if ct != nil && ct.ID == talkID {
			ct.CalNotif = calnotif
			break
		}
	}
	confTalkCacheMu.Unlock()
	return nil
}

func ShiftUpdateCalNotif(n *types.Notion, shiftID string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), shiftID,
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
	if err != nil {
		return err
	}
	// Mirror TalkUpdateCalNotif: patch the warm shift cache so a
	// subsequent ListWorkShifts read in the same process sees
	// the new CalNotif without waiting on a refresh tick. The
	// `shifts` slice is unprotected (matches the existing pattern
	// in invalidateShiftCache + the FetchShiftsCached refresh
	// path); a parallel-write race here is no worse than what
	// already exists upstream.
	for _, s := range shifts {
		if s != nil && s.Ref == shiftID {
			s.CalNotif = calnotif
			break
		}
	}
	return nil
}

// ConfUpdateOrientCalNotif stamps the orientation-invite state
// triple ("UID:Sequence:Hashbytes") on a conf row's
// OrientCalNotif rich_text column. Mirrors TalkUpdateCalNotif /
// ShiftUpdateCalNotif's in-place cache patch so the next render
// reads the fresh value without waiting on a cache refresh tick.
func ConfUpdateOrientCalNotif(n *types.Notion, confRef string, calnotif string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), confRef,
		map[string]*notion.PropertyValue{
			"OrientCalNotif": notion.NewRichTextPropertyValue(
				[]*notion.RichText{
					{
						Type: notion.RichTextText,
						Text: &notion.Text{
							Content: calnotif,
						}},
				}...),
		})
	if err != nil {
		return err
	}
	// Patch the warm conf cache. confs[] holds pointers; the
	// same pointer is what FetchConfsCached returns, so a
	// single mutation reaches every reader.
	for _, c := range confs {
		if c != nil && c.Ref == confRef {
			c.OrientCalNotif = calnotif
			break
		}
	}
	return nil
}

func ListSpeakersNotion(n *types.Notion) ([]*types.Speaker, error) {
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

func ListHotelsNotion(n *types.Notion) ([]*types.Hotel, error) {
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

func ListJobsNotion(n *types.Notion) ([]*types.JobType, error) {
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

func ListDiscountsNotion(n *types.Notion) ([]*types.DiscountCode, error) {
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

func CheckInNotion(n *types.Notion, ticket string) (string, bool, error) {
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

	if len(pages) == 0 {
		return "", true, fmt.Errorf("Ticket not found")
	}

	page := pages[0]

	revoked := page.Properties["Revoked"].Checkbox
	if revoked != nil && *revoked {
		return "", true, fmt.Errorf("Ticket was revoked")
	}

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

		/* Check for existing ticket already */
		pages, err := RefTicketPages(n, uniqID)
		if err != nil {
			return err
		}
		if len(pages) > 0 {
			/* Set each page to unrevoked */
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
			// Amount Paid is built below — go-notion's
			// `Number float64 json:omitempty` would elide a
			// zero-value float from the PATCH body, leaving
			// the property with type=number but no `number`
			// sub-field, which Notion 400s on. For free comp
			// tickets we just leave the column unset (Notion
			// treats it as null).
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

func ListConfInfosNotion(ctx *config.AppContext, confTag string) ([]*types.ConfInfo, error) {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}
	confByTag := make(map[string]*types.Conf, len(confs))
	for _, c := range confs {
		if c != nil && c.Tag != "" {
			confByTag[c.Tag] = c
		}
	}

	n := ctx.Notion
	db := ctx.Env.Notion.ConfInfoDb
	if db == "" {
		return nil, nil
	}

	var out []*types.ConfInfo
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(), db, notion.QueryDatabaseParam{
			StartCursor: nextCursor,
		})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			ci := parseConfInfo(page.ID, page.Properties, confByTag)
			if confTag != "" && ci.ConfTag != confTag {
				continue
			}
			out = append(out, ci)
		}
	}
	return out, nil
}

func UploadFile(n *types.Notion, contentType, filename string, data []byte) (string, error) {
	upload, err := n.Client.CreateFileUpload(context.Background())
	if err != nil {
		return "", err
	}

	upload.Filename = filename
	upload.ContentType = contentType
	result, err := n.Client.UploadFile(context.Background(), upload, data)
	if err != nil {
		return "", err
	}

	if result.Status != notion.FileStatusUploaded {
		return "", fmt.Errorf("Unable to upload file. %v", result)
	}

	return result.ID, nil
}
