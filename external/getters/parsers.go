package getters

import (
	"strconv"
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

func parseSelectOrText(field string, props map[string]notion.PropertyValue) string {
	if val := parseSelect(field, props); val != "" {
		return val
	}
	return parseRichText(field, props)
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
		Ref:            pageID,
		CodeName:       parseRichText("CodeName", props),
		Discount:       parseRichText("Discount", props),
		UsesCount:      uint(props["UsesCount"].Number),
		AffiliateEmail: parseAffiliateEmail(props),
	}

	for _, confRef := range props["Conference"].Relation {
		discount.ConfRef = append(discount.ConfRef, confRef.ID)
	}

	discount.ParseDiscountExpr()

	return discount
}

// parseAffiliateEmail reads the AffiliateEmail property as either a
// Notion email field or a rich_text field, whichever the admin chose
// to set up. Returning the empty string for missing / unset values
// means non-affiliate codes (the legacy ones) parse unchanged.
func parseAffiliateEmail(props map[string]notion.PropertyValue) string {
	if v, ok := props["AffiliateEmail"]; ok && v.Email != "" {
		return v.Email
	}
	return parseRichText("AffiliateEmail", props)
}

// parseAffiliateUsage projects one AffiliateUsageDb row into the
// internal struct. CodeName comes from rich_text, Email may be an
// email or rich_text property, ConfTag from a Notion select, the sats
// amounts + count from numbers, Created from Notion's built-in
// created_time.
func parseAffiliateUsage(pageID string, props map[string]notion.PropertyValue, createdAt *time.Time) *types.AffiliateUsage {
	return &types.AffiliateUsage{
		ID:             pageID,
		CodeName:       parseRichText("DiscountCode", props),
		AffiliateEmail: parseAffiliateEmail(props),
		ConfTag:        parseSelect("Conference", props),
		SavedSats:      int64(props["SavedSats"].Number),
		EarnedSats:     int64(props["EarnedSats"].Number),
		TicketsCount:   uint(props["TicketsCount"].Number),
		Created:        createdAt,
	}
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
		ID:    pageID,
		Name:  parseRichText("Name", props),
		URL:   props["URL"].URL,
		Img:   parseRichText("Img", props),
		Type:  parseRichText("Type", props),
		Desc:  parseRichText("Desc", props),
		Order: int(props["Order"].Number),
	}
	hotel.ConfRef = parseConfRef(props)
	return hotel
}

func parseSpeaker(pageID string, props map[string]notion.PropertyValue) *types.Speaker {
	speaker := &types.Speaker{
		ID:            pageID,
		Name:          parseRichText("Name", props),
		Photo:         parseRichText("NormPhoto", props),
		Email:         strings.TrimSpace(props["Email"].Email),
		Phone:         parseRichText("Phone", props),
		Signal:        parseRichText("Signal", props),
		Telegram:      parseRichText("Telegram", props),
		Twitter:       types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr:         parseRichText("npub", props),
		Github:        props["Github"].URL,
		Instagram:     parseRichText("Instagram", props),
		LinkedIn:      parseRichText("LinkedIn", props),
		Website:       props["Website"].URL,
		Company:       parseRichText("Company", props),
		OrgLogo:       parseRichText("OrgPhoto", props),
		AvailToHire:   parseCheckbox(props["AvailToHire"].Checkbox),
		LookingToHire: parseCheckbox(props["LookingToHire"].Checkbox),
		TShirt:        parseSelect("TShirt", props),
		Roles:         parseSelectList("Roles", props),
	}

	return speaker
}

func parseProposal(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue) *types.Proposal {
	prop := &types.Proposal{
		ID:              pageID,
		Title:           parseRichText("Title", props),
		Description:     parseRichText("Desc", props),
		Setup:           parseRichText("Setup", props),
		Comments:        parseRichText("Comments", props),
		TalkType:        parseSelect("TalkType", props),
		Status:          parseSelect("Status", props),
		DesiredDuration: int(props["DesiredDuration"].Number),
		AvailDuration:   int(props["AvailDuration"].Number),
		InviteToken:     parseRichText("InviteToken", props),
	}
	if tag := parseSelect("ScheduleFor", props); tag != "" {
		prop.ScheduleFor = lookupConfByTag(ctx, tag)
	}
	for _, ref := range props["speakers"].Relation {
		if ref != nil && ref.ID != "" {
			prop.SpeakerConfRefs = append(prop.SpeakerConfRefs, ref.ID)
		}
	}
	return prop
}

