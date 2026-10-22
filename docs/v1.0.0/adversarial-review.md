# Adversarial Review Report

## 审查等级: L3 (完整审查)

## 审查对象

| 类别 | 范围 |
|---|---|
| 设计文档 | `docs/v1.0.0/设计文档.md`（428 行） |
| 契约规范 | `docs/v1.0.0/契约规范.md`（705 行） |
| 规划任务 | `docs/v1.0.0/规划任务文档.md`（239 行） |
| 宿主客户端 | `internal/codexhost/`：`transport.go` 283 行、`client.go` 316 行、`methods.go` 350 行、`server_requests.go` 75 行、`errors.go` 75 行、`discovery.go` 86 行、`child_windows.go` 22 行、`child_unix.go` 15 行 |
| 工具层 | `internal/tool/computer/`：`service.go` 399 行、`contract.go` 77 行、`errors.go` 55 行、`service_test.go` 470 行 |
| 应用接线 | `internal/app/specs_computer.go:9-18`、`specs_registry.go:16`、`specs.go:155-157`、`runtime.go:163-168`、`runtime.go:244-252`、`runtime.go:324-340`、`computer_tools_test.go:12` |
| MCP 信封 | `internal/mcp/server.go:274`、`:301-309`、`:316-331` |
| 配置 | `internal/config/config.go:90-99`、`:158-165`、`:213-216`；`internal/desktopruntime/config_command.go:19-35`、`:156-166`；`config_windows.go:130-144`；`service_environment_windows.go:58-68`、`:100-126` |
| 桌面端 | `desktop/windows/shared/Models/RuntimeModels.cs:107-117`、`RuntimeService.cs:738-741`、`desktop/windows/winui/CapabilitiesPage.xaml.cs:66`/`:279`；`desktop/macos/AgentDockApp/Sources/ServiceConfigurationController.swift:11`、`:231` |
| 测试 | `internal/codexhost` 11 个 `Test`（`transport_test.go` 7 + `server_requests_test.go` 3 + `client_smoke_test.go` 1 门控冒烟）、`internal/tool/computer` 15 个、`internal/app/computer_tools_test.go` 1 个、`tests/contract/` 25 个 pytest 用例 |
| 审查方式 | 主 Agent 人工对抗审查；**未**委派 SubAgent（外部服务 503 不可用），全部结论由本 Agent 用 `read`/`grep`/`go test` 直接取证 |

## 维度一：覆盖盲区

| 检查项 | 结论 |
|---|---|
| 每条验收标准 S1–S10 是否有可执行断言 | S1→`TestComputerToolsAreFeatureGated`；S2→`TestToolOutputSchemasDoNotExposeGenericOK`（禁顶层 `ok`）+ `TestComputerToolsAreFeatureGated`（`CodexHostEnabled=true` 下 `NewRuntime` 经 `compileAvailableToolContracts` 校验两个 schema 非空且可 `CompileInputSchema`）；S3→`TestOutputContractCoverageMatchesPublicTools`；S4→`TestHandleCallPassesContentThrough`；S5→extractor `ratio=1.0`；S6→**无实测**（见 High-1）；S7→`computerToolEnvelope` 透传 + `service.go:317-323`；S8→`TestHandleStatusKeepsShapeWhenHostUnavailable` + `hostError`（`errors.go:48`）；S9→`TestHandleStatusReportsMissingPlugin`；S10→`TestHandleCallReportsPluginError` |
| 失败路径是否有用例 | 有：宿主不可达（`TestHandleStatusKeepsShapeWhenHostUnavailable`）、插件级失败（`TestHandleCallKeepsHostOnPluginFailure`）、连接终态重建（`TestHandleCallRespawnsHostAfterConnectionLoss`）、陈旧 client 误杀（`TestResetIfDeadIgnoresReplacedClient`）、缺参/非对象参数（两个 `Rejects*`） |
| 传输层异常分支 | 入站超限（`transport_test.go:232`）、出站超限（`:273`）、EOF（`:196`）、关闭后调用（`:182`）、ServerRequest panic（`transport.go:168-185` 有 recover，**无专测**，见 L6） |
| 平台分支 | `child_windows.go:14-18` 与 `child_unix.go` 仅编译期校验（`GOOS=windows/{amd64,arm64}`、`GOOS=darwin/{arm64,amd64}` 四组 cross-build 均通过）；**无运行时断言** |
| 桌面端 | 只做了参数透传；`CapabilitiesPage` 无 `computer_status` 展示，`codex_home` 仅在配置页可见（`CapabilitiesPage.xaml.cs:66`）——契约 S 系列未承诺面板展示，属设计边界，不是缺陷 |

盲区结论：唯一硬缺口是 S6 的 P95 指标无实测数据；其余验收标准均有确定性断言。

## 维度二：安全审计

