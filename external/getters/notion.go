package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
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

var shifts []*types.WorkShift
var lastShiftFetch time.Time

var orgs []*types.Org
var lastOrgFetch time.Time

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
	JobShifts
	JobOrgs
)

var taskChan chan JobType = make(chan JobType)
var cacheTTL time.Duration

type TalksCallback func(ctx *config.AppContext, talks []*types.Talk)
type SpeakersCallback func(ctx *config.AppContext, speakers []*types.Speaker)

var onTalksRefresh []TalksCallback
var onSpeakersRefresh []SpeakersCallback

func OnTalksRefresh(cb TalksCallback) {
	onTalksRefresh = append(onTalksRefresh, cb)
}

func OnSpeakersRefresh(cb SpeakersCallback) {
	onSpeakersRefresh = append(onSpeakersRefresh, cb)
}

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

func loadFromCache() bool {
        loaded := true
        if !readCache("confs", &confs) { loaded = false }
        if !readCache("speakers", &cacheSpeakers) { loaded = false }
        if !readCache("talks", &talks) { loaded = false }
        if !readCache("discounts", &discounts) { loaded = false }
        if !readCache("hotels", &hotels) { loaded = false }
        if !readCache("jobs", &jobs) { loaded = false }
        if !readCache("shifts", &shifts) { loaded = false }
        if !readCache("orgs", &orgs) { loaded = false }

        if loaded {
                now := time.Now()
                lastConfsFetch = now
                lastSpeakerFetch = now
                lastTalksFetch = now
                lastDiscountFetch = now
                lastHotelFetch = now
                lastJobTypeFetch = now
                lastShiftFetch = now
                lastOrgFetch = now
                return true
        }

        return false
}

func WaitFetch(ctx *config.AppContext) {
	cacheTTL = time.Duration(ctx.Env.CacheTTLSec) * time.Second

	if !ctx.InProduction {
		EnableDiskCache()
	}

	// Try loading from disk cache first (dev only)
	if diskCacheEnabled && loadFromCache() {
                ctx.Infos.Printf("Loaded all data from disk cache!")
                return 
	}

	ctx.Infos.Printf("Disk cache incomplete, fetching from Notion...")

	// Phase 1: fetch all independent data in parallel
	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); runJob(ctx, JobConfs); lastConfsFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobSpeakers); lastSpeakerFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobDiscounts); lastDiscountFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobHotels); lastHotelFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobJobs); lastJobTypeFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobOrgs); lastOrgFetch = time.Now() }()
	wg.Wait()

	// Phase 2: fetch data that depends on phase 1
	wg.Add(2)
	go func() { defer wg.Done(); runJob(ctx, JobTalks); lastTalksFetch = time.Now() }()
	go func() { defer wg.Done(); runJob(ctx, JobShifts); lastShiftFetch = time.Now() }()
	wg.Wait()
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
	case JobShifts:
		getShifts(ctx)
	case JobOrgs:
		getOrgs(ctx)
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
		writeCache("confs", confs)
	}
}

func FetchConfsCached(ctx *config.AppContext) ([]*types.Conf, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
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
		writeCache("speakers", cacheSpeakers)
                ctx.Infos.Printf("there are %d callbacks", len(onSpeakersRefresh))
		for _, cb := range onSpeakersRefresh {
			cb(ctx, cacheSpeakers)
		}
	}
}

/* This may return nil */
func FetchSpeakersCached(ctx *config.AppContext) ([]*types.Speaker, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
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
		writeCache("talks", talks)
		for _, cb := range onTalksRefresh {
			cb(ctx, talks)
		}
	}
}

/* This may return nil */
func FetchTalksCached(ctx *config.AppContext) ([]*types.Talk, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
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
		writeCache("discounts", discounts)
	}
}

/* This may return nil */
func FetchDiscountsCached(ctx *config.AppContext) ([]*types.DiscountCode, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
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
		writeCache("hotels", hotels)
	}
}

/* This may return nil */
func FetchHotelsCached(ctx *config.AppContext) ([]*types.Hotel, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
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
		writeCache("jobs", jobs)
	}
}