// lookupConfByTag finds a Conf by its tag in the cached Confs list. Returns
// nil if the tag isn't recognized — callers must handle that case.
func lookupConfByTag(ctx *config.AppContext, tag string) *types.Conf {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil
	}
	for _, c := range confs {
		if c.Tag == tag {
			return c
		}
	}
	return nil
}

func parseRecording(pageID string, props map[string]notion.PropertyValue) *types.Recording {
	rec := &types.Recording{
		ID:         pageID,
		TalkName:   parseRichText("TalkName", props),
		YTLink:     props["YTLink"].URL,
		XLink:      props["XLink"].URL,
		XReplyLink: props["XReplyLink"].URL,
		FileURI:    parseRichText("FileURI", props),
		PublishAt:  parseDate("PublishAt", props),
	}
	for _, ref := range props["talk"].Relation {
		if ref != nil && ref.ID != "" {
			rec.ConfTalkID = ref.ID
			break
		}
	}
	return rec
}

func parseConfTalk(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue, proposalMap map[string]*types.Proposal) *types.ConfTalk {
	ct := &types.ConfTalk{
		ID:              pageID,
		Clipart:         parseRichText("Clipart", props),
		Sched:           parseTimes("TalkTime", props),
		ProductionNotes: parseRichText("ProductionNotes", props),
		Venue:           parseSelect("Venue", props),
		SocialCard:      parseRichText("SocialCard", props),
		CalNotif:        parseRichText("CalNotif", props),
	}
	if tag := parseSelect("Event", props); tag != "" {
		ct.Conf = lookupConfByTag(ctx, tag)
	}
	if proposalMap != nil {
		if id := parseRef(props, "proposal"); id != "" {
			if p, ok := proposalMap[id]; ok {
				ct.Proposal = p
			}
		}
	}
	return ct
}

func parseSpeakerConf(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue, speakerMap map[string]*types.Speaker, proposalMap map[string]*types.Proposal) *types.SpeakerConf {
	sp := &types.SpeakerConf{
		ID:           pageID,
		ComingFrom:   parseRichText("ComingFrom", props),
		Availability: parseSelectList("Avails", props),
		RecordOK:     parseSelect("RecordOK", props),
		Visa:         parseSelect("Visa", props),
		FirstEvent:   parseCheckbox(props["FirstEvent"].Checkbox),
		DinnerRSVP:   parseCheckbox(props["DinnerRSVP"].Checkbox),
		Sponsor:      parseCheckbox(props["Sponsor"].Checkbox),
		Company:      parseRichText("Company", props),
		OrgPhoto:     parseRichText("OrgPhoto", props),
		InvitedAt:    parseDate("InvitedAt", props),
		ViewedAt:     parseDate("ViewedAt", props),
		AcceptedAt:   parseDate("AcceptedAt", props),
	}
	for _, tag := range parseSelectList("OtherEvents", props) {
		if c := lookupConfByTag(ctx, tag); c != nil {
			sp.OtherEvents = append(sp.OtherEvents, c)
		}
	}
	if speakerMap != nil {
		if id := parseRef(props, "speaker"); id != "" {
			if speaker, ok := speakerMap[id]; ok {
				sp.Speaker = speaker
			}
		}
	}
	if proposalMap != nil {
		// `talk` is a multi-relation: every proposal this speaker is
		// delivering at this conf.
		for _, ref := range props["talk"].Relation {
			if ref == nil || ref.ID == "" {
				continue
			}
			if proposal, ok := proposalMap[ref.ID]; ok {
				sp.Proposals = append(sp.Proposals, proposal)
			}
		}
	}
	return sp
}

