# Agent 客户端入口组件规格（AGENT-SURFACES.md）

> 本文是 [DESIGN.md](DESIGN.md) §2「场景 → 组件硬映射」的展开规格：**Agent 客户端 9 个 surface 的入口组件契约**。
> **当前状态（2026-11 审计修复后）：S1–S9 全部已有入口实现；S9 从右侧栏 TeamSurface 改为会话内的 agent surface（PopoverBase 团队浮层 + 主栏子会话视图）。** 2026-11 多角色审计修复补充了 US-01 分段交错、US-03/04 呈现保真、UX-02 dock 预算、UX-06 stopping、UX-18 草稿持久化、UX-19 播报、RG-09 旧权限提示、US-11 todo 披露等规格（各节「2026-11 审计」段）。每节保留目标 API 草案（作为契约）、最终实现位置、迁移步骤与验收（迁移步骤仅作历史记录）。
> 落地顺序与 lint 规则见 [DESIGN-LINT.md](DESIGN-LINT.md)；token/预设见 [DESIGN.md](DESIGN.md) §5/§6。

---

## 0. 定位与总原则

### 0.1 为什么要"入口组件"

Agent 客户端的 UI 不是 CRUD 页面，而是**推理与行动的外化**：会话线程、工具调用、推理、审批、计划、提问、composer、运行态、团队/分支。每类表面都有固定的任务语义和状态机；如果每个模块各自用官方件拼一遍，就会出现：

- 4 条工具卡渲染路径、14 个状态映射函数（当前真实存量）；
- 审批/计划/提问三种对话框的 Default/Esc 语义各写一遍；
- composer 两个实现（会话/首页）行为漂移。

入口组件的职责：**锁定语义/状态/骨架/焦点规则/token，开放内容 slot**。

### 0.2 入口组件硬原则

1. **薄组合**：只组合官方 mygo 控件 + design token，不手绘控件；不覆盖官方状态色（hover/pressed/disabled 由 mygo 提供）。
2. **唯一出口**：场景内只能调用入口函数；入口内部才允许直接编排官方件。
3. **状态归入口，数据归业务**：入口持有视觉/交互状态（折叠、焦点、动画），业务持有数据与业务状态；入口不接受"任意 UI 片段列表"来绕过语义。
4. **默认安全**：审批/计划审阅无 Default；危险确认只让安全动作（取消）Default 承接 Enter，危险动作永不为 Default；Esc/外部点击语义写死并可见。
5. **可访问**：role/name/state/播报由入口统一设置；业务只需传 label 文案。
6. **可测**：每个入口有帧测试 + 快照；迁移时以现有测试为回归保护网。

### 0.3 surface 总览

| # | surface | 目标入口 | 当前实现（2026-11-09） | 状态 |
|---|---|---|---|---|
| S1 | 会话线程 | `uikit.SessionThread` | `internal/uikit/session.go`（Phase 3a；session 仅留 Row/Empty slot） | 已有 |
| S2 | 推理展示 | `uikit.ReasoningTrace` | `internal/uikit/reasoning.go`（Phase 3a） | 已有 |
| S3 | 工具调用 | `uikit.ToolRow` + `uikit.ToolCallCard` | `internal/uikit/{toolrow,toolcall}.go`；toolcards 只留注册（Phase 2c） | 已有 |
| S4 | 审批 | `uikit.ApprovalDialog` | `internal/uikit/approval.go`；approval 只留队列（Phase 3b） | 已有 |
| S5 | 计划审阅 | `uikit.PlanReviewDialog` | `internal/uikit/planreview.go`；interaction 只留 FIFO（Phase 3b） | 已有 |
| S6 | 用户提问 | `uikit.AskDialog` | `internal/uikit/ask.go`；interaction 只留 FIFO（Phase 3b） | 已有 |
| S7 | Composer | `uikit.ComposerShell` | `internal/uikit/composer.go`；session/home 只留 toolbar/提交 slot（Phase 3c） | 已有 |
| S8 | 运行态/流式/恢复 | `uikit.RunStatus` | `internal/uikit/runstatus.go`（Phase 3c） | 已有 |
| S9 | 多 Agent / 团队 | 会话 Agent Run Band（`session` 包） | `internal/ui/session/agentsurface.go`（Run Band + Popover 团队/子代理/任务 + 主栏子会话视图）；团队/子代理零值常显；旧 `uikit.TeamSurface` 已删除 | 已有（2026-11-11 Run Band） |
| S10 | 会话上下文账本 | 会话「上下文 / Context」页签（`session` 包） | `internal/ui/session/context_pane.go` + `internal/contracts/contextinspect.go`（agent 折叠 `ContextInspector`） | 已有（2026-11-14） |
| S11 | 跨会话洞察 | 一级侧栏「洞察 / Insights」screen | `internal/ui/insights/` + `internal/modules/insights/` + `internal/contracts/insights.go` | 已有（2026-11-15） |

> 错误展示是横跨所有 surface 的原语：`uikit.ErrorLine` 已有（RoleAlert + 可选中 + DangerText），大块错误用 `uikit.ErrorPanel`（待建，见 §2.6）。

---

## 1. 表面规格（逐个）

### S1 SessionThread（会话线程）

**职责**：把 `[]ChatMessage` 渲染成可滚动、可流式跟随、可键盘/读屏访问的会话线程；承载消息体、推理、工具行、过程活动带与元信息行（时间/紧凑用量/单一 ⋯ 动作）。

**数据模型**：`messages []contracts.ChatMessage`（`ID/Role/Text/Reasoning/Streaming/Error/ToolCalls/Feedback/CreatedAt/Usage`）、`running bool`、会话切换状态。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| empty | `List.Children` 渲染首屏 hero（不显示空白滚动区）；hero 同时给出首启检查清单（缺模型/API Key/未打开工作区 → 各自深链），因为 Home 已不是导航目的地（fix wave 1 / FR-07）。hero 的「新对话」在已是空会话时只聚焦 composer，不重复建会话；真新建时用空标题以保留 auto-title（FR-30） |
| streaming | 最后一行原地增长；`FollowEnd` 贴底；元信息行隐藏、过程活动带保持展开；正文用 `markdown.Stream` |
| scrolled-up | 停止跟随；浮层「回到最新」（`ScrollToEnd`） |
| fold-toggle | `ListState.HoldPlace()`，折叠行不跳动 |
| session-switch | 重置 `ListState` / 展开状态 / 用量 / 滚动 |
| error | 失败轮显示错误行 + 重试；元信息行只保留时间/用量，不提供 ⋯ 动作。`ErrorKind=iterations` 显示「已达最大迭代次数」+ 新建会话建议（上限为装配级产品定义，默认不限，无用户设置入口；不再以 rig 英文串为标题）；`ErrorKind=loop` 为历史遗留（循环保护已移除），显示中性文案「该回合曾被旧版循环保护停止」+ muted 原始诊断；`ErrChatCompacting`/`ErrCompactConflict` 在发送失败时本地化（`会话正在压缩，请稍后再试` / `压缩期间历史被修改，已取消`） |
| truncated | 最后一条 assistant 的 `Truncated=true` 时在回复末尾显示可折叠「回答被截断」提示（展开说明输出上限与两种处理方式）；非最后一条不显示 |

**会话壳层（B3 顶栏 + composer dock，2026-11-10，DSH 对齐；2026-11-11 Agent Run Band）**：标题行顺序固定为 **标题/面包屑 → Agent Run Band（运行中 → 智能体团队 → 子智能体 N → 后台任务 → 待审批）→ spacer → ⋯**（`screen.header` / `screen.actions` / `screen.tabs` 槽位，40px 标题行 + 32px Tabs 行，浅色 header 面、仅底边一条 hairline、行间无线、无返回按钮、无 `Alt+←` 历史回退）。Band 是标题后的**一行原生入口按钮**（不再外套第二层容器：官方按钮自带描边，外套容器会形成"控件套控件"的双重描边；只用间距分组），不是卡片；团队/子代理是关键功能入口，**零值也常显**但**零值不显示数字 0**（tooltip/无障碍名仍带精确计数），后台任务与待审批只在有内容时出现；只有团队/子代理后端都不存在且无运行/任务/审批时整条不渲染。三个 agent 入口都是 `ui.PopoverBase` 浮层（团队：成员 + 共享任务；子智能体：树/列表切换；后台任务：状态/命令/用时/停止），不占右栏。进入子智能体或团队成员时，主栏切到子会话：标题变成 `主标题 › 子智能体名` 面包屑，点主标题返回父会话；一次性子智能体只读，可继续成员有输入框（`ChildAgentService.SendChildAgent`）。回合尾部新增文件卡：「交付文件」（write_file）与「已编辑 N 个文件」（edit_file/str_replace_editor），每行「在侧边栏预览 / 查看改动 / 用系统应用打开」，预览打开右栏文件标签。目标条（`uikit.StatusBadge` 阶段徽标——目标进行中 / 等待继续 / 目标已暂停 / 目标受阻 / 目标已完成 + 目标正文 + 自动续跑轮次 `n/cap` + 需要人类继续时的「继续目标」按钮 + ⋯「目标操作」菜单：暂停/继续/完成/编辑/清除；blocked 时第二行显示人类可读原因、tooltip 保留稳定 code）与审批内联卡在 composer 上方 dock（阅读列宽度居中，`session.banner` 槽），不占 transcript 空间、不遮挡输入。目标读取 `contracts.KeyGoal` 的 per-session durable GoalService，切会话只显示该会话目标；续跑由 agent 后端驱动，引擎注入的 goal round 在 transcript 以「目标续跑 · 第 n/cap 轮」回执行呈现，不伪装成用户气泡。通知/ toast 固定在右上角、B3 顶栏下方（最多 2 条，自上而下堆叠），不遮挡标题/Tabs/顶栏动作。右栏是默认收起的多标签页面：开始引导（单个工作区文件卡片，无说明文字）+ 文件 + 每个打开文件一个可关闭标签；页面内容一律可滚动。侧边栏终端/命令页已于 0.2.41 删除。

**目标 API 草案**：

```go
type SessionThreadOptions struct {
    Messages   []contracts.ChatMessage
    Running    bool
    Empty      func(c *ui.Context) // 首屏 hero
    State      *ui.ListState       // Key/Label/FollowEnd 由入口设置
    OnRetry    func()
    OnCopy     func(messageID string)
    OnFeedback func(messageID, feedback string)
    Markdown   *markdown.Options
}

func SessionThread(c *ui.Context, o SessionThreadOptions)
```

**最终实现（Phase 3a）**：`uikit.SessionThread`（`internal/uikit/session.go`）——`Row`/`Empty` slot，`State`/`FollowEnd`/`LaidOut`/`HoldPlace`/`Error`/`OnRetry`/`Markdown`/`Locale`；消息体（含折叠过程带与元信息行）仍属消息体 `Row` slot（streaming/失败态的元信息行隐藏由业务实现，入口不重复渲染动作）。

**布局与层级**：阅读列 `design.TranscriptWidth`（960，`MaxWidthPercent(92)`）居中；消息间距 `design.Space5`；正文优先，工具行弱化；页内不叠卡片。

**交互/键盘/焦点**：List 行可选/可聚焦；Enter 展开折叠的推理/工具/过程详情；上下键滚动；动作收敛为消息元信息行上的一个 ⋯ 菜单（复制消息/重新生成/用量明细）；元信息行（时间 + 紧凑用量）在助手消息顶部，折叠过程摘要与元信息共用一行；最新回复正文底部另有常显「复制」按钮；`ui.NewTester` 可断言 `RoleList`/`RoleListItem` 行名与菜单项。

**a11y**：List `Label`（"对话消息"）；行 `Label` 只用「角色+序号」（避免 Markdown 源文本进 a11y 树）；流式结束 `c.Announce`；滚动浮层按钮有 Label。

**禁止**：`Scroll`+循环构建消息；手写"比较 scrollHeight"贴底；每帧重建全部消息；给每条消息套卡片；消息动作常驻三个及以上按钮或独占 32px 动作行（必须收进元信息行的单一 ⋯）；用量以原始长数字展示（必须紧凑单位 K/M/B）。

