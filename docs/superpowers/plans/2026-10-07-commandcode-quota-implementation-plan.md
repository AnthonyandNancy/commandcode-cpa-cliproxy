# CommandCode 额度能力（页面 + CPAMP 凭证管理）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 CommandCode 凭据具备“套餐 / 月额度 / 5 小时窗口 / 每周窗口 / 用量报表”能力：插件账号页按账号展示，同时通过 CPA quota provider 契约对接到 CPAMP 凭证管理（与 Codex 额度同一机制）。

**Architecture:** 新增一个纯 Go 的额度模块（`quota.go`）从上游四个 `/alpha` 端点取数并归一化；页面走既有的 `?action=` 单路由（新增只读 `quota` 动作 + 弹层），CPAMP 走宿主 quota provider（注册能力 `quota_provider` + `quota.identifier/describe/fetch/reset`）。两处消费同一份 `QuotaReport`，不落盘、不改凭据文件格式、不改 CPA/CPAMP 源码。

**Tech Stack:** Go 1.26（插件 SDK `github.com/router-for-me/CLIProxyAPI/v8 v8.0.17`）、纯 Go 单测 + `httptest` 假上游、c-shared 构建（MinGW）。

## Global Constraints

- 只改本插件仓库，不改 CPA/CPAMP 源码（本地也没有其源码）。
- 不新增第三方依赖；`go.mod` 保持现状。
- API key 只进请求头：不得出现在 URL、页面、日志（沿用现有约定）。
- 账号页文案必须中英双语；样式只用 CPAMP 注入的 CSS 变量。
- 额度是实时查询，不写入凭据文件（凭据文件仍是 host 与插件共同维护的单一真相）。
- 任务按 TDD 执行：先写失败测试 → 确认失败 → 最小实现 → 确认通过 → 提交。
- 本环境无法访问 `commandcode.ai` 与运行的 CPAMP：上游真实联调属于部署环境冒烟项，不作为本地完成条件。
- 上游端点是官方 CLI 的私有 `/alpha` 接口，字段按参考实现 `@mars-sea/dsh-commandcode-provider@0.12.0`（本机 `C:\Users\guojl\.dsh\profiles\desktop\node_modules\@mars-sea\dsh-commandcode-provider\lib\index.js`）记录固定；必须容忍单端点失败。

---

# Implementation Plan

## 1. Goal

**本次修改目标**

1. 插件账号页每个 CommandCode 账号可查看：套餐与账期、月额度（上限/已用/剩余/已购/赠送 + 比例条）、5 小时窗口、每周窗口（含重置时间）、用量（请求/失败、成功率、花费、Token 出入）。
2. 复刻截图（`Command Code / 套餐、额度与配额窗口`）所示额度与用量信息；数据口径与参考实现一致。
3. 按 Codex 额度的同一机制把 CommandCode 额度接入 CPAMP 凭证管理：注册 `quota_provider` 能力并实现 `quota.identifier/describe/fetch/reset`。

**不包含**

- 不实现 dsh-commandcode-provider 的其余能力：多账号轮换/冷却探测、模型选择器套餐过滤、免费模型排序、OAuth 登录、协议选择（这些超出截图与已确认需求，且与“CPA 负责轮换/路由”的既有边界冲突）。
- 不写额度到凭据文件、不新增配置键、不改页面既有写操作语义（`allow-page-write` 只门禁写）。
- 不改 CPA/CPAMP 源码、不改 `store/registry-entry.json` 的 schema。

**成功标准**

- `go test ./internal/...` 全绿；`go vet ./...` 干净；`gofmt -l` 无输出；`go build -buildmode=c-shared` 通过。
- 本地（httptest 假上游）验证：`?action=quota&file=...` 返回套餐/三窗口/用量视图；页面具备额度按钮与弹层。
- 契约验证：`Registration()` 含 `"quota_provider":true`；`quota.identifier/describe/fetch/reset` 返回宿主可解析的帧。
- 部署环境冒烟（非本地完成条件）：`GET /v0/management/quota/providers` 出现 commandcode；`POST /v0/management/quota/fetch {auth_index, provider:"commandcode"}` 返回归一化额度；CPAMP 凭证列表该凭据带 `supports_quota=true`，凭证管理页能打开额度。

---

## 2. Current Understanding

**当前实现方式**

- 插件是 CPA 动态插件（c-shared）：C ABI 在 `plugin.go`，业务在 `internal/commandcode`。
- 注册与分发：`rpc.go:67-104`（`HandleMethod`）、`rpc.go:116-136`（`registrationPayload` 的能力集只有 model/auth/executor/management）。
- 账号页：`management.go:27-31`（唯一菜单资源 `/accounts`）、`management.go:60-105`（`?action=` 分发）、`management.go:109-245`（`host.auth.list`/`host.auth.get` 读列表）、`management.go:249-407`（写操作）。
- 页面：`page.go:11-92`（中英文案表）、`page.go:117-198`（HTML 组装）、`page.go:310-501`（前端 runtime：列表/弹窗/toast）。
- 上游访问基础设施：`httpclient.go:20-70`（client + headers）、`models.go:113-195`（按凭据 GET + TTL 缓存 + 降级，缓存模式可复用）、`identity.go:122-205`（`DeriveDevice`）、`provider.go:44-79`（常量与默认值）。
- 现状：**仓库内没有任何额度/quota 代码**；旧计划 `docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md:420-422` 明确把“额度/健康能力”列为不做。

**涉及模块**

| 模块 | 现状 | 本次关系 |
|---|---|---|
| `provider.go` | 端点常量：generate/fingerprint/lifecycle/models | 新增 4 个 `/alpha` 端点常量 |
| `quota.go`（新） | 不存在 | 取数/解析/缓存/页面视图 |
| `quota_provider.go`（新） | 不存在 | quota.* RPC + CPAMP 归一化映射 |
| `rpc.go` | 无 quota 能力 | 注册 `quota_provider` + 分发 |
| `management.go` | action=data/save/toggle/remove/restore/purge | 新增只读 `action=quota` |
| `page.go` | 账号列表 + 增改弹窗 | 新增额度按钮 + 额度弹层 |
| `models.go` / `httpclient.go` / `identity.go` / `account.go` | 既有基础设施 | 直接复用，不修改 |

**数据流 / 调用链**

1. 页面：浏览器 `GET <page>?action=quota&file=<name>` → `handleManagementRequest` → `loadAccountByFile`（`host.auth.list` + `host.auth.get` → `ParseAccount`）→ `FetchQuotaReport` → `buildQuotaView` → JSON。
2. CPAMP：`POST /v0/management/quota/fetch {auth_index, provider:"commandcode"}` → CPA `pluginhost` 按 provider 找到本插件 → RPC `quota.fetch`（携带凭据 `StorageJSON`）→ `handleQuotaFetch` → `ParseAccount` → `FetchQuotaReport` → `QuotaFetchResponseFor` → 归一化 `QuotaFetchResponse`。
3. 上游（两者共用）：`whoami` →（orgId）→ 并行 `usage/summary`、`billing/credits`、`billing/subscriptions?orgId=`，全部 `GET` + `Authorization: Bearer <key>` + `x-command-code-version` / `x-cli-environment`。

**已确认的影响范围（调查结论）**

- 宿主契约来自 SDK v8.0.17：`sdk/pluginabi/types.go:90-93`（方法名）、`sdk/pluginapi/types.go:1570-1770`（类型）、`internal/pluginhost/rpc_schema.go:48,190`（`quota_provider` 能力字段）。
- CPAMP 集成点已存在，无需面板改动：`internal/api/server_management.go:84-86`（`/quota/providers|fetch|reset`）、`internal/api/handlers/management/plugin_quota.go:59-133`（fetch）、`auth_files.go:673-691`（`supports_quota`/`quota_provider` 标记）。
- `quota.reset` 会被管理端点无条件调用（`plugin_quota.go:135-236`），必须实现（返回明确“不支持”）。
- 上游数据口径（参考实现）：`credits.monthlyCredits` 是**剩余**；月上限来自套餐表（GOAT=70）；`windowLimits.fiveHour/weekly = {used, cap, exceeded, resetAt(ms)}`；`subscriptions.data = {planId,status,currentPeriodEnd}`；`usage.successRate` 已是百分数；页面“请求”取 `completedCount`。
- **需求文档不一致（需在执行第 8 步修订）**：旧计划附录 B 把额度列为“不做”，与本次已确认需求冲突 → 以新需求为准，同步修订文档。

