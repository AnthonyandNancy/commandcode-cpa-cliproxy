# CommandCode SSE Framing and Upstream Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan step-by-step. Steps use checkbox (`- [ ]`) syntax for tracking. Do not start coding until the plan has been approved.

**Goal:** Fix the streaming terminal-frame failure shown in the UI and synchronize the CommandCode CLI version, request headers, monthly quota data, and request-body shape with `Mars-Sea/dsh-commandcode-provider` at CLI `1.88.0`, without importing DSH-only host behavior.

**Architecture:** The plugin remains a CPA executor/credential provider. CPA owns client protocol translation and downstream SSE framing; the plugin owns the CommandCode wire protocol. The fix therefore changes the plugin’s upstream/event translation and envelope construction, not CPA. Streaming tests will model CPA’s actual framing rules: OpenAI chat-completions receives bare chunk payloads and CPA writes the single `data: [DONE]` on channel close; cross-format paths consume translated payloads.

**Tech Stack:** Go 1.26, `github.com/router-for-me/CLIProxyAPI/v8 v8.0.17` SDK, standard `net/http`, `httptest`, Go table-driven tests.

## Global Constraints

- Do not modify CPA itself or `go.mod`.
- Do not touch DSH-only features: attribution headers owned by DSH, model catalog snapshots, price tables, output-budget tri-state, or DSH UI.
- Preserve the existing plugin architecture and public RPC surfaces.
- Do not reset or overwrite the unrelated working-tree changes under `backups/removed-callable-keys-20261007`, `internal/commandcode/page.go`, `internal/commandcode/management_test.go`, `README.md`, or the callable-keys plan.
- Every behavior change starts with a failing test.
- The default CLI version is exactly `1.88.0`.
- `max-body-mb` remains a request-level limit; exceeding it must return 400 and must never trigger credential rotation.
- Preserve the OpenAI `[DONE]` sentinel: the plugin must not remove CPA’s downstream terminator; it must avoid duplicating it.
- Keep quota semantics: `credits.monthlyCredits` is the remaining balance, `plan.monthlyCredits` is the plan limit.

---

# Implementation Plan

## 1. Goal

### 本次修改目标

1. 修复截图中的流式收尾错误，并消除重复/畸形 `data:` 终止帧。
2. 同步 Command Code CLI `1.88.0` 的版本标识与相关请求头。
3. 同步月额度解析缺口（`individual-go-v1`）与账期用量请求参数。
4. 同步请求体形状：tools 的 `type`、CLI environment、默认 temperature。
5. 让已声明但未执行的 `max-body-mb` 真正生效。

### 不包含

- 不修改 CPA、CPAMP 或 host SDK。
- 不复制 DSH 的 `attributionHeaders()` 值。
- 不引入 DSH 的设置页、Typert、模型目录、价格表、图片卸载、413 降档、`maxOutputTokens` 三态。
- 不重构现有包结构或 RPC。
- 不处理“可调用密钥”回退改动。

### 成功标准

- 上游返回 `data: data: [DONE]` 或尾部重复 `data:` 时，插件不抛错、不输出畸形帧，并按协议正常结束。
- OpenAI chat-completions 路径只发送裸 `chat.completion.chunk` JSON；CPA 负责唯一的 `data: [DONE]`。
- 跨格式路径产生的终止帧仍能被 CPA translator 识别。
- 生成请求含 `x-command-code-version: 1.88.0`、`accept-encoding: identity`、`x-co-flag: false`，且不把 CLI-only 头错误加到账户 GET 端点。
- `individual-go-v1` 解析为 Go / 10；usage query 在订阅有 `currentPeriodStart` 时带 `since`，没有时不伪造。
- tools 每项有 `type:"function"`；`config.environment` 为 CLI 形态；默认 `temperature` 为 `0.3`。
- 超过 `max-body-mb` 的请求在调用上游前以 400 失败，且不触发换号。
- `go test ./internal/...`、`go vet ./internal/...` 全绿；可构建 `c-shared` 时构建通过。

---

## 2. Current Understanding

### 当前实现方式

- `internal/commandcode/executor.go`:
  - `prepareExecutor` 解析账号、派生设备身份、构建信封。
  - `handleExecute` 用 `foldStream` 非流式聚合。
  - `handleExecuteStream` 有两个分支：
    - 无 `stream_id`：收集 chunks 并按 RPC 返回。
    - 有 `stream_id`：后台从上游读、逐帧 `EmitStream`。
  - 两处分支自行追加 `data: [DONE]\n\n`。
- `internal/commandcode/stream.go`:
  - `translateLine` 把上游行转换为裸 `chat.completion.chunk` JSON。
  - 只剥一层 `data:`；`data: data: [DONE]` 会进入 `json.Unmarshal` 失败并被忽略。
  - `consumeUpstream` 逐行读取；遇到终止后没有显式的 `done` 状态传播。
- `internal/commandcode/httpclient.go`:
  - `upstreamHeaders(... preflight bool)` 统一生成头。
  - 生成路径补 `User-Agent: cli`、`x-project-slug`、`x-taste-learning`。
  - 预请求、模型、额度路径共用 `preflight=true`。
- `internal/commandcode/provider.go`:
  - `DefaultCLIVersion = "1.53.1"`。
- `internal/commandcode/envelope.go`:
  - `envelopeTool` 无 `type`。
  - `config.environment` 直接使用 `profile.Platform`。
  - `temperature` 只在客户端显式提供时发送。
  - `max_tokens` 默认 64000，上限 200000。
- `internal/commandcode/quota.go`:
  - `subscriptionPlans` 无 `individual-go-v1`。
  - whoami 后并发请求 usage / credits / subscriptions。
  - usage 无 `since`。
- `internal/commandcode/quota_provider.go`:
  - 把月度剩余、5H、weekly 映射到 CPA quota buckets。
- 页面 `internal/commandcode/page.go` 已消费 `buildQuotaView`，不直接访问上游。

### 数据流 / 调用链