**工作步骤展示（2026-11-10，DSH 对齐）**：transcript 的过程显示由两个设置控制——`ui.workSteps.mode`（简洁 compact / **标准 standard（默认）** / 详细 detailed / 完全展开 verbose）与 `ui.workSteps.collapse`（**回答结束后 completion（默认）** / 下次有新消息时 next-input）。运行中：标准/简洁只渲染一行原地更新的工作步骤（活动图标 + 状态词 + 标准档的实时细节 + 用时；状态词按工具族：正在分析请求/正在读取文件/正在运行命令/正在搜索网页/正在协调子智能体/等待你的操作…），不把每个工具行铺进 transcript；详细/完全展开保留完整实时时间线。完成后：简洁/标准/详细把可折叠回合收成一条摘要（有工具或有思考才折叠），完全展开永不折叠、不分组并直接显示完整时间线；next-input 时当前最新一轮完成后保持展开，下一条用户消息到来后回落折叠，用户手动展开/折叠的选择优先。设置入口在「设置 → 行为 → 工作步骤」；旧值/空值按标准 + 回答结束后处理。

**同一回合多步过程（2026-11-10）**：连续的、已结束的助手步骤（每一步带工具调用、无错误）合并为**一条**过程摘要行——「过程 · 思考 · 合计工具数」，默认折叠；折叠时只显示**最终结果**（最后一步的回复与决策卡），中间步骤的回复/工具行收进折叠区；展开恢复每一步的完整时间线。窗口行按回合分组（`uikit.SessionThread.SameTurn/GroupRow`），a11y 行名用步骤范围（如「助手 1–3」）。最后一条已结束回复的正文底部有一个「复制」按钮（复制该回复文本，与 ⋯ 的「复制消息」一致）。

**当前实现**：`internal/ui/session/view.go` `buildChatPane`（List + ListState + FollowEnd + HoldPlace）与 `buildAssistantMessage`/`buildMessage`/过程折叠/元信息行。过程带：思考与非决策工具行以 16px 标记列（sparkles/工具分类图标/运行中 Spinner）+ 动作名 + 状态 + `·` 预览 + 内联 chevron 组成的一条缩进行距活动带；完成且过程 ≥2 行时折叠为一行摘要（`过程 · 思考 · N 个工具调用 · 用时`，流式展开），展开恢复完整时间线；决策卡（ask_user/plan_review/exit_plan_mode）不受折叠影响。帧测试：`internal/ui/session/transcript_window_test.go:32`（虚拟化）、`scroll_follow_test.go:35,65`（跟随）、`interaction_batch_a_test.go:123`（streaming 隐藏动作）、`chrome_b3_test.go`（B3 顶栏/⋯ 会话操作/目标 dock/审批 banner 槽/Esc 审批优先）。

**落地补充（2026-11-15 收尾）**：空会话 hero 在已有空会话时主按钮「开始输入/Start typing」只聚焦 composer，另给 3 个示例 prompt（点击仅填草稿）；从洞察/自动化/能力进入会话时页头 `‹ 来源屏` 走 `nav.Back()`，非会话屏不再双高亮活动会话行；恢复会话从 durable `user_message`/`run_end` 回填 user 与 turn 最终 assistant 时间（压缩/rewind 不错配、不伪造），无时钟且无 usage 的恢复回合不渲染孤立 ⋯。子代理完成回执行尾「打开子会话」；会话 ⋯ 菜单在无目标时给「设置目标」、有目标时「编辑目标」；冷恢复空草稿发送按钮为 DisabledPrimary；活动行 kind 词不重复。窗口标题/托盘 tooltip 显示 `运行中 N` / `等待审批`（`Root.Activity()`，Chat/Approvals/语言事件刷新）。

**0.2.40 DSH 客户端对齐补充**：composer 上方 Todo dock（只读待办，进行中/待处理/已完成，空态隐藏；当前 store 仍为进程级）；消息/暂存图片点击打开原生 Modal 原图画廊（多图翻页、Esc/背景关闭、历史无字节诚实提示）；会话 ⋯「创建提醒/本会话提醒」；⋯「导出原始日志（ZIP）」；轨迹页「已知耗时/最慢工具」与每卡「用时」（未知显示 —）。

**查找与轨迹定位（2026-11-15）**：Cmd/Ctrl+F 打开官方 `ui.FindBar`（自动切到「对话」；子会话忽略），匹配每条消息的正文+推理（大小写不敏感，每条消息计 1 次），显示计数并用 `uikit.BuildSessionRowMap` 把消息映射到 transcript 分组行后 `ScrollIntoView`；Enter/Shift+Enter/上一/下一/Done/Esc 走官方控件；Esc 分层——字段自己消费，运行中 `interruptEsc` 先关 FindBar 且不停止 run，空闲时仅 findOpen 注册 screen Esc 并让位 slash 菜单；关闭保留 query/current，重开回到当前匹配，键盘交还 composer。轨迹页：`trajList.FollowEnd` 跟随流式新调用；计数行在 `uikit.ReadingColumn` 与卡片同轴；每卡「在对话中查看」（可及名「在对话中定位 <工具>」），方向键只移动选中、Enter/双击才切回对话并滚动到该消息行。

**分段交错时间线（2026-11 审计 US-01/US-02，A/C/F 波）**：live 读模型不再把多轮思考拼成单 string——`contracts.ChatMessage.ReasoningSegments []ReasoningSegment{Text, Iteration, TextOffset}` 按模型迭代保留思考分段（`Reasoning` 平铺字段保留兼容），复用 `ToolCall.TextOffset` 机制：`TextOffset` 是该迭代思考到达时助手累计正文的字节长度。`session.mergeTimelineEvents` 把思考分段与工具调用按偏移合并为一条时间线，**思考↔工具↔正文交错渲染**（同偏移交替约定；已知限制：同迭代多并行工具全偏移相等时排序近似，完全精确需 `ToolCall.Iteration`，记入审计最终报告限制）。**live/restored 同构**：重启后从 rig 历史逐轮恢复的分段与 live 呈现一致（US-02 关闭）。standard/compact 摘要行在分段 >1 时显示「N 段思考」（`processSummaryParts`）；detailed/verbose 展开完整交错时间线。帧测试：`internal/ui/session` 的 US-01 live/restored 集成测试与 `trajectory_content_test.go`。

**工作步骤思考态与时态（2026-11 审计 US-03）**：standard/compact 单行按真实阶段取词（`liveWorkStepPhase`）——有运行中工具＝该工具族的进行时词（正在读取文件/正在运行命令…）+ 该调用摘要；最后一个工具完成后正文未到＝「正在思考」（standard 档附当前迭代思考的一行预览，≤96 rune）；正文已到＝「正在回复」；无任何事件＝「正在分析请求」。**工具完成后不再滞留进行时词**：工具行状态词走 `uikit.StatusText` 完成时态（已完成/Done），回合结束后收进过程摘要。

**轨迹含思考与正文（2026-11 审计 US-04）**：轨迹 tab 不再是「只有工具调用」——每回合按时间线渲染思考折叠行（「思考」disclosure，分段计数）+ 工具卡 + 正文片段（「正文」），与对话页共用 `mergeTimelineEvents`。决定性断言：`TestTrajectoryIncludesReasoningAndBody`（两轮思考行 + 正文 + 工具卡）。

**运行态播报（2026-11 审计 UX-19）**：流式 settle 后 `c.Announce`「回答已完成/Reply finished」；审批到达 `c.Announce`「需要审批：工具调用等待你的决定」（去重：同一最旧请求只播一次）。standard/compact 的活动行是 `Role(ui.RoleStatus)`  live region（detailed/verbose 由 `uikit.RunStatus` 的 RoleStatus 行承担），读屏随阶段朗读当前状态词。

**dock 高度预算（2026-11 审计 UX-02/US-12）**：composer 上方 dock（目标条 + 审批内联卡 + 子会话 banner）有总高度预算 `composerDockMaxHeight = design.Space8 * 10`（320 DIP），超出部分 dock 内滚动（`TrackScroll`），**决策按钮在 900×600 窗口可达**；几何帧测试钉住 input.bottom ≤ 预算（修复前 1036/974 → 修复后 507/允许 395，transcript ≈40 DIP 与 demo 同量级）。

**右栏文件树 Outline（2026-11 审计 UD-14）**：右栏文件页工作区树用官方 `ui.Outline`（懒加载缓存，`filesOutline`），不再 `ui.Tree` + 400 cap——第 401 项可达；侧栏工作区树同构。

**迁移步骤**：① 把 `buildChatPane` 的 List 配置/空态/回到最新抽成 `uikit.SessionThread`；② 消息体渲染保留为可插拔 `func(*ui.Context)`（业务 slot）；③ 用现有 transcript/scroll 测试作回归；④ 新增快照 `uikit-session-thread.png`。

**验收**：`TestSessionThreadVirtualizesLongConversation`、`TestSessionThreadFollowAndBackToLatest`、`TestSessionThreadDefaultRows`（uikit）；流式隐藏动作由业务 Row 实现，由 session `TestStreamingMessageHidesActions` 覆盖；+ 快照。

---

### S2 ReasoningTrace（推理 / 思维链）

**职责**：把模型推理作为**次级折叠内容**展示：默认不抢主视觉；streaming 时展开；结束后收起为一行摘要。

**数据模型**：`reasoning string`（兼容平铺）、**`segments []contracts.ReasoningSegment{Text, Iteration, TextOffset}`（2026-11 审计 US-01 修订：单 string 规格编码了 live 折叠缺陷，已废弃为唯一来源）**、`streaming bool`、`open bool`（按 message ID 持久化）。渲染优先用分段：多段思考按迭代折叠/展开，与工具调用按 TextOffset 交错（S1 时间线）；旧消息无分段时回退平铺 `Reasoning` 单块。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| streaming | 展开；Spinner + 「思考中…」；正文 `markdown.Stream`（muted 主题） |
| done | 收起；Sparkles + 「思考」+ 行数；点击/Enter 展开 |
| empty | 不渲染任何占位 |
| toggle | `HoldPlace`，不推动下方内容 |
| error | 推理不显示错误（错误归 ErrorPanel），可折叠保留已生成部分 |

**目标 API 草案**：

```go
func ReasoningTrace(c *ui.Context, o ReasoningTraceOptions)
type ReasoningTraceOptions struct {
    Reasoning string
    Streaming bool
    Open      *bool
    OnToggle  func(open bool)
}
```

**最终实现（Phase 3a）**：`uikit.ReasoningTrace`（`internal/uikit/reasoning.go`）——`Key`/`Reasoning`/`Streaming`/`Open`/`OnToggle`/`HoldPlace`/`Markdown`/`Locale`；muted 主题经 `uikit.ReasoningMarkdown`。

**布局**：触发行 = 图标 + 文案 + 行数 + chevron；面板左缩进对齐正文，muted 字号（`design.TypeXXS/XS`）；不使用卡片、不加 glow/阴影。

**交互**：Enter/Space 展开；`Changed()` 回写；不抢滚动（`HoldPlace`）；不与正文重复渲染。

**a11y**：trigger `Label`（思考中…/思考）；面板内容可被读屏按需展开；流式完成 `c.Announce`。

**禁止**：大 Card 把 CoT 渲染成正文；默认展开（除 streaming）；把推理拼进消息正文；每帧重建 trigger。

**当前实现**：`internal/ui/session/view.go` `buildReasoningDisclosure`（`ui.CollapsibleBase` + muted markdown + Spinner + 行数 + `thinkOpen`）；分段来源 `reasoningSegmentsOrFallback`（live 读模型 `ReasoningSegments`，restored 从 rig 历史逐轮恢复，二者同构）。

**迁移步骤**：抽函数 → `uikit.ReasoningTrace`；保留 muted Markdown options；新增帧测试覆盖 streaming 展开/结束后收起/`HoldPlace`。

**验收**：`TestReasoningTraceExpandsWhileThinking`、`TestReasoningTraceCollapsesAfterStreaming` + 快照 `uikit-reasoning.png`。

---

### S3 ToolRow / ToolCallCard（工具调用）

**职责**：工具调用是 Agent 行动的外化，必须有稳定解剖：类别图标、友好名、状态机、摘要预览、可复制的输入/输出段、团队跳转；**决策类工具**（计划审阅/exit plan/ask）用完整卡。