---

## 3. Implementation Steps

> 说明：Step 1–3 的代码块属于**同一个新文件 `internal/commandcode/quota.go`**，按步骤顺序追加；每个步骤结束时该文件都必须能编译（import 在使用它的步骤补齐）。

### Step 1: 上游端点常量 + 套餐表 + 数值助手

#### Target

- 修改：`internal/commandcode/provider.go`（端点常量）
- 新建：`internal/commandcode/quota.go`（套餐表、plan 解析、数值/比例助手）
- 新建：`internal/commandcode/quota_test.go`

#### Current Behavior

`provider.go:74-79` 只有 `EndpointGenerate/EndpointFingerprint/EndpointLifecycle/EndpointModels`；没有 billing/usage 端点，也没有套餐与月额度概念。

#### Change

1）`provider.go` 端点常量块追加：

```go
// Endpoint paths on the CommandCode API.
const (
	EndpointGenerate    = "/alpha/generate"
	EndpointFingerprint = "/alpha/fingerprint/record"
	EndpointLifecycle   = "/alpha/lifecycle-events"
	EndpointModels      = "/provider/v1/models"
	// Account endpoints behind the account page's quota panel and CPAMP's
	// credential quota: identity, usage totals, balances/windows, subscription.
	EndpointWhoami               = "/alpha/whoami"
	EndpointUsageSummary         = "/alpha/usage/summary"
	EndpointBillingCredits       = "/alpha/billing/credits"
	EndpointBillingSubscriptions = "/alpha/billing/subscriptions"
)
```

2）新建 `quota.go`：

```go
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
// *_reported flags in QuotaCredits tell a missing field from a real zero.
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
```

3）新建 `quota_test.go`：

```go
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
```

#### Reason

额度上限的口径是“套餐月额度”，不是上游余额；端点与套餐表必须与官方 CLI/参考实现一致，先固定常量与纯函数便于 TDD。

#### Risk

套餐表可能随官方调整而过期（未知 plan 会优雅降级为“只显示剩余”）；`individual-go` 与 `individual-goat` 这类前缀冲突靠表顺序解决，测试已覆盖。

#### Verification

- [ ] 1. 写失败测试（以上 `quota_test.go`）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run 'TestSubscriptionPlanInfoLongestPrefix|TestQuotaPercentAndFraction' -v`，预期 FAIL（`subscriptionPlanInfo` 未定义）
- [ ] 3. 实现 `provider.go` 常量与 `quota.go` 第一部分
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/provider.go internal/commandcode/quota.go internal/commandcode/quota_test.go && git commit -m "feat(quota): add account endpoint constants and subscription plan table"`

---

### Step 2: 额度报表取数、解析与缓存（`FetchQuotaReport`）

#### Target

- 修改：`internal/commandcode/quota.go`（追加：`QuotaWindow/QuotaAccount/QuotaPlan/QuotaCredits/QuotaUsage/QuotaReport`、`FetchQuotaReport`、解析函数、缓存）
- 修改：`internal/commandcode/quota_test.go`（追加假上游与取数测试）

#### Current Behavior

不存在任何 `/alpha/billing`、`/alpha/usage` 调用；也没有额度的进程内缓存。

#### Change

`quota.go` 的 import 改为：

```go
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
```

追加：

```go
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
```

`quota_test.go` 追加（import 改为 `import ("net/http" "net/http/httptest" "testing"` + `"sync/atomic"`）：

```go
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
```

#### Reason

参考实现用同一组端点与降级语义；`*_Reported` 标志避免把缺失余额误画成 0/100%；短 TTL 缓存保护上游，满足“页面按需查询 + CPAMP 轮询”并存。

#### Risk

`whoami` 失败时订阅查询不携带 orgId（与参考实现一致，仍能返回该账号默认订阅）；上游响应非 JSON 会记为该端点失败；缓存对同 key 不同 base-url 的账号不区分（极少见，记录为已知限制）。

#### Verification

- [ ] 1. 写失败测试（以上 5 个测试）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run TestFetchQuotaReport -v`，预期 FAIL（`FetchQuotaReport` 未定义）
- [ ] 3. 实现 `quota.go` 第二部分
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/quota.go internal/commandcode/quota_test.go && git commit -m "feat(quota): fetch and normalize the account quota report"`

---

### Step 3: 页面视图（`buildQuotaView`）

#### Target

- 修改：`internal/commandcode/quota.go`（追加 `quotaView/quotaMonthlyView/quotaWindowView/quotaUsageView` 与构造函数）
- 修改：`internal/commandcode/quota_test.go`（追加视图测试）

#### Current Behavior

报表只有 Go 结构，页面还没有可渲染的 JSON 形状；比例/已用等派生值尚无归属。

#### Change

`quota.go` 追加：

```go
// quotaView is the JSON the account page renders. Derived figures (used,
// percentages) are computed here so the page stays a formatter; the *_reported
// flags keep a missing figure from being drawn as a confident zero.
type quotaView struct {
	Account    string            `json:"account,omitempty"`
	PlanName   string            `json:"plan_name,omitempty"`
	PlanStatus string            `json:"plan_status,omitempty"`
	PeriodEnds int64             `json:"period_ends,omitempty"`
	Monthly    *quotaMonthlyView `json:"monthly,omitempty"`
	Windows    []quotaWindowView `json:"windows,omitempty"`
	Usage      *quotaUsageView   `json:"usage,omitempty"`
	Failures   []string          `json:"failures,omitempty"`
}

type quotaMonthlyView struct {
	LimitReported     bool    `json:"limit_reported"`
	Limit             float64 `json:"limit"`
	RemainingReported bool    `json:"remaining_reported"`
	Remaining         float64 `json:"remaining"`
	PurchasedReported bool    `json:"purchased_reported"`
	Purchased         float64 `json:"purchased"`
	FreeReported      bool    `json:"free_reported"`
	Free              float64 `json:"free"`
	Known             bool    `json:"known"`
	Used              float64 `json:"used"`
	Percent           int     `json:"percent"`
	Exhausted         bool    `json:"exhausted"`
}

type quotaWindowView struct {
	Key      string  `json:"key"`
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Percent  int     `json:"percent"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"reset_at,omitempty"`
}

type quotaUsageView struct {
	Requests    float64 `json:"requests"`
	Failed      float64 `json:"failed"`
	SuccessRate float64 `json:"success_rate"`
	Cost        float64 `json:"cost"`
	Credits     float64 `json:"credits"`
	TokensIn    float64 `json:"tokens_in"`
	TokensOut   float64 `json:"tokens_out"`
}

// buildQuotaView renders one report for the account page.
func buildQuotaView(report QuotaReport) quotaView {
	view := quotaView{
		Account:    firstNonEmpty(report.Account.UserName, report.Account.Name),
		PlanName:   report.Plan.Name,
		PlanStatus: report.Plan.Status,
		PeriodEnds: report.Plan.CurrentPeriodEnd,
		Failures:   report.Failures,
	}
	view.Monthly = buildQuotaMonthlyView(report)
	if report.Credits.FiveHour != nil {
		view.Windows = append(view.Windows, buildQuotaWindowView("five_hour", report.Credits.FiveHour))
	}
	if report.Credits.Weekly != nil {
		view.Windows = append(view.Windows, buildQuotaWindowView("weekly", report.Credits.Weekly))
	}
	if report.Usage.Reported {
		view.Usage = &quotaUsageView{
			Requests:    report.Usage.CompletedCount,
			Failed:      report.Usage.FailedCount,
			SuccessRate: report.Usage.SuccessRate,
			Cost:        report.Usage.TotalCost,
			Credits:     report.Usage.TotalCredits,
			TokensIn:    report.Usage.TotalTokensIn,
			TokensOut:   report.Usage.TotalTokensOut,
		}
	}
	return view
}

// buildQuotaMonthlyView derives the monthly meter: the limit is the PLAN's
// credit total, the reported monthlyCredits is the remaining balance. A missing
// balance stays a missing figure (Known=false), never "100% used".
func buildQuotaMonthlyView(report QuotaReport) *quotaMonthlyView {
	monthly := report.Credits
	plan := report.Plan
	view := &quotaMonthlyView{
		Purchased:         monthly.PurchasedCredits,
		PurchasedReported: monthly.PurchasedReported,
		Free:              monthly.FreeCredits,
		FreeReported:      monthly.FreeReported,
		RemainingReported: monthly.MonthlyReported,
		Remaining:         math.Max(0, monthly.MonthlyCredits),
	}
	if plan.MonthlyCredits != nil {
		view.LimitReported = true
		view.Limit = *plan.MonthlyCredits
	}
	if !view.LimitReported && !view.RemainingReported && !view.PurchasedReported && !view.FreeReported {
		return nil
	}
	if view.LimitReported && view.RemainingReported && view.Limit > 0 {
		view.Known = true
		view.Used = math.Max(0, view.Limit-view.Remaining)
		view.Percent = quotaPercent(view.Used, view.Limit)
		view.Exhausted = view.Remaining <= 0
	}
	return view
}

func buildQuotaWindowView(key string, window *QuotaWindow) quotaWindowView {
	return quotaWindowView{
		Key:      key,
		Used:     window.Used,
		Cap:      window.Cap,
		Percent:  quotaPercent(window.Used, window.Cap),
		Exceeded: window.Exceeded,
		ResetAt:  window.ResetAtMS,
	}
}
```

