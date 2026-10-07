# 部署手册（commandcode CPA 插件）

## 1. 平台矩阵

| 平台 | 产物 | 构建命令 |
|---|---|---|
| Windows amd64 | `commandcode.dll` | `go build -buildmode=c-shared -o dist/commandcode.dll .` |
| Linux amd64 | `commandcode.so` | `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o dist/commandcode.so .` |
| Linux arm64 | `commandcode.so` | `CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc go build -buildmode=c-shared -o dist/commandcode.so .` |
| macOS arm64 | `commandcode.dylib` | `CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -buildmode=c-shared -o dist/commandcode.dylib .` |

要求：Go 1.26+、CGO 可用、C 编译器可用。产物架构必须与 CPA 进程一致。

> **Windows 构建注意**：MinGW 下 `-o xxx-v0.1.0.dll` 这类「输出名自带 `-v<版本>`」会触发链接脚本冲突（`export_file.def ... appears multiple times`）。请先构建成 `commandcode.dll`，再复制/重命名为 `commandcode-v0.1.0.dll`（宿主按文件名解析插件 ID 与版本，复制不影响 ABI）。
> 已验证：产物只依赖 `KERNEL32.dll` 与系统 UCRT（Win10+ 自带），无需额外运行库。

## 2. 安装到 CPA

```
<CPA 工作目录>/
├── config.yaml
├── plugins/
│   └── <goos>/<goarch>/commandcode.<ext>     # 手动安装时放这里
└── auth/                                     # 凭据目录（账号 JSON 落在这里）
```

`config.yaml`：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    commandcode:
      enabled: true
      priority: 100
      # 可选：见 README 的配置表
```

重启 CPA，确认日志出现 `plugin loaded` 且 `plugin_id=commandcode`；管理面板出现 `CommandCode 账号 / Accounts` 菜单。

## 3. 通过 CPAMP 插件商店安装（可选）

`store/registry-entry.json` 是可直接使用的商店条目模板（schema_version 2）：

1. 把仓库推到 GitHub，按 `v*` 打 tag，Release 里上传每个平台的 zip（内含对应动态库）与 `checksums`（如 `commandcode.sha256`）。
2. 在 `registry-entry.json` 里替换 `repository`、`artifacts[].url`、`sha256`、`size`。
3. 把这个 registry 传给 CPA：

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/<owner>/<repo>/main/store/registry-entry.json"
```

4. 在 CPAMP → 插件商店里选择版本安装（latest / 指定 release tag / 手动 tag）。

## 4. 上线前检查清单

- [ ] `plugins.enabled: true`，且 `plugins.configs.commandcode.enabled: true`
- [ ] 动态库架构与 CPA 一致
- [ ] 凭据目录可写（插件页新增账号会写文件；不写也可只读运行）
- [ ] 反代放行 `GET /v0/resource/plugins/*`（否则插件页面空白）
- [ ] 建议：CPA `host` 绑定 `127.0.0.1`，或对外拒绝 `/v0/resource/plugins/*`
- [ ] 若要会话粘滞生效：确认 CPA `routing.session-affinity` 已按需开启（`session-affinity-ttl` 建议 ≥ 会话时长）
- [ ] 若使用上游出口代理：`upstream-proxy-url` 只支持 `http://` / `https://`（`direct` 强制直连）
- [ ] 多实例部署时各实例的 `fingerprint-salt` / `project-dir` / `masquerade-*` 保持一致（否则同一账号会被上游看作多台设备）

## 5. 验收（不需要真实账号的部分）

```bash
go test ./internal/...            # 业务规则与验收测试
go test ./internal/... -run TestAcceptance -v
```

真实账号冒烟（需要一个 `user_...` key）：

```bash
# 1) 通过插件页或 CPAMP 添加账号
# 2) 走 CPA 的 OpenAI 端点
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <CPA-API-KEY>" -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"stream":true}'
# 3) 观察 CPAMP 请求监控：模型、账号、用量是否入账
```

## 6. 排障

| 现象 | 排查 |
|---|---|
| 面板里没有菜单/页面空白 | 插件是否启用、`plugins.enabled`、反代是否放行 `/v0/resource/plugins/*`、`GET /v0/management/plugins` 是否列出插件 |
| 页面能开但列表为空 | 是否已导入账号；`type` 必须是 `commandcode`，`api_key` 必须以 `user_` 开头 |
| 请求 401 | 账号 key 无效（上游认证失败）；该账号会停止参与路由，更新 key 后恢复 |
| 请求 429/503 | 账号级异常：CPA 会冷却该账号并换号；看 CPAMP 请求监控里的账号与状态 |
| 请求 400/404/422 | 请求级错误：不会换号（这是刻意的，避免放大重试）；按错误信息修请求 |
| 模型列表缺少模型 | 该账号套餐不含该模型；插件按凭据向上游拉取目录，失败时回退内置列表 |
| 面板展示名短暂显示 `commandcode` | `host.auth.save` 路径的已知瞬时差异，宿主重扫凭据后回到插件返回的显示名 |

## 7. 卸载

1. 停用插件（`plugins.configs.commandcode.enabled: false`）并重启 CPA。
2. 移除 `commandcode.<ext>`。
3. 账号凭据仍留在 `auth/` 目录，可按需删除或保留（删除后不会被任何 provider 使用）。
