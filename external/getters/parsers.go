package getters

import (
	"strings"
        "time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func fileGetURL(files []*notion.File) string {
	if len(files) == 0 {
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

func parseSelect(field string, props map[string]notion.PropertyValue) string {
        if props[field].Select == nil {
                return ""
        }

        return props[field].Select.Name
}

func parseDate(field string, props map[string]notion.PropertyValue) *time.Time {
	dd := props[field].Date
	if dd != nil {
                return &dd.Start
	}
        return nil
}

func parseTimes(field string, props map[string]notion.PropertyValue) *types.Times {
	tt := props[field].Date
	if tt != nil {
		return &types.Times{
			Start: tt.Start,
			End:   tt.End,
		}
	}

        return nil
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
			var sb strings.Builder
			for _, t := range val.Title {
				if t.Text != nil {
					sb.WriteString(t.Text.Content)
				}
			}
			return sb.String()
		}
		/* FIXME: log err? */
		return ""
	}

	var sb strings.Builder
	for _, rt := range val.RichText {
		if rt.Text != nil {
			sb.WriteString(rt.Text.Content)
		}
	}
	return sb.String()
}

func parseDiscount(pageID string, props map[string]notion.PropertyValue) *types.DiscountCode {
	discount := &types.DiscountCode{
		Ref:        pageID,
		CodeName:   parseRichText("CodeName", props),
		PercentOff: uint(props["PercentOff"].Number),
                Discount:   parseRichText("Discount", props),
	}

	for _, confRef := range props["Conference"].Relation {
		discount.ConfRef = append(discount.ConfRef, confRef.ID)
	}

	return discount
}

func parseRef(props map[string]notion.PropertyValue, refname string) string {
	if len(props[refname].Relation) > 0 {
		return props[refname].Relation[0].ID
	}
	return ""
}

func parseConfRef(props map[string]notion.PropertyValue) string {
        return parseRef(props, "conf")
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
	speaker := &types.Speaker{
		ID:       pageID,
		Name:     parseRichText("Name", props),
		Photo:    parseRichText("NormPhoto", props),
		OrgPhoto: parseRichText("OrgPhoto", props),
		Website:  props["Website"].URL,
		Github:   props["Github"].URL,
		Email:    props["Email"].Email,
		Twitter:  types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr:    parseRichText("npub", props),
		Company:  parseRichText("Company", props),
	}

	return speaker
}

