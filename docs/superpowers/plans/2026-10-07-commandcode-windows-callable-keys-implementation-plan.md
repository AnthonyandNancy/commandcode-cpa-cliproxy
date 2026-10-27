# Implementation Plan

**Feature:** CommandCode 窗口内联与可调用密钥（含 `upstream idle for 30s` 调查）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 每个 Step 完成后必须：勾选步骤、更新 `## 4. Changed Files Tracking`、在 `## 7. Execution Log` 追加记录。执行中发现范围扩大或 Plan 不准，先写 `## 6. Plan Adjustment` 并暂停等待确认。

**Goal:** 在 CommandCode CPA 插件内完成三件事：调查流式 HTTP 500（`upstream idle for 30s`）根因并给出最小处置；账号页每个账号项内联显示 5 小时/每周窗口；新增“可调用密钥”CRUD，并在执行器入口只放行列表内的 CPAMP `api-keys`。

**Architecture:** 窗口数据复用既有 `FetchQuotaReport`（60s 缓存）与 `buildQuotaWindowView`，只新增一个批量子路由；可调用密钥不落明文，只存 `caller_scope` 哈希（宿主已把调用方 key 映射成不可逆 `caller_scope` 下发到执行器 Metadata），来源列表按宿主同款规则定位 `config.yaml` 的 `api-keys` 只读解析；准入判断放在插件执行器入口，不新增宿主能力、不改 CPA/CPAMP。

**Tech Stack:** Go 1.26（业务与测试纯 Go，无 cgo）、插件 SDK `github.com/router-for-me/CLIProxyAPI/v8 v8.0.17`、`gopkg.in/yaml.v3`（已在 go.mod）、页面为无构建步骤的原生 HTML/CSS/JS。

## Global Constraints

- 只改本插件仓库；不得修改 CPA/CPAMP 源码。
- 不新增第三方依赖；`go.mod` / `go.sum` 保持现状。
- API key 只进请求头：不得出现在 URL、日志、页面明文；页面与日志只允许掩码（`MaskKey`）与哈希。
- 账号页文案必须中英双语（`accountPageCopy` 的 `zh` 与 `en` 两份都要加），样式只用 CPAMP 注入的 CSS 变量。
- 账号页所有交互沿用单路由 `?action=` 约定，不新增资源路径。
- 账号页写操作受 `allow-page-write` 门禁；读操作不受影响。
- 测试必须纯 Go（`go test ./internal/...`），不依赖真实网络、CPA 或 CPAMP。
- 需求 3 的“来源”是 CPAMP 认证配置（CPA `config.yaml` 的 `api-keys`）。插件 SDK 没有任何读取宿主配置的 RPC（已核实 `pluginabi` 的 `host.*` 白名单），因此来源读取采用“同进程内按宿主同款规则定位 config.yaml 并只读解析”；读取失败时页面明示原因并禁止新增，运行时策略见 Step 6。

---

## 1. Goal

### 本次修改目标

1. **需求 1（调查）**：定位 `HTTP 500 upstream stream failed: upstream idle for 30s` 的产生链路与触发条件，用可复现的单测固化“空闲判定”语义，并给出“是否改码、改哪里”的结论；结论确认后按选定选项执行最小修复。
2. **需求 2（窗口内联）**：账号页每个账号 item 下方显示该账号的“5 小时窗口”和“每周窗口”（用量/上限、百分比、超限、重置时间），数据与“额度”弹层、CPAMP quota provider 同源。
3. **需求 3（可调用密钥）**：账号页新增“可调用密钥”区块（查/增/改/删），只有列表内且启用的 CPAMP `api-keys` 才能调用本插件模型；其他密钥调用时按常规报错（403，不冷却、不换号）。

### 不包含

- 不实现模型目录按密钥过滤（非名单密钥仍能在 `/v1/models` 看到模型，仅调用被拒）。
- 不做密钥轮换/过期时间/配额绑定（YAGNI）。
- 不实现配置文件的写入：来源列表只读 CPAMP 写出的 config.yaml，插件只在自有状态文件里维护“子集”。
- 不新增 `request_interceptor` 等宿主能力；不动账号凭据文件格式。
- 不修改 CPA/CPAMP 源码、不改 `store/registry-entry.json`、不新增依赖、不做无关重构。
- 需求 1 在结论确认前不改任何生产代码（Step 1 只加 characterization 测试与诊断记录）。

### 成功标准

- `make test`（`go test ./internal/...`）、`go vet ./...`、`gofmt -l .` 全部干净；按目标平台 `make build-windows`（或 linux/darwin）能产出 c-shared。
- 本地 httptest 验证：账号页 HTML 含窗口容器与 `call('windows')`；`?action=windows` 返回每个活跃账号的 `five_hour`/`weekly` 摘要，单账号失败不影响其他账号。
- 本地 httptest 验证：`?action=keys` 能列出 config.yaml 中的 api-keys（掩码 + 是否启用）；`key_add/key_update/key_delete` 行为正确且受 `allow-page-write` 门禁；非来源列表的 key 添加被拒。
- 本地 httptest 验证：执行器在 `caller_scope` 命中启用条目时放行；未命中/缺失且列表非空时返回 HTTP 403 请求级错误；列表为空时保持原行为（不限制）。
- 需求 1 的产出：`stream_test.go` 固化空闲语义 + `## 7. Execution Log` 记录诊断结论与选定选项。

## 2. Current Understanding

### 当前实现方式

**需求 1 链路（已核实）**
- 默认空闲 30s：`provider.go:64`（`DefaultStreamIdleTimeout = 30 * time.Second`），装载于 `config.go:54` 与 `config.go:169-173`（配置键 `stream-idle-timeout`）。
- 计时语义：`stream.go:292-351` `consumeUpstream` 用 `ReadBytes('\n')` 按“完整行”推进；计时器在读第一字节前就启动（`stream.go:294`），每收到一行重置（`stream.go:328-335`），超时返回 `upstream idle for %s`（`stream.go:347-348`）。**首字节等待计入空闲；“只收到半行/无换行”也计入空闲。**
- 流式失败上报：`executor.go:231-239` 异步流式分支打日志 `warn "upstream stream failed"` 并 `EmitStreamError(streamID, classifyStreamError(...).Error())`；`classifyStreamError`（`executor.go:256-263`）把非 `*UpstreamError` 包装成 `zeroOutputError{"upstream stream failed: " + err}`。
- HTTP 500 来源：插件流式阶段错误帧不携带 HTTP 状态，宿主按默认 500 处理（SDK `pluginabi.Error` 注释：HTTPStatus 为 0 时 CPA 默认 500），且流式 RPC 已返回成功，宿主无法再换号。
- 非流式/内联分支（`executor.go:151/204-217`）同样返回该错误，但走 RPC 错误通道并带 429 语义（`zeroOutputError.StatusCode()=429`），宿主可冷却换号。

**需求 2 链路（已核实）**
- 取数：`quota.go:220-287` `FetchQuotaReport`（whoami 串行 + usage/credits/subscriptions 并行），60s 内存缓存（`quota.go:19/409-431`）；窗口解析 `quota.go:344-379`，DTO `quota.go:469-476`，视图 `buildQuotaView`/`buildQuotaWindowView`（`quota.go:488-556`）。
- 页面现状：`action=data` 返回 `accountView`（`management.go:42-58`，无额度字段）；卡片 `renderRow`（`page.go:581-658`）只渲染名称/状态/掩码/优先级/冷却；窗口只在“额度”弹层（`page.go:482-561`）出现。
- 可复用：`FetchQuotaReport`、`buildQuotaWindowView`、quota 缓存、前端 `meterRow`、文案 `window_5h`/`window_weekly`。

**需求 3 链路（已核实）**
- CPA 客户端认证配置 = config.yaml 顶层 `api-keys`（`internal/config/sdk_config.go:63`，v8 别名 `access.api-keys`）；运行时注册为 `config-api-key` provider，认证后 `Principal` 就是 key 原文；管理 API 为 `/v0/management/api-keys`（GET/PUT/PATCH/DELETE）。
- 宿主下发调用方身份：`sdk/api/handlers/handlers.go:243-244` 写入 `caller_scope = sha256("cli-proxy-api:caller-scope:v1\x00" + key)`（`sdk/cliproxy/session/identity.go:210`），经 `mergeExecutorMetadata` 进入执行器请求 Metadata（`internal/pluginhost/adapters_executors.go:1054`）。插件已读 Metadata（`executor.go:45-60`）。
- 宿主 RPC 白名单（`pluginabi/types.go:95-114`）没有配置读取接口；`/v0/resource/plugins/*` 页面路由在宿主侧不经管理鉴权（`internal/api/server_management.go:292-308`）。
- 宿主的 config 路径发现算法（`cmd/server/main.go:853-882`）：`-config/--config/-config=/--config=`，否则 `cwd/config.yaml`。插件与宿主同进程，可直接复用同一算法。
- 现状：插件没有任何按调用方 key 的准入逻辑；不存在“可调用密钥”代码。

### 涉及模块

| 文件 | 角色 |
|---|---|
| `internal/commandcode/management.go` | 账号页单路由 `?action=` 分发；新增 `windows` 与 `keys/key_add/key_update/key_delete` |
| `internal/commandcode/page.go` | 账号页 HTML/CSS/JS 与双语文案；新增窗口内联、可调用密钥区块 |
| `internal/commandcode/quota.go` | 额度取数与窗口视图（复用，原则上不改） |
| `internal/commandcode/executor.go` | 执行器入口；新增可调用密钥准入（需求 3）；需求 1 选项 C 时改调用签名 |
| `internal/commandcode/stream.go` | 空闲计时；需求 1 选项 C 的修改点 |
| `internal/commandcode/config.go` | 配置装载；需求 1 选项 C 新增首字节超时字段 |
| `internal/commandcode/provider.go` | 默认常量；需求 1 选项 B/C 的默认值 |
| `internal/commandcode/callable.go`（新增） | 来源读取（config.yaml）、状态文件读写、`caller_scope` 计算与准入策略 |
| `internal/commandcode/*_test.go` | 纯 Go 单测：`stream_test.go`（新增）、`callable_test.go`（新增）、`management_test.go`（扩展） |
| `README.md` | 功能/配置文档（窗口展示位置、可调用密钥、需求 1 结论涉及默认值时更新） |

### 数据流 / 调用链

- 列表：页面 `load()` → `?action=data` → `listAccountViews()`（host.auth.list + host.auth.get）→ 渲染卡片。
- 窗口（新增）：页面 `load()` 后 → `?action=windows` → 逐账号 `accountRawJSON` + `ParseAccount` → `FetchQuotaReport`（60s 缓存）→ `buildQuotaWindowView` → 卡片内联。
- 可调用密钥（新增）：页面 `?action=keys` → `configuredAPIKeys()`（读 config.yaml）+ `loadCallableKeys()`（读状态文件）→ 掩码候选列表；写操作经 `key_add/key_update/key_delete` → `saveCallableKeys()`。
- 准入（新增）：客户端请求 → CPA 认证（api-keys）→ 宿主写 `caller_scope` 到 Metadata → 本插件 `executor.execute(_stream)` → `allowCaller(metadata)` → 放行/403。

### 已确认的影响范围

