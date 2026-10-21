---
last_updated: 2026-10-21
source_files:
  - internal/httpx/server.go
  - internal/httpx/oauth.go
  - internal/httpx/mcp_endpoint.go
  - internal/httpx/runtime_api.go
  - internal/httpx/request_limit.go
  - internal/httpx/status_page.go
  - internal/runtimeapi/dispatch.go
  - internal/runtimeapi/methods.go
  - internal/mcp/server.go
  - internal/mcp/apps.go
  - internal/desktopcontrol/control.go
  - internal/desktopcontrol/control_unix.go
  - internal/desktopruntime/control.go
  - internal/nexusbridge/client.go
  - internal/acp/types.go
  - internal/config/config.go
  - internal/tool/task/task_actions.go
  - internal/tool/skill/contract.go
  - internal/tool/plugin/service.go
  - internal/tool/mcp/contract.go
  - internal/evolution/contract.go
related_memory: []
---

# 服务层与 IPC 通信

AgentDock **全部对外与对内接口的权威清单**。其它文档提到 API 时一律 `→ 详见` 指回这里。

## 五条通道

| 通道 | 传输 | 认证 | 消费方 |
|---|---|---|---|
| MCP | StreamableHTTP `/mcp` 或 stdio | Bearer / OAuth | AI 客户端 |
| Runtime API | HTTP `/internal/runtime/*` | Bearer / OAuth / 回环 | 桌面控制面板 |
| 本地控制 | Unix socket / Windows NamedPipe | 文件系统权限 | 桌面端 |
| Nexus Bridge | WebSocket | Device Token | NexusDock 云端 |
| ACP | JSON-RPC over stdio | 无（本地子进程） | Claude / Codex 等 agent |

## 1. HTTP 路由全表

