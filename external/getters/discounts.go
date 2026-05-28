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

func getDiscounts(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting discounts...")
	if UsePostgresBackend(ctx) {
		discounts, err = listDiscountsPostgres(ctx)
	} else {
		discounts, err = ListDiscountsNotion(ctx.Notion)
	}

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
		queueRefresh(JobDiscounts)
	}

	return discounts, nil
}

func ListDiscounts(n *types.Notion) ([]*types.DiscountCode, error) {
	return ListDiscountsNotion(n)
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

	// Empty ConfRef = wildcard. Used by self-service affiliate
	// codes (which mint without any Conference relation) so a
	// single user code applies at every active event without an
	// admin re-attaching it per launch. Admin-created codes that
	// want this universal-redemption behavior can leave the
	// Conference relation empty too.
	if len(discount.ConfRef) > 0 {
		found := false
		for _, discountConfRef := range discount.ConfRef {
			found = found || discountConfRef == confRef
		}
		if !found {
			return tixPrice * count, nil, fmt.Errorf("%s not a valid code for conference (%s)", code, confRef)
		}
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
	if UsePostgresBackend(ctx) {
		return incrementDiscountUsesPostgres(ctx, discountRef, addCount)
	}
	return incrementDiscountUsesNotion(ctx, discountRef, addCount)
}

func incrementDiscountUsesNotion(ctx *config.AppContext, discountRef string, addCount uint) error {
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