**数据模型**：`contracts.ToolCall{ID,Name,Status,Summary,Arguments,Output,TextOffset,...}`；渲染状态 `open`（按 call key 持久化）；`PanelSections`/`TeamToolFooter`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| running/in_progress/active | `ui.Spinner` + WarningText 状态词；不可复制结果 |
| success/ok/done | SuccessText 状态；详情默认折叠（长结果） |
| error/failed | DangerText；**默认展开**（失败可读） |
| cancelled | LabelTertiary（中性，不画失败） |
| unknown | LabelTertiary；未知非空状态原样显示 |
| no-sections | 退化为安静单行（无展开箭头） |

**目标 API 草案**：

```go
type ToolCallCardOptions struct {
    Key      string
    Call     contracts.ToolCall
    Sections []uikit.ToolPanelSection
    Footer   func(c *ui.Context) // 跨模块导航（团队/任务板）
    Open     *bool
    OnToggle func(open bool)
}
func ToolCallCard(c *ui.Context, o ToolCallCardOptions)

// 紧凑行（transcript 普通工具调用）
func ToolRow(c *ui.Context, o uikit.ToolRowOptions) // 已有
```

**布局**：紧凑行（图标/名/状态/单行预览/chevron）用于普通调用；预览由 `toolcards.Preview` 的本地化模板按工具名从 arguments 生成（team_task_*/job_*/terminal_*/web_*/file/shell 等），未知工具或字段缺失回退后端 summary，统一单行+96 runes 截断；决策类工具在 transcript 用完整卡（图标+名+状态+步骤+详情）；轨迹 pane 复用同一卡。详情段统一：输入（命令/路径/前后内容/JSON）→ 输出；可复制；mono 8 行上限 + 截断提示。团队类工具不再提供跨模块 footer：团队/子代理从会话头部入口进入，工具卡只保留输入/输出与状态。

**交互/键盘**：trigger 可聚焦，Enter/Space 展开；hover/focus 背景 `BgHover`；详情内复制按钮可 Tab 到；状态变化自动更新但**不移动已展开区域**。

**a11y**：trigger `Label = 名称 · 状态`；spinner `Label`；状态文本不单靠颜色；详情区域可读。

**禁止**：塞进消息气泡；纯 `ui.Text` 渲染工具结果；另起一套工具卡解剖（现存量 4 条路径）；状态色当正文（用 `*Text`）；每帧重算状态映射（收敛到 `uikit.StatusText`）。

**当前实现**：`internal/uikit/toolrow.go`（ToolRow 唯一解剖）；`internal/ui/toolcards/toolcards.go`（注册渲染 + 状态词表 + PanelSections + TeamToolFooter + plan_review 专用卡）；`internal/ui/session/view.go` fallback 与决策卡（`buildToolCard`）。测试：`internal/uikit/toolrow_test.go:49,90,107`；toolcards 全包测试。

**迁移步骤**：① 把 session 的 `buildToolCard` 卡头改为组合 `uikit.ToolRow`/`ToolCallCard`；② 决策卡/plan_review 卡收敛进 `ToolCallCard`；③ 状态函数收敛到 `uikit.StatusText/StatusBadge`；④ 4 条路径减到 2 条（紧凑行 + 完整卡）；⑤ 保持 toolcards 注册机制不变（只换渲染器内部）。

**验收**：`TestToolRowStatusStates`、`TestToolCallCardFailureDefaultOpen`、`TestToolRowDegradesWithoutSections` + 快照 `uikit-toolrow.png`/`uikit-toolcard.png`。

---

### S4 ApprovalDialog（审批 / 中断）

**职责**：工具调用前的安全决策。必须让用户看清"要执行什么、影响是什么、三个选择分别意味着什么"，且**不能让一次误触 Enter 就放行/拒绝**。

**数据模型**：`contracts.ApprovalRequest{ID,Tool,Summary,Arguments,SessionID,Source,...}`、`Approvals.Pending()` 队列、`ApprovalDecision` 三值（deny/allow/allow-always）。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| pending（最旧一条） | 输入框上方（composer dock）出现内联审批卡；无 Default/自动焦点；队列计数显示「还有 N 条待审批」；批准前 agent 轮保持运行态 |
| allowed / denied / allow-always | 卡片消失并 resolve；下一请求出现 |
| Esc（无文本焦点/审批优先） | 视为 **deny 最早一条**，不中断本轮（第二次 Esc 才中断生成）；session 拥有该语义 |
| 非会话屏收到请求 | 自动切回会话屏（`Navigator.Push("session")`），审批卡可见 |
| error | 保持可见错误；不静默重排 |
| multi-pending | 一次只显示一条内联卡，不叠层 |
| provenance | 请求带 `SessionID`/`Source` 时，卡体首行显示「来自会话：<标题> · <来源（用户/自动化）>」（标题查 `ChatService.Session`，查不到回退 ID）；请求来自**其他**会话时该行带「打开该会话」动作（切会话 + 进会话屏）。无 `SessionID` 的旧后端保持原卡（FR-29） |

**目标 API 草案**：

```go
type ApprovalDialogOptions struct {
    Request  contracts.ApprovalRequest
    Pending  int
    Labels   ApprovalLabels // 允许/始终允许/拒绝/影响声明文案
    OnResolve func(contracts.ApprovalDecision)
    Open     *bool
}
func ApprovalDialog(c *ui.Context, o ApprovalDialogOptions)
```

**布局（固定，2026-11-18 视觉语言 v2）**：内联卡 = **中性卡体**（`BgRaised` + 1px `BorderL3` + `RadiusLG(12)`）+ **左缘 2px `Warning` 竖条**（不圆角，全卡唯一语义色块）→ 头部：`uikit.StatusBadge("需要审批")` + 工具友好名 14/500 + 右侧来源行（12 tertiary，跨会话时带「打开该会话」动作）+ 队列计数 → 一行影响声明（12/18 `WarningText`）→ 目标块（shell/终端类标「命令」、write/edit 标「路径」，mono 13/20 + `BgStrong` + `RadiusSM` + 1px `BorderL1`）→ 「将改动」（`uikit.DiffPreview`，块底白、仅增删行染色）→ 「原始参数」折叠（`Collapsible`）→ footer：`范围`（12 tertiary + 菜单：仅此一次 / 本会话内始终允许 `前缀` / 总是允许（写入设置））· **拒绝**（次）· **允许一次**（Primary，同时是全屏唯一 accent 填充）· `⋯`（更多规则）；`Esc`/`⌘↵` 用 `uikit.Kbd`+`FormatShortcut` 画在 footer 左侧；无 Default、无 AutoFocus。**不再有满宽警示横幅与整卡警示描边**（A2 F-01：满宽色带会让审批卡变成第二个主色）。

**交互/键盘**：Tab 循环；Enter 只有在焦点落到某按钮时才触发该按钮；Esc = deny 最早一条（有文本焦点时也由 session 的审批优先路径处理，且不中断本轮）；内联模式没有 backdrop。不设默认焦点。

**a11y**：卡片是命名 region（「需要审批 · <工具>」）；危险图标不只靠颜色；影响声明可读；状态词明确。

**禁止**：默认焦点落在允许或拒绝（真实事故：发消息后 Enter 误触拒绝）；普通 Button + 中性文案；把审批塞进 `AlertDialog`（末位默认语义）；把队列叠成多层 Modal；审批动作图标化无文字。

**当前实现**：`uikit.ApprovalDialog`（`Inline` 模式为产品默认的内联卡；`Inline=false` 保留 Modal 渲染给 embedder/兼容；「将改动」由 `uikit.DiffPreview` 渲染）；`internal/ui/approval/approval.go` 注册 `session.banner` 槽、维护 FIFO/settle 状态并在非会话屏自动切回会话。测试：`internal/ui/approval/*_test.go`（inline 渲染/队列计数/三种决定/自动切屏）、`internal/uikit/approval_test.go`、`internal/ui/session/chrome_b3_test.go`（Esc 审批优先）。

**迁移步骤**：抽 `uikit.ApprovalDialog`（保留当前行为逐字一致：footer 顺序、无 Default、Esc=deny、影响声明、队列计数）；新增帧测试断言"打开后无焦点按钮/Enter 不产生决定"。

**验收**：`TestApprovalFooterHasNoDefaultAction`、`TestApprovalEscapeDenies`、`TestApprovalBackdropClickDenies` + 快照 `approval.png`（light/dark）。

---

### S5 PlanReviewDialog（计划审阅）

**职责**：Agent 提交计划后，让用户**批准 / 继续规划（带反馈）/ 取消计划**。核心语义：**Esc 不取消计划**（取消是不可逆决定，必须显式点击）。

**数据模型**：`contracts.PlanReview{Content,State}`、`PlanReviewer`（SetPlanHandler/Exit）、`planFeedback string`、`planErr`、`planEscHint`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| reviewing | Modal；plan Markdown 可滚动；反馈输入框；无 Default |
| approve | 显式按钮（Primary 最右）；成功后关闭 |
| keep-planning | 有反馈才可用；Enter=发送反馈并保持审阅；失败保留对话框可重试 |
| cancel | 「取消计划」显式按钮；Esc **不**取消 |
| Esc 首次 | 不关闭；显示一次性 `WarningText` + `RoleAlert` 提示「Esc 不能关闭…」 |
| error | 显示错误；允许 Esc 关闭（避免卡死）；不要重复入队 |
| approved/cancelled | 出队；不重复显示 |

**目标 API 草案**：

```go
type PlanReviewOptions struct {
    Plan       contracts.PlanReview
    Feedback   *string
    Footer     []uikit.DialogAction // 由入口按序生成：取消计划 → 继续规划 → 批准
    OnApprove  func()
    OnKeep     func(feedback string)
    OnCancel   func()
    OnDismiss  func() // Esc（仅 error 态允许）
}
func PlanReviewDialog(c *ui.Context, o PlanReviewOptions)
```

**布局**：标题（Activity + 计划待审阅）→ `Scroll MaxHeight(Space6*12)` 的 `markdown.View` → 反馈 `TextInput`（Label）→ 错误行 → Divider → footer（取消计划 → 继续规划[无反馈 Disabled] → 批准[Primary 最右]）→ 提示行（Enter 语义随反馈状态变化）。**无 Default**。

**交互/键盘**：反馈输入框 Enter=继续规划（仅非空）；空反馈不批准；Esc 首次只提示；批准/取消必须点击或 Tab+Enter。

**a11y**：提示用 `RoleAlert`；反馈输入框有 Label；计划正文是 Markdown（读屏可读）。

**禁止**：Esc 静默取消整份计划；空反馈 Enter 触发批准；自拼 footer 顺序；把"批准"做成默认按钮。

**当前实现**：`internal/ui/interaction/interaction.go` `planPanel`（含 Esc 警告、无 Default、footer 顺序、keepPlanning）。测试：`interaction/plan_loop_test.go:150-320`（含 `:289 TestPlanEscapeShowsVisibleHint`）、`interaction_test.go`。

**迁移步骤**：抽为 `uikit.PlanReviewDialog`；保留消息队列（FIFO 一次一条）；帧测试覆盖 Esc 不取消/提示/空反馈不批准/失败可重试。

**验收**：`TestPlanReviewEscapeDoesNotCancel`、`TestPlanReviewEmptyFeedbackNeverApproves`、`TestPlanReviewKeepPlanningSendsFirst` + 快照 `plan.png`。

---

### S6 AskDialog（用户提问）

**职责**：Agent 向用户提问（选项或自由文本），答案回传为结构化回答。安全语义：**误按 Enter 不得替用户选一个没选的选项**。

**数据模型**：`contracts.UserQuestion{ID,Text,Options}`、`otherOpen bool`、`answer string`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| options | 每个选项一条**全宽等权** SecondaryButton（不排主次）；尾部「其他…」 |
| other-open | 「其他…」展开 AutoFocus TextInput；Enter 提交；Esc 收回选项（不关问题） |
| free-text | TextInput + Enter 提交；空回答禁用提交 |
| cancel | footer「取消」为 Default（选项态）；Esc/外部点击=取消 |
| error | 错误行显示，不关闭问题 |

**目标 API 草案**：

