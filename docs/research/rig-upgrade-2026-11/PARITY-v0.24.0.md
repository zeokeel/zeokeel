# Rig v0.19.0 → v0.24.0 能力对照（PARITY）

> 回答两个问题：**升级后哪些自研功能可以不再自己做？** 以及反向的：**rig 删掉/改了语义之后，我们必须自己接管什么？**
> 分析时间：2026-11-20（v0.24.0 tag 当日）。作者：parity-analyst（只读代码，未改任何 `.go`/`go.mod`/`go.sum`）。

---

## 0. 结论摘要（先看这一页）

| 结论 | 计数 |
| --- | --- |
| B 清单 18 个候选里：**可直接删 / 已在用 rig，无需再自研** | **2** 项（#3 上下文压缩、#16 编排工具大部分） |
| **需改造后可采用（其中 5 项有净删除）** | **8** 项（#1 #4 #6 #7 #8 #9 #14 #17；#4 #6 #8 净删除≈0，属"补能力"） |
| **不能替换 / 本仓无对应实现** | **8** 项（#2 #5 #10 #11 #12 #13 #15 #18；其中 #10/#11/#12 是「本仓完全没有」，属**新增能力**而非删除） |
| C 清单（rig 删除/改语义）中**真实影响本仓** | **4** 条硬影响（C1 已接管 / C2 / C7 / C8）+ 1 条注释级（C6）+ 1 条待验证（C10）+ 其余 5 条无引用 |

**Top5 可删行数（估算，非测试代码；括号内为对应测试量级）**

| 排序 | 项 | 净可删 | 风险 |
| --- | --- | --- | --- |
| 1 | #1 会话持久化 → `session/sqlite` | **460–960 行**（测试 800–1500 行） | 高 |
| 2 | #7 AGENTS.md 指令层级 → `instructions` | **≈120 行**（测试 366 行需重写） | 中 |
| 3 | #9 settings 值脱敏 → `secrets.RedactForExport` | **≈40 行** | 低 |
| 4 | #17 MCP 资源工具前缀（rig 改名后的必然改动） | **≈25 行** | 中（破坏性） |
| 5 | #14 gate 只读/工作区分支 → `gate.AllowThenAsk` | **≈40 行** | 中 |

**风险最高的两条（必须先处理）**

1. **`toolkit.deniedGlobs`（「设置 → 数据与隐私 → 文件隐藏」）在 v0.24.0 静默失效。** v0.19.0 里 `Policy.DeniedPath` 真的让文件工具隐藏命中项（`sandbox/path.go:148-175` + `tools/builtin/builtin.go:419`）；v0.24.0 起 `DeniedPath` 退化为「只判根归属」（`sandbox/path.go:154-160`），`WithDeniedGlobs` 标 Deprecated（`sandbox/sandbox.go:536-543`）。**而本仓没有任何测试断言"实际被隐藏"**：`internal/modules/toolkit/denied_globs_test.go` 只测设置行合并，`internal/regression/denied_globs_test.go` 只测 UI 往返 → 升级后**不会红灯**，只是功能变哑。纯产品侧接管，必须自己写。
2. **`session/sqlite` 是嵌套子模块（无 tag）且会把 `modernc.org/sqlite` 抬到 v1.62.0。** 主模块 zip 里根本没有它（`unzip -l v0.24.0.zip | grep sqlite` 只有 `docs/sqlite.md`）；子模块 `@v/list` 为空、`@latest` 是伪版本 `v0.0.0-20261120012823-c73e7cb83ecf`，其 `go.mod` 要求 `modernc.org/sqlite v1.62.0`，而本仓 `go.mod:11` 钉的是 `v1.61.1`。同时它的检索实现是**裸 JSON 文本 LIKE**（`search.go` 中 `CAST(m.payload AS TEXT) LIKE ?`），会丢掉本仓 `search_text` 解码列的检索质量。

---

## A. 版本核对

### A.1 v0.24.0 是当前最新 tag（三条独立证据）

| 证据 | 内容 |
| --- | --- |
| 主模块 module 路径 | `go.mod:1` `module github.com/zeokeel/zeokeel`；`go.mod:7` 依赖 `github.com/zeokeel/zeokeel-rig v0.24.0`（工作区已由 Lead 从 v0.19.0 改上，`git diff go.mod` 可见） |
| goproxy.cn `@latest` | `{"Version":"v0.24.0","Time":"2026-11-20T01:28:23Z"}` |
| 本地模块缓存 info | `.../@v/v0.24.0.info` 同上时间；另有 `v0.23.4`、两个 `v0.23.5-0.2026…` 伪版本（未打 tag 的中间提交） |

网络只有 goproxy.cn 可用，`@v/list` 只回 `v0.19.0` / `v0.24.0`（我们请求过的两个），**不足以证明中间版本序列**，所以序列以 rig 的 `CHANGELOG.md` 标题为准（它是变更的权威叙述）。

### A.2 版本序列与每个 minor 的主线

`CHANGELOG.md` 逆序排列，行号即锚点：

| 版本 | CHANGELOG 行 | 主线（一句话） | 对产品的意义 |
| --- | --- | --- | --- |
| v0.19.0 | 552 | 轮数默认不限（`MaxIterations` 0 = unbounded） | 本仓已对齐（`agent.go:52`） |
| v0.20.0 | 439 | **定位变更为「个人 agent 客户端 SDK（单用户）」**；删 50 文件 / 15,488 行（`tools/escalate`、`mcpserver`、`internal/filewatch`、`sandbox/escalate`、`tools/report`、`settings_get/set`、`present_artifact`） | 本仓已自研 settings 工具兜底 |
| v0.20.1 | 426 | 修 `secrets.New` 快照 nil（v0.20.0 tag 未出 release） | 无 |
| v0.21.0 | 368 | **懒加载工具（`tool_search`）**：零配置 tools 39.1KB→16.0KB（≈−59%），`InitialActiveTools` + `rig.ToolActivator` | **本仓未采用**，是最大一块「便宜收益」 |
| v0.22.0 | 336 | 沙箱「死路」修复：逐命令提权 `require_escalated`、`sandbox.Denial.Kind` | 本仓未接提权通道 |
| v0.23.0 | 275 | 沙箱能干活：`WritableRoot{ReadOnlySubpaths}` 唯一可写面模型、命令级免审批、工具链缓存可写 | 影响 read-only 语义（见 C） |
| v0.23.1 | 233 | 真机 CI 抓漏（Windows 保护路径、`sandbox.Preflight` + `kit.Agent.Preflight()`、`Config.Sandbox` 三态） | 可用 `sandbox.Preflight` 优化设置页文案 |
| v0.23.2 | 200 | 保护路径「构造时不存在」fail-closed；`unprotectedPaths` 诊断 | 与 C.3 相关（随后 v0.24.0 整体弃用） |
| v0.23.3 / v0.23.4 | 190 / 178 | macOS 真机 job 迁出阻塞链路（CI 运维） | 无 |
| v0.24.0 | 7 | **权限预设四档 + `ToolReviewer` 预审 / `memory` 公共契约 + `rig.SpillStore` / `session/sqlite` 子模块 / MCP 资源工具改名（破坏性）/ 审批默认不超时 / `Approval:auto`=全放行 / 保护路径与拒绝 glob 弃用 / `MaxIterations` 默认 0** | 见 B/C 全篇 |

### A.3 `docs/` 结构的坑（rig 自己说明了）

- v0.24.0 **删除了 `docs/MODULES.md`**（CHANGELOG 行 7 的「文档体系重构」条目），现行单一事实来源是 `docs/README.md` 索引 + `docs/{ARCHITECTURE,TOOLS,CONFIG,AI-SDK,MEMORY,SANDBOX-OS,sqlite,COVERAGE}.md`。任务书里提的 `docs/MODULES.md` 在 v0.24.0 **不存在**（`ls docs` 已核实）。
- `docs/sqlite.md` 第 1 节明确写 `session/sqlite` **「尚未发布」**，并给三档对照表（Memory / NDJSON / SQLite）。这解释了为什么它被打包成独立子模块。

---

## B. 「可以不再自己维护」清单（18 项逐条核实）

图例：✅ 可直接删（rig 等价物成熟）｜⚠️ 需改造｜❌ 不能替换（语义不同 / rig 明确不做 / 本仓没有）

### B.0 汇总表

