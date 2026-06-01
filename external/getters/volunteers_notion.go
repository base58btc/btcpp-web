package getters

import (
	"context"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func UpdateVolunteerStatus(ctx *config.AppContext, volRef, status string) error {
	n := ctx.Notion

	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"Status": {
				Type: notion.PropertySelect,
				Select: &notion.SelectOption{
					Name: status,
				},
			},
		})

	return err
}

func UpdateVolunteerAvailability(ctx *config.AppContext, volRef string, days []string) error {
	n := ctx.Notion

	availability := make([]*notion.SelectOption, len(days))
	for i, d := range days {
		availability[i] = &notion.SelectOption{Name: d}
	}

	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"Availability": {
				Type:        notion.PropertyMultiSelect,
				MultiSelect: &availability,
			},
		})

	return err
}

func UpdateVolunteerWorkPrefs(ctx *config.AppContext, volRef string, workYesRefs, workNoRefs []string) error {
	n := ctx.Notion

	if len(workYesRefs) == 0 {
		err := clearRelationProperty(n.Config.Token, volRef, "WorkYes")
		if err != nil {
			return err
		}
	} else {
		yesRel := make([]*notion.ObjectReference, len(workYesRefs))
		for i, r := range workYesRefs {
			yesRel[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: r}
		}
		_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
			map[string]*notion.PropertyValue{
				"WorkYes": {
					Type:     notion.PropertyRelation,
					Relation: yesRel,
				},
			})
		if err != nil {
			return err
		}
	}

	if len(workNoRefs) == 0 {
		return clearRelationProperty(n.Config.Token, volRef, "WorkNo")
	}

	noRel := make([]*notion.ObjectReference, len(workNoRefs))
	for i, r := range workNoRefs {
		noRel[i] = &notion.ObjectReference{Object: notion.ObjectPage, ID: r}
	}
	_, err := n.Client.UpdatePageProperties(context.Background(), volRef,
		map[string]*notion.PropertyValue{
			"WorkNo": {
				Type:     notion.PropertyRelation,
				Relation: noRel,
			},
		})

	return err
}

func registerVolunteerNotion(n *types.Notion, vol *types.Volunteer) error {
	normalizeVolunteerInput(vol)
	parent := notion.NewDatabaseParent(n.Config.VolunteerDb)

	availability := make([]*notion.SelectOption, len(vol.Availability))
	for i, av := range vol.Availability {
		availability[i] = &notion.SelectOption{
			Name: av,
		}
	}

	workYes := make([]*notion.ObjectReference, len(vol.WorkYes))
	for i, wy := range vol.WorkYes {
		workYes[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     wy.Ref,
		}
	}
	workNo := make([]*notion.ObjectReference, len(vol.WorkNo))
	for i, wn := range vol.WorkNo {
		workNo[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     wn.Ref,
		}
	}
	otherEvents := make([]*notion.ObjectReference, len(vol.OtherEvents))
	for i, oe := range vol.OtherEvents {
		otherEvents[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     oe.Ref,
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
		"Availability": &notion.PropertyValue{
			Type:        notion.PropertyMultiSelect,
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
			Type:     notion.PropertyCheckbox,
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
		"Status": {
			Type: notion.PropertySelect,
			Select: &notion.SelectOption{
				Name: "Applied",
			},
		},
	}

	if len(vol.WorkYes) != 0 {
		vals["WorkYes"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: workYes,
		}
	}

	if len(vol.WorkNo) != 0 {
		vals["WorkNo"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: workNo,
		}
	}

	if len(vol.OtherEvents) != 0 {
		vals["OtherEvents"] = &notion.PropertyValue{
			Type:     notion.PropertyRelation,
			Relation: otherEvents,
		}
	}

	if vol.Twitter.Handle != "" {
		vals["Twitter"] = notion.NewRichTextPropertyValue(
			[]*notion.RichText{
				{Type: notion.RichTextText,
					Text: &notion.Text{Content: vol.Twitter.Handle}},
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

func normalizeVolunteerInput(vol *types.Volunteer) {
	if vol == nil {
		return
	}
	vol.Name = strings.TrimSpace(vol.Name)
	vol.Email = strings.TrimSpace(vol.Email)
	vol.Phone = strings.TrimSpace(vol.Phone)
	vol.Signal = strings.TrimSpace(vol.Signal)
	vol.ContactAt = strings.TrimSpace(vol.ContactAt)
	vol.Comments = strings.TrimSpace(vol.Comments)
	vol.DiscoveredVia = strings.TrimSpace(vol.DiscoveredVia)
	vol.Hometown = strings.TrimSpace(vol.Hometown)
	vol.Twitter = types.ParseTwitter(vol.Twitter.Handle)
	vol.Nostr = strings.TrimSpace(vol.Nostr)
	vol.Shirt = strings.TrimSpace(vol.Shirt)
}

func GetVolInfosNotion(ctx *config.AppContext, confRef string) ([]*types.VolInfo, error) {
	var vis []*types.VolInfo
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolInfoDb

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
			vi := parseVolInfo(page.ID, page.Properties)
			vis = append(vis, vi)
		}
	}

	return vis, nil
}

func ListVolunteerAppsNotion(ctx *config.AppContext, email string) ([]*types.Volunteer, error) {
	var vols []*types.Volunteer
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolunteerDb

	var filter *notion.Filter
	if email != "" {
		filter = &notion.Filter{
			Property: "Email",
			Text: &notion.TextFilterCondition{
				Equals: email,
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
			v := parseVolunteer(ctx, page.ID, page.Properties)
			vols = append(vols, v)
		}
	}

	return vols, nil
}

// FetchVolunteerNotion retrieves a single volunteer page directly by ID. This
// is a strongly-consistent read, unlike QueryDatabase's eventually-consistent
// index.
func FetchVolunteerNotion(ctx *config.AppContext, volRef string) (*types.Volunteer, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), volRef)
	if err != nil {
		return nil, err
	}
	return parseVolunteer(ctx, page.ID, page.Properties), nil
}

func ListVolunteersForConfNotion(ctx *config.AppContext, confRef string) ([]*types.Volunteer, error) {
	var vols []*types.Volunteer
	hasMore := true
	nextCursor := ""
	n := ctx.Notion
	db := ctx.Env.Notion.VolunteerDb

	filter := &notion.Filter{
		Property: "ScheduleFor",
		Relation: &notion.RelationFilterCondition{
			Contains: confRef,
		},
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
			v := parseVolunteer(ctx, page.ID, page.Properties)
			vols = append(vols, v)
		}
	}

	return vols, nil
}