```go
type AskDialogOptions struct {
    Question  contracts.UserQuestion
    Answer    *string
    OtherOpen *bool
    OnAnswer  func(answer string)
    OnCancel  func()
}
func AskDialog(c *ui.Context, o AskDialogOptions)
```

**布局**：标题（Sparkles + 需要你的确认）→ 问题正文 → 选项列 + 「其他…」/输入框 → Divider → footer（选项态只有「取消」且 `Default: !otherOpen`；自由文本态：取消 → 提交[Primary，空文本 Disabled]）→ 提示行（Tab 选择/Enter 语义）。

**禁止**：Enter 默认选中第一个选项；选项排主次（Primary/Accent）；空回答提交；Esc 在「其他…」打开时关闭整个问题。

**当前实现**：`internal/ui/interaction/interaction.go` `questionPanel`。测试：`interaction_test.go:395,419,441,475`。

**迁移步骤**：抽 `uikit.AskDialog`；保留与 plan/present 共用的 FIFO 队列；帧测试断言"打开后 Enter 取消而非选第一项"。

**验收**：`TestAskDialogStrayEnterCancels`、`TestAskDialogOtherEscapeCollapses`、`TestAskDialogFreeTextRequiresNonEmpty` + 快照 `ask.png`。

---

### S7 Composer（输入 / 模型 / 权限 / 附件 / 发送）

**职责**：用户与 Agent 交互的主入口。统一两个 composer（会话页 / 首页）的解剖，避免同一动作两种外观；保持"流式期间可继续输入 + 停止/排队"的安全语义。

**数据模型**：`draft string`、`attachments []Attachment`、`running bool`、`queue []string`、权限选择、模型与推理等级（`modelcatalog.models` 的模型列表与 `DefaultThinking`）、会话×模型推理等级覆盖（`session.<id>.thinking.<modelID>`，默认跟随该模型默认值；旧 `session.<id>.thinking`/`session.<id>.effort` 仅作旧会话兼容回落，任何一次显式选择都会清掉旧行）、`focusComposer`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| idle | 输入 + 工具条 + 发送（Primary）；未配置模型时发送禁用并在 composer 内显示原因 + 「打开模型设置」（FR-31） |
| running | 输入仍可编辑；发送位变「排队」+「停止」（仅后端实现 `contracts.PromptQueue` 时；发送先 `Send`，running 冲突/预读 running 时 `EnqueuePrompt`）；Esc 中断（菜单优先）；提示文案说明 Enter 语义 |
| queueing-off | Enter 换行并提示草稿保留 |
| queue-error | 显示错误行 + 重试 |
| attachments | chips + 移除；排队只发文字提示 |
| slash/@ menu | 建议菜单优先吃 ↑↓/Enter/Tab/Esc；不抢焦点 |
| disabled（无模型） | 发送禁用（鼠标与 Enter 均不发） + 「尚未配置模型：发送已禁用」 + 「打开模型设置」深链；无 API Key 仅警告（本地模型可忽略） |

**目标 API 草案**：

```go
type ComposerOptions struct {
    Draft       *string
    Running     bool
    Queue       []string
    Attachments func(c *ui.Context)
    Toolbar     func(c *ui.Context) // 附件/模型/权限/推理
    Send        func()
    Stop        func()
    OnKey       func(ui.TextKey) bool
    Placeholder string
    Focus       *bool
    Error       string
}
func ComposerShell(c *ui.Context, o ComposerOptions)
```

**布局**：composer 上方 dock（阅读列宽度居中、`session.banner` 槽）：目标条（有目标时）→ 审批内联卡（有 pending 时）→ 卡片 `t.Composer()`；输入 `ui.TextArea`（MinHeight Space9、MaxHeight Space8*6、边框透明、聚焦 accent 边）；工具条一行：`uikit.ComposerToolbar`（官方 `ui.Toolbar`，默认无边框、hover 才显底色，单 Tab stop、左右键）包住附件按钮 + 模型菜单 + 权限按钮；附件按钮 `uikit.ComposerAttachButton` 直连原生文件选择器（无「手动输入路径…」菜单/对话框，拖入/粘贴保留）；窄宽度由官方 Toolbar 折叠低频控件进 overflow；模型 chip 始终构建（不再折进权限菜单），权限按钮本身显示当前权限（工作区修改 / 所有权限 / 只读），点击只打开权限三态菜单；右侧发送/停止固定，**不进溢出菜单**；键位提示在宽 composer 显示、窄 composer 放 tooltip。模型 chip 打开**两级紧凑菜单**：一级只有「模型」「推理等级」两行（Trailing 当前值）+「打开模型设置…」；catalog 已注册但列表为空、而 `Config().Model` 非空时（装配默认模型，FR-26），chip 文案为「<model>（默认模型，尚未配置连接）」并在菜单顶部加一行「默认模型来自装配，添加模型行后可覆盖」；「模型」二级按服务商分组（`MatchProviderPreset`，未匹配“自定义服务”）列出模型；「推理等级」二级为「跟随模型默认（…）」+ 该模型模式；模型级 `DefaultThinking` 在 Home composer 的「推理等级」与设置页编辑弹窗配置。

**交互/键盘**：Enter 发送、Shift+Enter 换行、Cmd+Enter 发送；运行中 Esc 中断；slash/@ 菜单优先级最高；Tab 顺序：输入 → 菜单项 → 发送 → 停止。

**a11y**：输入有 Label；图标按钮 Label+Tooltip；运行态 `RoleStatus`/`Announce`；队列 Badge 可读。

**禁止**：两个 composer 各自维护宽度/发送样式；把发送藏进 Toolbar 溢出；在 build 里做 I/O；每帧 `input.Focus()` 对抗失焦（用 `c.Key`/FocusBind）；伪造"已发送"；给附件留手动路径输入（选择文件器 + 拖入/粘贴即可）。

**当前实现**：会话 `internal/ui/session/view.go` `buildComposer`（含 queue/stop/slash/@/Enter 语义）；首页 `internal/ui/home/composer.go`。共用 `design.Tokens.Composer()`。差异清单见 [DESIGN.md](DESIGN.md) §1.3 与 ui-inventory 报告 §C6。

**草稿持久化（2026-11 审计 UX-18）**：per-session 草稿**文本**落 settings 存储（`sessionDraftSettingKey(sessionID)` 行），重启后恢复；只持久化文本（附件/引用不持久化）。切换会话保存/恢复草稿的既有语义不变；编辑消息/hero 示例对非空草稿的保护见 UX-05。

**附件恢复语义（2026-11-17 UI 审查 P3）**：历史消息里的附件 chip **与热态同构**——文件名 + 大小（`contracts.Attachment.Name` / `Bytes`；`Size()` 优先 `Data` 长度，恢复态回退 `Bytes`）。持久化路径：文件附件沿用文本分片「[附件] name (N bytes)」；**图片附件同样补一条伴随元数据分片**（`rig.TextPart`，不改 rig 类型），恢复时与相邻 `ImagePart` 合并并解码回 `Data/MIME/Bytes`。该元数据分片只服务恢复，**不进正文、transcript 与导出**；旧记录（无元数据分片）回退为 Name「图片」+ 解码大小，不 panic。

**权限双层披露（2026-11 审计 US-05/(a)）**：composer 权限按钮是**全局**开关（菜单标注「（全局）」），同时决定审批 gate 与 OS 沙箱：只读＝仅只读/信息类工具免审批（有可用 runner 时 shell 类工具同样免问并在 runner 内执行、写由围栏限制；没有 runner 时回落到审批且工具侧拒绝）；工作区修改（默认）＝再加工作区内文件读写免审批，OS 沙箱可用时 shell 类工具在沙箱内自动放行、没有系统沙箱时命令以宿主权限运行（仅应用层策略与审批，设置页显示"需要处理"行）；所有权限＝全部免审批且 OS 沙箱不包装命令（以宿主权限运行，警示态徽章 RG-02）。**会话/自动化级「所有权限」仅放宽审批，OS 沙箱跟随全局**——权限说明与系统提示词明示该两层语义及反向泄漏（会话 read-only + 全局 full 时工作区外读不受限）；「本会话内始终允许」保持会话级。**工作区预设自动放行 `apply_patch`**：它一次原子地写/删/移动多个工作区文件，审批卡**没有 diff 预览**（只有工具结果逐文件列出），与 write_file/edit_file 的"工作区内文件读写免审批"同档；信任前提是"工作区内任意文件修改都允许"——若产品改主意要求多文件改动必须人工过目，就要把它移出工作区免审批集并在此写明。沙箱拒绝经 rig DenialSignatures 归因到工具结果/错误行（RG-03，ErrorKind sandbox-denied）。

**全局热键冲突二次确认（2026-11 审计 UX-11）**：设置外观页的全局热键**直接输入路径**保存前做冲突检测（与命令面板/侧栏等已声明快捷键比对），冲突时显示警告行且保存需再次确认（`conflictArmed` 两段式）；录制路径仍被面板 shortcut-first 截获。

**迁移步骤**：① 抽 `uikit.ComposerShell`（卡片+输入+工具条槽+发送/停止槽+错误行）；② 会话/首页各自提供 toolbar slot 与发送逻辑；③ 统一宽度（`design.TranscriptWidth`/容器宽）与发送控件外观；④ 帧测试覆盖 Enter/Shift+Enter/运行中 Esc/排队/附件直连 picker。

**验收**：`TestComposerEnterSends`、`TestComposerShiftEnterNewline`、`TestComposerEscapeInterrupts`、`TestComposerQueueDuringRun` + 快照 `chat-composer.png`。

---

### S8 RunStatus（流式 / 队列 / 停止 / 用量 / 恢复）

**职责**：把 Agent 的运行状态如实、克制地表达出来：正在生成、排队中、可停止、失败可重试、用量可查；不伪造进度、不推动布局。运行中的过程由 S1 的「工作步骤」单行/时间线呈现（默认标准模式一行原地更新，不铺完整时间线）；运行中需要审批时，输入框上方的内联审批卡是唯一阻断点（不弹模态、不遮 transcript）；通知类消息走**右下角 toast**（内容列右下、避开 composer 与状态条，最多 2 条，其余合并为「＋N 条排队」；错误不自动消失），不遮挡输入、转录与顶栏动作（2026-11-18：右上角会压住阅读列正文）。

**数据模型**：`running`、`queued []string`、`usage`（token 计数）、`lastError`、`retryable`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| idle | 不显示运行态 |
| restored-interrupted | 恢复后 `ChatSession.Interrupted`（上次 run 未完成、未重放）：transcript 顶部一条中性提示——`uikit.StatusBadge`「上次运行被中断」+「未重放；请检查工作区状态后继续。」+「继续」+ 忽略图标。继续只聚焦 composer（不自动重放），忽略仅本次隐藏；后端下一次 run 清零后提示自然消失；sidebar 不加徽章（非持久提示不进入导航） |
| streaming | 最后消息尾部 Spinner + 状态文本；输入区停止按钮；`RoleStatus`/Busy；composer 键位提示为「Enter 排队 · Esc 停止」；**有待审批时改为「Esc 拒绝审批（再按停止）」**，与审批卡的「Esc 拒绝」一致（PM-12） |
| queued | **计数的落点随运行态切换**（2026-11-18 定稿，别把两处当成同一控件）：**运行中**只由 composer 动作行的 `×N` chip 报数（chip 紧贴「排队」控件左侧，点击展开面板，标题「队列中的消息 · N 条」，可移除单条/清空）；**停止后**改由状态条的 `已排队 N 条` 徽章接管（恢复/取消在那枚徽章的面板里）。两处都不在零计数时渲染空壳。排队项来自后端 `contracts.PromptQueue`（按发送顺序 + 本地时间）；UI 不持有队列、不发送、不 drain |
| stopping | 停止按钮 pressed/disabled，文案「停止中…/Stopping…」；不可重复触发；**settle 后才 toast「已停止」**（UX-06：取消是异步的，running=true 期间不宣布完成） |
| failed | 错误行 + 重试（带标签按钮，不是图标） |
| queue-send-failed | 投递失败由后端把该条放回队首（并保留），UI 只渲染 `QueuedPrompts` 供用户移除；无「暂停/恢复/重试」内存语义，不得逐帧自动重发或每帧请求重绘 |
| completed | 完成 toast/Announce；用量折叠显示 |
| long-run | 不显示假进度；需要时 `Progress` 仅在有真实进度（下载/批量） |