| # | 候选 | 本仓体量（非测试） | rig v0.24.0 对应面 | 结论 | 净删除 |
| --- | --- | --- | --- | --- | --- |
| 1 | 会话持久化 | `internal/modules/sessionstore` 1471 行（store 937 / search 190 / search_text 269 / module 75），测试 1723 | `session/sqlite`（7 表 + 检索 + Import/Export + 租约）、`session/store`（NDJSON） | ⚠️ | 460–960 |
| 2 | 事件日志/回放 | 无独立包；事件写 `rig.SessionEvent` 进 store；导出 `agent/session_log_export.go` 158 | `events`（Envelope + Emitter/Decoder/Replay） | ❌ | 0 |
| 3 | 上下文压缩 | `agent/context_budget.go` 101 / `precompact.go` 152 / `compact_now.go` 189 | `session/compact`（`LLMCompactor`/`WindowCompactor`） | ✅ 已在用 | 0 |
| 4 | 成本/用量 | `internal/modules/insights` 1600 + `agent/usage.go` 70 + `observability.go` 642 | `costmeter` + `modelprice` | ⚠️ 部分 | ~0 |
| 5 | 记忆 | `modules/memory` 798 + `memorytools` 277 | `memory`（Wiki 契约）+ `internal/memwiki` + `tools/memory` + sqlite `rig_memory_pages` | ❌ | 0 |
| 6 | 工具结果溢出 | `agent/spill.go` 63 | 已在用 `rig.SpillPolicy`；v0.24 增 `rig.SpillStore` + `rig_spill` + `GC` + `spill_read` | ⚠️ | 0–63 |
| 7 | 指令层级/AGENTS.md | `agent/agent.go:832-1006` ≈175 + 测试 366 | `instructions`（四级 + `@import` + 预算 + `Set.Render`） | ⚠️ | ≈120 |
| 8 | 环境上下文 | `agent/system_prompt.go:85-135`（≈50） | `instructions.DetectEnvironment` + `Environment.Render` | ⚠️ | ≈0–30 |
| 9 | 输出脱敏 | `ui/settings/redact.go` 156 + `toolkit/settings_tools.go` 204 | `secrets`（15 类模式 + 流式 Writer/Reader + `RedactForExport`） | ⚠️ | ≈40 |
| 10 | 检索（BM25/模糊） | 无（`workspace.Search` 只按文件名，给文件选择器） | `tools/search`（BM25 + 模糊 + `@path`） | ❌ 本仓无 | 0（新增） |
| 11 | 补丁应用 | **无**（只用 rig builtin `write_file`/`edit_file`/`str_replace_editor`） | `tools/patch`（apply_patch，原子多文件） | ❌ 本仓无 | 0（新增） |
| 12 | 回滚/撤销 | **无**（无 `gitsnap`，grep 为空；UI 的 Undo 是产品状态回滚，与文件无关） | `tools/undo` + `rig` 快照 Tracker | ❌ 本仓无 | 0（新增） |
| 13 | 沙箱策略/执行 | `modules/sandbox` 258（只 probe + provide） | `sandbox` / `sandbox/osrunner` / `sandbox.Preflight` | ⚠️ 复核 | ~0 |
| 14 | 审批/gate | `modules/approvals` 568；gate 决策矩阵 `agent/plan_mode.go:245-370` ≈130 | `gate`（`PersonalDefault/PersonalAsk/AllowThenAsk/ToolReviewer`）+ `kit.PermissionPreset` | ⚠️ 部分 | ≈40 |
| 15 | 工具集装配 | `modules/toolkit` 67 文件 17836 行 | `kit` + `tools/full` + `presets` + `catalog` + `discovery` | ❌ 装配层 / ✅ `discovery` | 0 |
| 16 | 子代理/团队/工作流/ralph/present/ask/goal/schedule | 自研 `subagent` 299 + `team_state` 698 + `team_activity` 297 + `childagents` 447 + `child_sessions` 868 | `tools/{subagent,team,workflow,ralph,schedule}`、`tools/hosttools`（ask/plan_review/exit_plan_mode） | ✅ 大部分已在用 | 0 |
| 17 | MCP / LSP | `toolkit/mcp_*` 共 ≈1900；`toolkit/lsp.go` 339 | `tools/mcp`（资源工具改名）/ `tools/lsp` | ⚠️ 破坏性 | ≈25 |
| 18 | 模型供应商 | `modelcatalog` 1059 + `contracts/modelproviders.go` | `provider/openai`（已用）、`provider/{local,ollama,lmstudio}` | ❌ | 0 |

---

### B.1 会话持久化 —— ⚠️ 需改造，收益最大

**本仓现状**
- `internal/modules/sessionstore/store.go`（937 行）自建 SQLite，**四张表**：`sessions / messages / events / checkpoints`（包注释 `store.go:1-45` 即 schema 文档）。经 `contracts.KeyStore` 提供 `contracts.Store`（`internal/contracts/store.go:37`），接口同时暴露 `rig.SessionLog`、`History()`、`CheckpointStore()`、`ReplaceHistory`、`CreateSession/DeleteSession/RenameSession/Sessions/GetSessionMeta/AppendEvent`（`contracts/store.go:45-78`）。
- 另有全库检索：`search.go`（190）`SearchMessages` + `search_text.go`（269）解码列（**故意**不是裸 JSON：`search_text.go:1-10` 解释"引号与反斜杠是用户内容，不是转义"）。

**rig v0.24.0 对应能力**（`session/sqlite`，v0.24.0 提交 `c73e7cb`；非测试 8231 行）
- 表：`rig_sessions / rig_messages / rig_events / rig_costs / rig_memory_pages / rig_spill` + 扩展账本（`schema.go`）。
- 契约：`Store.History()` / `Checkpoints()` / `SessionLog()`（`views.go:28-34`）、`SearchMessages/SearchEvents`（`search.go:62,178`）、`Memory()`（`memory.go:19`）、`Spill()`（`spill.go:20`）、`SaveCost/CostTotals/CostByModel`（`cost.go:63,88,105`）、`ImportDir`（`migrate.go:48` 幂等 NDJSON 迁移）、`ExportToDir/CompareSessions`（`export.go:101,276`）、`Lease`（`lease.go`）、`CanOpen/OpenReadOnly`（`canopen.go`）。
- 文档：`docs/sqlite.md`（584 行）为权威说明。

**替换可行性：⚠️ 可改造，但不能"直接删"**

可被 rig 覆盖的：消息历史、检查点、事件日志、跨会话消息检索、花费落库、记忆/溢出落库、多进程租约。
**必须保留/自己补的四处硬缺口**：

1. **没有 `RenameSession`**。子模块只有 `Create/Append/AppendBatch/Load/List/Delete/Replace`（`sessions.go:61-323`；全包 grep `Rename|SetTitle` 为空）。本仓 UI 的会话重命名（`contracts/store.go:68`）要改成 `Extension` + 裸 SQL，或用 `s.DB()`（`open.go:337`）自己写。
2. **检索质量回退**。rig 用 `CAST(m.payload AS TEXT) LIKE ?`（`search.go` SearchMessages 的 SQL），即**匹配原始 JSON**；本仓专门维护 `search_text` 解码列来避免"引号/反斜杠/tool_call 参数 JSON 转义"造成的漏命中（`search_text.go:1-10`）。直接换会退化中文与含引号内容的检索。可选折中：用 `Options.Extensions`（`open.go:78`）给 `rig_messages` 加解码列 + 自写检索 SQL，此时只删 `store.go` 的其余部分。
3. **依赖与发版形态**。`session/sqlite` 是**嵌套 module**：不在主模块 zip 里（`unzip -l … v0.24.0.zip | grep sqlite` 只有 `docs/sqlite.md`）；子模块没有 tag（`@v/list` 空），只能钉伪版本 `v0.0.0-20261120012823-c73e7cb83ecf`；其 `go.mod` 要求 `modernc.org/sqlite v1.62.0`（本仓现为 `v1.61.1`，`go.mod:11`），会连带抬 `modernc.org/libc`。其余间接依赖（go-humanize/uuid/go-strftime/bigfft）本仓 `go.mod` 已齐，**不新增其它新依赖**。
4. **现有数据库迁移**。rig 的 `ImportDir` 只吃 NDJSON 目录（`migrate.go:48`），不吃本仓现有 `zeokeel.db` 的 `sessions/messages/events/checkpoints` 表。要写一次性迁移（读旧表 → `Create` + `AppendBatch` + `SessionLog.AppendEvent`），好在 `ExportToDir` 也给了反方向的自检工具。

**删除估算**：`store.go` 937 → 适配层约 300（净删 ≈640）；若接受 rig 检索再去 `search.go`+`search_text.go` 459（合计 ≈1100，测试 1723 行大部分要重写/删除）。保守方案（保检索质量）= 净删 ≈640 行 + 重写 800 行测试。

