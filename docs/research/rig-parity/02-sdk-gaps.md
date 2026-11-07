# SDK 待补功能清单（`old/` → Rig，属于 SDK 的部分）

> 用途：只列**属于 SDK 职责**、目前 Rig v0.12.1 还缺（或只有部分）的功能，并给出 `old/` 中的
> 具体参考路径，方便直接去补 Rig。
> old 根目录：**`/data/mygo/old`**；Rig 根目录：`/data/zeokeel-rig`。
> 边界：凡是需要 UI / SQLite 持久化 / 进程宿主 / OS 集成的，都不属于 SDK（见 §3）。

## 0. 判定"属于 SDK"的标准

同时满足才算：
1. 与 agent 循环、provider、工具实现、协议客户端（MCP/LSP）、审批/上下文等**通用能力**有关；
2. 不依赖具体产品 UI、数据库 schema、进程装配（Rig README 明说这些不属于 SDK）；
3. 能被任意 Rig 使用方通过**接口/选项**使用，产品只做实现或接线。

Rig 的契约都应该放在**根包 `github.com/zeokeel/zeokeel-rig`**（`package rig`，只依赖标准库），
实现放子包；只做 additive 变更（v0.13.x minor），不引入新依赖、保持 `CGO_ENABLED=0`。

---

## 1. 清单总览

| 优先级 | # | SDK 功能 | Rig v0.12.1 现状 | old 参考实现（关键路径） | rig 建议落点 | 预估 |
|---|---|---|---|---|---|---|
| P0 | 1 | **上下文压缩 Compaction**（窗口压缩/LLM 摘要/工具结果裁剪/图片 offload/手动 compact） | 无压缩接口与实现 | `internal/runtime/compaction.go`、`compaction_llm.go`、`context.go`、`manual_compaction.go` | 根包 `Compactor`/`ContextBudget` 契约 + `session/compact` 实现 + `kit.Config`/`agent` 接线 | 3–5 天 |
| P0 | 2 | **LSP references / implementation**（+ UTF-16 位置校验） | `tools/lsp/doc.go` 明确未实现 references、implementation | `internal/lsp/lsp.go`、`client.go`、`results.go`、`position.go` | `tools/lsp` 新方法 + `lsp_references`/`lsp_implementation` 工具（或 legacy `lsp` action） | 0.5–1 天 |
| P1 | 3 | **工具结果 spill**（>阈值落盘 + 摘要指针，避免每轮重发） | 只按字节截断，无落盘替换 | `internal/session/tool_spill.go`、`tool_spill_linux.go`、`tool_spill_other.go`；调用点 `session/controller.go:1148` | `tools/internal/toolutil` spill helper + agent/History 选项；`full` 接线 | 1 天 |
| P1 | 4 | **LLM auto-review Gate**（模型审前审批，超时回退人工） | `gate` 只有静态 gate；`tools/review` 有 Reviewer 工具但无 Gate 适配 | `internal/runtime/review.go` | `gate/review.go`（`Review(reviewer, opts) rig.Gate`）或 `tools/review.NewToolGate` | 0.5–1 天 |
| P1 | 5 | **Checkpoint + 中断回合恢复**（回合边界持久化契约 + 恢复助手） | 无 checkpoint 契约/钩子 | `internal/runtime/checkpoint.go`、`checkpoint_recovery.go`；持久化参考 `internal/store/checkpoint.go` | 根包 `Checkpoint`/`CheckpointStore` 契约 + `agent` 边界钩子 + `agent.RecoverInterruptedTurn` | 2–3 天 |
| P1 | 6 | **审批/提问 Gate 辅助**（Asker → Gate；并发审批队列语义） | `gate.Func` 可拼；无现成 Ask gate/队列 | `internal/runtime/question_gate.go`、`approval.go`、`allow.go` | `gate/ask.go`（基于 `rig.Asker`）+ `ErrApprovalExpired` 语义 | 0.5 天 |
| P2 | 7 | **MCP 通知流**（GET SSE、通知分发、Last-Event-ID 恢复、session DELETE） | `tools/mcp/doc.go` 明确未实现 | `internal/mcp/mcp.go`、`resources.go`、`transport_http_test.go`（请求-响应参考） | `tools/mcp/http_transport.go` 增加 GET stream + 通知回调 + DELETE | 1–2 天 |
| P2 | 8 | **Goal round driver**（跨回合自动推进，预算/上限） | 有 goal 工具 + revision-CAS，无自动驱动 | `internal/session/goal_driver.go`、`goal_driver_test.go` | `agent/GoalDriver` 或 `kit` 辅助（消费 `rig.GoalStore`） | 1 天 |
| P3 | 9 | **Agent 模式/工具集预设**（Standard/Minimal/Creator → 工具子集） | `kit.Config.Mode` 是**权限模式**，不是 agent 模式 | `internal/runtime/modes.go`、`mode_deep.go` | `kit.Config.ToolPreset` 或 `tools/presets`（只做工具子集；prompt 留产品） | 0.5 天 |
| P3 | 10 | **Reasoning effort 一等选项** | 可用 `rig.Request.Extra["reasoning_effort"]` 但无 typed 配置 | `internal/provider/openai/openai.go:91-99`、`runtime/settings.go:51,106` | `openai.Config.DefaultReasoningEffort` + `kit.Config.ReasoningEffort`（或仅文档化 Extra） | 0.2 天 |
| P3 | 11 | **Terminal/后台 owner 作用域清理**（DSH 细节，非 old 缺口） | 未实现（CHANGELOG 已记） | `internal/tools/terminal.go`（会话管理参考） | `tools/terminal`/`tools/jobs` owner 字段与 `CloseAll(owner)` | 0.5 天 |
| P3 | 12 | **LSP UTF-16 位置校验 helper**（并入 #2） | `character` 按 UTF-16 unit 语义透传，无越界校验 | `internal/lsp/position.go`（`UTF16Len`/`ValidatePosition`） | `tools/lsp` 增加 `ValidatePosition`；工具入口校验 | 0.3 天 |