**目标 API 草案**：

```go
type RunStatusOptions struct {
    Running   bool
    Queued    []string
    Usage     Usage
    Error     string
    Retryable bool
    OnStop    func()
    OnRetry   func()
    OnDismiss func()
}
func RunStatus(c *ui.Context, o RunStatusOptions)
```

**布局（2026-11-18 v3）**：composer 下方一条 **32 高状态条**，两段：左「队列」（停止后才有 `已排队 N 条`）｜中 空｜右「指标行」= `⟨activity⟩ N 轮 M 步 · X tok/s` + `⟨data⟩ T tok · 缓存命中 P%` + `⟨meter 32×4⟩ Q%`（**没有 `详情` 按钮**：2026-11-18 用户裁决——数值已全部内联；精确值走 meter 的 `Description`「42K / 128K」）。整簇在内容列内**水平居中**，不是右对齐工具栏；元素尺度统一：文字 `TypeXXS`(12)、图标 `IconSizeS`(16)、容量条 `ui.Meter` 32×4（`Warning 85%` 琥珀 / `Critical 100%` 红，`NormalColor` = Accent）。**运行态不在条上重复**（2026-11-18 用户裁决）：transcript 末尾已有 `生成中`，条上不再画 `运行中`；只有 `停止中…` 仍在条上显示（那不是重复）。DSH 词序：**「轮」「步」之间不隔 `·`**，只有最后一段前有；组内 `·` 两侧 `Space1`，组间 `Space3`；数字 mono 12；容量用官方 `ui.Meter` 收窄到 32×4（默认 80×8 太重），阈值 `Warning 85%` / `Critical 100%` 白送。窄窗 1024 只留 meter + 百分比（两组文字省略，不折行）。消息自身的用量收进消息元信息行（时间 · 用时 · tokens · ⋯，12/18，hover/focus 显形、行高固定 24）。**running ≠ warning**：进行中不用琥珀，warning 只给 needs-action。错误行贴近失败内容；toast 只做通知，不替代可操作错误。

**禁止**：伪造进度条；把"停止"和"发送"并排同权重；运行中每帧重排布局（几何稳定）；用常驻 badge 代替 toast/错误；用 `ui.Toggle` 表达忙碌状态。

**Job 输出（0.2.40）**：后台任务浮层每行可展开 256 DIP mono 输出面板（复制命令/输出、截断/中断提示、500ms live 刷新、结束补读）；停止为两段式（第一次 arm「确认停止」，3 秒内第二次 `KillJob`，超时/关闭复位），键盘 Enter 可确认。

**当前实现**：session 生成中 Spinner/文本、queue Badge/popover、stop 按钮、消息元信息行（时间 + `FormatCompactCount` 紧凑用量 + ⋯ 用量明细）、`buildRetryAction`；后台完成通知静默由可选 `contracts.ForegroundSession`（uiroot 提供、host 接线）判定：只有当前屏幕=会话且该会话在前台才静默，其他屏幕的后台完成照常通知；无该服务（headless）回退 agent active 会话判定。toasts 模块（`c.AddToast` + `ToastViewportBase`，2 条上限 + 合并 ×N）。测试：`queue_send_test.go`、`usage_test.go`、`toasts_test.go`。

**停止态与 settle toast（2026-11 审计 UX-06）**：主停止点击后进入 stopping 态（`uikit.ComposerShell` 停止位读「停止中…」、disabled 防重复），后端 run 真正 settle 后才发「已停止」toast；与 team/job 停止的既有「停止中…」语义对齐。regression 钉住：`TestStopCommandEnabledAndCancel`（stopping/settle 后 toast）。

**循环保护与工具循环配置已移除（v0.2.50，用户裁决；2026-11-20 rig v0.24.0 升级后补充口径）**：「让工具自由发挥」——产品侧硬 loop 熔断（wall-clock / token / 无变更调用预算）与 advisory 重复提醒（3/5/8 阈值 nudge）**整体删除**：产品不因循环保护取消 run，也不往工具结果里追加任何提醒/熔断文本。四个 `agent.loop*` 设置键、contracts Normalize 助手与上限常量、设置页「循环预算」节全部删除；`chat.maxIterations` 用户旋钮同步删除（settings 键、模型页输入、读取点）。**轮数默认不限**（rig 自 v0.19.0 起原生 unlimited，v0.24.0 起 `agent`/`kit` 默认同为 0=不限）。`agent.Config.MaxIterations` 保留为**装配级产品定义**（0=unlimited），非用户配置；委派子代理沿用同一装配预算。历史兼容：`contracts.MessageErrorKindLoop` 常量保留供旧持久化 run_end 恢复（ClassifyError 仍识别旧 "loop guard: …" 文本），session 对 kind loop 显示**历史中性文案**——zh「该回合曾被旧版循环保护停止」/ en "This turn was stopped by the legacy loop guard (removed in this version)"，不再解析 trip 预算（`LoopTrip*` 常量与 `loopTripReason` 已删），原始错误文本保留为 muted 诊断细节。**审批超时保留**（`agent.approvalTimeoutSeconds`，属审批非工具循环）：无任何 run 级时长兜底，无人值守自动化建议设置审批超时（RG-04）；审批超时 description 已同步。

**rig 自带的重复调用闸（v0.24.0 起，产品不可配置）**：升级到 rig v0.24.0 后，rig 的 agent 循环**自带**一道重复调用闸（常量写死：连续 3 轮完全相同的调用注入一条提示；同一签名连续 5 轮、或单轮 3 个完全相同的调用，从第 5 轮/第 3 个起返回 `rig: repeated call blocked` 工具错误）。它在 `rig/agent` 内部、没有 `AgentSpec` 开关，因此**不是产品设置**，也无法由产品关闭：上一条的「工具自由」只对**不同**调用成立（45 次只读链正常完成），完全相同的重复调用由 rig 收口。`agent/tool_freedom_test.go` 的 `TestDefaultRunIdenticalCallsFollowRigRepeatGuard` 钉住当前行为；若要让重复调用也完全自由，需要在 rig 增加开关（上游改动，见 `docs/research/rig-upgrade-2026-11/PARITY-v0.24.0.md`）。

测试：`agent/tool_freedom_test.go`（默认装配 45 次只读调用正常完成、无取消、无产品侧 nudge 文本；8 次完全相同调用按 rig 闸在第 5 轮起被拦）、`contracts/loopkeys_removed_test.go`（键表无 loop*/maxIterations 用户键负向断言 + 历史 kind 分类保留）、`session/loop_error_copy_test.go`（历史中性文案帧测试，中英）、`settings/loop_config_removed_test.go` / `loop_config_removed_internal_test.go`（设置页无循环预算节、模型页无最大迭代输入、搜索词汇负向断言、旧存储行被忽略不改写）。

**旧权限运行中 job 提示（2026-11 审计 RG-09）**：权限降级不追溯——后台任务浮层经可选 `contracts.JobPermissionReporter` 读模型显示「N 个运行中任务仍按旧权限运行」+ 说明（OS 沙箱约束在启动时施加、不追溯变更）+ 两段式批量「停止仍按旧权限运行的任务」入口（第一次 arm 确认，3 秒内第二次执行）；job 路由修复（RG-01 pwsh/promoted 前缀）后 kill 真实可达。

**迁移步骤**：抽 `uikit.RunStatus`（状态行/队列/停止/重试/用量）；保留通知通道不变；帧测试覆盖排队/停止/重试/用量折叠。

**验收**：`TestRunStatusDoesNotFakeProgress`、`TestRunStatusQueueRetry`、`TestRunStatusUsageCollapsed` + 快照 `run-status.png`。

---

### S9 会话 agent surface（多 Agent / 团队 / 任务）

**职责**：把子代理与团队成员作为**子会话**呈现：头部入口、团队浮层、主栏子会话视图（面包屑返回）、共享任务；运行态必须真实（`RunState`），不得把 `active`（可用性）画成 running。**团队/子代理不再放进侧边栏或右栏页签。**

**数据模型**：`contracts.ChildAgentService`（`ChildAgents(parentSessionID)` / `ChildAgent(id)` / `SendChildAgent`；子代理=one-shot，队友=continuable）、`TeamMember{RunState,SessionID,Historical,Resumable,...}`、`ChildAgent.Historical`（持久历史行）/`ChildAgent.Resumable`（该历史行可冷恢复继续）、`TeamActivity`（有界）、`BoardTask{Status,Owner,BlockedBy,WriteScopes}`、`TaskAssigner`（可选）。

**持久子会话（2026-11-11，DSH 对齐）**：teammate 与同步子代理共用一套稳定身份——创建时分配 `child-<8 hex>` 的 child-session ID，团队 member ID、子智能体 ID 与「已结束」历史行是同一个值；rig manager 的 `member-N` / 子代理注册表 ID 只在后端解析。记录写入 `<数据目录>/child-sessions.json`（v2；有界 64 条 + 14 天 TTL + 原子写 + 损坏隔离；自动迁移旧 `child-agents.json`），spawn 时即写、完成/失败/中断/关闭/退出持续更新。重启后恢复为 `Historical` 行：其中 `Resumable` 的历史记录可继续（后端从持久 child session 冷恢复），团队浮层「已结束」区显示「历史 · 可继续」并提供「继续此队友」、子智能体切换器提供「继续此子代理」、子会话显示冷恢复输入框（仍调用 `SendChildAgent`）；不可恢复的历史行显示「历史 · 只读」并明示原因，无输入框。历史按 `SessionID` 归属到当初的会话：页头计数与团队浮层只列当前会话的历史，运行中队友仍是进程级可见。会话浮层的共享任务按会话归属、缓存优先读取（缓存未命中只做一次只读文档加载，不迁移/不写；无活动会话时不回退全局板）。共享任务板按会话归属（DSH 的 Lead-session 语义）：`team_task_*` 携带 run-scoped 会话上下文，每个会话持久化到 `<数据目录>/team-tasks/<session>.json`，旧 `team-tasks.json` 首次使用自动迁移；会话 UI 只列当前会话的任务，`KeyTaskBoard` 聚合视图留给无会话上下文的宿主。子代理/队友完成时，父会话的 transcript 显示一行只读完成回执（`ChatRoleNotice`，由 child-session 记录派生）；队友完成还会以 user 消息注入下一轮父会话模型上下文（每个队友一次，`Notified` 持久化；同步一次性子代理的答案已在工具结果里，不重复注入）。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| running | 运行中 Badge/活动行；团队浮层显示运行点 |
| idle | 空闲；可向可继续成员发消息 |
| done/completed | 完成 + 可查看产出 |
| failed | 失败 + 错误原因 |
| interrupted | 已中断（非失败） |
| one-shot | 只读：无 composer，不显示「发送」 |
| historical-resumable | 已结束记录（`Historical=true, Resumable=true`）：浮层/切换器给「继续此队友/此子代理」，子会话有冷恢复输入框；发送后显示「已发送，等待回复」，回答回来后显示最新回复；不伪造进度 |
| historical-read-only | 已结束且不可恢复：只读并说明「已结束的记录（应用重启后仍保留）：只读，不能再继续对话。」 |
| task blocked | 依赖未完成：显示 blockedBy，状态不可推进 |

**入口结构**：

