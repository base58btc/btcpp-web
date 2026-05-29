package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

func ListWorkShiftsNotion(ctx *config.AppContext) ([]*types.WorkShift, error) {
	var shiftList []*types.WorkShift
	n := ctx.Notion

	jobtypes, err := FetchJobsCached(ctx)
	if err != nil {
		return nil, err
	}

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.ShiftDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			shift := parseWorkShift(ctx, page.ID, page.Properties, jobtypes)
			shiftList = append(shiftList, shift)
		}
	}

	return shiftList, nil
}

// invalidateShiftCache forces the next FetchShiftsCached call to refetch.
func invalidateShiftCache() {
	shifts = nil
}

// buildShiftPropertiesJSON constructs the Notion `properties` payload for a
// shift page. We build this by hand (rather than using go-notion's
// PropertyValue/CreatePage) because the library marks every value field as
// json:omitempty, which silently drops zero-value Numbers (e.g. Priority=0)
// and produces an invalid Notion request.
func buildShiftPropertiesJSON(name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) map[string]interface{} {
	props := map[string]interface{}{
		"Name": map[string]interface{}{
			"title": []map[string]interface{}{
				{"text": map[string]interface{}{"content": name}},
			},
		},
		"MaxVols":  map[string]interface{}{"number": maxVols},
		"Priority": map[string]interface{}{"number": priority},
	}

	if !start.IsZero() {
		date := map[string]interface{}{
			"start": start.Format(time.RFC3339),
		}
		if !end.IsZero() {
			date["end"] = end.Format(time.RFC3339)
		}
		props["ShiftTime"] = map[string]interface{}{"date": date}
	}

	if jobType != nil {
		props["TypeRef"] = map[string]interface{}{
			"relation": []map[string]interface{}{{"id": jobType.Ref}},
		}
	}

	return props
}

// notionPagePost sends a JSON request directly to Notion's pages API. method
// is "POST" for create, "PATCH" for update. urlPath is appended to the v1/pages
// base. Returns the parsed JSON response or an error.
func notionPagePost(token, method, urlPath string, body map[string]interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, "https://api.notion.com/v1/pages"+urlPath, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion API error: %v", errResp)
	}
	return nil
}

// CreateShift creates a new WorkShift page in the Notion ShiftDb. ShiftTime
// must have a non-nil End. Bypasses go-notion's CreatePage to avoid the
// omitempty zero-value bug for Number properties.
func CreateShift(ctx *config.AppContext, conf *types.Conf, jobType *types.JobType, name string, start, end time.Time, maxVols, priority uint) error {
	if conf == nil || conf.Ref == "" {
		return fmt.Errorf("CreateShift: conf is nil or has empty ref")
	}

	props := buildShiftPropertiesJSON(name, jobType, start, end, maxVols, priority)
	props["ConfRef"] = map[string]interface{}{
		"relation": []map[string]interface{}{{"id": conf.Ref}},
	}

	body := map[string]interface{}{
		"parent": map[string]interface{}{
			"database_id": ctx.Notion.Config.ShiftDb,
		},
		"properties": props,
	}

	err := notionPagePost(ctx.Notion.Config.Token, "POST", "", body)
	if err != nil {
		return err
	}

	invalidateShiftCache()
	return nil
}

// UpdateShift updates a WorkShift's mutable fields. Pass nil for jobType to
// skip updating the type. Pass a zero start to skip updating the time. Uses
// direct HTTP PATCH to avoid go-notion's omitempty issues.
// UpdateShiftTimes patches only the ShiftTime property on a shift,
// leaving Name / JobType / MaxVols / Priority / Assignees untouched.
// Used by the gantt drag/resize UI on /volcoord/shifts so a coord
// can move + reshape a shift in place without re-sending fields
// that haven't changed (which would clobber concurrent edits).
//
// After the PATCH succeeds we *synchronously* reload the shifts
// cache rather than calling invalidateShiftCache (which nils the
// slice and queues an async refresh). The drag UI reloads the page
// immediately after the POST returns; an async refresh would race
// the next GET and serve a nil slice — making every shift on the
// page momentarily disappear.
func UpdateShiftTimes(ctx *config.AppContext, shiftRef string, start, end time.Time) error {
	if start.IsZero() {
		return fmt.Errorf("UpdateShiftTimes: start required")
	}
	date := map[string]interface{}{
		"start": start.Format(time.RFC3339),
	}
	if !end.IsZero() {
		date["end"] = end.Format(time.RFC3339)
	}
	body := map[string]interface{}{
		"properties": map[string]interface{}{
			"ShiftTime": map[string]interface{}{"date": date},
		},
	}
	if err := notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+shiftRef, body); err != nil {
		return err
	}
	if fresh, err := ListWorkShifts(ctx); err == nil {
		shifts = fresh
		lastShiftFetch = time.Now()
		writeCache("shifts", shifts)
	} else {
		ctx.Err.Printf("UpdateShiftTimes: cache reload (continuing): %s", err)
	}
	return nil
}