**迁移风险**：中高。WAL/DSN/busy 语义一致（都走 `modernc.org/sqlite`），但**表名/列名全变**（`sessions`→`rig_sessions` 等），任何直接读库的诊断/外部分析脚本会断。`busyTimeout` 有默认值（`DefaultBusyTimeout = 5s`，`open.go:73`）；本仓的 `cfg.BusyTimeoutMs` 要映射过去。

### B.2 事件日志/回放 —— ❌ 不建议替换

- 本仓没有独立的"事件包"：事件以 `rig.SessionEvent{SessionID,Type,Time,Payload}` 写进 store（`agent.go:2745-2760` 附近的 `appendEvent`），读出给 trajectory/insights/导出。事件种类是**产品定义**的 20+ 种（`insights/aggregate.go:39-43` 列出 `run_start/context_snapshot/usage/model_call/tool_call/…`）。
- rig `events` 是**线格式**而非存储：`Envelope{Version,Seq,Time,Kind,Payload}`（`events/events.go:70-88`，14 个 `Kind*` 常量）+ `Emitter`/`Decoder`/`Replay`（`emitter.go:50`、`decoder.go:34,117`）。它的价值在"可搬运的 NDJSON"，不在"事件该长什么样"。
- 结论：**没有可直接删的代码**。唯一低成本对齐点：给现有事件载荷加 `version` 字段，并把 `session_log_export.go`（158 行）的 JSONL 记录结构对齐 `events.Envelope`，以便未来用 `events.Replay` 读回。若采纳 B.1 的 `session/sqlite`，事件会自动落到 `rig_events(session_id,seq,kind,at,payload)`（`views.go` `eventView.AppendEvent`），那时"导出→回放"才第一次有官方读取器。

### B.3 上下文压缩 —— ✅ 已经在用，无自研可删

本仓**没有**自己的压缩算法：`context_budget.go:96`、`precompact.go:146`、`compact_now.go:79`、`context_recovery.go:72` 全部安装 `compact.LLMCompactor{… Fallback: compact.WindowCompactor{}}`。产品自研的只有"预算怎么算"（`context_budget.go:36-60` 的 `deriveContextBudget`：`min(0.8W, W-MaxTokens-64Ki…)`）和"压缩前后要发什么事件/横幅/归档"（`compact_archive.go` 162、`context_archive.go` 137），这些是产品语义，rig 不做。
**唯一要注意的 rig 侧修复**：v0.24.0 修了「压缩摘要链不再丢弃上一次摘要」（CHANGELOG 行 154-157），这对本仓是纯收益（更准的 `PriorMessages` 夹紧，见同段）。**无需改本仓代码。**

### B.4 成本/用量 —— ⚠️ 只能补能力，不能替换

- 本仓：`agent/usage.go`（70）把 `RunResult.Usage` 写到 `contracts.MessageUsage`（`contracts/chat.go:372-386`，含 `ContextTokens` 专门区分"最后一次调用的 prompt tokens"）；`insights`（1600 行）按 `model_call`/`usage` 事件折叠出 day/model/workspace/session 四维桶，并用**模型目录价目**算钱（`contracts/modelcatalog.go:67` `ModelInfo.Cost`）。这是一整套产品统计口径 + UI。
- rig：`costmeter.Meter`（`costmeter/meter.go:168`，`WithBudget/WithWarnAt/WithWarnFunc/WithPricer`）+ `modelprice.Lookup`（`modelprice/price.go:184`，34 条内置价目、本地 provider 前缀白名单）。定位是**一次 Run 的花费上限与告警**，不是会话/日/模型聚合。
- 结论：**不能替换**（语料与口径都不同：本仓价目来自可编辑的 model catalog，rig 是内置表）。可选增量：接 `costmeter` 做 `MaxRunCostUSD` 预算护栏（v0.24.0 明确把"成本控制"从轮数护栏转交给它，CHANGELOG 行 26-30）；若采纳 B.1，`session/sqlite` 的 `rig_costs` + `CostEntryFromSummary`（`cost.go:28`）可顺手得到花费持久化。
- 注意：本仓**没有**任何 USD 预算/告警代码（grep `costmeter|MaxRunCostUSD` 为空），这是"缺能力"而不是"重复实现"。

### B.5 记忆 —— ❌ 语义不同，不能替换

| 维度 | 本仓 `internal/modules/memory`（798）+ `internal/memorytools`（277） | rig v0.24.0 |
| --- | --- | --- |
| 数据模型 | 条目列表：`id/text/scope`（global 或 workspace），JSON 文件 + 原子落盘 + 损坏隔离（`memory.go:309-384`） | wiki 页：`name(slug)/title/summary/content`，一页一文件或一表 |
| 工具面 | 产品自有 `memory` 工具包装（`action: list/add/update/remove`，`memorytools/memory.go:44`），**保留 rig 的 `rig.MemoryStore` 旧接口** | `tools/memory`（525 行，`action: index/read/write/append/search/delete`，渐进式，`index` 绝不返回正文） |
| 存储后端 | 自己写 | `tools/memory.NewFileWiki/NewWorkspaceWiki/NewMemoryWiki`（`tools/memory/memory.go:42,45,52`）+ `session/sqlite` 的 `rig_memory_pages`（`memory.go:19`） |
| 治理 | 有 consolidation policy（条数/长度/去重/过期，`memory.go:605-710`） | 无（只有单页/摘要/正文上限规则） |
| 作用域 | 全局 + 工作区两种 scope，UI 内存面板可编辑 | 按工作区目录隔离（`<stateDir>/memory/<workspaceID>/`），**不写进仓库** |

结论：**不能替换**。换成 wiki 模型 = 重写存储 + 工具 + UI 面板 + 迁移既有条目（条目→页需要模型或启发式改写）。可白拿的部分：`memory` 包导出的规则函数（`memory/memory.go:56-78`：`ValidateName/NormalizeSummary/AppendLog/RewriteLog/ClampText`）与 `session/sqlite` 的记忆后端——如果哪天产品决定走 wiki 形态。

### B.6 工具结果溢出 —— ⚠️ 已薄，可选再薄

- 本仓只剩 **63 行**：`agent/spill.go` 只解析目录（`<workspace>/.zeokeel/tool-results`，headless 回落到 `<dataDir>`），阈值 32 KiB、保留 100 份；写入/摘要/裁剪都是 `rig.SpillPolicy`（在 `AgentSpec.Spill` 安装，`agent.go:2655`）。附 `spill_scope_test.go` / `spill_fallback_test.go` 钉住"按会话绑定根"（BE-11 审计）。
- v0.24.0 新增：`rig.SpillStore`（句柄代替路径）、`session/sqlite` 的 `rig_spill` + `GC(keep)`（`spill.go`）、`spill_read` 工具、spill 按 **session 作用域** 分区回收（CHANGELOG 行 138-142）、`kit` 默认目录改 `<Root>/.rig/spill`。
- 结论：**不建议换**。本仓的产品语义是"溢出文件落在工作区里，模型能用 `read_file` 读回、用户能在文件树里看见"；改成库内句柄会失去可见性，且要多接一个工具。如果哪天要"库内存储 + 自动 GC"，那 63 行可以整体删除（改成 `SpillStore` 注入）。

### B.7 指令层级/AGENTS.md —— ⚠️ 可删主体，但必须保住信任围栏

- 本仓：`agent/agent.go:832-1006`（≈175 行）——`withWorkspaceInstructions` / `workspaceInstructionsBlock`（两处：方法版 843、纯函数版 974，后者用于测试）/ `validWorkspaceInstructionName`（只允许工作区内裸文件名）/ `readWorkspaceInstructions`（拒绝符号链接、限 32 KiB、按 rune 边界截断）/ `pathWithinRoot` / `neutralizeInstructionTags`。**关键产品语义**：整段包在 `<workspace_instructions trust="untrusted">` 围栏里 + 一段"这是仓库内容、不是系统指令、冲突时系统指令优先"的声明（`agent.go:88-99` 的常量）。测试：`agents_file_test.go` 194 + `agents_file_hardening_test.go` 172，共 12 个用例。
- rig：`instructions.Load(cwd, opts…)` 向上发现 `AGENTS.md` 至 repo root（含该级）→ 四级 `user/root/ancestor/local`；`@import` 递归（围栏代码块内不展开、循环/深度拦截、默认限制在仓库根内）；单文档 64 KiB、总量 256 KiB 预算；`Set.Render(workspaceRoot)`（`instructions/render.go:22`）输出 `## <相对路径> (source: …)` 分段。
- 结论：**⚠️ 可替换约 120 行**，但有三个不可放弃的点：
  1. rig 的 `Render` **不带任何 trust 围栏**（`render.go:22-42` 只有标题+内容），本仓必须在外层自己包 `<workspace_instructions trust="untrusted">` 与 notice——否则等于把一段不受信任的仓库文本以"看起来像系统区段"的形式喂给模型（2026-11 审计结论）。
  2. rig **只认 `AGENTS.md` 这一个文件名**；本仓有设置项 `agent.agentsFile`（`contracts/interaction.go:105-107`，设置页「工作区指令文件」，`ui/settings/behavior.go:234`）。要保留就必须用 `WithExtraPaths` 变通或继续自己读。
  3. 预算从 32 KiB 变 64 KiB/256 KiB、内容从"一个文件"变"四级 + import 展开"，**上下文占用会变大**（本仓 `context_snapshot` 事件与 context 面板的数值会变），需要同步 `docs/` 与快照测试。