- **直接修改**：`management.go`、`page.go`、`executor.go`、新增 `callable.go`、测试文件；需求 1 结论确认后可能改 `provider.go`/`config.go`/`stream.go`。
- **关联检查**：quota 缓存三处消费（卡片/弹层/CPAMP）、`action=data` 兼容性、removed 文件扫描不被状态文件干扰、现有 executor 测试的 Metadata 夹具、README。
- **确认无需修改**：`plugin.go`、`host.go`、`account.go`、`identity.go`、`envelope.go`、`models.go`、`upstream.go`、`quota_provider.go`、`rpc.go`（能力集不变）、`store/registry-entry.json`、`Makefile`。

### 双窗口缓存与成本说明

`FetchQuotaReport` 一次读取 4 个 `/alpha` 端点，但同一 key 60s 内命中缓存；`windows` 批量动作有界并发（4）并为每个账号设置 15s 超时，单账号失败只影响该账号的显示。

---

## 3. Implementation Steps

### Step 1: 需求 1 调查 — 用单测固化“空闲判定”语义

#### Target

- 新增：`internal/commandcode/stream_test.go`
- 生产代码：无修改

#### Current Behavior

- 无任何直接测试覆盖 `consumeUpstream` 的空闲判定；超时语义只由 `executor_test.go` / `acceptance_test.go` 间接覆盖。
- 已知语义（`stream.go:292-351`）：计时从流开始（首字节前）启动；每收到一条完整行重置；半行不重置。

#### Change

新增 4 个 characterization 测试（固定现有行为，不追求先红后绿；**使用当前签名 `consumeUpstream(body, idle, emit)`**，Step 2 若选选项 C 再按该 Step 更新为双超时签名）：

```go
package commandcode

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestUpstreamIdleFiresWithoutData(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 30ms") {
		t.Fatalf("err = %v, want the idle error", err)
	}
}

func TestUpstreamIdleResetsOnEveryCompleteLine(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
			time.Sleep(5 * time.Millisecond)
		}
		_ = writer.Close()
	}()

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	chunks := 0
	err := translator.consumeUpstream(reader, time.Second, func([]byte) bool {
		chunks++
		return true
	})
	if err != nil {
		t.Fatalf("consumeUpstream error = %v", err)
	}
	if chunks != 3 {
		t.Fatalf("chunks = %d, want 3", chunks)
	}
}

func TestUpstreamIdleTreatsPartialLineAsSilence(t *testing.T) {
	reader, writer := io.Pipe()
	// A write without a newline never completes ReadBytes('\n'); the write
	// itself lands in the pipe buffer and the goroutine returns.
	go func() { _, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"no-newline\"}") }()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 30ms") {
		t.Fatalf("err = %v, want the partial-line idle error", err)
	}
}

func TestUpstreamIdleAfterFirstLineUsesTheSameTimer(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
		_, _ = io.WriteString(writer, "\n")
	}()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 40*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 40ms") {
		t.Fatalf("err = %v, want the inter-chunk idle error", err)
	}
}
```

> 说明：这 4 个用例在 Step 1 是“现状固化”；Step 2 选 **选项 C** 时更新为双超时签名（首字节静默 → `upstream produced no data for ...`，行间静默 → `upstream idle for ...`，代码见 Step 2 选项 C）；选 **选项 B/A** 时保持单签名不变。

- [ ] **Step 1.1: 写诊断记录模板并采集部署证据（人工）**

在 `## 7. Execution Log` 记录（缺部署环境时标注“本地无法采集，待部署环境补充”）：

```
- 时间窗：<发生时间>
- 触发模型：<model>
- 首次输出前静默 / 中途静默：<分类>
- 客户端是否收到部分内容：<是/否>
- CPA 日志行：<warn upstream stream failed ...>
- 该时段上游/网络异常：<有/无>
- 结论：<预首字节静默 / 中途静默 / 半行误判 / 上游故障>
```

- [ ] **Step 1.2: 写 `stream_test.go`（使用上面 Change 中的 4 个用例，当前签名）**

保存为 `internal/commandcode/stream_test.go`（package `commandcode`，imports：`io`、`strings`、`testing`、`time`）。

- [ ] **Step 1.3: 运行测试并确认现状语义**

Run: `go test ./internal/commandcode/ -run TestUpstreamIdle -v`
Expected: PASS；`-count=5` 复跑稳定。同时通过阅读 `stream.go:292-351` 确认结论：“首字节等待计入空闲；半个 SSE 行不算活动；行间空档与首字节共用 `stream-idle-timeout`”。

- [ ] **Step 1.4: 提交（仅测试与计划文档）**

```bash
git add internal/commandcode/stream_test.go docs/superpowers/plans/2026-10-07-commandcode-windows-callable-keys-implementation-plan.md
git commit -m "test: characterize upstream idle semantics"
```

#### Reason

需求 1 是“先调查后决策”。先固化语义与证据，避免凭猜测调参；测试同时是后续修复的回归网。

#### Risk

- 部分测试用 `time.Sleep`，在极慢 CI 上可能抖动；阈值已取毫秒级并留足余量（`5ms` 步进 vs `30-1000ms` 超时）。
- 诊断记录依赖部署环境日志；本地环境无法访问 `commandcode.ai` 与 CPAMP 时只能记录模板。

#### Verification

- `go test ./internal/commandcode/ -run TestUpstreamIdle -v` 通过。
- `## 7. Execution Log` 有诊断记录（或明确标注“待部署环境补充”）。

---

### Step 2: 需求 1 修复 — 按 Step 1 结论执行且只执行一个选项

#### Target

- 选项 A（零代码）：插件配置 `stream-idle-timeout`
- 选项 B（改默认值）：`internal/commandcode/provider.go`、`README.md`
- 选项 C（拆分首字节超时）：`provider.go`、`config.go`、`stream.go`、`executor.go`、`stream_test.go`、`config_test.go`、`README.md`

#### Current Behavior

- 默认 30s 空闲即失败；首字节等待与行间空档共用同一阈值；流式阶段失败只能以 500 结束（无法换号）。

#### Change

**先决条件**：Step 1 结论 + 用户确认后，只执行下面一个选项。

**选项 A：配置侧处置（不写代码）**
- 在面板 `plugins.configs.commandcode` 设置 `stream-idle-timeout: 2m`（或按日志证据取值），验证错误文案变为 `upstream idle for 2m0s` 且不再触发。
- 仅更新 `README.md`：在 `stream-idle-timeout` 行补充“遇到长思考模型可按需调大”。
- 验证命令：`go test ./internal/commandcode/ -run TestConfig`（确保配置键解析正常）。

**选项 B：调大默认值（最小代码变更）**

```go
// provider.go:63-65
	// DefaultStreamIdleTimeout / DefaultNonStreamIdleTimeout bound upstream silence.
	// Stream idle is deliberately generous: reasoning models can think for minutes
	// before the first byte, and a stream that is killed after it started cannot be
	// retried transparently by CPA.
	DefaultStreamIdleTimeout    = 120 * time.Second
	DefaultNonStreamIdleTimeout = 90 * time.Second
```

`README.md` 配置表把 `stream-idle-timeout` 默认值改为 `120s`。
现有测试引用常量（`config_test.go:29`），无需改测试。

**选项 C：首字节与行间空闲拆分（长思考模型可见收益）**

`provider.go` 新增常量：

```go
	// DefaultStreamFirstByteTimeout bounds the wait for the first upstream SSE
	// line. It is separate from the inter-chunk idle timeout because reasoning
	// models can stay silent for minutes before the first token.
	DefaultStreamFirstByteTimeout = 5 * time.Minute
```

`config.go`：`RuntimeConfig` 增加 `StreamFirstByteTimeout time.Duration`；`DefaultConfig()` 设默认；`yamlConfig` 增加 `StreamFirstByteTimeout *string \`yaml:"stream-first-byte-timeout"\``；`ApplyConfigYAML` 增加：

```go
	if parsed.StreamFirstByteTimeout != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(*parsed.StreamFirstByteTimeout)); err == nil && d > 0 {
			next.StreamFirstByteTimeout = d
		}
	}
```

`ConfigFields()` 增加：

```go
		{Name: "stream-first-byte-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "流式首字节超时 (stream first-byte timeout)"},
```

`stream.go`：把 `consumeUpstream(body, idle, emit)` 改为 `consumeUpstream(body, firstByte, idle, emit)`：

```go
func (t *streamTranslator) consumeUpstream(body io.Reader, firstByte, idle time.Duration, emit func([]byte) bool) error {
	if firstByte <= 0 {
		firstByte = idle
	}
	reader := bufio.NewReaderSize(body, 64*1024)
	timer := time.NewTimer(firstByte)
	defer timer.Stop()
	sawLine := false

	type readResult struct {
		line []byte
		err  error
		done bool
	}
	results := make(chan readResult)
	stop := make(chan struct{})
	defer close(stop)

	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case results <- readResult{line: line}:
				case <-stop:
					return
				}
			}
			if err != nil {
				select {
				case results <- readResult{err: err, done: true}:
				case <-stop:
				}
				return
			}
		}
	}()

	for {
		select {
		case result := <-results:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			sawLine = true
			timer.Reset(idle)
			if result.done {
				if result.err == io.EOF {
					return nil
				}
				return result.err
			}
			if chunk, ok := t.translateLine(result.line); ok && chunk != nil {
				if !emit(chunk) {
					return errClientGone
				}
			}
		case <-timer.C:
			if sawLine {
				return fmt.Errorf("upstream idle for %s", idle)
			}
			return fmt.Errorf("upstream produced no data for %s", firstByte)
		}
	}
}
```

`foldStream` 同步加参数：

```go
func foldStream(body io.Reader, model, completionID string, created int64, firstByte, idle time.Duration) ([]byte, usageInfo, error) {
	translator := newStreamTranslator(completionID, model, created)
	// ...原 payload/chunks 逻辑不变...
	err := translator.consumeUpstream(body, firstByte, idle, func(chunk []byte) bool {
		// ...原回调不变...
	})
	// ...原收尾不变...
}
```

`executor.go` 三处调用改为：

```go
	payload, _, errFold := foldStream(resp.Body, req.Model, completionID, time.Now().Unix(), cfg.StreamFirstByteTimeout, cfg.NonStreamIdleTimeout)
```

```go
		errStream := translator.consumeUpstream(resp.Body, cfg.StreamFirstByteTimeout, cfg.StreamIdleTimeout, func(chunk []byte) bool {
```

```go
		errStream := translator.consumeUpstream(resp.Body, cfg.StreamFirstByteTimeout, cfg.StreamIdleTimeout, func(chunk []byte) bool {
```

`config_test.go` 增加解析断言：

```go
	// inside TestApplyConfigYAML ... (与 stream-idle-timeout 同行级)
	if cfg.StreamFirstByteTimeout != 3*time.Minute {
		t.Errorf("StreamFirstByteTimeout = %s", cfg.StreamFirstByteTimeout)
	}
```

并在 YAML 夹具加入 `stream-first-byte-timeout: 3m`。

`stream_test.go` 的 4 个用例更新为双超时签名（替换 Step 1 的对应实现）：