func parseConf(pageID string, props map[string]notion.PropertyValue) *types.Conf {
	conf := &types.Conf{
		Ref:            pageID,
		Tag:            parseRichText("Name", props),
		UID:            parseUniqueID("ID", props),
		Active:         parseCheckbox(props["Active"].Checkbox),
		Desc:           parseRichText("Desc", props),
		OGFlavor:       parseRichText("OG_Flavor", props),
		Emoji:          parseRichText("Emoji", props),
		Tagline:        parseRichText("Tagline", props),
		DateDesc:       parseRichText("DateDesc", props),
		Location:       parseRichText("Location", props),
		Venue:          parseRichText("Venue", props),
		VenueMap:       props["VenueMap"].URL,
		VenueWebsite:   props["VenueWebsite"].URL,
		ShowHackathon:  parseCheckbox(props["Show Hacks"].Checkbox),
		HasSatellites:  parseCheckbox(props["Has Satellites"].Checkbox),
		OrientCalNotif: parseRichText("OrientCalNotif", props),
	}

	stdate := parseDate("StartDate", props)
	if stdate != nil {
		conf.StartDate = *stdate
	}
	edate := parseDate("EndDate", props)
	if edate != nil {
		conf.EndDate = *edate
	}

	// Timezone is an IANA name (e.g. "Europe/Vienna"). Pre-load
	// the *time.Location once at parse time so hot paths in
	// scheduling / agenda rendering hit Conf.Loc() without a
	// LoadLocation round-trip per request. Read tolerant of the
	// Notion column type — try select first (the natural fit
	// for an enum-shaped IANA name) and fall back to rich_text
	// so either schema choice round-trips. Unparseable values
	// leave TZ nil; callers fall back via Loc().
	conf.Timezone = strings.TrimSpace(parseSelect("Timezone", props))
	if conf.Timezone == "" {
		conf.Timezone = strings.TrimSpace(parseRichText("Timezone", props))
	}
	if conf.Timezone != "" {
		if loc, err := time.LoadLocation(conf.Timezone); err == nil {
			conf.TZ = loc
		}
	}

	return conf
}

func parseConfTicket(pageID string, props map[string]notion.PropertyValue) *types.ConfTicket {
	ticket := &types.ConfTicket{
		ID:         pageID,
		Tier:       parseRichText("Tier", props),
		Local:      uint(props["Local"].Number),
		BTC:        uint(props["BTC"].Number),
		USD:        uint(props["USD"].Number),
		Max:        uint(props["Max"].Number),
		Currency:   parseRichText("Currency", props),
		Symbol:     parseRichText("Symbol", props),
		PostSymbol: parseRichText("PostSymbol", props),
		Expires:    parseTimes("Expires", props),
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
		Amount:     props["Amount Paid"].Number,
		Currency:   parseSelect("Currency", props),
		Revoked:    parseCheckbox(props["Revoked"].Checkbox),
	}
	if len(props["conf"].Relation) > 0 {
		regis.ConfRef = props["conf"].Relation[0].ID
	}
	return regis
}

func parseJobType(pageID string, props map[string]notion.PropertyValue) *types.JobType {
	jobtype := &types.JobType{
		Ref:          pageID,
		Tag:          parseRichText("Tag", props),
		DisplayOrder: int(props["DisplayOrder"].Number),
		Title:        parseRichText("Title", props),
		Tooltip:      parseRichText("Tooltip", props),
		LongDesc:     parseRichText("LongDesc", props),
		Show:         parseCheckbox(props["Show"].Checkbox),
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
		WorkYes:     parseJobList(ctx, "WorkYes", props),
		WorkNo:      parseJobList(ctx, "WorkNo", props),

		FirstEvent: parseCheckbox(props["FirstEvent"].Checkbox),
		Hometown:   parseRichText("Hometown", props),
		Twitter:    types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr:      parseRichText("npub", props),
		Shirt:      parseSelect("Shirt", props),
		Status:     parseSelect("Status", props),
		CreatedAt:  parseDate("created", props),
	}

	return vol
}

