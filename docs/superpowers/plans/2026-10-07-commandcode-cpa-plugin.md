# CommandCode CPA 插件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **续跑说明：** 每个 Task 末尾有 `Status` 行。接手者从第一个 `Status: TODO` 的 Task 开始；`Status: DONE` 表示该 Task 的验证命令已通过。若发现与代码不符的轻微差异，直接改 Plan 并在该 Task 的 `Deviation` 行记录原因。

**Goal:** 交付一个可被 CPAMP 安装/管理、由 CPA 承载运行的 `commandcode` provider 插件：账号以 CPA 凭据文件形式进入号池，会话按 CPA 会话 ID 钉账号，上游协议行为与 `commandcode-proxy` 一致（除客户端 key 来源与号池外），插件页提供账号全功能管理。

**Architecture:** 两层结构 —— 根包 `main`（cgo，`-buildmode=c-shared`，只做 C ABI 转发与 host 调用注入）＋ `internal/commandcode`（纯 Go，承载全部业务规则，可脱离 cgo 单测）。所有轮换/冷却/亲和/协议翻译由 CPA 承担；插件只负责凭据解析、设备身份、上游协议、模型目录、错误分级与账号页。

**Tech Stack:** Go 1.26（cgo/`-buildmode=c-shared`，MinGW gcc on Windows）、`github.com/router-for-me/CLIProxyAPI/v8 v8.0.17`（仅用 `sdk/pluginabi`、`sdk/pluginapi` 类型与常量，保证 ABI 契约一致）、`gopkg.in/yaml.v3`（配置块解析）、标准库 `net/http`、`httptest`（测试用假上游）。

## Global Constraints

- **不修改** CPIProxyAPI / CPAMP 源码；插件只经 ABI 与 host RPC 交互。
- **禁止插件内跨凭据轮换/重试**：轮换、冷却、重试一律交给 CPA（历史事故：1 次请求被放大成 30 次上游调用）。
- **插件不拥有**：凭据选择、会话绑定、冷却状态、客户端鉴权、客户端协议翻译。
- **密钥永不进 URL / 日志 / 页面明文**（页面只显示尾 4 位）。
- **账号数据唯一真源** = auth 目录下 `commandcode-<keyHash8>.json`；任何写路径都必须 read-modify-write，不得清空 `type`，不得改文件名。
- **删除语义 A**：把文件改名为 `<name>.json.removed-<ts>`（不参与扫描、可恢复），`purge` 才真删。
- **会话身份**：只读 `Metadata["canonical_session_id"]`；取不到则不伪造，退化为不发会话字段。
- **设备身份**：一账号一台设备，由 api_key + salt 确定性派生（重启/多实例一致）。
- **provider id** 固定 `commandcode`；执行器输入/输出格式固定 `chat-completions`。
- **测试命令**：`go test ./internal/...`（纯 Go，无需 gcc）；构建命令：`go build -buildmode=c-shared -o dist/commandcode.dll .`
- **平台**：开发环境 Windows + MinGW；产物同时支持 linux/darwin 交叉编译（`CGO_ENABLED=1 GOOS=linux GOARCH=amd64`）。

## File Structure