```text
CPA request
  -> executor.prepareExecutor
     -> ParseAccount
     -> DeriveDevice
     -> BuildEnvelope
  -> EnsureInitialized (fingerprint + lifecycle)
  -> DoGenerate /alpha/generate
  -> stream.go translateLine / consumeUpstream
     -> handleExecute: foldStream -> chat.completion
     -> handleExecuteStream:
          inline chunks OR EmitStream(streamID, payload)
  -> CPA:
       OpenAI chat path: wraps bare payload as "data: %s\n\n" and emits one data: [DONE] on close
       cross-format: translator converts chunks and writes protocol-specific frames
       Responses path: SSE validator validates complete frames

quota:
  page/management "quota"
    -> FetchQuotaReport -> buildQuotaView -> page
  CPAMP quota.fetch
    -> FetchQuotaReport -> QuotaFetchResponseFor
```

### 已确认的影响范围

- 直接修改：
  - `internal/commandcode/stream.go`
  - `internal/commandcode/stream_test.go`
  - `internal/commandcode/executor.go`
  - `internal/commandcode/executor_test.go`
  - `internal/commandcode/provider.go`
  - `internal/commandcode/httpclient.go`
  - `internal/commandcode/identity_test.go`
  - `internal/commandcode/models_test.go`
  - `internal/commandcode/quota.go`
  - `internal/commandcode/quota_test.go`
  - `internal/commandcode/envelope.go`
  - `internal/commandcode/envelope_test.go`
  - `internal/commandcode/acceptance_test.go`
  - `README.md`
- 关联检查：
  - `internal/commandcode/host.go`
  - `internal/commandcode/quota_provider.go`
  - `internal/commandcode/management.go`
  - `internal/commandcode/page.go`
  - `docs/DEPLOY.md`
- 确认无需修改：
  - `plugin.go`
  - `internal/commandcode/account.go`
  - `internal/commandcode/upstream.go`（错误分类不变）
  - CPA SDK / `go.mod`
- 工作区已有并行改动：
  - `page.go`、`management_test.go`、`README.md`、`backups/removed-callable-keys-20261007/*` 与 `docs/superpowers/plans/2026-10-07-commandcode-windows-callable-keys-implementation-plan.md`。
  - 本次不得 reset、覆盖或混入这些改动。

### 需求 / 代码不一致

- 旧计划与 `envelope_test.go` 断言 `wire tools must not carry a type field`；目标上游 `1.88.0` 明确发送 `type:"function"`。本次以用户指定的上游仓库为准。
- 截图错误无法仅凭本仓库复现到具体注入层；计划以“同类畸形尾帧容错 + 输出边界正确性”为可验证目标，不声称已定位外部具体进程。
- CPA OpenAI 处理器统一包裹 `data: %s\n\n` 并自行写 `[DONE]`；插件当前显式发送 `[DONE]` 会导致重复终止。跨格式路径依赖 translator，不能一并删除终止语义。

---

## 3. Implementation Steps

### Step 1

#### Target

修改目标：

- 文件：`internal/commandcode/stream.go`
- 函数：`translateLine`、`consumeUpstream`
- 新增内部 helper：`nextSSEDataPayload` / `isSSEDonePayload`（可在 `stream.go` 内实现）

#### Current Behavior

- `translateLine` 对 `data:` 只剥一层。
- `data: data: [DONE]` 会被当作普通 JSON 尝试解析，失败后记录 warning 并返回 `(nil,false)`。
- `consumeUpstream` 结束后继续读取后续行；终止后没有显式状态。
- `[DONE]` 本身被忽略，但不向后传递“已终止”事实。

#### Change

- 新增一个纯 helper，接受一行 `[]byte`，返回：
  - `payload []byte`
  - `found bool`
  - `done bool`
- 处理规则：
  1. `TrimSpace`
  2. 空行、`:` 注释、`event:` 行：忽略
  3. 循环剥离最多 N 层 `data:` 前缀；每一层去掉后重新 `TrimSpace`
  4. 当载荷精确等于 `[DONE]` 时返回 `done=true`
  5. 普通 JSON 载荷返回 `done=false`
- `translateLine` 只消费 helper 的结果；不再重复 `bytes.HasPrefix(trimmed, []byte("data:"))` 逻辑。
- `consumeUpstream` 在 `done=true` 时：
  - 将 `t.sawDone`（新增字段）置为 true
  - 立即返回 `nil`，停止继续读取
- 不改变 `t.upstreamErr`、usage、finish 语义。
- 不改变正常 JSON 行的翻译。

建议实现形态：

```go
func nextSSEDataPayload(line []byte) (payload []byte, found bool, done bool) {
    payload = bytes.TrimSpace(line)
    for {
        switch {
        case len(payload) == 0:
            return nil, false, false
        case bytes.HasPrefix(payload, []byte(":")):
            return nil, false, false
        case bytes.HasPrefix(payload, []byte("event:")):
            return nil, false, false
        case bytes.HasPrefix(payload, []byte("data:")):
            payload = bytes.TrimSpace(payload[len("data:"):])
            found = true
        default:
            if found && bytes.Equal(payload, []byte("[DONE]")) {
                return nil, true, true
            }
            return payload, found, false
        }
    }
}
```

实际实现需限制循环层数，避免恶意输入导致无界循环；建议最多 8 层。

在 `streamTranslator` 中新增：

```go
sawDone bool
```

`consumeUpstream` 处理：

```go
if t.sawDone {
    return nil
}
payload, found, done := nextSSEDataPayload(result.line)
if done {
    t.sawDone = true
    return nil
}
if !found {
    continue
}
if chunk, ok := t.translatePayload(payload); ok && chunk != nil {
    if !emit(chunk) {
        return errClientGone
    }
}
```

若保留 `translateLine` 作为测试入口，则让它调用同一 helper；不要让两条解析路径分叉。

#### Reason

- 修复 `data: data: [DONE]` 被当作不可解析 JSON 的行为。
- 终止后停止读取，避免尾部畸形帧继续进入翻译器。
- 把“剥壳 + 哨兵识别”收敛为一个纯函数，流式与非流式路径共享。