**合计约 12–17 人日**；P0 两项约 4–6 天，建议先做。

---

## 2. 逐项细节（old 具体路径）

### P0-1 上下文压缩 Compaction

**为什么属于 SDK**：agent 自带 History/Provider，压缩是"何时把历史换成摘要"的通用策略，
任何使用方都需要；产品只负责把压缩结果持久化替换（old 是 session/store 层）。

**Rig 现状**：`agent` 每轮把完整历史发给 provider；`kit.Config`/`AgentSpec` 无预算/压缩项。

**old 参考**：
- `/data/mygo/old/internal/runtime/compaction.go`（331 行）
  - `type Compactor interface`（:21）、`DefaultCompactionKeepLast = 8`（:29）、
    `DefaultImageKeepLast = 2`（:37）、`ImageOffloadPlaceholder`（:41）、
    `WindowCompactor`（:64）、`CompactionSummaryContent`（:164）、
    `estimateTokens`（:204）、`offloadOldImages`（:278）
- `/data/mygo/old/internal/runtime/compaction_llm.go`（304 行）
  - `LLMCompactor`（:48）、`PruneToolResults`（:269）、`DefaultToolResultKeepLast = 4`（:13）
- `/data/mygo/old/internal/runtime/context.go`（30 行）：预算/窗口辅助
- `/data/mygo/old/internal/runtime/manual_compaction.go`（152 行）：手动压缩（`/compact` 语义）
- 产品侧参考（**不要照搬进 SDK**）：`/data/mygo/old/internal/session/controller_compact.go`（256 行，持久替换）、`/data/mygo/old/internal/state/compaction.go`
- 可移植测试：`/data/mygo/old/internal/runtime/compaction_test.go`、`compaction_llm_test.go`、`manual_compaction_test.go`、`compaction_fp10_test.go`

**建议 rig 落点**：
- 根包：`type Compactor interface { Compact(ctx context.Context, messages []Message, budget int) ([]Message, error) }`，
  `AgentSpec.Compactor`/`ContextBudget`（或 `kit.Config.Compaction`）；
- `session/compact`：`WindowCompactor`（保留系统消息 + 最近 N 轮 + 图片 offload）、`LLMCompactor`（provider 摘要 +
  `PruneToolResults`）、`EstimateTokens` 导出（支持 CJK）；
- 产品负责把压缩后的消息持久化替换（old controller_compact 的边界）。

**验收**：预算触发/不触发、系统消息保留、工具结果裁剪、图片占位、摘要失败回退窗口压缩、
`-race`、无压缩器时行为与现在一致。

### P0-2 LSP references / implementation（+ UTF-16 校验）

