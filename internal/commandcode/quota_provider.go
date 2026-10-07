package commandcode

import (
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// QuotaFetchResponseFor maps one report onto the host's normalized quota frame.
// CPAMP credential management renders it exactly like the Codex quota: a plan
// line, bounded summary metrics and one bucket per metered window.
func QuotaFetchResponseFor(report QuotaReport) pluginapi.QuotaFetchResponse {
	out := pluginapi.QuotaFetchResponse{}
	if report.Plan.PlanID != "" || report.Plan.Name != "" {
		out.Subscription = &pluginapi.QuotaSubscription{
			Plan:     report.Plan.Name,
			TierName: report.Plan.Name,
			TierID:   report.Plan.PlanID,
		}
	}
	monthly := buildQuotaMonthlyView(report)
	if report.Credits.MonthlyReported {
		out.Summary = append(out.Summary,
			quotaCurrencyMetric("monthly_remaining", "剩余额度 Remaining", report.Credits.MonthlyCredits),
			quotaCurrencyMetric("purchased", "已购额度 Purchased", report.Credits.PurchasedCredits),
			quotaCurrencyMetric("free", "赠送额度 Free", report.Credits.FreeCredits),
		)
	}
	if monthly != nil && monthly.LimitReported {
		out.Summary = append(out.Summary, quotaCurrencyMetric("monthly_limit", "月额度上限 Monthly limit", monthly.Limit))
	}
	if monthly != nil && monthly.Known {
		out.Summary = append(out.Summary, quotaCurrencyMetric("monthly_used", "本月已用 Monthly used", monthly.Used))
	}
	if report.Usage.Reported {
		out.Summary = append(out.Summary,
			pluginapi.QuotaMetric{Key: "requests", Label: "请求 Requests", Value: report.Usage.CompletedCount, Format: "number"},
			pluginapi.QuotaMetric{Key: "requests_failed", Label: "失败 Failed", Value: report.Usage.FailedCount, Format: "number"},
			pluginapi.QuotaMetric{Key: "success_rate", Label: "成功率 Success rate", Value: report.Usage.SuccessRate, Unit: "%", Format: "number"},
			pluginapi.QuotaMetric{Key: "spend", Label: "花费 Spend", Value: report.Usage.TotalCost, Format: "currency", Currency: "USD"},
			pluginapi.QuotaMetric{Key: "tokens", Label: "Token 总数 Tokens", Value: report.Usage.TotalTokensIn + report.Usage.TotalTokensOut, Format: "number"},
		)
	}

	group := pluginapi.QuotaGroup{DisplayName: "配额窗口 Quota windows"}
	if monthly != nil && monthly.Known {
		group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{
			Window:            "monthly",
			RemainingFraction: clampFraction(monthly.Remaining / monthly.Limit),
			ResetTime:         rfc3339Millis(report.Plan.CurrentPeriodEnd),
			Description:       fmt.Sprintf("used %.2f / %.2f", monthly.Used, monthly.Limit),
		})
	}
	if window := report.Credits.FiveHour; window != nil {
		group.Buckets = append(group.Buckets, quotaBucket("5h", window))
	}
	if window := report.Credits.Weekly; window != nil {
		group.Buckets = append(group.Buckets, quotaBucket("weekly", window))
	}
	if len(group.Buckets) > 0 {
		out.Groups = []pluginapi.QuotaGroup{group}
	}
	return out
}

func quotaBucket(window string, limit *QuotaWindow) pluginapi.QuotaBucket {
	bucket := pluginapi.QuotaBucket{
		Window:      window,
		ResetTime:   rfc3339Millis(limit.ResetAtMS),
		Description: fmt.Sprintf("used %.2f / cap %.2f", limit.Used, limit.Cap),
	}
	if limit.Cap > 0 {
		bucket.RemainingFraction = clampFraction(1 - limit.Used/limit.Cap)
	}
	return bucket
}

func quotaCurrencyMetric(key, label string, value float64) pluginapi.QuotaMetric {
	return pluginapi.QuotaMetric{Key: key, Label: label, Value: value, Format: "currency", Currency: "USD"}
}
