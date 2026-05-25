package handlers

import "testing"

func TestBuildDiscountExpr(t *testing.T) {
	tests := []struct {
		name string
		form DiscountForm
		want string
	}{
		{
			name: "percent with max and date range",
			form: DiscountForm{
				CodeName:     "OSS",
				DiscountType: "percent",
				Amount:       "20",
				MaxAllowed:   "50",
				ValidFrom:    "2026-05-19",
				ExpiresAt:    "2026-05-22",
			},
			want: "%20:50@20260519-20260522",
		},
		{
			name: "dollars with expiry",
			form: DiscountForm{
				CodeName:     "TENOFF",
				DiscountType: "dollars",
				Amount:       "10",
				ExpiresAt:    "2026-05-22",
			},
			want: "$10<20260522",
		},
		{
			name: "start only",
			form: DiscountForm{
				CodeName:     "LATE",
				DiscountType: "percent",
				Amount:       "15",
				ValidFrom:    "2026-05-19",
			},
			want: "%15@20260519-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDiscountExpr(tt.form)
			if err != nil {
				t.Fatalf("buildDiscountExpr: %v", err)
			}
			if got != tt.want {
				t.Fatalf("buildDiscountExpr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildDiscountExprValidation(t *testing.T) {
	tests := []struct {
		name string
		form DiscountForm
	}{
		{
			name: "bad percent",
			form: DiscountForm{CodeName: "BAD", DiscountType: "percent", Amount: "101"},
		},
		{
			name: "bad date order",
			form: DiscountForm{
				CodeName:     "BAD",
				DiscountType: "dollars",
				Amount:       "10",
				ValidFrom:    "2026-05-22",
				ExpiresAt:    "2026-05-19",
			},
		},
		{
			name: "bad affiliate email",
			form: DiscountForm{
				CodeName:       "BAD",
				DiscountType:   "dollars",
				Amount:         "10",
				AffiliateEmail: "not-email",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildDiscountExpr(tt.form); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