| 检查项 | 结论 |
|---|---|
| 凭据泄漏到日志 | `client.go:212` 的 stderr 尾在返回前经 `redactEnvironmentValues`（`:258-270`）做完整值替换；`details.stderr` 走同一路径。**缺口**：`len(value) < 4` 跳过（`:264`），短 token 不遮；只做完整值替换，`sk-` 片段不遮（L1） |
| 权限提升 | 代理不越权：`computer_call` 的 `server` 默认 `computer-use`，但可被模型改成任意已注册 server 名。这是**有意设计**（通用代理），且插件侧仍有自己的 `always_allowed_app_ids` 白名单与审批。AgentDock 未自行放行任何操作 |
| 注入面 | `arguments` 用 `OpenObject` 透传，不经任何字符串拼接进 shell；`tools` 名走 JSON-RPC 参数而非命令行；`exec.Command` 用参数数组（`client.go:99`） |
| 路径穿越 | `Config.CodexExecutablePath` 在配置命令侧校验绝对路径 + 普通文件（`config_command.go:156-163`），`CodexHome` 校验绝对路径（`:164-166`）；运行期 `lookPath`（`discovery.go:64`）只做存在性/可执行性检查 |
| 子进程逃逸 | `processcontrol.Attach`（`client.go:127`）把子进程绑到 AgentDock 生命周期；`Close` 幂等（`service.go:301-314`）；Windows 只设 `HideWindow`（`child_windows.go:14-18`），**不加** `CREATE_NO_WINDOW`——已在文档声明为有意选择 |
| 敏感数据落盘 | 截图以 inline content block 透传，`service.go:317-323` 只 `json.Unmarshal` 后放入 map，不写文件、不复制进 `text`（`text` 只拼 `type=="text"`，`methods.go:287-305`） |
| 无鉴权暴露面 | 新增工具随 `AGENTDOCK_CODEX_HOST_ENABLED` 门控，默认 false（`config.go:158`）；门控测试 `TestComputerToolsAreFeatureGated` |
| 依赖完整性 | 无新第三方依赖；`golang.org/x/sys` 已在 `go.mod`，仅 `child_windows.go` 用 |

安全结论：无 Block。L1 是既有 ACP 同款实现（`internal/acp/process.go:156-170`）的复用，不是本特性引入。

## 维度三：生产就绪

| 检查项 | 结论 |
|---|---|
| 启动失败是否拖垮主进程 | 否。`NewRuntime` 只 `toolcomputer.New`（`runtime.go:163-168`），`New` 不启动子进程（`service.go:53-60`）；`TestComputerToolsAreFeatureGated` 在无 Codex 的机器上也 PASS |
| 宿主崩溃恢复 | `resetIfDead`（`service.go:231-248`）只在终态错误上丢弃 client；下次调用 `acquire`→`connect`（`:253-298`）重起子进程并重建 thread。`TestHandleCallRespawnsHostAfterConnectionLoss` 覆盖 |
| 陈旧 client 误杀 | 身份检查 `service.go:238`；`TestResetIfDeadIgnoresReplacedClient` 覆盖 |
| 可观测性 | 失败统一带 `code`/`message`/`category`/`details`（`service.go:370-380`），`details.codex_code` 保留 Codex 原始码（`errors.go:28-54`）；`computer_status` 回显 `codex_home`（`service.go:159-164`）用于现场比对 |
| 日志足够定位 | 部分不足：`handleNotification` 对 `mcpServer/startupStatus/updated` 只 `slog.Debug` 且 `truncate(..., 2048)`（`client.go:221`/`:223`），插件启动失败真因可能被截断（L2） |
| 超时策略 | 两层：工具层 `timeout_ms` 默认 120 s、上限 600 s（`contract.go:19-22`、`service.go:390-399`）；握手/建线程各 30 s 硬超时（`client.go:23`/`:25`）。取小者由 `context.WithTimeout` 嵌套自然实现 |
| 优雅关闭 | `Runtime.Close` 先 `computer.Close()` 再 `command.Close()`（`runtime.go:244-252`）；`computer` 的 Close 错误只 append 不中断（L5） |
| Windows 服务会话 | R2 已声明：Computer Use 仅用户前台会话生效；失败文本透传 |

## 维度四：逻辑漏洞

| 推演 | 结果 |
|---|---|
| 并发首调双起进程 | `acquire`→`connect` 全程持 `s.mu`（`service.go:264-265`），第二个调用者阻塞后看到 `s.client != nil` 直接复用（`:267-270`）。**安全** |
| 同一 client 上两个并发失败 | 两个 goroutine 各自 `resetIfDead`；第一个置 `s.client=nil` 并 `Close`，第二个在 `:238` 身份检查失败直接返回，不会二次 Close。**安全** |
| 失败路径两个 goroutine 抢 `s.mu` | `resetIfDead` 内 `s.mu.Lock`（`:235`），`connect` 内 `s.mu.Lock`（`:264`），同一把锁，无死锁（`resetIfDead` 不调 `connect`） |
| 无插件 | `computerUse` 默认值 `available=false`（`service.go:95-99`）；`server` 过滤不影响判定（判定在 `:100-114`，过滤在 `:115-117`），`TestHandleStatusFilterDoesNotHideAvailability` 覆盖 |
| 非对象 `arguments` | 双层：schema 层 `runtime.go:324-340` → `INVALID_ARGUMENT`（实测文案 `tool arguments do not match the declared input schema`）；handler 层 `service.go:208-211` → `COMPUTER_INVALID_ARGUMENTS`。**不静默降级** |
| 翻页不收敛 | `for range mcpStatusMaxPages`（`methods.go:148`）上限 50，穷尽后 `:172` 返回已收集结果，`host_ok` 仍 true（L3） |
| 入站 33 MiB 单行 | `transport.go:148-157` → `CODEXHOST_MESSAGE_TOO_LARGE`，`Retryable=false`，`details` 只有 `limit`。**已测** |
| 出站超限 | `transport.go:214-215` 预检后直接返回，`writeMessage` 不进入 `writeMu` 段，一个字节都不写。`TestWriteRejectsOversized` 断言 `writer.Len()==0` |
| 请求超时后宿主侧已执行 | `transport.go:100-102` 只 `removePending`，宿主可能已执行动作却应答丢失。at-least-once 语义未在契约声明（L4） |
| `timeout_ms` 越界 | `service.go:390-399` 夹到 `[1, 600000]`；schema 层 `BoundedInteger` 先拦 |
| `nil` map / nil receiver | `ServerName()`（`:63-68`）、`Close()`（`:301-305`）、`toolNames`（`:382-388`）都有 nil 守卫；`ToolCallResult.Images()`（`methods.go:309-313`）有 nil 守卫 |
| `content` 空 | `computerToolEnvelope`（`server.go:319-321`）回落单 text block，不会产出空 content |