| 文件 | 职责 |
|---|---|
| `go.mod` | 模块 `commandcode-cliproxy-plugin`；依赖 CLIProxyAPI/v8 v8.0.17、yaml.v3 |
| `plugin.go` | cgo 头 + C ABI 导出 + host 函数表注入（唯一 cgo 文件） |
| `internal/commandcode/provider.go` | 常量：provider id、版本、默认端点、默认配置 |
| `internal/commandcode/config.go` | 配置块解析（yaml）+ 热更新（非法保留上次有效值）+ 存取器 |
| `internal/commandcode/host.go` | host RPC 封装（可注入 caller/emitter，供测试桩） |
| `internal/commandcode/account.go` | 凭据模型、认领判定、稳定文件名/ID、密钥掩码、read-modify-write 写回 |
| `internal/commandcode/identity.go` | 设备身份确定性派生 + 指纹载荷 + lifecycle + 初始化节流 |
| `internal/commandcode/envelope.go` | chat-completions → CommandCode 信封（9 键顺序、system 块、tools、tool_choice、threadId、空 system 占位、图片预算） |
| `internal/commandcode/models.go` | `model.static` 兜底 + `model.for_auth` 上游目录 + TTL 缓存 + 失败回退 |
| `internal/commandcode/upstream.go` | 上游 HTTP 调用、状态码映射、请求级/凭据级错误分级、traceparent |
| `internal/commandcode/stream.go` | 上游 SSE → chat-completions chunk；非流式折叠；空闲超时；零输出判定 |
| `internal/commandcode/executor.go` | `executor.execute` / `execute_stream` 处理与错误出口 |
| `internal/commandcode/page.go` | 账号页 HTML（中英双语 + CPAMP CSS 变量 + 脱敏） |
| `internal/commandcode/management.go` | 管理路由声明 + `accounts`/`data`/`save`/`toggle`/`remove`/`restore`/`purge` 端点 |
| `internal/commandcode/rpc.go` | `HandleMethod` 分发 + 注册 payload + envelope 辅助 |
| `internal/commandcode/acceptance_test.go` | 需求验收端到端测试（假 host + 假上游） |
| `docs/DEPLOY.md`、`README.md`、`store/registry-entry.json` | 部署与分发 |

---

## Task 1: 工程骨架、ABI 契约与注册能力

**修改位置**
- 新建：`go.mod`、`internal/commandcode/provider.go`、`internal/commandcode/host.go`、`internal/commandcode/rpc.go`、`internal/commandcode/rpc_test.go`、`plugin.go`

**当前行为**
- 目录 `D:\code\ai\commandcode-cpa-cliproxy` 为空，无任何代码与模块。

**修改内容**
- 新增模块与依赖（CLIProxyAPI/v8 v8.0.17、yaml.v3）。
- 新增 provider 常量、host RPC 封装（`HostCaller`/`StreamEmitter` 可注入）、`HandleMethod` 分发骨架、注册 payload（能力：model_provider / auth_provider / executor / executor_model_scope=both / input+output=chat-completions / management_api，Metadata 含名称、版本、作者、仓库、Logo、ConfigFields）。
- 新增 cgo ABI 文件 `plugin.go`（C 结构体定义 + `cliproxy_plugin_init/call/free/shutdown`，host 函数表注入 `commandcode.SetHostCaller` / `SetStreamEmitter`）。

**对应需求**
- 需求「插件形式交付、不改 CPA/CPAMP 源码」；设计「注册与能力层」「ABI/RPC 层」。

**依赖**
- 无（首个任务）。

**Interfaces**
- Produces: `commandcode.HandleMethod(method string, request []byte) ([]byte, error)`、`commandcode.Registration() []byte`、`commandcode.SetHostCaller(fn HostCaller)`、`commandcode.SetStreamEmitter(fn StreamEmitter)`、`commandcode.HostCallJSON(method string, req any, out any) error`

**验证方式**
- `go test ./internal/... -run TestHandleMethodUnknown` 通过（未知方法返回错误 envelope）。
- `go build ./...` 通过；`go build -buildmode=c-shared -o dist/commandcode.dll .` 产出动态库（Task 9 复验）。

- [ ] Step 1: 写失败测试 `TestHandleMethodUnknown` 与 `TestRegistrationCapabilities`
- [ ] Step 2: 运行 `go test ./internal/... -run 'TestHandleMethodUnknown|TestRegistrationCapabilities'`，预期 FAIL（包不存在）
- [ ] Step 3: 实现 provider.go / host.go / rpc.go / plugin.go 与 go.mod
- [ ] Step 4: 运行同命令，预期 PASS；再跑 `go build ./...`
- [ ] Step 5: `git init` + 首次提交（无 git 则跳过提交）

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 2: 凭据模型与认领（账号文件）