**Rig 现状**：`/data/zeokeel-rig/tools/lsp/doc.go:57` 明确列未实现 references、implementation。

**old 参考**：
- `/data/mygo/old/internal/lsp/lsp.go`：`Operation` 定义（:22 `OpHover`；:115-123 动作别名表含
  `defined`/`references`/`implementation`）
- `/data/mygo/old/internal/lsp/client.go`：:414-421 方法映射
  （`textDocument/definition|references|implementation|hover`）、:48-51 capabilities 声明
- `/data/mygo/old/internal/lsp/results.go`：Location/LocationLink 归一与输出上限
- `/data/mygo/old/internal/lsp/position.go`：`UTF16Len`（:11）、`ValidatePosition`（:27）——
  1-based 行/列 → UTF-16 code unit 校验
- `/data/mygo/old/internal/lsp/fake_server_test.go`：假服务器测试写法可移植

**建议 rig 落点**：`tools/lsp/client.go` 加 `References`/`Implementation`；`tools/lsp/tools.go` 加
`lsp_references`、`lsp_implementation`（legacy `lsp` action 加 `references|implementation`）；
工具入口用 `ValidatePosition` 先校验。

**验收**：单/多 Location、LocationLink、空结果、UTF-16 越界可读错误、超时、fake server 测试。

### P1-3 工具结果 spill

**为什么属于 SDK**：通用"大结果不重复进上下文"策略；需要可选文件落点（产品给 root）。

**old 参考**：
- `/data/mygo/old/internal/session/tool_spill.go`（193 行）：阈值 `32KiB`、头部 `2KiB`、
  保留 `100` 个、目录 `.zeokeel/tool-results`、摘要格式
- `/data/mygo/old/internal/session/tool_spill_linux.go`（81 行）、`tool_spill_other.go`（39 行）
- 调用点：`/data/mygo/old/internal/session/controller.go:1148`（`spillToolResult`）
- 测试：`tool_spill_test.go`、`tool_spill_internal_test.go`、`tool_spill_toctou_test.go`

**建议 rig 落点**：`tools/internal/toolutil` 增加 `Spill(root, callID, result)` helper；`agent` 提供可选
`SpillPolicy`（阈值/目录/保留数）或在 `session.History` 实现侧替换；`full.All` 从 `AllOptions` 接线。

**验收**：小于阈值不动、超阈值写文件+摘要指针、保留上限清理、无 root 不 spill、并发安全、路径不越界。

### P1-4 LLM auto-review Gate

**old 参考**：
- `/data/mygo/old/internal/runtime/review.go`（292 行）：`DefaultToolReviewTimeout = 15s`（:14）、
  `ToolReviewInput`（:21）、`ToolReviewer`（:47）、`LLMToolReviewer`（:75）、
  `ParseToolReviewDecision`（:180）、失败回退人工
- 产品接线参考（不要搬）：`/data/mygo/old/internal/app/*`（审批 UI 接线）

**建议 rig 落点**：`gate/review.go` 提供 `Review(reviewer rig.Reviewer, opts)` 返回 `rig.Gate`：
allow→DecisionAllow、deny→DecisionDeny(reason)、超时/错误按配置回退 `DecisionDeny` 或调用下一个 Gate；
可直接复用 `tools/review.SubagentReviewer`（SDK 内已有）与 `SubagentRunner`。

**验收**：allow/deny、reason 透传、15s 超时回退、ctx 取消、`-race`。

### P1-5 Checkpoint + 中断回合恢复

**old 参考**：
- `/data/mygo/old/internal/runtime/checkpoint.go`（204 行）：`Checkpoint`（:12）、
  Phase `completed`/`requested`/`tool_pending`（:32-42）、`CheckpointStore`（:64）
- `/data/mygo/old/internal/runtime/checkpoint_recovery.go`（174 行）：`InterruptedTurnRepair`（:39）、
  `CheckpointLoader`（:77）、`RecoverInterruptedTurn`（:86）
- 持久化参考（产品）：`/data/mygo/old/internal/store/checkpoint.go`、`session/controller_recovery.go`
- 测试：`checkpoint_test.go`、`checkpoint_barrier_test.go`、`checkpoint_recovery_test.go`

