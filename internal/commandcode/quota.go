package commandcode

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// quotaCacheTTL bounds how often the four /alpha account endpoints are queried.
// The account page and CPAMP credential management both refresh on demand, so a
// short cache keeps a refresh loop from hammering upstream.
const quotaCacheTTL = 60 * time.Second

// quotaTimeout caps one report fetch even when request-timeout is unlimited
// (0 = no limit), because the page and the management panel wait for it.
const quotaTimeout = 30 * time.Second

// subscriptionPlans maps a subscription planId to its display name and the
// plan's monthly credit allowance. Prefixes are matched longest-first, so a
// longer id must stay above every shorter id it starts with (individual-goat
// before individual-go, individual-pro-v1 before individual-pro). The values
// mirror the official CLI's plan map, the same table the reference provider
// (dsh-commandcode-provider 0.12.0) ships.
var subscriptionPlans = []struct {
	Prefix         string
	Name           string
	MonthlyCredits float64
}{
	{Prefix: "individual-provider", Name: "Provider", MonthlyCredits: 15},
	{Prefix: "individual-ultra", Name: "Ultra", MonthlyCredits: 300},
	{Prefix: "individual-goat", Name: "GOAT", MonthlyCredits: 70},
	{Prefix: "individual-max", Name: "Max", MonthlyCredits: 150},
	{Prefix: "individual-pro-v1", Name: "Pro", MonthlyCredits: 80},
	{Prefix: "individual-pro", Name: "Pro", MonthlyCredits: 30},
	{Prefix: "individual-go", Name: "Go", MonthlyCredits: 10},
	{Prefix: "teams-pro", Name: "Teams Pro", MonthlyCredits: 40},
}

// subscriptionPlanInfo resolves a subscription planId to its display name and
// monthly credits, mirroring the CLI's longest-prefix rule (lowercase,
// underscores folded to dashes). Unknown ids report ok=false so callers keep
// the raw id and render no monthly ratio.
func subscriptionPlanInfo(planID string) (name string, monthlyCredits float64, ok bool) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(planID), "_", "-"))
	if normalized == "" {
		return "", 0, false
	}
	for _, entry := range subscriptionPlans {
		if strings.HasPrefix(normalized, entry.Prefix) {
			return entry.Name, entry.MonthlyCredits, true
		}
	}
	return "", 0, false
}

// recordValue returns the JSON object behind one decoded value.
func recordValue(value any) map[string]any {
	doc, _ := value.(map[string]any)
	return doc
}

// floatValue reads a JSON number (or numeric string) as float64.
func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return parsed, true
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// floatValueOr is floatValue with a fallback, used for shape stability: the
// *_Reported flags in QuotaCredits tell a missing field from a real zero.
func floatValueOr(value any, fallback float64) float64 {
	if parsed, ok := floatValue(value); ok {
		return parsed
	}
	return fallback
}

// periodEndMillis normalizes currentPeriodEnd: epoch millis, or an ISO date.
func periodEndMillis(value any) int64 {
	if parsed, ok := floatValue(value); ok {
		return int64(parsed)
	}
	text := strings.TrimSpace(stringValue(value))
	if text == "" {
		return 0
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.UnixMilli()
	}
	if parsed, err := time.Parse("2006-01-02", text); err == nil {
		return parsed.UnixMilli()
	}
	return 0
}

// quotaPercent is consumption in percent units, deliberately NOT clamped: an
// over-quota window reports its real overshoot (150%). Bar widths clamp in the
// renderers.
func quotaPercent(used, cap float64) int {
	if cap <= 0 {
		return 0
	}
	return int(math.Round(used / cap * 100))
}

