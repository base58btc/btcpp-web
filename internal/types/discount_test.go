package types

import (
	"testing"
	"time"
)

func TestParseDiscountExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantType rune
		wantAmt  uint
		wantMax  uint
		wantExtra uint
		wantErr  bool
	}{
		{"percent 50", "%50", '%', 50, 0, 0, false},
		{"percent 100", "%100", '%', 100, 0, 0, false},
		{"dollar 10", "$10", '$', 10, 0, 0, false},
		{"dollar with limit", "$10:50", '$', 10, 50, 0, false},
		{"percent bogo", "%50+1", '%', 50, 0, 1, false},
		{"fixed price", "=25", '=', 25, 0, 0, false},
		{"fixed price with limit", "=25:70", '=', 25, 70, 0, false},
		{"percent with limit", "%20:100", '%', 20, 100, 0, false},
		{"dollar bogo", "$5+2", '$', 5, 0, 2, false},
		{"empty expr", "", 0, 0, 0, 0, true},
		{"bad prefix", "x50", 0, 0, 0, 0, true},
		{"bad amount", "%abc", 0, 0, 0, 0, true},
		{"bad limit", "$10:abc", 0, 0, 0, 0, true},
		{"bad extra", "%50+abc", 0, 0, 0, 0, true},
		// Date modifiers parse without error (dates checked separately)
		{"until date", "=100<20260519", '=', 100, 0, 0, false},
		{"on date", "=100@20260519", '=', 100, 0, 0, false},
		{"date range", "=100@20260519-20260520", '=', 100, 0, 0, false},
		{"limit and date", "=100:50<20260519", '=', 100, 50, 0, false},
		{"percent until date", "%50<20260519", '%', 50, 0, 0, false},
		{"bad date", "=100<notadate", 0, 0, 0, 0, true},
		{"bad range end", "=100@20260519-notadate", 0, 0, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{Discount: tt.expr}
			err := dc.ParseDiscountExpr()

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tt.expr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %s", tt.expr, err)
			}

			if dc.DiscType != tt.wantType {
				t.Errorf("DiscType: got %c, want %c", dc.DiscType, tt.wantType)
			}
			if dc.Amount != tt.wantAmt {
				t.Errorf("Amount: got %d, want %d", dc.Amount, tt.wantAmt)
			}
			if dc.MaxUses != tt.wantMax {
				t.Errorf("MaxUses: got %d, want %d", dc.MaxUses, tt.wantMax)
			}
			if dc.ExtraQty != tt.wantExtra {
				t.Errorf("ExtraQty: got %d, want %d", dc.ExtraQty, tt.wantExtra)
			}
		})
	}
}

func TestApplyDiscount(t *testing.T) {
	tests := []struct {
		name        string
		expr        string
		ticketPrice uint
		want        uint
	}{
		{"50% off $100", "%50", 100, 50},
		{"50% off $99", "%50", 99, 49},
		{"100% off", "%100", 100, 0},
		{"0% off", "%0", 100, 100},
		{"$10 off $100", "$10", 100, 90},
		{"$10 off $5 (floor 0)", "$10", 5, 0},
		{"$0 off", "$0", 100, 100},
		{"fixed $25", "=25", 100, 25},
		{"fixed $25 (cheap ticket)", "=25", 10, 25},
		{"fixed $0", "=0", 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{Discount: tt.expr}
			if err := dc.ParseDiscountExpr(); err != nil {
				t.Fatalf("parse error: %s", err)
			}
			got := dc.ApplyDiscount(tt.ticketPrice)
			if got != tt.want {
				t.Errorf("ApplyDiscount(%d) = %d, want %d", tt.ticketPrice, got, tt.want)
			}
		})
	}
}

