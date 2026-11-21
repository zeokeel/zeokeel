# 哪些产品能力适合上移到 rig（SDK）——评估与分批计划（2026-11-21）

> 前提：rig（`github.com/zeokeel/zeokeel-rig`）与 ZeoKeel 同一维护方，所以"上移"是组织上可行的。
> 目标是**把与宿主无关的语义放进 SDK，产品只保留接线（contracts 适配 + UI + 产品策略选择）**。
> 但"只做接线"只对**后端**成立：UI 层（约 10.3 万行）与产品契约（约 8.8 千行）必然留在本仓。

## 0. 判定标准（四条同时满足才适合上移）

1. **宿主无关**：不依赖 `internal/contracts`、不依赖本仓 UI/事件命名。
2. **UI 无感**：产出的是数据/状态/决策，不是帧或文案（文案可留在 SDK 用中性英文）。
3. **策略中性**：产品策略（默认权限档、提示词内容、白名单取舍）以配置传入，SDK 不替产品做决定。
4. **复用价值**：至少第二个宿主会用到（CLI、IDE、其他客户端、headless、自动化）。

## 1. 现状量化（本仓后端 17 个模块，非测试约 6.4 万行）

| 模块 | 非测试行数 | 性质 |
|---|---|---|
| agent | ~14k（agent.go 3.8k、child_sessions 868、team 746、system_prompt 717、observability 649、cold_resume 461…） | 混合：run 管道/提示词/事件折叠可上移；UI 事件投影、产品预设保留 |
| toolkit | ~7.6k | 大部分已是 rig 工具；产品胶水（模式面/preset/计划白名单/隐藏规则）可上移 |
| automation / update / importer / acp / workspace / insights | 4.3k / 3.1k / 1.6k / 2.9k / 2.4k / 1.6k | 产品为主；ACP、workspace 检测部分可上移 |
| sessionstore | ~1.5k | **强候选**（见 A.1） |
| approvals | ~0.6k（另有 UI 侧） | **强候选**（见 A.5） |
| memory / skills / goal / modelcatalog / sandbox / hosttools | 2.3k / 2.1k / 2.1k / 1.1k / 0.2k / 1.5k | 多数已有 rig 对应包，产品只剩适配与 UI |

## 2. 分层清单

### A 级：高契合、收益明确（建议优先）

| # | 能力 | 现状 | 上移形态 | 产品保留 | 预估净删（含测试） |
|---|---|---|---|---|---|
| A.1 | **会话存储 SQLite + 解码列检索** | `sessionstore` 4 表 + `search_text` 解码列（1.5k + 1.7k 测试） | 补强 rig `session/sqlite`（RenameSession、解码列检索、保留其 cost/memory/spill/lease/export） | 会话列表/标题/工作区绑定、洞察查询 | ~1.5k + 测试迁移 |
| A.2 | **提示词分段装配 + 上下文快照度量** | `system_prompt.go` 717 行（身份/环境/工作目录/日期/审批/模式/config/AGENTS.md/preset/memory/skills/工具指引、分段渲染、token 估算） | rig `prompt` 包：`Assemble(Options) (string, []Section)` + 稳定 ID/渲染规则；**纯函数、无 I/O** | 各段文案、注入来源 UI、快照事件 | ~700 + 测试 |
| A.3 | **上下文溢出恢复** | `context_recovery.go` 177 行（一次压实 + 同轮重试、预算预留、失败归档、审计事件） | rig `agent` 选项：`AutoRecoverContext`（detect→compact→retry once），或 `session/recovery` 包 | UI 的"恢复中/失败"提示与事件折叠 | ~180 + 测试 |
| A.4 | **审批队列语义** | `approvals` 的 Pending/Resolve/超时/记忆/会话覆盖/收据（约 0.6k 核心） | rig `gate` 的 `Queue`/`Manager`（按会话的 pending、决定、持久化接口、always/once/session 语义） | 审批卡 UI、文案、权限档 UI、持久化实现 | ~400 + 测试 |
| A.5 | **委派子会话核心** | `child_sessions.go` 868 + `cold_resume.go` 461 + `subagent.go` 302 + `child_notices.go` 198 | rig `agent/delegate` 或 `session/children`：子会话记录（存于 History/Store）、预算、排除项、冷启动 continuation、完成收据 | 团队/子代理 UI、命名与图标、产品事件 | ~1.2k + 测试 |

**A 级合计**：产品后端可减约 **4–5k 行**（约占非测试后端 7–8%），"接线"部分主要是 contracts 适配器与 UI 投影。

### B 级：可上移，但需要先定契约（中等收益）

