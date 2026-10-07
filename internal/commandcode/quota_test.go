package commandcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

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

// quotaFixtureHandler serves the four /alpha account endpoints with the values
// the reference dashboard shows (GOAT plan: 70 monthly, 29.36 left).
func quotaFixtureHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case EndpointWhoami:
			_, _ = w.Write([]byte(`{"user":{"id":"u1","name":"AnthonyandNancy","userName":"AnthonyandNancy"},"org":{"id":"org_123"}}`))
		case EndpointUsageSummary:
			_, _ = w.Write([]byte(`{"totalCount":14779,"completedCount":14779,"failedCount":19,"successRate":99.87,"totalCost":41.3353,"totalCredits":41.34,"totalTokensIn":2800000000,"totalTokensOut":15800000,"periodBasis":"billing-period"}`))
		case EndpointBillingCredits:
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":29.36,"purchasedCredits":0,"freeCredits":0,"planId":"individual-goat"},"windowLimits":{"fiveHour":{"used":2.18,"cap":14,"exceeded":false,"resetAt":1791341541000},"weekly":{"used":10.07,"cap":35,"exceeded":false,"resetAt":1791705406000}}}`))
		case EndpointBillingSubscriptions:
			if got := r.URL.Query().Get("orgId"); got != "org_123" {
				t.Errorf("orgId = %q, want org_123", got)
			}
			_, _ = w.Write([]byte(`{"data":{"planId":"individual-goat","status":"active","currentPeriodEnd":1792272000000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestFetchQuotaReportParsesAllEndpoints(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	server := httptest.NewServer(quotaFixtureHandler(t))
	defer server.Close()

	account := newTestAccount(server.URL)
	report, err := FetchQuotaReport(context.Background(), account, DefaultConfig())
	if err != nil {
		t.Fatalf("FetchQuotaReport() error = %v", err)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("Failures = %v, want none", report.Failures)
	}
	if report.Account.UserName != "AnthonyandNancy" {
		t.Errorf("account = %+v, want the whoami identity", report.Account)
	}
	if report.Plan.PlanID != "individual-goat" || report.Plan.Name != "GOAT" || report.Plan.Status != "active" {
		t.Errorf("plan = %+v, want GOAT/active", report.Plan)
	}
	if report.Plan.MonthlyCredits == nil || *report.Plan.MonthlyCredits != 70 {
		t.Errorf("plan.MonthlyCredits = %v, want 70", report.Plan.MonthlyCredits)
	}
	if report.Plan.CurrentPeriodEnd != 1792272000000 {
		t.Errorf("period end = %d, want 1792272000000", report.Plan.CurrentPeriodEnd)
	}
	if !report.Credits.MonthlyReported || report.Credits.MonthlyCredits != 29.36 {
		t.Errorf("credits = %+v, want 29.36 remaining and reported", report.Credits)
	}
	if report.Credits.FiveHour == nil || report.Credits.FiveHour.Used != 2.18 || report.Credits.FiveHour.Cap != 14 {
		t.Fatalf("fiveHour = %+v, want 2.18/14", report.Credits.FiveHour)
	}
	if report.Credits.FiveHour.ResetAtMS != 1791341541000 || report.Credits.FiveHour.Exceeded {
		t.Errorf("fiveHour = %+v, want reset 1791341541000 and not exceeded", report.Credits.FiveHour)
	}
	if report.Credits.Weekly == nil || report.Credits.Weekly.Used != 10.07 || report.Credits.Weekly.Cap != 35 {
		t.Fatalf("weekly = %+v, want 10.07/35", report.Credits.Weekly)
	}
	if report.Usage.CompletedCount != 14779 || report.Usage.FailedCount != 19 || report.Usage.SuccessRate != 99.87 {
		t.Errorf("usage = %+v, want the summary totals", report.Usage)
	}
}

func TestFetchQuotaReportPartialFailure(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EndpointBillingSubscriptions {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		quotaFixtureHandler(t)(w, r)
	}))
	defer server.Close()

	report, err := FetchQuotaReport(context.Background(), newTestAccount(server.URL), DefaultConfig())
	if err != nil {
		t.Fatalf("partial failure must still report: %v", err)
	}
	if len(report.Failures) != 1 || !strings.Contains(report.Failures[0], EndpointBillingSubscriptions) {
		t.Fatalf("Failures = %v, want the subscription endpoint only", report.Failures)
	}
	// The credits payload also carries the plan id, so the plan survives.
	if report.Plan.PlanID != "individual-goat" || report.Plan.MonthlyCredits == nil {
		t.Errorf("plan = %+v, want the credits fallback plan", report.Plan)
	}
}

func TestFetchQuotaReportAllFail(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	report, err := FetchQuotaReport(context.Background(), newTestAccount(server.URL), DefaultConfig())
	if err == nil {
		t.Fatal("FetchQuotaReport(all 401) error = nil, want an error")
	}
	if report.HasData() {
		t.Errorf("report = %+v, want no data", report)
	}
	if len(report.Failures) != 4 {
		t.Errorf("Failures = %v, want all four endpoints", report.Failures)
	}
}

func TestFetchQuotaReportCaches(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		quotaFixtureHandler(t)(w, r)
	}))
	defer server.Close()

	account := newTestAccount(server.URL)
	cfg := DefaultConfig()
	if _, err := FetchQuotaReport(context.Background(), account, cfg); err != nil {
		t.Fatalf("first FetchQuotaReport() error = %v", err)
	}
	if _, err := FetchQuotaReport(context.Background(), account, cfg); err != nil {
		t.Fatalf("cached FetchQuotaReport() error = %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != 4 {
		t.Errorf("upstream calls = %d, want 4 (second read served from cache)", got)
	}
}

func TestFetchQuotaReportUnknownPlan(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case EndpointWhoami:
			_, _ = w.Write([]byte(`{"user":{"name":"X"},"org":{"id":"o1"}}`))
		case EndpointUsageSummary:
			_, _ = w.Write([]byte(`{}`))
		case EndpointBillingCredits:
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":3},"windowLimits":{}}`))
		case EndpointBillingSubscriptions:
			_, _ = w.Write([]byte(`{"data":{"planId":"individual-mystery","status":"active"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	report, err := FetchQuotaReport(context.Background(), newTestAccount(server.URL), DefaultConfig())
	if err != nil {
		t.Fatalf("FetchQuotaReport() error = %v", err)
	}
	if report.Plan.Name != "individual-mystery" || report.Plan.MonthlyCredits != nil {
		t.Errorf("plan = %+v, want the raw id and no monthly total", report.Plan)
	}
	if !report.Credits.MonthlyReported || report.Credits.MonthlyCredits != 3 {
		t.Errorf("credits = %+v, want the reported balance", report.Credits)
	}
	if report.Credits.FiveHour != nil || report.Credits.Weekly != nil {
		t.Errorf("windows = %+v/%+v, want none when unreported", report.Credits.FiveHour, report.Credits.Weekly)
	}
}