func TestCalcTotal(t *testing.T) {
	tests := []struct {
		name        string
		expr        string
		ticketPrice uint
		count       uint
		want        uint
	}{
		// Simple percentage
		{"50% off, 1 ticket", "%50", 100, 1, 50},
		{"50% off, 3 tickets", "%50", 100, 3, 150},

		// Simple dollar off
		{"$10 off, 1 ticket", "$10", 100, 1, 90},
		{"$10 off, 3 tickets", "$10", 100, 3, 270},

		// Fixed price
		{"=25, 1 ticket", "=25", 100, 1, 25},
		{"=25, 4 tickets", "=25", 100, 4, 100},

		// BOGO: %50+1 means buy 1 full, get 1 at 50% off
		{"bogo 50%+1, 1 ticket (remainder=full)", "%50+1", 100, 1, 100},
		{"bogo 50%+1, 2 tickets (1 full + 1 half)", "%50+1", 100, 2, 150},
		{"bogo 50%+1, 3 tickets (1 group + 1 remainder full)", "%50+1", 100, 3, 250},
		{"bogo 50%+1, 4 tickets (2 full + 2 half)", "%50+1", 100, 4, 300},

		// BOGO: %100+1 means buy 1 get 1 free
		{"bogo free+1, 2 tickets", "%100+1", 100, 2, 100},
		{"bogo free+1, 4 tickets", "%100+1", 100, 4, 200},
		{"bogo free+1, 3 tickets", "%100+1", 100, 3, 200},

		// BOGO: $20+1 means buy 1 full, get 1 at $20 off
		{"bogo $20+1, 2 tickets", "$20+1", 100, 2, 180},
		{"bogo $20+1, 4 tickets", "$20+1", 100, 4, 360},

		// BOGO with +2: buy 1 full, get 2 at discount
		{"bogo 50%+2, 3 tickets (1 full + 2 half)", "%50+2", 100, 3, 200},
		{"bogo 50%+2, 4 tickets (1 full + 2 half + 1 full)", "%50+2", 100, 4, 300},
		{"bogo 50%+2, 6 tickets (2 full + 4 half)", "%50+2", 100, 6, 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{Discount: tt.expr}
			if err := dc.ParseDiscountExpr(); err != nil {
				t.Fatalf("parse error: %s", err)
			}
			got := dc.CalcTotal(tt.ticketPrice, tt.count)
			if got != tt.want {
				t.Errorf("CalcTotal(%d, %d) = %d, want %d", tt.ticketPrice, tt.count, got, tt.want)
			}
		})
	}
}

func TestIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		maxUses   uint
		usesCount uint
		want      bool
	}{
		{"unlimited", 0, 100, false},
		{"under limit", 50, 30, false},
		{"at limit", 50, 50, true},
		{"over limit", 50, 60, true},
		{"zero uses, has limit", 10, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{MaxUses: tt.maxUses, UsesCount: tt.usesCount}
			if got := dc.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUsesRemaining(t *testing.T) {
	tests := []struct {
		name      string
		maxUses   uint
		usesCount uint
		want      uint
	}{
		{"unlimited returns 0", 0, 50, 0},
		{"30 of 50 used", 50, 30, 20},
		{"all used", 50, 50, 0},
		{"over used", 50, 60, 0},
		{"none used", 70, 0, 70},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{MaxUses: tt.maxUses, UsesCount: tt.usesCount}
			if got := dc.UsesRemaining(); got != tt.want {
				t.Errorf("UsesRemaining() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseDateModifiers(t *testing.T) {
	t.Run("until date (<)", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100<20260519"}
		if err := dc.ParseDiscountExpr(); err != nil {
			t.Fatal(err)
		}
		if dc.ValidFrom != nil {
			t.Error("ValidFrom should be nil for < modifier")
		}
		if dc.ValidUntil == nil {
			t.Fatal("ValidUntil should not be nil")
		}
		// Should be end of May 19
		expected := time.Date(2026, 5, 19, 23, 59, 59, 0, time.UTC)
		if !dc.ValidUntil.Equal(expected) {
			t.Errorf("ValidUntil = %v, want %v", dc.ValidUntil, expected)
		}
	})

	t.Run("on date (@single)", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100@20260519"}
		if err := dc.ParseDiscountExpr(); err != nil {
			t.Fatal(err)
		}
		if dc.ValidFrom == nil {
			t.Fatal("ValidFrom should not be nil")
		}
		if dc.ValidUntil == nil {
			t.Fatal("ValidUntil should not be nil")
		}
		expectedFrom := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
		expectedUntil := time.Date(2026, 5, 19, 23, 59, 59, 0, time.UTC)
		if !dc.ValidFrom.Equal(expectedFrom) {
			t.Errorf("ValidFrom = %v, want %v", dc.ValidFrom, expectedFrom)
		}
		if !dc.ValidUntil.Equal(expectedUntil) {
			t.Errorf("ValidUntil = %v, want %v", dc.ValidUntil, expectedUntil)
		}
	})

	t.Run("date range (@from-to)", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100@20260519-20260520"}
		if err := dc.ParseDiscountExpr(); err != nil {
			t.Fatal(err)
		}
		if dc.ValidFrom == nil || dc.ValidUntil == nil {
			t.Fatal("both ValidFrom and ValidUntil should be set")
		}
		expectedFrom := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
		expectedUntil := time.Date(2026, 5, 20, 23, 59, 59, 0, time.UTC)
		if !dc.ValidFrom.Equal(expectedFrom) {
			t.Errorf("ValidFrom = %v, want %v", dc.ValidFrom, expectedFrom)
		}
		if !dc.ValidUntil.Equal(expectedUntil) {
			t.Errorf("ValidUntil = %v, want %v", dc.ValidUntil, expectedUntil)
		}
	})

	t.Run("limit and date combined", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100:50<20260519"}
		if err := dc.ParseDiscountExpr(); err != nil {
			t.Fatal(err)
		}
		if dc.Amount != 100 {
			t.Errorf("Amount = %d, want 100", dc.Amount)
		}
		if dc.MaxUses != 50 {
			t.Errorf("MaxUses = %d, want 50", dc.MaxUses)
		}
		if dc.ValidUntil == nil {
			t.Fatal("ValidUntil should not be nil")
		}
		expected := time.Date(2026, 5, 19, 23, 59, 59, 0, time.UTC)
		if !dc.ValidUntil.Equal(expected) {
			t.Errorf("ValidUntil = %v, want %v", dc.ValidUntil, expected)
		}
	})
}

func TestIsDateExpired(t *testing.T) {
	may19Start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	may19Mid := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	may19End := time.Date(2026, 5, 19, 23, 59, 59, 0, time.UTC)
	may20Mid := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	may18Mid := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		expr string
		now  time.Time
		want bool
	}{
		// =100<20260519 -> valid until end of May 19
		{"before deadline", "=100<20260519", may18Mid, false},
		{"on deadline day", "=100<20260519", may19Mid, false},
		{"at end of deadline", "=100<20260519", may19End, false},
		{"after deadline", "=100<20260519", may20Mid, true},

		// =100@20260519 -> valid only on May 19
		{"before single day", "=100@20260519", may18Mid, true},
		{"on single day start", "=100@20260519", may19Start, false},
		{"on single day mid", "=100@20260519", may19Mid, false},
		{"on single day end", "=100@20260519", may19End, false},
		{"after single day", "=100@20260519", may20Mid, true},

		// =100@20260519-20260520 -> valid May 19-20
		{"before range", "=100@20260519-20260520", may18Mid, true},
		{"in range day 1", "=100@20260519-20260520", may19Mid, false},
		{"in range day 2", "=100@20260519-20260520", may20Mid, false},
		{"after range", "=100@20260519-20260520", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC), true},

		// No date constraint
		{"no date, any time", "%50", may19Mid, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &DiscountCode{Discount: tt.expr}
			if err := dc.ParseDiscountExpr(); err != nil {
				t.Fatalf("parse error: %s", err)
			}
			if got := dc.IsDateExpired(tt.now); got != tt.want {
				t.Errorf("IsDateExpired(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestIsExpiredCombined(t *testing.T) {
	t.Run("uses exhausted but date valid", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100:50<20260519", UsesCount: 50}
		dc.ParseDiscountExpr()
		if !dc.IsExpired() {
			t.Error("should be expired (uses exhausted)")
		}
	})

	t.Run("uses ok but date passed", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100:50<20200101", UsesCount: 10}
		dc.ParseDiscountExpr()
		if !dc.IsExpired() {
			t.Error("should be expired (date passed)")
		}
	})

	t.Run("both ok", func(t *testing.T) {
		dc := &DiscountCode{Discount: "=100:50<20300101", UsesCount: 10}
		dc.ParseDiscountExpr()
		if dc.IsExpired() {
			t.Error("should not be expired")
		}
	})
}