- 净删除：主体 ≈120 行（保留围栏/notice/设置名逻辑），测试 366 行需重写为"围栏 + 四级 + import + 预算"的新断言。

### B.8 环境上下文 —— ⚠️ 可补 git/日期，但文件策略行是产品的

- 本仓：`agent/system_prompt.go:85-135`——`Environment: <goos>/<goarch>. <shell 说明>` + `workingDirectoryLine(root, sandboxMode)`，后者按权限预设输出三种不同文案（read-only / workspace-write / full-access），**并显式披露"full 时 OS 沙箱被绕过、相对路径按进程 cwd 解析"**（注释标注 2026-11 审计 RG-07）。
- rig：`instructions.DetectEnvironment(workspaceRoot, cwd, now)`（`render.go:110`）给 workspace/cwd/platform/date/git（只读 `.git/HEAD`，不 exec git）+ `GitDirtyState` 三态（"绝不能默认声称工作区干净"），`Render` 对每个值单行化 + 限长 512 rune 防注入。
- 结论：**⚠️ 增量而非替换**。`Environment.Render()` 不包含"文件策略"这一行，那正是产品最需要在 prompt 里说清的东西（权限预设与 OS 墙的关系）。建议：保留 `workingDirectoryLine`，把 git 分支/日期用 `DetectEnvironment` 补进去（净删除 ≈0，收益是模型不再需要 `git branch` 试探）。

### B.9 输出脱敏 —— ⚠️ 能用一半

本仓有**两个不同用途**的脱敏，别混：

| 本仓 | 用途 | rig 对应 | 判断 |
| --- | --- | --- | --- |
| `internal/ui/settings/redact.go`（156）`hasCredentialMarker` / `maskedSettingValue` | 诊断页与导出报告里**按设置键名**整行隐藏（`apikey/token/secret/…`）+ JSON 结构遍历 | `secrets.Redactor`（`secrets/redactor.go:46,54`）+ `RedactForExport`（`secrets/export.go:27`） | 部分等价：rig 强在"已知明文 + 15 类凭据模式 + 流式跨 chunk"，弱在"按设置键名整行隐藏"的产品策略 |
| `internal/modules/toolkit/settings_tools.go`（204）`redactSettingText` | **模型读设置**时脱敏（防 `settings_get` 把 provider key 吐给模型） | `secrets.RedactForExport` | ✅ 可换（≈20-40 行） |

- 重要前提（v0.24.0 的定位变化）：`secrets` 包现在**明确不是运行时能力**——包注释写「本包**不是**运行时默认能力…kit 的默认装配不再调用本包（工具输出明文直通）」（`secrets/export.go:1-11`），`kit` 的 `Config.Redactor` 默认 nil，`kit/doc.go:110-112` 写明「输出脱敏默认关闭」。**所以不能指望 rig 替我们脱敏工具输出**；能白拿的只有模式规则表（15 类内置凭据形态 + `RedactJSON` 键级脱敏，`secrets/redactor.go:179,198,227`）与「导出时洗一遍」的便捷入口。
- 本仓现状：没有任何「工具输出统一脱敏」的环节（grep `Redactor` 为空）；`toolkit/rebind.go` 的 `boundOutput` 只截断不脱敏。若产品要，需要自己在 `proxies`/`boundOutput` 处接 `secrets.Redactor`——这是**产品自建**（≈30 行接线），不是 rig 自带。
- 结论：⚠️ 可删 ≈20-40 行（`settings_tools.go` 里那段自写正则/JSON 遍历 → `secrets.RedactForExport`/`RedactJSON`），但「工具输出脱敏」这条产品需求 rig **不背**。

### B.10 检索 —— ❌ 本仓没有对应实现（是新增能力）

- 本仓：`internal/modules/workspace/workspace.go:834` 的 `Search` 是**文件名字面子串**搜索，服务对象是 UI 文件选择器（`contracts/workspace.go:54-56`），不是模型工具；`web_search` 走外部端点（`internal/websearch`，540 行）。**没有**面向模型的本地内容检索工具。
- rig：`tools/search`——文件名模糊打分 + 内容 BM25（k1=1.2/b=0.75）+ `@path` 提及解析，输出契约稳定。（v0.24.0 还修了 CJK 分词：unigram + 相邻 bigram，CHANGELOG 行 145-146，正好对本仓中文用户重要。）
- 结论：**不删代码，属于"白捡一个新能力"**。注意与产品现有策略的一致性：`DeniedGlobs` 失效后（见 C.2），这个新工具也会把 `.env` 读出来——两者要一起解决。

### B.11 补丁应用 —— ❌ 本仓没有

grep `apply_patch` 只命中两处**注释/展示映射**（`ui/session/worksteps.go:99` 把工具名映射成"编辑文件"步骤；`uikit/approval.go:829` 说明它没有 diff 预览），**没有任何注册点**，也没有 `tools/patch` 导入。模型现在的多文件修改只能靠多次 `write_file`/`edit_file`。
rig `tools/patch`：一次原子应用多文件补丁（统一 diff 与 `*** Begin Patch` 两种格式、三级上下文容错、歧义即拒绝、路径三条硬规则、CRLF/权限保真、dry-run）。
结论：**新增能力**；接入只需在 `toolkit/rebind.go` 的工具列表里加一项，但要不要暴露给模型是产品决策（会影响 `uikit` 的 diff 预览与审批卡文案）。

### B.12 回滚/撤销 —— ❌ 本仓没有

全仓 grep `gitsnap|tools/undo` 在业务代码里为空（`apply_patch` 只有两处注释/展示映射，见 B.11）；`Undo` 这个词只出现在 UI 的"撤销某次产品操作"（归档会话、删记忆、删自动化任务：`ui/sidebar/archive_notice.go:20`、`ui/session/memory_tools.go:182`、`ui/automation/view.go:1276`），与"回滚文件改动"无关。
rig `tools/undo`：回合级快照 + 全有或全无回退（前像 + 逆操作栈）、只回退被改动文件、保留权限、二进制/超预算只报告、`Tracker` 保留最近 N 回合。
结论：**新增能力**，且是"agent 改坏代码"场景的刚需；接入点应是 `AgentSpec` 的 run 前快照钩子（rig 侧 v0.20.0 由 `kit.Config.UndoTracker` 提供，本仓不用 kit，需要自己在 `run` 前后调用 `undo.Tracker`）。

### B.13 沙箱策略/执行 —— ⚠️ 现在已全在 rig，但语义变化要复核

- 本仓 `internal/modules/sandbox`（258 行 / 2 文件）只做一件事：**探测一次**平台 runner（`osrunner.New`）并把 fail-closed runner + `Status()` 作为 `contracts.SandboxService` 提供（包注释 `sandbox.go:1-12`）。策略构造在 `toolkit/toolkit.go:531-548`（`newPolicy`），模式映射在 `toolkit.go:432-470`（`permissionSandboxMode`：read-only / workspace-write / danger-full-access）。
- rig：`sandbox`（`Policy/Runner/Denial/Preflight`）、`sandbox/osrunner`（四后端）、v0.23.1 新增 `sandbox.Preflight`（一句话回答"现在是否隔离/能否联网/被挡时点哪里"，且禁止内部术语——正好是本仓设置页最缺的东西，`ui/settings/sandbox.go:78-100` 目前是自己拼状态文案）。
- 结论：**没有可删的策略代码**（本仓没自研隔离）。可选：用 `Preflight` 替换/补强设置页与 `sandbox_status` 文案（净删除 ≈0）。**真正的动作在 C 清单**：read-only 语义与保护路径/拒绝 glob 的承诺都变了。

