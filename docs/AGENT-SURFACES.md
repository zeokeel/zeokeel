# Agent 客户端入口组件规格（AGENT-SURFACES.md）

> 本文是 [DESIGN.md](DESIGN.md) §2「场景 → 组件硬映射」的展开规格：**Agent 客户端 9 个 surface 的入口组件契约**。
> **当前状态（2026-11-10）：S1–S9 全部已有入口实现；S9 从右侧栏 TeamSurface 改为会话内的 agent surface（PopoverBase 团队浮层 + 主栏子会话视图）。** 每节保留目标 API 草案（作为契约）、最终实现位置、迁移步骤与验收（迁移步骤仅作历史记录）。
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
| error | 失败轮显示错误行 + 重试；元信息行只保留时间/用量，不提供 ⋯ 动作。`ErrorKind=iterations` 显示「已达最大迭代次数」+「打开模型设置」+ 提高上限建议（不再以 rig 英文串为标题）；`ErrChatCompacting`/`ErrCompactConflict` 在发送失败时本地化（`会话正在压缩，请稍后再试` / `压缩期间历史被修改，已取消`） |
| truncated | 最后一条 assistant 的 `Truncated=true` 时在回复末尾显示可折叠「回答被截断」提示（展开说明输出上限与两种处理方式）；非最后一条不显示 |

**会话壳层（B3 顶栏 + composer dock，2026-11-10，DSH 对齐；2026-11-11 Agent Run Band）**：标题行顺序固定为 **标题/面包屑 → Agent Run Band（运行中 → 智能体团队 → 子智能体 N → 后台任务 → 待审批）→ spacer → ⋯**（`screen.header` / `screen.actions` / `screen.tabs` 槽位，40px 标题行 + 32px Tabs 行，浅色 header 面、仅底边一条 hairline、行间无线、无返回按钮、无 `Alt+←` 历史回退）。Band 是标题后的**一行原生入口按钮**（不再外套第二层容器：官方按钮自带描边，外套容器会形成"控件套控件"的双重描边；只用间距分组），不是卡片；团队/子代理是关键功能入口，**零值也常显**但**零值不显示数字 0**（tooltip/无障碍名仍带精确计数），后台任务与待审批只在有内容时出现；只有团队/子代理后端都不存在且无运行/任务/审批时整条不渲染。三个 agent 入口都是 `ui.PopoverBase` 浮层（团队：成员 + 共享任务；子智能体：树/列表切换；后台任务：状态/命令/用时/停止），不占右栏。进入子智能体或团队成员时，主栏切到子会话：标题变成 `主标题 › 子智能体名` 面包屑，点主标题返回父会话；一次性子智能体只读，可继续成员有输入框（`ChildAgentService.SendChildAgent`）。回合尾部新增文件卡：「交付文件」（write_file）与「已编辑 N 个文件」（edit_file/str_replace_editor），每行「在侧边栏预览 / 查看改动 / 用系统应用打开」，预览打开右栏文件标签。目标条（Badge + 目标文本 + ⋯「目标操作」）与审批内联卡在 composer 上方 dock（阅读列宽度居中，`session.banner` 槽），不占 transcript 空间、不遮挡输入。通知/ toast 固定在右上角、B3 顶栏下方（最多 2 条，自上而下堆叠），不遮挡标题/Tabs/顶栏动作。右栏是默认收起的多标签页面：开始引导（工作区文件 / 新建终端两张等宽卡片，无说明文字）+ 文件 + 终端 + 每个打开文件一个可关闭标签；页面内容一律可滚动。

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

**迁移步骤**：① 把 `buildChatPane` 的 List 配置/空态/回到最新抽成 `uikit.SessionThread`；② 消息体渲染保留为可插拔 `func(*ui.Context)`（业务 slot）；③ 用现有 transcript/scroll 测试作回归；④ 新增快照 `uikit-session-thread.png`。