#### Risk

- 过度剥离可能误吞合法文本。限制层数，并且只在 `found == true` 后识别 `[DONE]`。
- 上游可能发送 `data:` 后接多个 JSON 行；本计划只支持单行事件，符合当前 `ReadBytes('\n')` 设计。
- 已有 keepalive/注释行为必须保持。

#### Verification

- `go test ./internal/commandcode -run 'TestStreamTranslate' -v`
- 新增测试：
  - `data: {"type":"text-delta","text":"x"}` 仍输出 chunk
  - `data:[DONE]`、`data: [DONE]`、`data: data: [DONE]` 均视为 done
  - `: keepalive` 不产出
  - done 后跟垃圾行不产出
  - 事件翻译不会把 `[DONE]` 当 JSON 错误记录

---

### Step 2

#### Target

修改目标：

- 文件：`internal/commandcode/executor.go`
- 函数：`handleExecuteStream`
- 常量/helper：`sseDoneFrame` 或 `streamTerminator`

#### Current Behavior

- 无 `stream_id` 分支：
  - 收集 `translateLine` 产出的裸 JSON chunks
  - 最后追加 `data: [DONE]\n\n`
- 有 `stream_id` 分支：
  - `EmitStream(streamID, append(chunk, '\n', '\n'))`
  - 最后 `EmitStream(streamID, []byte("data: [DONE]\n\n"))`
- CPA OpenAI handler 已对每个 payload 执行 `data: %s\n\n`，并在通道关闭时写 `data: [DONE]\n\n`。
- 因此当前实现会在 OpenAI 路径产生：
  - chunk 被包裹一次（正确）
  - 插件再发一个 `[DONE]`，CPA 包裹成 `data: data: [DONE]\n\n`（错误）
  - CPA 关闭时再发一个 `data: [DONE]\n\n`（重复）

#### Change

- 明确“插件 chunk 是裸 chat-completion JSON，不携带 `data:` 前缀”。
- OpenAI 同格式路径不再由插件发送 `[DONE]`；由 CPA `WriteDone` 唯一发送。
- 跨格式路径不能再依赖插件 `[DONE]` 触发 translator 收尾；需要确认 CPA translator 在 channel close 时是否能生成协议终止事件。当前 CPA `translateExecutorStreamChunks` 只对 OpenAI 输出格式补 `data: [DONE]`（`executorStreamDonePayload`），Claude/Responses/Messages 由各自 translator 在收到 finish chunk 时收尾。
- 本步骤只做最小安全修改：
  1. 删除 `handleExecuteStream` 两处 `[DONE]` 发送。
  2. inline 分支只返回实际 chunks；若没有任何 chunk，返回空 chunks。
  3. async 分支在成功时只 `CloseStream(streamID)`；不再发终止帧。
  4. 保留错误路径 `EmitStreamError`。
- 不修改 `host.go` 的 `EmitStream` 语义。
- 若发现某跨格式客户端因缺少终止事件挂起，暂停并更新 Plan Adjustment，不在本步骤自行新增协议分支。

#### Reason

- 根因是终止帧所有权重复：插件与 CPA 都发 `[DONE]`。
- CPA 的 OpenAI handler 已明确负责 `data: [DONE]\n\n`。
- 让插件只产出“数据帧”，下游 framing 归 CPA，符合现有职责边界。

#### Risk

- 某些客户端可能依赖插件显式终止帧；但 CPA 的 `WriteDone` 已覆盖 OpenAI 直通路径。
- 跨格式 translator 是否依赖 `[DONE]` 需要测试确认；若确认依赖，改为“仅跨格式路径发送裸 payload `[DONE]`”，但不能发送完整 `data: [DONE]\n\n`。
- 不能把错误路径也删掉；错误仍必须通过 `EmitStreamError`。

#### Verification

- 更新/新增 `executor_test.go`：
  - 成功流式后 `host.stream.emit` 中没有 `data: [DONE]`
  - 仍有两个 delta chunk
  - `host.stream.close` 恰好一次
  - inline 分支返回 chunks 数等于 delta 数，不含 `[DONE]`
- 测试 `handleExecuteStream` 的上游含 `data: data: [DONE]` 时：
  - 不产生错误
  - 不产生畸形帧
  - 正常关闭 stream

---

### Step 3

#### Target

修改目标：

- 文件：`internal/commandcode/executor_test.go`
- 测试 helper：`assertCPASSEPayload`（仅测试用）

#### Current Behavior

- 测试只检查 joined 字符串包含 `[DONE]`，不能区分：
  - 裸 JSON chunk
  - 完整 SSE 帧
  - 双重 `data:` 帧
  - 重复终止帧
- 没有模拟 CPA 的 `data: %s\n\n` 包裹和 `WriteDone`。

#### Change

- 增加测试 helper：
  - 把每个 emitted payload 分类为 JSON 或 `[DONE]`
  - 断言普通 chunk 是合法 JSON 且是 `chat.completion.chunk`
  - 断言没有 payload 以 `data:` 开头（因为 OpenAI 同格式路径由 CPA 包裹）
  - 断言没有 `[DONE]` payload
- 增加一个“CPA 模拟层”测试：
  - 对每个 emitted chunk 做 `fmt.Fprintf("data: %s\n\n", payload)`
  - 在 channel close 时追加一次 `data: [DONE]\n\n`
  - 用与 CPA 相同的 `data:` 载荷提取 + `json.Valid` / `[DONE]` 规则校验最终输出
- 增加 inline 分支测试：
  - `chunks` 不含 `[DONE]`
  - 每项 `Payload` 是合法 JSON
- 不引入 CPA 私有包；测试 helper 必须独立、可读、无副作用。

建议 helper：