| # | 能力 | 说明 |
|---|---|---|
| B.1 | **运行模式白名单 + 工具面过滤** | `plan/minimal/creator` 三张表（383 行）→ rig `tools/presets` 或 `gate` 的"surface policy"；模式是产品概念，但"按模式给白名单"是通用机制。可把表与过滤函数上移，产品传模式配置。 |
| B.2 | **动态发现尾部 + initial active 推导** | 我们已用 rig `tools/discovery`；把"尾部名单 + 显式授权保留 + 子会话排除"做成 SDK `discovery.Options.Tail/Forced`，产品只剩配置。 |
| B.3 | **Jobs 读模型** | 把"跨 root/build 合并快照、kind 解析"下移到 `jobs.Manager`（产品 UI 直接用）。 |
| B.4 | **工作区探测（git 分支/脏状态/工作树）** | `workspace` 模块里的 git 探测与 worktree 检测 → rig `sandbox`/`worktree` 工具层，产品只做切换 UI。 |
| B.5 | **模型目录/价格** | `modelcatalog`（1.1k）→ rig `modelprice` 扩展（可编辑目录、价格表），产品只做设置页。 |
| B.6 | **ACP 服务端** | `acp` 2.9k 是编辑器协议宿主；若第二个宿主也要 ACP，上移为 rig `acp` 包，产品只接线。 |

**B 级合计**：再减约 **1.5–2.5k 行**，但涉及 SDK 契约设计与跨产品共识，建议在 A 级稳定后做。

### C 级：不建议上移（留在产品）

- **UI 全部**（10.3 万行）：帧、交互、文案、视觉 token、窗口/托盘/快捷键。
- **产品契约**（8.8 千行）：`contracts` 是 UI 与后端的边界；上移后只会变成 SDK 类型 + 适配层，行数不减。
- **产品策略与内容**：系统提示词正文、审批文案、权限档默认值、预设（persona/prompt）、团队/子代理的展示语义、洞察口径、自动化触发器 UI、更新器、导入器。
- **安全边界的最终决定权**：隐藏路径列表、沙箱降级策略、审批默认值——SDK 提供机制，产品做选择。

## 3. 迁移机制（保证产品仍"只接线"）

1. **先在 SDK 定义接口 + 语义文档 + 迁移测试**（rig 侧 `storagetest` 这类契约测试已有先例）。
2. **产品 `contracts` 用类型别名适配 SDK 类型**（像现在 `rig.SessionLog`/`History` 那样），UI 不动。
3. **一次只搬一处**，产品保留"旧实现 → 新实现"的对照测试（golden 输出），确认字节级/行为级一致后再删旧代码。
4. **rig 版本节奏**：每批上移发一个 minor tag；产品按现有升级流程（研究文档 + 审计）接入。
5. **删除前审计**：同本轮做法——变异测试证明旧代码的测试确实由新实现接管。

## 4. 风险与不做的理由

- **SDK API 稳定性负担**：上移的接口一旦发布就要兼容；提示词装配/审批队列这种"策略密集"的接口最容易反复。
- **产品语义泄漏**：把"plan/minimal/creator"直接写进 SDK 会让 SDK 长成 ZeoKeel 专用；B 级必须先抽象成通用配置。
- **数据迁移**：A.1（store）涉及现有 `zeokeel.db` 表结构；必须带一次性迁移 + 回滚 + 旧库只读降级。
- **测试迁移成本**：A 级测试约 5–6k 行要搬到 rig 或重写，短期总工作量是**净增**，收益是后续维护面变小。

## 5. 建议顺序（若决定推进）

1. **A.2 提示词装配/快照**（纯函数、无 I/O、无迁移风险）→ 建立"上移一套 + 产品接线"的样板。
2. **A.3 上下文溢出恢复**（有明确边界与现有测试）。
3. **A.4 审批队列**（产品 UI 已稳定，语义可文档化）。
4. **A.1 会话存储**（收益最大但迁移风险最高，放最后并单独设计迁移）。
5. **A.5 委派子会话**（最大的一块，需要子会话存储/预算/收据的契约共识）。
6. B 级（模式白名单、发现尾部、jobs、workspace、modelcatalog、ACP）按第二宿主需求逐项评估。

## 6. 一页结论

- **适合上移**：会话存储（带解码检索）、提示词分段/快照、上下文溢出恢复、审批队列、委派子会话、模式白名单、发现尾部、jobs 读模型、工作区探测、模型目录、ACP。
- **不适合**：UI、产品契约、提示词内容、权限默认值、团队/洞察/自动化/更新器的产品语义。
- **"产品只做接线"在后端可以做到大约 7–10% 的代码缩减**（A 级 4–5k + B 级 1.5–2.5k），UI 与契约层不变；短期总工作量是净增，换的是长期单一实现与跨宿主复用。