**修改位置**
- 新建：`internal/commandcode/account.go`、`internal/commandcode/account_test.go`

**当前行为**
- 无凭据模型；host 侧凭据文件格式为：顶层 `type` 决定 provider，其余字段由插件自有（`api_key` 等），宿主消费 `disabled/priority/weight/note/prefix/proxy_url`。

**修改内容**
- 新增 `Account` 结构与解析：`ParseAccount(raw) (Account, handled bool, err error)`（`type != "commandcode"` 或缺少 `api_key` 前缀 `user_` → handled=false）。
- 新增稳定命名：`FileName()` = `commandcode-<sha256(api_key)[:16]>.json`、`AuthID()`；`LabelOrDefault()`。
- 新增 `MaskKey`（尾 4 位）、`WriteFileJSON(raw map[string]any, a Account) ([]byte, error)`（read-modify-write：保留未知字段、保留宿主字段、写 `type`/`api_key`/`label`/可选 `base_url`）。
- 新增 `AuthDataFor(a Account, raw []byte) pluginapi.AuthData`（Provider/ID/FileName/Label/StorageJSON/Metadata{type}）。

**对应需求**
- 需求「账号进入 CPA 凭据池、沿用 CPAMP 凭证管理」「插件页与面板同一份数据」；设计「数据 §新增凭据文件」「凭据层」。

**依赖**
- Task 1（模块与依赖）。

**Interfaces**
- Consumes: `pluginapi.AuthData`
- Produces: `commandcode.ParseAccount(raw []byte) (Account, bool, error)`、`Account.FileName() string`、`commandcode.AuthDataFor(Account, []byte) pluginapi.AuthData`、`commandcode.MaskKey(string) string`

**验证方式**
- `go test ./internal/... -run TestAccount` 全部通过（命名确定性、非本插件文件不认领、掩码、写回保留未知字段）。

- [ ] Step 1: 写失败测试（4 个：解析/命名稳定性/不认领/写回保留）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 account.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 3: 设备身份派生与初始化预请求

**修改位置**
- 新建：`internal/commandcode/identity.go`、`internal/commandcode/identity_test.go`

**当前行为**
- 无设备身份概念；宿主不提供任何伪装能力。

**修改内容**
- 实现确定性派生：同一 api_key + salt → 同一 `machineId`（8-4-4-4-12 形状）、MAC 列表（排序去重）、`DESKTOP-xxxxxx` 主机名、gitEmail、cpu/mem/timezone/osUser、platform/arch（windows/x64 伪装）、项目目录 slug。
- `FingerprintPayload(salt)` 输出 `/alpha/fingerprint/record` 需要的对象（thumbmark + components）。
- `EnsureInitialized(ctx, account, cfg)`：每账号单飞（同账号并发只发一次），首次与每 8h±2h 抖动后并行发送 `POST /alpha/fingerprint/record` 与 `POST /alpha/lifecycle-events`（`cli_session_exists` + metadata{sessionId, cliVersion, mode, os}），失败仅告警不阻塞请求，且不更新节流时间（下次请求重试）。
- 预请求使用与生成请求相同的出口（同一 HTTP 客户端/代理配置）。

**对应需求**
- 需求关键规则「一个账号=一台稳定设备，确定性派生、可复现；指纹与初始化预请求与生成请求同出口」；设计「数据 §设备身份」「身份层」。

**依赖**
- Task 2（Account、BaseURL）。

**Interfaces**
- Produces: `commandcode.DeriveDevice(apiKey, salt, projectDir string) DeviceProfile`、`(DeviceProfile) FingerprintPayload(salt string) map[string]any`、`(DeviceProfile) ProjectSlug() string`、`commandcode.EnsureInitialized(ctx context.Context, a Account, cfg RuntimeConfig) error`

**验证方式**
- `go test ./internal/... -run TestIdentity`（同一 key 两次派生结果一致；不同 key 不同；预请求 URL/头/体形状；失败不污染节流；同账号并发只发一次）。

