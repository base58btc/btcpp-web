package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	notion "github.com/niftynei/go-notion"
)

func createAffiliateCodeNotion(n *types.Notion, email, codeName string, buyerPct uint, confRefs []string) (string, error) {
	props := map[string]*notion.PropertyValue{
		"CodeName":       titleValue(codeName),
		"Discount":       richTextValue(fmt.Sprintf("%%%d", buyerPct)),
		"AffiliateEmail": notion.NewEmailPropertyValue(email),
	}
	if len(confRefs) > 0 {
		props["Conference"] = relationValue(confRefs)
	}
	parent := notion.NewDatabaseParent(n.Config.DiscountsDb)
	page, err := n.Client.CreatePage(context.Background(), parent, props)
	if err != nil {
		return "", err
	}
	queueRefresh(JobDiscounts)
	return page.ID, nil
}

func updateAffiliateCodeNotion(ctx *config.AppContext, codeID, codeName string, buyerPct uint, confRefs []string) error {
	props := map[string]*notion.PropertyValue{
		"CodeName": titleValue(codeName),
		"Discount": richTextValue(fmt.Sprintf("%%%d", buyerPct)),
	}
	if len(confRefs) > 0 {
		props["Conference"] = relationValue(confRefs)
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), codeID, props)
	if err != nil {
		return err
	}
	if len(confRefs) == 0 {
		if err := clearRelationProperty(ctx.Notion.Config.Token, codeID, "Conference"); err != nil {
			return err
		}
	}
	queueRefresh(JobDiscounts)
	return nil
}

func archiveAffiliateCodeNotion(ctx *config.AppContext, codeID string) error {
	body, err := json.Marshal(map[string]interface{}{"archived": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH",
		"https://api.notion.com/v1/pages/"+codeID,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Notion.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion archive discount %s: %v", codeID, errResp)
	}
	queueRefresh(JobDiscounts)
	return nil
}

func recordAffiliateUsageNotion(ctx *config.AppContext, in AffiliateUsageInput) error {
	if ctx.Notion.Config.AffiliateUsageDb == "" {
		return fmt.Errorf("RecordAffiliateUsage: AffiliateUsageDb not configured")
	}
	props := map[string]*notion.PropertyValue{
		"Name":           titleValue(fmt.Sprintf("%s/%s/%d", in.CodeName, in.ConfTag, in.TicketsCount)),
		"DiscountCode":   richTextValue(in.CodeName),
		"AffiliateEmail": notion.NewEmailPropertyValue(in.AffiliateEmail),
		"Conference":     selectValue(in.ConfTag),
	}
	if in.SavedSats != 0 {
		props["SavedSats"] = numberValue(float64(in.SavedSats))
	}
	if in.EarnedSats != 0 {
		props["EarnedSats"] = numberValue(float64(in.EarnedSats))
	}
	if in.TicketsCount != 0 {
		props["TicketsCount"] = numberValue(float64(in.TicketsCount))
	}
	parent := notion.NewDatabaseParent(ctx.Notion.Config.AffiliateUsageDb)
	_, err := ctx.Notion.Client.CreatePage(context.Background(), parent, props)
	return err
}

func listAffiliateUsageNotion(ctx *config.AppContext) ([]*types.AffiliateUsage, error) {
	if ctx.Notion.Config.AffiliateUsageDb == "" {
		return nil, fmt.Errorf("AffiliateUsageDb not configured")
	}
	n := ctx.Notion
	var out []*types.AffiliateUsage
	hasMore := true
	cursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.AffiliateUsageDb, notion.QueryDatabaseParam{
				StartCursor: cursor,
			})
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			created := p.CreatedTime
			out = append(out, parseAffiliateUsage(p.ID, p.Properties, &created))
		}
		cursor = next
		hasMore = more
	}
	return out, nil
}

func updateAffiliateUsageSatsNotion(ctx *config.AppContext, usageID string, savedSats, earnedSats int64) error {
	if usageID == "" {
		return fmt.Errorf("UpdateAffiliateUsageSats: usageID is required")
	}
	body := map[string]interface{}{
		"properties": map[string]interface{}{
			"SavedSats":  map[string]interface{}{"number": savedSats},
			"EarnedSats": map[string]interface{}{"number": earnedSats},
		},
	}
	return notionPagePost(ctx.Notion.Config.Token, "PATCH", "/"+usageID, body)
}

func queryAffiliateUsageByEmailNotion(ctx *config.AppContext, email string) ([]*types.AffiliateUsage, error) {
	if email == "" {
		return nil, nil
	}
	if ctx.Notion.Config.AffiliateUsageDb == "" {
		return nil, fmt.Errorf("AffiliateUsageDb not configured")
	}
	n := ctx.Notion
	var out []*types.AffiliateUsage
	hasMore := true
	cursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.AffiliateUsageDb, notion.QueryDatabaseParam{
				StartCursor: cursor,
				Filter: &notion.Filter{
					Property: "AffiliateEmail",
					Text:     &notion.TextFilterCondition{Equals: email},
				},
			})
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			created := p.CreatedTime
			out = append(out, parseAffiliateUsage(p.ID, p.Properties, &created))
		}
		cursor = next
		hasMore = more
	}
	return out, nil
}

func queryAffiliateUsageByConfNotion(ctx *config.AppContext, confTag string) ([]*types.AffiliateUsage, error) {
	if confTag == "" {
		return nil, nil
	}
	if ctx.Notion.Config.AffiliateUsageDb == "" {
		return nil, fmt.Errorf("AffiliateUsageDb not configured")
	}
	n := ctx.Notion
	var out []*types.AffiliateUsage
	hasMore := true
	cursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.AffiliateUsageDb, notion.QueryDatabaseParam{
				StartCursor: cursor,
				Filter: &notion.Filter{
					Property: "Conference",
					Select:   &notion.SelectFilterCondition{Equals: confTag},
				},
			})
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			created := p.CreatedTime
			out = append(out, parseAffiliateUsage(p.ID, p.Properties, &created))
		}
		cursor = next
		hasMore = more
	}
	return out, nil
}