无死循环、无空指针、无除零。

## 维度五：依赖完整

| 检查项 | 结论 |
|---|---|
| 跨模块引用是否都已定义 | `hostClient` 接口 5 个方法（`service.go:28-34`）全部由 `*codexhost.Client` 实现：`Info()`（`methods.go:51`）、`EnsureThread`（`client.go:151`）、`ListMCPServers`（`methods.go:139`）、`CallMCPTool`（`:239`）、`Close`（`client.go:168`） |
| 配置项默认值 | `CodexHostEnabled` 默认 false（`config.go:158`）；`ComputerUseServer` 默认 `computer-use`（`:162-165`）；`ServerName` 空则补默认（`service.go:54-56`）；`timeout_ms` 默认 120000（`contract.go:20`）；`detail` 空则 `toolsAndAuthOnly`（`methods.go:143-145`） |
| 环境变量传导链 | 配置 → `config.go:213-216` → `service_environment_windows.go:100-126`（Windows 服务）→ `AgentDockLocalContext`/`runtime.go:163-168` → `codexhost.Config` → `client.go:88-95` overlay `CODEX_HOME` |
| 桌面端字段对齐 | C# `RuntimeModels.cs:107-117` 4 个属性 ↔ `RuntimeService.cs:738-741` 4 个 `--` 参数 ↔ `config_command.go:28-31` 4 字段。**对齐** |
| 契约 vs 实现 | `contract.go:48-55` 的 `OutputSchema` 字段集与 `service.go:154-164`/`statusFailure:345-356` 实际输出一致；`CallOutputSchema`（`contract.go:65-76`）与 `toolResult`（`service.go:316-337`）/`callFailure`（`:358-368`）一致 |
| 零消费方符号 | `CodeUnsupportedMethod`（`errors.go:23`）Go 侧无引用（L7）；`ToolCallResult.Images()`/`ImageContent`（`methods.go:309-330`）仅 `client_smoke_test.go:79` 计数（L8） |
| 测试数据 | `internal/codexhost/testdata/schema/*.json` 8 个，`server_requests_test.go:18` 逐个校验 |

## 维度六：算法正确性

| 检查项 | 结论 |
|---|---|
| 分页游标 | `nextCursor` 为 `*string`；`nil` 或 trim 后空串都视为终结（`methods.go:167-169`）——避免空串游标导致的无限循环 |
| `toolNames` 排序 | 无序切片（`service.go:382-388`），文档已声明「调用方不得依赖顺序」（`设计文档.md:290`）。**未排序不是缺陷**，但快照测试不能断言顺序 |
| `timeoutFromArgs` 夹取 | `<1` → 默认值（**不是** 1 ms）；`>600000` → 上限。`<1` 回落默认值而非最小值是刻意行为，schema 层已用 `BoundedInteger(1, 600000)` 拦截，handler 层只是兜底 |
| `isEmptyContent` 判定 | 用 `len(content)==0`，不判 nil——`content:null` 与 `content:[]` 都回落 text block（`server.go:318-321`），语义一致 |
| `structuredContent` 判定 | `len>0 && string != "null"`（`service.go:332`）——正确区分 JSON `null` 与缺省 |
| `isError` 合成 | `payload["is_error"] \|\| !payload["host_ok"]`（`server.go:322-325`）：宿主失败必为 error，插件失败也为 error，两者都不掩盖 |
| `isTerminalHostError` 集合 | 只含 `CONNECTION_CLOSED`/`CONNECTION_FAILED`/`NOT_RUNNING`（`service.go:218-225`）。`START_FAILED` 不在内——启动失败的 client 从未被缓存，无需 reset。**正确** |
| 错误码映射 | `errors.go:47-54`：`CODEX_NOT_FOUND`/`NOT_RUNNING`/`START_FAILED` → `HOST_UNAVAILABLE`；`REQUEST_CANCELED` → `TIMEOUT`；其余 → `CALL_FAILED`。12 → 4 的收敛无遗漏 |
| `redactEnvironmentValues` 复杂度 | O(env × len(text))，env 来自 `MinimalSystemEnv`，量级 O(10)，text 上限 64 KiB（`stderrTailBytes`）——可接受 |

无相似度/hash/加权运算，不涉及数值算法正确性风险。

## 维度七：性能边界