- [ ] Step 1: 写失败测试（含 httptest 假上游断言请求体）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 identity.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 4: 信封构造（chat-completions → CommandCode）

**修改位置**
- 新建：`internal/commandcode/envelope.go`、`internal/commandcode/envelope_test.go`

**修改内容**
- system/developer → `params.system` 文本块数组（块间补 `\n`，保留 `cache_control`）；无 system 且开关开启时发单空格占位。
- messages：user 文本/多模态（`image_url` → `{type:image,image,mimeType}`）、assistant（顺序 reasoning → text → tool-call）、tool（tool-result + toolName 反查）、未知 role 归一化为 user。
- tools：`{name, description, input_schema}`（无 `type`），无工具时发空数组；`tool_choice` 字符串/对象双向映射（`required`→`any`）。
- 参数：`model`、`messages`、`max_tokens`（上限 200000）、`stream:true`、可选 `temperature`、`reasoning_effort`。
- `config`（workingDir/environment/date/structure…）、`memory/taste/skills` 为 null、`permissionMode=standard`、`mode=cliMode`。
- `threadId`：仅当会话身份是合法 UUID 时写入；键顺序固定 `config, memory, taste, skills, permissionMode, threadId, mode, promptCache, params`。
- 图片预算：工具结果内 base64 图片按 `MaxToolImageMB` 保留最新若干张，其余替换为占位文本。

**对应需求**
- 需求「除客户端 key 来源与会话身份外与 commandcode-proxy 一致」；设计「执行器层 请求前置」「上游协议层」。

**依赖**
- Task 3（DeviceProfile 提供 workingDir/environment/date 口径）。

**Interfaces**
- Produces: `commandcode.BuildEnvelope(payload []byte, sessionID string, profile DeviceProfile, cfg RuntimeConfig) ([]byte, error)`

**验证方式**
- `go test ./internal/... -run TestEnvelope`：9 键顺序、threadId 仅在 UUID 时存在、空 system 占位、tools 形状、tool_choice 映射、图片预算替换、多模态转换。

- [ ] Step 1: 写失败测试
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 envelope.go（逐条对照 `commandcode-proxy/proxy.mjs` 的 `buildCcRequest`）
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 5: 模型目录（静态兜底 + 按凭据拉取 + 缓存）

**修改位置**
- 新建：`internal/commandcode/models.go`、`internal/commandcode/models_test.go`

**修改内容**
- `StaticModels()`：与 commandcode-proxy 内置列表同源（Anthropic/OpenAI/DeepSeek/Kimi/GLM/MiniMax/Qwen/Step/Xiaomi/Gemini），带显示名与能力声明。
- `ModelsForAccount(ctx, account, cfg)`：带该凭据 `Authorization: Bearer <user_key>` + CLI 头请求 `{base}/provider/v1/models`，解析为 `[]pluginapi.ModelInfo`（ID 用上游原名、DisplayName、ContextLength、输入/输出上限、supportedParameters 声明 tools/tool_choice 与视觉能力），按 api_key 缓存 `ModelCacheTTL`（默认 5 分钟），失败/空 → 回退 `StaticModels()`，绝不返回空 provider。

**对应需求**
- 需求「按凭据获取模型目录、不硬编码套餐、全套餐兼容」；设计「模型层」。

**依赖**
- Task 2、Task 3（CLI 头与 profile）。

**Interfaces**
- Produces: `commandcode.StaticModels() []pluginapi.ModelInfo`、`commandcode.ModelsForAccount(ctx, Account, RuntimeConfig) ([]pluginapi.ModelInfo, error)`

**验证方式**
- `go test ./internal/... -run TestModels`：成功解析、失败回退、缓存命中（假上游计数）、空列表回退、模型 ID 保持上游原名。

- [ ] Step 1: 写失败测试
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 models.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 6: 上游调用与错误分级