func UpdateShift(ctx *config.AppContext, shiftRef, name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) error {
	props := buildShiftPropertiesJSON(name, jobType, start, end, maxVols, priority)

	body := map[string]interface{}{
		"properties": props,
	}

	err := notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+shiftRef, body)
	if err != nil {
		return err
	}

	invalidateShiftCache()
	return nil
}

func AssignVolunteerToShift(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

	// First get the current shift to get existing assignees
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return err
	}

	var shift *types.WorkShift
	for _, s := range allShifts {
		if s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil {
		return fmt.Errorf("shift %s not found", shiftRef)
	}

	// Check if already assigned
	for _, assignee := range shift.AssigneesRef {
		if assignee == volRef {
			return nil // Already assigned
		}
	}

	// Build new assignees list
	newAssignees := make([]*notion.ObjectReference, len(shift.AssigneesRef)+1)
	for i, ref := range shift.AssigneesRef {
		newAssignees[i] = &notion.ObjectReference{
			Object: notion.ObjectPage,
			ID:     ref,
		}
	}
	newAssignees[len(shift.AssigneesRef)] = &notion.ObjectReference{
		Object: notion.ObjectPage,
		ID:     volRef,
	}

	_, err = n.Client.UpdatePageProperties(context.Background(), shiftRef,
		map[string]*notion.PropertyValue{
			"Assignees": {
				Type:     notion.PropertyRelation,
				Relation: newAssignees,
			},
		})

	if err == nil {
		// Update local cache
		shift.AssigneesRef = append(shift.AssigneesRef, volRef)
	}

	return err
}

func RemoveVolunteerFromShift(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

	// First get the current shift to get existing assignees
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return err
	}

	var shift *types.WorkShift
	for _, s := range allShifts {
		if s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil {
		return fmt.Errorf("shift %s not found", shiftRef)
	}

	// Build new assignees list without the volunteer
	newAssignees := make([]*notion.ObjectReference, 0)
	newAssigneesRef := make([]string, 0)
	for _, ref := range shift.AssigneesRef {
		if ref != volRef {
			newAssignees = append(newAssignees, &notion.ObjectReference{
				Object: notion.ObjectPage,
				ID:     ref,
			})
			newAssigneesRef = append(newAssigneesRef, ref)
		}
	}

	// If relation is empty, use direct HTTP request since go-notion's
	// omitempty causes empty slices to be omitted from JSON
	if len(newAssignees) == 0 {
		err = clearRelationProperty(n.Config.Token, shiftRef, "Assignees")
	} else {
		_, err = n.Client.UpdatePageProperties(context.Background(), shiftRef,
			map[string]*notion.PropertyValue{
				"Assignees": {
					Type:     notion.PropertyRelation,
					Relation: newAssignees,
				},
			})
	}

	if err == nil {
		// Update local cache
		shift.AssigneesRef = newAssigneesRef
	}

	return err
}

// clearRelationProperty makes a direct HTTP request to Notion API to clear a relation
func clearRelationProperty(token, pageID, propertyName string) error {
	payload := map[string]interface{}{
		"properties": map[string]interface{}{
			propertyName: map[string]interface{}{
				"relation": []interface{}{},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", "https://api.notion.com/v1/pages/"+pageID, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion API error: %v", errResp)
	}

	return nil
}

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

	// WorkYes
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

	// WorkNo
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

func RegisterVolunteer(n *types.Notion, vol *types.Volunteer) error {
	normalizeVolunteerInput(vol)
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

// FetchVolunteer retrieves a single volunteer page directly by ID. This is a
// strongly-consistent read (unlike QueryDatabase, which uses an
// eventually-consistent index), so it should be used after writes when the
// caller needs to render the just-updated state.
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
