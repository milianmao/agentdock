---
last_updated: 2026-10-21
source_files: []
related_memory: []
---

# 02-功能模块与设计策略 索引

AgentDock 有哪些能力、这些能力怎么组织、为什么这样组织。

## 文档清单

| 文档 | 一句话摘要 | 适用场景 |
|---|---|---|
| [核心功能模块](./核心功能模块.md) | 31 个工具按 14 组能力分组 + 5 类 CLI + 桌面端双栈 + 4 个内置 Skill + 模块交互矩阵 | 找某个能力在哪个包、确认某个工具是否需要 Nexus/浏览器/ACP |
| [设计模式与策略](./设计模式与策略.md) | 12 条贯穿全仓的实现约定：注册表、契约分离、依赖注入、三层路径校验、事务骨架、feature gate、归一化适配器、锁分级、原子写、限额分布 | 要新增一个工具/一条事务链，或想知道"这里为什么这么写" |
| [关键设计决策](./关键设计决策.md) | 16 条 ADR（背景 → 备选 → 取舍 → 结果），含单进程架构、Schema 校验边界、文件系统持久化、Skill/Plugin 分链、generation 更新、cloudflared 解耦、原生桌面重写 | 质疑某个设计、准备推翻重做、或评估改动波及面 |

## 关键事实速查

- 工具注册唯一入口 `buildToolSpecs`，顺序固定，重名 panic（细节见 [核心功能模块](./核心功能模块.md)）。
- 3 个可用性门：`requiresNexus`（`evolve`/`recall_*`/`workflow_template_manage`）、`requiresBrowser`（`browser_*`）、`requiresACP`（`acp_*`）。
- **输入** schema 在 `Runtime.Call` 唯一执行；**输出** schema 不校验，允许加字段。
- 工具包之间零直接 import，跨模块能力用函数值注入。
- 所有持久化走 `atomicfile`（含父目录 fsync），并发靠 `processlock`（跨进程）与 `filelock`（PID + 10min stale）。
- 四种事务共享骨架，但回滚单位不同：Skill 目录 swap / Plugin 版本目录 / 自更新 generation / CLI 文件快照。

## 相关分类

→ 详见 [架构总览](../01-架构总览/README.md)
→ 详见 [业务流](../03-核心数据业务流/README.md)
→ 详见 [总索引](../README.md)