**验收**：`TestSessionThreadVirtualizesLongConversation`、`TestSessionThreadFollowAndBackToLatest`、`TestSessionThreadDefaultRows`（uikit）；流式隐藏动作由业务 Row 实现，由 session `TestStreamingMessageHidesActions` 覆盖；+ 快照。

---

### S2 ReasoningTrace（推理 / 思维链）

**职责**：把模型推理作为**次级折叠内容**展示：默认不抢主视觉；streaming 时展开；结束后收起为一行摘要。

**数据模型**：`reasoning string`、`streaming bool`、`open bool`（按 message ID 持久化）。

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

**当前实现**：`internal/ui/session/view.go` `buildReasoningDisclosure`（`ui.CollapsibleBase` + muted markdown + Spinner + 行数 + `thinkOpen`）。

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

**布局**：紧凑行（图标/名/状态/单行预览/chevron）用于普通调用；决策类工具在 transcript 用完整卡（图标+名+状态+步骤+详情）；轨迹 pane 复用同一卡。详情段统一：输入（命令/路径/前后内容/JSON）→ 输出；可复制；mono 8 行上限 + 截断提示。团队类工具不再提供跨模块 footer：团队/子代理从会话头部入口进入，工具卡只保留输入/输出与状态。

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

**布局（固定，V3 强调）**：内联卡（`Border(1, Warning)` + `RadiusLG`）→ 顶部警示横幅（Shield + 加粗「需要审批」+ 工具友好名 + 队列计数 + 「Esc 拒绝」，`Warning.Alpha(0.22)` 底、`WarningText` 前景）→ 中性卡体：**来源行（有 `SessionID` 时：会话标题 + 来源 + 跨会话「打开该会话」）** → 一行影响声明 → 摘要优先（shell/终端类工具标「命令」，其余工具标「摘要」；mono 框）→ 「参数」折叠（Scroll 140）→ 会话级允许提示 → `uikit.DialogFooter`：**拒绝 → 本会话内始终允许 → 允许(Primary 最右)**；无 Default、无 AutoFocus。卡片整体用警示色描边/横幅，和 transcript 工具卡、composer 明显区分，不允许被看漏或误认。

**交互/键盘**：Tab 循环；Enter 只有在焦点落到某按钮时才触发该按钮；Esc = deny 最早一条（有文本焦点时也由 session 的审批优先路径处理，且不中断本轮）；内联模式没有 backdrop。不设默认焦点。

**a11y**：卡片是命名 region（「需要审批 · <工具>」）；危险图标不只靠颜色；影响声明可读；状态词明确。

**禁止**：默认焦点落在允许或拒绝（真实事故：发消息后 Enter 误触拒绝）；普通 Button + 中性文案；把审批塞进 `AlertDialog`（末位默认语义）；把队列叠成多层 Modal；审批动作图标化无文字。

**当前实现**：`uikit.ApprovalDialog`（`Inline` 模式为产品默认的 V3 内联卡；`Inline=false` 保留旧 Modal 渲染给 embedder/兼容）；`internal/ui/approval/approval.go` 注册 `session.banner` 槽、维护 FIFO/settle 状态并在非会话屏自动切回会话。测试：`internal/ui/approval/*_test.go`（inline 渲染/队列计数/三种决定/自动切屏）、`internal/uikit/approval_test.go`、`internal/ui/session/chrome_b3_test.go`（Esc 审批优先）。

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

