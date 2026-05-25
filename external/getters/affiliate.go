package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	notion "github.com/niftynei/go-notion"
)

// CreateAffiliateCode mints a new DiscountCode row owned by the
// dashboard user. The Discount expression is `%X` where X is the
// slider value (0-20, in steps of 5). AffiliateEmail is set to the
// authed email so webhooks know to record usage. ConfRef wires the
// code to every active conf passed in (typically all currently-Active
// confs at creation time).
//
// Caller is responsible for uniqueness — see IsCodeNameAvailable.
// Returns the new page ID.
func CreateAffiliateCode(n *types.Notion, email, codeName string, buyerPct uint, confRefs []string) (string, error) {
	if email == "" {
		return "", fmt.Errorf("CreateAffiliateCode: empty email")
	}
	if codeName == "" {
		return "", fmt.Errorf("CreateAffiliateCode: empty codeName")
	}
	// CodeName is the DiscountsDb title-typed property; AffiliateEmail
	// is an email-typed property. Using rich_text for either gets a
	// "expected to be title / email" rejection from Notion.
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

// UpdateAffiliateCode patches an existing DiscountCode row owned by
// an affiliate. Rewrites the Discount expression based on the new
// slider value. Re-syncs ConfRef to whatever's currently active so a
// code created last year still works at this year's events.
func UpdateAffiliateCode(ctx *config.AppContext, codeID, codeName string, buyerPct uint, confRefs []string) error {
	if codeID == "" {
		return fmt.Errorf("UpdateAffiliateCode: empty codeID")
	}
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
	// go-notion's PropertyValue.Relation is `omitempty`, so an
	// empty slice gets dropped from the PATCH body and Notion
	// rejects "Conference" with no concrete sub-field. To
	// actually clear the relation we issue a separate raw PATCH
	// via the existing clearRelationProperty helper.
	if len(confRefs) == 0 {
		if err := clearRelationProperty(ctx.Notion.Config.Token, codeID, "Conference"); err != nil {
			return err
		}
	}
	queueRefresh(JobDiscounts)
	return nil
}

// ArchiveAffiliateCode soft-deletes the DiscountCode row in Notion
// (recoverable from the trash for 30 days). Past AffiliateUsage rows
// stay put — disabling the code doesn't erase the affiliate's
// historical earnings record. Mirrors DeleteConfTalk's raw HTTP PATCH
// because go-notion doesn't expose `archived` on UpdatePageProperties.
func ArchiveAffiliateCode(ctx *config.AppContext, codeID string) error {
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

// GetDiscountByRef looks up a DiscountCode by its Notion page ID
// against the warm cache. Returns nil when the cache doesn't have a
// match (e.g. mid-refresh). Used by webhooks that have a
// discount-ref from checkout metadata and need to read AffiliateEmail.
func GetDiscountByRef(ctx *config.AppContext, ref string) (*types.DiscountCode, error) {
	if ref == "" {
		return nil, nil
	}
	discounts, err := FetchDiscountsCached(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range discounts {
		if d != nil && d.Ref == ref {
			return d, nil
		}
	}
	return nil, nil
}

// FindAffiliateCodeByEmail returns the (live, non-archived) discount
// code an affiliate owns, or nil if they don't have one. Reads from
// the warm cache. Email match is case-insensitive.
func FindAffiliateCodeByEmail(ctx *config.AppContext, email string) (*types.DiscountCode, error) {
	if email == "" {
		return nil, nil
	}
	discounts, err := FetchDiscountsCached(ctx)
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(email)
	for _, d := range discounts {
		if d != nil && strings.ToLower(d.AffiliateEmail) == target {
			return d, nil
		}
	}
	return nil, nil
}

// IsCodeNameAvailable returns true when no live discount currently
// uses the given name. Case-insensitive (Notion's user-facing match
// is also case-insensitive). Cache-only — there's a small race window
// where two simultaneous creates can both pass the check; the
// creator should re-check after CreatePage and archive any duplicate.
func IsCodeNameAvailable(ctx *config.AppContext, codeName string) (bool, error) {
	if codeName == "" {
		return false, nil
	}
	discounts, err := FetchDiscountsCached(ctx)
	if err != nil {
		return false, err
	}
	target := strings.ToUpper(codeName)
	for _, d := range discounts {
		if d != nil && strings.ToUpper(d.CodeName) == target {
			return false, nil
		}
	}
	return true, nil
}

// AffiliateUsageInput is the data needed to record one redemption.
// SavedSats / EarnedSats are stored as int64 sats so the value is
// stable across ticket currencies (the buyer might have paid in
// USD, EUR, etc — the affiliate's view is BTC-denominated).
type AffiliateUsageInput struct {
	CodeName       string
	AffiliateEmail string
	ConfTag        string
	SavedSats      int64
	EarnedSats     int64
	TicketsCount   uint
}

// RecordAffiliateUsage appends one row to AffiliateUsageDb. Called
// from the Stripe + OpenNode webhooks after a successful checkout
// when the discount has an AffiliateEmail. Failure is best-effort —
// the caller logs and continues so a Notion blip can't block the
// ticket-issuance pipeline.
//
// AffiliateEmail is written as a Notion email-typed property to
// match the DiscountsDb AffiliateEmail column convention. The
// AffiliateUsageDb column should be email type. SavedSats /
// EarnedSats / TicketsCount are number-typed and skipped when
// zero — go-notion's PropertyValue.Number is `,omitempty`, so a 0
// value JSON-marshals as `{"type": "number"}` with no concrete body
// and Notion rejects it. Empty cells read back as 0 from the
// dashboard sum, so omitting is semantically equivalent.
func RecordAffiliateUsage(ctx *config.AppContext, in AffiliateUsageInput) error {
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

// ListAffiliateUsage issues a live paginated query for every
// AffiliateUsageDb row. This is intended for admin/backfill jobs, not
// request paths.
func ListAffiliateUsage(ctx *config.AppContext) ([]*types.AffiliateUsage, error) {
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

// UpdateAffiliateUsageSats rewrites the stored sats split on an existing
// AffiliateUsage row. It uses the direct Notion JSON path so EarnedSats
// can be set to zero; go-notion omits zero number values.
func UpdateAffiliateUsageSats(ctx *config.AppContext, usageID string, savedSats, earnedSats int64) error {
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

// QueryAffiliateUsageByEmail issues a live Notion query against
// AffiliateUsageDb filtering on the AffiliateEmail rich_text equals
// the given email. No caching — affiliates expect to see fresh stats
// the moment they refresh the dashboard after a redemption.
func QueryAffiliateUsageByEmail(ctx *config.AppContext, email string) ([]*types.AffiliateUsage, error) {
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

// QueryAffiliateUsageByConf issues a live Notion query against
// AffiliateUsageDb filtering on Conference == confTag. Used by the
// per-conf admin dashboard to roll up affiliate redemptions for the
// stats panel + top-affiliates table. No caching — same rationale
// as QueryAffiliateUsageByEmail.
func QueryAffiliateUsageByConf(ctx *config.AppContext, confTag string) ([]*types.AffiliateUsage, error) {
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

// AffiliateStatsTotals are the aggregate numbers shown on the
// dashboard's affiliate section. Saved / Earned are denominated
// in sats (converted from per-checkout fiat at write time) so the
// total is comparable across multi-currency confs.
type AffiliateStatsTotals struct {
	TicketsSold int
	SavedSats   int64
	EarnedSats  int64
}

// SumAffiliateStatsByEmail aggregates every AffiliateUsage row for a
// given email. One Notion query per call (no cache). Returns zeroed
// totals when the affiliate has never had a redemption.
func SumAffiliateStatsByEmail(ctx *config.AppContext, email string) (AffiliateStatsTotals, error) {
	rows, err := QueryAffiliateUsageByEmail(ctx, email)
	if err != nil {
		return AffiliateStatsTotals{}, err
	}
	var totals AffiliateStatsTotals
	for _, r := range rows {
		if r == nil {
			continue
		}
		totals.TicketsSold += int(r.TicketsCount)
		totals.SavedSats += r.SavedSats
		totals.EarnedSats += r.EarnedSats
	}
	return totals, nil
}
