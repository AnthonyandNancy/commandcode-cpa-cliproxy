package commandcode

import "testing"

func TestSubscriptionPlanInfoLongestPrefix(t *testing.T) {
	cases := []struct {
		planID string
		name   string
		month  float64
		ok     bool
	}{
		{"individual-goat", "GOAT", 70, true},
		{"individual-go", "Go", 10, true},
		{"individual-pro-v1", "Pro", 80, true},
		{"individual-pro", "Pro", 30, true},
		{"individual-provider", "Provider", 15, true},
		{"individual-max", "Max", 150, true},
		{"individual-ultra", "Ultra", 300, true},
		{"teams-pro", "Teams Pro", 40, true},
		{"individual-unknown", "", 0, false},
		{"", "", 0, false},
	}
	for _, tc := range cases {
		name, month, ok := subscriptionPlanInfo(tc.planID)
		if ok != tc.ok || name != tc.name || month != tc.month {
			t.Errorf("subscriptionPlanInfo(%q) = (%q, %v, %v), want (%q, %v, %v)",
				tc.planID, name, month, ok, tc.name, tc.month, tc.ok)
		}
	}
}

func TestQuotaPercentAndFraction(t *testing.T) {
	if got := quotaPercent(40.64, 70); got != 58 {
		t.Errorf("quotaPercent(40.64, 70) = %d, want 58", got)
	}
	if got := quotaPercent(20, 10); got != 200 {
		t.Errorf("quotaPercent over quota = %d, want 200 (not clamped)", got)
	}
	if got := quotaPercent(1, 0); got != 0 {
		t.Errorf("quotaPercent with no cap = %d, want 0", got)
	}
	if got := clampFraction(1.4); got != 1 {
		t.Errorf("clampFraction(1.4) = %v, want 1", got)
	}
	if got := clampFraction(-0.2); got != 0 {
		t.Errorf("clampFraction(-0.2) = %v, want 0", got)
	}
	if got := periodEndMillis(float64(1792272000000)); got != 1792272000000 {
		t.Errorf("periodEndMillis(millis) = %d", got)
	}
	if got := periodEndMillis("2026-10-19T00:00:00Z"); got == 0 {
		t.Error("periodEndMillis(RFC3339) = 0, want millis")
	}
	if got := periodEndMillis("nope"); got != 0 {
		t.Errorf("periodEndMillis(garbage) = %d, want 0", got)
	}
}