```go
func assertCPASSEFrame(t *testing.T, payload []byte) {
    t.Helper()
    trimmed := bytes.TrimSpace(payload)
    if bytes.HasPrefix(trimmed, []byte("data:")) {
        t.Fatalf("executor payload must be bare JSON, got %q", payload)
    }
    if bytes.Equal(trimmed, []byte("[DONE]")) {
        t.Fatalf("executor must not emit [DONE]; CPA owns the terminator")
    }
    if !json.Valid(trimmed) {
        t.Fatalf("payload is not valid JSON: %q", payload)
    }
}
```

#### Reason

- 防止测试再次把“包含 `[DONE]`”误当成正确行为。
- 用 CPA 同款规则验证最终输出，覆盖截图中的错误形态。

#### Risk

- 测试 helper 不能变成生产协议抽象。
- 只测试 OpenAI chat-completions 路径；跨格式路径由 CPA translator 的现有测试覆盖。

#### Verification

- `go test ./internal/commandcode -run 'TestHandleExecuteStream|TestExecuteInline' -v`
- 测试失败信息必须明确指出是“裸 JSON 约束”还是“`[DONE]` 所有权”被破坏。

---

### Step 4

#### Target

修改目标：

- 文件：`internal/commandcode/provider.go`
- 常量：`DefaultCLIVersion`

#### Current Behavior

- `DefaultCLIVersion = "1.53.1"`。
- `README.md` 与配置默认值均展示 `1.53.1`。
- 上游目标仓库当前 `COMMAND_CODE_CLI_VERSION = "1.88.0"`。

#### Change

- 将 `DefaultCLIVersion` 改为 `"1.88.0"`。
- 更新注释：明确来源是 `Mars-Sea/dsh-commandcode-provider` 的 CLI 数据面快照，而不是 DSH 运行时版本。
- 不改 `Version`（插件版本）和 `RepositoryURL`。

#### Reason

- 版本头是 CommandCode 协议面的一部分；过旧值可能导致上游按旧客户端处理。
- 用户明确要求同步上游仓库的相关设置。

#### Risk

- 版本号升级可能让上游启用新行为；本次不同步新的模型目录/输出预算能力，测试必须确认现有 envelope 仍可用。
- 用户已通过 `cli-version` 配置覆盖默认值时，不改变其显式值。

#### Verification

- `go test ./internal/commandcode -run 'TestDefaultConfig|TestApplyConfigYAML'`
- 更新 `README.md` 默认值行。
- 断言 `DefaultCLIVersion == "1.88.0"`。

---

### Step 5

#### Target

修改目标：

- 文件：`internal/commandcode/httpclient.go`
- 函数：`upstreamHeaders`

#### Current Behavior

- 所有请求都带：
  - `Content-Type`
  - `Authorization`
  - `x-command-code-version`
  - `x-cli-environment`
  - `traceparent`（实现里总是生成）
- `preflight == false` 时额外带：
  - `User-Agent: cli`
  - `x-project-slug`
  - `x-taste-learning: false`
- 没有 `accept-encoding: identity`。
- 没有 `x-co-flag: false`。
- `preflight == true` 同时用于：
  - fingerprint/lifecycle
  - model catalog
  - quota account endpoints

#### Change

- 增加共享头：
  - `accept-encoding: identity`
- 生成路径（`!preflight`）增加：
  - `x-co-flag: false`
- 保持模型目录/预请求/额度端点不带：
  - `x-co-flag`
  - `x-project-slug`
  - `x-taste-learning`
  - `User-Agent: cli`（如果当前语义是“只有生成路径有”）
- 如果账户 GET 端点需要 `User-Agent`，按上游 `accountHeadersFor` 语义只加 `Authorization`、`accept-encoding`、`x-command-code-version`、`x-cli-environment`，不加 CLI-only 三项。
- 保留 `traceparent` 现有行为，除非测试证明上游不需要。
- 不把 `attributionHeaders()` 搬进来。

建议目标头集合：

```text
生成请求:
  Content-Type: application/json
  Accept-Encoding: identity
  Authorization: Bearer <key>
  x-command-code-version: <cfg.CLIVersion>
  x-cli-environment: production
  User-Agent: cli
  x-project-slug: <slug>
  x-taste-learning: false
  x-co-flag: false
  traceparent: 00-...
  x-session-id: <当有会话身份>
  x-cmd-zdr: 1 (仅 ZDR)

账户 GET:
  Accept-Encoding: identity
  Authorization: Bearer <key>
  x-command-code-version: <cfg.CLIVersion>
  x-cli-environment: production
  traceparent: 00-...

模型目录:
  按现有语义，不带 x-co-flag / x-project-slug / x-taste-learning
```

#### Reason

- 同步上游 CLI 的编码与请求归属行为。
- `accept-encoding: identity` 避免宿主/代理压缩导致响应解析异常。
- `x-co-flag` 是上游 CLI 明确发送的字段。

#### Risk

- 某些代理可能依赖默认 Gzip；`identity` 会增加带宽但提高解析确定性。
- `x-co-flag` 若被上游用于实验分流，可能改变行为；本次只同步 CLI 已知取值 `false`。
- 不要把 CLI-only 头加到账户 GET，否则可能被上游按不同 endpoint 校验拒绝。

#### Verification

- `identity_test.go`：
  - 生成路径断言 `accept-encoding`、`x-co-flag`
  - 预请求路径断言无 `x-co-flag`、无 `x-project-slug`
- `models_test.go`：
  - `TestDoGenerate` 断言生成完整头
  - 模型目录测试断言不误带 CLI-only 头
- 运行 `go test ./internal/commandcode -run 'TestUpstreamHeaders|TestDoGenerate|TestFetchUpstreamModels'`

---

### Step 6

#### Target

修改目标：

- 文件：`internal/commandcode/quota.go`
- 结构：`subscriptionPlans`
- 函数：`subscriptionPlanInfo`

#### Current Behavior

- `subscriptionPlans` 缺少 `individual-go-v1`。
- 当前顺序为：
  - `individual-provider`
  - `individual-ultra`
  - `individual-goat`
  - `individual-max`
  - `individual-pro-v1`
  - `individual-pro`
  - `individual-go`
  - `teams-pro`