func parseTalk(pageID string, props map[string]notion.PropertyValue, speakerMap map[string]*types.Speaker) *types.Talk {

	talk := &types.Talk{
		ID:          pageID,
		Name:        parseRichText("Talk Name", props),
		Clipart:     parseRichText("Clipart", props),
		Description: parseRichText("Description", props),
		CalNotif:    parseRichText("CalNotif", props),
		TalkCardURL: props["TalkCardURL"].URL,
		Sched:       parseTimes("Talk Time", props),
                Venue:       parseSelect("Venue", props),
                Event:       parseSelect("Event", props),
                Type:        parseSelect("Talk Type", props),
                Section:     parseSelect("Section", props),
	}

	if talk.Sched != nil {
		talk.TimeDesc = talk.Sched.Desc()
		talk.DayTag = talk.Sched.Day()
	}

	/* Find all speakers for this talk */
	if speakerMap != nil {
		for _, speakerRel := range props["speakers"].Relation {
			if speaker, ok := speakerMap[speakerRel.ID]; ok {
				talk.Speakers = append(talk.Speakers, speaker)
			}
		}
	}

	if len(talk.Clipart) > 4 {
		talk.AnchorTag = talk.Clipart[:len(talk.Clipart)-4]
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

        stdate := parseDate("StartDate", props)
        if stdate != nil {
                conf.StartDate = *stdate        
        }
        edate := parseDate("EndDate", props)
        if edate != nil {
                conf.EndDate = *edate
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
		PostSymbol:   parseRichText("PostSymbol", props),
                Expires:   parseTimes("Expires", props),
	}

	if len(props["Conf"].Relation) > 0 {
		ticket.ConfRef = props["Conf"].Relation[0].ID
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

func parseJobType(pageID string, props map[string]notion.PropertyValue) *types.JobType {
	jobtype := &types.JobType{
		Ref:    pageID,
		Tag:    parseRichText("Tag", props),
                DisplayOrder: int(props["DisplayOrder"].Number),
		Title: parseRichText("Title", props),
		Tooltip: parseRichText("Tooltip", props),
		LongDesc: parseRichText("LongDesc", props),
		Show:    parseCheckbox(props["Show"].Checkbox),
        }

        return jobtype
}

func parseSelectList(field string, props map[string]notion.PropertyValue) []string {
        var list []string 
        options := props[field].MultiSelect

        if options == nil {
                return list
        }

        for _, opt := range *options {
                list = append(list, opt.Name)
        }
        return list
}

func parseConfOne(ctx *config.AppContext, field string, props map[string]notion.PropertyValue) *types.Conf {
        objRefs := props[field].Relation

        confs, _ := FetchConfsCached(ctx)
        for _, ref := range objRefs {
                for _, c := range confs {
                        if c.Ref == ref.ID {
                                return c
                        }
                }
        }

        return nil
}

func parseConfList(ctx *config.AppContext, field string, props map[string]notion.PropertyValue) []*types.Conf {
        var list []*types.Conf
        objRefs := props[field].Relation

        confs, _ := FetchConfsCached(ctx)
        for _, ref := range objRefs {
                for _, c := range confs {
                        if c.Ref == ref.ID {
                                list = append(list, c)
                                break
                        }
                }
        }
        return list
}

func parseOrgOne(ctx *config.AppContext, field string, props map[string]notion.PropertyValue) *types.Org {
        objRefs := props[field].Relation

        orgs, _ := FetchOrgsCached(ctx)
        for _, ref := range objRefs {
                for _, org := range orgs {
                        if org.Ref == ref.ID {
                                return org
                        }
                }
        }

        return nil
}

func parseJobList(ctx *config.AppContext, field string, props map[string]notion.PropertyValue) []*types.JobType {
        var list []*types.JobType
        objRefs := props[field].Relation

        jobs, _ := FetchJobsCached(ctx)
        for _, ref := range objRefs {
                for _, j := range jobs {
                        if j.Ref == ref.ID {
                                list = append(list, j)
                                break
                        }
                }
        }
        return list
}

func parseVolunteer(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue) *types.Volunteer {
	vol := &types.Volunteer{
		Ref:           pageID,
		Name:          parseRichText("Name", props),
		Email:         props["Email"].Email,
		Phone:         props["Phone"].PhoneNumber,
		Signal:        parseRichText("Signal", props),
                Availability:  parseSelectList("Availability", props),
		ContactAt:     parseRichText("ContactAt", props),
		Comments:      parseRichText("Comments", props),
		DiscoveredVia: parseRichText("DiscoveredVia", props),

		ScheduleFor: parseConfList(ctx, "ScheduleFor", props),
		OtherEvents: parseConfList(ctx, "OtherEvents", props),
		WorkYes: parseJobList(ctx, "WorkYes", props),
		WorkNo: parseJobList(ctx, "WorkNo", props),

		FirstEvent:    parseCheckbox(props["FirstEvent"].Checkbox),
		Hometown: parseRichText("Hometown", props),
		Twitter: types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr: parseRichText("npub", props),
                Shirt: parseSelect("Shirt", props),
                Status: parseSelect("Status", props),
                CreatedAt: parseDate("created", props),
	}

	return vol
}

func parseVolInfo(pageID string, props map[string]notion.PropertyValue) *types.VolInfo {
        vinfo := &types.VolInfo{
                Ref:           pageID,
                ConfRef:       parseConfRef(props),
                OrientLink:    props["OrientLink"].URL,
                OrientTimes:   parseTimes("OrientTimes", props),
                Notes:         parseRichText("Notes", props),
        }

        return vinfo
}

func parseTalkApp(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue) *types.TalkApp {
	talk := &types.TalkApp{
		Ref:           pageID,
		Name:          parseRichText("Name", props),
		Phone:         props["Phone"].PhoneNumber,
		Email:         props["Email"].Email,
		Signal:        parseRichText("Signal", props),
		Telegram:      parseRichText("Telegram", props),
		ContactAt:     parseRichText("ContactAt", props),
		Hometown:   parseRichText("Hometown", props),
                Visa:       parseSelect("Visa", props),
		Twitter:    types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr:      parseRichText("npub", props),
		Github:     props["Github"].URL,
		Website:    props["Website"].URL,
                Shirt:      parseSelect("Shirt", props),
		Pic:        fileGetURL(props["Pic"].Files),
		Org:        parseRichText("Org", props),
		Sponsor:    parseCheckbox(props["Sponsor"].Checkbox),
		OrgTwitter:    types.ParseTwitter(parseRichText("OrgTwitter", props)),
		OrgNostr:      parseRichText("OrgNpub", props),
		OrgSite:       props["OrgSite"].URL,
		OrgLogo:        fileGetURL(props["OrgLogo"].Files),

		TalkTitle:        parseRichText("TalkTitle", props),
		Description:        parseRichText("Description", props),
                PresType:     parseSelect("PresType", props),
		TalkSetup:    parseCheckbox(props["TalkSetup"].Checkbox),

		DinnerRSVP:    parseCheckbox(props["DinnerRSVP"].Checkbox),
                Availability:  parseSelectList("Availability", props),
		DiscoveredVia: parseRichText("DiscoveredVia", props),

		ScheduleFor: parseConfList(ctx, "ScheduleFor", props),
		OtherEvents: parseConfList(ctx, "OtherEvents", props),
		Comments:      parseRichText("Comments", props),
		FirstEvent:    parseCheckbox(props["FirstEvent"].Checkbox),
	}

	return talk
}

func parseJobTypes(field string, props map[string]notion.PropertyValue, jobtypes []*types.JobType) *types.JobType {
        for _, jobRel := range props[field].Relation {
                for _, job := range jobtypes {
                        if jobRel.ID == job.Ref {
                                return job
                        }
                }
        }

        return nil
}

func parseWorkShift(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue, jobtypes []*types.JobType) *types.WorkShift {

	shift := &types.WorkShift{
		Ref:         pageID,
		Name:        parseRichText("Name", props),
		MaxVols:     uint(props["MaxVols"].Number),
                Type:        parseJobTypes("TypeRef", props, jobtypes),
                Conf:        parseConfOne(ctx, "ConfRef", props),
		ShiftTime:   parseTimes("ShiftTime", props),
		Priority:    uint(props["Priority"].Number),
		CalNotif:    parseRichText("CalNotif", props),
	}

	/* Find all assignees for this shift */
        shift.AssigneesRef = make([]string, 0)
        for _, assRel := range props["Assignees"].Relation {
                shift.AssigneesRef = append(shift.AssigneesRef, assRel.ID)
        }

        for _, leaderRel := range props["ShiftLeader"].Relation {
                shift.ShiftLeaderRef = leaderRel.ID
        }

	return shift
}
