// recalc-affiliate-usage repairs historical AffiliateUsage sats splits.
//
// Existing rows do not store checkout IDs, currency, or fiat amounts, so this
// command cannot replay the original checkout exactly. Instead, for affiliate
// percentage codes in the supported 0-20% range, it treats the existing
// SavedSats+EarnedSats as the fixed 20% affiliate ceiling and reapportions it
// according to the current discount expression:
//
//	%20 => 100% saved, 0% earned
//	%15 => 75% saved, 25% earned
//	%0  => 0% saved, 100% earned
//
// Fixed-dollar and exact-price discounts are skipped because their split
// depends on the original checkout amount.
package main

import (
	"flag"
	"log"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
)

const configFile = "config.toml"

type cfgFile struct {
	Notion struct {
		Token            string `toml:"token"`
		DiscountsDb      string `toml:"discountsdb"`
		AffiliateUsageDb string `toml:"affiliateusagedb"`
	} `toml:"notion"`
}

func main() {
	write := flag.Bool("write", false, "Apply updates to Notion. Without this flag, only logs the proposed changes.")
	codeFilter := flag.String("code", "", "Only process this discount code")
	confFilter := flag.String("conf", "", "Only process this conference tag")
	emailFilter := flag.String("email", "", "Only process this affiliate email")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.DiscountsDb == "" || c.Notion.AffiliateUsageDb == "" {
		log.Fatalf("missing notion.token / discountsdb / affiliateusagedb in %s", configFile)
	}

	n := &types.Notion{Config: &types.NotionConfig{
		Token:            c.Notion.Token,
		DiscountsDb:      c.Notion.DiscountsDb,
		AffiliateUsageDb: c.Notion.AffiliateUsageDb,
	}}
	n.Setup(c.Notion.Token)
	ctx := &config.AppContext{Notion: n}

	discounts, err := getters.ListDiscounts(n)
	if err != nil {
		log.Fatalf("list discounts: %s", err)
	}
	discountByCode := make(map[string]*types.DiscountCode, len(discounts))
	for _, d := range discounts {
		if d == nil || d.CodeName == "" {
			continue
		}
		discountByCode[strings.ToUpper(d.CodeName)] = d
	}

	rows, err := getters.ListAffiliateUsage(ctx)
	if err != nil {
		log.Fatalf("list affiliate usage: %s", err)
	}
	log.Printf("loaded %d AffiliateUsage rows and %d discounts", len(rows), len(discountByCode))
	if !*write {
		log.Printf("dry run only; pass -write to update Notion")
	}

	var scanned, changed, unchanged, skipped, failed int
	for _, row := range rows {
		if row == nil {
			continue
		}
		if !matches(*codeFilter, row.CodeName) || !matches(*confFilter, row.ConfTag) || !matches(*emailFilter, row.AffiliateEmail) {
			continue
		}
		scanned++

		discount := discountByCode[strings.ToUpper(row.CodeName)]
		if discount == nil {
			skipped++
			log.Printf("skip %s code=%q: discount not found", shortID(row.ID), row.CodeName)
			continue
		}
		if discount.AffiliateEmail == "" {
			skipped++
			log.Printf("skip %s code=%s: discount has no affiliate email", shortID(row.ID), row.CodeName)
			continue
		}
		if discount.DiscType != '%' || discount.Amount > 20 {
			skipped++
			log.Printf("skip %s code=%s discount=%q: only %%0..%%20 affiliate codes are repairable", shortID(row.ID), row.CodeName, discount.Discount)
			continue
		}

		newSaved, newEarned := recalcSplit(row.SavedSats, row.EarnedSats, discount.Amount)
		if newSaved == row.SavedSats && newEarned == row.EarnedSats {
			unchanged++
			continue
		}

		changed++
		log.Printf("%s %s code=%s discount=%s created=%s saved %d→%d earned %d→%d",
			mode(*write), shortID(row.ID), row.CodeName, discount.Discount, formatTime(row.Created),
			row.SavedSats, newSaved, row.EarnedSats, newEarned)
		if !*write {
			continue
		}
		if err := getters.UpdateAffiliateUsageSats(ctx, row.ID, newSaved, newEarned); err != nil {
			failed++
			log.Printf("  FAILED %s: %s", shortID(row.ID), err)
			continue
		}
		time.Sleep(350 * time.Millisecond)
	}

	log.Printf("done scanned=%d changed=%d unchanged=%d skipped=%d failed=%d", scanned, changed, unchanged, skipped, failed)
	if failed > 0 {
		log.Fatalf("failed to update %d rows", failed)
	}
}

func recalcSplit(savedSats, earnedSats int64, buyerPct uint) (int64, int64) {
	ceiling := savedSats + earnedSats
	if ceiling <= 0 {
		return 0, 0
	}
	newSaved := ceiling * int64(buyerPct) / 20
	return newSaved, ceiling - newSaved
}

func matches(filter, value string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(filter), strings.TrimSpace(value))
}

func mode(write bool) string {
	if write {
		return "update"
	}
	return "would update"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}