| 检查项 | 结论 |
|---|---|
| 单消息上限 | 32 MiB（`transport.go:17`）双向生效；`bufio.Scanner` 初始 64 KiB 自适应增长至 32 MiB（`:123`）。超大无障碍树 + 截图是本特性的主要负载，32 MiB 留有余量 |
| 内存峰值 | 单条消息峰值 ≈ 2×32 MiB（scanner 缓冲 + `append([]byte(nil), ...)` 拷贝，`transport.go:125`）。并发 in-flight 时每条请求独立等待，但读循环串行处理，不会同时持有 N 份消息体 |
| 并发模型 | 读循环单 goroutine（`:121`），写侧 `writeMu` 串行（`:40`/`:218-219`），pending map 用 `pendingMu` 保护（`:42`）。**无竞态**（设计层面；`-race` 因本机无 gcc 不可用，见验证缺口） |
| 首调延迟 | `connect` 持锁覆盖 `Start`+`EnsureThread`，最长 30+30 s（R10 已声明）。只影响首次调用 |
| 翻页开销 | 50 页上限，每页一次 RPC。最坏 50 次串行往返（`methods.go:148-171`），全部在 `timeout_ms` 预算内 |
| `status` 大输出 | `include_tools=true` 时每个 server 的每个 tool 的 `inputSchema` 都要 `json.Unmarshal`（`service.go:142-147`）。server 数与 tool 数由 Codex 决定；实测冒烟 91 tools（Linux `codex_apps`）可在单个 `timeout_ms` 内完成 |
| 截图 base64 复制次数 | `content` 逐项 `Unmarshal` 一次（`service.go:317-323`），`text` 不含 base64，信封直接引同一 map（`server.go:326-330`）。**无额外复制** |
| 长时运行内存 | stderr 环形尾固定 64 KiB（`client.go:27`）；pending map 在超时/关闭时删除（`transport.go:101`/`:104`）。**无泄漏** |
| P95 目标 | S6 的 3000 ms 无实测数据（High-1） |

## 契约对齐（附加维度）

| 检查项 | 结论 |
|---|---|
| extractor 覆盖率 | `endpoints=6 covered=6 ratio=1.0`，`python3 -m pytest tests/contract -q` → 25 passed |
| 契约 fence 是否含真实示例 | `codex_home` 已进两处成功示例（`契约规范.md:148`/`:294`），生成的 `test_computer_contract.py:60`/`:103`/`:106` 与之同步 |
| 契约声明 vs 输出 schema required | 契约示例推断出的 required 含 `codex_home`（extractor 行为），Go 侧 `OutputObject(props, "host_ok")` 只要求 `host_ok`。**两者不矛盾**：前者是示例自洽性校验，后者是运行时 schema |
| 顶层 `ok` 禁令 | `TestToolOutputSchemasDoNotExposeGenericOK`（`internal/mcp/registry_test.go:52`）覆盖全 registry；`computer_status`/`computer_call` 用 `host_ok` |
| 错误码表 | 契约 §1 的 12 个 `CODEXHOST_*` ↔ `errors.go:11-24`；4 个 `COMPUTER_*` ↔ `internal/tool/computer/errors.go:14-19` |
| 文档锚点 | `python3 /tmp/audit_anchors.py` → `checked_ok=390 problems=12`，12 条全为 `ToolDefinitions.swift`（仓库外 `/tmp/ocu` 镜像，已知假象）；仓内锚点 100% 在范围内且内容逐条 awk 核实 |

## 发现问题

### 🔴 Block (必须修复)

| # | 维度 | 问题 | 建议 |
|---|---|---|---|
| — | — | **无** | — |

### 🟡 High (建议修复)

| # | 维度 | 问题 | 证据 | 建议 |
|---|---|---|---|---|
| H1 | 覆盖盲区 | S6「P95 ≤ 3000 ms」是设计目标，**无任何实测数据**。本机无插件、无 Windows/macOS 真机，无法测量 | `契约规范.md:57`；`设计文档.md:403`（R3：Linux 版无该插件，真机验证只在 Windows） | 保留为「设计目标，未实测」并在交付文案中显式声明；Windows 真机 T12 第 4–6 步补记单次耗时；**不得**在发布说明中写成已达成 |
| H2 | 契约对齐 | extractor 的 `infer_schema` 只按数组首元素推断（`extract.py:387-390`），`computer_call.content` 是异构数组（text + image），fence 无法表达 item schema。当前靠「不写 fence + 契约正文说明」规避，属契约表达能力缺口而非实现缺口 | `extract.py:387-390`；`契约规范.md:355` | 保持现状（写 fence 会把 item schema 钉成 `{type,text}`，反而更错）；在契约 §2.4 明确「item schema 由宿主 `inputSchema` 单一事实源决定，本契约只声明透传语义」 |

### 🟢 Low (可后续优化)

| # | 维度 | 问题 | 证据 | 建议 |
|---|---|---|---|---|
| L1 | 安全审计 | `redactEnvironmentValues` 对 `<4` 字节值跳过，且只做完整值替换，`sk-xxxx` 部分泄露不遮 | `client.go:258-270`，跳过点 `:264` | 与 ACP 侧（`internal/acp/process.go:156-170`）同款实现，属既有技术债；建议统一抽到共用包后加前缀规则 |
| L2 | 生产就绪 | 插件启动失败真因只在 `slog.Debug` 且截断到 2048 字节 | `client.go:218-229`，截断 `:221`/`:223` | `mcpServer/startupStatus/updated` 提到 `Info` 级别；截断上限按字段拆开（状态 vs 错误详情） |
| L3 | 逻辑漏洞 | 翻页 50 页后静默返回，`host_ok` 仍 true，无「已截断」标记 | `methods.go:148`/`:172` | 可在 `computer_status` 加 `truncated: true`；需同步契约与 coverage 变体，故本轮不做 |
| L4 | 逻辑漏洞 | 请求超时后宿主侧动作可能已执行但应答丢失（at-least-once），契约未声明 | `transport.go:100-102` | 在契约 §3.3.2 加一句「`COMPUTER_TIMEOUT` 不保证宿主未执行」 |
| L5 | 生产就绪 | `Runtime.Close` 对 `computer` 的 Close 错误只 append，聚合错误文本可能被后续覆盖 | `runtime.go:244-252` | 现状与其他子系统一致（`browser`/`command`/`dynamicMCP` 同款），统一改才合理 |
| L6 | 覆盖盲区 | `handleServerRequest` 的 `recover()`（`transport.go:168-185`）无专测 | `transport.go:170-173` | 补一个注入 panic handler 的用例；需改 `answerServerRequest` 为可注入，属结构改造 |
| L7 | 依赖完整 | `CodeUnsupportedMethod`（`errors.go:23`）Go 侧零消费方。原契约 `:94` 声称它对应宿主未知 ServerRequest，但实际代码直接回 JSON-RPC `-32601`（`server_requests.go:64`），该常量从未产出 | `internal/codexhost/errors.go:23`、`server_requests.go:63-65` | **本轮已修正契约措辞**，标注为「保留码，当前不会出现在输出里」；常量本身保留作语义占位，不删 |
| L8 | 依赖完整 | `ToolCallResult.Images()`/`ImageContent`（`methods.go:309-330`）生产代码零调用，仅冒烟日志计数 | `methods.go:309`、`client_smoke_test.go:79` | 与已删的 `Meta` 不同，它被测试真实使用且语义清晰；保留 |
| L9 | 生产就绪 | `New()` 只判空不 trim `ServerName`；面板直传带空格的名字会与宿主广播名不匹配 | `service.go:53-60` | 曾尝试修复后回滚（超出本轮范围）；建议配置命令层 trim |