```go
func TestUpstreamIdleFiresWithoutData(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 2*time.Second, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream produced no data for 2s") {
		t.Fatalf("err = %v, want the pre-first-byte error", err)
	}
}

func TestUpstreamIdleResetsOnEveryCompleteLine(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
			time.Sleep(5 * time.Millisecond)
		}
		_ = writer.Close()
	}()

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	chunks := 0
	err := translator.consumeUpstream(reader, time.Second, 30*time.Millisecond, func([]byte) bool {
		chunks++
		return true
	})
	if err != nil {
		t.Fatalf("consumeUpstream error = %v", err)
	}
	if chunks != 3 {
		t.Fatalf("chunks = %d, want 3", chunks)
	}
}

func TestUpstreamIdleTreatsPartialLineAsSilence(t *testing.T) {
	reader, writer := io.Pipe()
	go func() { _, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"no-newline\"}") }()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, time.Second, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream produced no data for 1s") {
		t.Fatalf("err = %v, want the partial-line pre-first-byte error", err)
	}
}

func TestUpstreamIdleAfterFirstLineReportsInterChunkSilence(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
		_, _ = io.WriteString(writer, "\n") // a keepalive line still counts as activity
	}()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, time.Second, 40*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 40ms") {
		t.Fatalf("err = %v, want the inter-chunk idle error", err)
	}
}
```

`README.md`：配置表新增 `stream-first-byte-timeout` 行（默认 `5m`），并说明流式空闲拆分为“首字节/行间”。

- [ ] **Step 2.1: 写入选项对应的代码/文档**
- [ ] **Step 2.2: 运行相关测试**

Run: `go test ./internal/commandcode/ -run 'TestUpstreamIdle|TestConfig' -v`
Expected: PASS。

- [ ] **Step 2.3: 提交**

```bash
git add internal/commandcode/provider.go internal/commandcode/config.go internal/commandcode/stream.go internal/commandcode/executor.go internal/commandcode/stream_test.go internal/commandcode/config_test.go README.md
git commit -m "fix: give the upstream stream a first-byte grace period"
```

（选项 A/B 相应缩减 `git add`。）

#### Reason

500 的根因是“30s 无完整行即判死”，而长思考模型的静默常常发生在首字节前。拆分阈值能保住换号无法生效的流式路径，同时不牺牲行间卡死的检测。

#### Risk

- 选项 B 会把所有流式卡死的最坏等待从 30s 拉到 120s；只影响失败请求，不影响正常延迟。
- 选项 C 新增配置键；老配置不受影响（缺省用默认值），但 `consumeUpstream` 签名变化会同时影响内联流式路径，需三处调用全改。
- 选项 A 依赖部署环境手动改配置，无法在本地验证上游行为。

#### Verification

- `go test ./internal/commandcode/ -run 'TestUpstreamIdle|TestConfig|TestExecutor'` 通过。
- 新增/修改的断言覆盖：首字节静默→首字节错误、首行后静默→行间错误、只有换行 keepalive→不判死。
- 部署冒烟（非本地完成条件）：长思考请求不再 500；若仍触发，日志中的 `reason` 能区分两类超时文案。

---

### Step 3: 需求 2 后端 — `?action=windows`

#### Target

- 修改：`internal/commandcode/management.go`
  - `handleManagementRequest` 增加 `case action == "windows"`
  - 新增：`windowsFetchTimeout`、`windowsConcurrency`、`accountWindowsView`、`fetchAccountWindows`、`loadAccountForView`
  - 复用改造：`loadAccountByFile` 内部改用 `loadAccountForView`

#### Current Behavior

- `?action=quota&file=...` 单账号返回完整 `quotaView`（含 `windows`）；列表 `?action=data` 不含额度字段；页面只能打开弹层逐账号查看。

#### Change

`management.go` 的 `handleManagementRequest` 中，在 `case action == "quota":` 之后插入：

```go
	case action == "windows":
		// Read-only, like the quota dialog: the card rows must not require the
		// page-write switch. One slow account degrades to an {error} entry.
		views, errList := listAccountViews()
		if errList != nil {
			return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errList.Error()})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{
			"windows": fetchAccountWindows(context.Background(), views, cfg),
		}})
```

在 `accountView` 定义之后新增：

```go
// windowsFetchTimeout bounds one account's window lookup inside the page's
// batch call: the list must render even when a single upstream is slow.
const windowsFetchTimeout = 15 * time.Second

// windowsConcurrency bounds how many accounts are queried at once so a large
// pool cannot burst the upstream account endpoints.
const windowsConcurrency = 4

// accountWindowsView is the compact window summary rendered under one account
// item. A nil window is omitted (unreported stays unseen, mirroring the dialog).
type accountWindowsView struct {
	FiveHour *quotaWindowView `json:"five_hour,omitempty"`
	Weekly   *quotaWindowView `json:"weekly,omitempty"`
	Error    string           `json:"error,omitempty"`
}
```

在 `listAccountViews` 之后新增：

```go
// fetchAccountWindows collects the 5h/weekly windows for every active account.
// Failures are per account: the page shows "—" for the broken row instead of
// failing the whole batch.
func fetchAccountWindows(ctx context.Context, views []accountView, cfg RuntimeConfig) map[string]accountWindowsView {
	out := map[string]accountWindowsView{}
	var mu sync.Mutex
	sem := make(chan struct{}, windowsConcurrency)
	var wg sync.WaitGroup

	for _, view := range views {
		if view.Removed || strings.TrimSpace(view.AuthIndex) == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(view accountView) {
			defer wg.Done()
			defer func() { <-sem }()

			result := accountWindowsView{}
			account, errLoad := loadAccountForView(view)
			if errLoad != nil {
				result.Error = errLoad.Error()
			} else {
				reqCtx, cancel := context.WithTimeout(ctx, windowsFetchTimeout)
				report, errFetch := FetchQuotaReport(reqCtx, account, cfg)
				cancel()
				if errFetch != nil {
					result.Error = errFetch.Error()
				} else {
					if report.Credits.FiveHour != nil {
						window := buildQuotaWindowView("five_hour", report.Credits.FiveHour)
						result.FiveHour = &window
					}
					if report.Credits.Weekly != nil {
						window := buildQuotaWindowView("weekly", report.Credits.Weekly)
						result.Weekly = &window
					}
				}
			}

			mu.Lock()
			out[view.File] = result
			mu.Unlock()
		}(view)
	}
	wg.Wait()
	return out
}

// loadAccountForView resolves one listed credential through the host so the API
// key never leaves the plugin.
func loadAccountForView(view accountView) (Account, error) {
	raw, errGet := accountRawJSON(view.AuthIndex)
	if errGet != nil {
		return Account{}, errGet
	}
	account, handled, errParse := ParseAccount(raw)
	if errParse != nil {
		return Account{}, errParse
	}
	if !handled {
		return Account{}, fmt.Errorf("credential does not belong to the %s provider", ProviderID)
	}
	return account, nil
}
```

`loadAccountByFile` 内循环体简化为复用（行为不变）：

```go
	for _, view := range views {
		if !strings.EqualFold(view.File, file) || view.AuthIndex == "" {
			continue
		}
		account, errLoad := loadAccountForView(view)
		if errLoad != nil {
			return Account{}, file, errLoad
		}
		return account, view.File, nil
	}
```

导入调整：`management.go` 增加 `"sync"`（已有 `"sync/atomic"`，两者共存）。

- [ ] **Step 3.1: 按上述片段修改 `management.go`**
- [ ] **Step 3.2: 编译与既有测试**

Run: `go test ./internal/commandcode/ -run 'TestAccounts|TestManagement' -v`
Expected: PASS（`action=quota` 与 `action=data` 行为不变）。

- [ ] **Step 3.3: 提交**

```bash
git add internal/commandcode/management.go
git commit -m "feat: expose per-account 5h/weekly windows for the account list"
```

#### Reason

需求 2 要求窗口出现在每个账号 item 下方；后端一次批量返回可复用 60s 缓存，并保持“卡片先渲染、窗口后填充”的响应体验。

#### Risk

- 首次加载（缓存冷）会并发触发 `min(4, N)` 个账号 × 4 端点的上游查询；15s 超时后单账号降级为 `error`。
- removed 账号没有 `AuthIndex`，天然跳过。
- `loadAccountByFile` 的实现替换属于同一职责的复用，不改变外部行为。

#### Verification

- 新增单测（Step 5）断言：活跃账号返回两个窗口、removed 文件不在结果中、上游失败账号带 `error` 且整体 `ok=true`。
- 手工：`GET <page>?action=windows` 返回 JSON 形状 `{"ok":true,"data":{"windows":{"commandcode-xxx.json":{...}}}}`。

---

### Step 4: 需求 2 前端 — 卡片内联窗口

#### Target

- 修改：`internal/commandcode/page.go`
  - `pageStyle` 增加窗口容器样式
  - `renderRow` 增加窗口容器
  - `load()` 触发 `renderWindows()`
  - 新增 `renderWindows()` 函数

#### Current Behavior

- `renderRow` 只在 `info` 内渲染 name + meta；窗口仅存在于额度弹层。

#### Change

`pageStyle` 内 `.account .meta` 之后追加：

```
.account .windows { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 8px; margin-top: 8px; }
.account .windows .meter-row { margin: 0; }
.account .windows .meter { height: 6px; }
```

`renderRow` 在 `info.appendChild(meta);` 之后追加：

```js
    if (!account.removed) {
      var windows = document.createElement('div');
      windows.className = 'windows';
      windows.setAttribute('data-file', account.file);
      info.appendChild(windows);
    }
```

`pageRuntime` 内新增：

```js
  // Windows are loaded after the list so a slow upstream never delays the
  // account rows: every card gets a placeholder that fills in when the batch
  // call returns.
  function renderWindows() {
    var hosts = {};
    Array.prototype.forEach.call(document.querySelectorAll('.account .windows'), function (node) {
      hosts[node.getAttribute('data-file')] = node;
    });
    if (!Object.keys(hosts).length) return;
    call('windows').then(function (result) {
      var payload = (result && result.data) || {};
      var windows = payload.windows || {};
      Object.keys(hosts).forEach(function (file) {
        var host = hosts[file];
        host.innerHTML = '';
        var view = windows[file] || {};
        var list = [];
        if (view.five_hour) list.push(view.five_hour);
        if (view.weekly) list.push(view.weekly);
        if (!list.length) {
          var empty = element('div', 'muted small', '—');
          if (view.error) empty.title = view.error;
          host.appendChild(empty);
          return;
        }
        list.forEach(function (window) {
          var label = window.key === 'five_hour' ? S.window_5h : S.window_weekly;
          var note = window.reset_at ? ((S.reset_at || '') + ' ' + localDateTime(window.reset_at)) : '';
          host.appendChild(meterRow(
            label,
            money(window.used) + ' / ' + money(window.cap),
            window.percent + '%',
            window.percent,
            note,
            window.exceeded
          ));
        });
      });
    });
  }
```

`load()` 内 `accounts.forEach(...)` 渲染完成后追加：

```js
        renderWindows();
```

- [ ] **Step 4.1: 修改 `page.go`**
- [ ] **Step 4.2: 页面断言测试先行**

在 `management_test.go` 的 `TestAccountsPageHasQuotaPanel` 后新增：

```go
func TestAccountsPageHasInlineWindows(t *testing.T) {
	html := AccountsPageHTML("zh")
	for _, want := range []string{"windows.className = 'windows'", "call('windows')", ".account .windows", "renderWindows"} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}
```

> 断言字符串与 Step 4 的实现片段一一对应；如果实现时改了变量样式，同步调整断言。

- [ ] **Step 4.3: 运行页面测试**

Run: `go test ./internal/commandcode/ -run TestAccountsPage -v`
Expected: PASS。

- [ ] **Step 4.4: 提交**

```bash
git add internal/commandcode/page.go internal/commandcode/management_test.go
git commit -m "feat: render 5h/weekly windows under each account row"
```

#### Reason