**建议 rig 落点**：根包 `Checkpoint`/`CheckpointStore` 契约 + `agent` 在 "模型请求前 / 副作用工具前 /
回合完成" 调 store（可选）；`agent.RecoverInterruptedTurn(ctx, store, sessionID)` 只做消息层修复
（补 unknown-outcome、assistant 中断锚点），不重放副作用。

**验收**：三个 phase 写入、恢复幂等、不重放副作用、Stop 不误判、无 store 时行为不变。

### P1-6 审批/提问 Gate 辅助

**old 参考**：
- `/data/mygo/old/internal/runtime/question_gate.go`（107 行）：`QuestionGate.Ask(ctx, callID, question, options)`
  （:25）、`ErrQuestionTimeout`（:32，超时回退 pending/fire-and-forget）、`ask_user` 拦截（:35）
- `/data/mygo/old/internal/runtime/approval.go`（234 行）：`ApprovalQueue`（:45）、`NewApprovalQueue`（:76）、
  `ErrApprovalExpired`（:16）、多会话排队/上下文结束语义
- `/data/mygo/old/internal/runtime/allow.go`：允许模式/参数匹配（是否需审批）

**建议 rig 落点**：`gate/ask.go`：`Ask(asker rig.Asker, opts)` 实现 `rig.Gate`（把工具调用转成问题，
Allow/Deny/Always）；`ErrApprovalExpired` 可用于多 run 并发等待语义；产品实现 `Asker`（UI）即可。

**验收**：allow/deny/always、超时回退、ctx 结束、并发多 run 不串答案。

### P2-7 MCP 通知流

**old 参考**：
- `/data/mygo/old/internal/mcp/mcp.go`、`resources.go`、`workspace.go`
- `/data/mygo/old/internal/mcp/transport_http_test.go`（HTTP 请求-响应参考；old 也没有 GET 通知流）
- Rig 侧未实现清单：`/data/zeokeel-rig/tools/mcp/doc.go:61`

**建议 rig 落点**：`tools/mcp/http_transport.go`：可选 `Subscribe(ctx)` GET SSE → 分发 notification；
`Last-Event-ID` 断点；`DeleteSession` 优雅关闭；保持现有 Request/Notify 行为不变；httptest 测试。

**验收**：通知不进 pending/不重复、断线恢复、Close 幂等、无订阅服务器兼容。

### P2-8 Goal round driver

**old 参考**：
- `/data/mygo/old/internal/session/goal_driver.go`（265 行）：`GoalRoundDriver`（:51）、
  `NewGoalRoundDriver`（:59）、`SessionGoalRoundsKey`（:30）、`GoalRoundDriverConfig`（:36，预算/cap）
- 测试：`goal_driver_test.go`、`goal_edit_test.go`、`goal_phase_internal_test.go`

**建议 rig 落点**：`agent/GoalDriver`（或 `tools/hosttools` 旁）：
`Run(ctx, agent, goalStore, prompt) (RunResult, error)`，循环 `Run` 直到 complete/blocked/预算尽；
edit 不重置 rounds；blocked 返回原因。持久化/UI 留产品。

**验收**：预算/cap、complete、blocked、edit 不重置、blocked→人工恢复、ctx 取消。

### P3-9 Agent 模式/工具集预设

**old 参考**：
- `/data/mygo/old/internal/runtime/modes.go`（333 行）：`Modes()`（:100）、`NormalizeMode`（:107）、
  `modeAllowsTool`、`minimalToolNames`、`ModeTools`（:153）
- `/data/mygo/old/internal/runtime/mode_deep.go`（145 行，旧别名）
- 测试：`modes_test.go`、`mode_acceptance_test.go`、`mode_pin_test.go`

**建议 rig 落点**：`kit.Config.ToolPreset`（standard/minimal/creator → 工具名子集过滤），
prompt 策略留产品；或 `tools/presets` 导出 `Preset(name) []string`。

**验收**：每个预设工具集断言、未知模式报错、与 Gate/Policy 叠加不冲突。

### P3-10 Reasoning effort 一等公民

**old 参考**：
- `/data/mygo/old/internal/provider/openai/openai.go:91-99`：`ReasoningEffort` 字段
- `/data/mygo/old/internal/runtime/settings.go:51,106`：`provider.reasoning_effort` 设置键
- `/data/mygo/old/internal/runtime/model_adapter.go`：请求构造侧

