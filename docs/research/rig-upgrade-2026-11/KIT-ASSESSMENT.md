# 评估：能不能用 rig 的 `kit`？（2026-11-21）

> 结论：**能用，但不是一个"搬进来就能删自研"的可插拔库**。`kit` 是给"宿主想要一个完整 agent"的
> 一站式装配层（CLI/SDK 形态）；ZeoKeel 本身已经是同一批 rig 原语之上、带产品契约的装配层。
> 整体替换会重写 agent 模块并丢掉产品能力；**建议不替换**，只按需借鉴其中的独立件
> （`costmeter` 预算、`Preflight` 诊断、`kit.Runtime` 的共享经理）。
> 本文记录实测证据与兼容性矩阵，防止后续重复评估。

## 1. `kit` 是什么

`kit.New(kit.Config)` 一次构造出 `*kit.Agent`（+ 可选 `*kit.Runtime`），内部组装：

- provider（openai/ollama/lmstudio/local，或注入 `Config.Provider`）
- 系统提示词（identity/environment/instructions(AGENTS.md)/memory/skills/前端段）
- `tools/full` 完整工具集（经 `ToolPreset` 过滤 + 零配置体检裁剪）或 **注入 `Config.Tools`**
- 审批（`Approval=ask/auto/never`，`Asker`、审批记忆 `Approvals`、提示链、超时）
- 会话（`SessionStoreDir`/`SessionStore` 的 NDJSON 目录实现，或注入 `History`/`SessionLog`/`CheckpointStore`）
- 压缩、溢出（`Spill`）、动态工具发现（`ToolDiscovery`/`InitialActiveTools`/`Activations`）、`undo`、
  `costmeter`（`Meter`/`MaxRunCostUSD`）、后台任务/PTY（`Jobs`/`Background`）、MCP、记忆 wiki、技能、
  调度器、团队任务、子代理 runner、目标/计划控制器、`Preflight` 诊断、`Shutdown` 生命周期。

它是一个**成品装配**（约 2000 行 `kit.go` + 若干子文件），不是"组件库"；可注入点很多，
但默认值就是一套完整产品行为。

## 2. 实测（spike，已删除，仅留结论）

在本仓模块内用 `kit.New` + 注入脚本化 provider / 自建工具 / 临时目录跑了一轮：

```go
ag, err := kit.New(kit.Config{
    Model: "test-model", Provider: scripted, Tools: []rig.Tool{echoTool{}},
    Root: t.TempDir(), SessionStoreDir: t.TempDir(), Approval: kit.ApprovalAuto,
})
result, err := ag.Run(ctx, kit.RunRequest{
    SessionID: "s1", Input: []rig.Message{rig.NewMessage(rig.RoleUser, "hi")}, Emit: collect,
})
```

结果：

- `err=nil`，`last="done"`，事件序列 `turn_start / text_delta / message / turn_end` ——
  **与我们 `handleEvent` 消费的 `rig.Event` 同构**，事件桥接不是障碍。
- `ag.Tools()` = 16 个（默认 `tools/full` 经体检裁剪后的结果）。
- `ag.Preflight()` 直接给出沙箱强制度、后端、网络是否受管、审批链与可执行建议
  （实测这台机器：`Enforced:false / Enforcement:none / Network:not-governed` + 两条建议）。
- `ag.Shutdown(ctx)` 关闭后台句柄与存储。

结论：**kit 能在本仓里跑起来**，它的 `Config` 也接受我们的 `History`/`SessionLog`/`CheckpointStore`/
`Gate`/`Tools`/`Asker`/`Spill`/`Skills`/`Goals`/`Undo` 等注入；所以"技术上能替换 agent 模块"并不是假的。

## 3. 为什么仍不建议整体替换（兼容性矩阵）