## 手动推演记录

10 条推演全部落到具体行号，逐条给出「输入 → 路径 → 输出」。

1. **首调并发双起进程**
   输入：两个 goroutine 同时调 `computer_call`，`Service.client == nil`。
   路径：两者都进 `acquire`（`service.go:253`）→ `connect`（`:263`）→ `s.mu.Lock()`（`:264`）。先到者继续，后到者阻塞。
   输出：先到者建好 `s.client`/`s.threadID`（`:295-296`）并返回；后到者醒来在 `:267-270` 命中 `s.client != nil && s.threadID != ""` 直接复用。**只起一个子进程**。

2. **死 client 重建**
   输入：宿主进程被杀。
   路径：后续 `CallMCPTool`（`service.go:191`）返回 `CODEXHOST_CONNECTION_CLOSED` → `resetIfDead`（`:193`）→ `isTerminalHostError` 为真（`:218-225`）→ 身份检查通过（`:238`）→ `s.client = nil`（`:242`）→ `client.Close()`（`:246`）。
   输出：`callFailure` 带 `error.code=COMPUTER_CALL_FAILED`、`details.codex_code=CODEXHOST_CONNECTION_CLOSED`；下次调用 `connect` 走 `:282-297` 重新 `start` + `EnsureThread`。`TestHandleCallRespawnsHostAfterConnectionLoss` 验证。

3. **同一 client 两个并发失败**
   输入：同一 client 上两个 in-flight 调用同时因连接关闭失败。
   路径：goroutine A 进 `resetIfDead`，持锁，`s.client = nil`，解锁，`Close`。goroutine B 进 `resetIfDead`，持锁后在 `:238` 见 `s.client(nil) != client`，直接 `return`（`:239-240`）。
   输出：**不会二次 Close**，也不会误杀 A 期间新建的 client。`TestResetIfDeadIgnoresReplacedClient` 验证。

4. **无插件**
   输入：宿主返回 `servers=[codex_apps]`（无 `computer-use`）。
   路径：`computerUse` 取默认值（`service.go:95-99`）→ 循环 `:100-114` 无一命中 → `:115-117` 的过滤只影响 `reported` 切片。
   输出：`host_ok=true`、`computer_use={"available":false,"server":"computer-use","reason":"..."}`、`servers` 含实际 server。`TestHandleStatusReportsMissingPlugin` 验证。

5. **宿主不可达时 status 形状与信封**
   输入：`codex` 不在 PATH。
   路径：`acquire`（`service.go:253`）→ `connect` → `start` 失败 → `hostError("start Codex app-server", ...)` → `HandleStatus` 的 `:81-84` 分支 → `statusFailure`（`:345-356`）。MCP 层 `server.go:307-309` 走 `computerToolEnvelope` → `host_ok=false` 使 `isError=true`（`:322-325`）。
   输出：`{"host_ok":false,"servers":[],"computer_use":{...},"error":{"code":"COMPUTER_HOST_UNAVAILABLE","details":{"codex_code":"CODEXHOST_CODEX_NOT_FOUND"}}}` + 信封 `isError=true`。`TestHandleStatusKeepsShapeWhenHostUnavailable` 验证。

6. **非对象 `arguments` 双层拦截**
   输入：`computer_call{tool:"list_apps", arguments:"nope"}`。
   路径：第一层 `Runtime.Call` → `validateToolArguments`（`runtime.go:324-340`）→ `INVALID_ARGUMENT`，实测文案 `tool arguments do not match the declared input schema`，`details.reason` 为 `toolcontract.CompactValidationError` 结果。绕过 `tools/call` 直接调 handler 时进第二层 `argumentsArg`（`service.go:203-213`）→ `:208-211` → `COMPUTER_INVALID_ARGUMENTS`，**不调宿主**。
   输出：两种情况都不产生 `CallMCPTool` 调用。`TestHandleCallRejectsNonObjectArguments` 验证第二层。

7. **50 页翻页静默截断**
   输入：宿主每到最后一页都返回非空 `nextCursor`。
   路径：`for range mcpStatusMaxPages`（`methods.go:148`）跑满 50 轮 → 循环结束 → `:172` `return servers, nil`。
   输出：返回前 50 页的 servers，**无 error**；`HandleStatus` 的 `host_ok` 仍为 true。L3 记录。

