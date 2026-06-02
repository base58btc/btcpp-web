package getters

import (
	"context"
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

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

// buildShiftPropertiesJSON constructs the Notion properties payload for a
// shift page. We build this by hand because go-notion omits zero-value Numbers.
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

// CreateShift creates a new WorkShift page in the Notion ShiftDb. ShiftTime
// must have a non-nil End. Bypasses go-notion's CreatePage to avoid the
// omitempty zero-value bug for Number properties.
func CreateShiftNotion(ctx *config.AppContext, conf *types.Conf, jobType *types.JobType, name string, start, end time.Time, maxVols, priority uint) error {
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

// UpdateShiftTimes patches only the ShiftTime property on a shift, leaving
// Name / JobType / MaxVols / Priority / Assignees untouched.
func UpdateShiftTimesNotion(ctx *config.AppContext, shiftRef string, start, end time.Time) error {
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
	refreshShiftCache(ctx, "UpdateShiftTimes")
	return nil
}

// UpdateShift updates a WorkShift's mutable fields. Pass nil for jobType to
// skip updating the type. Pass a zero start to skip updating the time.
func UpdateShiftNotion(ctx *config.AppContext, shiftRef, name string, jobType *types.JobType, start, end time.Time, maxVols, priority uint) error {
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

func AssignVolunteerToShiftNotion(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

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

	for _, assignee := range shift.AssigneesRef {
		if assignee == volRef {
			return nil
		}
	}

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
		shift.AssigneesRef = append(shift.AssigneesRef, volRef)
	}

	return err
}

func RemoveVolunteerFromShiftNotion(ctx *config.AppContext, volRef, shiftRef string) error {
	n := ctx.Notion

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
		shift.AssigneesRef = newAssigneesRef
	}

	return err
}

func shiftUpdateCalNotifNotion(n *types.Notion, shiftID string, calnotif string) error {
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
	for _, s := range shifts {
		if s != nil && s.Ref == shiftID {
			s.CalNotif = calnotif
			break
		}
	}
	return nil
}
