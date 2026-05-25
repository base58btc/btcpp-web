package handlers

import "testing"

func TestAffiliateMath(t *testing.T) {
	tests := []struct {
		name        string
		preDiscount int64
		count       int64
		paid        int64
		wantSaved   int64
		wantEarned  int64
	}{
		{
			// User-reported regression: $65 ticket, 10% off → buyer
			// pays $58.50, but bug had buyer at $58 because of an
			// unrelated rounding path. The fix here is that pre-
			// discount per-ticket = 6500 cents, not 5800. Saved =
			// 700 ($7), earned = 600 ($6) out of the 1300 ceiling.
			name:        "10pct_65_ticket",
			preDiscount: 6500,
			count:       1,
			paid:        5800,
			wantSaved:   700,
			wantEarned:  600,
		},
		{
			// Silent (%0) code: buyer pays full price, affiliate
			// pockets the entire 20% ceiling.
			name:        "silent_pct_zero",
			preDiscount: 10000,
			count:       1,
			paid:        10000,
			wantSaved:   0,
			wantEarned:  2000,
		},
		{
			// Max (%20) code: buyer takes the whole ceiling,
			// affiliate earns nothing. This is intentionally checked
			// in fiat cents before BTC conversion so a clean 20%
			// discount cannot leave an EarnedSats remainder.
			name:        "buyer_takes_full_ceiling",
			preDiscount: 10000,
			count:       1,
			paid:        8000,
			wantSaved:   2000,
			wantEarned:  0,
		},
		{
			// Mid-slider %15 over multiple tickets: ceiling scales
			// with count.
			name:        "fifteen_pct_three_tickets",
			preDiscount: 10000,
			count:       3,
			paid:        25500, // 3 × 100 × 0.85
			wantSaved:   4500,
			wantEarned:  1500, // 3 × 2000 ceiling = 6000; 6000 - 4500
		},
		{
			// Defensive: paidTotal > original (shouldn't happen, but
			// rounding / tax weirdness could push it). Saved floored
			// at 0; earned uses the full ceiling.
			name:        "overpaid_floors_at_zero",
			preDiscount: 10000,
			count:       1,
			paid:        10500,
			wantSaved:   0,
			wantEarned:  2000,
		},
		{
			// Defensive: a discount deeper than 20% (e.g. an admin
			// %25 code with an AffiliateEmail attached). Earned
			// floors at 0, no negative payouts.
			name:        "deeper_than_ceiling_floors_earned",
			preDiscount: 10000,
			count:       1,
			paid:        7000,
			wantSaved:   3000,
			wantEarned:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSaved, gotEarned := affiliateMath(tt.preDiscount, tt.count, tt.paid)
			if gotSaved != tt.wantSaved {
				t.Errorf("savedCents = %d, want %d", gotSaved, tt.wantSaved)
			}
			if gotEarned != tt.wantEarned {
				t.Errorf("earnedCents = %d, want %d", gotEarned, tt.wantEarned)
			}
		})
	}
}
