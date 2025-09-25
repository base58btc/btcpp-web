package getters

import (
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func fileGetURL(files []*notion.File) string {
	if files == nil {
		return ""
	}

	file := files[0]
	if file.Internal != nil {
		return file.Internal.URL
	}
	if file.External != nil {
		return file.External.URL
	}
	return ""
}

func parseCheckbox(checkbox *bool) bool {
	if checkbox == nil {
		return false
	}
	return *checkbox
}

func parseUniqueID(field string, props map[string]notion.PropertyValue) uint64 {
	uniqID := props[field].UniqueID
	if uniqID == nil {
		return uint64(0)
	}
	return uint64(uniqID.Number)
}

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

func parseDiscount(pageID string, props map[string]notion.PropertyValue) *types.DiscountCode {
	discount := &types.DiscountCode{
		Ref:        pageID,
		CodeName:   parseRichText("CodeName", props),
		PercentOff: uint(props["PercentOff"].Number),
	}

	for _, confRef := range props["Conference"].Relation {
		discount.ConfRef = append(discount.ConfRef, confRef.ID)
	}

	return discount
}

func parseConfRef(props map[string]notion.PropertyValue) string {
	if len(props["conf"].Relation) > 0 {
		return props["conf"].Relation[0].ID
	}
	return ""
}

func parseHotel(pageID string, props map[string]notion.PropertyValue) *types.Hotel {
	hotel := &types.Hotel{
		ID:       pageID,
		Name:     parseRichText("Name", props),
		URL:      props["URL"].URL,
		PhotoURL: fileGetURL(props["PhotoURL"].Files),
		Type:     parseRichText("Type", props),
		Desc:     parseRichText("Desc", props),
	}

	hotel.ConfRef = parseConfRef(props)
	return hotel
}

func parseSpeaker(pageID string, props map[string]notion.PropertyValue) *types.Speaker {
	var twitter string
	parseTwitter := parseRichText("Twitter", props)
	if strings.Contains(parseTwitter, "http") {
		twitter = parseTwitter
	} else if parseTwitter != "" {
		twitter = fmt.Sprintf("https://x.com/%s", parseTwitter)
	}

	speaker := &types.Speaker{
		ID:       pageID,
		Name:     parseRichText("Name", props),
		Photo:    parseRichText("NormPhoto", props),
		OrgPhoto: parseRichText("OrgPhoto", props),
		Website:  props["Website"].URL,
		Github:   props["Github"].URL,
		Email:    props["Email"].Email,
		Twitter:  twitter,
		Nostr:    parseRichText("npub", props),
		Company:  parseRichText("Company", props),
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
		CalNotif:    parseRichText("CalNotif", props),
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
		UID:           parseUniqueID("ID", props),
		Active:        parseCheckbox(props["Active"].Checkbox),
		Desc:          parseRichText("Desc", props),
		StartDate:     props["StartDate"].Date.Start,
		OGFlavor:      parseRichText("OG_Flavor", props),
		Emoji:         parseRichText("Emoji", props),
		Tagline:       parseRichText("Tagline", props),
		DateDesc:      parseRichText("DateDesc", props),
		Location:      parseRichText("Location", props),
		Venue:         parseRichText("Venue", props),
		ShowAgenda:    parseCheckbox(props["Show Agenda"].Checkbox),
		ShowHackathon: parseCheckbox(props["Show Hacks"].Checkbox),
		ShowTalks:     parseCheckbox(props["Show Talks"].Checkbox),
		HasSatellites: parseCheckbox(props["Has Satellites"].Checkbox),
	}

	if props["Color"].Select != nil {
		conf.Color = props["Color"].Select.Name
	}

	return conf
}

func parseConfTicket(pageID string, props map[string]notion.PropertyValue) *types.ConfTicket {
	ticket := &types.ConfTicket{
		ID:       pageID,
		Tier:     parseRichText("Tier", props),
		Local:    uint(props["Local"].Number),
		BTC:      uint(props["BTC"].Number),
		USD:      uint(props["USD"].Number),
		Max:      uint(props["Max"].Number),
		Currency: parseRichText("Currency", props),
		Symbol:   parseRichText("Symbol", props),
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