| 产品能力 | 现状（本仓） | 换 kit 后 | 影响 |
|---|---|---|---|
| 会话存储 | `sessionstore` SQLite：检索解码列、归档、洞察、事件折叠 | kit 默认 `session/store` NDJSON 目录（可注入 `History`+`SessionLog`，但 `SessionStore` 目录仍会被用于会话索引/租约等） | 要么双写，要么重做会话索引/删除/重命名；`RenameSession` 仍需自己补 |
| 系统提示词 | 每 run 渲染（`SystemPromptFunc`）、分段/来源、上下文快照、日期、模式/权限行、AGENTS.md 信任围栏 | kit 自己拼 prompt（instructions/memory/skills/前端段），只有 `Config.SystemPrompt`/`PromptSections` 两个扩展口 | **上下文检查器（提示词分段/估算）整块要重做**；信任围栏、工具 guidance、发现面一致性都要重新实现 |
| 工具面 | 每 run 按 会话模式/preset/权限 过滤的 `toolkit` 构建（plan/minimal/creator 白名单）、plan 审批、spill store、undo 数据目录、发现尾部 | `Config.Tools` 在**构造期**固定；kit 自带 `tools/full`/preset/发现 | agent 必须按会话（甚至按 run）重建；产品模式切换/权限覆盖要重写 |
| 审批 | 产品审批队列/帧/收据/持久化、会话级覆盖、plan 模式语义、沙箱授权 | `Asker` 回调解；gate 记忆/超时/提示链在 kit 内 | 可适配 `Asker`，但"谁来决定"会变成两层；plan 白名单与沙箱语义要搬进 `Config.Gate` |
| 会话/工作区 | 一进程多会话、每会话工作区根、切换工作区 | `Agent.Root` 固定；`Runtime.NewAgent` 可按根建多个 agent | 要改成"一会话一 kit.Agent"（stores/jobs 共享靠 Runtime），现有 run 级装配全部重写 |
| 团队/子代理/工作流 | 产品 `agent` 模块自建 orchestration（子会话、预算、收据、cold resume） | kit 只有 `Runner`/`TeamTasks` 注入点 | 产品编排层基本要重写或包一层 |
| 事件/轨迹/洞察 | 产品定义 20+ 事件、快照/轨迹/洞察页 | kit 只把 `rig.Event` 透传 | 事件桥可复用，但产品事件仍要自己写 |
| ACP/自动化/更新器 | 产品模块 | kit 不涉及 | 保持不变 |
| 成本 | 产品 catalog + 事件折叠 | kit `costmeter` 提供每次 Run 预算护栏（新能力） | **可作为独立件借鉴** |

同时 kit 的默认工具集（`tools/full`）与我们 toolkit 的构建方式互斥：
`Config.Tools` 非 nil 时不再经过 OS 沙箱包装与产品策略，反过来我们的工具也已经带策略。

## 4. 三个选项

- **A. 整体替换 agent 模块（不推荐）**：能跑，但要重写提示词/工具面/审批/会话编排/上下文检查器，
  产品能力净损失或等价重写，收益只是"少维护 agent 装配"。
- **B. 用 kit 新做一个极小产品入口（可评估，不急）**：例如"嵌入式/单工作区 CLI"用 `kit.New` 十几行
  起一个 agent。本仓的 headless 与桌面共享同一装配、同一存储与审批语义，另起一条只增加分叉。
- **C. 只借鉴独立件（推荐）**：
  1. **`costmeter` 每次 Run 预算护栏**：产品已有成本折叠，但没有"单轮预算熔断"；可在现有 run 外包一层
     `Meter`/`MaxRunCostUSD`（不需要 kit）。
  2. **`Preflight` 诊断**：可作为设置页"沙箱/审批现状"的补充来源（我们已有沙箱状态页，可先对比差异）。
  3. **`kit.Runtime` 的共享 jobs/terminals/MCP 池**：我们已有 job service 与按工作区构建，暂不必要。

## 5. 什么情况下重新评估

- 产品决定放弃"多会话 + 每会话工作区 + 产品审批 UI"这类桌面特性，转向极简 CLI/嵌入式形态；
- 或 kit 把提示词/工具面从"构造期固定"改成"每 run 可注入"（那样替换 agent 循环的代价才真正变小）；
- 或我们需要 SDK 官方的成本账本/多进程租约，且不愿意自己维护适配层。

在此之前：**不引入 kit 依赖、不替换 agent 模块**；需要时按 §4-C 单点借鉴。
