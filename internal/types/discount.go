package types

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDiscountExpr parses a discount expression string and populates the
// DiscountCode's parsed fields.
//
// Syntax:
//   %50           -> 50% off
//   $10           -> $10 off
//   $10:50        -> $10 off, max 50 uses
//   %50+1         -> 50% off a second ticket (BOGO)
//   =25           -> fixed price $25
//   =25:70        -> fixed price $25, max 70 uses
//   =100<20260519 -> fixed $100 until end of May 19, 2026
//   =100@20260519 -> fixed $100 only on May 19, 2026
//   =100@20260519-20260520 -> fixed $100 from May 19 to May 20, 2026
//   =100:50<20260519 -> fixed $100, max 50 uses OR until May 19, 2026
func (dc *DiscountCode) ParseDiscountExpr() error {
	expr := strings.TrimSpace(dc.Discount)
	if expr == "" {
		return fmt.Errorf("empty discount expression")
	}

	// Parse type prefix
	switch expr[0] {
	case '%', '$', '=':
		dc.DiscType = rune(expr[0])
	default:
		return fmt.Errorf("unknown discount type: %c", expr[0])
	}

	rest := expr[1:]

	// Parse date modifiers first (< or @) since they're at the end
	// and may contain digits that look like other modifiers
	if ltIdx := strings.Index(rest, "<"); ltIdx != -1 {
		dateStr := rest[ltIdx+1:]
		until, err := parseDate(dateStr)
		if err != nil {
			return fmt.Errorf("invalid date in %q: %w", expr, err)
		}
		// < means "until end of day"
		endOfDay := until.Add(24*time.Hour - time.Second)
		dc.ValidUntil = &endOfDay
		rest = rest[:ltIdx]
	} else if atIdx := strings.Index(rest, "@"); atIdx != -1 {
		dateStr := rest[atIdx+1:]
		if dashIdx := strings.Index(dateStr, "-"); dashIdx != -1 {
			// @YYYYMMDD-YYYYMMDD range
			fromStr := dateStr[:dashIdx]
			toStr := dateStr[dashIdx+1:]
			from, err := parseDate(fromStr)
			if err != nil {
				return fmt.Errorf("invalid start date in %q: %w", expr, err)
			}
			to, err := parseDate(toStr)
			if err != nil {
				return fmt.Errorf("invalid end date in %q: %w", expr, err)
			}
			// Start at beginning of from day
			startOfDay := from
			dc.ValidFrom = &startOfDay
			// End at end of to day
			endOfDay := to.Add(24*time.Hour - time.Second)
			dc.ValidUntil = &endOfDay
		} else {
			// @YYYYMMDD single day
			day, err := parseDate(dateStr)
			if err != nil {
				return fmt.Errorf("invalid date in %q: %w", expr, err)
			}
			startOfDay := day
			dc.ValidFrom = &startOfDay
			endOfDay := day.Add(24*time.Hour - time.Second)
			dc.ValidUntil = &endOfDay
		}
		rest = rest[:atIdx]
	}

	// Check for +N modifier (BOGO)
	if plusIdx := strings.Index(rest, "+"); plusIdx != -1 {
		extra, err := strconv.ParseUint(rest[plusIdx+1:], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid extra qty in %q: %w", expr, err)
		}
		dc.ExtraQty = uint(extra)
		rest = rest[:plusIdx]
	}

	// Check for :N modifier (max uses)
	if colonIdx := strings.Index(rest, ":"); colonIdx != -1 {
		limit, err := strconv.ParseUint(rest[colonIdx+1:], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid max uses in %q: %w", expr, err)
		}
		dc.MaxUses = uint(limit)
		rest = rest[:colonIdx]
	}

	// Parse the amount
	amount, err := strconv.ParseUint(rest, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid amount in %q: %w", expr, err)
	}
	dc.Amount = uint(amount)

	return nil
}

// parseDate parses a YYYYMMDD string into a time.Time at midnight UTC.
func parseDate(s string) (time.Time, error) {
	return time.Parse("20060102", s)
}

// ApplyDiscount calculates the per-ticket price after applying this discount.
func (dc *DiscountCode) ApplyDiscount(ticketPrice uint) uint {
	switch dc.DiscType {
	case '%':
		return ticketPrice * (100 - dc.Amount) / 100
	case '$':
		if dc.Amount >= ticketPrice {
			return 0
		}
		return ticketPrice - dc.Amount
	case '=':
		return dc.Amount
	}
	return ticketPrice
}

// CalcTotal calculates the total price for a given ticket count.
func (dc *DiscountCode) CalcTotal(ticketPrice, count uint) uint {
	if dc.ExtraQty > 0 {
		// BOGO: for every 1 full-price ticket, ExtraQty tickets at discount
		groupSize := 1 + dc.ExtraQty
		fullGroups := count / groupSize
		remainder := count % groupSize

		discountedPrice := dc.ApplyDiscount(ticketPrice)

		total := fullGroups * (ticketPrice + dc.ExtraQty*discountedPrice)
		// remainder tickets are full price
		total += remainder * ticketPrice
		return total
	}

	// Simple discount per ticket
	perTicket := dc.ApplyDiscount(ticketPrice)
	return perTicket * count
}

// IsExpired returns true if the code has reached its usage limit
// or is outside its valid date range.
func (dc *DiscountCode) IsExpired() bool {
	if dc.MaxUses > 0 && dc.UsesCount >= dc.MaxUses {
		return true
	}
	return dc.IsDateExpired(time.Now().UTC())
}

// IsDateExpired checks if the discount is outside its valid date range.
func (dc *DiscountCode) IsDateExpired(now time.Time) bool {
	if dc.ValidFrom != nil && now.Before(*dc.ValidFrom) {
		return true
	}
	if dc.ValidUntil != nil && now.After(*dc.ValidUntil) {
		return true
	}
	return false
}

// UsesRemaining returns how many uses are left, or 0 if unlimited.
func (dc *DiscountCode) UsesRemaining() uint {
	if dc.MaxUses == 0 {
		return 0
	}
	if dc.UsesCount >= dc.MaxUses {
		return 0
	}
	return dc.MaxUses - dc.UsesCount
}