/* This may return nil */
func FetchJobsCached(ctx *config.AppContext) ([]*types.JobType, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if jobs == nil || lastJobTypeFetch.Before(deadline) {
		lastJobTypeFetch = time.Now()
		taskChan <- JobJobs
	}

	return jobs, nil
}

func getShifts(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting shifts...")
	shifts, err = ListWorkShifts(ctx)

	if err != nil {
		ctx.Err.Printf("error fetching shifts %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d shifts!", len(shifts))
		writeCache("shifts", shifts)
	}
}

/* This may return nil */
func FetchShiftsCached(ctx *config.AppContext) ([]*types.WorkShift, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if shifts == nil || lastShiftFetch.Before(deadline) {
		lastShiftFetch = time.Now()
		taskChan <- JobShifts
	}

	return shifts, nil
}

func getOrgs(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting orgs...")
	orgs, err = ListOrgs(ctx.Notion)

	if err != nil {
		ctx.Err.Printf("error fetching orgs %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d orgs!", len(orgs))
		writeCache("orgs", orgs)
	}
}

/* This may return nil */
func FetchOrgsCached(ctx *config.AppContext) ([]*types.Org, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if orgs == nil || lastOrgFetch.Before(deadline) {
		lastOrgFetch = time.Now()
		taskChan <- JobOrgs
	}

	return orgs, nil
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

func listTalksForEvent(ctx *config.AppContext, speakerMap map[string]*types.Speaker, eventTag string) ([]*types.Talk, error) {
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
				Filter: &notion.Filter{
					Property: "Event",
					Select: &notion.SelectFilterCondition{
						Equals: eventTag,
					},
				},
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			talk := parseTalk(page.ID, page.Properties, speakerMap)
			talks = append(talks, talk)
		}
	}

	return talks, nil
}

func listTalks(ctx *config.AppContext, speakers []*types.Speaker) ([]*types.Talk, error) {
	// Build speaker map for O(1) lookups during parsing
	speakerMap := make(map[string]*types.Speaker)
	for _, s := range speakers {
		speakerMap[s.ID] = s
	}

	// Fetch talks per conference to stay under Notion's pagination limits
	cachedConfs, err := FetchConfsCached(ctx)
	if err != nil {
		return nil, err
	}

	var allTalks []*types.Talk
	for _, conf := range cachedConfs {
		confTalks, err := listTalksForEvent(ctx, speakerMap, conf.Tag)
		if err != nil {
			ctx.Err.Printf("listTalks: failed for event %s: %s", conf.Tag, err)
			continue
		}
		ctx.Infos.Printf("listTalks: loaded %d talks for %s", len(confTalks), conf.Tag)
		allTalks = append(allTalks, confTalks...)
	}

	ctx.Infos.Printf("listTalks: total %d talks loaded across %d confs", len(allTalks), len(cachedConfs))
	return allTalks, nil
}

func TalkUpdateCardURL(n *types.Notion, talkID string, cardURL string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), talkID,
		map[string]*notion.PropertyValue{
			"TalkCardURL": notion.NewURLPropertyValue(cardURL),
		})
	return err
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