- 会话头第一行（`screen.header`/`screen.actions` 槽）：`标题/面包屑 → Agent Run Band（运行中 → 智能体团队 → 子智能体 N → 后台任务 → 待审批）→ spacer → ⋯`；Band 是一行原生入口按钮（无外层容器/双重描边），团队/子代理零值常显但不显示 0 计数，后台任务/待审批只在有内容时出现；各入口都是 `ui.PopoverBase` 原生浮层（Esc/点外关闭/焦点归还），不占右栏。
- 团队工作台：团队浮层底部「打开工作台」→ 原生 `ui.Modal`（DialogWidthXL），左列成员（状态/模型/最新活动/打开/停止 running/已结束历史）、右列共享任务（subject/状态/owner/依赖/写入范围），列内各自有界滚动；Esc/关闭/点外部回到团队浮层，浮层展开状态、滚动位置与任务快照保留，焦点归还入口；不新增右栏常驻页。
- 团队浮层：成员卡（状态点/名字/模型/最新活动/「打开」）+ 共享任务卡（subject/状态/owner/依赖/写入范围）；running 成员提供「停止」（`TeamService.Interrupt`，中性 cancelled），行尾 ⋯ danger 菜单「关闭成员」走 `uikit.ConfirmDanger`（取消 Default）；空态说明「暂无共享任务」；已结束的持久记录按「已结束」分组展示：`Resumable` 行带「历史 · 可继续」与「继续此队友」动作，不可恢复行带「历史 · 只读」（打开仍只读）。
- 子智能体浮层：子代理/队友列表；选中进入主栏子会话。
- 后台任务浮层：`JobService.Jobs()` 的行（状态/命令/用时/退出码）+ 停止（`KillJob`）；下方「历史」分组读取 durable `execution/*`（`contracts.ExecutionHistoryService`）：job/terminal 开始/结束/中断、命令/cwd/root/模式，明确「输出不会持久化」，interrupted 用中性状态，live job 同 ID 去重；无 live job 但有历史时入口仍出现（重启后可达）。

**布局与交互**：

- 子会话在主栏渲染：标题变为 `父标题 › 子智能体名` 面包屑，点父标题返回父会话；内容区显示任务（prompt）、状态、输出与活动时间线；可继续成员底部有消息输入框（`SendChildAgent`），一次性成员只读；重启后可冷恢复的历史记录同样有输入框（placeholder「继续该队友/子代理…」，发送后 hint 说明冷恢复），不可恢复历史明示只读原因。
- 团队浮层的数据只读，但 running 成员可停止/关闭（控制动作）；成员/任务数据来自 `TeamService`/`TaskBoard`；任务完成/指派由工具或服务调用完成，浮层在打开时读取一次任务快照（关闭重开刷新）。
- 头部窄带（阅读列 < 2/3）退化为图标+计数，可及名保留全称。
- a11y：入口按钮 Label 为全称；状态文案可读（不只靠颜色）；浮层为 native popover/dialog 语义。

**Todo dock 全局披露与保序（2026-11 审计 US-11）**：composer 上方 Todo dock 的 store 是**进程级全局**（跨会话共享）——dock 内一行 TypeXXS 静默文案明示「待办为全应用共享，不区分当前会话」，避免把别的会话写的清单读成本会话计划；列表**按 agent 写入的编号步骤序渲染**（不按状态分组重排），`TestTodoDockKeepsAgentOrder` 以行 Y 坐标钉住顺序契约。

**禁止**：把团队/子代理塞进侧边栏或右栏常驻页签；`active` 当 running；伪造子代理活动时间线；一次性子代理显示可发送输入框。

**当前实现**：`internal/ui/session/agentsurface.go`（三入口 + 三个 Popover 浮层 + 子会话视图 + 回合尾文件卡）、`internal/modules/agent/childagents.go`（统一 `ChildAgentService` 读模型）、`internal/modules/agent/child_sessions.go`（持久子会话 `child-sessions.json`）、`internal/modules/agent/child_notices.go`（完成回执：下一轮模型上下文，仅队友）、`internal/ui/session/view.go`（transcript 的 `ChatRoleNotice` 回执行：live 按时间合入、运行中放在流式回答上方；重启恢复的消息无时间戳时按 `Finished` 追加）、`internal/modules/toolkit/jobs_service.go`（`JobService`）、`internal/modules/toolkit/execution_history.go`（`ExecutionHistoryService`，durable `execution/*` 有界折叠）。旧 `internal/uikit/team.go` / `rightpanel` 团队/任务 pane 已删除。

**验收**：`internal/ui/session/agentsurface_test.go`（帧测试：header 顺序、团队浮层、switcher、子会话面包屑/返回、双向不可用、jobs 停止、running 成员停止/关闭确认、子会话停止、执行历史分组、文件卡预览回调、历史分组/只读历史会话/冷恢复历史继续发送）、`internal/modules/agent/childagents_test.go`、`internal/modules/agent/child_sessions_test.go`（重启后同一身份、文档往返、v1 迁移、损坏隔离、保留上限）、`internal/modules/agent/child_notices_test.go`（回执只注入一次、失败重试、跨重启/Close 重写、跨会话归属）、`internal/modules/toolkit/jobs_service_test.go`、uimodtest 新 E2E（团队浮层 / 文件页 / 快捷键 / 冷恢复发送）、`ui-drive` team 步骤（含 `team-history`）。

### S10 ContextInspector（会话 Context 页签）

**职责**：把后端 `contracts.ContextInspector`（`contracts.KeyContext`）折叠出的会话上下文事实如实呈现：当前 used/window、最近一次请求快照、逐模型调用 token/成本、压缩/溢出事件。仅在会话页签内展示，不做跨会话洞察。

**页头动作**：Context 页头承载「压缩上下文」（仅会话空闲可用；运行中/compacting disabled+原因 tooltip，复用 `/compact` 的 `runCompactCommand`）与「上下文设置」（`OpenSettingsPageAt("model","model.context-budget")` 打开模型页并展开上下文预算；无该 optional 接口时回退打开模型页）。

**入口**：会话页签条在「对话 / 轨迹 / 记忆」后追加「上下文 / Context」；**仅当 `ctx.Get(contracts.KeyContext)` 存在时注册**（无后端不显示，保持三页签兼容）。子会话视图不显示该页签（无独立会话上下文，进入子会话时回到对话页签）。

**布局**：`uikit.ReadingColumn`（960 居中、`MaxWidthPercent(92)`、无 `Grow`、PaddingX 16 / Gap 20；与对话/Goal dock/子会话 banner 同轴）内三段 `uikit.Section`（概览 / 模型调用 / 上下文事件），页面用 `ui.Scroll`；集合一律官方组件——模型调用用 `ui.Table`（列：轮次/模型/输入/完成/缓存/上下文/时间[/成本]，虚拟化、`MaxWidth(TranscriptWidth)`、`MaxHeight` 有界、窄窗横向滚动不换行），事件用 `ui.List`（`MaxHeight` 有界）。

- 概览（价格在「设置 → 模型」编辑，按当前价格重算）：`uikit.PropertySheet`（160 DIP 标签列 + 值左对齐 + 32 行 + Divider）展示「上下文用量」（`used / window tokens (n%)`；window 未知时只显示 used，不伪造百分比）与仅在 `CostKnown` 时出现的「累计成本」；最近一次快照另起小节展示时间/模型/窗口/系统提示（bytes · tokens）/估算输入/历史消息（用户/助手/工具）以及工具清单（数量 + 名称摘要，tooltip 全量）与注入来源 chips（AGENTS.md/预设/记忆/技能，只显示实际存在的）。
- 系统提示分段（`Snapshot.Sections` 非空才出现，旧快照整段隐藏，不伪造）：`ui.Table` 按 section 显示本地化名称（identity/environment/…/tool-guidance 映射，未知 ID 回退记录 label/ID，工具 tips 带 ID+Hash）、来源、字节与估算 tokens；表下 `uikit.PropertySheet` 给「分段合计」以及（window 已知时）「估算输入占比 xK / yK (n%)」，并说明「正文不进入事件，仅元数据」。
- 模型调用：最新在前；每行 iteration/model/prompt/completion/cached/context tokens/时间；成本列仅在会话 `CostKnown` 时存在，行级未知成本显示「—」而不是 0。
- 上下文事件：最新在前；compact 分四类展示：`Inferred=true` →「压缩（推断）」；`Inferred=false` 且 `Detail=manual` →「压缩」；`Inferred=false` 且 `Outcome==""` →「自动压缩」（运行前 exact，显示 before→after tokens 与消息数 `Replaced → Saved`）；`Outcome!=""` →「自动压缩跳过/失败：failed|no-reduction|archive-failed|replace-failed」+ 本地化原因（历史未修改），**只显示 before→after tokens，不显示会误导的消息 delta，绝不显示「已压缩」**；未知 Outcome 回退原文。spill 显示路径 + 磁盘字节；`recovery` kind 标注「自动压缩重试（成功/失败/压缩失败）」+ 本地化说明 + before→after tokens（其 Saved 是重试次数，不显示成消息数）；`archive` kind 标注「归档/归档（截断）」，说明归档 N 条、超上限省略 M 条（截断），meta 显示归档字节 + before→after tokens；archive/compact 行在存在 durable 归档时给「查看归档」→ 原生 Modal 只读 `ui.List`（角色 Badge/原文/工具调用，截断明示）；读取走可选 `contracts.ContextArchiveService`（agent 按 sessionID+event seq 读 `compact_archive`，有界 200 条/8 KiB 文本/1 KiB 参数，只读审计、绝不注入模型；Seq 由折叠事件持久化，重启后可读）。archive 与 compact 各自独立成行，不合并/不重复计数。手动 `/compact` 的 `ErrCompactArchive`（归档失败、历史未修改）走本地化失败提示，绝不显示「已压缩」。
- 系统提示分段：仅 `Snapshot.Sections` 非空时显示「系统提示分段」表（本地化名称/来源/字节/估算 Tokens），下方显示分段合计与（window 已知时）估算输入占比；空快照整段隐藏，正文不进入事件；未知 section ID 回退 Label/ID，hash 仅 tooltip。

**状态**：`has=false`（或折叠数据全空）显示空态「本会话还没有上下文数据」+ 说明运行一轮后出现，绝不渲染 0 值概览；无会话显示「还没有会话」空态；无后端不注册页签（防御分支有空态文案）。

**刷新**：inspector 无 Subscribe；页签进入、会话切换、ChatService 通知（含 run settle）时标记 dirty 并读取一次，按 `SessionContext.Revision` 去重；稳定帧/render 不重复读取；每次切会话重置两个集合的滚动位置。

**a11y/i18n**：Table `Label`「模型调用列表」、List `Label`「上下文事件列表」、每行有可读名称；全部文案中英；times 用 `uikit.FormatDateTime`/本地时钟。

**禁止**：`Scroll` + 手写循环画调用/事件；伪造 0/假成本；window 未知时编造百分比；侧边栏加 Context 徽章；跨会话合并数据；在每帧无条件读 inspector。

### S11 Insights（跨会话洞察页）

**职责**：把 `contracts.InsightsService`（`contracts.KeyInsights`）按 7/30 天窗口聚合出的跨会话用量如实呈现：totals（轮次/步数/输入/输出/缓存 tokens、成本、工具调用、活跃会话数）、按天序列、按模型、按工作区、会话排行。这是**只读诊断页**：它不改变会话、不写设置、不做图表。

**数据模型**（`internal/contracts/insights.go`，UI 不 import rig）：`InsightsService.Insights(ctx, InsightsQuery{Days, SessionLimit, ForceRefresh}) (Insights, error)`；`Insights{From,To,GeneratedAt,Totals,Daily,Models,Workspaces,Sessions,Truncated,TruncatedSessions}`。后端 `internal/modules/insights` 只做有界读取（`Store.Sessions()` + 每个窗口内会话一次 `Store.Last()`，默认上限 4000 事件/会话），折叠规则：`model_call`（逐调用）为 token 主源，缺 `model_call` 的旧会话回退 `usage`（逐回合，`iterations` 计步），两者并存时不重复计 token；`turns` 数 `usage`，`steps` 数 `model_call`（回退用 usage.iterations），`tool_calls` 数 `tool_call`，`compactions/spills` 数 `compact/spill`。成本按**当前**模型目录每 1M token 单价现算（与「设置 → 模型」同源）；无单价的模型只计 `UnpricedCalls`，绝不产生 0 成本。日桶按后端配置时区（默认本地，产品为 Asia/Shanghai）的本地日零点切分，窗口含今天；结果按 `(days, sessionLimit)` 缓存 15s（`ForceRefresh` 绕过），刷新在后台 goroutine，不占帧。

**入口**：一级侧栏入口——`RegisterScreen(ScreenSpec{ID:"insights", Order:25})` + `sidebar.ScreenNavSpec("insights") = (icons.Activity, "洞察", "Insights")`；位于「自动化」与「能力」之间。顶栏贡献 `insights.title`（`screen.header`，TypeM/600）与 `insights.actions`（`screen.actions`，唯一的即时动作「刷新」图标按钮，加载中 disabled）。不新增右栏常驻页、不在会话页签内重复。