`quota_test.go` 追加：

```go
func TestBuildQuotaView(t *testing.T) {
	monthly := 70.0
	report := QuotaReport{
		Account: QuotaAccount{UserName: "AnthonyandNancy"},
		Plan:    QuotaPlan{PlanID: "individual-goat", Name: "GOAT", Status: "active", MonthlyCredits: &monthly, CurrentPeriodEnd: 1792272000000},
		Credits: QuotaCredits{
			MonthlyCredits: 29.36, MonthlyReported: true,
			FiveHour: &QuotaWindow{Used: 2.18, Cap: 14, ResetAtMS: 1791341541000},
			Weekly:   &QuotaWindow{Used: 10.07, Cap: 35, ResetAtMS: 1791705406000},
		},
		Usage: QuotaUsage{Reported: true, CompletedCount: 14779, FailedCount: 19, SuccessRate: 99.87, TotalCost: 41.3353, TotalCredits: 41.34, TotalTokensIn: 2800000000, TotalTokensOut: 15800000},
	}

	view := buildQuotaView(report)
	if view.PlanName != "GOAT" || view.PeriodEnds != 1792272000000 {
		t.Errorf("view = %+v, want the plan header", view)
	}
	if view.Monthly == nil || !view.Monthly.Known {
		t.Fatalf("monthly = %+v, want a known meter", view.Monthly)
	}
	if view.Monthly.Used != 40.64 || view.Monthly.Percent != 58 {
		t.Errorf("monthly = %+v, want 40.64 used / 58%%", view.Monthly)
	}
	if view.Monthly.Remaining != 29.36 || view.Monthly.Exhausted {
		t.Errorf("monthly = %+v, want 29.36 remaining", view.Monthly)
	}
	if len(view.Windows) != 2 || view.Windows[0].Key != "five_hour" || view.Windows[1].Key != "weekly" {
		t.Fatalf("windows = %+v, want five_hour then weekly", view.Windows)
	}
	if view.Windows[0].Percent != 16 || view.Windows[1].Percent != 29 {
		t.Errorf("window percents = %d/%d, want 16/29", view.Windows[0].Percent, view.Windows[1].Percent)
	}
	if view.Usage == nil || view.Usage.Requests != 14779 || view.Usage.Failed != 19 {
		t.Errorf("usage = %+v, want the summary totals", view.Usage)
	}
}

func TestBuildQuotaViewUnknownPlanKeepsRemainingOnly(t *testing.T) {
	report := QuotaReport{
		Plan:    QuotaPlan{PlanID: "individual-mystery", Name: "individual-mystery"},
		Credits: QuotaCredits{MonthlyCredits: 5, MonthlyReported: true},
	}
	view := buildQuotaView(report)
	if view.Monthly == nil {
		t.Fatal("monthly = nil, want the reported balance kept")
	}
	if view.Monthly.Known || view.Monthly.LimitReported {
		t.Errorf("monthly = %+v, want no ratio without a plan total", view.Monthly)
	}
	if !view.Monthly.RemainingReported || view.Monthly.Remaining != 5 {
		t.Errorf("monthly = %+v, want the reported remaining", view.Monthly)
	}
}

func TestBuildQuotaViewNoCreditsNoMonthly(t *testing.T) {
	report := QuotaReport{Plan: QuotaPlan{PlanID: "individual-goat", Name: "GOAT"}}
	view := buildQuotaView(report)
	if view.Monthly != nil {
		t.Errorf("monthly = %+v, want nil when the billing endpoint reported nothing", view.Monthly)
	}
}
```

#### Reason

需求 1/2 需要“每个账号看三窗口 + 额度/用量卡片”；把派生值放在 Go 侧便于测试（页面只做格式化），也符合仓库“业务规则可在无浏览器环境测试”的既有分层。

#### Risk

百分比按参考实现不封顶（150% 可显示），前端单独 clamp 条宽；未知套餐只显示剩余。

#### Verification

- [ ] 1. 写失败测试（以上 3 个测试）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run TestBuildQuotaView -v`，预期 FAIL
- [ ] 3. 实现视图类型与构造函数
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/quota.go internal/commandcode/quota_test.go && git commit -m "feat(quota): derive the account page quota view"`

---

### Step 4: CPAMP 归一化映射（`QuotaFetchResponseFor`）

#### Target

- 新建：`internal/commandcode/quota_provider.go`（本步只放映射函数）
- 修改：`internal/commandcode/quota_test.go`（追加映射测试）

#### Current Behavior

没有宿主归一化额度结构，CPAMP 无可渲染数据。

#### Change

新建 `quota_provider.go`：

```go
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
```

`quota_test.go` 追加：

```go
func TestQuotaFetchResponseForMapsWindowsAndMetrics(t *testing.T) {
	monthly := 70.0
	report := QuotaReport{
		Plan:    QuotaPlan{PlanID: "individual-goat", Name: "GOAT", CurrentPeriodEnd: 1792272000000},
		Credits: QuotaCredits{MonthlyCredits: 29.36, MonthlyReported: true, PurchasedReported: true, FreeReported: true,
			FiveHour: &QuotaWindow{Used: 2.18, Cap: 14, ResetAtMS: 1791341541000},
			Weekly:   &QuotaWindow{Used: 10.07, Cap: 35, ResetAtMS: 1791705406000}},
		Usage: QuotaUsage{Reported: true, CompletedCount: 14779, FailedCount: 19, SuccessRate: 99.87, TotalCost: 41.3353, TotalCredits: 41.34},
	}
	resp := QuotaFetchResponseFor(report)
	if resp.Subscription == nil || resp.Subscription.TierID != "individual-goat" || resp.Subscription.Plan != "GOAT" {
		t.Fatalf("subscription = %+v, want the GOAT plan", resp.Subscription)
	}
	if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 3 {
		t.Fatalf("groups = %+v, want monthly/5h/weekly buckets", resp.Groups)
	}
	if got := resp.Groups[0].Buckets[0]; got.Window != "monthly" || got.ResetTime != "2026-10-19T16:00:00Z" {
		t.Errorf("monthly bucket = %+v", got)
	}
	five := resp.Groups[0].Buckets[1]
	if five.Window != "5h" || five.RemainingFraction < 0.84 || five.RemainingFraction > 0.85 {
		t.Errorf("5h bucket = %+v, want ~0.844 remaining", five)
	}
	if resp.Groups[0].Buckets[2].Window != "weekly" {
		t.Errorf("weekly bucket = %+v", resp.Groups[0].Buckets[2])
	}
	if len(resp.Summary) == 0 {
		t.Fatal("summary is empty, want metrics")
	}
}

func TestQuotaFetchResponseForUncappedWindowHasZeroFraction(t *testing.T) {
	report := QuotaReport{Credits: QuotaCredits{FiveHour: &QuotaWindow{Used: 3, Cap: 0}}}
	resp := QuotaFetchResponseFor(report)
	if len(resp.Groups) != 1 || resp.Groups[0].Buckets[0].RemainingFraction != 0 {
		t.Fatalf("buckets = %+v, want an uncapped bucket with zero ratio", resp.Groups)
	}
}
```