**修改位置**
- 新建：`internal/commandcode/upstream.go`、`internal/commandcode/upstream_test.go`

**修改内容**
- `DoGenerate(ctx, account, profile, body, cfg) (*http.Response, error)`：`POST {base}/alpha/generate`，头 `Content-Type/User-Agent: cli/x-command-code-version/x-cli-environment/x-project-slug/x-taste-learning/x-session-id/Authorization/traceparent`（+ZDR 时 `x-cmd-zdr: 1`）。
- `ClassifyUpstream(status, body) UpstreamError`：按 commandcode-proxy 的映射得下游状态（400→400、401→401、402→429、403→401、404→404、422→400、429→429、5xx→502/503，其余→502），并判定 `RequestScoped`（下游 400/404 → 请求级；429/402/401/5xx → 凭据级），保留上游 `error.code` 与 message（截断 200 字符）。
- 客户端断连：`ctx` 取消时中止上游请求。

**对应需求**
- 需求关键规则「400/404/422 请求级一次返回、禁止跨账号放大」「401/403 失效」「402/429/503 走冷却与换号」；设计「上游协议层」「执行器层 错误出口」。

**依赖**
- Task 2、Task 3。

**Interfaces**
- Produces: `commandcode.DoGenerate(ctx, Account, DeviceProfile, []byte, RuntimeConfig) (*http.Response, error)`、`commandcode.ClassifyUpstream(status int, body []byte) UpstreamError`、`(UpstreamError) StatusCode() int`、`(UpstreamError) Error() string`

**验证方式**
- `go test ./internal/... -run TestUpstream`：请求头/URL 断言、各状态映射、请求级判定、错误体 code 透出。

- [ ] Step 1: 写失败测试
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 upstream.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 7: 执行器（非流式折叠 + 流式逐帧 + 零输出/空闲超时）

**修改位置**
- 新建：`internal/commandcode/stream.go`、`internal/commandcode/executor.go`、`internal/commandcode/executor_test.go`

**修改内容**
- `executor.execute`：组装信封 → 预请求 → 上游 → 折叠 SSE 为单个 `chat.completion`（usage 含 `prompt_tokens_details.cached_tokens`），错误按分级返回。
- `executor.execute_stream`：同步握手（请求级错误必须经 RPC 错误通道返回，避免 CPA 换号放大）→ 逐帧翻译为 `chat.completion.chunk` 并经 `host.stream.emit` 下发 → `[DONE]`；空闲超时（默认 30s）与零输出（无任何可见内容且无 usage）→ 返回 429 语义错误；客户端断连 → 停止读取上游并 close stream。
- 会话身份：从 `req.Metadata["canonical_session_id"]` 读取，缺失时按 CPA 顺序从 `req.Headers`（X-Session-ID → Session-Id → Session_id → X-Client-Request-Id）兜底，仍无则不发。

**对应需求**
- 需求「反代协议兼容（流式/非流式/工具/图片/推理强度/缓存命中）」「会话唯一性」「零输出与超时→可重试」；设计「核心流程」「执行器层」。

**依赖**
- Task 4、5、6。

**Interfaces**
- Consumes: `pluginapi.ExecutorRequest`、`pluginapi.ExecutorResponse`、`pluginapi.ExecutorStreamChunk`
- Produces: `commandcode.HandleExecute(raw []byte) ([]byte, error)`、`commandcode.HandleExecuteStream(raw []byte) ([]byte, error)`

**验证方式**
- `go test ./internal/... -run TestExecutor`：非流式折叠含工具调用与 usage；流式帧序与 `[DONE]`；上游 400 → 错误带 `http_status:400` 且 CPA 不轮换语义；上游 429 → 凭据级；零输出 → 429；`canonical_session_id` → 上游 `x-session-id`/threadId；无会话身份 → 不发。

- [ ] Step 1: 写失败测试（含 httptest 上游与桩 host）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 stream.go / executor.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 8: 账号管理页（全功能、双入口并存、删除语义 A）