8. **入站 33 MiB 单行**
   输入：宿主写一行 `maxMessageBytes+1` 字节。
   路径：`bufio.Scanner` 在 `transport.go:124` 返回 false，`scanner.Err()` 为 `bufio.ErrTooLong` → `:149-155` → `finish(newError(CodeMessageTooLarge, ..., false, {"limit":...}, nil))`。
   输出：所有 pending 被唤醒为 `CODEXHOST_MESSAGE_TOO_LARGE`、`Retryable=false`；**不**落 `CONNECTION_FAILED`。`TestOversizeInboundMessageIsReportedAsTooLarge` 验证。

9. **出站 32 MiB+1 零字节写入**
   输入：构造 `maxMessageBytes+1` 的序列化结果。
   路径：`writeMessage` 在 `transport.go:214-215` 预检即返回，未进 `writeMu`（`:218`），未调 `writer.Write`（`:225`）。
   输出：`CodeMessageTooLarge`、`Retryable=false`、`details.limit==32<<20`，`writer.Len()==0`。`TestWriteRejectsOversized` 验证。

10. **`codex_home` 完整链路**
    输入：`CODEX_HOME=/home/dev/.codex` 的 shell 里启动 AgentDock，`computer_status` 调用。
    路径：`config.go:215`（`AGENTDOCK_CODEX_HOME`，空则 `discovery.go:48-59` 回落）→ `runtime.go:165` → `codexhost.Config.CodexHome` → `client.go:92-94` overlay → 子进程握手 → `methods.go:43-45` 存进 `c.info` → `Info()`（`:49-58`）→ `service.go:162-164` 写入 `result["codex_home"]`。
    输出：`codex_home="/home/dev/.codex"`；若握手未完成或字段为空则**整键缺席**（`:162` 的 `!= ""` 守卫）。`TestHandleStatusReportsResolvedCodexHome` / `TestHandleStatusOmitsUnknownCodexHome` 双向验证。

## 行号核验清单

逐条 `sed -n` 拉出目标行内容比对，全部命中；`/tmp/audit_anchors.py` 复验 `checked_ok=390`。

| # | 锚点 | 目标行内容摘要 | 命中 |
|---|---|---|---|
| 1 | `transport.go:17` | `const maxMessageBytes = 32 << 20` | ✅ |
| 2 | `transport.go:40` | `writeMu sync.Mutex` | ✅ |
| 3 | `transport.go:66` | `func (c *transport) call(ctx, method, params)` | ✅ |
| 4 | `transport.go:121` | `func (c *transport) readLoop()` | ✅ |
| 5 | `transport.go:123` | `scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)` | ✅ |
| 6 | `transport.go:148-157` | `bufio.ErrTooLong` → `CodeMessageTooLarge` 分支 | ✅ |
| 7 | `transport.go:159` | `c.finish(...CodeConnectionClosed...)` | ✅ |
| 8 | `transport.go:165-186` | `handleServerRequest` + `recover()` | ✅ |
| 9 | `transport.go:189` | `func (c *transport) handleResponse` | ✅ |
| 10 | `transport.go:209` | `func (c *transport) writeMessage` | ✅ |
| 11 | `transport.go:214-215` | 出站长度预检 | ✅ |
| 12 | `client.go:23` / `:25` / `:27` | `initializeTimeout` / `threadTimeout` / `stderrTailBytes` | ✅ |
| 13 | `client.go:92-95` | `overlay["CODEX_HOME"] = discovery.CodexHome` + `envstore.Merge` | ✅ |
| 14 | `client.go:99` | `exec.Command(discovery.Path, "app-server")` | ✅ |
| 15 | `client.go:127` | `processcontrol.Attach(command)` | ✅ |
| 16 | `client.go:142` | `if _, err := client.Initialize(...)` | ✅ |
| 17 | `client.go:168` | `func (c *Client) Close() error` | ✅ |
| 18 | `client.go:218-229` | `handleNotification` switch | ✅ |
| 19 | `client.go:221` / `:223` | `truncate(..., 2048)` | ✅ |
| 20 | `client.go:258-270` | `redactEnvironmentValues`（跳过点 `:264`） | ✅ |
| 21 | `methods.go:21` | `func (c *Client) Initialize` | ✅ |
| 22 | `methods.go:43-45` | `c.info = result` | ✅ |
| 23 | `methods.go:51-58` | `func (c *Client) Info()` | ✅ |
| 24 | `methods.go:139` / `:144` | `ListMCPServers` / `detail = "toolsAndAuthOnly"` | ✅ |
| 25 | `methods.go:148` / `:172` | `for range mcpStatusMaxPages` / 穷尽返回 | ✅ |
| 26 | `methods.go:176` | `const mcpStatusMaxPages = 50` | ✅ |
| 27 | `methods.go:239` | `func (c *Client) CallMCPTool` | ✅ |
| 28 | `methods.go:287` | `Text()` | ✅ |
| 29 | `methods.go:309-330` | `Images()` + `ImageContent` | ✅ |
| 30 | `discovery.go:25` / `:48` / `:64` | `DiscoverCodex` / `resolveCodexHome` / `lookPath` | ✅ |
| 31 | `errors.go:11-24`（codexhost） | 12 个 `CODEXHOST_*` 常量 | ✅ |
| 32 | `service.go:28-34` | `hostClient` 接口 5 方法（含 `Info()`） | ✅ |
| 33 | `service.go:53-60` | `New` 补默认 server 名，惰性启动 | ✅ |
| 34 | `service.go:95-99` | `computerUse` 默认值 map | ✅ |
| 35 | `service.go:100-114` / `:115-117` | 判定循环 / `serverFilter` 过滤 | ✅ |
| 36 | `service.go:142-147` | `input_schema` Unmarshal | ✅ |
| 37 | `service.go:159-164` | `codex_home` 回显 | ✅ |
| 38 | `service.go:172-197` | `HandleCall` 函数体 | ✅ |
| 39 | `service.go:203-213` / `:208-211` | `argumentsArg` / 非对象报错 | ✅ |
| 40 | `service.go:218-225` | `isTerminalHostError` | ✅ |
| 41 | `service.go:231-248` / `:238` | `resetIfDead` / 身份检查 | ✅ |
| 42 | `service.go:264-265` | `s.mu.Lock()` / `defer Unlock` | ✅ |
| 43 | `service.go:287-294` | 启动/建线程失败即回收 | ✅ |
| 44 | `service.go:301-314` | `Close` | ✅ |
| 45 | `service.go:316-368` | `toolResult` / `statusFailure` / `callFailure` | ✅ |
| 46 | `service.go:382-388` | `toolNames` | ✅ |
| 47 | `service.go:390-399` | `timeoutFromArgs` | ✅ |
| 48 | `errors.go:14-19` / `:28-54` / `:47-54`（computer） | 4 个 `COMPUTER_*` / `hostError` / 映射 switch | ✅ |
| 49 | `contract.go:19-22` / `:48-55` / `:65-76` | 超时常量 / `OutputSchema` / `CallOutputSchema` | ✅ |
| 50 | `server.go:274` / `:301-309` / `:316-331` / `:319-321` / `:322-325` | `toolEnvelope` / 分支 / `computerToolEnvelope` / 回落 / `isError` 合成 | ✅ |
| 51 | `runtime.go:163-168` / `:244-252` / `:296` / `:324-340` | `NewRuntime` / `Close` / `Call` / `validateToolArguments` | ✅ |
| 52 | `specs_computer.go:9-18`、`specs_registry.go:16`、`specs.go:155-157` | 两个工具 spec / 注册 / 门控 | ✅ |
| 53 | `config.go:90-99` / `:158` / `:162-165` / `:213-216` | 4 字段 / 门控默认 false / server 默认 / 赋值 | ✅ |
| 54 | `config_command.go:19-35` / `:156-166`；`config_windows.go:130-144`；`service_environment_windows.go:58-68` / `:100-126` | 请求结构 / 校验 / 映射 / JSON 字段 / 环境导出 | ✅ |
| 55 | `child_windows.go:14-18` | `configureChild` 只设 `HideWindow` | ✅ |
| 56 | `registry_test.go:52` | `TestToolOutputSchemasDoNotExposeGenericOK` | ✅ |
| 57 | `output_contract_coverage_test.go:51-52` / `:95-97` | 两条登记 / `IntegrationOnly` 白名单 | ✅ |
| 58 | `computer_tools_test.go:12` | `TestComputerToolsAreFeatureGated` | ✅ |
| 59 | `transport_test.go:232` / `:273` | 两个超限用例 | ✅ |
| 60 | `service_test.go:270` / `:288` | 两个 `codex_home` 用例 | ✅ |
| 61 | `desktop/windows/shared/Models/RuntimeModels.cs:107-117`、`desktop/windows/shared/Services/RuntimeService.cs:738-741`、`desktop/windows/winui/CapabilitiesPage.xaml.cs:66`/`:279` | C# 4 属性 / 4 参数 / 读写 | ✅ |
| 62 | `desktop/macos/AgentDockApp/Sources/ServiceConfigurationController.swift:11` / `:231` | macOS 配置属性 / 环境变量 | ✅ |