### B.14 审批/gate —— ⚠️ 部分可换

- 本仓：`internal/modules/approvals`（568）是**产品审批队列 + UI 协作**（`Mode` ∈ read-only/workspace/ask/auto/never，`approvals.go:60-62`；`auto` 立即 allow `approvals.go:270`；超时来自设置行 `SettingAgentApprovalTimeoutSeconds`，`approvals.go:329`；含"记住本会话允许"与通知）。gate 决策矩阵在 `agent/plan_mode.go:245-370`（≈130 行），顺序：plan 模式 → minimal/creator 白名单 → 会话级 override（full/read-only/workspace）→ 沙箱免问 → 交互工具 → 已记住 → 无 approvals 时 fail-closed → 请求审批。
- rig：`gate.AllowThenAsk(allow, inner)`（`gate/sandbox.go:76`）+ `gate.SandboxReadOnlyTools`（`gate/sandbox.go:41-68`，判据是"是否触及用户资源"而非"是否只读"）+ `gate.PersonalDefault/PersonalAsk`（`gate/defaults.go:55,73`，= `AllowThenAsk(SandboxReadOnlyTools, inner)`）+ `gate.ToolReviewer`（`gate/review.go:43`）+ `kit.PermissionPreset` 四档（`kit/presets_permission.go:39-45`）。
- 可换的部分：**只读/工作区两个白名单分支**——本仓 `contracts.ReadOnlyAutoAllowedTools`（`contracts/subagents.go:93-125`）与 `WorkspaceAutoAllowedTools`（:150）和 rig 的 `SandboxReadOnlyTools` 高度重合（本仓多了 `working_directory`、`lsp_*`、`session_*`、team 只读工具；rig 多了 `search/inspect/sandbox_status/tool_search/spill_read/job_wait/terminal_read`）。可用 `gate.AllowThenAsk(append(gate.SandboxReadOnlyTools, 产品额外项…), inner)` 替掉这两段（≈40 行）。
- **不能换**：会话级 override、plan/minimal/creator 三档运行时模式、`approvals` 队列与 UI、"never" 的 headless 否决。
- **`kit.PermissionPreset` 不适用**：本仓在 `internal/`、`cmd/` 里**零引用** `zeokeel-rig/kit`（`rg -n 'zeokeel-rig/kit' internal cmd` 无输出；只有 `docs/research/rig-parity/` 的两个探针程序用过）；本仓的权限预设是 `contracts.NormalizePermissionMode`（read-only/workspace/full + legacy ask/never 归一）。
- 净删除 ≈40 行（同时能把"白名单两处不同步"这类审计隐患消掉）。

### B.15 工具集装配 —— ❌ 装配层不能换，`discovery` 可单独捡

- 本仓 `internal/modules/toolkit`（67 文件、17836 行）是**产品装配层**：按 root 重建 root-scoped 工具（`rebind.go` 612 行）、MCP 热重载/禁用（`mcp_control.go` 350、`mcp_runtime.go` 740）、LSP 单服务器生命周期（`lsp.go` 339）、任务板（`task_board*` 1742）、执行历史、jobs 路由、禁用工具列表、按会话裁剪。`rig` 的 `kit` + `tools/full` + `presets` + `catalog` 是"一次性装配一个 agent"的形态，无法表达"工作区切换即重建 + MCP 动态增删 + 产品工具混入"。
- **可单独采用**：`tools/discovery`（`tool_search`，叶子包，`discovery.New(Options{List, Activate})`，`discovery.go:84`）+ `agent.AgentSpec.InitialActiveTools`（`agent/agent.go:205`）+ `rig.ToolActivator`（`agent.go:83`）。收益有实测：v0.21.0 零配置 tools 从 **39.1 KB → 16.0 KB（≈3,995 tokens，−59%）**，且"不激活时 tools 逐字节稳定、激活只在末尾追加、激活不重建 system prompt"。本仓每次请求发**全部**注册工具（`agent.go:2637` `Tools: runTools`），是当前最大的 token 浪费点。
- 代价（rig 自己写明）：激活那一轮 prefix cache 不命中；未激活工具派发会被拒（`rig: tool "x" is registered but not active in this session; activate it with tool_search first`）；本仓的 `toolGuidanceLines`（`system_prompt.go:166`）要改成能容忍"工具有没有激活"。

### B.16 子代理/团队/工作流/ralph/present/ask/goal/schedule —— ✅ 大部分已在用 rig

逐个核实（本仓是否为 rig 包装层）：

| 能力 | rig 源 | 本仓 | 能否直接删 |
| --- | --- | --- | --- |
| `subagent` / `subagent_fork` | `tools/subagent`（`subagent.go:18,30,35,44`） | **自研**（`agent/subagent.go` 299 + `childagents.go` 447 + `child_sessions.go` 868）：按实时状态重建 runner（provider 注入/模型切换/设置/工作区/审批 gate） | ❌ 不能删：rig 的 tool 只是 schema + 调用壳，runner 必须由宿主按会话状态构造 |
| `team` 工具族 | `tools/team.Manager`（`manager.go:151`）+ 任务板（`tasks.go:100,491`） | **已在用 rig manager**，自研的是"真实运行态追踪 + 活动读模型 + 帧刷新"（`team_state.go` 698、`team_activity.go` 297、`team.go` 746 的接线） | ❌ rig 只报"可用性"，UI 需要真实 running/idle/done/failed |
| `workflow` | `tools/workflow`（`workflow.go:16,39`） | **已用**（`orchestration.go` 注册 rig tool，配 `delegatedParentSpec`） | ✅ 无可删 |
| `ralph` | `tools/ralph`（`ralph.go:15,36`） | **已用**（同上） | ✅ 无可删 |
| `ask_user` / `plan_review` / `exit_plan_mode` | `tools/hosttools` | **已用**（`modules/hosttools/tools.go:55` 的 `rigHostToolNames` 只保留这三个名字） | ✅ 无可删 |
| `present` | rig 的 `present` = `rig.FilePresenter`（声明已有文件） | **自研**（产品的 `present` 是内容 artifact：title+body → `contracts.ArtifactPresenter`，`modules/hosttools/tools.go:8-14` 明确"两者不能同名"） | ❌ 语义不同 |
| goal 三件套 | rig 无独立 goal 工具（v0.16.2 只有 context-less 选项） | **自研**（`internal/hosttools/goaltools.go` + `goalrevision.go`，基于 `contracts.GoalService`） | ❌ rig 不提供 |
| `schedule` | `tools/schedule`（`schedule.go:115`） | **已用**（`toolkit.go:192` `schedule.Tools(nil)`） | ✅ 无可删 |

结论：**编排面基本已经"rig 提供工具、产品提供运行态"**，没有可删的自研实现；`present`/`goal` 是产品语义，rig 明确不做。

### B.17 MCP / LSP —— ⚠️ 破坏性改名，必须改调用点

- **MCP**：本仓 `toolkit/mcp_stdio.go:127` 现在这样拼资源工具名：
  `mcp.ResourceTools(client, "mcp__"+sanitizeMCPName(serverName)+"__")`。
  - v0.19.0 语义：名字 = `prefix + base`（`resource_tools.go:34-37`）→ 最终 `mcp__<server>__list_resources`，**产品自己补 server 维度**（注释 `mcp_stdio.go:118-121` 就是这个用意）。
  - v0.24.0 语义：名字 = `prefix + sanitize(server) + "__" + base`（`resource_tools.go:47-57`）→ 若继续传 `"mcp__<server>__"`，会变成 **`mcp__<server>__<server>__list_resources`（双 server 段）**。
  - 好消息：两边清洗规则**逐字相同**（rig `tools/mcp/tools.go:430-449` vs 本仓 `mcp_stdio.go:134-153`），所以只要把调用改成 `mcp.ResourceTools(client, mcp.DefaultPrefix)`，最终名字与今天**完全一致**（`mcp__<server>__list_resources`），存量设置里的 `mcp.disabledTools` 名字不会断。同时可以删掉本仓的 `sanitizeMCPName` 与手拼（≈25 行）。
  - 另可顺手用 v0.24.0 的新面：`mcp.AggregateTools` / `NewToolsChecked` / `NameCollisionError`（`tools.go:121,150`）替掉本仓"注册重名就跳过并告警"的兜底（`toolkit/registry.go:20-35`）——注意这属于**改动**而非删除。
- **LSP**：本仓 `toolkit/lsp.go`（339）是 rig `tools/lsp` 的包装（单服务器 + 设置 + 生命周期 + 启动失败降级）。rig 侧 v0.24.0 修了"服务端启动即崩溃时写失败被当成可重试错误"（CHANGELOG 行 166-169）。**保持现状即可**，无可删。

