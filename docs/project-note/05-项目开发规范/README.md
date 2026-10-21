---
last_updated: 2026-10-21
source_files:
  - Makefile
  - .github/workflows/ci.yml
  - scripts/governance/inventory.yaml
related_memory: []
---

# 05-项目开发规范 索引

三篇：写代码、写测试、提交与发布。规范中凡有工具强制的，此处只指位置，不复述。

## 文档清单

| 文档 | 回答什么 | 首屏价值 |
|---|---|---|
| [编码规范](./编码规范.md) | 代码怎么放、错误怎么造、日志怎么写 | `make check` 五道门禁、`contract.go`/`service.go`/`store.go` 分工、`ToolError` 三构造器 |
| [测试策略](./测试策略.md) | 什么必须测、在哪测、怎么跑 | 三平台矩阵、`-count=20` 竞态、`browser_integration` tag、**禁止**测什么 |
| [Git工作流与CI](./Git工作流与CI.md) | 怎么提交、CI 怎么跑、发布怎么触发 | Conventional Commits + 中文主题、6 个 workflow、3 条硬性发布约束 |

## 一张图定位

```mermaid
flowchart LR
    A[写代码] --> B[编码规范]
    B --> C[跑测试]
    C --> D[测试策略]
    D --> E[提交]
    E --> F[Git工作流与CI]
```

## 三条最常踩的规范

1. **本地绿 ≠ CI 绿**：CI 用 `git ls-files '*.go'` 全量 gofmt，本地 `make fmt-check` 只查三个目录；CI 强制 `-count=1`，本地可能命中缓存。
2. **`-race` 不在 `make check` 里**：并发相关改动（锁、`Close` 顺序、租约）必须单独跑 `make race`。
3. **新增脚本必须登记** `scripts/governance/inventory.yaml`，否则 `TestScriptGovernance` 直接失败。

## 相关分类

→ 详见 [架构总览](../01-架构总览/README.md)
→ 详见 [功能结构与策略](../02-功能结构和策略/README.md)
→ 详见 [数据与组件设施](../04-数据与组件设施/README.md)
→ 详见 [总索引](../README.md)