## 审查发现 → 当场修复

| # | 发现 | 修复 |
|---|---|---|
| 1 | 死 client 永不重建：终态错误后 `s.client` 一直指向死对象，后续调用必然再失败 | 新增 `resetIfDead`（`service.go:231-248`）+ 在 `HandleStatus`/`HandleCall` 的错误分支调用；`TestHandleCallRespawnsHostAfterConnectionLoss` |
| 2 | `server` 过滤造成假阴性：过滤发生在判定之前时，`server` 传别的名字会让 `computer_use.available` 变 false | 判定提前到 `service.go:100-114`，过滤后移到 `:115-117`；`TestHandleStatusFilterDoesNotHideAvailability` |
| 3 | `codexhost.Version` 死代码 | 删除；改用 `buildinfo.Version`（`client.go:84-86`） |
| 4 | `resetIfDead` 并发误杀：迟到的失败会拆掉并发新建的 client | 加身份检查（`service.go:238`）；`TestResetIfDeadIgnoresReplacedClient` |
| 5 | `Service.Enabled` / `Config.Enabled` 死代码 | 删除，门控统一走 `CodexHostEnabled` + `requiresComputer`（`specs.go:155-157`） |
| 6 | `computer_status` 失败仍被信封报成功：失败结果无 `content`，通用信封判 `isError=false` | `toolEnvelope` 加 `computer_*` 分支（`server.go:307-309`）+ `computerToolEnvelope` 以 `host_ok` 参与合成（`:322-325`） |
| 7 | `statusFailure` 丢 `computer_use`：失败路径缺字段，客户端需先判 `host_ok` | `statusFailure`（`service.go:345-356`）带完整 `computer_use`；`TestHandleStatusKeepsShapeWhenHostUnavailable` |
| 8 | `arguments` 静默降级：非对象参数被替换成 `{}`，换来误导性的插件 schema 报错 | `argumentsArg`（`:203-213`）直接报 `COMPUTER_INVALID_ARGUMENTS`；`TestHandleCallRejectsNonObjectArguments` |
| 9 | 入站超长误报为连接失败：`bufio.ErrTooLong` 落 `CONNECTION_FAILED`（retryable），调用方会无意义重试 | 单独映射 `CodeMessageTooLarge` + `Retryable=false`（`transport.go:148-157`）；`TestOversizeInboundMessageIsReportedAsTooLarge` |
| 10 | `ToolCallResult.Meta` 死字段：解析了但不透传，契约也不声明 | 删除（`methods.go:229-233`、`:256-260`） |
| 11 | 出站超限零测试：「超限一个字节都不写」是可断言不变量却无覆盖 | `TestWriteRejectsOversized`（`transport_test.go:273-300`） |
| 12 | **`computer_status` 不回显 `codex_home`**：三处文档承诺，实现只把 `initialize.codexHome` 存在 `c.info`。`CODEX_HOME` 指错目录与「插件未安装」输出完全同形，是现场误判第一来源 | `hostClient` 加 `Info()`（`service.go:29`）；`HandleStatus` 回显非空值（`:159-164`）；输出 schema 加 `codex_home`（`contract.go:52`，不入 required）；双向测试 `TestHandleStatusReportsResolvedCodexHome` / `TestHandleStatusOmitsUnknownCodexHome` |