// parseHHMM parses "HH:MM" military-time text and combines it with day's
// year/month/day/location to produce a fully-qualified time.Time. Returns
// false on any parse failure so callers can skip the field rather than
// poison the row.
func parseHHMM(s string, day time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return time.Time{}, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return time.Time{}, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, day.Location()), true
}

// parseTimesRange reads a rich-text field formatted as "HH:MM,HH:MM"
// (military-time start,end) and resolves it against day to produce a
// *Times. day's date + location anchor both endpoints. Returns nil if
// the field is empty or malformed; if only the start parses, End stays
// nil so callers see a half-open range rather than garbage.
func parseTimesRange(field string, props map[string]notion.PropertyValue, day time.Time) *types.Times {
	raw := strings.TrimSpace(parseRichText(field, props))
	if raw == "" {
		return nil
	}
	parts := strings.SplitN(raw, ",", 2)
	start, ok := parseHHMM(parts[0], day)
	if !ok {
		return nil
	}
	if len(parts) < 2 {
		return &types.Times{Start: start}
	}
	end, ok := parseHHMM(parts[1], day)
	if !ok {
		return &types.Times{Start: start}
	}
	return &types.Times{Start: start, End: &end}
}

// parseConfInfo parses a row from the ConfInfoDb. The "Conf" column
// holds the conf's Tag (e.g. "atx25") rather than a Notion relation —
// try select first (the natural fit for an enum-shaped tag) and fall
// back to rich_text so either schema choice round-trips. Day is
// combined with the resolved Conf.StartDate so the returned *Times
// values carry the conf's timezone.
//
// When the tag can't be matched in confByTag, the row still comes back
// with ConfTag and Day populated but empty time fields — useful for
// admin tooling that lists "orphan" rows.
func parseConfInfo(pageID string, props map[string]notion.PropertyValue, confByTag map[string]*types.Conf) *types.ConfInfo {
	tag := parseSelect("Conf", props)
	if tag == "" {
		tag = parseRichText("Conf", props)
	}
	day := int(props["Day"].Number)
	ci := &types.ConfInfo{
		ID:      pageID,
		ConfTag: tag,
		Day:     day,
		Venues:  parseSelectList("Venues", props),
	}
	conf := confByTag[tag]
	if conf == nil || day < 1 {
		return ci
	}
	// anchor = midnight of the conf's Nth day in the conf's local
	// timezone. parseHHMM reads day.Location() for the constructed
	// wall-clock time, so anchoring in conf.Loc() (rather than
	// StartDate's own zone — which Notion may have stored as UTC)
	// is what makes "9:30 AM coffee" land on the right absolute
	// instant for the conf's location.
	loc := conf.Loc()
	sd := conf.StartDate.In(loc)
	anchor := time.Date(sd.Year(), sd.Month(), sd.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, day-1)
	ci.Doors = parseTimesRange("Doors", props, anchor)
	ci.Breakfast = parseTimesRange("Breakfast", props, anchor)
	ci.Lunch = parseTimesRange("Lunch", props, anchor)
	ci.Coffee = parseTimesRange("Coffee", props, anchor)
	return ci
}

func parseVolInfo(pageID string, props map[string]notion.PropertyValue) *types.VolInfo {
	vinfo := &types.VolInfo{
		Ref:         pageID,
		ConfRef:     parseConfRef(props),
		OrientLink:  props["OrientLink"].URL,
		OrientTimes: parseTimes("OrientTimes", props),
		Notes:       parseRichText("Notes", props),
	}

	return vinfo
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
		Ref:       pageID,
		Name:      parseRichText("Name", props),
		MaxVols:   uint(props["MaxVols"].Number),
		Type:      parseJobTypes("TypeRef", props, jobtypes),
		Conf:      parseConfOne(ctx, "ConfRef", props),
		ShiftTime: parseTimes("ShiftTime", props),
		Priority:  uint(props["Priority"].Number),
		CalNotif:  parseRichText("CalNotif", props),
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