需求 2 的展示位置在卡片内；复用 `meterRow` 与既有文案保证与弹层视觉一致、无新样式体系。

#### Risk

- 每个活跃账号一次批量请求；无账号时不会调用。
- `window.used/cap` 缺少时不渲染该行（与弹层一致），显示 “—” 并保留 `title` 里的失败原因。

#### Verification

- 页面 HTML 含 `.windows` 容器、`call('windows')`、`renderWindows`。
- 手工：打开账号页，卡片下方出现 5H/每周窗口；点击额度弹层内容不回归。

---

### Step 5: 需求 2 测试 — `windows` 路由覆盖

#### Target

- 修改：`internal/commandcode/management_test.go`

#### Current Behavior

- 已有 `TestAccountsQuotaAction` 覆盖单账号弹层数据；无批量 windows 覆盖。

#### Change

新增（复用 `quotaFixtureHandler`、`newAuthStubHost`、`accountAction`、`decodeManagement`、`mustFrame`）：

```go
func TestAccountsWindowsAction(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	upstream := httptest.NewServer(quotaFixtureHandler(t))
	defer upstream.Close()

	host := newAuthStubHost(t, map[string]string{
		"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890","base_url":"` + upstream.URL + `"}`,
		"commandcode-def.json": `{"type":"commandcode","api_key":"user_test0987654321","base_url":"` + upstream.URL + `"}`,
	})
	// A removed credential must not produce a window entry.
	if err := os.WriteFile(filepath.Join(host.dir, "commandcode-old.json.removed-1"), []byte(`{"type":"commandcode","api_key":"user_old123"}`), 0o600); err != nil {
		t.Fatalf("write removed fixture: %v", err)
	}

	response := decodeManagement(t, mustFrame(t, accountAction("windows", nil, nil)))
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Windows map[string]struct {
				FiveHour *struct {
					Key     string `json:"key"`
					Percent int    `json:"percent"`
				} `json:"five_hour"`
				Weekly *struct {
					Key string `json:"key"`
				} `json:"weekly"`
				Error string `json:"error"`
			} `json:"windows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("windows body decode: %v", err)
	}
	if !payload.OK {
		t.Fatalf("windows action failed: %s", response.Body)
	}
	abc, ok := payload.Data.Windows["commandcode-abc.json"]
	if !ok || abc.FiveHour == nil || abc.FiveHour.Key != "five_hour" || abc.FiveHour.Percent != 16 {
		t.Fatalf("abc windows = %+v, want five_hour 16%%", abc)
	}
	if abc.Weekly == nil || abc.Weekly.Key != "weekly" {
		t.Fatalf("abc weekly = %+v, want the weekly window", abc.Weekly)
	}
	if _, ok := payload.Data.Windows["commandcode-def.json"]; !ok {
		t.Error("the second active account is missing from the batch")
	}
	if _, ok := payload.Data.Windows["commandcode-old.json.removed-1"]; ok {
		t.Error("a removed credential produced a window entry")
	}
}
```

新增上游失败降级用例：

```go
func TestAccountsWindowsActionDegradesPerAccount(t *testing.T) {
	resetQuotaCache()
	t.Cleanup(resetQuotaCache)

	upstream := httptest.NewServer(quotaFixtureHandler(t))
	defer upstream.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	newAuthStubHost(t, map[string]string{
		"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890","base_url":"` + upstream.URL + `"}`,
		"commandcode-dead.json": `{"type":"commandcode","api_key":"user_dead1234567890","base_url":"` + dead.URL + `"}`,
	})

	response := decodeManagement(t, mustFrame(t, accountAction("windows", nil, nil)))
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Windows map[string]struct {
				FiveHour *struct{ Key string } `json:"five_hour"`
				Error    string                `json:"error"`
			} `json:"windows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("windows body decode: %v", err)
	}
	if !payload.OK {
		t.Fatalf("a single broken account must not fail the batch: %s", response.Body)
	}
	if payload.Data.Windows["commandcode-abc.json"].FiveHour == nil {
		t.Error("the healthy account lost its window")
	}
	if entry := payload.Data.Windows["commandcode-dead.json"]; entry.Error == "" {
		t.Errorf("the broken account = %+v, want an error entry", entry)
	}
}
```

- [ ] **Step 5.1: 写失败测试**

Run: `go test ./internal/commandcode/ -run TestAccountsWindows -v`
Expected: FAIL（`action windows` 未实现时返回 `unknown action`）。

- [ ] **Step 5.2: 确认 Step 3 实现后重跑**

Run: `go test ./internal/commandcode/ -run TestAccountsWindows -v`
Expected: PASS。

- [ ] **Step 5.3: 提交**

```bash
git add internal/commandcode/management_test.go
git commit -m "test: cover the account windows batch endpoint"
```

#### Reason

需求 2 的验收点是“每个账号 item 显示窗口”，必须有批量路由的成功/降级/removed 三类覆盖。

#### Risk

- 依赖 `quotaFixtureHandler` 的固定数值（five_hour 16%）；修改夹具时需同步。
- 并发测试中 quota 缓存是包级全局，用例前后必须 `resetQuotaCache()`。

#### Verification

- 两个新用例稳定通过（可加 `-count=3` 复跑）。

---

### Step 6: 需求 3 后端 — `callable.go`（来源、存储、准入）

#### Target

- 新增：`internal/commandcode/callable.go`

#### Current Behavior

- 无来源读取、无状态文件、无准入策略代码。

#### Change

新增完整文件：

```go
package commandcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// callableKeysFileName is the plugin-owned subset of the panel's api-keys that
// may call CommandCode models. The name must not end in .json: CPA treats every
// *.json file in the auth directory as a credential.
const callableKeysFileName = "commandcode-callable-keys.data"

// callerScopeMetadataKey mirrors sdk/cliproxy/executor.CallerScopeMetadataKey.
// The literal matches the host contract the same way canonical_session_id does
// in executor.go.
const callerScopeMetadataKey = "caller_scope"

// callableKeyEntry stores the facts the runtime needs without the raw key: the
// host's irreversible caller_scope is the matching value, KeyHash is the stable
// id, and Masked is all the page ever shows.
type callableKeyEntry struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Masked    string `json:"masked"`
	Label     string `json:"label,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type callableKeysFile struct {
	Version int                `json:"version"`
	Keys    []callableKeyEntry `json:"keys"`
}

var (
	callableKeysMu     sync.RWMutex
	callableKeysCache  *callableKeysFile
	callableKeysSource string
	callableKeysStamp  time.Time
)

// callerScopeFor mirrors sdk/cliproxy/session.CallerScope, the host's
// irreversible namespace for one client API key. Contract test:
// TestCallerScopeMatchesHost.
func callerScopeFor(apiKey string) string {
	value := strings.TrimSpace(apiKey)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("cli-proxy-api:caller-scope:v1\x00" + value))
	return hex.EncodeToString(sum[:])
}

// configFilePath returns the host's config.yaml path. It mirrors the host's own
// plugin bootstrap discovery (cmd/server) so the plugin reads exactly the file
// the panel writes: -config/--config flags first, then ./config.yaml.
// Tests replace it to point at a fixture.
var configFilePath = defaultConfigFilePath

func defaultConfigFilePath() string {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-config" || args[i] == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(args[i], "-config="):
			return strings.TrimPrefix(args[i], "-config=")
		case strings.HasPrefix(args[i], "--config="):
			return strings.TrimPrefix(args[i], "--config=")
		}
	}
	if wd, errGetwd := os.Getwd(); errGetwd == nil {
		return filepath.Join(wd, "config.yaml")
	}
	return "config.yaml"
}

type configAPIKeys struct {
	APIKeys []string `yaml:"api-keys"`
	Access  struct {
		APIKeys []string `yaml:"api-keys"`
	} `yaml:"access"`
}