### B.18 模型供应商 —— ❌ 不能替换

本仓只用 rig `provider/openai`（`agent/agent.go:37,2721`）覆盖所有 OpenAI 兼容端点；产品侧能力是**模型目录**：`modules/modelcatalog`（1059 行：模型 id、上下文窗口、思考参数、**三家价目**）+ `contracts/modelproviders.go`（预设 provider 列表 = base URL + 示例 model）。rig 的 `provider/{local,ollama,lmstudio}`（各 ~数百行）是专用预设（`ollama.New`/`lmstudio.New` + `Endpoint()`），本仓的用法是"用户填 base URL"，不需要专用构造器。
结论：**不能替换，也不需要替换**。若未来要"本地模型自动发现/上下文探测"，那是新增能力，不是删代码。

---

## C. rig 删除/改语义后「我们必须自己接管」清单

逐条用 grep 核实本仓是否受影响。**结论：4 条硬影响（C1 已接管 / C2 / C7 / C8）+ 1 条注释级（C6）+ 1 条待验证（C10）+ 5 条无影响（C3/C4/C5/C9/C11）。**

| # | rig 变更 | 本仓是否引用 | 判断 |
| --- | --- | --- | --- |
| C1 | v0.20.0 删 `tools/settings`（`settings_get/settings_set`） | **是，且已接管** | ✅ 已完成，永久保留 |
| C2 | v0.24.0 拒绝 glob 只记录不拦截 | **是** | 🔴 **必须自己写** |
| C3 | v0.24.0 保护路径 API 弃用（`WithProtectedPaths`/`ProtectedPaths` 恒空） | 否（grep 为空） | ⚪ 无影响 |
| C4 | v0.24.0 审批默认不超时（`gate.Ask` timeout=0） | 否（审批是产品自研队列） | ⚪ 无影响 |
| C5 | v0.24.0 `Approval:"auto"` = 全放行（= `gate.AllowAll`） | 间接（本仓 `ModeAuto` 本就立即 allow） | ⚪ 语义已一致 |
| C6 | v0.24.0 `MaxIterations` 默认 0（不限轮数） | 是（`effectiveMaxIterations`） | 🟡 注释过时，可删哨兵说明 |
| C7 | v0.24.0 read-only = "只有 `/dev/null` 可写"（无 `/tmp`）；`osrunner` 不再做网络/socket/seccomp | 间接（权限预设→沙箱模式） | 🟠 承诺与文案需复核 |
| C8 | v0.24.0 MCP 资源工具改名（破坏性） | **是**（`mcp_stdio.go:127`） | 🔴 **不改就双前缀** |
| C9 | v0.20.0 删 `tools/report`、`mcpserver`、`internal/filewatch`、`sandbox/escalate`、`tools/escalate`、`present_artifact` | 否（grep 无引用；`present_artifact` 仅在注释里出现） | ⚪ 无影响 |
| C10 | v0.24.0 `job_output` 分页（`output_offset`/`next_offset`/`output_truncated`） | 间接（本仓解析 `job_list` 文本） | 🟠 升级后需跑 jobs 相关测试 |
| C11 | v0.24.0 `present` 真正拿到 Presenter | 否（本仓 `present` 自研，不用 kit） | ⚪ 无影响 |

### C1（已完成接管）settings 工具

`internal/modules/toolkit/settings_tool_impl.go:1-12` 写明：v0.19.0 之前来自 rig `tools/settings`，**rig v0.20.0 删除该包**，于是产品自己实现，保持名字/schema 逐字节兼容；`settings_tools.go:1-12` 再加一层凭据脱敏包装（`settings_get` 永不回明文，且审批矩阵里**不**被任何预设自动允许）。
→ **这段代码不是"临时补丁"，是永久产品代码**（372 行 + 测试）。任何"能不能回到 rig"的讨论到此为止。

### C2（🔴 必须接管）拒绝 glob / 文件隐藏

- 本仓现状：`toolkit/toolkit.go:541` `sandbox.WithDeniedGlobs(cfg.DeniedGlobs...)`；设置行 `toolkit.deniedGlobs`（`contracts/settingskeys.go:47-55`）由设置页「数据与隐私 → 文件隐藏（高级）」编辑（`ui/settings/denied_globs.go`），声称"对模型的文件工具隐藏匹配文件（shell 旁路仍在），默认空、重启生效"。
- rig 行为分水岭：
  - v0.19.0：`sandbox/path.go:148-175`（glob 参与 `DeniedPath`）+ `tools/builtin/builtin.go:419`（`r.policy.DeniedPath(abs)` 过滤目录/glob/grep 命中）→ **真的隐藏**。
  - v0.24.0：`sandbox/path.go:154-160` `DeniedPath(abs) = CheckPath(abs) != nil`（只判根归属）；`sandbox/sandbox.go:536-543` 明确 "Deprecated：不再参与任何强制…文件工具也不会据此隐藏结果"；CHANGELOG Removed 段（行 128-130）。
- 影响：**功能静默变哑**，而且**没有测试会红**——`internal/modules/toolkit/denied_globs_test.go` 只断言"设置行怎么合并成切片"（`TestResolveDeniedGlobs`），`internal/regression/denied_globs_test.go` 只断言 UI 保存/读取/清空三个用例。用户以为 `.env` 已对模型隐藏，实际上 `read_file`/`grep`/`glob`/`search`（若新接）都能读。
- 建议接管方式（产品侧 ≈100 行）：
  1. 在 `toolkit/rebind.go` 的 `proxies`/工具包装层（所有文件工具都经过它）自己写匹配过滤（注意：rig 的 glob 匹配器是**私有**的 `sandbox.normalizeGlobs`（`sandbox/sandbox.go:816`），v0.24.0 不导出可复用的匹配函数，所以这层要自己实现）：对 `read_file`/`glob`/`grep`/`str_replace_editor`/`read_image` 的**入参路径**做拒绝，对**结果条目**做剔除；
  2. 或者更彻底：把这些敏感文件移出可读面（rig 的建议是"用 `WritableRoot.ReadOnlySubpaths` 或把敏感文件搬走"），但产品做不到替用户搬文件 → **必须走第 1 种**；
  3. 补一个**行为级**回归测试（现在缺），断言"配置 `**/.env` 后 `read_file(".env")` 与 `grep` 命中列表都不含它"。
- 同时提醒：`WithDeniedGlobs` 已 Deprecated，**继续调用不会编译失败但毫无作用**——容易让下一个读代码的人误以为它还在拦。

### C3（⚪ 无影响）保护路径弃用

本仓没有引用 `WithProtectedPaths`/`ProtectedPaths`/`DisableProtection`/`ProtectPaths`/`ProtectWorkspaceMetadata`（grep 全空），也不用 `kit`。唯一相关的是 `sandbox.go`/`sandbox_status` 的展示字段。**不需要接管，但要确认没有任何 UI 文案承诺"`.git`/`~/.ssh` 受保护"**（grep「保护路径/protected path」在 `internal/**/*.go` 为空，安全）。

### C4/C5（⚪ 已一致）审批超时与 auto

- 本仓审批是产品自研队列（`approvals.go` 568 行），超时来自设置行（`approvals.go:329` `SettingAgentApprovalTimeoutSeconds`），**不受 rig `gate.Ask` 默认 timeout=0 影响**。
- `ModeAuto` 在本仓本来就是"立即 allow"（`approvals.go:262-270`），与 v0.24.0 的 `Approval:"auto"` = `gate.AllowAll` 同义。**无需改动**；反过来说，如果产品以后想接 v0.24.0 的 `ToolReviewer`（`gate/review.go:43`）做"auto 档自动预审、明确拒绝才回退 ask"，那是新能力。

### C6（🟡 注释级）MaxIterations 默认 0

`agent/agent.go:52-58` 的 `effectiveMaxIterations` 已按"0 = 不限轮数"实现（注释还引用了 v0.19.0）。但 `agent.go:2629` 的运行期注释仍写 **"Rig treats 0 as its built-in 20-round default, so a 0 limit is passed as an effectively unreachable cap"** ——v0.24.0 下这句话是错的（`MaxIterations<=0` 就是不限）。建议随升级一并删掉这句与可能的哨兵常量（如果还有），免得后人重建"20 轮兜底"的心智模型。

### C7（🟠 需复核）read-only 语义收紧 + osrunner 与 DSH 对齐