`internal/httpx/server.go:41-59`。

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/` | 无 | HTML 状态页（`status_page.go`） |
| GET | `/healthz` | 无 | `{"ok":true,"version":"..."}` |
| GET | `/.well-known/mcp.json` | 无 | Server Card |
| GET | `/.well-known/mcp/server-card.json` | 无 | 同上（别名） |
| GET | `/artifacts/public/{id}/{name}` | HMAC 签名 | 公开产物 |
| GET/HEAD | `/.well-known/oauth-authorization-server` | 无 | AS Metadata |
| GET/HEAD | `/.well-known/oauth-protected-resource/mcp` | 无 | PR Metadata |
| POST | `/register` | 无（限流 30/min） | DCR 注册 |
| GET/POST | `/oauth/authorize` | 登录密码（POST 限流 10/5min） | 授权端点 |
| POST | `/oauth/token` | client 凭证 + PKCE | 令牌端点 |
| GET/POST | `/oauth/mcp/callback` | 无 | 外部 MCP OAuth 回调中转 |
| GET/POST | `/context` | Bearer / OAuth | 上下文聚合（8s 超时） |
| * | `/internal/runtime/*` | Bearer / OAuth | 管理 API（见 §2） |
| POST | `/mcp` | Bearer / OAuth | MCP StreamableHTTP |

OAuth 路由仅 `cfg.OAuthEnabled` 时注册（`oauth.go:46`）。

**中间件只有 `loggingMiddleware`**（`middleware.go:36-48`）—— 无 CORS、无 compress、无 recover。

**Server 参数**（`server.go:91-100`）：`ReadHeaderTimeout` 10s、`ReadTimeout` 15s、`IdleTimeout` 60s、`MaxHeaderBytes` 1 MiB；优雅关闭 10s（`:67-90`）。

### 请求体上限

| 端点 | 上限 | 超限行为 | 位置 |
|---|---|---|---|
| `/mcp` | 1 MiB | 413 `request body exceeds 1048576 bytes` | `mcp_endpoint.go:65-92` |
| OAuth 表单 | 64 KiB | 413 / 表单错误 | `oauth.go:28`、`:452` |
| DCR（`/register` JSON） | 1 MiB | 413 | `oauth.go:140` |
| `/internal/runtime/*` POST | 64 KiB + 1 | `INVALID_ARGUMENT` | `runtime_api.go:92` |
| Nexus Bridge Runtime 请求 | 64 KiB | `INVALID_ARGUMENT` | `nexusbridge/client.go:371-373` |
| 单条本地控制消息 | 1 MiB | 连接关闭 | `desktopcontrol/control.go:11` |

### `/mcp` 认证与预检

`mcp_endpoint.go:45`：

```go
staticOK := cfg.AuthToken != "" && authorizer.Authorized(r)
oauthOK := authorizedOAuth(r, cfg, oauthStore)
if authRequired && !staticOK && !oauthOK { 401 + Bearer challenge }
```

`cfg.AuthToken != ""` 前置不可省 —— `Bearer.Authorized` 在空 token 时返回 `true`（陷阱 A1）。

预检顺序：读 body（限额）→ JSON 解析 → 回填 `r.Body`/`ContentLength` → 注入 trace context 与 `requestmeta.WithBaseURL`（`:60-62`）。解析失败返回 **HTTP 200** + `-32700 Parse error`（`:78-88`）：MCP 客户端按 JSON-RPC 语义解析，HTTP 状态码不是错误通道。

### 回环闸门与限流

未启用认证时 `/internal/runtime/analytics` **只允许直连回环**，否则 403 `LOCAL_ACCESS_REQUIRED`（`runtime_api.go:49-53`）；其余 `/internal/runtime/*` 在"未认证 + 回环绑定"下对本机任意进程开放（陷阱 A2）。判定看 `RemoteAddr` + `Host`，且任何转发头（`Forwarded`/`X-Forwarded-*`/`X-Real-IP`/`CF-*`）或 `Sec-Fetch-Site: cross-site` 出现即拒绝（`runtime_analytics_access.go:22-42`）。

`request_limit.go:26-50`：固定窗口，最多 4096 key，**满了 fail-closed**。`:55-86` 从 `X-Forwarded-For` **右到左**取第一个非可信代理地址（左侧可伪造）；链中任一值解析失败即回退到直接对端，不做部分信任（`:71-78`）。可信 CIDR 在 `config.go:310-324` 归一化。

**安全头**：状态页 CSP + `X-Content-Type-Options`（`status_page.go:208-214`）；授权页 CSP **不设** `form-action`，否则 POST 自提交被拦（`oauth_authorize_page.go:30-36`、`:44-51`）。

## 2. Runtime API

`MethodAllowed`（`dispatch.go:18-32`）**早于认证**执行，只为生成 405；`AllowHeader`（`:34-49`）填 `Allow` 头。

| 路径 | 方法 | 说明 |
|---|---|---|
| `/internal/runtime/status` | GET | 版本、端口、连接状态 |
| `/internal/runtime/analytics` | GET | 执行记录 + 工具计数（500 环形） |
| `/internal/runtime/overview` | GET | 首页聚合 |
| `/internal/runtime/diagnostics` | GET | 诊断（可选能力） |
| `/internal/runtime/capabilities` | GET / POST | 工具列表；POST 强制 refresh |
| `/internal/runtime/skills` | GET | 全部 Skill |
| `/internal/runtime/skills/{ref}` | GET | 详情 |
| `/internal/runtime/skills/{ref}/files` | GET | 文件列表 |
| `/internal/runtime/skills/{ref}/file/{path}` | GET | 单文件 |
| `/internal/runtime/plugins` | GET | 全部 Plugin |
| `/internal/runtime/plugins/{name}` | GET | 详情 |
| `/internal/runtime/tasks` | GET | 列表（`?status=&limit=`） |
| `/internal/runtime/tasks/{id}` | GET / DELETE | 详情 / 删除 |
| `/internal/runtime/evolve` | POST | 演进管理 |
| `/internal/runtime/mcp` | GET / POST | 列表 / 管理动作 |
| `/internal/runtime/mcp/{name}` | GET | 单 server |
| `/internal/runtime/mcp/oauth/callback` | POST | MCP OAuth 回调 |
| `/internal/runtime/resource-library` | GET / POST | 资源库 |

### 处理链

`httpx/runtime_api.go:40-85`：`MethodAllowed` → 405 → analytics 回环闸门 → 401 → 读 body（仅 4 个 POST 路径，`:89-96`）→ 超时（默认 8s，`resource-library` 2min，`:67-73`）→ `runtimeapi.Dispatch` → 错误映射（`validation`→400、`not_found`→404、其它→500，`:98-111`）。

**POST body 契约**（`dispatch.go:207+`）：64 KiB 上限、`DisallowUnknownFields`、拒绝尾随内容（"must contain exactly one JSON value"）、只转发**真正提供**的可选字段 —— 避免 Go 零值被解释成有语义的空 enum。

`runtimeMCPManageActions` 白名单：`add`、`remove`、`enable`、`disable`、`env_set`、`env_unset`、`env_list`、`refresh`、`authorize`、`auth_clear`。

**Runtime 接口**：基础 `Runtime`（`RuntimeStatus`、`RuntimeAnalytics`、`RuntimeOverview`、`RuntimeCapabilities`、`RuntimeEvolve`）+ 可选 `DiagnosticsRuntime`、`ResourceLibraryRuntime`、`MCPOAuthRuntime`、Skill/Plugin/Task/MCP 各自的 `Runtime*`。用类型断言探测（`dispatch.go:78-82`），不支持时返回 `*_UNSUPPORTED`。

## 3. MCP 协议层

`mcp/server.go:45-56` 传输选项：

| 选项 | 值 | 理由 |
|---|---|---|
| `Stateless` | `true` | 无会话，任意实例可处理任意请求 |
| `JSONResponse` | `true` | 不用 SSE 流式响应 |
| `MaxRequestBodyBytes` | `1 << 20` | 与上限表一致 |
| `PropagateRequestCancellation` | `true` | 客户端取消传到 handler |
| `DisableLocalhostProtection` | `OAuthServerURL != "" && AuthRequired()` | 公网部署需要 |

`Server` 方法：`AgentDockContext:60`、`AgentDockLocalContext:66`（Nexus 远程只取本地部分）、`ToolNames:74`、`ToolContractHash:81`（客户端缓存失效）、`ToolDescriptors:89`（Nexus 注册）、`Invoke:96`、`HTTPHandler:104`、`ServeStdio:108`、`ReadAppResource`（`apps.go:158`）。

**信封** `toolEnvelope`（`:274-305`）：成功 → `{content:[{type:"text",text:<json>}], structuredContent:<obj>}`；错误 → `isError:true`，`content` 内 `{"error":{code,message,category,details}}`；`PERMISSION_REQUIRED` 额外附 `permission_request{tool_name,permission,status:"required"}`（`:283-289`）。`dynamicMCPToolEnvelope`（`:307-320`）保留外部原始 `content` 只补结构化字段。`cloneWithoutInternalImage`（`:322-331`）剥掉内部传阅的 base64 图像，只留 URL/ID。

**MCP Apps 资源**（`apps.go:23-113`）：`off` → 0；默认 7（Image/Context/Workspace/TaskProgress/FileChange/DynamicMCP/Artifact）；有 Nexus +2（Recall/Workflow，`:78-95`）；有 ACP +2（ACPStatus/ACPPrompt，`:96-113`）。`view_image` 无错误且 meta 有 `ui` 且含 `ImageContent` 时追加 `mcpapps.ImageResultText`。工具 → UI 绑定表在 `app/mcp_apps.go:44`（11 条，其中 `workflow_template_manage` 绑 `Action:"match"`）。

## 4. 工具动作枚举

工具名与契约细节见 [核心功能模块](../02-功能结构和策略/核心功能模块.md)，此处只列 schema enum。

| 工具 | 动作 |
|---|---|
| `task_manage` | `create` `list` `get` `checkpoint` `block` `resume` `final_review` `complete`（`tool/task/task_actions.go:12`） |
| `workflow_template_manage` | `publish` `retire` `list` `get` `get_many` `match` `vector_index`（`:14`） |
| `skill_manage` | `install` `remove` `env_set` `env_unset` `env_list`（`tool/skill/contract.go:10`） |
| `plugin_manage` | `inspect` `validate` `install` `update` `enable` `disable` `remove`（`tool/plugin/service.go:137-207`） |
| `mcp_manage` | `list` `inspect` `add` `remove` `enable` `disable` `env_set` `env_unset` `env_list` `refresh` `authorize` `auth_clear`（`tool/mcp/contract.go:21`） |
| `evolve` | `propose` `bind` `supersede` `retract`（`evolution/contract.go:10`） |
| `acp_session` | `start` `list` `status` `close` |
| `browser_session` | `start` `close` `list` |
| `file_publish` | 无动作（单次发布） |

`plugin_manage` 的 `data_policy`：`keep`|`purge`。`mcp_manage` 的 `transport`：`streamable_http`|`stdio`；回调 `local`|`agentdock`|`nexus`。

## 5. 本地控制通道

一行一个 JSON（`desktopcontrol/control.go`）：

```json
{"id":"1700000000000000000","method":"service.status","params":{"runtime_root":"/path"}}
```

| 平台 | 端点 | 建立 | 权限 |
|---|---|---|---|
| Unix | `<runtimeRoot>/control.sock` | `net.Listen("unix",...)` | 目录 0700，socket 0600 |
| Windows | 命名管道（名含 SHA-256） | `\\.\pipe\...` | 保护性 DACL |

Unix 侧（`control_unix.go:18-50`）：目录 0700 → 若存在且非 socket 报错 → 是 socket 则删除陈旧残留 → listen → chmod 0600 → ctx 取消时关闭。

`desktopruntime/control.go:26` 的方法：`ping`（`{"ready":true}`）、`service.status`（服务状态 + `nexus_connected`）、`tunnel.status`。`runtime_root` 必填（`:34-36`），未识别方法返回中文错误 `不支持的本地控制方法: %s`。

## 6. Nexus Bridge

| 项 | 值 | 位置 |
|---|---|---|
| 单条消息上限 | 8 MiB | `client.go:27` |
| invoke 排空超时 | 5s | `:28` |
| 稳定连接判定 | 30s | `:29` |
| 官方端点 / WS 端点 | `https://mcp.nexusdock.co` / `https://ws.nexusdock.co` | `:30-31` |
| 写超时 | 15s | `:405` |

**握手**（`connect`，`:164`）：scheme 改写（`:170-171`）→ path += `protocol.ConnectPath`（`:173`）→ `Authorization: Bearer <device token>`（`:175-177`）→ `context.AfterFunc` 关 socket（`:187-189`）→ `SetReadLimit`（`:192`）→ 发 `node.hello`（`:199-204`）→ 收 `node.ready` 校验（`:205-211`）→ `SetNexusOAuthCallback`（`:212-225`，失败只告警）。

`invoke`（`:279`）的 operation：

| Operation | 参数 | 位置 |
|---|---|---|
| `runtime.request` | `runtimeapi.Request` | `:306` |
| `context.local` | — | `:313` |
| `tool.call` | `{tool, arguments}` | `:315` |
| `resource.read` | `{uri}` | `:326` |
| `artifact.read` | `{artifact_id, offset, max_bytes}` | `:335` |

未识别 → `不支持的 NexusDock 节点操作: %s`。**`recover` 收敛**（`:288-303`）：任何 panic 变 `NODE_OPERATION_FAILED`，只结束当前请求（陷阱 F2）。

`bridgeError`（`:421`）把 `*app.ToolError` 转 `protocol.RemoteError`，其它统一 `internal`。`resource-library` 失败**不记录原始消息**（可能含凭据/路径），只记 `error_code` 与 `error_category`（`:353-357`）。

## 7. ACP

`ProtocolVersion = 1`、`maxEventCount = 512`（`acp/types.go:13,19`）。`SessionRecord`（`:95`）：`ID`、`Agent`、`Status`（`:84`）、`Runs`（`:111 Event`）、时间戳。传输为 stdio JSON-RPC 一行一条，进程由 `acpruntime.Manager`（`manager.go:39`）拉起，`MaxConcurrentRuns` 与 `InteractionTimeout` 来自配置。

`prompts.go` / `prompt_settle.go` / `compatibility.go` 有已知竞态（回归测试在 `steering_compatibility_test.go`），CI 用 `-count=20` 覆盖（陷阱 G1）。

## 8. 认证常量

| 项 | 值 | 位置 |
|---|---|---|
| `ProtocolVersion` / `ServerName` / `PathModel` | `2026-08-11` / `agentdock` / `host` | `config.go:19-21` |
| `RecallTimeoutMS` | `30000` | `:22` |
| OAuth 密码最小长度 | 12 rune | `:342-357` |
| OAuth client secret 最小 | 32 字节 | `:358-361` |
| OAuth TTL | `:615-645` | access / refresh / code |
| OAuth resource 绑定 | `issuer + "/mcp"` | `httpx/oauth.go:389-407`、`:509-518` |
| PKCE | 仅 S256 | `oauth.go:532-536`（`ForcePKCE` 在 `:536`） |

## 相关文档

→ 详见 [MCP工具调用全链路](../03-核心数据业务流/MCP工具调用全链路.md)
→ 详见 [Nexus配对与隧道全链路](../03-核心数据业务流/Nexus配对与隧道全链路.md)
→ 详见 [组件架构与通信](./组件架构与通信.md)（通道拓扑图）
→ 详见 [类型系统与数据模型](./类型系统与数据模型.md)（请求/响应结构体）