**修改位置**
- 新建：`internal/commandcode/page.go`、`internal/commandcode/management.go`、`internal/commandcode/management_test.go`

**修改内容**
- `management.register` 声明资源：`/accounts`（菜单：`CommandCode 账号 / Accounts`）；动作端点 `/accounts/data`、`/accounts/save`、`/accounts/toggle`、`/accounts/remove`、`/accounts/restore`、`/accounts/purge`（均无 Menu）。
- `/accounts/data?format=json`：`host.auth.list` + `get_runtime` → 账号列表（展示名、状态、disabled、优先级、权重、备注、冷却到期、近期失败），脱敏（只显示尾 4 位）。
- `/accounts/save`：密钥经请求头 `X-CommandCode-Api-Key`（不进 URL）→ 校验（`user_` 前缀、base_url 合法性）→ `host.auth.save{name, json}`（幂等：同密钥同文件）。
- `/accounts/toggle`：写 `disabled`（read-modify-write → save）。
- `/accounts/remove`：把文件重命名为 `<name>.removed-<ts>`（`os.Rename`，路径来自 host.auth.list 的 `Path`）；`/accounts/restore` 反向重命名；`/accounts/purge` 真删（二次确认由页面保证）。
- 页面 HTML：中英双语（`?lang=` 与 `Accept-Language` + 页内切换）、只用 CPAMP 注入的 CSS 变量（`--app-*`、`--primary-*`、语义色、圆角）、loading/empty/error/success 四态、卡片列表 + 行内表单。
- `AllowPageWrite` 配置为 false 时只读（写端点返回 403 文案）。

**对应需求**
- 需求「插件单开一页管理账号、与 CPAMP 凭证页同一份数据」，冲突结论（写入通道 + 删除语义 A）；设计「API §B」「前端」。

**依赖**
- Task 1（host 封装）、Task 2（account 写回）。

**Interfaces**
- Produces: `commandcode.ManagementResources() []map[string]string`、`commandcode.HandleManagement(raw []byte) ([]byte, error)`、`commandcode.AccountsPageHTML(lang string, cfg RuntimeConfig) string`

**验证方式**
- `go test ./internal/... -run TestAccountsPage|TestManagement`：路由声明、页面含双语与 CSS 变量、data 端点 JSON 形状、save 幂等且文件含 `type/api_key`、toggle 写 disabled、remove 改名后可恢复、purge 删除、写开关关闭时 403、密钥不出现在页面/日志。

- [ ] Step 1: 写失败测试（用临时目录模拟 authDir，桩 host.auth.*）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 page.go / management.go
- [ ] Step 4: 运行，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 9: 配置、熔断边界、分发物与文档

**修改位置**
- 新建：`internal/commandcode/config.go`、`internal/commandcode/config_test.go`、`README.md`、`docs/DEPLOY.md`、`store/registry-entry.json`、`Makefile`

**修改内容**
- 配置块（`plugins.configs.commandcode`）：`base-url`、`fingerprint-salt`、`project-dir`、`cli-mode`、`cli-version`、`lifecycle-mode`、`zdr`、`model-cache-ttl`、`empty-system-placeholder`、`max-tool-image-mb`、`allow-page-write`、`upstream-proxy-url`、`stream-idle-timeout`、`non-stream-idle-timeout`、`masquerade-platform/arch`；非法 YAML 保留上一次有效值。
- `Metadata.ConfigFields` 与上述键一一对应（供 CPAMP 渲染中文/英文标签）。
- 熔断边界：host RPC 失败/超时不影响请求主路径（预请求失败仅告警）；`plugins.enabled=false` 或插件卸载表现为 provider 不可用。
- 文档：部署（构建命令、平台矩阵、plugins 配置、反代需放行 `/v0/resource/plugins/*`、安全边界与本地绑定建议）、账号导入与删除语义说明、账号页使用说明。
- 分发：`Makefile` 交叉编译目标、`store/registry-entry.json`（schema_version 2 + install.artifacts 模板）、checksum 说明。