- v0.24.0：`read-only` 的正确语义是"**只有 `/dev/null` 可写**（不再有可写 `/tmp`）"，`workspace-write` 是"workspace + 私有 `/tmp`"，`danger-full-access` 透传（CHANGELOG 行 100-104）；`osrunner` 不再渲染 `--cap-drop ALL`/`--unshare-net`/seccomp/socket 遮蔽（行 105-112），"网络、读范围、进程可见性、设备访问都不在沙箱承诺内"。
- 本仓影响点：
  1. `contracts.PermissionReadOnly` 下 shell 仍需审批，**用户批准后**命令在一个没有可写 `/tmp` 的环境里跑 → 依赖 `/tmp` 的命令（`go build` 的临时文件、`npm`、`pip`）会失败。产品文案 `system_prompt.go:112-135` 现在说"read-only：文件工具可读不可写、shell 被禁用"，与实际"shell 可被批准执行但 `/tmp` 只读"有偏差 → **需要改文案或改策略**（例如 read-only 档直接不暴露 shell 工具）。
  2. 如果设置页/`sandbox_status` 任何地方承诺"沙箱内不能联网/看不到宿主进程"，现在不再成立（grep 未发现断言式承诺，但 `ui/settings/sandbox.go:78-113` 的 `sandboxNoteText` 需人工过一遍）。
  3. 本仓 `agent/sandbox.go:22-27` 的 `sandboxShellAutoAllowed` 判据是"runner 可用 + shell 家族"，**不检查 `danger-full-access`**；在 full 档下前面已经 `DecisionAllow`，所以无实际漏洞，但与 rig 新判据不同形，值得注释说明。

### C8（🔴 必须改）MCP 资源工具改名

见 B.17：**不改 `mcp_stdio.go:127` 就会得到双 server 段**（`mcp__srv__srv__list_resources`）。改动 1 行 + 删本仓 `sanitizeMCPName`（≈20 行），并加一条断言"资源工具名 = `mcp__<server>__list_resources`"的测试（现在没有直接断言名字形状的用例，`mcp_stdio.go` 附近只有间接断言）。注意这是**破坏性**：如果本仓曾经把资源工具名写进任何持久化（`mcp.disabledTools` 设置行、`ui/toolcards` 的映射、快照），要么保持原名字（推荐，改 prefix 即可保持），要么做一次性迁移。

### C9（⚪ 无影响）v0.20.0 删除的六块能力

`tools/escalate` / `sandbox/escalate` / `mcpserver` / `internal/filewatch` / `tools/report` / `present_artifact` 在本仓**均无引用**（grep `escalat` 在 `internal`/`cmd` 为空；`present_artifact` 只在 `modules/hosttools/tools.go` 的注释里被提及"故意不暴露"）。本仓也没有实现自己的提权通道（`require_escalated` 从未接）——这意味着**用户被沙箱挡住时没有合规逃生口**，是产品缺口而不是升级缺口（v0.22.0 起 rig 才提供 `sandbox_permissions:"require_escalated"`）。

### C10（🟠 待验证）jobs 输出分页

v0.24.0 的 `job_output` 增加了 `output_offset`/`next_offset`/`output_truncated`，并在超限时补 `[truncated: output exceeded N bytes; next offset: N]`。本仓 `toolkit/jobs_service.go:187-205` 自己实现 `boundJobOutput`（按**尾部**截断 + 前缀标记）并解析 `job_list` 的稳定文本格式。升级后需要跑 `jobs_service_test.go`/`execution_routing` 相关测试确认文本解析没被打乱（rig 侧同时把截断切片改成**分页续读**，`consumed` 只推进到实际交付字节）。

---

## D. 汇总建议（按收益 × 风险排序）

### 第一批：低风险、当天可做（预计净删 ≈150 行，改 ≈3 处）

| 动作 | 收益 | 受影响测试 |
| --- | --- | --- |
| **D1. 修 MCP 资源工具前缀**（`mcp_stdio.go:127` → `mcp.DefaultPrefix`，删 `sanitizeMCPName`） | 避免双前缀（当前会产出 `mcp__srv__srv__*`）；净删 ≈25 行 | `mcp_stdio` 相关（`mcp_reconnect_test.go`、`toolkit` 的 MCP 用例），建议新增名字形状断言 |
| **D2. 删 `agent.go:2629` 过时注释 + 明确 `effectiveMaxIterations` 的 0 语义** | 防回归（心智模型） | `agent` 包全量（注释级，无测试改动） |
| **D3. `settings_get` 脱敏改用 `secrets.RedactForExport`** | 去掉自写正则/JSON 遍历；顺带获得 15 类凭据模式 | `toolkit/settings_tools` 的脱敏用例；`ui/settings/redact` 保持不动（UI 侧语义不同） |
| **D4. 接 `tools/discovery`（懒加载）** | **最大 token 收益**：rig 实测 −59% tools 体积；本仓现在每次全量发 | `agent` 的 `toolGuidanceLines`/context snapshot（工具数量会变）、`toolkit` 的注册计数用例、`ui/settings` 工具计数展示 |

> D4 虽在第一批收益最高，但它是**行为变更**（模型可能"看不见"某个工具直到 `tool_search`）。建议排在 D1-D3 之后、单独一个 PR，并先只对"人格/工具多的场景"开启。

### 第二批：中等风险，需要 2-3 天（预计净删 ≈200 行，重写测试 ≈700 行）

| 动作 | 收益 | 风险 |
| --- | --- | --- |
| **D5. AGENTS.md → `instructions`**（保留 `<workspace_instructions trust="untrusted">` 围栏与 notice，保留 `agent.agentsFile` 设置名，用 `WithExtraPaths` 变通） | 净删 ≈120 行；白拿 `@import`、四级、总量预算、防注入单行化 | 上下文占用会变大（32 KiB→64 KiB/256 KiB），`context_snapshot` 数值变化 → 要同步 docs/快照；`agents_file_*_test.go` 12 个用例重写 |
| **D6. gate 只读/工作区白名单改用 `gate.AllowThenAsk` + `SandboxReadOnlyTools`（追加产品额外项）** | 净删 ≈40 行；消掉"两处白名单漂移"审计隐患；白拿 `spill_read`/`job_wait`/`terminal_read` 等新只读工具 | 必须保留会话 override / plan/minimal/creator / never 分支；`approvals` 队列不动 |
| **D7. 环境块补 git 分支 + 日期（`instructions.DetectEnvironment`）** | 模型少跑 `git branch`；`GitDirtyState` 三态防"假干净" | 必须保留 `workingDirectoryLine` 的文件策略披露（v0.24.0 无对应） |
| **D8. 工具输出接 `secrets.Redactor`**（可选） | 产品需求：现在只有 settings 面脱敏，工具输出不脱敏 | rig v0.24.0 **明确默认不脱敏**（`secrets/export.go:1-11`、`kit/doc.go:110-112`）→ 这是产品自建，不是「rig 白送」；在 `rebind.go` 的 `proxies`/`boundOutput` 处接线，注意流式（`secrets.Writer` 必须 Flush）与性能（`RedactForExport` 每次重建规则表，注释明确「不要放进逐 token 热路径」） |

### 第三批：高风险高收益，需要单独排期（预计净删 ≈640–1100 行，重写测试 ≈800–1500 行）

| 动作 | 收益 | 为什么危险 |
| --- | --- | --- |
| **D9. 会话持久化迁 `session/sqlite`** | 净删 ≈640（保检索质量）～1100（接受 rig 检索）行；白拿 `rig_events`/`rig_costs`/`rig_memory_pages`/`rig_spill`/租约/`ImportDir`/`ExportToDir`/`CanOpen` 只读打开 | ① 子模块**无 tag**，只能钉伪版本 `v0.0.0-20261120012823-c73e7cb83ecf`；② 抬 `modernc.org/sqlite` v1.61.1→**v1.62.0**；③ 表名列名全变，现有 `zeokeel.db` 需一次性迁移（`ImportDir` 只吃 NDJSON，本仓是 SQLite→SQLite）；④ 检索是裸 JSON LIKE（`search.go`），本仓 `search_text` 解码列的收益会丢；⑤ 没有 `RenameSession`，要 Extension/裸 SQL |

### 暂不动（并说明为什么）

