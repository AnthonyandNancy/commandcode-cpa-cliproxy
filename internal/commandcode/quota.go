package commandcode

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
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