// clampFraction renders a [0,1] ratio for the host's quota buckets.
func clampFraction(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// rfc3339Millis renders epoch millis as RFC3339, empty when unknown.
func rfc3339Millis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// QuotaWindow is one metered rolling window from /alpha/billing/credits.
type QuotaWindow struct {
	Used      float64
	Cap       float64
	Exceeded  bool
	ResetAtMS int64
}

// QuotaAccount is the whoami identity behind one credential.
type QuotaAccount struct {
	Name     string
	UserName string
}

// QuotaPlan is the subscription state from /alpha/billing/subscriptions.
type QuotaPlan struct {
	PlanID           string
	Name             string
	Status           string
	MonthlyCredits   *float64 // nil when the plan id is unknown
	CurrentPeriodEnd int64    // epoch millis, 0 when unreported
}

// QuotaCredits is the balance/window state from /alpha/billing/credits. The
// *_Reported flags separate "the endpoint said zero" from "the endpoint said
// nothing": rendering a missing balance as 0 would claim "nothing left".
type QuotaCredits struct {
	MonthlyCredits    float64
	MonthlyReported   bool
	PurchasedCredits  float64
	PurchasedReported bool
	FreeCredits       float64
	FreeReported      bool
	FiveHour          *QuotaWindow
	Weekly            *QuotaWindow
}

// QuotaUsage is the totals from /alpha/usage/summary.
type QuotaUsage struct {
	Reported       bool
	TotalCount     float64
	CompletedCount float64
	FailedCount    float64
	SuccessRate    float64 // already percent units (99.87)
	TotalCost      float64
	TotalCredits   float64
	TotalTokensIn  float64
	TotalTokensOut float64
	PeriodBasis    string
}

// QuotaReport is everything the four account endpoints answered for one
// credential. Failures lists the endpoints that did not answer; a partial
// report is normal, only a report with no usable endpoint is an error.
type QuotaReport struct {
	Account  QuotaAccount
	Plan     QuotaPlan
	Credits  QuotaCredits
	Usage    QuotaUsage
	Failures []string
}

// reported reports whether the billing endpoint answered anything usable.
func (c QuotaCredits) reported() bool {
	return c.MonthlyReported || c.PurchasedReported || c.FreeReported || c.FiveHour != nil || c.Weekly != nil
}

// HasData reports whether at least one endpoint answered.
func (r QuotaReport) HasData() bool {
	return r.Plan.PlanID != "" || r.Credits.reported() || r.Usage.Reported
}

// FetchQuotaReport reads one credential's subscription, credits, metered
// windows and usage totals from the /alpha account endpoints. whoami runs
// first because it yields the org id the subscription query needs; the other
// three run in parallel. A single dead endpoint is recorded in Failures and
// leaves the rest of the report usable; only a report with no usable endpoint
// at all is an error. The credential itself is never logged.
func FetchQuotaReport(ctx context.Context, account Account, cfg RuntimeConfig) (QuotaReport, error) {
	if report, ok := quotaCacheGet(account.APIKey); ok {
		return report, nil
	}
	if strings.TrimSpace(account.APIKey) == "" {
		return QuotaReport{}, fmt.Errorf("account has no api key")
	}
	profile := DeriveDevice(account.APIKey, account.DeviceSalt(cfg), account.ProjectDirValue(cfg), cfg.MasqueradePlatform, cfg.MasqueradeArch)
	client := newUpstreamClient(cfg)
	base := account.ResolvedBaseURL(cfg)
	if base == "" {
		return QuotaReport{}, fmt.Errorf("base url is empty")
	}
	headers := upstreamHeaders(account, profile, cfg, "", true)

	timeout := quotaTimeout
	if cfg.RequestTimeout > 0 && cfg.RequestTimeout < timeout {
		timeout = cfg.RequestTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	report := QuotaReport{Usage: QuotaUsage{PeriodBasis: "billing-period"}}
	whoami, errWhoami := quotaGetJSON(reqCtx, client, base, EndpointWhoami, headers)
	if errWhoami != nil {
		report.Failures = append(report.Failures, EndpointWhoami+": "+errWhoami.Error())
	} else {
		report.Account = parseQuotaAccount(whoami)
	}

	subscriptionPath := EndpointBillingSubscriptions
	if orgID := parseQuotaOrgID(whoami); orgID != "" {
		subscriptionPath += "?orgId=" + url.QueryEscape(orgID)
	}

	var (
		mu           sync.Mutex
		wg           sync.WaitGroup
		usageDoc     map[string]any
		creditsDoc   map[string]any
		subscription map[string]any
	)
	fetch := func(path string, target *map[string]any) {
		defer wg.Done()
		doc, errGet := quotaGetJSON(reqCtx, client, base, path, headers)
		mu.Lock()
		defer mu.Unlock()
		if errGet != nil {
			report.Failures = append(report.Failures, path+": "+errGet.Error())
			return
		}
		*target = doc
	}
	wg.Add(3)
	go fetch(EndpointUsageSummary, &usageDoc)
	go fetch(EndpointBillingCredits, &creditsDoc)
	go fetch(subscriptionPath, &subscription)
	wg.Wait()

	report.Credits = parseQuotaCredits(creditsDoc)
	report.Usage = parseQuotaUsage(usageDoc)
	report.Plan = parseQuotaPlan(subscription, creditsDoc)
	if !report.HasData() {
		return report, fmt.Errorf("all CommandCode account endpoints failed")
	}
	quotaCachePut(account.APIKey, report)
	return report, nil
}

// quotaGetJSON performs one authenticated GET and decodes a JSON object. A
// non-2xx status or a body that is not a JSON object is an error the caller
// records and moves past; the API key stays in the header only.
func quotaGetJSON(ctx context.Context, client *http.Client, base, path string, headers map[string]string) (map[string]any, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if errRequest != nil {
		return nil, errRequest
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	var doc map[string]any
	if errDecode := json.NewDecoder(resp.Body).Decode(&doc); errDecode != nil {
		return nil, fmt.Errorf("invalid response body")
	}
	return doc, nil
}

func parseQuotaAccount(doc map[string]any) QuotaAccount {
	user := recordValue(doc["user"])
	return QuotaAccount{Name: stringValue(user["name"]), UserName: stringValue(user["userName"])}
}

func parseQuotaOrgID(doc map[string]any) string {
	return stringValue(recordValue(doc["org"])["id"])
}

func parseQuotaPlan(subscription, credits map[string]any) QuotaPlan {
	sub := recordValue(subscription["data"])
	creditsData := recordValue(credits["credits"])
	planID := firstNonEmpty(stringValue(sub["planId"]), stringValue(creditsData["planId"]))
	if planID == "" && sub == nil {
		return QuotaPlan{}
	}
	plan := QuotaPlan{
		PlanID: planID,
		Name:   planID,
		Status: stringValue(sub["status"]),
	}
	if name, monthly, ok := subscriptionPlanInfo(planID); ok {
		plan.Name = name
		plan.MonthlyCredits = &monthly
	}
	plan.CurrentPeriodEnd = periodEndMillis(sub["currentPeriodEnd"])
	return plan
}

func parseQuotaCredits(doc map[string]any) QuotaCredits {
	credits := recordValue(doc["credits"])
	windowLimits := recordValue(doc["windowLimits"])
	out := QuotaCredits{
		MonthlyCredits:   floatValueOr(credits["monthlyCredits"], 0),
		PurchasedCredits: floatValueOr(credits["purchasedCredits"], 0),
		FreeCredits:      floatValueOr(credits["freeCredits"], 0),
	}
	if _, ok := floatValue(credits["monthlyCredits"]); ok {
		out.MonthlyReported = true
	}
	if _, ok := floatValue(credits["purchasedCredits"]); ok {
		out.PurchasedReported = true
	}
	if _, ok := floatValue(credits["freeCredits"]); ok {
		out.FreeReported = true
	}
	out.FiveHour = parseQuotaWindow(windowLimits["fiveHour"])
	out.Weekly = parseQuotaWindow(windowLimits["weekly"])
	return out
}

// parseQuotaWindow returns nil for an unreported window: absence is NOT "a
// window with no cap", and only the un-reported case must keep a row off the UI.
func parseQuotaWindow(value any) *QuotaWindow {
	doc := recordValue(value)
	if doc == nil {
		return nil
	}
	return &QuotaWindow{
		Used:      floatValueOr(doc["used"], 0),
		Cap:       floatValueOr(doc["cap"], 0),
		Exceeded:  doc["exceeded"] == true,
		ResetAtMS: int64(floatValueOr(doc["resetAt"], 0)),
	}
}

func parseQuotaUsage(doc map[string]any) QuotaUsage {
	if doc == nil {
		return QuotaUsage{}
	}
	return QuotaUsage{
		Reported:       true,
		TotalCount:     floatValueOr(doc["totalCount"], 0),
		CompletedCount: floatValueOr(doc["completedCount"], 0),
		FailedCount:    floatValueOr(doc["failedCount"], 0),
		SuccessRate:    floatValueOr(doc["successRate"], 0),
		TotalCost:      floatValueOr(doc["totalCost"], 0),
		TotalCredits:   floatValueOr(doc["totalCredits"], 0),
		TotalTokensIn:  floatValueOr(doc["totalTokensIn"], 0),
		TotalTokensOut: floatValueOr(doc["totalTokensOut"], 0),
		PeriodBasis:    firstNonEmpty(stringValue(doc["periodBasis"]), "billing-period"),
	}
}

type quotaCacheEntry struct {
	report    QuotaReport
	expiresAt time.Time
}

var (
	quotaCacheMu sync.Mutex
	quotaCache   = map[string]quotaCacheEntry{}
)

func quotaCacheGet(apiKey string) (QuotaReport, bool) {
	key := KeyHash(apiKey)
	if key == "" {
		return QuotaReport{}, false
	}
	quotaCacheMu.Lock()
	defer quotaCacheMu.Unlock()
	entry, ok := quotaCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return QuotaReport{}, false
	}
	return entry.report, true
}

func quotaCachePut(apiKey string, report QuotaReport) {
	key := KeyHash(apiKey)
	if key == "" {
		return
	}
	quotaCacheMu.Lock()
	quotaCache[key] = quotaCacheEntry{report: report, expiresAt: time.Now().Add(quotaCacheTTL)}
	quotaCacheMu.Unlock()
}

// resetQuotaCache clears the cached reports (tests and credential edits).
func resetQuotaCache() {
	quotaCacheMu.Lock()
	quotaCache = map[string]quotaCacheEntry{}
	quotaCacheMu.Unlock()
}