// configuredAPIKeys reads the auth api-keys list the panel manages. Both the
// flat key and the v8 access.api-keys alias are accepted; order is preserved and
// duplicates removed. The keys stay in memory only, never in a URL or log.
func configuredAPIKeys() ([]string, error) {
	path := strings.TrimSpace(configFilePath())
	if path == "" {
		return nil, fmt.Errorf("config path is unknown")
	}
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), errRead)
	}
	var parsed configAPIKeys
	if errUnmarshal := yaml.Unmarshal(data, &parsed); errUnmarshal != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", filepath.Base(path), errUnmarshal)
	}
	seen := map[string]bool{}
	keys := []string{}
	for _, list := range [][]string{parsed.APIKeys, parsed.Access.APIKeys} {
		for _, key := range list {
			key = strings.TrimSpace(key)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func callableKeysPath() (string, error) {
	dir := accountDir()
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("auth directory is unknown")
	}
	return filepath.Join(dir, callableKeysFileName), nil
}

// loadCallableKeys reads the state file with an mtime cache. A missing file is
// an empty list, not an error.
func loadCallableKeys() (*callableKeysFile, error) {
	path, errPath := callableKeysPath()
	if errPath != nil {
		return nil, errPath
	}
	stamp := time.Time{}
	if info, errStat := os.Stat(path); errStat == nil {
		stamp = info.ModTime()
	} else if !os.IsNotExist(errStat) {
		return nil, errStat
	}

	callableKeysMu.RLock()
	if callableKeysCache != nil && callableKeysSource == path && callableKeysStamp.Equal(stamp) {
		file := callableKeysCache
		callableKeysMu.RUnlock()
		return file, nil
	}
	callableKeysMu.RUnlock()

	file := &callableKeysFile{Version: 1, Keys: []callableKeyEntry{}}
	if data, errRead := os.ReadFile(path); errRead == nil {
		if errDecode := json.Unmarshal(data, file); errDecode != nil {
			return nil, fmt.Errorf("callable keys file is unreadable: %w", errDecode)
		}
	} else if !os.IsNotExist(errRead) {
		return nil, errRead
	}
	if file.Keys == nil {
		file.Keys = []callableKeyEntry{}
	}

	callableKeysMu.Lock()
	callableKeysCache = file
	callableKeysSource = path
	callableKeysStamp = stamp
	callableKeysMu.Unlock()
	return file, nil
}

func saveCallableKeys(file *callableKeysFile) error {
	path, errPath := callableKeysPath()
	if errPath != nil {
		return errPath
	}
	file.Version = 1
	if file.Keys == nil {
		file.Keys = []callableKeyEntry{}
	}
	data, errMarshal := json.MarshalIndent(file, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	if errWrite := os.WriteFile(path, append(data, '\n'), 0o600); errWrite != nil {
		return errWrite
	}
	if info, errStat := os.Stat(path); errStat == nil {
		callableKeysMu.Lock()
		callableKeysCache = file
		callableKeysSource = path
		callableKeysStamp = info.ModTime()
		callableKeysMu.Unlock()
	}
	return nil
}

// allowCaller enforces the callable-keys list for requests that reached the
// CommandCode executor.
//
// Policy:
//   - empty list: enforcement is off (backward compatible rollout; the page
//     shows "no restriction" so the state is never silent);
//   - non-empty list: only an enabled entry whose caller_scope matches passes,
//     a request without caller_scope is denied;
//   - the state file cannot be located/parsed: allow and log a warning, because
//     failing closed would take the provider down with the file.
func allowCaller(metadata map[string]any) error {
	scope := ""
	if metadata != nil {
		if value, ok := metadata[callerScopeMetadataKey].(string); ok {
			scope = strings.TrimSpace(value)
		}
	}
	file, errLoad := loadCallableKeys()
	if errLoad != nil {
		Logf("warn", "callable keys unavailable; request allowed", map[string]any{"reason": errLoad.Error()})
		return nil
	}
	if len(file.Keys) == 0 {
		return nil
	}
	if scope == "" {
		return callableKeyDenied()
	}
	for _, entry := range file.Keys {
		if entry.Disabled || entry.Scope == "" {
			continue
		}
		if entry.Scope == scope {
			return nil
		}
	}
	return callableKeyDenied()
}

func callableKeyDenied() error {
	return &downstreamError{
		status:  http.StatusForbidden,
		code:    "callable_key_required",
		message: "this API key is not allowed to call CommandCode models",
	}
}
```

- [ ] **Step 6.1: 新增文件并编译**

Run: `go build ./... && go vet ./internal/commandcode/`
Expected: 通过（`allowCaller` 暂未被调用也允许存在，但 go vet 不应报错）。

- [ ] **Step 6.2: 提交**

```bash
git add internal/commandcode/callable.go
git commit -m "feat: add callable-key source, store and policy"
```

#### Reason

准入需要“来源（CPAMP）+ 子集（插件）+ 运行时匹配（caller_scope）”三块；把它们收敛在一个文件里，执行器只调用 `allowCaller`。

#### Risk

- `configFilePath` 是包级变量（可注入测试）；生产路径发现逻辑与宿主一致，但 home/远程配置部署没有本地 config.yaml → 来源不可用（页面明示，运行时不受影响）。
- 状态文件不可定位/不可解析时策略为放行 + 警告（避免单点故障拖垮 provider）；页面会暴露该错误，便于管理员修复。
- `callerScopeFor` 的常量字符串与宿主版本绑定；Step 10 的契约测试会与 `sdk/cliproxy/session.CallerScope` 对比，宿主升级时该测试会先爆。

#### Verification

- `go build ./...`、`go vet ./internal/commandcode/` 通过。
- Step 10 的 `TestCallerScopeMatchesHost`、`TestAllowCallerPolicy` 通过。

---

### Step 7: 需求 3 运行时接入 — 执行器准入

#### Target

- 修改：`internal/commandcode/executor.go`
  - `handleExecute`
  - `handleExecuteStream`

#### Current Behavior

- 两个入口在 `json.Unmarshal` 后直接 `prepareExecutor` 并访问上游，没有任何调用方准入。

#### Change

`handleExecute`：

```go
	cfg := CurrentConfig()
	if errAllowed := allowCaller(req.Metadata); errAllowed != nil {
		return nil, errAllowed
	}
	account, profile, envelope, errPrepare := prepareExecutor(&req, cfg)
```

`handleExecuteStream`：

```go
	cfg := CurrentConfig()
	if errAllowed := allowCaller(req.Metadata); errAllowed != nil {
		return nil, errAllowed
	}
	account, profile, envelope, errPrepare := prepareExecutor(&req.ExecutorRequest, cfg)
```

注意顺序：准入在 `initialize`（指纹/lifecycle 预请求）之前，被拒请求不会产生任何上游流量。

- [ ] **Step 7.1: 修改 `executor.go`**
- [ ] **Step 7.2: 运行执行器测试**

Run: `go test ./internal/commandcode/ -run 'TestExecutor|TestAcceptance' -v`
Expected: PASS（没有状态文件时列表为空 → 放行，既有行为不变）。

- [ ] **Step 7.3: 提交**

```bash
git add internal/commandcode/executor.go
git commit -m "feat: reject non-callable keys at the CommandCode executor"
```

#### Reason

执行器是唯一能精确判断“请求确实路由到本插件模型”的位置；错误带 403 状态即请求级错误，宿主不会换号/冷却，符合“按常规报错”。

#### Risk

- 流式与非流式路径都拦截；被拒请求不会调用 `initialize`。
- 若未来某请求没有 `caller_scope`（CPA 未启用 api-keys），且列表非空 → 403；这是“只有名单内密钥可调用”的必然结果。

#### Verification

- 既有执行器/验收测试全绿（空列表放行）。
- Step 10 的 `TestExecutorDeniesNonCallableKey` 断言 403 且未访问上游。

---

### Step 8: 需求 3 管理端 — `keys/key_add/key_update/key_delete`

#### Target

- 修改：`internal/commandcode/management.go`
  - `handleManagementRequest` 新增 4 个分支
  - 新增 `callableKeyCandidateView` 与 4 个 handler

#### Current Behavior

- 管理端只有账号动作与 `quota`/`windows`（Step 3 后）；无密钥列表动作。

#### Change

`handleManagementRequest` 的 `case action == "windows":` 之后插入：

```go
	case action == "keys":
		return handleCallableKeysList(cfg)
	case action == "key_add":
		return handleCallableKeyAdd(req, cfg, lang)
	case action == "key_update":
		return handleCallableKeyUpdate(req, cfg, lang)
	case action == "key_delete":
		return handleCallableKeyDelete(req, cfg, lang)
```

文件末尾（`parseBoolQuery` 之前）新增：

```go
// callableKeyCandidateView is one row the page renders: a key currently present
// in the panel config plus whether the plugin lets it call CommandCode models.
// The raw key never reaches the browser; Masked is all the page sees.
type callableKeyCandidateView struct {
	ID       string `json:"id"`
	Masked   string `json:"masked"`
	Allowed  bool   `json:"allowed"`
	Stale    bool   `json:"stale,omitempty"`
	Label    string `json:"label,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

func handleCallableKeysList(cfg RuntimeConfig) ([]byte, error) {
	stored, errLoad := loadCallableKeys()
	if errLoad != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
	}
	byID := map[string]callableKeyEntry{}
	for _, entry := range stored.Keys {
		byID[entry.ID] = entry
	}

	source, errSource := configuredAPIKeys()
	data := map[string]any{
		"write_enabled": cfg.AllowPageWrite,
		"keys":          []callableKeyCandidateView{},
	}
	if errSource != nil {
		data["source_error"] = errSource.Error()
		return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: data})
	}

	candidates := make([]callableKeyCandidateView, 0, len(source))
	seen := map[string]bool{}
	for _, apiKey := range source {
		id := KeyHash(apiKey)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		entry, allowed := byID[id]
		candidates = append(candidates, callableKeyCandidateView{
			ID:       id,
			Masked:   MaskKey(apiKey),
			Allowed:  allowed,
			Label:    entry.Label,
			Disabled: entry.Disabled,
		})
	}
	// Entries whose key is no longer in the panel config stay visible as stale
	// so the page can offer cleanup instead of hiding them.
	for _, entry := range stored.Keys {
		if seen[entry.ID] {
			continue
		}
		candidates = append(candidates, callableKeyCandidateView{
			ID: entry.ID, Masked: entry.Masked, Allowed: true, Stale: true,
			Label: entry.Label, Disabled: entry.Disabled,
		})
	}
	data["keys"] = candidates
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: data})
}

func handleCallableKeyAdd(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	apiKey := strings.TrimSpace(req.Headers.Get("X-CommandCode-Callable-Key"))
	if apiKey == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "key_required")})
	}
	source, errSource := configuredAPIKeys()
	if errSource != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errSource.Error()})
	}
	if !containsString(source, apiKey) {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "callable_not_in_source")})
	}

	file, errLoad := loadCallableKeys()
	if errLoad != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
	}
	entry := callableKeyEntry{
		ID:        KeyHash(apiKey),
		Scope:     callerScopeFor(apiKey),
		Masked:    MaskKey(apiKey),
		Label:     strings.TrimSpace(req.Query.Get("label")),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	replaced := false
	for i := range file.Keys {
		if file.Keys[i].ID != entry.ID {
			continue
		}
		entry.CreatedAt = file.Keys[i].CreatedAt
		file.Keys[i] = entry
		replaced = true
		break
	}
	if !replaced {
		file.Keys = append(file.Keys, entry)
	}
	if errSave := saveCallableKeys(file); errSave != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errSave.Error()})
	}
	Logf("info", "commandcode callable key saved", map[string]any{"key": entry.Masked})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: entry})
}

func handleCallableKeyUpdate(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	id := strings.TrimSpace(req.Query.Get("id"))
	if id == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "callable_missing")})
	}
	file, errLoad := loadCallableKeys()
	if errLoad != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
	}
	for i := range file.Keys {
		if file.Keys[i].ID != id {
			continue
		}
		file.Keys[i].Label = strings.TrimSpace(req.Query.Get("label"))
		file.Keys[i].Disabled = parseBoolQuery(req.Query.Get("disabled"))
		if errSave := saveCallableKeys(file); errSave != nil {
			return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errSave.Error()})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: file.Keys[i]})
	}
	return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "callable_missing")})
}

func handleCallableKeyDelete(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	id := strings.TrimSpace(req.Query.Get("id"))
	if id == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "callable_missing")})
	}
	file, errLoad := loadCallableKeys()
	if errLoad != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
	}
	kept := file.Keys[:0]
	removed := false
	for _, entry := range file.Keys {
		if entry.ID == id {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "callable_missing")})
	}
	file.Keys = kept
	if errSave := saveCallableKeys(file); errSave != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errSave.Error()})
	}
	Logf("info", "commandcode callable key removed", map[string]any{"id": id})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"id": id}})
}
```

- [ ] **Step 8.1: 修改 `management.go`**
- [ ] **Step 8.2: 编译**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 8.3: 提交**

```bash
git add internal/commandcode/management.go
git commit -m "feat: add callable-key CRUD to the account page API"
```

#### Reason

来源校验（必须存在于 config.yaml 的 api-keys）只能在服务端做；掩码 + 哈希存储满足“密钥不进页面”的既有约定。

#### Risk

- 成功添加/删除会清空该 key 的 quota/模型缓存吗？不需要：凭证未变，缓存键与调用方无关。
- `file.Keys[:0]` 复用底层数组后立即序列化，逻辑安全；后续如需保留旧切片应复制。

#### Verification

- Step 10 的 `TestCallableKeysManagementCRUD` 覆盖：列表候选、来源外添加被拒、添加后可调用、删除、`allow-page-write=false` 拒绝写。

---

### Step 9: 需求 3 页面 — “可调用密钥”区块与双语文案

#### Target

- 修改：`internal/commandcode/page.go`
  - `accountPageCopy` 的 `zh`/`en`
  - `AccountsPageHTML`（新增 section）
  - `pageStyle`（新增样式）
  - `pageScriptLiteral`（新增文案键）
  - `pageRuntime`（新增列表/添加/启用/删除逻辑）

#### Current Behavior

- 页面只有账号列表与额度弹层；没有任何密钥管理 UI。

#### Change

`accountPageCopy["zh"]` 在 `"failed"` 之前追加：

```go
		"callable_title":          "可调用密钥",
		"callable_hint":           "只有列表中的 CPAMP API 密钥可以调用 CommandCode 模型；明文密钥不会显示或保存。来源：CPAMP 认证配置的 api-keys。",
		"callable_add":            "添加",
		"callable_label":          "备注（可选）",
		"callable_empty":          "尚未添加可调用密钥：当前不限制调用。",
		"callable_not_in_source":  "该密钥不在 CPAMP 认证配置的 api-keys 列表中。",
		"callable_source_error":   "无法读取 CPAMP 配置的 api-keys。",
		"callable_allowed":        "允许调用",
		"callable_not_allowed":    "未启用",
		"callable_stale":          "来源已移除",
		"callable_missing":        "该可调用密钥不存在。",
		"callable_remove_confirm": "删除后该密钥将无法调用 CommandCode 模型。确认继续？",
```

`accountPageCopy["en"]` 对应追加：

```go
		"callable_title":          "Callable keys",
		"callable_hint":           "Only the CPAMP API keys listed here may call CommandCode models. Plaintext keys are never shown or stored. Source: the api-keys list in CPAMP auth config.",
		"callable_add":            "Add",
		"callable_label":          "Label (optional)",
		"callable_empty":          "No callable keys yet: calling is currently unrestricted.",
		"callable_not_in_source":  "This key is not in the CPAMP auth api-keys list.",
		"callable_source_error":   "Cannot read the CPAMP api-keys configuration.",
		"callable_allowed":        "Allowed",
		"callable_not_allowed":    "Not enabled",
		"callable_stale":          "Removed from source",
		"callable_missing":        "That callable key does not exist.",
		"callable_remove_confirm": "After deletion this key can no longer call CommandCode models. Continue?",
```

`AccountsPageHTML` 在 footer notes 之前（`// Footer notes.` 之前）插入：

```go
	// Callable keys: only the keys listed here may reach the CommandCode models.
	b.WriteString("<section class=\"card\">\n  <div class=\"section-head\"><h2>")
	b.WriteString(t("callable_title"))
	b.WriteString("</h2><span class=\"muted small\" id=\"callableState\"></span></div>\n  <p class=\"muted small\">")
	b.WriteString(t("callable_hint"))
	b.WriteString("</p>\n  <div class=\"callable-add\">\n    <input id=\"callableKey\" type=\"password\" autocomplete=\"off\" spellcheck=\"false\" placeholder=\"sk-...\">\n    <input id=\"callableLabel\" type=\"text\" autocomplete=\"off\" placeholder=\"")
	b.WriteString(t("callable_label"))
	b.WriteString("\">\n    <button class=\"btn primary\" id=\"callableAdd\">")
	b.WriteString(t("callable_add"))
	b.WriteString("</button>\n  </div>\n  <div id=\"callableList\" class=\"accounts\"></div>\n</section>\n")
```

`pageStyle` 末追加：

```
.callable-add { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
.callable-add input { max-width: 320px; }
```

`pageScriptLiteral` 的 `keys` 列表追加：

```go
		"callable_title", "callable_hint", "callable_add", "callable_label", "callable_empty",
		"callable_not_in_source", "callable_source_error", "callable_allowed", "callable_not_allowed",
		"callable_stale", "callable_missing", "callable_remove_confirm",
```

`pageRuntime` 内（`var quotaBody = ...` 之后）新增变量：

```js
  var callableList = document.getElementById('callableList');
  var callableState = document.getElementById('callableState');
  var callableKeyInput = document.getElementById('callableKey');
  var callableLabelInput = document.getElementById('callableLabel');
```

`pageRuntime` 内新增函数：

```js
  function loadCallableKeys() {
    return call('keys').then(function (result) {
      callableList.innerHTML = '';
      callableState.textContent = '';
      if (!result || result.ok === false) {
        callableState.textContent = (result && result.error) || S.failed;
        return result;
      }
      var payload = result.data || {};
      if (payload.source_error) callableState.textContent = S.callable_source_error;
      var keys = payload.keys || [];
      if (!keys.length) {
        callableList.appendChild(element('p', 'muted small', S.callable_empty || ''));
        return result;
      }
      keys.forEach(function (entry) {
        var row = document.createElement('div');
        row.className = 'account' + (entry.stale || entry.disabled ? ' off' : '');
        var info = document.createElement('div');
        var name = document.createElement('div');
        name.className = 'name';
        name.appendChild(element('span', '', entry.masked || entry.id));
        var pillText = entry.stale ? S.callable_stale : (entry.allowed && !entry.disabled ? S.callable_allowed : S.callable_not_allowed);
        name.appendChild(element('span', 'pill ' + (entry.allowed && !entry.disabled && !entry.stale ? 'ok' : 'grey'), pillText));
        info.appendChild(name);
        if (entry.label) info.appendChild(element('div', 'meta', entry.label));
        row.appendChild(info);

        var actions = document.createElement('div');
        actions.className = 'row-actions';
        function button(text, className, handler) {
          var btn = document.createElement('button');
          btn.className = 'btn ' + (className || '');
          btn.textContent = text;
          btn.disabled = !writeEnabled;
          btn.addEventListener('click', function () {
            btn.disabled = true;
            Promise.resolve(handler()).then(function (res) {
              if (res && res.ok === false) toast(res.error || S.failed);
              return loadCallableKeys();
            }).finally(function () { btn.disabled = !writeEnabled; });
          });
          return btn;
        }
        if (entry.stale) {
          actions.appendChild(button(S.purge, 'danger', function () {
            if (!window.confirm(S.callable_remove_confirm)) return Promise.resolve(null);
            return call('key_delete', { id: entry.id });
          }));
        } else if (entry.allowed) {
          actions.appendChild(button(entry.disabled ? S.enable : S.disable, '', function () {
            return call('key_update', { id: entry.id, disabled: entry.disabled ? '0' : '1', label: entry.label || '' });
          }));
          actions.appendChild(button(S.remove, 'danger', function () {
            if (!window.confirm(S.callable_remove_confirm)) return Promise.resolve(null);
            return call('key_delete', { id: entry.id });
          }));
        } else {
          actions.appendChild(element('span', 'muted small', S.callable_not_allowed));
        }
        row.appendChild(actions);
        callableList.appendChild(row);
      });
      return result;
    });
  }
```

事件绑定（`document.getElementById('refresh')...` 附近）新增：

```js
  document.getElementById('callableAdd').addEventListener('click', function () {
    var key = callableKeyInput.value.trim();
    if (!key) { toast(S.key_required); return; }
    var headers = { 'X-CommandCode-Callable-Key': key };
    call('key_add', { label: callableLabelInput.value.trim() }, { headers: headers }).then(function (result) {
      if (result && result.ok === false) { toast(result.error || S.failed); return; }
      callableKeyInput.value = '';
      callableLabelInput.value = '';
      toast(S.saved || 'saved');
      loadCallableKeys();
    });
  });
```

`load()` 中，在 `if (!accounts.length) { ... } else { ... }` 整块之后、`return result;` 之前追加（**无账号时也要能管理可调用密钥，因此不能放进 else 分支**）：

```js
        loadCallableKeys();
```

- [ ] **Step 9.1: 修改 `page.go`**
- [ ] **Step 9.2: 页面测试**

在 `TestAccountsPageHasInlineWindows` 旁新增：

```go
func TestAccountsPageHasCallableKeys(t *testing.T) {
	zh := AccountsPageHTML("zh")
	for _, want := range []string{`id="callableList"`, `id="callableAdd"`, `call('keys')`, `call('key_add'`, "可调用密钥", "CPAMP 认证配置的 api-keys"} {
		if !strings.Contains(zh, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	en := AccountsPageHTML("en")
	for _, want := range []string{"Callable keys", "call('key_delete'", "not in the CPAMP auth api-keys list"} {
		if !strings.Contains(en, want) {
			t.Errorf("en page is missing %q", want)
		}
	}
	if !strings.Contains(zh, "'X-CommandCode-Callable-Key': key") {
		t.Error("the page must send the callable key as a request header")
	}
}
```

Run: `go test ./internal/commandcode/ -run TestAccountsPage -v`
Expected: PASS。

- [ ] **Step 9.3: 提交**

```bash
git add internal/commandcode/page.go internal/commandcode/management_test.go
git commit -m "feat: add the callable-keys section to the account page"
```

#### Reason

CRUD 的入口在账号页；候选列表由服务端计算（allowed/disabled/stale），页面只做格式化，符合仓库“业务规则在 Go 侧可测”的分层。

#### Risk

- 页面明文 key 只在密码输入框中短暂存在，提交走请求头；不写入 DOM 其他地方。
- 候选但未启用的行没有“一键允许”按钮：浏览器拿不到明文 key，必须粘贴添加。这是既定的密钥安全约定，属于有意为之。

#### Verification

- 页面测试通过；手工验证添加/停用/删除与 toast 提示。

---

### Step 10: 需求 3 测试 — 来源、策略、契约与准入

#### Target

- 新增：`internal/commandcode/callable_test.go`

#### Current Behavior

- 无调用方准入测试。

#### Change

新增完整测试文件：

```go
package commandcode

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/session"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// withConfigFile points the plugin at a fixture config.yaml for one test.
func withConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	previous := configFilePath
	configFilePath = func() string { return path }
	t.Cleanup(func() { configFilePath = previous })
	return path
}

func TestCallerScopeMatchesHost(t *testing.T) {
	for _, key := range []string{"sk-aaa", " user_bbb ", ""} {
		if got, want := callerScopeFor(key), session.CallerScope(key); got != want {
			t.Errorf("callerScopeFor(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestConfiguredAPIKeysReadsBothLayouts(t *testing.T) {
	withConfigFile(t, "api-keys:\n  - sk-flat\n  - sk-shared\naccess:\n  api-keys:\n    - sk-nested\n    - sk-shared\n")
	keys, err := configuredAPIKeys()
	if err != nil {
		t.Fatalf("configuredAPIKeys error = %v", err)
	}
	want := []string{"sk-flat", "sk-shared", "sk-nested"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
}

func TestConfiguredAPIKeysMissingFile(t *testing.T) {
	previous := configFilePath
	configFilePath = func() string { return filepath.Join(t.TempDir(), "missing.yaml") }
	t.Cleanup(func() { configFilePath = previous })
	if _, err := configuredAPIKeys(); err == nil {
		t.Fatal("a missing config file must be reported")
	}
}

func TestDefaultConfigFilePathParsesArgs(t *testing.T) {
	previous := os.Args
	os.Args = []string{"cpa", "--config", "/tmp/panel/config.yaml"}
	t.Cleanup(func() { os.Args = previous })
	if got := defaultConfigFilePath(); got != "/tmp/panel/config.yaml" {
		t.Fatalf("defaultConfigFilePath() = %q", got)
	}
}

func TestAllowCallerPolicy(t *testing.T) {
	restoreDefaults(t)
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})

	allowed := callerScopeFor("sk-allowed")
	if err := allowCaller(nil); err != nil {
		t.Fatalf("an empty list must not restrict: %v", err)
	}
	if err := saveCallableKeys(&callableKeysFile{Keys: []callableKeyEntry{
		{ID: KeyHash("sk-allowed"), Scope: allowed, Masked: MaskKey("sk-allowed")},
		{ID: KeyHash("sk-disabled"), Scope: callerScopeFor("sk-disabled"), Masked: MaskKey("sk-disabled"), Disabled: true},
	}}); err != nil {
		t.Fatalf("save callable keys: %v", err)
	}

	if err := allowCaller(map[string]any{callerScopeMetadataKey: allowed}); err != nil {
		t.Errorf("the allowed scope must pass: %v", err)
	}
	if err := allowCaller(map[string]any{callerScopeMetadataKey: callerScopeFor("sk-disabled")}); err == nil {
		t.Error("a disabled entry must be denied")
	}
	if err := allowCaller(map[string]any{callerScopeMetadataKey: callerScopeFor("sk-other")}); err == nil {
		t.Error("an unknown scope must be denied")
	}
	if err := allowCaller(nil); err == nil {
		t.Error("a missing caller_scope must be denied while the list is non-empty")
	}
	if err := allowCaller(map[string]any{}); err == nil {
		t.Error("an empty metadata map must be denied while the list is non-empty")
	}
}

func TestExecutorDeniesNonCallableKey(t *testing.T) {
	restoreDefaults(t)
	resetInitState()
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})
	if err := saveCallableKeys(&callableKeysFile{Keys: []callableKeyEntry{
		{ID: KeyHash("sk-allowed"), Scope: callerScopeFor("sk-allowed"), Masked: MaskKey("sk-allowed")},
	}}); err != nil {
		t.Fatalf("save callable keys: %v", err)
	}

	_, err := HandleMethod(pluginabi.MethodExecutorExecute, mustJSONRaw(t, map[string]any{
		"AuthID":          "commandcode-abc.json",
		"AuthProvider":    ProviderID,
		"Model":           "m",
		"Payload":         []byte(`{"model":"m","messages":[]}`),
		"OriginalRequest": []byte(`{"model":"m","messages":[]}`),
		"StorageJSON":     []byte(`{"type":"commandcode","api_key":"user_test1234567890"}`),
		"Metadata":        map[string]any{callerScopeMetadataKey: callerScopeFor("sk-other")},
		"Headers":         http.Header{},
	}))
	if got := statusOf(err); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-callable key", got)
	}
}

// decodeManagementOK reads the {ok,error} envelope shared by the page actions.
func decodeManagementOK(t *testing.T, response pluginapi.ManagementResponse) bool {
	t.Helper()
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("management body decode: %v", err)
	}
	return payload.OK
}

func TestCallableKeysManagementCRUD(t *testing.T) {
	restoreDefaults(t)
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})
	withConfigFile(t, "api-keys:\n  - sk-known\n  - sk-other\n")

	// List: both panel keys are candidates, none allowed yet.
	listResponse := decodeManagement(t, mustFrame(t, accountAction("keys", nil, nil)))
	var list struct {
		OK   bool `json:"ok"`
		Data struct {
			SourceError string `json:"source_error"`
			Keys        []struct {
				ID       string `json:"id"`
				Masked   string `json:"masked"`
				Allowed  bool   `json:"allowed"`
				Stale    bool   `json:"stale"`
				Disabled bool   `json:"disabled"`
			} `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResponse.Body, &list); err != nil {
		t.Fatalf("keys body decode: %v", err)
	}
	if !list.OK || list.Data.SourceError != "" {
		t.Fatalf("keys payload = %s", listResponse.Body)
	}
	if len(list.Data.Keys) != 2 || list.Data.Keys[0].Allowed {
		t.Fatalf("keys = %+v, want two candidates, none allowed", list.Data.Keys)
	}
	if list.Data.Keys[0].Masked == "sk-known" {
		t.Fatal("the page must only ever see the masked key")
	}
	id := list.Data.Keys[0].ID

	// Add: the raw key travels in the header, never in the URL.
	add := decodeManagement(t, mustFrame(t, accountAction("key_add", map[string]string{"label": "laptop"},
		http.Header{"X-CommandCode-Callable-Key": []string{"sk-known"}})))
	if !decodeManagementOK(t, add) {
		t.Fatalf("key_add failed: %s", add.Body)
	}

	// A key outside the panel config is rejected.
	rejected := decodeManagement(t, mustFrame(t, accountAction("key_add", nil,
		http.Header{"X-CommandCode-Callable-Key": []string{"sk-unknown"}})))
	if decodeManagementOK(t, rejected) {
		t.Fatalf("key_add accepted an unknown key: %s", rejected.Body)
	}

	// Update: disable the entry.
	if !decodeManagementOK(t, decodeManagement(t, mustFrame(t, accountAction("key_update", map[string]string{"id": id, "disabled": "1"}, nil)))) {
		t.Fatal("key_update failed")
	}

	// Delete: the entry disappears from the allowed set.
	if !decodeManagementOK(t, decodeManagement(t, mustFrame(t, accountAction("key_delete", map[string]string{"id": id}, nil)))) {
		t.Fatal("key_delete failed")
	}
	after := decodeManagement(t, mustFrame(t, accountAction("keys", nil, nil)))
	if err := json.Unmarshal(after.Body, &list); err != nil {
		t.Fatalf("keys body decode: %v", err)
	}
	for _, entry := range list.Data.Keys {
		if entry.ID == id && entry.Allowed {
			t.Fatalf("deleted key is still allowed: %+v", entry)
		}
	}

	// Writes honour allow-page-write=false.
	ApplyConfigYAML([]byte("allow-page-write: false\n"))
	blocked := decodeManagement(t, mustFrame(t, accountAction("key_add", nil,
		http.Header{"X-CommandCode-Callable-Key": []string{"sk-known"}})))
	if decodeManagementOK(t, blocked) {
		t.Fatal("key_add must be rejected while writes are disabled")
	}
}
```

- [ ] **Step 10.1: 写测试并确认当前失败点**

Run: `go test ./internal/commandcode/ -run 'TestCallerScope|TestConfiguredAPIKeys|TestAllowCaller|TestExecutorDenies' -v`
Expected: 在 Step 6-8 完成后 PASS；若在实现前运行，应为编译失败/功能失败。

- [ ] **Step 10.2: 全量测试**

Run: `go test ./internal/...`
Expected: PASS。

- [ ] **Step 10.3: 提交**

```bash
git add internal/commandcode/callable_test.go
git commit -m "test: cover callable-key source, policy and executor denial"
```

#### Reason

需求 3 的三个关键不变量（来源一致性、策略边界、执行器拒绝）必须有独立断言；契约测试保证与宿主 `CallerScope` 算法不漂移。

#### Risk

- `newAuthStubHost` 提供 `accountDir()`；若未来测试桩变动，需要同步。
- 契约测试依赖 SDK `sdk/cliproxy/session`（同模块，纯 Go）。

#### Verification

- 上述 6 个用例（契约、两种配置布局、缺文件、参数解析、策略表、执行器拒绝、CRUD）全绿；`go test ./internal/... -count=2` 复跑稳定。

---

### Step 11: 文档更新 — README

#### Target

- 修改：`README.md`

#### Current Behavior

- README 只描述弹层额度与旧配置表。

#### Change

- 账号页操作表：把“额度”行补充“账号卡下方内联显示 5H/每周窗口”；
- 新增小节“可调用密钥”：说明来源（CPAMP 认证配置 api-keys）、空列表语义（不限制）、非名单 key 返回 403、需要 CPA 客户端认证已启用；
- 若 Step 2 选了 B/C，更新配置表默认值/新键。

- [ ] **Step 11.1: 修改 `README.md`**
- [ ] **Step 11.2: 提交**

```bash
git add README.md
git commit -m "docs: document inline windows and callable keys"
```

#### Reason

行为/配置变化必须可发现；空列表语义是运维关键信息。

#### Risk

- 无。

#### Verification

- README 中 `grep` 到 `可调用密钥`、`stream-idle-timeout` 默认值正确。

---

### Step 12: 全量验证与收尾

#### Target

- 仓库级验证（无新代码文件）

#### Current Behavior

- 各 Step 已独立提交。

#### Change

- 运行完整验证命令；更新 `## 4. Changed Files Tracking` 与 `## 7. Execution Log`；确认无多余改动（`git status --short` 应只剩文档跟踪）。

- [ ] **Step 12.1: 全量命令**

```bash
gofmt -l .
go vet ./...
go test ./internal/... -count=1
make build-windows   # 或按目标平台
```

Expected: `gofmt -l .` 无输出；vet 干净；测试全绿；产物生成在 `dist/`。

- [ ] **Step 12.2: 检查文档与跟踪表**

```bash
git status --short
git log --oneline -12
```

- [ ] **Step 12.3: 最终提交（如仍有文档改动）**

```bash
git add docs/superpowers/plans/2026-10-07-commandcode-windows-callable-keys-implementation-plan.md
git commit -m "docs: update the implementation plan tracking"
```

#### Reason

Phase 5 的最终报告需要可复现的验证证据。

#### Risk

- c-shared 构建需要 C 编译器与宿主架构一致；无编译器时记录为未验证并在最终报告说明。

#### Verification

- 命令输出与预期一致；`## 7. Execution Log` 完整。

---

## 4. Changed Files Tracking

> 以下为执行后的实际结果（2026-10-07）。

| File | Action | Change |
| ---- | ------ | ------ |
| `internal/commandcode/stream_test.go` | Add | 4 个 characterization 测试：无数据超时、逐完整行重置、半个 SSE 行=静默、首行后行间静默，固化 `consumeUpstream` 现状语义 |
| `internal/commandcode/management.go` | Modify | 新增 `?action=windows`（并发 4、单账号 15s、逐账号降级）与 `keys/key_add/key_update/key_delete`；抽出 `loadAccountForView` 供 `loadAccountByFile` 复用；新增 `sync` import |
| `internal/commandcode/page.go` | Modify | 账号卡内联 5H/每周窗口（`.windows` 样式 + `renderWindows()` + `load()` 触发）；新增“可调用密钥”卡片、双语文案、添加/停用/启用/删除交互；`pageScriptLiteral` 增加文案键 |
| `internal/commandcode/executor.go` | Modify | `handleExecute`/`handleExecuteStream` 在 `prepareExecutor`/`initialize` 之前调用 `allowCaller(req.Metadata)` |
| `internal/commandcode/callable.go` | Add | 新增：config.yaml `api-keys`/`access.api-keys` 只读解析（宿主同款 `-config` 路径发现）、`caller_scope` 计算、状态文件读写（路径+mtime 缓存）、`allowCaller` 策略与 403 错误 |
| `internal/commandcode/callable_test.go` | Add | 哈希向量契约、两种配置布局、缺文件、参数解析、策略表（空列表/命中/停用/未知/缺 scope）、执行器 403、管理端 CRUD 与写开关 |
| `internal/commandcode/management_test.go` | Modify | `?action=windows` 批量成功/逐账号降级/removed 跳过；页面内联窗口与可调用密钥断言 |
| `README.md` | Modify | 账号卡内联窗口说明、新增“可调用密钥”章节、职责边界表补充 |

> 计划中提及但**未修改**（Step 2 未执行，见 `## 6. Plan Adjustment`）：`provider.go`、`config.go`、`stream.go`、`config_test.go`。
>
> **2026-10-07 后续：用户要求移除“可调用密钥”功能，Step 6-10 的相关交付已回退**，`callable.go` / `callable_test.go` 移入 `backups/removed-callable-keys-20261007/`；窗口内联（Step 3-5、9 的窗口部分）保留。详见 `## 6. Plan Adjustment` 的 Plan Adjustment 3。

## 5. Verification Checklist

> 图例：`[x]` 本地已验证；`[ ]` 需部署环境冒烟。

### 主流程验证

- [x] 账号页每个活跃账号卡片下方渲染 5H/每周窗口：`renderWindows`/`.windows`/`call('windows')` 断言通过（浏览器真实渲染待部署冒烟）。
- [x] `?action=windows` 批量返回活跃账号、逐账号降级、removed 跳过：`TestAccountsWindowsAction`、`TestAccountsWindowsActionDegradesPerAccount`。
- [x] `?action=keys` 列出 config.yaml 的 api-keys（掩码、allowed/disabled/stale）：`TestCallableKeysManagementCRUD`。
- [ ] 从页面添加来源内 key 后，用该 key 真实调用本插件模型成功（执行器放行路径的端到端调用属部署冒烟；本地已覆盖空列表放行与策略表）。
- [x] 删除/停用该 key 后调用返回 403：`TestExecutorDeniesNonCallableKey` + `TestAllowCallerPolicy`（流式/非流式共用同一入口）。

### 关联流程验证

- [x] `action=data` 与账号 CRUD 不回归：既有 `TestAccounts*` 全绿。
- [x] removed 账号不产生窗口条目；上游单账号失败仅该行 `error`：windows 两个用例覆盖。
- [x] 非来源列表 key 添加被拒；config.yaml 缺失时来源不可用（页面报错、禁止新增；运行时放行+warn）：CRUD 用例 + `TestConfiguredAPIKeysMissingFile` + 策略测试。
- [x] `allow-page-write=false` 时可调用密钥写操作被拒：CRUD 用例末尾断言。

### 边界情况验证

- [x] 窗口 `exceeded`/`percent` 由 `buildQuotaWindowView` 复用；窗口整体缺失时 `windows` 条目省略该字段（与弹层一致）。`cap=0`/`reset_at` 缺失为既有 `quotaPercent`/渲染逻辑，未改动。
- [x] 可调用密钥：空列表不限制；非空且缺 `caller_scope` → 403；重复添加幂等（替换同 ID）；删除持久化正确；状态文件不可用 → 放行+warn：策略/CRUD 用例覆盖。
- [x] 需求 1 三类流（无数据、半行、行间静默）行为已固化：`stream_test.go`。

### Test / Build / Lint / Typecheck

- [x] `go test ./internal/... -count=1` 全绿。
- [x] `go vet ./...` 无输出。
- [x] `gofmt -l .` 无输出。
- [x] c-shared 构建成功：`go build -buildmode=c-shared -o dist/commandcode.dll .`（当前 shell 无 `make`，使用 Makefile 等价命令）。
- [ ] 部署冒烟（非本地完成条件）：CPAMP 页面渲染、真实窗口数据、真实调用方准入、需求 1 诊断证据与选项选择。

## 6. Plan Adjustment

### Plan Adjustment 3（2026-10-07，用户要求：移除“可调用密钥”功能）

- 原计划：Step 6-10 交付“可调用密钥”来源读取、存储、准入与页面 CRUD，并在 Step 7/9 接入执行器与页面。
- 新需求：用户明确要求去掉该功能，保留账号卡内联 5H/每周窗口。
- 调整内容（均在工作区，未提交）：
  - 移除：`internal/commandcode/callable.go`、`internal/commandcode/callable_test.go`（移入 `backups/removed-callable-keys-20261007/`，非永久删除）。
  - 回退：`executor.go` 的 `allowCaller` 调用；`management.go` 的 `keys/key_add/key_update/key_delete` 分支、`callableKeyCandidateView` 与 4 个 handler；`page.go` 的可调用密钥 HTML/CSS/文案/`loadCallableKeys`/事件绑定；`management_test.go` 的 `TestAccountsPageHasCallableKeys`；README 的“可调用密钥”章节与职责行。
  - 保留：`windows` 批量路由与卡片内联窗口、`stream_test.go`、Step 1 的调查结论。
- 新增修改范围：无（全部为回退既有改动）。
- 影响：可调用密钥相关测试与断言随之移除；`go test ./internal/...` 仍须全绿。

### Plan Adjustment 1（已执行，未扩大范围）

- 原计划：Step 10 使用 `sdk/cliproxy/session.CallerScope` 做契约断言。
- 新发现：该 SDK 包传递依赖 `internal/util` → `github.com/gin-gonic/gin`，需要向插件 `go.sum` 增加依赖，违反 Global Constraints（不新增依赖），`go test` 直接 setup failed。
- 为什么需要调整：契约断言不能用会引入新依赖的 import；用固定向量可达到同等约束效果。
- 调整内容：改为固定哈希向量 `hex(sha256("cli-proxy-api:caller-scope:v1\x00"+"user_contract_key")) = 645081fcc27fdc374fa19d452d5d5458b87ae5bbf30817920b497babdd4c1607`（来自宿主 `sdk/cliproxy/session/identity.go` 算法），另加 trim/空值断言。
- 新增修改范围：无（仅 `callable_test.go` 内部替换）。

### Plan Adjustment 2（测试细节，已执行）

- 原计划：测试直接用 `http.Header{"X-CommandCode-Callable-Key": []string{...}}` 传 key。
- 新发现：`http.Header.Get` 会规范化查询键（`X-Commandcode-...`），与字面 key 不匹配；生产链路 net/http 已规范化，仅测试构造受影响。
- 调整内容：测试新增 `callableHeader()`（`http.Header.Set`）构造请求头。
- 新增修改范围：无。

### Step 2 状态：未执行（按门禁等待确认）

- 需求 1 的修复选项 A/B/C 需要 Step 1.1 的部署诊断证据 + 用户选择；本地无法访问 `commandcode.ai`/CPA 部署，故保持未执行。
- 因此 `provider.go`、`config.go`、`stream.go`、`config_test.go` 未修改，`stream-idle-timeout` 默认值仍为 `30s`。

## 7. Execution Log

### Step 1 Completed

Modified:
- `internal/commandcode/stream_test.go`（新增）

Changes:
- 4 个 characterization 测试：无数据超时、逐完整行重置、半个 SSE 行=静默、首行后行间静默。

Reason:
- 固化 `upstream idle for 30s` 的判定语义（首字节计入、逐行重置、半行=静默），作为需求 1 调查证据与后续修复回归网。

Verification:
- `go test ./internal/commandcode/ -run TestUpstreamIdle -v -count=3`：全部 PASS。

### Step 2 Pending

- 未执行；等待 Step 1.1 诊断记录与选项选择（A 配置 / B 默认值 / C 首字节拆分）。

### Step 3 Completed

Modified:
- `internal/commandcode/management.go`

Changes:
- 新增 `accountWindowsView`、`windowsFetchTimeout=15s`、`windowsConcurrency=4`、`fetchAccountWindows`、`loadAccountForView`；新增 `?action=windows` 分支；`loadAccountByFile` 复用 `loadAccountForView`；新增 `sync` import。

Reason:
- 需求 2 需要批量、可降级的窗口数据源，复用 `FetchQuotaReport`（60s 缓存）与 `buildQuotaWindowView`。

Verification:
- `go build ./...`、`gofmt -l` 干净；既有 `TestAccounts|TestManagement` 全绿。

### Step 4 Completed

Modified:
- `internal/commandcode/page.go`

Changes:
- `.account .windows` 样式；`renderRow` 增加窗口容器；新增 `renderWindows()`（调 `?action=windows`）；`load()` 渲染后触发。

Reason:
- 需求 2 要求窗口显示在每个账号 item 下方，复用 `meterRow` 与既有 `window_5h/window_weekly` 文案。

Verification:
- `TestAccountsPageHasInlineWindows` PASS。

### Step 5 Completed

Modified:
- `internal/commandcode/management_test.go`

Changes:
- `TestAccountsWindowsAction`（双账号 + removed 跳过 + five_hour 16%）、`TestAccountsWindowsActionDegradesPerAccount`（坏账号 error 不拖垮整体）、页面内联窗口断言。

Reason:
- 覆盖批量路由的成功、降级、removed 三类行为。

Verification:
- `go test ./internal/commandcode/ -run 'TestAccountsWindows|TestAccountsPageHasInlineWindows' -v`：全 PASS（`-count=3` 稳定）。

### Step 6 Completed

Modified:
- `internal/commandcode/callable.go`（新增）

Changes:
- `configuredAPIKeys`（`api-keys` + `access.api-keys`、去重）、`defaultConfigFilePath`（宿主同款 `-config` 发现，可注入）、`callerScopeFor`、状态文件 `commandcode-callable-keys.data` 读写（路径+mtime 缓存）、`allowCaller`（空列表放行/命中放行/停用与未知拒绝/无 scope 拒绝/存储不可用放行+warn）、`callableKeyDenied`（403）。

Reason:
- 需求 3 的来源、子集存储与运行时策略集中在一个模块，执行器只调用 `allowCaller`。

Verification:
- `go build ./...`、`go vet ./...` 通过；策略与契约测试见 Step 10。

### Step 7 Completed

Modified:
- `internal/commandcode/executor.go`

Changes:
- `handleExecute`/`handleExecuteStream` 在 `prepareExecutor` 前调用 `allowCaller(req.Metadata)`（被拒请求不产生任何上游流量）。

Reason:
- 执行器是唯一能精确判断请求路由到本插件的位置；403 为请求级错误，不冷却/不换号。

Verification:
- 既有 executor/acceptance 测试全绿（无状态文件=空列表=不限制）；`TestExecutorDeniesNonCallableKey` PASS。

### Step 8 Completed

Modified:
- `internal/commandcode/management.go`

Changes:
- `?action=keys/key_add/key_update/key_delete`；`callableKeyCandidateView`；来源校验（不在 `api-keys` 拒绝）、掩码+哈希存储、`allow-page-write` 门禁、明文 key 走 `X-CommandCode-Callable-Key` 请求头。

Reason:
- 需求 3 的 CRUD 与来源约束必须服务端实现。

Verification:
- `TestCallableKeysManagementCRUD` PASS（列表/添加/来源外拒绝/停用/删除/写开关）。

### Step 9 Completed

Modified:
- `internal/commandcode/page.go`

Changes:
- “可调用密钥”卡片（`callableList`/`callableAdd`）、中英双语文案、`loadCallableKeys()` 渲染（stale/禁用/未启用状态）、添加走请求头、`load()` 末尾调用（无账号也可管理）。

Reason:
- 需求 3 的页面入口与状态展示。

Verification:
- `TestAccountsPageHasCallableKeys` PASS；手工检查无明文 key 写入 DOM（仅密码输入框与请求头）。

### Step 10 Completed

Modified:
- `internal/commandcode/callable_test.go`（新增）

Changes:
- 契约哈希向量、两种配置布局、缺文件、参数解析、策略表、执行器 403、管理端 CRUD；`withConfigFile`/`callableHeader`/`decodeManagementOK` 辅助。

Reason:
- 覆盖需求 3 的来源一致性、策略边界、执行器拒绝与 CRUD。

Verification:
- `go test ./internal/... -count=1` 全绿。

### Step 11 Completed

Modified:
- `README.md`

Changes:
- 账号卡内联窗口说明、新增“可调用密钥”章节（来源、启用方式、403 语义、空列表语义、配置来源限制）、职责边界表补充。

Reason:
- 行为与配置变化可发现。

Verification:
- README 含 `可调用密钥`、`caller_scope`、内联窗口说明。

### Step 12 Completed

Modified:
- 无代码改动；本 Plan 文档（Changed Files Tracking / Plan Adjustment / Execution Log）

Changes:
- 全量验证并回填实际结果。

Verification:
- `gofmt -l .` 无输出；`go vet ./...` 干净；`go test ./internal/... -count=1` 全绿；`go build ./...` 通过；`go build -buildmode=c-shared -o dist/commandcode.dll .` 成功（`make` 不在当前 shell，用 Makefile 等价命令）。

### 需求 1 诊断记录（Step 1.1）

```
- 时间窗：待部署环境补充
- 触发模型：待部署环境补充
- 首次输出前静默 / 中途静默：待部署环境补充
- 客户端是否收到部分内容：待部署环境补充
- CPA 日志行：待部署环境补充
- 该时段上游/网络异常：待部署环境补充
- 结论：待确认（决定 Step 2 选项）
```