#### Reason

宿主只认 `QuotaFetchResponse`（Subscription/Summary/Groups+Buckets）；把 CommandCode 的三窗口与余额映射成 CPAMP 可直接渲染的形状，是需求 3 的关键一步。

#### Risk

窗口标签（`5h`/`weekly`/`monthly`）由面板直接显示，属于约定值；`RemainingFraction` 只表达剩余比例，具体 `used/cap` 数值通过 `Description` 与 Summary 指标补充。若部署面板对指标/桶有额外校验，部署冒烟时以实际渲染为准（本地无法验证）。

#### Verification

- [ ] 1. 写失败测试（以上 2 个测试）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run TestQuotaFetchResponseFor -v`，预期 FAIL
- [ ] 3. 实现 `quota_provider.go` 映射部分
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/quota_provider.go internal/commandcode/quota_test.go && git commit -m "feat(quota): map reports onto the host quota frame"`

---

### Step 5: quota RPC 实现与注册（`quota.identifier/describe/fetch/reset`）

#### Target

- 修改：`internal/commandcode/quota_provider.go`（追加四个 handler）
- 修改：`internal/commandcode/rpc.go`（能力字段 + 注册 + 分发）
- 修改：`internal/commandcode/quota_test.go`、`internal/commandcode/rpc_test.go`

#### Current Behavior

`rpc.go:27-35` 的能力集没有 quota；`HandleMethod` 不认识 `quota.*`；宿主管理端会得到 unknown method。

#### Change

1）`quota_provider.go` import 改为：

```go
import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)
```

追加：

```go
// handleQuotaIdentifier implements quota.identifier. The identifier is the
// provider key CPAMP matches credentials on ("commandcode"), the same value
// auth.identifier and executor.identifier already answer with.
func handleQuotaIdentifier(request []byte) ([]byte, error) {
	return okEnvelope(map[string]string{"identifier": ProviderID}), nil
}

// handleQuotaDescribe implements quota.describe: this provider serves its own
// credentials only and has no reset operation (the windows roll upstream).
func handleQuotaDescribe(request []byte) ([]byte, error) {
	return okEnvelope(pluginapi.QuotaDescribeResponse{
		SupportedProviders: []string{ProviderID},
		DisplayName:        "CommandCode",
		SupportsReset:      false,
	}), nil
}

// handleQuotaFetch implements quota.fetch: CPAMP credential management asks for
// one credential's quota by auth index; the host hands us the credential JSON
// in StorageJSON.
func handleQuotaFetch(request []byte) ([]byte, error) {
	var req pluginapi.QuotaFetchRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	account, handled, errParse := ParseAccount(req.StorageJSON)
	if !handled {
		return nil, fmt.Errorf("credential is not a %s account", ProviderID)
	}
	if errParse != nil {
		return nil, errParse
	}
	report, errFetch := FetchQuotaReport(context.Background(), account, CurrentConfig())
	if errFetch != nil {
		return nil, errFetch
	}
	return okEnvelope(QuotaFetchResponseFor(report)), nil
}

// handleQuotaReset implements quota.reset. CommandCode meters rolling windows
// upstream and exposes no reset call, so the answer is a clear refusal instead
// of an unknown-method failure: the management endpoint always calls reset.
func handleQuotaReset(request []byte) ([]byte, error) {
	return okEnvelope(pluginapi.QuotaResetResponse{
		Success: false,
		Message: "CommandCode 不支持重置额度窗口（窗口由上游滚动重置）",
	}), nil
}
```

2）`rpc.go`：`capabilities` 结构体加字段：

```go
// capabilities mirrors the host's rpcCapabilities frame. Only the flags this
// plugin implements are set.
type capabilities struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	ManagementAPI         bool                         `json:"management_api"`
	QuotaProvider         bool                         `json:"quota_provider"`
}
```

`registrationPayload()` 的 Capabilities 追加 `QuotaProvider: true`；`HandleMethod` 的 switch 追加：

```go
	case pluginabi.MethodQuotaIdentifier:
		return handleQuotaIdentifier(request)

	case pluginabi.MethodQuotaDescribe:
		return handleQuotaDescribe(request)

	case pluginabi.MethodQuotaFetch:
		return handleQuotaFetch(request)

	case pluginabi.MethodQuotaReset:
		return handleQuotaReset(request)
```

3）测试。

`rpc_test.go`：匿名结构体加 `QuotaProvider bool \`json:"quota_provider"\``；能力断言改为包含 quota：

```go
	if !got.Capabilities.ModelProvider || !got.Capabilities.AuthProvider || !got.Capabilities.Executor || !got.Capabilities.ManagementAPI || !got.Capabilities.QuotaProvider {
		t.Errorf("capabilities = %+v, want model/auth/executor/management/quota all enabled", got.Capabilities)
	}
```

`quota_test.go` 追加：

```go
func decodeQuotaEnvelope(t *testing.T, frame []byte, out any) {
	t.Helper()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("frame is not valid JSON: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("frame reported a failure: %s", frame)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		t.Fatalf("result decode: %v", err)
	}
}

func TestQuotaIdentifierDescribeReset(t *testing.T) {
	raw, err := handleQuotaIdentifier(nil)
	if err != nil {
		t.Fatalf("handleQuotaIdentifier() error = %v", err)
	}
	var identifier struct {
		Identifier string `json:"identifier"`
	}
	decodeQuotaEnvelope(t, raw, &identifier)
	if identifier.Identifier != ProviderID {
		t.Errorf("identifier = %q, want %q", identifier.Identifier, ProviderID)
	}

	raw, err = handleQuotaDescribe(nil)
	if err != nil {
		t.Fatalf("handleQuotaDescribe() error = %v", err)
	}
	var describe pluginapi.QuotaDescribeResponse
	decodeQuotaEnvelope(t, raw, &describe)
	if len(describe.SupportedProviders) != 1 || describe.SupportedProviders[0] != ProviderID {
		t.Errorf("supported providers = %v, want [%s]", describe.SupportedProviders, ProviderID)
	}
	if describe.SupportsReset {
		t.Error("supports_reset = true, want false")
	}

	raw, err = handleQuotaReset(nil)
	if err != nil {
		t.Fatalf("handleQuotaReset() error = %v", err)
	}
	var reset pluginapi.QuotaResetResponse
	decodeQuotaEnvelope(t, raw, &reset)
	if reset.Success || reset.Message == "" {
		t.Errorf("reset = %+v, want a clear refusal", reset)
	}
}

func TestQuotaFetchHandler(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	server := httptest.NewServer(quotaFixtureHandler(t))
	defer server.Close()

	previous := CurrentConfig()
	ApplyConfigYAML([]byte("base-url: " + server.URL))
	t.Cleanup(func() { runtimeConfigValue.Store(previous); resetQuotaCache() })

	request, _ := json.Marshal(pluginapi.QuotaFetchRequest{
		AuthIndex:   "commandcode-abc.json",
		AuthID:      "commandcode-abc.json",
		Provider:    ProviderID,
		StorageJSON: []byte(`{"type":"commandcode","api_key":"user_test1234567890"}`),
	})
	raw, err := handleQuotaFetch(request)
	if err != nil {
		t.Fatalf("handleQuotaFetch() error = %v", err)
	}
	var response pluginapi.QuotaFetchResponse
	decodeQuotaEnvelope(t, raw, &response)
	if response.Subscription == nil || response.Subscription.TierID != "individual-goat" {
		t.Fatalf("subscription = %+v, want GOAT", response.Subscription)
	}
	if len(response.Groups) != 1 || len(response.Groups[0].Buckets) != 3 {
		t.Fatalf("groups = %+v, want three buckets", response.Groups)
	}
}

func TestQuotaFetchHandlerRejectsForeignCredential(t *testing.T) {
	request, _ := json.Marshal(pluginapi.QuotaFetchRequest{Provider: "other", StorageJSON: []byte(`{"type":"other","api_key":"x"}`)})
	if _, err := handleQuotaFetch(request); err == nil {
		t.Fatal("handleQuotaFetch(foreign) error = nil, want a rejection")
	}
}

func TestHandleMethodDispatchesQuota(t *testing.T) {
	raw, err := HandleMethod("quota.identifier", nil)
	if err != nil {
		t.Fatalf("HandleMethod(quota.identifier) error = %v", err)
	}
	var identifier struct {
		Identifier string `json:"identifier"`
	}
	decodeQuotaEnvelope(t, raw, &identifier)
	if identifier.Identifier != ProviderID {
		t.Errorf("identifier = %q, want %q", identifier.Identifier, ProviderID)
	}
}
```