## 文档口径修正（不算代码修复）

| # | 位置 | 修正 |
|---|---|---|
| 1 | `契约规范.md` §3.3.2（`:353-354`） | `arguments` 校验从「handler 单层」改为实测的两层口径（schema 层 `Runtime.Call` + handler 层 `HandleCall`） |
| 2 | `设计文档.md` / `契约规范.md` / `规划任务文档.md` | `transport.go` 锚点整体重排（新增 `ErrTooLong` 分支导致后移） |
| 3 | 三份文档 | 6 类历史 off-by 引用同步（`extract.py:389-391` → `387-390`、`transport_test.go` 行数 224 → 300 等） |
| 4 | `契约规范.md` `ToolCallResult` fence | 去掉 `Meta` 字段 |
| 5 | `契约规范.md:5` | 修复乱码 |
| 6 | `规划任务文档.md:67`/`:88`/`:132`/`:140`/`:180`；`契约规范.md:52`/`:53`；`设计文档.md:136` | **5 处假测试名**替换为实测存在的 `Test` 名（如 `TestTransport` → `-run 'TestCall\|TestServerRequest\|TestOversize\|TestWriteRejects'`；`TestOutputContractCoverage` → `TestOutputContractCoverageMatchesPublicTools`） |
| 7 | `设计文档.md:383` | 「单条消息超」→「单条消息超限」 |
| 8 | `设计文档.md:77` | `transport_test.go` 行数 224 → 300 |
| 9 | `契约规范.md:148`/`:294`、`:190`、`:317`；`设计文档.md:272`、`:293` | `codex_home` 两处示例 fence + 三处字段语义 + 流程图终端节点 |
| 10 | `契约规范.md:94` | `CODEXHOST_UNSUPPORTED_METHOD` 原写「宿主发起未知 ServerRequest」，与实现不符（实际直接回 `-32601`，`server_requests.go:64`）；改为「保留码，当前不会出现在输出里」 |
| 11 | 三份文档（本轮） | 全量锚点重映射：`service.go` 因 `Info()`（+1）与 `codex_home` 块（+7）整体后移，逐条 awk 核实后更新（`设计文档.md` 约 22 处、`契约规范.md` 6 处、`规划任务文档.md` 10 处），并顺带修正 `discovery.go:48-61` → `:48-59`、`CallMCPTool` `:240` → `:239`、`connect` 闭合行 `:282-286` → `:287-294` |

## 审查后复验

```
$ CGO_ENABLED=0 go test ./internal/tool/computer/ -count=1
ok  	github.com/uvwt/agentdock/internal/tool/computer	0.010s

$ CGO_ENABLED=0 go test ./internal/codexhost/ -run 'TestCall|TestServerRequest|TestOversize|TestWriteRejects' -count=1
ok  	github.com/uvwt/agentdock/internal/codexhost

$ python3 <contract-oracle-extractor>/extract.py --sdd docs/v1.0.0/契约规范.md ...
[contract-oracle-extractor] endpoints=6 covered=6 ratio=1.0 test_files=2 sse_events=0 enums=0

$ python3 -m pytest tests/contract -q
25 passed

$ python3 /tmp/audit_anchors.py
checked_ok=390  problems=12   # 12 条全为 ToolDefinitions.swift（仓库外镜像）
```

## 未决冲突（需真机确认，不属本次审查缺陷）

上游逆向笔记 `codex-computer-use-cli.md`（**仓库外**，随设计输入提供；`设计文档.md:401` 引用同一事实）记录 macOS 上 `mcpServer/tool/call` 返回 `Apple event error -10000: Sender process is not authenticated`。若 Windows 复现，**宿主路线整体不成立**，须回落自研执行器。`设计文档.md:401`（R1）已写明分支与「不得静默回退」。该风险无法在本机（Linux，无插件）证伪或证实。

## Overall

**Pass with Comments**（Block = 0；High = 2；Low = 9）

- High-1（S6 未实测）是**口径问题**而非实现问题，已在契约与设计文档显式标注「设计目标，未实测」，不影响合入。
- High-2（异构数组表达力）是 extractor 表达能力上限，已用「不写 fence + 正文声明」规避并可解释。
- Low 9 条均为可后续优化项，其中 L1/L5/L9 与既有子系统同款，单独改会破坏一致性。
- 12 条审查发现已当场修复并有测试或实测证据兜底。
- 唯一无法在本机关闭的是 Windows 真机链路（T12），需按清单执行。