| 项 | 理由 |
| --- | --- |
| **`internal/ui/*` 任何东西** | 产品语义层（设计契约、i18n、状态语义、快照）。rig 不提供 UI，`present/goal` 一类的"工具 vs UI"边界必须留在产品侧。 |
| **#5 记忆（条目模型）** | rig 的 wiki 模型（slug 页 + 摘要 + 渐进读）与产品条目模型（id/text/scope + 合并策略 + 内存面板）不可无损互换；换 = 重写 + 迁移 + 用户可见行为改变。 |
| **#4 成本统计（insights）** | 口径是产品定义的（日/模型/工作区/会话 + 可编辑价目表），rig `costmeter` 是"单次 Run 预算告警"，两者互补不重叠。可增量接预算，但不该替换。 |
| **#15 `kit` / `tools/full` / `presets` / `catalog` 装配层** | 本仓的装配是"多工作区 + 动态 MCP + 按会话裁剪 + root 重建"，`kit` 是"一次装配一个 agent"；换等于重写 17836 行装配层。`kit.PermissionPreset` 同理（本仓不用 kit）。 |
| **#16 `present` / goal 三件套** | rig 的 `present` 声明**已有文件**（`FilePresenter`），本仓的 `present` 是**内容产物**（`ArtifactPresenter`）；rig 没有 goal 工具。同名会把两套语义搅在一起（本仓 `modules/hosttools/tools.go:8-14` 已明确记录这个决定）。 |
| **#6 溢出目录形态** | 溢出文件落在工作区，模型能用 `read_file` 读回、用户能在文件树看见——这是产品可见语义；换成库内句柄就消失。 |
| **#11 补丁 / #12 撤销 / #10 本地检索** | **本仓没有这些能力**，rig 都提供，但接入是"新增功能"而非"删除自研"，应走产品需求评审（尤其 `undo` 会牵动审批卡与快照）。 |
| **C2 拒绝 glob** | **不能"不动"**：这是必须接管的（rig 已弃用），只是它属于"新增产品代码"（≈100 行 + 1 个行为级测试），不在"删除批次"里。 |

---

## 附录 A：证据与命令索引

```bash
# 版本
curl -s https://goproxy.cn/github.com/zeokeel/zeokeel-rig/@latest            # v0.24.0 / 2026-11-20T01:28:23Z
curl -s https://goproxy.cn/github.com/zeokeel/zeokeel-rig/session/sqlite/@v/list   # 空（无 tag）
curl -s https://goproxy.cn/github.com/zeokeel/zeokeel-rig/session/sqlite/@latest  # v0.0.0-20261120012823-c73e7cb83ecf
unzip -l .../@v/v0.24.0.zip | grep -i sqlite      # 只有 docs/sqlite.md（嵌套 module 不在主模块 zip）

# rig 源码（v0.24.0）
rg -n '^func [A-Z]|^type [A-Z]' $R/session/compact/*.go | grep -v _test
rg -n '^func [A-Z]|^type [A-Z]' $R/instructions/*.go | grep -v _test
rg -n 'SandboxReadOnlyTools' -A 28 $R/gate/sandbox.go
rg -n 'func ResourceToolName' -A 12 $R/tools/mcp/resource_tools.go       # v0.24.0: prefix+server+"__"+base
rg -n 'func ResourceTools' -A 6  $R19/tools/mcp/resource_tools.go        # v0.19.0: prefix+base
rg -n 'DeniedPath|deniedGlobs' $R19/sandbox/path.go $R19/tools/builtin/builtin.go
rg -n 'func \(p \*Policy\) DeniedPath' -A 8 $R/sandbox/path.go           # v0.24.0: 只判根归属

# 本仓量化
rg -o 'github\.com/zeokeel/zeokeel-rig[a-z0-9/_]*' --glob '*.go' -N | sort | uniq -c | sort -rn
wc -l internal/modules/{sessionstore,memory,toolkit,agent}/*.go | grep -v _test
rg -n 'zeokeel-rig/gate' --glob '*.go' internal cmd        # 只有 plan_mode.go
rg -n 'escalat|present_artifact|ProtectedPaths|DeniedGlobs|MaxIterations' --glob '*.go' internal cmd
```

## 附录 B：本次分析的限制

1. **没有编译/测试**（Lead 正在占用构建）：所有"本仓现状"结论来自源码阅读 + grep，未验证 v0.24.0 下的实际编译错误（例如 `mcp.ResourceTools` 签名兼容但语义变了——这类"能编译、语义变"的坑正是本报告要抓的）。
2. **没有对齐中间版本**：goproxy 只回 `v0.19.0`/`v0.24.0`，v0.20.0–v0.23.4 的行为靠 `CHANGELOG.md` 叙述；`/data/mygo/.cache/rigcheck/repo`（v0.23.4-3-g5e67e2c）与 `/data/mygo/audit/upstream/rig` 未作为结论依据。
3. **`session/sqlite` 源码取自其伪版本 zip**（`v0.0.0-20261120012823-c73e7cb83ecf`，与 v0.24.0 同一提交时间），因为嵌套 module 不在主模块 zip 内；已核对其 `go.mod` 指向 `zeokeel-rig v0.24.0`。
4. **行数为 `wc -l` 口径**，含注释与空行；"净可删"是"替换后可以删掉的现有文件行数"的估算，不含新增适配层。
## 附录 C：并发工作区状态（本报告的基线快照）

分析期间 Lead 与队友正在同一工作区改代码。本报告的「本仓现状」以 **2026-11-20 11:00–11:30 工作树**为基线；读完后已看到下列改动落地（未由我修改，仅记录，避免报告结论与本报告矛盾时误判）：

| 文件 | 状态 | 与本报告的对应 |
| --- | --- | --- |
| `internal/modules/toolkit/mcp_stdio.go` | 已改：`mcp.ResourceTools(client, "mcp__")`，注释说明 v0.24.0 自带 server 段 | **C8 / D1 已被采纳** |
| `internal/modules/toolkit/denied_glob_gate.go` | 新增（未跟踪）：产品侧 glob 拦截（枚举工具隐藏、单路径工具拒绝、目录子树同一规则） | **C2 已被采纳（实现中）** |
| `internal/modules/toolkit/settings_tool_impl.go` | 未跟踪（本报告读到的 168 行为当时内容） | C1 的永久产品代码 |
| `internal/modules/toolkit/{rebind,lsp,settings_tools}.go`、`internal/modules/agent/{agent,spill}.go`、`internal/memorytools/memory.go` 等 | 已改（升级适配） | 报告中涉及这些文件的"行数/行为"描述可能已偏移，结论（能否替换）不受影响 |

因此：**C2 与 C8 不需要在第二批/第三批重复排期**；D 表里剩下的 D2（注释）、D3（脱敏）、D4（懒加载）、D5–D9 仍是待办。若需要我针对已落地改动做一次"是否引入新回归"的复核（例如 `denied_glob_gate.go` 与 `grep`/`glob` 结果剔除的交互、`sanitizeMCPName` 变成死代码后是否还有测试引用），可以另开一轮只读复核。

---

## 附录 E：v0.26.1 落地状态（2026-11-20 晚，本报告的"后续"）

本报告写在 rig v0.24.0 上，当天下午 rig 又发了 v0.24.1 / v0.25.0 / v0.26.0 / v0.26.1。
本仓随后直接升级到 **v0.26.1**，其中几条结论因此变化（细节见 `SDK-v0.26.1-DELTA.md`）：

| 本报告条目 | 落地状态 |
| --- | --- |
| **C2 拒绝 glob（须自建）** | ✅ **已由 SDK 接管**：v0.25.0 的 `sandbox.WithHidden` 参与全部路径判定。产品侧只保留 `hiddenPatterns` 的**模式加宽**（`**/.git` → 同时加 `**/.git/**` 等）以保留"命中目录连子树一起隐藏"的承诺；`denied_glob_gate.go`（529 行）与其测试（379 行）已删除，行为级回归迁到 `toolkit/hidden_paths_test.go`。 |
| **C8 MCP 资源工具改名** | ✅ 已完成（`mcp.DefaultPrefix` + rig 自带 server 段 + `__2` 消歧）。 |
| **D3 改用 `secrets.RedactForExport`** | ❌ **作废**：v0.26.0 把 `secrets` 整包删除（SDK 不再做任何脱敏）。 |
| **D9 `session/sqlite` 迁移的三个反对意见** | ✅ 解除：v0.25.0 起子模块**有 tag**、`UpdateMeta` 补上改名/换工作区、检索补上 `search_text`/FTS5/CJK、导入导出对称。剩：抬 `modernc.org/sqlite` 到 v1.62.0 + SQLite→SQLite 迁移自写。 |
| **C7 read-only 语义** | ✅ 已定调：rig v0.26 允许"read-only + runner"执行命令，但产品只读预设**不把 runner 交给 shell 工具**，保留"shell 被禁用"的承诺（`rebind.go`）。 |