`quota_test.go` 的 import 需要包含 `encoding/json` 与 `github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi`。

#### Reason

宿主只有看到注册能力 `quota_provider` 才会把 CPAMP 的额度请求路由到本插件（`rpc_schema.go:48,190`、`rpc_client.go:169-177`）；`quota.reset` 必须存在，否则管理端点会报 unknown method。

#### Risk

旧版宿主不认识 `quota_provider` 字段（JSON 解码忽略未知字段）——能力不会被激活，但注册与既有功能不受影响；已注册能力导致 `quota.identifier/describe` 会在宿主列出 provider 时被调用（轻量、无网络）。

#### Verification

- [ ] 1. 写失败测试（以上 4 个测试 + rpc_test 修改）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run 'TestQuota|TestHandleMethodDispatchesQuota|TestRegistrationCapabilities' -v`，预期 FAIL
- [ ] 3. 实现 handlers 与 `rpc.go` 注册/分发
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/quota_provider.go internal/commandcode/rpc.go internal/commandcode/quota_test.go internal/commandcode/rpc_test.go && git commit -m "feat(quota): register a quota provider with CPA"`

---

### Step 6: 账号页额度动作 `?action=quota`

#### Target

- 修改：`internal/commandcode/management.go`（`handleManagementRequest` 新增只读分支）
- 修改：`internal/commandcode/management_test.go`（追加动作测试）

#### Current Behavior

`management.go:70-104` 的动作只有 data/save/toggle/remove/restore/purge；写动作受 `cfg.AllowPageWrite` 门禁。

#### Change

1）`management.go` import 加 `"context"`。

2）在 `case action == "data":` 之后插入：

```go
	case action == "quota":
		// Read-only: the quota panel must stay available when page writes are
		// disabled. loadAccountByFile resolves the credential through the host
		// so the API key never travels to the browser.
		account, _, errLoad := loadAccountByFile(strings.TrimSpace(req.Query.Get("file")))
		if errLoad != nil {
			return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
		}
		report, errFetch := FetchQuotaReport(context.Background(), account, cfg)
		if errFetch != nil {
			return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errFetch.Error()})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: buildQuotaView(report)})
```

3）`management_test.go` 追加：

```go
func TestAccountsQuotaAction(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	upstream := httptest.NewServer(quotaFixtureHandler(t))
	defer upstream.Close()

	newAuthStubHost(t, map[string]string{
		"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`,
	})

	previous := CurrentConfig()
	ApplyConfigYAML([]byte("base-url: " + upstream.URL + "\nallow-page-write: false\n"))
	t.Cleanup(func() { runtimeConfigValue.Store(previous); resetQuotaCache() })

	frame, err := handleManagementRequest(managementRequest("GET", "/accounts", map[string]string{
		"action": "quota",
		"file":   "commandcode-abc.json",
	}, nil))
	if err != nil {
		t.Fatalf("handleManagementRequest(quota) error = %v", err)
	}
	response := decodeManagement(t, frame)
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			PlanName   string `json:"plan_name"`
			PeriodEnds int64  `json:"period_ends"`
			Monthly    *struct {
				Known   bool    `json:"known"`
				Used    float64 `json:"used"`
				Percent int     `json:"percent"`
			} `json:"monthly"`
			Windows []struct {
				Key     string `json:"key"`
				Percent int    `json:"percent"`
			} `json:"windows"`
			Usage *struct {
				Requests float64 `json:"requests"`
				Failed   float64 `json:"failed"`
			} `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("quota body decode: %v", err)
	}
	if !payload.OK {
		t.Fatalf("quota action failed: %s", response.Body)
	}
	if payload.Data.PlanName != "GOAT" || payload.Data.Monthly == nil || !payload.Data.Monthly.Known {
		t.Fatalf("data = %+v, want the GOAT monthly meter", payload.Data)
	}
	if payload.Data.Monthly.Used != 40.64 || payload.Data.Monthly.Percent != 58 {
		t.Errorf("monthly = %+v, want 40.64 / 58%%", payload.Data.Monthly)
	}
	if len(payload.Data.Windows) != 2 || payload.Data.Windows[0].Key != "five_hour" {
		t.Errorf("windows = %+v, want five_hour then weekly", payload.Data.Windows)
	}
	if payload.Data.Usage == nil || payload.Data.Usage.Requests != 14779 || payload.Data.Usage.Failed != 19 {
		t.Errorf("usage = %+v, want the summary totals", payload.Data.Usage)
	}
}

func TestAccountsQuotaActionUnknownFile(t *testing.T) {
	newAuthStubHost(t, map[string]string{
		"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`,
	})
	frame, err := handleManagementRequest(managementRequest("GET", "/accounts", map[string]string{
		"action": "quota",
		"file":   "no-such-file.json",
	}, nil))
	if err != nil {
		t.Fatalf("handleManagementRequest(quota unknown) error = %v", err)
	}
	response := decodeManagement(t, frame)
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if payload.OK || payload.Error == "" {
		t.Fatalf("payload = %+v, want a clear error", payload)
	}
}
```

#### Reason

需求 1 要求“在本插件页对应账号中”看到额度；`allow-page-write=false` 是只读模式，额度查看属于读操作，必须保持可用。

#### Risk

一次额度查询最多 4 个上游请求，会占用该管理请求若干秒；已有 30s 上限与 60s 缓存。页面按需触发（不在列表加载时批量查询）。

#### Verification

- [ ] 1. 写失败测试（以上 2 个测试）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run TestAccountsQuotaAction -v`，预期 FAIL
- [ ] 3. 实现 `management.go` 分支
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/management.go internal/commandcode/management_test.go && git commit -m "feat(quota): serve the per-account quota action on the account page"`

---

### Step 7: 账号页额度按钮与弹层（`page.go`）

#### Target

- 修改：`internal/commandcode/page.go`（文案表、HTML、CSS、前端 runtime）
- 修改：`internal/commandcode/management_test.go`（页面结构测试）

#### Current Behavior

页面只有账号列表与“新增/编辑”弹窗（`page.go:169-189`、`page.go:358-374`），行操作只有启停/编辑/移出（`page.go:425-445`），没有额度入口。

#### Change

1）中英文字案表追加（zh 与 en 各自 map，键相同）：

```go
		"quota":           "额度",
		"quota_title":     "额度与用量",
		"close":           "关闭",
		"plan":            "套餐",
		"period_ends":     "账期截止",
		"monthly":         "月额度",
		"window_5h":       "5 小时窗口",
		"window_weekly":   "每周窗口",
		"reset_at":        "重置于",
		"quota_limit":     "月额度上限",
		"quota_used":      "本月已用",
		"quota_remaining": "剩余",
		"quota_purchased": "已购",
		"quota_free":      "赠送",
		"quota_empty":     "上游未返回额度数据。",
		"exhausted":       "已用尽",
		"usage_requests":  "请求",
		"usage_failed":    "失败",
		"usage_success":   "成功率",
		"usage_spend":     "花费",
		"usage_tokens":    "Token",
		"tokens_in":       "入",
		"tokens_out":      "出",