func ListWorkShifts(ctx *config.AppContext) ([]*types.WorkShift, error) {
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

func GetShiftsForConf(ctx *config.AppContext, confTag string) ([]*types.WorkShift, error) {
	allShifts, err := FetchShiftsCached(ctx)
	if err != nil {
		return nil, err
	}

	var filtered []*types.WorkShift
	for _, shift := range allShifts {
		if shift.Conf != nil && shift.Conf.Tag == confTag {
			filtered = append(filtered, shift)
		}
	}
	return filtered, nil
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

	resp, err := http.DefaultClient.Do(req)
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

	resp, err := http.DefaultClient.Do(req)
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

func CalcDiscount(ctx *config.AppContext, confRef string, code string, tixPrice uint, count uint) (uint, *types.DiscountCode, error) {
	discount, err := FindDiscount(ctx, code)

	if err != nil {
		return tixPrice * count, nil, err
	}

	/* Discount not found! */
	if discount == nil {
		return tixPrice * count, nil, fmt.Errorf("Discount code \"%s\" not found", code)
	}

	found := false
	for _, discountConfRef := range discount.ConfRef {
		found = found || discountConfRef == confRef
	}

	if !found {
		return tixPrice * count, nil, fmt.Errorf("%s not a valid code for conference (%s)", code, confRef)
	}

	if discount.MaxUses > 0 && discount.UsesCount >= discount.MaxUses {
		return tixPrice * count, nil, fmt.Errorf("Discount code \"%s\" has been fully redeemed", code)
	}
	if discount.IsDateExpired(time.Now().UTC()) {
		return tixPrice * count, nil, fmt.Errorf("Discount code \"%s\" has expired", code)
	}

	if count == 0 {
		count = 1
	}

	total := discount.CalcTotal(tixPrice, count)
	return total, discount, nil
}

func IncrementDiscountUses(ctx *config.AppContext, discountRef string, addCount uint) error {
	// Find the discount to get current uses count
	cachedDiscounts, err := FetchDiscountsCached(ctx)
	if err != nil {
		return err
	}

	var currentUses uint
	for _, d := range cachedDiscounts {
		if d.Ref == discountRef {
			currentUses = d.UsesCount
			// Update the cached value immediately so subsequent
			// checks see the new count without waiting for cache refresh
			d.UsesCount += addCount
			break
		}
	}

	newCount := float64(currentUses + addCount)

	_, err = ctx.Notion.Client.UpdatePageProperties(context.Background(), discountRef,
		map[string]*notion.PropertyValue{
			"UsesCount": {
				Type:   notion.PropertyNumber,
				Number: newCount,
			},
		})

	// Force discount cache refresh on next access
	lastDiscountFetch = time.Time{}

	return err
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
                                Type: notion.PropertyCheckbox,
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
		_, err = n.Client.CreatePage(context.Background(), parent, vals)
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
                "Status": {
                        Type: notion.PropertySelect,
                        Select: &notion.SelectOption{
                                Name: "Applied",
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

func GetVolInfo(ctx *config.AppContext, confRef string) (*types.VolInfo, error) {
        infos, err := GetVolInfos(ctx, confRef)
        if err != nil {
                return nil, err
        }

        if len(infos) == 0 {
                return nil, fmt.Errorf("Invalid confref for volinfos %s", confRef)
        }

        return infos[0], nil
}

func GetVolInfoMap(ctx *config.AppContext) (map[string]*types.VolInfo, error) {
        vmap := make(map[string]*types.VolInfo)
        volinfos, err := GetVolInfos(ctx, "")
        if err != nil {
                return vmap, err
        }

	confs, err = FetchConfsCached(ctx)
        if err != nil {
                return vmap, err
        }
        for _, vi := range volinfos {
                for _, conf := range confs {
                        if conf.Ref == vi.ConfRef {
                                vmap[conf.Tag] = vi
                                break
                        }
                }
        }

        return vmap, nil
}

func GetVolInfos(ctx *config.AppContext, confRef string) ([]*types.VolInfo, error) {
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

func ListVolunteerApps(ctx *config.AppContext, email string) ([]*types.Volunteer, error) {
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
func FetchVolunteer(ctx *config.AppContext, volRef string) (*types.Volunteer, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), volRef)
	if err != nil {
		return nil, err
	}
	return parseVolunteer(ctx, page.ID, page.Properties), nil
}

func ListVolunteersForConf(ctx *config.AppContext, confRef string) ([]*types.Volunteer, error) {
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
                "Avails":  &notion.PropertyValue {
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

        if tapp.Twitter.Handle != "" {
                vals["Twitter"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.Twitter.Handle}},
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

        if tapp.OrgTwitter.Handle != "" {
                vals["OrgTwitter"] = notion.NewRichTextPropertyValue(
                        []*notion.RichText{
                                {Type: notion.RichTextText,
                                 Text: &notion.Text{Content: tapp.OrgTwitter.Handle}},
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
