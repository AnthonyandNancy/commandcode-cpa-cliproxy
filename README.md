# commandcode-cpa-cliproxy

把 **CommandCode**（`api.commandcode.ai`）封装成 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)（CPA）插件：任何支持 OpenAI / Anthropic / Responses 协议的客户端（Codex、Claude Code、Cursor、SDK……）都能通过 CPA 调用 CommandCode 背后的模型，**客户端只需要 CPA 的 API key**。

- 多个 CommandCode 账号组成号池：账号就是 CPA 凭据（auth 目录下的 `commandcode-<hash>.json`），**沿用 CPAMP 凭证管理**（可见、可启停、可排序、可删除）。
- **会话唯一性**：以 CPA 的会话 ID 为会话身份，由 CPA 的会话亲和（`routing.session-affinity`）把会话钉到某个上游账号；插件只是把同一身份按上游规则转发出去。
- **账号级异常跟随 CPAMP 机制**：402/429/503/零输出/超时按凭据冷却并换号；400/404/422 等请求级错误一次返回，绝不在账号间放大。
- **一账号一台稳定设备**：设备指纹由 API key 确定性派生，重启/多实例结果一致。
- **全套餐兼容**：Go / GOAT / Pro / Max / Provider 都可用；模型目录按凭据向上游获取，不硬编码套餐。
- 插件自带**中英双语账号页**，样式跟随 CPAMP 注入的 CSS 变量（light/dark 自动适配）。
- **凭据额度**：账号页可查看套餐/月额度/5H 窗口/每周窗口与用量，并通过 CPA quota provider 对接到 CPAMP 凭证管理。