**Rig 现状**：`rig.Request.Extra map[string]any` + `provider/openai` 的 `mergeExtra` 已可传
（`openai.go:459`），只是没有 typed 配置。建议 `kit.Config.ReasoningEffort` + `openai.Config.DefaultReasoningEffort`
（或只在文档写明 Extra 用法，成本最低）。

### P3-11 Terminal owner 作用域（DSH 细节）

`/data/mygo/old/internal/tools/terminal.go` 的会话管理可参考；Rig CHANGELOG 已列
"owner-scoped terminal backend registry"未实现。给 `tools/terminal`/`tools/jobs` 的 Manager 加 owner 标签与
`CloseAll(owner)` 即可。

---

## 3. 明确**不属于** SDK（不要放进 Rig，留产品仓）

| 功能 | old 路径 | 原因 |
|---|---|---|
| UI shell（chat/composer/面板/审批 UI） | `/data/mygo/old/internal/shell`（25,229 行） | mygo UI，产品自有 |
| 会话控制器/自动标题/附件/fork/export/feedback | `/data/mygo/old/internal/session`（8,840 行） | 需要产品数据模型/UI |
| SQLite 持久化（sessions/messages/events/checkpoints/automation） | `/data/mygo/old/internal/store`（5,887 行） | Rig 只有 `History`/`SessionLog` 契约 |
| 组合根/进程宿主/设置/automation/审批接线 | `/data/mygo/old/internal/app`（8,153 行） | 进程装配，产品保留（kernel 装配） |
| ACP server | `/data/mygo/old/internal/acp`（1,863 行） | 产品协议宿主；可调用 Rig agent |
| system prompt 组合 / plan mode prompt | `/data/mygo/old/internal/prompt`（1,398 行） | 产品 prompt 策略 |
| 事件 projector / compaction 事件 / workspace diff / 会话引用 | `/data/mygo/old/internal/state`、`workspacechanges`、`sessionreference` | 产品事件模型/UI |
| 全局快捷键 / 导入器 / 自动更新 / i18n / 图标 / 设计系统 / 品牌 / winproc | `/data/mygo/old/internal/{globalhotkey,importer,updater,i18n,icons,design,brand,winproc}` | OS/产品层 |
| 模型目录/预设 | `/data/mygo/old/internal/{modelcatalog,presets}` | 产品配置 |
| auto-memory 抽取/整合 | `/data/mygo/old/internal/memory`（1,059 行） | 产品策略；Rig 已有 `memory` 工具 + `MemoryStore` 契约 |
| 审批队列持久化 / 会话级 always-allow | `/data/mygo/old/internal/session/permission_persist*` | 产品存储 |
| PTC 运行时 | — | 旧产品已决定不做；Rig 已有 opt-in `run_code` |

---

## 4. 已被 Rig 覆盖、无需重补（确认过的对照）

- 多模态：`old/internal/runtime/multimodal.go`（280 行，eino 侧图片注入）→ Rig
  `rig.ToolResult.Parts` + provider/openai `image_url` 已覆盖（`provider/openai/openai.go:495-519`）。
- 子代理/team 异步、审批继承：`old/internal/runtime/subagent*.go`、`sink*.go` → Rig
  `agent.NewRunner`（克隆 Gate）+ async team + `rig.NoticeSource`。
- 流式 reasoning 事件：`old/internal/runtime/stream.go` → Rig `rig.EventReasoningDelta`。
- MCP tools/resources/prompts、LSP hover/definition/diagnostics/symbols、web_search 接口、
  skills 文件加载、team task board、schedule CRUD、goal CRUD、jobs/terminal 基础：
  均已在 Rig v0.12.1（见 `docs/ai/tools.json`）。

---

## 5. 建议实施顺序

1. **P0-1 Compaction**（最大缺失；先定义根包契约 + `session/compact` 的 Window 实现，再接 LLM 摘要）。
2. **P0-2 LSP references/implementation**（小而高频，顺便把 `ValidatePosition` 带进工具入口）。
3. **P1-4 review gate / P1-1 spill / P1-2 checkpoint**（按产品优先级）。
4. **P2-7 MCP 通知流**、**P2-8 goal driver**。
5. P3 视需要。

每个条目都建议：根包只加接口/类型（additive），实现放子包；测试用 fake provider / fake server /
临时目录；`-race` + `CGO_ENABLED=0`；不要引入新依赖。