**布局**（`internal/ui/insights/view.go`，页面用 `ui.Scroll`，正文不叠第二行 header）：
- 工具栏：`uikit.SegmentedControl` 分段控件「7 天 / 30 天」（radio 组语义、方向键、`Label`「时间范围」；选中段 `BgSelected` + `LabelPrimary` + 600 + 2 DIP Accent 下划线，无 accent 填充）；右侧为弱化的范围/更新时间（`11-09 – 11-15 · 更新于 HH:MM`）。
- KPI 行：四个扁平指标列（label TypeXXS 弱化 / value TypeM·600 / 一行 TypeXXS 细节），无卡片、无底色：`tokens`（输入·输出）、`成本`（仅 `PricedCalls>0` 时出现；部分未定价标「部分模型未定价」）、`会话`（轮次·步数）、`工具调用`（压缩·溢出）。强调色预算：本页 0 个 accent 填充、0 个 primary 按钮。
- 按天：`ui.Table`（日期/tokens/成本/会话/工具；成本列仅当某天有价时出现；只列有活动的天（空天不出现），全空时该节空态；最新在前；`MaxHeight` 有界）。
- 按模型：`ui.Table`（模型/调用/tokens/缓存/成本；未知模型显示「未知模型」，无价行成本「—」）。
- 按工作区：`ui.Table`（工作区/会话/tokens/成本），仅当存在已关联工作区时显示；未关联组显示「未关联工作区」，名称由后端按工作区服务解析。
- 页面正文统一包在 `uikit.ReadingColumn`（960/92% 居中），宽窗表格不再被拉到屏幕最右。会话排行：`ui.List`（标题 + 模型/工作区/更新时间元信息 + tokens + 可选成本）+ `SearchField` 本地筛选（标题/模型/工作区显示名与 ID），行内单点（`ObserveInput`）打开会话，Enter/双击走 List `Submitted`；行内文字/成本在 `row.Selected()` 时切 `t.AccentText`（选中行对比 ≥4.5:1，亮/暗）；打开 = `ChatService.SetActiveSession` + `Navigator.Push("session")`，失败留在本页并以 `ErrorLine` 显示。

**状态**：加载态 = `Spinner` + 「正在汇总用量…」（不渲染骨架 0 值）；空态 = 「还没有可统计的用量」/「这个时间范围内还没有用量」+ 说明；错误态 = `uikit.ErrorLine` + 重试（首次失败只显示错误与重试，已有数据时错误行下方保留旧数据）；无后端 = 「洞察后端未启用」空块（screen 仍注册）。

**刷新/性能**：进入页面即后台读取一次；打开期间数据超过 60s 自动后台刷新一次；「刷新」动作 `ForceRefresh`。聚合从不在 build 回调里做 I/O，结果经 `UIRuntime.Post` 回 UI 线程；乱序/过期结果按 requestID 丢弃；每帧只读模块状态，不读 store。会话级读取达到上限时页面显示「统计达到单会话读取上限，较早的事件可能未计入（n 个会话）」并标记 `Truncated`，不静默少算。

**a11y/i18n**：Table/List 均有 `Label`（按天用量/按模型用量/按工作区用量/会话排行列表），每行有可读名称；刷新按钮 `Label`+Tooltip；全部文案中英；日期 `formatDay` 随语言（zh `01-02` / en `Jan 2`），时间用 `uikit.FormatDateTime`/`FormatClock`；会话行可达名含标题/模型/tokens/成本。

**禁止**：自绘图表/canvas/`ui/chart` 装饰；`Scroll` + 手写循环画集合；卡片墙/KPI 卡片；伪造 0 或假成本（无价 → 隐藏或「—」）；未知价格行渲染 `$0`；每帧全表扫/每帧重算；在 build 内读 store 或同步聚合；把洞察做成会话内重复数据；改会话页/右栏语义。

**当前实现**：`internal/contracts/insights.go`、`internal/modules/insights/`（`insights.go` 模块/缓存、`aggregate.go` 折叠）、`internal/ui/insights/`（`insights.go` 注册/加载、`view.go` 页面）、`internal/assembly/registry.go` + `assemblies/desktop.json`（`zeokeel/insights` + `zeokeel-ui/insights`，headless 不加载）、`internal/ui/sidebar/sidebar.go`（`ScreenNavSpec`）。

**验收**：`go test ./internal/modules/insights`（聚合/日界时区/无价/空/缓存/截断）、`go test ./internal/ui/insights`（入口/数据/空/无价/切范围/点击跳转/错误重试/加载/窄窗/英文/无后端）、`internal/ui/uimodtest/insights_e2e_test.go`（真实后端 2 轮 → 3M tokens/$4.00/排行点击；无价 → 隐藏成本）、`ui-snapshot` 场景 `p2-insights.png` / `p2-insights-dark.png`。

## 2. 跨 surface 规则

- **顶栏只有一块**：屏幕通过 `screen.header` / `screen.actions` / `screen.tabs` 槽位把标题/动作/Tabs 交给宿主 B3 顶栏（浅色 header 面 + 仅底边 hairline、行间无线），屏幕 body 不再叠第二行 header；无贡献的屏幕没有空工具栏；不提供返回按钮或 `Alt+←` 历史回退（侧栏即导航）。
- **输入框上方的 dock**：目标条与审批内联卡固定在 composer 上方（阅读列宽度居中），是会话页唯一的阻断/目标区；不把目标放回顶栏、不把审批做成居中 Modal。目标条只展示当前会话的 durable goal（phase/轮次/activation/blocked reason），UI 不缓存其他会话目标、不发送续跑；清除目标走 `ClearGoal` + `uikit.ConfirmDanger`。
- **会话级 vs 全局设置**：权限按钮只保留「权限模式」一项（按钮文案即当前权限），作用于所有会话；「本会话内始终允许」保持会话级（PM-14）。
- **记忆 scope**：`workspace:<id>` 绑定作用域的条目在会话「记忆」页签显示「工作区（本会话项目）」，不展示存储原值；设置→记忆是跨工作区管理页，保持「工作区」标签。
- **模型价格与成本**：价格在「设置 → 模型」的编辑表单里配置（输入 / 缓存输入 / 输出，每 1M token 的美元价；留空或 0 = 未配置），只写 `modelcatalog.models`，不复制进默认模型 mirror；非法值（非数字/负数/NaN/超大）就地报错且不保存。价格只用于成本估算，按当前价格重算（重启回放也用当前价格），无价格时 Context 页签不显示累计成本/成本列、行级未知成本显示「—」，绝不渲染 0/假成本。
- **通知位置**：toast/通知固定右上角、B3 顶栏下方 8 DIP，右对齐自上而下堆叠（最多 2 条）；不放在右下角、不遮挡标题/Tabs/顶栏动作或输入区。

### 2.1 焦点与 Esc 分层（从最内到最外）

| 层 | 谁先拿到键 | 规则 |
|---|---|---|
| 建议菜单（slash/@） | 输入框内的菜单 | ↑↓/Enter/Tab/Esc 由菜单消费；Esc 关闭菜单，不中断运行 |
| 浮层（Modal/Overlay） | 最后构建的 overlay 先吃 Esc | `OverlayShortcut` 约定；审批 Esc=deny；计划审阅 Esc 不取消；ask Esc=取消。审批 pending 时会话层的第一下 Esc 也按 deny 处理，composer 提示同步（PM-12） |
| 文本输入 | 输入框 | Enter 语义由所在 surface 定义（发送/提交/继续规划/换行） |
| 全局快捷键 | 壳层 | 不抢 overlay；运行中 Esc 中断只在无菜单/无 overlay 时生效 |

### 2.2 默认动作规则（安全）

| 场景 | Default | 依据 |
|---|---|---|
| 审批 | **无** | 防误触允许/拒绝 |
| 计划审阅 | **无** | 批准/取消都必须显式 |
| ask 选项 | 取消（Other 打开时输入框） | 防误选未选选项 |
| 自由文本对话框 | 输入框 Enter 提交（`EnterTo`） | 有文本输入时 Default 让位 |
| 非危险简单确认（AlertDialog） | 末位按钮（平台原生） | 只用纯确认；审批与危险确认禁用 |
| 危险确认 | 危险动作不得为 Default；安全动作（取消）可 Default | 删除/关闭成员/清空等；审批/计划则完全无 Default |

### 2.3 流式几何稳定

- 流式增长只允许发生在"消息内部"；外层列表/工具栏/composer 不重排。
- `ListState.FollowEnd` 贴底；用户上滚即停止跟随；「回到最新」浮层可恢复。
- 折叠/展开调用 `HoldPlace`，避免行跳动。
- 不做每 token 的入场动画堆叠；markdown 的 reveal/fade 由官方 `Stream` 负责。

### 2.4 状态语义不可降级

- `pending/running/success/error/cancelled/unknown` 六态词表统一（`uikit.StatusText` 收敛后唯一来源）。
- error 不用 warning/普通文本；cancelled 用中性色；`active` 不等于 running。
- 状态同时用「文案 + 颜色/图标」，颜色只做辅助。

### 2.5 双向上下文事件契约（DuetUI 式）

Agent 客户端的界面不是一次性输出：用户对界面的操作要作为**结构化上下文**回传 Agent。每个 surface 必须能产生下列事件（而不是只更新本地视觉）：

| surface | 用户动作 | 结构化事件（契约） |
|---|---|---|
| S1 会话线程 | 重试/重新生成、复制 | `retry(turnID)`、`copy(messageID)` |
| S2 推理 | 展开/收起 | 纯 UI 状态（不进入上下文） |
| S3 工具行 | 展开详情、团队跳转 | `openTool(toolCallID)`、`openTeam`（命令 ID） |
| S4 审批 | 允许/始终允许/拒绝 | `ApprovalDecision{deny,allow,allow-always}` |
| S5 计划审阅 | 批准/继续规划(反馈)/取消 | `PlanReview.Exit{approve,keep-planning,cancel}` + feedback |
| S6 提问 | 选择/自由回答/取消 | `UserAnswer{option, custom, cancel}` |
| S7 Composer | 发送/排队/停止/附件/模型切换 | `Prompt{text,attachments}`、`interrupt()`、设置写入 |
| S8 运行态 | 停止/重试 | `interrupt()`、`retry(lastTurn)` |
| S9 团队 | 派发/中断/关闭/指派/任务状态 | `TeamMember/BoardTask` 服务调用 |

**规则**：入口组件的回调必须落到 contracts 服务或稳定命令 ID；禁止只改 UI 状态而不回传（除非该状态明确是纯展示，如推理折叠）。

### 2.6 ErrorPanel（跨 surface 错误原语）

- 已有 `uikit.ErrorLine`（图标 + `RoleAlert` + DangerText + 可选中）用于行内错误。
- 待建 `uikit.ErrorPanel`：错误 + **恢复动作**（重试/去设置/打开日志）+ 可复制详情；用于审批失败、计划失败、会话失败、设置保存失败。
- 规则：错误必须给下一步；toast 只做通知，不替代可操作错误；错误文本可选中复制。

### 2.7 记忆与技能管理（非 surface，事实约定）

记忆 Tab、设置「记忆」页与能力页「技能」Tab 不是上述 9 个 Agent surface，但读写同一批 contracts 服务；改动它们时保持以下事实一致：