上游协议（信封结构、设备指纹、生命周期预请求、模型目录、错误映射、图片预算、零输出判定）与 [commandcode-proxy](https://github.com/MAXeaglet/commandcode-proxy) 保持一致；差异只有两处，且是需求要求的：**上游 key 来自号池**（不由客户端提供）、**会话身份来自 CPA**。

## 安装

前置：运行中的 CLIProxyAPI（v8.x，带插件支持）、`go` 工具链、C 编译器（Linux/macOS 用系统 gcc/clang；Windows 用 MinGW-w64）。

```bash
git clone <this repo> && cd commandcode-cpa-cliproxy
```

按 CPA 运行平台构建（架构必须一致）：

```bash
# Windows
go build -buildmode=c-shared -o dist/commandcode.dll .

# Linux / macOS
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o dist/commandcode.so .
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -buildmode=c-shared -o dist/commandcode.dylib .
```

把产物放到 CPA 的插件目录（`<pluginsDir>/<goos>/<goarch>/commandcode.so`），并在 `config.yaml` 启用：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    commandcode:
      enabled: true
      priority: 100
```

重启 CPA，日志出现 `plugin loaded ... plugin_id=commandcode` 即成功。

## 添加账号

两种入口，操作的是同一份数据：

1. **插件页**（推荐）：CPA 管理面板侧边栏 → `CommandCode 账号 / Accounts` → 填入 `user_...` key → 保存。页面地址 `/v0/resource/plugins/commandcode/accounts`。
2. **CPAMP 凭证管理**：导入一个 `type=commandcode` 的凭据 JSON（含 `api_key`），或在插件页保存后在凭证列表里启停/排序/删除。

页面上的操作：

| 操作 | 说明 |
|---|---|
| 新增 / 编辑 | API key 走请求头传输，不进 URL 与日志；同一 key 重复保存不会产生第二条账号 |
| 停用 / 启用 | 写 `disabled`，立即对路由生效 |
| 移出 | 把凭据文件重命名为 `*.removed-<时间>`，立即不参与请求，**可恢复** |
| 恢复 / 彻底删除 | 恢复回 `*.json`；彻底删除不可撤销 |
| 额度 | 查看该账号的套餐、月额度（上限/已用/剩余/已购/赠送）、5H 窗口、每周窗口与用量（请求/成功率/花费/Token）。只读，不受 `allow-page-write` 影响；数据实时取自上游 `/alpha` 账户端点。账号卡下方也会内联显示该账号的 5H / 每周窗口 |

> 删除在插件页是「先移出（可恢复）」；需要立即注销运行时可同时在 CPAMP 凭证管理里删除（管理 API 会同步注销）。

CPAMP 凭证管理侧：插件注册了 CPA 的 **quota provider**，凭证列表会带 `supports_quota=true`，面板可按凭据查询同一份额度（与 Codex 额度同一机制）；`quota.reset` 明确返回不支持（窗口由上游滚动重置）。

## 配置

`plugins.configs.commandcode`（面板保存即生效，无需重启）：

| 键 | 默认 | 说明 |
|---|---|---|
| `base-url` | `https://api.commandcode.ai` | 上游基址 |
| `fingerprint-salt` | 空 | 设备指纹盐：改它 = 全部账号换一台设备 |
| `project-dir` | `C:\Users\dev\projects\app` | 伪装的项目目录（与指纹同源自洽） |
| `cli-mode` | `agent` | 信封 `mode`（agent / learning / custom-agent / …） |
| `cli-version` | `1.88.0` | `x-command-code-version` 上报值 |
| `lifecycle-mode` | `interactive` | lifecycle 元数据 `mode` |
| `zdr` | `false` | 请求上游 ZDR-only 路由 |
| `model-cache-ttl` | `5m` | 每凭据模型目录缓存 |
| `empty-system-placeholder` | `true` | 无 system 时发空格占位，避免上游注入自身长提示词 |
| `max-tool-image-mb` | `6` | 工具截图总预算，超出的旧图替换为占位说明；`0` 关闭 |
| `max-body-mb` | `100` | 请求体上限 |
| `allow-page-write` | `true` | 关闭后账号页只读 |
| `upstream-proxy-url` | 空 | 上游出口代理（http/https；`direct` 强制直连） |
| `request-timeout` | 空 | 整请求超时，空=不限 |
| `stream-idle-timeout` | `30s` | 流式上游空闲超时 |
| `non-stream-idle-timeout` | `90s` | 非流式上游空闲超时 |
| `masquerade-platform` / `masquerade-arch` | `win32` / `x64` | 伪装平台与架构 |
| `language` | `auto` | 账号页语言（auto / zh / en） |

## 安全边界

- 插件页走 CPA 的 **resource 路由，不经管理密钥认证**：能打开该页面的人就能读写账号。请把 CPA `host` 限制在本机、不要对外暴露 `/v0/resource/plugins/*`，或在反代层拒绝该路径。
- 页面与日志只显示 key 的尾 4 位；key 不写入 URL。
- 若 CPA 前面有反向代理，必须把 `/v0/resource/plugins/*` 交给 CPA，否则插件页面空白。

## 与 CPA / CPAMP 的职责边界

| 能力 | 归属 |
|---|---|
| 客户端鉴权、协议翻译（OpenAI / Anthropic / Responses） | CPA |
| 会话身份推导、会话亲和（会话→账号绑定） | CPA |
| 凭据优先级 / 权重 / 启停 / 冷却 / 重试 / 错误规则 | CPA + CPAMP |
| 用量统计与请求监控 | CPA 解析执行器响应 → CPAMP |
| CommandCode 上游协议、设备指纹、模型目录、错误分级、账号页 | 本插件 |
| CommandCode 凭据额度（读取、归一化；插件页展示 + CPAMP 凭证管理查询） | 本插件（CPA quota provider） |

插件**不做**跨账号轮换（否则会与 CPA 的轮换叠加，历史上曾把 1 次请求放大成 30 次上游调用）。

## 开发

```bash
go test ./internal/...     # 全部业务规则（无需 C 编译器）
go vet ./...
go build -buildmode=c-shared -o dist/commandcode.dll .
```

实现计划与设计依据见 `docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md`、部署细节见 `docs/DEPLOY.md`。