**对应需求**
- 需求「插件形式交付、CPAMP 可安装管理」「样式/双语」「不新增无关能力」；设计「兼容/迁移」「发布物」。

**依赖**
- Task 1–8。

**Interfaces**
- Produces: `commandcode.ApplyConfigYAML(raw []byte)`、`commandcode.CurrentConfig() RuntimeConfig`

**验证方式**
- `go test ./internal/... -run TestConfig`；`make build-windows`（或直接 `go build -buildmode=c-shared`）产出 `dist/commandcode.dll`；`go vet ./...`。

- [ ] Step 1: 写失败测试（默认值、部分覆盖、非法值保留、ConfigFields 与键一致）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 实现 config.go 与文档/构建/分发文件
- [ ] Step 4: 运行测试 + 构建 + vet，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## Task 10: 需求验收与回归验证

**修改位置**
- 新建：`internal/commandcode/acceptance_test.go`

**修改内容**
- 端到端验收（桩 host + httptest 假上游）逐条覆盖 Requirement 验收标准：三协议入口由 CPA 承担（此处验证 provider 侧契约）、会话钉账号字段透传、请求级不轮换、凭据级可轮换、全套餐模型差异、账号页全功能、双入口一致（模拟面板 patch 字段后页面读数一致）、不变行为（无会话身份不伪造；未配置账号时明确错误且无上游调用）。
- 回归清单：既有 provider 不受影响（不涉及 CPA 代码 → 以"插件仅注册自身 provider/凭据类型"验证）；CPAMP 侧不需要改动（以资源路由与 auth-files 契约验证）。

**对应需求**
- Requirement「验收标准」全部条目。

**依赖**
- Task 1–9。

**Interfaces**
- 无新增导出符号（仅测试）。

**验证方式**
- `go test ./internal/... -v` 全绿；`go vet ./...`；`go build -buildmode=c-shared` 通过；人工核对 Requirement 验收清单（见 Plan 附录）。

- [ ] Step 1: 写验收测试（失败）
- [ ] Step 2: 运行，预期 FAIL
- [ ] Step 3: 修复实现缺口（只改实现，不改需求）
- [ ] Step 4: 全量 `go test ./internal/... -v` + `go vet ./...` + 构建，预期 PASS
- [ ] Step 5: 提交

**Status:** DONE (2026-10-07)
**Deviation:** —

---

## 附录 A：Requirement → Task 追溯矩阵

| Requirement 条目 | 落点 | 验证 |
|---|---|---|
| 客户端只用 CPA key | 不实现（CPA 现有能力）；插件不校验客户端 key | Task 10 回归项 |
| 会话唯一性：按 CPA 会话 ID 钉账号 | Task 7（`canonical_session_id` 透传） | Task 7 / 10 |
| 中途 429/503 走 CPAMP 冷却与换号 | Task 6（凭据级错误）+ 不实现轮换 | Task 6 / 10 |
| 401/403 失效停用 | Task 6 | Task 6 / 10 |
| 400/404/422 一次返回、禁止放大 | Task 6 + Task 7（`http_status`） | Task 6 / 7 / 10 |
| 模型按凭据获取、全套餐 | Task 5 | Task 5 / 10 |
| 账号可见/启停/排序/删除（面板） | 复用 CPA/CPAMP；Task 2 保证文件格式 | Task 2 / 8 |
| 插件页全功能账号管理 | Task 8 | Task 8 / 10 |
| 一账号一台稳定设备 | Task 3 | Task 3 / 10 |
| 指纹与初始化预请求同出口 | Task 3 | Task 3 / 10 |
| 样式与 CPAMP 一致 + 双语 | Task 8 | Task 8 |
| 插件形式交付、不改源码 | Task 1 / 9 | Task 1 / 9 |
| 上游协议与 commandcode-proxy 一致 | Task 4 / 6 / 7 | Task 4 / 6 / 7 / 10 |