**数据模型**：`draft string`、`attachments []Attachment`、`running bool`、`queue []string`、模型/权限/模式/人设选择、模型与推理等级（`modelcatalog.models` 的模型列表与 `DefaultThinking`）、会话×模型推理等级覆盖（`session.<id>.thinking.<modelID>`，默认跟随该模型默认值；旧 `session.<id>.thinking`/`session.<id>.effort` 仅作旧会话兼容回落，任何一次显式选择都会清掉旧行）、`focusComposer`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| idle | 输入 + 工具条 + 发送（Primary）；未配置模型时发送禁用并在 composer 内显示原因 + 「打开模型设置」（FR-31） |
| running | 输入仍可编辑；发送位变「排队」+「停止」；Esc 中断（菜单优先）；提示文案说明 Enter 语义 |
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
    Toolbar     func(c *ui.Context) // 附件/模型/权限/模式/推理/人设
    Send        func()
    Stop        func()
    OnKey       func(ui.TextKey) bool
    Placeholder string
    Focus       *bool
    Error       string
}
func ComposerShell(c *ui.Context, o ComposerOptions)
```

**布局**：composer 上方 dock（阅读列宽度居中、`session.banner` 槽）：目标条（有目标时）→ 审批内联卡（有 pending 时）→ 卡片 `t.Composer()`；输入 `ui.TextArea`（MinHeight Space9、MaxHeight Space8*6、边框透明、聚焦 accent 边）；工具条一行（附件菜单 + 模型 chip + 会话设置菜单；窄宽度折叠低频 chip 进菜单，veryCompact 时模型 chip 折进会话设置菜单并保留「模型：…／推理等级」回退入口）；右侧发送/停止固定，**不进溢出菜单**；键位提示在宽 composer 显示、窄 composer 放 tooltip。模型 chip 打开**两级紧凑菜单**：一级只有「模型」「推理等级」两行（Trailing 当前值）+「打开模型设置…」；catalog 已注册但列表为空、而 `Config().Model` 非空时（装配默认模型，FR-26），chip 文案为「<model>（默认模型，尚未配置连接）」并在菜单顶部加一行「默认模型来自装配，添加模型行后可覆盖」；「模型」二级按服务商分组（`MatchProviderPreset`，未匹配“自定义服务”）列出模型；「推理等级」二级为「跟随模型默认（…）」+ 该模型模式；模型级 `DefaultThinking` 在 Home composer 的「推理等级」与设置页编辑弹窗配置。

**交互/键盘**：Enter 发送、Shift+Enter 换行、Cmd+Enter 发送；运行中 Esc 中断；slash/@ 菜单优先级最高；Tab 顺序：输入 → 菜单项 → 发送 → 停止。

**a11y**：输入有 Label；图标按钮 Label+Tooltip；运行态 `RoleStatus`/`Announce`；队列 Badge 可读。

**禁止**：两个 composer 各自维护宽度/发送样式；把发送藏进 Toolbar 溢出；在 build 里做 I/O；每帧 `input.Focus()` 对抗失焦（用 `c.Key`/FocusBind）；伪造"已发送"。

**当前实现**：会话 `internal/ui/session/view.go` `buildComposer`（含 queue/stop/slash/@/Enter 语义）；首页 `internal/ui/home/composer.go`。共用 `design.Tokens.Composer()`。差异清单见 [DESIGN.md](DESIGN.md) §1.3 与 ui-inventory 报告 §C6。

**迁移步骤**：① 抽 `uikit.ComposerShell`（卡片+输入+工具条槽+发送/停止槽+错误行）；② 会话/首页各自提供 toolbar slot 与发送逻辑；③ 统一宽度（`design.TranscriptWidth`/容器宽）与发送控件外观；④ 帧测试覆盖 Enter/Shift+Enter/运行中 Esc/排队/附件路径对话框。

**验收**：`TestComposerEnterSends`、`TestComposerShiftEnterNewline`、`TestComposerEscapeInterrupts`、`TestComposerQueueDuringRun` + 快照 `chat-composer.png`。

---

### S8 RunStatus（流式 / 队列 / 停止 / 用量 / 恢复）

**职责**：把 Agent 的运行状态如实、克制地表达出来：正在生成、排队中、可停止、失败可重试、用量可查；不伪造进度、不推动布局。运行中的过程由 S1 的「工作步骤」单行/时间线呈现（默认标准模式一行原地更新，不铺完整时间线）；运行中需要审批时，输入框上方的内联审批卡是唯一阻断点（不弹模态、不遮 transcript）；通知类消息走右上角 toast（B3 顶栏下方，最多 2 条），不遮挡输入与顶栏动作。

**数据模型**：`running`、`queued []string`、`usage`（token 计数）、`lastError`、`retryable`。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| idle | 不显示运行态 |
| streaming | 最后消息尾部 Spinner + 状态文本；输入区停止按钮；`RoleStatus`/Busy；composer 键位提示为「Enter 排队 · Esc 停止」；**有待审批时改为「Esc 拒绝审批（再按停止）」**，与审批卡的「Esc 拒绝」一致（PM-12） |
| queued | 队列 Badge（可展开、可取消/重试）；排队只发文字提示 |
| stopping | 停止按钮 pressed/disabled；不可重复触发 |
| failed | 错误行 + 重试（带标签按钮，不是图标） |
| queue-send-failed | 投递失败：错误行 + 队列暂停，排队浮层里显式「重试」；不得逐帧自动重发或每帧请求重绘 |
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

**布局**：状态行在最后消息之后（消息内）或 composer 上方（运行态）；消息用量收进消息元信息行（时间 · 用时 · 紧凑 tokens · ⋯ → 用量明细子菜单，不再单独占 footer 行）；错误行贴近失败内容；toast 只做通知，不替代可操作错误。

**禁止**：伪造进度条；把"停止"和"发送"并排同权重；运行中每帧重排布局（几何稳定）；用常驻 badge 代替 toast/错误；用 `ui.Toggle` 表达忙碌状态。

**当前实现**：session 生成中 Spinner/文本、queue Badge/popover、stop 按钮、消息元信息行（时间 + `FormatCompactCount` 紧凑用量 + ⋯ 用量明细）、`buildRetryAction`；toasts 模块（`c.AddToast` + `ToastViewportBase`，2 条上限 + 合并 ×N）。测试：`queue_send_test.go`、`usage_test.go`、`toasts_test.go`。

**迁移步骤**：抽 `uikit.RunStatus`（状态行/队列/停止/重试/用量）；保留通知通道不变；帧测试覆盖排队/停止/重试/用量折叠。

**验收**：`TestRunStatusDoesNotFakeProgress`、`TestRunStatusQueueRetry`、`TestRunStatusUsageCollapsed` + 快照 `run-status.png`。

---

### S9 会话 agent surface（多 Agent / 团队 / 任务）

**职责**：把子代理与团队成员作为**子会话**呈现：头部入口、团队浮层、主栏子会话视图（面包屑返回）、共享任务；运行态必须真实（`RunState`），不得把 `active`（可用性）画成 running。**团队/子代理不再放进侧边栏或右栏页签。**

**数据模型**：`contracts.ChildAgentService`（`ChildAgents(parentSessionID)` / `ChildAgent(id)` / `SendChildAgent`；子代理=one-shot，队友=continuable）、`TeamMember{RunState,SessionID,Historical,...}`、`ChildAgent.Historical`（持久历史的只读行）、`TeamActivity`（有界）、`BoardTask{Status,Owner,BlockedBy,WriteScopes}`、`TaskAssigner`（可选）。

**持久子会话（2026-11-11，DSH 对齐）**：teammate 与同步子代理共用一套稳定身份——创建时分配 `child-<8 hex>` 的 child-session ID，团队 member ID、子智能体 ID 与「已结束」历史行是同一个值；rig manager 的 `member-N` / 子代理注册表 ID 只在后端解析。记录写入 `<数据目录>/child-sessions.json`（v2；有界 64 条 + 14 天 TTL + 原子写 + 损坏隔离；自动迁移旧 `child-agents.json`），spawn 时即写、完成/失败/中断/关闭/退出持续更新。重启后恢复为 `Historical` 只读行：团队浮层「已结束」区（带「历史」标记）、子智能体切换器/子会话照常打开，但无输入框（`SendChildAgent` 拒绝）。历史按 `SessionID` 归属到当初的会话：页头计数与团队浮层只列当前会话的历史，运行中队友仍是进程级可见。会话浮层的共享任务按会话归属、缓存优先读取（缓存未命中只做一次只读文档加载，不迁移/不写；无活动会话时不回退全局板）。共享任务板按会话归属（DSH 的 Lead-session 语义）：`team_task_*` 携带 run-scoped 会话上下文，每个会话持久化到 `<数据目录>/team-tasks/<session>.json`，旧 `team-tasks.json` 首次使用自动迁移；会话 UI 只列当前会话的任务，`KeyTaskBoard` 聚合视图留给无会话上下文的宿主。子代理/队友完成时，父会话的 transcript 显示一行只读完成回执（`ChatRoleNotice`，由 child-session 记录派生）；队友完成还会以 user 消息注入下一轮父会话模型上下文（每个队友一次，`Notified` 持久化；同步一次性子代理的答案已在工具结果里，不重复注入）。

**状态矩阵**：

| 状态 | 表现 |
|---|---|
| running | 运行中 Badge/活动行；团队浮层显示运行点 |
| idle | 空闲；可向可继续成员发消息 |
| done/completed | 完成 + 可查看产出 |
| failed | 失败 + 错误原因 |
| interrupted | 已中断（非失败） |
| one-shot | 只读：无 composer，不显示「发送」 |
| task blocked | 依赖未完成：显示 blockedBy，状态不可推进 |

**入口结构**：

- 会话头第一行（`screen.header`/`screen.actions` 槽）：`标题/面包屑 → Agent Run Band（运行中 → 智能体团队 → 子智能体 N → 后台任务 → 待审批）→ spacer → ⋯`；Band 是一行原生入口按钮（无外层容器/双重描边），团队/子代理零值常显但不显示 0 计数，后台任务/待审批只在有内容时出现；各入口都是 `ui.PopoverBase` 原生浮层（Esc/点外关闭/焦点归还），不占右栏。
- 团队浮层：成员卡（状态点/名字/模型/最新活动/「打开」）+ 共享任务卡（subject/状态/owner/依赖/写入范围）；空态说明「暂无共享任务」；已结束的持久记录按「已结束」分组展示（历史标记，只读）。
- 子智能体浮层：子代理/队友列表；选中进入主栏子会话。
- 后台任务浮层：`JobService.Jobs()` 的行（状态/命令/用时/退出码）+ 停止（`KillJob`）。

**布局与交互**：

- 子会话在主栏渲染：标题变为 `父标题 › 子智能体名` 面包屑，点父标题返回父会话；内容区显示任务（prompt）、状态、输出与活动时间线；可继续成员底部有消息输入框（`SendChildAgent`），一次性成员只读；重启后恢复的历史会话同样只读并明示「已结束的记录（应用重启后仍保留）」。
- 团队浮层只读，成员/任务数据来自 `TeamService`/`TaskBoard`；任务完成/指派由工具或服务调用完成，浮层在打开时读取一次任务快照（关闭重开刷新）。
- 头部窄带（阅读列 < 2/3）退化为图标+计数，可及名保留全称。
- a11y：入口按钮 Label 为全称；状态文案可读（不只靠颜色）；浮层为 native popover/dialog 语义。

**禁止**：把团队/子代理塞进侧边栏或右栏常驻页签；`active` 当 running；伪造子代理活动时间线；一次性子代理显示可发送输入框。

**当前实现**：`internal/ui/session/agentsurface.go`（三入口 + 三个 Popover 浮层 + 子会话视图 + 回合尾文件卡）、`internal/modules/agent/childagents.go`（统一 `ChildAgentService` 读模型）、`internal/modules/agent/child_sessions.go`（持久子会话 `child-sessions.json`）、`internal/modules/agent/child_notices.go`（完成回执：下一轮模型上下文，仅队友）、`internal/ui/session/view.go`（transcript 的 `ChatRoleNotice` 回执行：live 按时间合入、运行中放在流式回答上方；重启恢复的消息无时间戳时按 `Finished` 追加）、`internal/modules/toolkit/jobs_service.go`（`JobService`）。旧 `internal/uikit/team.go` / `rightpanel` 团队/任务 pane 已删除。

**验收**：`internal/ui/session/agentsurface_test.go`（帧测试：header 顺序、团队浮层、switcher、子会话面包屑/返回、双向不可用、jobs 停止、文件卡预览回调、历史分组/只读历史会话）、`internal/modules/agent/childagents_test.go`、`internal/modules/agent/child_sessions_test.go`（重启后同一身份、文档往返、v1 迁移、损坏隔离、保留上限）、`internal/modules/agent/child_notices_test.go`（回执只注入一次、失败重试、跨重启/Close 重写、跨会话归属）、`internal/modules/toolkit/jobs_service_test.go`、uimodtest 新 E2E（团队浮层 / 文件页 / 终端页 / 快捷键）、`ui-drive` team 步骤（含 `team-history`）。

## 2. 跨 surface 规则

- **顶栏只有一块**：屏幕通过 `screen.header` / `screen.actions` / `screen.tabs` 槽位把标题/动作/Tabs 交给宿主 B3 顶栏（浅色 header 面 + 仅底边 hairline、行间无线），屏幕 body 不再叠第二行 header；无贡献的屏幕没有空工具栏；不提供返回按钮或 `Alt+←` 历史回退（侧栏即导航）。
- **输入框上方的 dock**：目标条与审批内联卡固定在 composer 上方（阅读列宽度居中），是会话页唯一的阻断/目标区；不把目标放回顶栏、不把审批做成居中 Modal。
- **会话级 vs 全局设置**：会话设置菜单里「权限模式（所有会话）」「对话模式（所有会话）」的组标题带后缀，明确这两项作用于所有会话；人设与「本会话内始终允许」保持会话级（PM-14）。
- **记忆 scope**：`workspace:<id>` 绑定作用域的条目在会话「记忆」页签显示「工作区（本会话项目）」，不展示存储原值；设置→记忆是跨工作区管理页，保持「工作区」标签。
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

记忆 Tab 与设置「记忆」「技能」页不是上述 9 个 Agent surface，但读写同一批 contracts 服务；改动它们时保持以下事实一致：

- **记忆 scope**：条目保存工作区身份（`workspace:<id>`），`List/Search` 只返回 global + 当前工作区条目；管理页用可选 `MemoryAllLister.ListAll` 看全量。默认 scope 只由 `memory.Config.Scope` 决定（工具/设置页/提取省略 scope 时走它）；会话「记忆」Tab 显式写当前工作区。
- **记忆恢复**：`memory.json` 损坏时隔离为 `.corrupt-<ts>` 并以空 store 继续；`MemoryRecoveryReporter.RecoveryNotice()` 必须显示在记忆页（不能只写日志）。
- **技能根**：优先级为工作区（`skills`、`.agents/skills`、`.zeokeel/skills`）→ `skills.dirs` → assembly 目录 → `dataDir/skills` → `$ZEOKEEL_SKILLS` → `~/.zeokeel/skills`；同名遮蔽与扫描上限经 `SkillWarnings.Warnings()` 在技能页可见。
- **技能内容边界**：`List` 只读每个 `SKILL.md` 的前缀（名称/描述/可搜索摘要），`Read`/`load` 才读全文（≤1MiB）；编辑只重写名称/描述/正文，保留未知 frontmatter 键；写路径仍仅 `<dataDir>/skills`。

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
| S7 | ComposerShell | session + home composer | Composer 预设（已有） | Enter/Shift+Enter/Esc/排队 | `composer.png` |
| S8 | RunStatus | session 状态/队列/用量 + toasts | — | 不伪造进度/重试/用量 | `run-status.png` |
| S9 | TeamSurface | rightpanel agents/tasks | 团队语义（已有测试） | RunState 真实/指派/确认 | `team.png` |

**完成定义**：每个表面迁移后，旧实现删除或只保留数据/业务逻辑；现有测试全绿；新增入口测试 + 快照；[DESIGN.md](DESIGN.md) §6.3 状态从「待建」改「已有」；[DESIGN-LINT.md](DESIGN-LINT.md) 对应 DS-04 白名单更新。
