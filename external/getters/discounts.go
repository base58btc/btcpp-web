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

// DiscountInput is the normalized write shape for admin-created
// DiscountsDb rows. DiscountExpr uses the existing compact grammar
// parsed by types.DiscountCode.ParseDiscountExpr.
type DiscountInput struct {
	CodeName       string
	DiscountExpr   string
	ConfRef        string
	AffiliateEmail string
}

// CreateDiscount inserts a DiscountsDb row scoped to a single
// conference. AffiliateEmail is optional; when set, successful
// checkouts using the code will be credited to that affiliate.
func CreateDiscount(n *types.Notion, in DiscountInput) (string, error) {
	if in.CodeName == "" {
		return "", fmt.Errorf("CreateDiscount: CodeName is required")
	}
	if in.DiscountExpr == "" {
		return "", fmt.Errorf("CreateDiscount: DiscountExpr is required")
	}
	if in.ConfRef == "" {
		return "", fmt.Errorf("CreateDiscount: ConfRef is required")
	}

	props := map[string]*notion.PropertyValue{
		"CodeName":   titleValue(in.CodeName),
		"Discount":   richTextValue(in.DiscountExpr),
		"Conference": relationValue([]string{in.ConfRef}),
	}
	if in.AffiliateEmail != "" {
		props["AffiliateEmail"] = notion.NewEmailPropertyValue(in.AffiliateEmail)
	}

	parent := notion.NewDatabaseParent(n.Config.DiscountsDb)
	page, err := n.Client.CreatePage(context.Background(), parent, props)
	if err != nil {
		return "", err
	}
	if discounts != nil {
		discount := &types.DiscountCode{
			Ref:            page.ID,
			CodeName:       in.CodeName,
			Discount:       in.DiscountExpr,
			ConfRef:        []string{in.ConfRef},
			AffiliateEmail: in.AffiliateEmail,
		}
		_ = discount.ParseDiscountExpr()
		discounts = append(discounts, discount)
	}
	queueRefresh(JobDiscounts)
	return page.ID, nil
}

// UpdateDiscount patches an existing DiscountsDb row. The admin UI
// always submits the full editable shape, including the event relation,
// so this intentionally rewrites the code, expression, relation, and
// optional affiliate email together.
func UpdateDiscount(ctx *config.AppContext, discountID string, in DiscountInput) error {
	if discountID == "" {
		return fmt.Errorf("UpdateDiscount: discountID is required")
	}
	if in.CodeName == "" {
		return fmt.Errorf("UpdateDiscount: CodeName is required")
	}
	if in.DiscountExpr == "" {
		return fmt.Errorf("UpdateDiscount: DiscountExpr is required")
	}
	if in.ConfRef == "" {
		return fmt.Errorf("UpdateDiscount: ConfRef is required")
	}

	props := map[string]*notion.PropertyValue{
		"CodeName":       titleValue(in.CodeName),
		"Discount":       richTextValue(in.DiscountExpr),
		"Conference":     relationValue([]string{in.ConfRef}),
		"AffiliateEmail": notion.NewEmailPropertyValue(in.AffiliateEmail),
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), discountID, props)
	if err != nil {
		return err
	}
	if discounts != nil {
		for _, d := range discounts {
			if d == nil || d.Ref != discountID {
				continue
			}
			d.CodeName = in.CodeName
			d.Discount = in.DiscountExpr
			d.ConfRef = []string{in.ConfRef}
			d.AffiliateEmail = in.AffiliateEmail
			d.DiscType = 0
			d.Amount = 0
			d.MaxUses = 0
			d.ExtraQty = 0
			d.ValidFrom = nil
			d.ValidUntil = nil
			_ = d.ParseDiscountExpr()
			break
		}
	}
	queueRefresh(JobDiscounts)
	return nil
}

// ArchiveDiscount soft-deletes a DiscountsDb row in Notion. Past
// purchase rows keep their discount-ref history; future checkout
// lookups stop seeing the archived code after cache refresh.
func ArchiveDiscount(ctx *config.AppContext, discountID string) error {
	if discountID == "" {
		return fmt.Errorf("ArchiveDiscount: discountID is required")
	}
	body, err := json.Marshal(map[string]interface{}{"archived": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH",
		"https://api.notion.com/v1/pages/"+discountID,
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
		return fmt.Errorf("notion archive discount %s: %v", discountID, errResp)
	}
	if discounts != nil {
		filtered := discounts[:0]
		for _, d := range discounts {
			if d == nil || d.Ref != discountID {
				filtered = append(filtered, d)
			}
		}
		discounts = filtered
	}
	queueRefresh(JobDiscounts)
	return nil
}