## 附录 B：明确不做（回归时不要"顺手"实现）

额度/健康能力、官方 CLI 登录流程、模型→账号路由、跨凭据轮换、会话内容存储、其他 provider 改造、CPA/CPAMP 源码修改。

---

## 执行记录（Execution Log）

| Task | Status | 提交 | 说明 |
|---|---|---|---|
| 1 | DONE | 3dd1da0 | 骨架 + cgo ABI + 注册 payload；`RuntimeConfig`/`ConfigFields` 提前落地（Task 3-8 都消费它） |
| 2 | DONE | 426ce85 | 凭据模型、确定性文件名、read-modify-write、auth.parse |
| 3 | DONE | db45342 | 设备身份派生、指纹/lifecycle 预请求、节流与单飞 |
| 4 | DONE | bcff592 | 信封移植（键序、threadId 规则、system 块、tools、tool_choice、图片提升与预算） |
| 5 | DONE | bcff592 | 模型目录：静态兜底 + 按凭据拉取 + TTL 缓存 |
| 6 | DONE | bcff592 | 上游调用、状态码映射、请求级/凭据级错误分级 |
| 7 | DONE | be75b77 | 流转译、execute/execute_stream、零输出与空闲超时 |
| 8 | DONE | 503f73c | 账号页（双语/CPAMP 变量）、save/toggle/remove/restore/purge |
| 9 | DONE | (随 Task 10) | 配置测试、README、DEPLOY、store registry、Makefile |
| 10 | DONE | (随 Task 10) | 验收测试 + 全量验证（gofmt / vet / tests / c-shared 构建 / ABI 符号核对） |

### 执行期偏差（Deviation）

1. **Task 9 部分前置到 Task 1**：`config.go` 的 `RuntimeConfig`、默认值、`ApplyConfigYAML`、`ConfigFields()` 在 Task 1 实现，因为 Task 3-8 都要读配置；Task 9 只保留配置测试与文档/分发物。
2. **Task 5 与 Task 6 合并执行**：两者共享同一次构建/测试循环，无功能偏差。
3. **Task 6 对上游 404 的处理是有意偏差**：commandcode-proxy 把 404 映射为下游 404；本插件把「发给 CPA 的错误状态」压到 400（客户端文案仍保留 404 语义），因为 Requirement 明确要求 400/404/422 属于请求级错误、禁止跨账号放大，而 CPA 只按状态码判定请求级（404 会被判为凭据级 → 触发换号与冷却）。已在代码注释与 Plan 中记录。
4. **Task 7 由测试发现并修复两个实现缺陷**（非计划变更）：异步流把请求上下文与上游 body 的所有权收进 goroutine（否则 RPC 返回即取消/关闭，流被截断）；工具结果图片提升进 user 消息后必须复用 user 图片转换（否则仍是 `image_url` 形态、丢失 `mimeType` 与 `_toolImage` 标记）。
5. **额外健壮性（小范围，已记录）**：页面以凭据文件为 label/priority/note 的真源（宿主条目仅作回退）；插件记住上一次发现的 auth 目录，使「移出后仍可恢复/彻底删除」。

### 未在本环境验证（需在上线环境复核）

- **Linux / macOS 构建**：本机只有 Windows 目标 C 编译器（MinGW），无法产出 Linux/macOS 动态库；命令已写入 `Makefile` 与 `docs/DEPLOY.md`。
- **真实上游联调**：本环境无法访问 `commandcode.ai`（域名解析到非公网地址），也没有 `user_*` 凭据；上游协议按 commandcode-proxy 的既有实现移植并由假上游测试覆盖，真实账号冒烟步骤见 `docs/DEPLOY.md` §5。
- **真实 CPA 加载**：本环境没有运行中的 CPA 实例；ABI 导出符号（`cliproxy_plugin_init` / `cliproxyPluginCall` / `cliproxyPluginFree` / `cliproxyPluginShutdown`）已用 `nm` 核对存在。