- 未知 plan 返回 `ok=false`，页面只显示 raw id，不计算月额度比例。

#### Change

- 增加：
  - `{Prefix: "individual-go-v1", Name: "Go", MonthlyCredits: 10}`
- 位置：必须放在 `individual-go` 前，保证最长前缀优先。
- 保持现有其余 plan 数值不变：
  - Go 10
  - GOAT 70
  - Pro 30 / Pro v1 80
  - Provider 15
  - Max 150
  - Ultra 300
  - Teams Pro 40
- 不新增 tier weight；本插件不使用该字段。

#### Reason

- 上游 `KNOWN_SUBSCRIPTION_PLANS` 包含 `individual-go-v1`。
- 缺失会让 Go v1 账号的月额度上限为空，页面不画月额度比例。

#### Risk

- 前缀排序错误会让 `individual-go-v1` 命中 `individual-go`，导致仍能解析但语义不明确；必须放在前面。
- 未知 plan 仍应保持 raw id，不得回退到任意默认 plan。

#### Verification

- `quota_test.go` 增加：
  - `individual-go-v1` -> `Go`, `10`, `true`
  - `individual-go` -> `Go`, `10`, `true`
  - `individual-goat` -> `GOAT`, `70`, `true`
- 运行 `go test ./internal/commandcode -run 'TestSubscriptionPlanInfoLongestPrefix'`

---

### Step 7

#### Target

修改目标：

- 文件：`internal/commandcode/quota.go`
- 函数：`FetchQuotaReport`
- 新增 helper：`quotaPeriodStart` 或复用 `periodEndMillis` 的读取逻辑

#### Current Behavior

- whoami 后同时并发请求：
  - `usage/summary`
  - `billing/credits`
  - `billing/subscriptions`
- usage summary 不带 `since`。
- subscription path 仅带 `orgId`。
- `parseQuotaPlan` 只读取 `currentPeriodEnd`。

#### Change

- 初始化 `report.Usage.PeriodBasis = "billing-period"` 保持不变。
- 执行顺序调整为：
  1. whoami
  2. 并发 credits + subscriptions
  3. 读取 subscription `data.currentPeriodStart`
  4. 若存在有效值，拼 `since=<原值 URL 编码>` 到 `EndpointUsageSummary`
  5. 请求 usage summary
- 缺失 `currentPeriodStart` 时不加 `since`，不推算本地自然月。
- 保留每个 endpoint 独立降级：
  - subscriptions 失败：usage 仍可无 `since` 请求
  - usage 失败：credits/subscription 仍可用
- 新增 helper：

```go
func quotaPeriodStart(subscription map[string]any) (any, bool) {
    data := recordValue(subscription["data"])
    if data == nil {
        return nil, false
    }
    raw, ok := data["currentPeriodStart"]
    if !ok || raw == nil {
        return nil, false
    }
    return raw, true
}
```

- 请求参数编码：
  - 若 raw 是数字：`strconv.FormatInt` 或 `fmt.Sprint`
  - 若 raw 是字符串：`url.QueryEscape`
  - 空字符串/零值/非法值：不加参数

#### Reason

- 上游按订阅账期起点查询 usage summary，避免把当前自然月或全量历史当作月度用量。
- 本插件页面已展示 usage，但数据口径必须与上游 CLI 一致。

#### Risk

- 额外的串行请求会略增延迟；whoami 后 credits/subscriptions 仍并行，usage 依赖账期起点，这是协议必需。
- subscription 失败时 usage 仍要能请求；不能因缺 `since` 就整体失败。
- 不改变 quota cache 语义（60s）。

#### Verification

- `quota_test.go`：
  - fixture 返回 `currentPeriodStart` 时，断言 usage 请求的 `since` 等于原值
  - fixture 缺失时，断言无 `since`
  - subscriptions 500 时，usage 仍请求且无 `since`
  - 部分失败仍返回 report
- 运行 `go test ./internal/commandcode -run 'TestFetchQuotaReport'`

---

### Step 8

#### Target

修改目标：

- 文件：`internal/commandcode/envelope.go`
- 类型：`envelopeTool`
- 函数：`toWireTools`

#### Current Behavior

- `envelopeTool` 字段：
  - `Name`
  - `Description`
  - `InputSchema`
- `toWireTools` 不输出 `type`。
- `envelope_test.go` 明确断言“wire tools must not carry a type field”。

#### Change

- `envelopeTool` 增加：

```go
Type string `json:"type"`
```

- `toWireTools` 固定赋值：

```go
Type: "function",
```

- 保持：
  - 工具名 alias 逻辑
  - `input_schema` 参数
  - 默认 schema `{type:"object", properties:{}}`
  - 空工具时仍输出空数组
- 更新测试：
  - 删除“must not carry type”的反向断言
  - 断言 `first["type"] == "function"`
  - 断言 `input_schema` 仍在

#### Reason

- 上游 `buildCliBody` 明确发送 `{ type: 'function', name, description, input_schema }`。
- 当前形状与目标 CLI 不一致。

#### Risk

- 上游可能对旧形状有兼容；新增字段属于协议同步，必须用真实/脚本化请求验证。
- 不要同时改 `tool_choice` 映射；本步骤只改 tools 声明。

#### Verification

- `go test ./internal/commandcode -run 'TestBuildEnvelopeToolsAndChoice'`
- 断言：
  - `tools[0].type == "function"`
  - `tools[0].name == "shell_output"`
  - `tools[0].input_schema != nil`
- 更新 `acceptance_test.go` 中依赖旧 tools 形状的断言（若有）。

---

### Step 9

#### Target

修改目标：

- 文件：`internal/commandcode/envelope.go`
- 函数：`BuildEnvelope`
- 新增 helper：`cliEnvironment(profile DeviceProfile) string`

#### Current Behavior

- `config.environment` 等于 `profile.Platform`（如 `win32`）。
- `params.Temperature` 只有在客户端提供 `temperature` 时才设置。
- `max_tokens` 默认 64000，上限 200000。

