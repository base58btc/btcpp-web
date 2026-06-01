package getters

import (
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// CreateAffiliateCode mints a new DiscountCode row owned by the
// dashboard user. Caller is responsible for uniqueness; see
// IsCodeNameAvailable.
func CreateAffiliateCode(ctx *config.AppContext, email, codeName string, buyerPct uint, confRefs []string) (string, error) {
	if UsePostgresBackend(ctx) {
		return "", unsupportedPostgresBackend("CreateAffiliateCode")
	}
	if email == "" {
		return "", fmt.Errorf("CreateAffiliateCode: empty email")
	}
	if codeName == "" {
		return "", fmt.Errorf("CreateAffiliateCode: empty codeName")
	}
	return createAffiliateCodeNotion(ctx.Notion, email, codeName, buyerPct, confRefs)
}

// UpdateAffiliateCode patches an existing DiscountCode row owned by
// an affiliate.
func UpdateAffiliateCode(ctx *config.AppContext, codeID, codeName string, buyerPct uint, confRefs []string) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("UpdateAffiliateCode")
	}
	if codeID == "" {
		return fmt.Errorf("UpdateAffiliateCode: empty codeID")
	}
	return updateAffiliateCodeNotion(ctx, codeID, codeName, buyerPct, confRefs)
}

// ArchiveAffiliateCode soft-deletes the DiscountCode row. Past
// AffiliateUsage rows stay put.
func ArchiveAffiliateCode(ctx *config.AppContext, codeID string) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("ArchiveAffiliateCode")
	}
	return archiveAffiliateCodeNotion(ctx, codeID)
}

// GetDiscountByRef looks up a DiscountCode by ID against the warm cache.
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

// FindAffiliateCodeByEmail returns the live discount code an affiliate owns,
// or nil if they don't have one. Reads from the warm cache.
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

// IsCodeNameAvailable returns true when no live discount currently uses the
// given name. Case-insensitive. Cache-only; callers should still handle races.
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
type AffiliateUsageInput struct {
	CodeName       string
	AffiliateEmail string
	ConfTag        string
	SavedSats      int64
	EarnedSats     int64
	TicketsCount   uint
}

func RecordAffiliateUsage(ctx *config.AppContext, in AffiliateUsageInput) error {
	if UsePostgresBackend(ctx) {
		return recordAffiliateUsagePostgres(ctx, in)
	}
	return recordAffiliateUsageNotion(ctx, in)
}

// ListAffiliateUsage issues a live paginated query for every AffiliateUsageDb
// row. This is intended for admin/backfill jobs, not request paths.
func ListAffiliateUsage(ctx *config.AppContext) ([]*types.AffiliateUsage, error) {
	if UsePostgresBackend(ctx) {
		return listAffiliateUsagePostgres(ctx)
	}
	return listAffiliateUsageNotion(ctx)
}

// UpdateAffiliateUsageSats rewrites the stored sats split on an existing
// AffiliateUsage row.
func UpdateAffiliateUsageSats(ctx *config.AppContext, usageID string, savedSats, earnedSats int64) error {
	if UsePostgresBackend(ctx) {
		return updateAffiliateUsageSatsPostgres(ctx, usageID, savedSats, earnedSats)
	}
	return updateAffiliateUsageSatsNotion(ctx, usageID, savedSats, earnedSats)
}

// QueryAffiliateUsageByEmail issues a live query against AffiliateUsageDb
// filtering on the AffiliateEmail field.
func QueryAffiliateUsageByEmail(ctx *config.AppContext, email string) ([]*types.AffiliateUsage, error) {
	if UsePostgresBackend(ctx) {
		return queryAffiliateUsageByEmailPostgres(ctx, email)
	}
	return queryAffiliateUsageByEmailNotion(ctx, email)
}

// QueryAffiliateUsageByConf issues a live query against AffiliateUsageDb
// filtering on Conference == confTag.
func QueryAffiliateUsageByConf(ctx *config.AppContext, confTag string) ([]*types.AffiliateUsage, error) {
	if UsePostgresBackend(ctx) {
		return queryAffiliateUsageByConfPostgres(ctx, confTag)
	}
	return queryAffiliateUsageByConfNotion(ctx, confTag)
}

// AffiliateStatsTotals are the aggregate numbers shown on the dashboard's
// affiliate section.
type AffiliateStatsTotals struct {
	TicketsSold int
	SavedSats   int64
	EarnedSats  int64
}

// SumAffiliateStatsByEmail aggregates every AffiliateUsage row for an email.
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