```

```go
		"quota":           "Quota",
		"quota_title":     "Quota & usage",
		"close":           "Close",
		"plan":            "Plan",
		"period_ends":     "Period ends",
		"monthly":         "Monthly",
		"window_5h":       "5-hour window",
		"window_weekly":   "Weekly window",
		"reset_at":        "Resets at",
		"quota_limit":     "Monthly limit",
		"quota_used":      "Monthly used",
		"quota_remaining": "Remaining",
		"quota_purchased": "Purchased",
		"quota_free":      "Free",
		"quota_empty":     "Upstream returned no quota data.",
		"exhausted":       "Exhausted",
		"usage_requests":  "Requests",
		"usage_failed":    "failed",
		"usage_success":   "Success rate",
		"usage_spend":     "Spend",
		"usage_tokens":    "Tokens",
		"tokens_in":       "in",
		"tokens_out":      "out",
```

2）`pageScriptLiteral` 的 `keys` 列表追加同样的键名（`"quota", "quota_title", "close", "plan", "period_ends", "monthly", "window_5h", "window_weekly", "reset_at", "quota_limit", "quota_used", "quota_remaining", "quota_purchased", "quota_free", "quota_empty", "exhausted", "usage_requests", "usage_failed", "usage_success", "usage_spend", "usage_tokens", "tokens_in", "tokens_out"`）。

3）`AccountsPageHTML`：在 `b.WriteString("<div id=\"toast\" ...` 之前插入额度弹层：

```go
	b.WriteString("<div class=\"overlay\" id=\"quotaOverlay\" hidden>\n  <div class=\"modal\" role=\"dialog\" aria-modal=\"true\" aria-labelledby=\"quotaTitle\">\n")
	b.WriteString("    <div class=\"modal-head\"><h2 id=\"quotaTitle\">")
	b.WriteString(t("quota_title"))
	b.WriteString("</h2></div>\n    <div class=\"modal-body\" id=\"quotaBody\"></div>\n")
	b.WriteString("    <div class=\"modal-foot\"><button class=\"btn ghost\" id=\"quotaClose\">")
	b.WriteString(t("close"))
	b.WriteString("</button></div>\n  </div>\n</div>\n")
```

4）`pageStyle` 末尾追加：

```css
.meter-row { margin: 0 0 12px; }
.meter-head { display: flex; justify-content: space-between; gap: 12px; font-size: 12px; margin-bottom: 4px; }
.meter-label { color: var(--app-text-regular, #5f6c7b); }
.meter {
  height: 8px; border-radius: 999px; overflow: hidden;
  background: var(--app-border, rgba(15,23,42,.08));
}
.meter > span { display: block; height: 100%; width: 0; background: var(--primary-color, #409eff); }
.meter.bad > span { background: var(--danger-color, #f56c6c); }
.quota-head { margin-bottom: 12px; }
.quota-head-line { font-weight: 600; }
.quota-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); gap: 10px; margin-bottom: 10px; }
.tile {
  border: 1px solid var(--app-border, rgba(15,23,42,.08));
  border-radius: 10px; padding: 10px;
  background: var(--bg-secondary, var(--app-surface-muted, #f6faff));
}
.tile-label { margin-bottom: 2px; }
.tile-value { font-weight: 600; font-size: 16px; word-break: break-all; }
```

5）前端 runtime（`pageRuntime`）追加/替换：

在 `var editingFile = '';` 之后追加：

```js
  var quotaOverlay = document.getElementById('quotaOverlay');
  var quotaTitle = document.getElementById('quotaTitle');
  var quotaBody = document.getElementById('quotaBody');
```

在 `function openModal(account) {` 之前追加：

```js
  function money(value) { return '$' + Number(value || 0).toFixed(2); }
  function moneyExact(value) { return '$' + Number(value || 0).toFixed(4); }
  function tokens(value) {
    value = Number(value || 0);
    if (value >= 1e9) return (value / 1e9).toFixed(1) + 'B';
    if (value >= 1e6) return (value / 1e6).toFixed(1) + 'M';
    if (value >= 1e3) return (value / 1e3).toFixed(1) + 'K';
    return String(value);
  }
  function localDateTime(ms) { return ms > 0 ? new Date(ms).toLocaleString() : ''; }
  function localDate(ms) { return ms > 0 ? new Date(ms).toLocaleDateString() : ''; }

  function element(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  // A percentage bar: the printed figure keeps its real overshoot, the bar
  // width is clamped into [0, 100].
  function meterRow(label, valueText, percentText, percentValue, note, exceeded) {
    var row = element('div', 'meter-row');
    var head = element('div', 'meter-head');
    head.appendChild(element('span', 'meter-label', label));
    head.appendChild(element('span', 'muted small', valueText + (percentText ? '  ' + percentText : '')));
    row.appendChild(head);
    var track = element('div', 'meter' + (exceeded ? ' bad' : ''));
    var fill = element('span');
    fill.style.width = Math.min(100, Math.max(0, Number(percentValue) || 0)) + '%';
    track.appendChild(fill);
    row.appendChild(track);
    if (note) row.appendChild(element('div', 'muted small', note));
    return row;
  }

  function tile(label, value, sub) {
    var box = element('div', 'tile');
    box.appendChild(element('div', 'tile-label muted small', label));
    box.appendChild(element('div', 'tile-value', value));
    if (sub) box.appendChild(element('div', 'muted small', sub));
    return box;
  }

  function renderQuota(view) {
    quotaBody.innerHTML = '';
    var bits = [];
    if (view.plan_name) bits.push(view.plan_name);
    if (view.plan_status) bits.push(view.plan_status);
    if (view.account) bits.push(view.account);
    if (view.period_ends) bits.push((S.period_ends || '') + ' ' + localDate(view.period_ends));
    var head = element('div', 'quota-head');
    head.appendChild(element('div', 'quota-head-line', bits.join(' · ')));
    quotaBody.appendChild(head);

    var monthly = view.monthly;
    if (monthly && monthly.limit_reported && monthly.known) {
      quotaBody.appendChild(meterRow(
        S.monthly,
        money(monthly.used) + ' / ' + money(monthly.limit),
        monthly.percent + '%',
        monthly.percent,
        monthly.exhausted ? (S.exhausted || '') : '',
        monthly.exhausted
      ));
    } else if (monthly && monthly.remaining_reported) {
      quotaBody.appendChild(meterRow(S.monthly, money(monthly.remaining), '', 0, '', false));
    }
    (view.windows || []).forEach(function (window) {
      var label = window.key === 'five_hour' ? S.window_5h : S.window_weekly;
      var note = window.reset_at ? ((S.reset_at || '') + ' ' + localDateTime(window.reset_at)) : '';
      quotaBody.appendChild(meterRow(
        label,
        money(window.used) + ' / ' + money(window.cap),
        window.percent + '%',
        window.percent,
        note,
        window.exceeded
      ));
    });

    if (monthly) {
      var quotaTiles = element('div', 'quota-grid');
      quotaTiles.appendChild(tile(S.quota_limit, monthly.limit_reported ? money(monthly.limit) : '—'));
      quotaTiles.appendChild(tile(S.quota_used, monthly.known ? money(monthly.used) : '—'));
      quotaTiles.appendChild(tile(S.quota_remaining, monthly.remaining_reported ? money(monthly.remaining) : '—'));
      quotaTiles.appendChild(tile(S.quota_purchased, monthly.purchased_reported ? money(monthly.purchased) : '—'));
      quotaTiles.appendChild(tile(S.quota_free, monthly.free_reported ? money(monthly.free) : '—'));
      quotaBody.appendChild(quotaTiles);
    }
    if (view.usage) {
      var stats = element('div', 'quota-grid');
      stats.appendChild(tile(S.usage_requests, String(view.usage.requests), view.usage.failed + ' ' + S.usage_failed));
      stats.appendChild(tile(S.usage_success, view.usage.success_rate + '%'));
      stats.appendChild(tile(S.usage_spend, moneyExact(view.usage.cost), money(view.usage.credits) + ' credits'));
      stats.appendChild(tile(
        S.usage_tokens,
        tokens(view.usage.tokens_in + view.usage.tokens_out),
        tokens(view.usage.tokens_in) + ' ' + S.tokens_in + ' / ' + tokens(view.usage.tokens_out) + ' ' + S.tokens_out
      ));
      quotaBody.appendChild(stats);
    }
    if (!view.monthly && !(view.windows || []).length && !view.usage) {
      quotaBody.appendChild(element('p', 'muted small', S.quota_empty || ''));
    }
    (view.failures || []).forEach(function (failure) {
      quotaBody.appendChild(element('p', 'muted small', failure));
    });
  }

  function openQuota(account) {
    quotaTitle.textContent = account.label || account.file;
    quotaBody.innerHTML = '';
    quotaBody.appendChild(element('p', 'muted small', S.loading || ''));
    quotaOverlay.hidden = false;
    call('quota', { file: account.file }).then(function (result) {
      quotaBody.innerHTML = '';
      if (!result || result.ok === false) {
        quotaBody.appendChild(element('p', 'muted small', (result && result.error) || S.failed));
        return;
      }
      renderQuota(result.data || {});
    });
  }
```

在 `renderRow` 的 `} else {` 分支里、编辑按钮之前插入额度按钮：

```js
      var quotaBtn = document.createElement('button');
      quotaBtn.className = 'btn ghost';
      quotaBtn.textContent = S.quota;
      quotaBtn.addEventListener('click', function () { openQuota(account); });
      actions.appendChild(quotaBtn);
```

把既有 Escape 处理替换为同时关闭两个弹层：

```js
  document.getElementById('quotaClose').addEventListener('click', function () { quotaOverlay.hidden = true; });
  quotaOverlay.addEventListener('click', function (event) { if (event.target === quotaOverlay) quotaOverlay.hidden = true; });
  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    if (!quotaOverlay.hidden) { quotaOverlay.hidden = true; return; }
    if (!overlay.hidden) closeModal();
  });
```

6）`management_test.go` 追加：

```go
func TestAccountsPageHasQuotaPanel(t *testing.T) {
	html := AccountsPageHTML("zh")
	for _, want := range []string{`id="quotaOverlay"`, `id="quotaBody"`, `id="quotaClose"`, `.meter`, `call('quota'`} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if !strings.Contains(html, "额度") {
		t.Error("page is missing the zh quota copy")
	}
	english := AccountsPageHTML("en")
	if !strings.Contains(english, "Quota") || !strings.Contains(english, "5-hour window") {
		t.Error("page is missing the en quota copy")
	}
}
```

#### Reason

需求 1/2 的界面落点；复用现有弹层/请求/双语/CSS 变量体系，不引入前端依赖、不改页面既有写操作。

#### Risk

前端代码全部用 `textContent` 构建（无 HTML 注入）；额度弹层为只读，不受 `writeEnabled` 影响；新增 CSS 只用现有变量。

#### Verification

- [ ] 1. 写失败测试（`TestAccountsPageHasQuotaPanel`）
- [ ] 2. 运行：`go test ./internal/commandcode/ -run TestAccountsPage -v`，预期 FAIL
- [ ] 3. 实现 `page.go` 改动
- [ ] 4. 运行同一命令，预期 PASS
- [ ] 5. 提交：`git add internal/commandcode/page.go internal/commandcode/management_test.go && git commit -m "feat(quota): add the per-account quota panel to the account page"`

---

### Step 8: 文档修订、版本与产物

#### Target

- 修改：`docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md`（附录 B / 追溯矩阵）
- 修改：`README.md`（能力与职责边界）
- 修改：`internal/commandcode/provider.go`（`Version`）
- 修改：`store/registry-entry.json`（版本与 checksum）
- 修改：`dist/`（重建产物）

#### Current Behavior

- 旧计划 `docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md:420-422` 把“额度/健康能力”列为明确不做；追溯矩阵没有额度行。
- `README.md` 的职责边界表没有“凭据额度”行；账号页说明没有额度入口。
- `Version = "0.1.2"`，`store/registry-entry.json` 与 `dist/commandcode-v0.1.2-windows-amd64.zip` 对应。

#### Change

1）旧计划附录 B 删除“额度/健康能力”（保留其余不改），在附录 A 追溯矩阵追加两行：

```markdown
| 账号页展示月额度/5H 窗口/每周窗口与用量 | Task（新计划 Step 3/6/7） | Step 3 / 6 / 7 |
| CPAMP 凭证管理显示 CommandCode 额度（Codex 同机制） | Task（新计划 Step 4/5） | Step 4 / 5 + 部署冒烟 |
```

并在执行记录追加一行指向本计划文件。

2）`README.md`：账号页操作表加“额度”一行；“与 CPA / CPAMP 的职责边界”表加一行 `CommandCode 凭据额度（读取/归一化/页面与 CPAMP 展示）| 本插件`；配置表无需改动（无新键）。

3）`provider.go`：`var Version = "0.2.0"`。

4）构建与产物（按仓库既有发布流程）：

```bash
gofmt -l .                     # 预期无输出
go vet ./...                   # 预期通过
go test ./internal/...         # 预期全绿
make build-windows             # 产出 dist/commandcode.dll
cd dist && sha256sum commandcode.* > checksums.txt
```

更新 `store/registry-entry.json`：`version` 与 windows artifact 的 `url`/`sha256`/`size`（linux/darwin 若本机无法交叉编译则维持占位，并在 README/DEPLOY 中保持既有说明）。

#### Reason

需求文档与代码必须一致；插件通过 CPAMP 安装/升级依赖 registry entry 的版本与校验值。

#### Risk

本机只有 Windows 目标 C 编译器，linux/darwin 产物无法本地生成（与既有计划记录一致，保持占位并说明）。

#### Verification

- [ ] 1. 修订文档与版本号
- [ ] 2. 运行：`gofmt -l . && go vet ./... && go test ./internal/...`，预期全部通过
- [ ] 3. 运行：`make build-windows`，预期产出 `dist/commandcode.dll`
- [ ] 4. `sha256sum dist/commandcode.dll` 与 registry entry 对齐后更新
- [ ] 5. 提交：`git add README.md docs/superpowers/plans/ internal/commandcode/provider.go store/registry-entry.json dist/ && git commit -m "docs(quota): document the quota capability and cut v0.2.0"`

---

## 4. Changed Files Tracking

| File | Action | Reason |
| ---- | ------ | ------ |
| `internal/commandcode/quota.go` | Add | `/alpha` 额度取数（whoami → 并行三端点）、解析、60s 缓存、页面视图 `buildQuotaView` 与派生值 |
| `internal/commandcode/quota_provider.go` | Add | `quota.identifier/describe/fetch/reset` handlers 与 `QuotaFetchResponseFor` 宿主归一化映射 |
| `internal/commandcode/quota_test.go` | Add | 假上游四端点、解析/部分失败/全失败/缓存/未知套餐、视图、映射、handlers 单测 |
| `internal/commandcode/provider.go` | Modify | 新增 `EndpointWhoami/UsageSummary/BillingCredits/BillingSubscriptions`；`Version` 0.1.2 → 0.2.0 |
| `internal/commandcode/rpc.go` | Modify | `capabilities.QuotaProvider` 注册为 true；分发 4 个 `quota.*` 方法 |
| `internal/commandcode/management.go` | Modify | 新增只读 `?action=quota`（`loadAccountByFile` → `FetchQuotaReport` → `buildQuotaView`） |
| `internal/commandcode/page.go` | Modify | 账号行「额度」按钮、额度弹层、进度条/卡片样式、23 个中英文案键 |
| `internal/commandcode/rpc_test.go` | Modify | 断言注册能力含 `quota_provider` |
| `internal/commandcode/management_test.go` | Modify | `?action=quota`（含只读模式、未知文件）与页面额度面板测试 |
| `README.md` | Modify | 账号页操作表、职责边界表、能力清单补充额度说明 |
| `docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md` | Modify | 附录 B 移出“额度不做”、追溯矩阵与执行记录补额度行 |
| `store/registry-entry.json` | Modify | version 0.2.0、windows 产物 url/sha256/size、描述与 tags |
| `dist/commandcode.dll`、`dist/commandcode-v0.2.0.dll`、`dist/commandcode-v0.2.0-windows-amd64.zip`、`dist/commandcode-v0.2.0.dll.sha256` | Add | 重建的 v0.2.0 Windows 发布产物（`dist/` 被 `.gitignore` 忽略，不入库） |
| `backups/2026-10-07-quota/` | Add | 执行前对将被修改文件的备份（`*.go.bak` + README/registry 原件；不入库） |

> 上表为本次 Coding 的**实际**结果（含未入库的构建产物与备份）。

---

## 5. Verification Checklist

**主流程验证**
- [x] `go test ./internal/commandcode/ -run TestFetchQuotaReport -v` 全绿（四端点解析、部分失败、全失败、缓存、未知套餐）
- [x] `go test ./internal/commandcode/ -run 'TestBuildQuotaView|TestQuotaFetchResponseFor' -v` 全绿（页面视图与 CPAMP 映射）
- [x] `go test ./internal/commandcode/ -run 'TestAccountsQuotaAction|TestAccountsPageHasQuotaPanel' -v` 全绿（页面动作与页面结构）
- [x] 本地假上游 + 假 host 核对：`?action=quota&file=commandcode-abc.json` 返回月额度 40.64/70（58%）、5H 2.18/14（16%）、每周 10.07/35（29%）、请求 14779 + 19 失败

**关联流程验证**
- [x] `Registration()` 含 `"quota_provider":true`（`TestRegistrationCapabilities`）
- [x] `HandleMethod("quota.identifier" | "quota.describe" | "quota.fetch" | "quota.reset")` 均返回宿主可解析帧
- [ ] 部署环境（非本地，本环境无 CPAMP/真实上游）：`GET /v0/management/quota/providers` 出现 commandcode；`POST /v0/management/quota/fetch` 返回归一化额度；auth-files 里该凭据 `supports_quota=true`；CPAMP 凭证管理可打开额度

**边界情况验证**
- [x] 无效 key（401）→ 四端点全失败 → 明确错误、不缓存、不显示假数据
- [x] 单端点 500 → 其余数据照常返回，`failures` 显示该端点
- [x] 未知 planId → 只显示剩余，无比例条
- [x] `cap=0`（无上限窗口）→ 不画比例（Percent=0 / RemainingFraction=0）
- [x] `exceeded=true` / `resetAt=0` 的渲染分支在页面实现（`meter.bad` / 空重置时间）
- [x] `monthlyCredits` 缺失（`monthly_reported=false`）→ 显示 `—`，不显示 100% 已用
- [x] `allow-page-write=false` → `action=quota` 仍可用（`TestAccountsQuotaAction` 以只读配置执行）
- [x] API key 不出现在 URL、页面源码、日志字段中（沿用现有约定，未新增日志）

**Test / Build / Lint / Typecheck**
- [x] `gofmt -l`：无输出
- [x] `go vet ./...`：通过
- [x] `go test ./internal/... -count=1`：全绿
- [x] `go build -buildmode=c-shared -ldflags "-X commandcode-cliproxy-plugin/internal/commandcode.Version=0.2.0" -o dist/commandcode.dll .`：通过
- [x] `nm -g dist/commandcode.dll | grep cliproxy`：四个 ABI 符号存在；`strings` 确认版本戳 0.2.0

---

## 6. Plan Adjustment（执行期变更记录）

> 执行中发现“影响范围扩大 / 原 Plan 不准确 / 需要额外文件”时，**暂停编码**，在此记录并等待确认后再继续。

| 日期 | 原计划 | 新发现 | 调整内容 | 确认 |
| ---- | ------ | ------ | -------- | ---- |
| 2026-10-07 | Step 4 测试断言 `currentPeriodEnd: 1792272000000` → `2026-10-19T16:00:00Z` | 该字面量实际是 `2026-10-17T21:20:00Z`，计划中的换算写错 | 仅修正测试字面量为 `1792425600000`（= 2026-10-19T16:00:00Z）；实现（ms → RFC3339）不变 | 自证（实现与断言一致后通过） |
| 2026-10-07 | Step 8 用 `make build-windows` | 本环境 bash 无 `make`；且 Makefile `VERSION ?= 0.1.0` 会把产物盖成 0.1.0 | 直接执行 Makefile 底层命令：`go build -buildmode=c-shared -ldflags "-X ...Version=0.2.0" -o dist/commandcode.dll .` | 自证（产物版本戳 0.2.0、ABI 符号存在） |
| 2026-10-07 | D1 备份保留 `backups/2026-10-07-quota/*.go` | 备份的 `.go` 文件会被 `go vet ./...` 当成同模块的包（undefined: RuntimeConfig） | 备份文件重命名为 `*.go.bak`（内容不变，仍保留在 `backups/`） | 自证（`go vet ./...` 恢复通过） |

---

## 7. Execution Log

> 每完成一个 Step 追加记录（Modified / Changes / Reason / Verification）。

### Step 1 Completed

Modified: `internal/commandcode/provider.go`、`internal/commandcode/quota.go`(Add)、`internal/commandcode/quota_test.go`(Add)
Changes: 4 个 `/alpha` 端点常量；套餐→月额度表 + 最长前缀解析；`recordValue/floatValue/floatValueOr/periodEndMillis/quotaPercent/clampFraction/rfc3339Millis`
Reason: 固定上游契约与纯函数，便于 TDD
Verification: `go test -run 'TestSubscriptionPlanInfoLongestPrefix|TestQuotaPercentAndFraction'` PASS

### Step 2 Completed

Modified: `internal/commandcode/quota.go`、`internal/commandcode/quota_test.go`
Changes: `QuotaReport` 等类型；`FetchQuotaReport`（whoami → 并行 usage/credits/subscriptions、30s 上限、单端点失败降级、60s 缓存）；解析函数与缓存
Reason: 四个上游端点取数与容错是页面与 CPAMP 的共同数据源
Verification: `go test -run TestFetchQuotaReport -v` 5 项 PASS

### Step 3 Completed

Modified: `internal/commandcode/quota.go`、`internal/commandcode/quota_test.go`
Changes: `quotaView/quotaMonthlyView/quotaWindowView/quotaUsageView` 与 `buildQuotaView/buildQuotaMonthlyView/buildQuotaWindowView`
Reason: 页面只做格式化，派生逻辑可测
Verification: `go test -run TestBuildQuotaView -v` 3 项 PASS

### Step 4 Completed

Modified: `internal/commandcode/quota_provider.go`(Add)、`internal/commandcode/quota_test.go`
Changes: `QuotaFetchResponseFor`（Subscription + Summary + monthly/5h/weekly buckets）与 `quotaBucket/quotaCurrencyMetric`
Reason: 宿主只认归一化 `QuotaFetchResponse`
Verification: `go test -run TestQuotaFetchResponseFor -v` 2 项 PASS（含计划中时间戳字面量修正）

### Step 5 Completed

Modified: `internal/commandcode/quota_provider.go`、`internal/commandcode/rpc.go`、`internal/commandcode/quota_test.go`、`internal/commandcode/rpc_test.go`
Changes: 4 个 quota RPC handlers；`capabilities.quota_provider=true` 注册；方法分发
Reason: CPAMP 通过注册能力发现并路由额度请求；`quota.reset` 必须存在
Verification: `go test -run 'TestQuota|TestHandleMethodDispatchesQuota|TestRegistrationCapabilities' -v` 8 项 PASS

### Step 6 Completed

Modified: `internal/commandcode/management.go`、`internal/commandcode/management_test.go`
Changes: 只读 `?action=quota`；未知文件错误分支测试、只读模式测试
Reason: 需求 1 的页面数据入口
Verification: `go test -run TestAccountsQuotaAction -v` 2 项 PASS

### Step 7 Completed

Modified: `internal/commandcode/page.go`、`internal/commandcode/management_test.go`
Changes: 额度按钮 + 弹层 + 进度条/卡片样式 + 中英文案；`renderQuota/openQuota`
Reason: 需求 1/2 的界面落点
Verification: `go test -run 'TestAccountsPage|TestAccountsQuotaAction' -v` 5 项 PASS；`node --check`（提取 `pageRuntime`）exit=0

### Step 8 Completed

Modified: `internal/commandcode/provider.go`、`README.md`、`docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md`、`store/registry-entry.json`、`dist/`（产物）
Changes: 版本 0.2.0；文档修订（附录 B/追溯矩阵/README 能力与职责边界）；windows 产物 zip+sha256+size 写入 registry entry
Reason: 需求文档一致性与发布物
Verification: `gofmt -l` 无输出；`go vet ./...` 通过；`go test ./internal/... -count=1` 全绿；c-shared 构建成功（版本戳 0.2.0、ABI 符号存在）