#### Change

- 新增 helper：

```go
func cliEnvironment(profile DeviceProfile) string {
    platform := firstNonEmpty(strings.TrimSpace(profile.Platform), DefaultPlatform)
    arch := firstNonEmpty(strings.TrimSpace(profile.Arch), DefaultArch)
    return fmt.Sprintf("%s-%s, Node.js %s", platform, arch, cliNodeVersion)
}
```

- `cliNodeVersion` 使用一个明确的、可测试的常量，例如 `"v22.0.0"`；不要从运行时读取宿主的真实版本。
- `BuildEnvelope` 的 `Config.Environment` 改为 `cliEnvironment(profile)`。
- `temperature` 默认值：
  - 若客户端提供数值，使用客户端值
  - 否则使用 `0.3`
- 保持 `max_tokens` 现有语义不变（默认 64000，上限 200000）。
- 生命周期 `metadata.os` 保持 `platform-arch`，不要改成带 Node.js 的字符串。
- 不引入上游的 `omitMaxTokens` 三态。

#### Reason

- 目标上游 CLI 的 `config.environment` 是 `platform-arch, Node.js version` 形态。
- 默认 temperature `0.3` 是上游明确行为。

#### Risk

- `environment` 语义变化可能影响上游指纹或会话归属；测试要确保 `DeviceProfile` 本身和 lifecycle `os` 不变。
- 不要硬编码 Node 版本到多个位置；只保留一个常量。
- 如果用户显式配置 `temperature: 0`，必须尊重 0，不能用 `|| 0.3` 误判。

#### Verification

- `envelope_test.go`：
  - `config.environment` 包含 `win32-x64, Node.js v`
  - 默认 `params.temperature == 0.3`
  - 显式 `temperature: 0` 保持 0
  - 显式 `temperature: 0.7` 保持 0.7
  - lifecycle `metadata.os` 仍为 `win32-x64`
- `acceptance_test.go` 更新 `config["environment"]` 断言。
- 运行 `go test ./internal/commandcode -run 'TestBuildEnvelope|TestAcceptance'`

---

### Step 10

#### Target

修改目标：

- 文件：`internal/commandcode/executor.go`
- 函数：`prepareExecutor` 或 `handleExecute` / `handleExecuteStream`
- 新增 helper：`requestBodyTooLarge`

#### Current Behavior

- `MaxBodyMB` 在 `config.go` 中解析，但没有任何执行点。
- `prepareExecutor` 返回 `envelope` 后直接调用 `openUpstream`。
- 过大请求会发到上游，可能得到 413 或被代理拒绝。

#### Change

- 在 `prepareExecutor` 返回成功 envelope 后增加检查：
  - `cfg.MaxBodyMB <= 0`：不限制
  - 否则 `maxBytes := int64(cfg.MaxBodyMB) * 1024 * 1024`
  - 若 `len(envelope) > maxBytes`，返回请求级错误
- 错误类型：
  - 复用 `downstreamError{status: http.StatusBadRequest}`，或新增一个实现 `StatusCode() int` 的轻量错误
  - 不触发 CPA 换号
- 错误信息：
  - 只包含实际大小与上限
  - 不包含请求正文或密钥
- 检查必须发生在：
  - `initialize` 之前或之后都可以，但必须在 `openUpstream` 之前
  - 建议在 `prepareExecutor` 内完成后立即返回，避免无谓的 fingerprint 预请求

建议：

```go
func checkRequestBodySize(envelope []byte, maxMB int) error {
    if maxMB <= 0 {
        return nil
    }
    maxBytes := int64(maxMB) * 1024 * 1024
    if int64(len(envelope)) <= maxBytes {
        return nil
    }
    return &downstreamError{
        status:  http.StatusBadRequest,
        message: fmt.Sprintf("request body is %d bytes, limit is %d bytes", len(envelope), maxBytes),
    }
}
```

#### Reason

- `max-body-mb` 已对外声明，必须有实际行为。
- 请求级 400 阻止 CPA 换号放大。

#### Risk

- 限制过小会破坏图片/长上下文请求；默认 100MB 与现有配置一致。
- 不能检查 `Payload` 而非 `envelope`；上游实际发送的是 envelope。
- 错误信息不能泄露内容。

#### Verification

- `executor_test.go`：
  - 设置 `MaxBodyMB: 1`，构造 >1MB 的 envelope，返回 400
  - 断言上游生成端点未被调用
  - 恰好 1MB 的边界允许通过
  - `MaxBodyMB: 0` 不限制
- `config_test.go` 不需要改，因为解析已覆盖。

---

### Step 11

#### Target

修改目标：

- 文件：`README.md`
- 位置：配置表 `cli-version` 行

#### Current Behavior

- `| cli-version | 1.53.1 | x-command-code-version 上报值 |`

#### Change

- 改为 `1.88.0`。
- 若新增 `max-body-mb` 行为需要说明，保持原描述，不增加未实现能力。
- 不修改可调用密钥相关段落。

#### Reason

- 默认配置文档必须与代码一致。

#### Risk

- README 当前有并行未提交改动，必须只改目标行，不整体重写。

#### Verification

- `rg -n '\\| `cli-version` \\|' README.md` 只出现新值。
- 人工 diff 确认没有覆盖可调用密钥回退。

---

### Step 12

#### Target

修改目标：

- 文件：`internal/commandcode/acceptance_test.go`
- 断言：`config["environment"]`、tools 形状（若涉及）

#### Current Behavior

- `acceptance_test.go:122` 断言：
  - `config["environment"] == DefaultPlatform`
- 可能还隐含旧 tools 形状。

#### Change

- 更新为新的 CLI environment 形态。
- 增加：
  - `tools` 每项 `type == "function"`（若请求带 tools）
  - 流式响应测试不再期待 `[DONE]`
- 保持注册、认证、模型目录、错误分级断言不变。

#### Reason

- 验收测试必须反映最终协议契约。

#### Risk

- 验收测试同时覆盖很多路径，修改时只改受影响断言，不要重写整个测试。