- **记忆 scope**：条目保存工作区身份（`workspace:<id>`），`List/Search` 只返回 global + 当前工作区条目；管理页用可选 `MemoryAllLister.ListAll` 看全量。默认 scope 只由 `memory.Config.Scope` 决定（工具/设置页/提取省略 scope 时走它）；会话「记忆」Tab 显式写当前工作区。
- **记忆恢复**：`memory.json` 损坏时隔离为 `.corrupt-<ts>` 并以空 store 继续；`MemoryRecoveryReporter.RecoveryNotice()` 必须显示在记忆页（不能只写日志）。
- **技能根**：优先级为工作区（`skills`、`.agents/skills`、`.zeokeel/skills`）→ `skills.dirs` → assembly 目录 → `dataDir/skills` → `$ZEOKEEL_SKILLS` → `~/.zeokeel/skills`；同名遮蔽与扫描上限经 `SkillWarnings.Warnings()` 在能力页「技能」Tab 可见。
- **技能内容边界**：`List` 只读每个 `SKILL.md` 的前缀（名称/描述/可搜索摘要），`Read`/`load` 才读全文（≤1MiB）；编辑只重写名称/描述/正文，保留未知 frontmatter 键；写路径仍仅 `<dataDir>/skills`。
- **技能管理入口（2026-11-16）**：能力页每张技能卡都有 `⋯`（默认应用打开/文件管理器显示/复制路径）；只有 `<dataDir>/skills/<name>/SKILL.md` 目录形态技能额外出现编辑/删除（`skillsEditActionsAllowed` 同时校验 dataDir 与 `SKILL.md` 基名）；顶层 `<dataDir>/skills/foo.md` 与工作区/额外目录技能保持只读；没有任何可编辑技能时显示只读说明行；空列表只有集合行一个「新建技能」。
- **技能启停/复制/自动刷新（2026-11-16）**：`skills.disabled` 保存被禁用技能名，仅 toolkit 的 agent-facing skill store 过滤（UI 仍列出并可恢复）；只读技能可用「复制到我的技能」预填新建表单；外部 `SKILL.md` 修改在技能页打开时每 5s 自动重扫；创建/编辑弹窗提供 Markdown 预览。

### 2.7.1 连接器 OAuth 2.1（非 surface，事实约定）

能力页「连接器」Tab 的 OAuth 认证也不是 Agent surface，但会话工具依赖同一批连接：

- **协议边界**：OAuth 发现/注册/PKCE/回环/令牌/刷新/撤销全部由 `zeokeel-rig v0.18.0` 的 `tools/mcp` 实现；产品只经 `contracts.KeyMCPAuth` 提供状态与两阶段流程，并在 `internal/modules/toolkit/mcp_auth.go` 注入 `TokenStore`（settings）与状态映射。
- **令牌存储**：每个连接器一条 `mcp.oauth.<slug>.<sha8>` settings 记录（版本化 JSON，由 rig 序列化）；诊断导出与模型 `settings_get` 对该前缀整值隐藏，`headers` 对象/数组的所有值按凭证掩码。
- **浏览器与线程**：`Prepare` 在后台 goroutine 发现/注册/监听；浏览器只在 UI 主线程打开（`internal/lib/openurl`）；`Wait` 后台等待回调并换 token；授权成功后 toolkit 自动重连该连接器（无需重启）。headless 无浏览器时不提供交互式授权。
- **安全边界**：workspace `mcp.json` 的 OAuth 配置被忽略并告警；PKCE 必须 S256；MCP 请求只跟随同源重定向；`invalid_grant` 需重新授权。
- **令牌刷新**：每个 MCP 请求前检查到期时间，**提前 60 秒**单飞刷新；**toolkit 后台每 30s 检查、到期前 5 分钟主动刷新**；401 时刷新并重试一次（请求体可重放时）；refresh token 轮换后写回 settings，持久化失败会清空并要求重新授权。已授权连接器不再显示「重新授权」，只有 Required/Expired 才提供授权入口。UI 详情显示当前 access token 到期时间，刷新后随 `Status` 更新。
- **Headers 与工具开关**：连接器表单可配置任意 HTTP 请求头（`Name: value` 多行，诊断/模型读取脱敏），用于非标准认证/网关；详情列出该 server 的模型可见工具并支持逐工具 `tools.disabled` 开关，切换后热重载。
- **热重载（2026-11-16）**：连接器设置写入后，toolkit 经 `contracts.KeyMCPReloader` 重建受管连接、注销旧工具并注册新工具，`hosttools.KeyRegistry` / `KeyPlugins` / `KeyMCPAuth` 指针保持稳定；新增/编辑/移除/启停/授权不再要求重启。OAuth 行 URL 在保存与启动前去掉路径尾斜杠（`contracts.NormalizeMCPURL`），避免 PRM resource 尾斜杠误报 `resource_mismatch`。
- **GitHub 远程 MCP**：`api.githubcopilot.com/mcp` 不提供动态客户端注册，且 GitHub 令牌交换需要 client secret（当前公开客户端不支持）；UI 明确引导选择 **Bearer 令牌 + GitHub PAT**。

---

## 3. 迁移与验收总表

| # | 入口 | 迁移来源 | 先决条件 | 帧测试 | 快照 |
|---|---|---|---|---|---|
| S1 | SessionThread | session `buildChatPane` | ToolRow/ReasoningTrace 稳定 | 虚拟化/跟随/空态 | `uikit-session-thread.png` |
| S2 | ReasoningTrace | session `buildReasoningDisclosure` | — | streaming 展开/结束收起 | `uikit-reasoning.png` |
| S3 | ToolRow/`ToolCallCard` | uikit.ToolRow（已有）+ session 决策卡 + toolcards | StatusText 收敛 | 状态/失败展开/无详情 | `uikit-toolrow.png` |
| S4 | ApprovalDialog | approval | DialogFooter（已有） | 无 Default/Esc deny/队列 | `approval.png` |
| S5 | PlanReviewDialog | interaction planPanel | DialogFooter | Esc 不取消/空反馈不批准 | `plan.png` |
| S6 | AskDialog | interaction questionPanel | DialogFooter | Enter 误选防护/Other | `ask.png` |
| S7 | ComposerShell | session + home composer | Composer 权限模式（已有） | Enter/Shift+Enter/Esc/排队 | `composer.png` |
| S8 | RunStatus | session 状态/队列/用量 + toasts | — | 不伪造进度/重试/用量 | `run-status.png` |
| S9 | TeamSurface | rightpanel agents/tasks | 团队语义（已有测试） | RunState 真实/指派/确认 | `team.png` |

**完成定义**：每个表面迁移后，旧实现删除或只保留数据/业务逻辑；现有测试全绿；新增入口测试 + 快照；[DESIGN.md](DESIGN.md) §6.3 状态从「待建」改「已有」；[DESIGN-LINT.md](DESIGN-LINT.md) 对应 DS-04 白名单更新。

**OS 沙箱的「已隔离即免问」由 SDK 判定（2026-11-20，rig v0.26.1；同日改为个人客户端口径）**：shell 调用能否跳过审批不再由产品自己判断「runner 可用 + 工具名是 bash/pwsh」，而是把**当前 run 的 runner 与策略**交给 SDK 的 `gate.AllowSandboxedCommands`：它要求「shell 类工具 + runner 自报的强制程度不是 none + 策略不是 danger-full-access」，满足才免问。产品只提供两个事实（`contracts.SandboxService.Runner()` 与 toolkit 提供的 `contracts.ToolPolicy.PolicyFor(root)`，后者按**本次 run 绑定的工作区根**取那次构建的 `sandbox.Policy`——不是"当前工作区"那份，避免多工作区下判定与执行用的是两份策略），不再加自家模式规则：**全局只读预设按 SDK 语义执行**——挂了 runner 时命令照跑（写由 runner 围栏），没有 runner 时策略直接拒绝（工具报 `shell commands are disabled in read-only mode`）；**会话/自动化级的只读覆盖仍是硬拒绝**（不弹窗、直接 deny，由 `plan_mode.go` 的会话覆盖分支负责，见权限双层披露）。宿主没有可用 runner 时 `Runner()` 返回 **nil**（个人客户端"尽力而为"，不以启动失败或全量命令失败作为代价），命令以宿主权限运行、只受应用层策略与审批约束；设置页状态行如实说明这一点。`never` 档仍然一律拒绝（headless 显式 deny-all），plan/minimal/creator 白名单与会话级权限覆盖仍在最前。测试：`agent/sandbox_gate_test.go`（enforcement=none / 无策略 / danger-full-access 都必须回到审批；只读 + runner 免问、只读无 runner 询问）。

**只读免审批工具表以 SDK 为唯一来源**：`contracts.ReadOnlyAutoAllowedTools` 由 `gate.SandboxReadOnlyTools` 派生（SDK 的判据是「是否触及用户的文件/进程/网络」），产品只保留两张显式增量表——`productReadOnlyRemovals`（当前仅 `memory`：只读档不得改持久记忆）与 `productReadOnlyAdditions`（产品自有只读工具：skills/todo/team_task 查询/LSP 查询）。`contracts/permissions_sdk_test.go` 钉住「SDK 新增工具会自动进入只读集，除非显式列入 removals」，两处白名单不再可能漂移。

**`undo`（2026-11-21 接入）**：每次 run 开始时 harness 为该 run 工作区拍快照，模型可用 `undo` 回退本轮或指定轮次/单文件（`dry_run` 预览）。快照存储放在**产品数据目录**（`<dataDir>/undo/<root 哈希>`），不在工作区内——工作区内的镜像会被文件工具读到隐藏路径、让 glob/grep/search 结果翻倍，也可能被改写后由 undo 写回真文件。后端判定用 `undo.IsGitRepo`（git worktree/submodule 的 `.git` 是文件，仍走隐藏提交）；非 git 用文件后端（每回合复制工作区，保留 16 回合，个人客户端可接受）。**只读构建不建 tracker、不注册 `undo`**（它通过 tracker 直接写盘，不经过 `sandbox.Policy`）；工作区预设自动放行（与 write_file/edit_file/apply_patch 同档），plan/minimal/creator 不包含它；隐藏路径只做尽力而为的排除：git 后端把能归约成字面量的隐藏模式（`**/.env`→`.env`、`secrets/**`→`secrets`）作为 undo 忽略规则传下去，这类文件既不报告也不回退；带通配符的基名（`**/*.pem`）无法归约（SDK 的忽略规则不做 glob），非 git 的文件后端也没有忽略选项，因此这些情况下 undo 仍可能恢复/删除被隐藏的文件——隐藏列表本来就不是安全边界（shell 可绕过），这里只保证「不额外扩大」。另注意：SDK 的忽略规则里「无 `/` 的规则按任意层级同名元素匹配」，所以 `secrets/**` 归约出的 `secrets` 会把嵌套可见目录（`other/secrets/x`）也排除——方向是少回退，不是多暴露。快照失败只记日志，不影响 run；**已知限制**：同一工作区根下多个会话/运行共用一个 tracker，`undo` 无参数时回退最近一个回合（可能是另一个会话的），需要精确回退时显式传 `turn`；文件后端不设总量上限（工作区大小 × 保留回合数）。

**超大工具结果的读回（2026-11-21）**：toolkit 在数据目录建 `spillfile` store 并同时提供给 agent（`SpillPolicy.Store`）与 `spill_read` 工具；超过 32 KiB 的结果变成 `ref: <id>` 句柄，模型用 `spill_read {"id": …}` 读回（**单次上限 8 KiB、无分页参数**——需要更多内容时让命令本身缩小输出；文件后端时代可按 offset/limit 分页读回的便利不再存在），按会话作用域回收。`spill_read` 在 plan/minimal/creator 白名单里，且不受 preset 工具白名单收窄（它是运行时读回能力，否则溢出结果会变永久不可读）。store 在数据目录、不在工作区，因此不会污染文件面；没有数据目录的装配回落文件后端（原路径不变，`spill_read` 不注册）。

**新增 SDK 工具的两个已知边界（2026-11-21 审计结论，选择显式记录而不是加适配层）**：
- `apply_patch` 与 `search` 的相对路径**以工具根为基准**，不跟随 `working_directory`（SDK 的这两个构造器没有 cwd 选项；其余文件工具都跟随）。模型先 `working_directory("docs")` 再 `apply_patch` 相对路径时落点是 `<root>/x` 而不是 `<root>/docs/x`——这是 SDK 语义，不是越权；需要按会话 cwd 操作时用绝对路径或先 `read_file` 确认。
- `apply_patch` 的改动**不进回合尾的"已编辑 N 个文件"卡**（该卡按单文件 `path` 参数聚合，而补丁一次涉及多文件）。补丁结果在工具结果里逐文件列出。
- `request_sandbox_access` 的授权是**工作区构建级**（`sandbox.Policy` 上的 grant），不是对话级：换工作区/换权限重建策略会丢掉它，切回同一 (root, mode, runner) 的保留构建会复用同一 policy；审批文案由产品渲染为「路径 — 理由」，并且该工具**不在任何免审批集合**里（必须人工批准）。