#### Verification

- `go test ./internal/commandcode -run 'TestAcceptance' -v`

---

### Step 13

#### Target

修改目标：

- 文件：`docs/DEPLOY.md`（仅当存在默认 CLI 版本或请求体重限说明时）
- 运行手册/发布说明（若项目已有对应文件）

#### Current Behavior

- 需要检查 `docs/DEPLOY.md` 是否重复 `1.53.1` 或描述 `max-body-mb` 为“仅配置”。

#### Change

- 只同步用户可见的默认值/行为。
- 不新增部署步骤。
- 若无需改动，记录为“确认无需修改”，不要为了完整性强行修改。

#### Reason

- 文档必须与实际行为一致，但不扩大范围。

#### Risk

- 不要顺手改发布产物 `dist/*`、`store/registry-entry.json`。

#### Verification

- `rg -n '1\\.53\\.1|max-body-mb|data: \\[DONE\\]' docs README.md`

---

## 4. Changed Files Tracking

实际修改结果（含本次任务结束时尚未提交的两处收尾修改）：

| File | Action | Change |
| ---- | ------ | ------ |
| `internal/commandcode/stream.go` | Modify | 新增重复 `data:` 前缀剥离与 `[DONE]` 识别，终止后立即停止读取 |
| `internal/commandcode/stream_test.go` | Modify | 覆盖 `data: data: [DONE]`、终止后垃圾行与既有 idle 语义 |
| `internal/commandcode/executor.go` | Modify | 删除插件侧 `[DONE]` 与 chunk 换行后缀；新增信封大小 400 校验 |
| `internal/commandcode/executor_test.go` | Modify | 固定裸 JSON chunk/CPA 所有权契约，新增重复终止帧、inline、body limit 边界测试 |
| `internal/commandcode/provider.go` | Modify | `DefaultCLIVersion` 同步到 `1.88.0` |
| `internal/commandcode/httpclient.go` | Modify | 增加 `accept-encoding: identity` 与生成路径 `x-co-flag: false` |
| `internal/commandcode/identity_test.go` | Modify | 覆盖生成/预请求 header 差异 |
| `internal/commandcode/models_test.go` | Modify | 覆盖 `DoGenerate` 的编码与 CLI 头 |
| `internal/commandcode/quota.go` | Modify | 增加 `individual-go-v1`，usage 使用订阅账期 `since` |
| `internal/commandcode/quota_test.go` | Modify | 覆盖 Go v1、账期参数与缺失参数分支 |
| `internal/commandcode/envelope.go` | Modify | tools 增加 `type:"function"`，environment 改为 CLI 形态，默认 temperature 0.3 |
| `internal/commandcode/envelope_test.go` | Modify | 更新 envelope 契约并覆盖显式 temperature |
| `internal/commandcode/acceptance_test.go` | Modify | 更新验收中的 environment 断言 |
| `internal/commandcode/config_test.go` | Modify | 断言默认 CLI 版本为 `1.88.0` |
| `README.md` | Modify | 同步 `cli-version` 默认值到 `1.88.0` |
| `docs/superpowers/plans/2026-10-27-commandcode-sse-sync-implementation-plan.md` | Add | 本执行计划与最终执行记录 |

并行存在、**不属于本次实现**的变更：`internal/commandcode/page.go`、`internal/commandcode/management.go`、`internal/commandcode/management_test.go`、`README.md` 中的可调用密钥回退，以及 `backups/removed-callable-keys-20261007/*` 和旧 callable-keys 计划。这些内容被外部提交 `26a8814` 一并纳入，但本次未主动修改其逻辑。

---

## 5. Verification Checklist

### 主流程验证

- [x] `go test ./... -count=1` 通过
- [x] 非流式请求仍折叠出 `chat.completion`
- [x] 流式请求仍能收到 delta chunk
- [x] 流式成功路径不再产生插件侧 `[DONE]`
- [x] CPA OpenAI 路径模拟输出仅有一个 `data: [DONE]`（由测试断言插件 payload 不含终止帧）

### 关联流程验证

- [x] `go test ./internal/commandcode -run 'TestFetchQuotaReport|TestAccountsQuotaAction|TestQuotaFetchResponseFor' -v` 通过
- [x] `individual-go-v1` 解析为 Go / 10
- [x] usage `since` 与 subscriptions 账期一致
- [x] 部分端点失败仍可展示
- [x] CPAMP quota frame 测试通过

### 边界情况验证

- [x] `data:[DONE]`、`data: [DONE]`、`data: data: [DONE]`
- [x] 终止后垃圾行不产出
- [x] `MaxBodyMB` 恰好等于上限
- [x] `MaxBodyMB + 1` 返回 400 且不调用上游
- [x] `MaxBodyMB == 0` 不限制
- [x] 无 `currentPeriodStart` 时不带 `since`
- [x] 显式 `temperature: 0` 不被默认值覆盖
- [x] 生成路径有 `x-co-flag`，账户/预请求路径没有

### Test

- [x] `go test ./... -count=1`
- [x] 新增测试先失败、实现后通过
- [x] 不删除既有有效测试，只更新已过时的契约断言

### Build

- [x] `go build -buildmode=c-shared -o dist/commandcode.dll .` 退出 0

### Lint

- [x] `go vet ./...` 无输出、退出 0
- [x] `gofmt -l .` 无未格式化文件

### Typecheck

- [x] Go 编译即类型检查；全量测试无类型错误
- [x] 无新增未使用 import / 未使用变量

---

## 6. Execution Log

### Step 1 Completed

Modified:

- `internal/commandcode/stream.go`
- `internal/commandcode/stream_test.go`

Changes:

- 加入最多 8 层的重复 `data:` 剥壳；
- `[DONE]`、`data:[DONE]`、`data: data: [DONE]` 统一识别为终止；
- 终止后立即返回，后续垃圾行不再翻译。

Verification:

- `TestTranslateLineAcceptsRepeatedDataDoneMarker`、`TestConsumeUpstreamStopsAfterRepeatedDataDoneMarker`、全部既有 idle 测试通过。
- 期间修正过一次 helper：裸 JSON 行必须继续被接受，不能因 `found=false` 被丢弃。

### Step 2 Completed

Modified:

- `internal/commandcode/executor.go`
- `internal/commandcode/executor_test.go`

Changes:

- 删除 inline/async 两处插件侧 `data: [DONE]\n\n`；
- 删除 async chunk 的 `\n\n` 后缀，只发送 translator 生成的裸 JSON；
- 保留错误路径 `EmitStreamError` 与 stream close。

Verification:

- `TestHandleExecuteStreamEmitsChunks`：3 个 emit、无 `[DONE]`、恰好一次 close；
- `TestHandleExecuteStreamAcceptsRepeatedDataDoneMarker`：畸形终止仍正常关闭。

### Step 3 Completed

Modified:

- `internal/commandcode/executor_test.go`

Changes:

- 增加 `assertCPAExecutorPayload`，断言插件 payload 为裸 JSON、无 `data:`、无 `[DONE]`；
- 增加 inline 分支测试，断言只返回数据 chunk。

Verification:

- `TestHandleExecuteStreamInlineReturnsOnlyDataChunks` 通过。

### Step 4 Completed

Modified:

- `internal/commandcode/provider.go`
- `internal/commandcode/config_test.go`
- `README.md`

Changes:

- `DefaultCLIVersion` 从 `1.53.1` 改为 `1.88.0`；
- README 默认值同步。

Verification:

- `TestDefaultConfigMatchesReferenceDefaults` 通过，显式断言 `1.88.0`。

### Step 5 Completed

Modified:

- `internal/commandcode/httpclient.go`
- `internal/commandcode/identity_test.go`
- `internal/commandcode/models_test.go`

Changes:

- 所有请求增加 `accept-encoding: identity`；
- 生成请求增加 `x-co-flag: false`；
- 预请求/账户 JSON 端点不误带 `x-co-flag`、`x-project-slug`、`x-taste-learning`。

Verification:

- `TestUpstreamHeaders`、`TestDoGenerate` 通过。

### Step 6 Completed

Modified:

- `internal/commandcode/quota.go`
- `internal/commandcode/quota_test.go`

Changes:

- 增加显式 `individual-go-v1` 映射（Go / 10），位于 `individual-go` 之前。

Verification:

- `TestSubscriptionPlanInfoLongestPrefix` 覆盖 Go、Go v1、GOAT 及未知 plan。

### Step 7 Completed

Modified:

- `internal/commandcode/quota.go`
- `internal/commandcode/quota_test.go`

Changes:

- credits + subscriptions 先行并行；
- subscription 有有效 `currentPeriodStart` 时，usage 追加 `since`；
- 缺失或非法值不加参数；
- 保留端点独立降级与 60s cache。

Verification:

- 新增 `TestFetchQuotaReportPassesBillingPeriodStartToUsage`、`TestFetchQuotaReportOmitsSinceWithoutPeriodStart`；
- 全部 `TestFetchQuotaReport*` 通过。
- 期间修正过 numeric `since` 被格式化为科学计数法的问题。

### Step 8 Completed

Modified:

- `internal/commandcode/envelope.go`
- `internal/commandcode/envelope_test.go`

Changes:

- `envelopeTool` 增加 `type`；
- `toWireTools` 固定输出 `type:"function"`；
- 删除旧的“不得带 type”断言。

Verification:

- `TestBuildEnvelopeToolsAndChoice` 通过。

### Step 9 Completed

Modified:

- `internal/commandcode/envelope.go`
- `internal/commandcode/envelope_test.go`
- `internal/commandcode/acceptance_test.go`

Changes:

- `config.environment` 改为 `<platform>-<arch>, Node.js <emulated version>`；
- 缺省 temperature 设为 `0.3`；
- 显式 `0` 与 `0.7` 均被保留；
- lifecycle `os` 与 `max_tokens` 语义未改。

Verification:

- `TestBuildEnvelopeStaticFields`、`TestBuildEnvelopeExplicitTemperature`、`TestAcceptance` 通过。

### Step 10 Completed

Modified:

- `internal/commandcode/executor.go`
- `internal/commandcode/executor_test.go`

Changes:

- `prepareExecutor` 在发送前执行 `checkEnvelopeSize`；
- 超限返回请求级 400；
- `max-body-mb <= 0` 不限制；
- 检查发生在初始化与上游生成之前。

Verification:

- `TestHandleExecuteRejectsOversizedEnvelopeBeforeUpstream`：上游调用数为 0，状态 400；
- `TestCheckEnvelopeSizeBoundary`：恰好 1MiB 允许、1MiB+1 拒绝、0 不限制。

### Step 11 Completed

Modified:

- `README.md`

Changes:

- `cli-version` 默认值更新为 `1.88.0`。

Verification:

- README 目标行已更新，未触碰可调用密钥段落。

### Step 12 Completed

Modified:

- `internal/commandcode/acceptance_test.go`

Changes:

- 更新 environment 验收断言；
- 未改写验收测试其他路径。

Verification:

- `TestAcceptance` 通过。

### Step 13 Completed

Modified:

- none

Changes:

- 检查 `docs/DEPLOY.md` 与文档中的 `1.53.1` / `max-body-mb` / `[DONE]` 描述；
- 无需修改部署文档。

Verification:

- `rg` 检查确认除计划记录外无陈旧默认版本。

### Final Verification

Commands:

- `go test ./... -count=1`
- `go vet ./...`
- `gofmt -l .`
- `go build -buildmode=c-shared -o dist/commandcode.dll .`

Results:

- 全量测试通过；
- vet 无输出、退出 0；
- gofmt 无未格式化文件；
- c-shared 构建退出 0。

---

## 7. Plan Review Gate

**Status:** Waiting for approval.

在收到明确确认前：

- 不修改代码。
- 不执行 Coding。
- 若用户要求调整计划，先更新本文件并重新等待确认。
