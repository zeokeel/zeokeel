# 会话 / Agent 表面 UI-UX 升级审计（v0.2.37，2026-11-15）

> 范围：transcript、工具行/工具卡、推理、审批、计划/提问、composer、运行态、goal dock、inbox/队列、Context 页签、中断恢复、冷恢复、团队/子代理 Run Band 与浮层。
> 证据：代码 `file:line` + `.cache/ux-review/shots/` 快照（120 PNG，HEAD `1f66af3`，2026-11-15 04:15 生成；manifest 记录 `changed files: 0`）。本次为**只读审计**：未改代码、未跑 build/test/designlint。
> 定位：只找「功能已存在、体验未闭环」的升级面；契约违规只列会阻塞升级的条目（见 §2.2）。不与 02（壳层/IA）、03（系统质量）报告重复收口。

---

## 1. 结论摘要

1. **状态呈现已经基本可信，控制与定位仍断链**：goal dock、Run Band、inbox、中断提示、冷恢复都有真实数据源与帧测试，但队友/子代理**没有停止/关闭入口**（契约早已有），目标**没有零值入口**，查找命中**无法定位**。
2. **最关键的决策面仍信息不足**：write/edit 审批只有摘要 key:value + 折叠原始参数，**没有 diff 预览**；而它旁边就有可复用的官方 `editor.DiffView`（文件页已用）。
3. **底部 dock 没有高度预算**：审批卡（实测 ≈320 DIP）+ composer（≈137）+ 状态条 ≈487 DIP，在 900×600（host 最小宽口径）下 transcript 会被挤到 ≈0——目前**没有任何 900×600 证据帧**。
4. **恢复/历史会话的阅读体验退化**：restore 路径 `messageTime` 恒为零值，每轮元信息只剩一个孤立 `⋯`（无时间、无用时/用量），「可信但读不下去」。
5. **结构化事件仍在降级成纯文本**：工具行折叠预览直接吃后端英文原文（如团队任务 `created shared team task id: task-1 subject: …`），子代理完成回执是一行不能点击的死链文本。

---

## 2. 能力现状 vs 体验现状

### 2.1 表面盘点

| 已落地能力 | 当前 UI 是否闭环 | 缺口 |
|---|---|---|
| Goal v2 会话级目标（dock：phase/进度/blocked 原因/继续门/清除确认） | **基本闭环**：`p2-goal.png`、`p2-goal-blocked.png`、`p2-goal-paused.png`、`p2-cold-resume.png` | 只在「已有目标」时可见；创建/编辑入口仅在命令面板（P2-6）；状态词与运行态撞词（P3-3） |
| 后端 PromptQueue / inbox | **闭环**：`p2-inbox.png`、`p2-inbox-popover.png`；排队 badge 展开可看顺序/移除/清空，运行中「排队/停止」与键位提示一致 | 无阻塞项（Stop 后不自动 drain 已有英文/中文提示） |
| 中断恢复提示 | **闭环且诚实**：`p2-interrupted.png` / `p2-interrupted-dark.png` 顶部 pin 条 + 继续/忽略 | 「继续」只聚焦输入框且无可见反馈（P3-1） |
| 团队/子会话冷恢复 | **闭环**：历史·可继续/只读分组、继续入口、子会话输入框、「已发送，等待回复」；`p2-cold-resume.png`、`p2-cold-resume-child.png` | 主会话完成回执不能跳到子会话（P2-5）；无暗色帧 |
| Run Band（运行中/团队/子代理/后台/待审批） | **闭环**：零值常显不显示 0、后台/审批有内容才出；`p2-run-band.png`、`p1-approval.png` | 团队与子智能体把同一队友计两次（P3-2）；队友不可停/关（P1-2） |
| Context 页签（概览/分段/调用/事件） | **数据闭环、诚实**（未知不伪造 %，无价显示 —） | 排版未闭环：与对话不同轴、KV 值甩到最右、分组节奏弱（P2-7）；列头 i18n（P3-6） |
| 工具行 / 过程折叠 | **闭环**：状态 gutter、折叠摘要、展开时间线；`p2-tool-row-states*.png`、`p2-tool-row-complete.png` | 折叠预览是后端原文（P2-4） |
| 审批 inline 卡 | **契约闭环**（无 Default、Esc=deny、队列计数、来源行、影响声明） | 决策信息不足：无 diff（P1-1）；卡片高度无预算（P1-3） |
| 计划审阅 / 提问 | **闭环**：`p2-plan.png`、`p2-ask.png`（Esc 语义、空反馈不批、等权选项） | 无本轮项 |
| Composer（附件/粘贴/拖放/@/slash/模型/权限/排队/停止） | **闭环**：`p2-attachments.png`、`p2-drop.png`、`p2-slash.png`、`p2-running-composer.png`；900 宽下 chip 折叠有测试 | 无阻塞项 |
| 恢复会话的历史 transcript | **诚实但退化**：restore 后消息无时间/用量 | 每轮元信息裸 `⋯`（P2-1） |
| 轨迹页 | 能看，不好用 | 不跟随流式、无「回到最新」、计数行错轴、无回合分组/定位（P2-2） |
| 查找/定位 | **未闭环** | 无页内 FindBar；侧栏消息命中点击不定位（P2-3） |
| 子代理完成回执 | **半闭环** | 有回执、不可回链（P2-5） |

### 2.2 会阻塞升级的契约/缺陷（不展开其余违规）

| 阻塞点 | 证据 | 阻塞项 |
|---|---|---|
| `TeamService.Interrupt/Close` 已存在但 UI 零调用；S9「团队浮层只读」与之冲突 | `internal/contracts/team.go:147-150`；`internal/ui/session/agentsurface.go:612-665`（成员卡只有打开/继续）；AGENT-SURFACES S9 | P1-2 |
| `MessageSearchHit` 没有 messageID/位置，命中无法定位 | `internal/contracts/search.go:10-21`；`internal/ui/sidebar/sidebar.go:876-881` | P2-3 |
| restore 路径 `messageTime` 恒为零值 → 元信息无法根治 | `internal/modules/agent/agent.go:3097`；`internal/ui/session/view.go:1606` | P2-1 |
| inline 审批卡无高度约束（root 只 `FillWidth`） | `internal/uikit/approval.go:170-178` | P1-3 |

---

## 3. 升级机会（按影响排序）

### P1

#### P1-1 审批缺 diff：唯一不可逆决策却是「盲批」
- **问题**：write/edit 审批的「摘要」是 `key: value` 原始参数，下面只有折叠的「原始参数」；用户看不到将写入/改动什么。与「让用户看清影响」的 S4 目标不符。
- **证据**：`p1-approval.png`（摘要框 `subject: 检查发布清单 / description: …`，无 diff）；`internal/uikit/approval.go:220`（摘要段调用）、`:527`（原始参数折叠）、`:409-431`（键值白名单，仍是原文）；可复用能力已在仓库内：`internal/ui/rightpanel/changes.go:179`（`editor.ParsePatch` + `editor.DiffView`）；`docs/INTERACTION-DEPTH-ROUND.md:172` 仍列为开放项。
- **建议**：在「摘要」与「原始参数」之间加一个「将改动」折叠块（write/edit 默认展开）：write_file 由 arguments.content 生成全新增 diff；edit_file/str_replace_editor 由 old_string→new_string 合成 unified patch 后走 `editor.ParsePatch + DiffView`；无法合成时回退为 bounded mono 内容预览（复用 `toolcards` 的 `boundRunes/truncate`），块头显示路径与 `+N −M`。footer、无 Default、Esc=deny 一律不动。
- **改动面**：`internal/uikit/approval.go`（新 section + options 字段）、`internal/ui/approval/approval.go`（生成/传递 diff，文件读取走缓存 + `UIRuntime.Post`，禁止 build 内 I/O）、`internal/ui/toolcards`（复用 helper）；同步 AGENT-SURFACES S4。
- **验证**：`internal/uikit/approval_test.go` 帧测试（write/edit 出现 diff 行、patch 不可解析回退、空参数不渲染、仍无 Default）；快照 `p1-approval.png` + 新增 dark；`designlint --strict`。
- **风险/依赖**：合成 patch 的边界（超大文件、二进制）需上限；回退必须诚实。工作量 **M**。

#### P1-2 长任务里队友/子代理不可控（能看见，停不掉）
- **问题**：Run Band 能显示「智能体团队 N 运行中」，团队浮层成员卡却只有「打开/继续」；后台 job 有「停止」（KillJob），同类控制不一致；子会话也没有停止按钮。
- **证据**：`internal/contracts/team.go:147-150`（Interrupt/Close 已暴露）；`internal/ui/session/agentsurface.go:612-665`（成员卡唯一行内动作 `agentRowActionTooltip`）；`agentsurface.go:1241`（后台 job 可 kill）；`p2-team-overlay.png`；CHANGELOG 0.2.36 known limitation「运行中 overlay 无停止按钮」。
- **建议**：成员行在 `RunState=running` 时给「停止」（secondary，调 `Interrupt`，状态词走中性 cancelled，不画失败）；「关闭」收进成员行 ⋯ danger → `uikit.ConfirmDanger`（取消为 Default）。子会话 header 同位置给「停止」。入口结构不变，只补动作。
- **改动面**：`internal/ui/session/agentsurface.go`（成员卡/子会话 header，`uikit.IconButton`/`agentRowAction` 现成）；文档 S9「团队浮层只读」改为「只读数据 + 控制动作」并同步 DESIGN §2.1。
- **验证**：帧测试（running 行出现停止、点击调 fake `TeamService.Interrupt`、停止后 `interrupted` 中性、`Close` 走 ConfirmDanger 且取消 Default）；快照团队浮层。
- **风险/依赖**：这是明确的既有设计选择，需产品确认；Interrupt 失败用 `ErrorLine`；不把 `active` 画成 running。工作量 **M**。

#### P1-3 900×600 下底部 dock 没有高度预算（推算，待帧实测）
- **问题**：dock = goal 条 → 审批内联卡 → composer → 状态条，逐层累加、无人管总高。1512×944 帧实测：审批卡 ≈320 DIP（y457–777）、composer ≈137（y779–916）、状态条 ≈12 → dock ≈487 DIP；窗高 600 时扣除顶带 112 只剩 ≈488 → transcript ≈0；再加 goal 条（+≈40）或排队 badge 会溢出。
- **证据**：`p1-approval.png`（上述像素测量）；`internal/uikit/approval.go:170-178`（inline 卡 root 无 `MaxHeight`）；`internal/devtools/ui-snapshot/main.go:533,536`（快照只有 1024×720 与 1512×944，无 900×600）；`internal/uiroot/frame.go:27`（900 DIP 为 host 最小宽口径）。
- **建议**：给审批卡设高度预算（如 `MaxHeightPercent(60)`，或把「影响声明 + 摘要」放进有界 `Scroll`、横幅与 footer 固定），保证 transcript 可见高度；先在 900×600 帧上验证再定数值，窄窗摘要限 3–6 行。
- **改动面**：`internal/uikit/approval.go`；`ui-snapshot` 新步骤（900×600 + 审批 + goal + 排队）；可能 `session.buildComposerDock`。
- **验证**：帧测试 `SetSize(900,600)` 断言 transcript 高度 > 0、审批 footer 可点、composer 不重叠；900×600 快照。
- **风险/依赖**：不能牺牲决策可读性（参数本来折叠）；本条 900×600 结论为**推算**，先补帧。工作量 **S/M**。

### P2

#### P2-1 恢复/历史会话：每轮元信息退化成一个孤立 `⋯`
- **问题**：重启/冷恢复后消息既无时间也无用时/用量，元信息行只剩右上角一个 `⋯`；长恢复会话里形成一列无意义圆点，用户不知道每条是什么时候、多贵。
- **证据**：`internal/modules/agent/agent.go:3097`（`messageTime` 恒 zero）；`internal/ui/session/view.go:1605-1606`（`hasMenu` 不看时间、`hasTime` 仅看 CreatedAt）与 `:1621`（`⋯` 仍渲染）；`p2-interrupted.png`、`p2-interrupted-dark.png`（每轮仅 `⋯`）。
- **建议**：根治 = restore 回填 CreatedAt（durable `user_message`/`run_end` 已有时间）；UI 兜底 = 无时间且无用量时不渲染空元信息行，动作改为 hover/focus 出现或并入正文末尾（保持键盘可达）。
- **改动面**：`view.go`（`buildAssistantMetaContent`/`buildUserMessageMeta` 的 hasTime 分支）；`agent.go`（回填，由 Lead 决定是否本轮）。
- **验证**：帧测试用 `CreatedAt` 为零的 restored fixture 断言无裸 `⋯` 且动作仍可键盘到达；快照 `p2-interrupted*` 前后对比。
- **风险/依赖**：不能让动作入口真的消失（a11y）。工作量 **S**（UI 兜底）/ **M**（含后端）。

#### P2-2 轨迹页：承诺「跟随 + 回到最新」，实际都没有
- **问题**：注释写明 following the end + floating 回到最新，代码里 `trajList` 没设 FollowEnd、也没有浮层；计数行贴着面板左缘（x≈328）而卡片在阅读列（x≈417）；无回合分组/耗时/点击定位，卡片全是折叠态——长任务越用越难找。
- **证据**：`internal/ui/session/view.go:1974-1978`（注释承诺）、`:1984`（计数行 `PaddingX(Space5)`）、`:2011`（`ui.List` 无 FollowEnd/浮层）；`p1-trajectory.png`（像素：计数 x≈328 / 卡片 x≈417；整页仅一张折叠卡）；`docs/INTERACTION-DEPTH-ROUND.md:174`。
- **建议**：①`st.trajList` 设 FollowEnd + HoldPlace，并复用 SessionThread 的「回到最新」affordance；②计数行移进居中阅读列；③卡片头补回合序（时间字段若 read model 无则先只给回合）；④点击卡片定位到对话对应消息（`trajectoryItem` 已带 messageIndex，会话层 `ScrollTo`）。
- **改动面**：`view.go buildTrajectoryPane`、`state`（traj follow 状态）、可能抽 `uikit` 的回到最新浮层复用。
- **验证**：帧测试（新调用出现时自动跟随、上滚停止跟随、点浮层回底、点卡片切回对话并滚动）；快照。
- **风险/依赖**：时间字段来源待验证；定位需行号稳定。工作量 **M**。

#### P2-3 长会话「找不到」：无页内查找，搜索命中不定位
- **问题**：会话页没有 FindBar；侧栏搜索命中点击只切换会话、不滚到命中消息（命中契约里也没有 messageID，UI 无从定位）。
- **证据**：session 包无 FindBar 调用；官方控件存在：mygo v0.9.0 `ui/feedback.go:225`（`FindBar`）、`ui/list.go:330`（`ListState.ScrollTo`）；`internal/ui/sidebar/sidebar.go:876-881`（`activateMessageHit` 只 SetActiveSession+Push）；`internal/contracts/search.go:10-21`；`docs/INTERACTION-DEPTH-ROUND.md:177`。
- **建议**：①会话页 Cmd/Ctrl+F → `ui.FindBar`，在 `chatSession.Messages` 正文/推理上匹配计数，`st.chatList.ScrollTo(row)` 定位（先不做行内高亮，避免 markdown 重渲染风险）；②`MessageSearchHit` 增 `MessageID`，命中点击切会话后 ScrollTo；后端补字段前只显示「已打开会话」。
- **改动面**：`view.go`（FindBar + 匹配 + ScrollTo）、`uiroot`（若需全局 Cmd+F 键位则复用现有查找键）、`contracts/search.go` + 后端 search（依赖）。
- **验证**：帧测试（FindBar 一次 Esc 关闭、Tab 焦点、Enter 下一处、计数正确、命中行滚入可视区）；快照；FindBar Label。
- **风险/依赖**：Esc 分层（不得抢审批/运行中 Esc）；Markdown 源文本可能命中不可见字符，需在 UI 说明或只匹配正文。工作量 **M**。

#### P2-4 工具行折叠预览直接暴露后端英文原文
- **问题**：团队任务工具在 transcript 的折叠预览是 `created shared team task id: task-1 subject: 检查发布清单 status: pending owner: - blocked_by: - write…`——中文界面里像日志，且截断在句中。
- **证据**：`internal/ui/toolcards/toolcards.go:305`（`Preview: uikit.ToolPreview(summary, 96)` 直接吃 `call.Summary`）；`p2-tool-row-complete.png`、`p2-process-expanded.png`；源头 rig `tools/team/tasks.go:543`。
- **建议**：toolcards 增加「预览模板」注册表（工具名 → 从 arguments 取字段的本地化格式串），复用已有 `toolStringArg/firstToolStringArg`（`toolcards.go:479`/`:493`）；未登记工具保持原文（诚实回退）。顺带本地化审批摘要的键名（`internal/uikit/approval.go:409-431` 的 `subject/description` → 主题/说明）。
- **改动面**：`internal/ui/toolcards/toolcards.go`、`internal/uikit/approval.go`（键名）；测试。
- **验证**：toolcards 帧测试新增 team_task_create/job_start 等 case（断言本地化预览、仍单行截断）；快照 `p2-tool-row-*`。
- **风险/依赖**：只覆盖已知工具，未知保持原文。工作量 **S**。

#### P2-5 子代理完成回执是死链
- **问题**：队友/子代理完成的 transcript 回执是一行纯文本（输出被压成 160 字、去 Markdown），不能点开对应子会话；用户只能再去 header 的「子智能体」里找。
- **证据**：`internal/ui/session/view.go:1073-1088`（`ChatRoleNotice` 行只有图标+文本，无动作）；`view.go:761-776`（`noticeSummary` 去 Markdown/压缩）；`p2-cold-resume.png`（y≈725 的「队友「研究员」已完成…」一行）。
- **建议**：回执行尾加一个 `uikit.IconButton`「打开子会话」（调现有 `st.openChildView(childID)`；notice 的 ID 里已带 child ID），行语义仍是 `RoleListItem`；正文保持 2 行，tooltip 给全文。
- **改动面**：`view.go`（notice 行 + `childNoticeMessages` 携带 child ID）、`uikit.IconButton` 现成。
- **验证**：帧测试（回执行可及名「打开子会话…」、点击进入子会话；失败回执仍 danger）；快照。
- **风险/依赖**：不要变成卡片；键盘/读屏与 List 行一致。工作量 **S**。

#### P2-6 Goal 没有零值入口（发现性与团队/子代理不一致）
- **问题**：目标能力只在「已有目标」时以 dock 出现；创建/编辑入口只有命令面板（slash/palette），会话标题行与 ⋯ 菜单都没有；与 AGENTS §3.17「关键功能入口不因减法隐藏」不一致。
- **证据**：`internal/ui/session/session.go:806-813`（`zeokeel.session.goal` 命令）、`:816-838`（complete/clear）；`internal/ui/session/view.go:461-495`（⋯ 菜单只有复制/导出/归档）；`internal/ui/session/goal.go:328-338`（`st.goal == nil` 直接不渲染）。
- **建议**：会话 ⋯ 菜单加一项「设置/编辑目标」（`Enabled=goalService!=nil && 有活动会话`，`Run=openGoalDialog`）；dock 的 ⋯ 保持处置项。不要往 Run Band 再加常驻 chip（会破坏 header 顺序契约与强调预算）。
- **改动面**：`view.go buildConversationActionsMenu`；文档 S9/场景表。
- **验证**：帧测试（无目标时菜单有「设置目标」、点击开 Goal 对话框且空保存禁用；无 goalService 隐藏；有目标文案变「编辑目标」）。
- **风险/依赖**：菜单项+1（仍有分组，不违反两落点）；中英文案。工作量 **S**。

#### P2-7 Context 页排版与测量：信息真实，但不为决策排版
- **问题**：①与对话阅读列不同轴——1512 帧实测内容 x 368–1424，而对话/输入卡 x 417–1376，切 tab 左缘横跳 ≈49 DIP；②概览 `KeyValueRows` 值被 Spacer 顶到最右（标签 x≈369、值 x≈1383–1410），一行横跨 ≈1040 DIP，读一对数要横跳；③概览/最近请求/系统提示分段/模型调用/事件连成一列，缺分组节奏。
- **证据**：`internal/ui/session/context_pane.go:81`（多接了 `Grow(1)`，对比 `uikit/session.go:131` 与 `uikit/composer.go:171` 的同轴写法）；`internal/uikit/uikit.go:104-116`（`KeyValueRows` Spacer 两端对齐）；`p2-context.png`、`p2-context-sections.png`（像素测量同上）。
- **建议**：①容器与对话/轨迹统一 `Width(TranscriptWidth).MaxWidthPercent(92)`（去掉 `Grow(1)`），三 tab 左缘一致；②概览 KV 块限宽（`MaxWidth(design.DialogBodyMax)` 或 560）左对齐，表格仍占整列；③用现有 `uikit.Section` 间距/Divider 建节奏，不新增卡片。
- **改动面**：`context_pane.go`（布局）；可选给 `uikit.KeyValueRows` 加零值不变的 `MaxWidth`。
- **验证**：像素测量三 tab 左缘一致 + 快照对比；帧测试断言标题 x 相同（rect）。
- **风险/依赖**：窄窗表格横向滚动保留；不动设置页属性表。工作量 **S**。

### P3

| # | 项 | 问题 / 证据 | 建议 | 改动面 | 验证 | 工作量 |
|---|---|---|---|---|---|---|
| P3-1 | 中断提示「继续」名不副实 | 点击只 `st.focusComposer=true`，无 toast、无预填；用户以为坏了（`internal/ui/session/interrupted.go:53`；AGENT-SURFACES S8 明确只聚焦） | 文案改「在输入框继续」+ toast 说明，或预填最后一条用户消息（不自动发送，需产品确认） | `interrupted.go` | 帧测试点击后有可见反馈/焦点在输入 | S |
| P3-2 | 同一队友被计两次 | 团队 chip 与子智能体 chip 都显示 1，实际只有一个「研究员」（`p2-cold-resume-child.png`；`agentsurface.go:355`/`:821` 各自计数） | 子智能体 chip 只计一次性 subagent，或 tooltip 明示「含队友 N」；不改入口 | `agentsurface.go` 计数与 tooltip | 帧测试计数；快照 | S |
| P3-3 | 目标状态词与运行态撞词 | 「目标进行中」（armed）与 header「运行中」同词同 warning 色；disarmed/paused 都退成中性灰（`goal.go:32-46`；`uikit/status.go:277-299`；`p2-goal.png` vs `p2-cold-resume.png`） | 改「目标已启用 / 等待继续 / 已暂停」，强调 activation 而非 run | `goal.go` 文案 | 帧测试文案；快照 | S |
| P3-4 | 子会话（冷恢复）composer 禁用发送仍像品牌蓝（V5 在第 3 处 composer 回退） | 空草稿时主 composer 的发送是中性（`uikit/composer.go:382` 用 `t.DisabledPrimary()`），子会话却用 `ui.PrimaryButton + t.PrimaryButton()` 再 `Disabled(true)`（`agentsurface.go:1590-1592`）；`p2-cold-resume-child.png` 像素采样按钮底 `#86A1FA`（品牌蓝），主 composer 为 `#F8F9FA` | 子会话发送按可用性切 `t.DisabledPrimary()`（或抽 uikit 的 composer 动作位复用） | `agentsurface.go` | 帧测试「空草稿时发送为中性」+ 像素断言；快照 | S |
| P3-5 | 子会话活动行 kind 与正文重复 | 「04:15 关闭 关闭「研究员」」；代码只对 Finish 去 kind（`agentsurface.go:1480-1495`；`p2-cold-resume-child.png`） | Close/Interrupt 同规则去 kind，或 kind 用过去式 | `agentsurface.go` | 帧测试活动行文案 | S |
| P3-6 | Context 列头中英混排 / 重名 | 中文界面列头是「轮次/模型/Prompt/完成/缓存/上下文/时间/成本」；「上下文」与页签同名（`context_pane.go:390`） | Prompt→提示词/输入 tokens；上下文→上下文 tokens | `context_pane.go` | 中英帧测试/快照 | S |
| P3-7 | 新表面证据债 | 暗色帧只覆盖 context/goal/interrupted/insights/tool-row-states；approval、团队浮层、冷恢复子会话、轨迹页无暗色帧；全仓无 900×600 帧（`ls .cache/ux-review/shots`，`ui-snapshot/main.go:533,536`） | `ui-snapshot` 补 4 张暗色 + 1 张 900×600（含审批+goal+队列） | `internal/devtools/ui-snapshot`（非产品代码） | 快照人工复核 | S |

---

## 4. 不建议做（anti-scope）

- **不做团队工作台 overlay（980×740）**：INTERACTION-DEPTH §5.1 的大形态属新功能扩张；本轮用成员行级「停止/关闭」把控制闭环即可。
- **不给子会话补完整 tool-call transcript**：`ChildAgent` 契约没有 tool/推理字段（`internal/contracts/agentsurface.go:53-81`），要做先改契约+后端，不是 UI 升级。
- **不重加常驻消息「复制」按钮**：V11 已做减法（卡外复制 chip 删除）；复制保留在 ⋯ 菜单与代码块复制。
- **不给 Context/Insights 加图表、KPI 卡片、跨会话合并**：DESIGN §2.3 明确禁止；表格+KV 已足够决策。
- **不加导航级徽章**（侧栏 goal/inbox/interrupted 计数）：S8/S10 明确「非持久提示不进导航」。
- **不动状态条 6 项**：用户明确要求恢复；「剩余 %」虽与上下文占比重复，也按 §3.17 保留。
- **不引第三方 UI/图标/样式库、不建第二套设计系统**；新视觉一律走现有 token/uikit。
- **不适配 640×480**：低于产品最小窗，历史已接受不修。

---

## 5. 局限

- 只读审计：未运行 `go build/test`、`designlint`、`ui-snapshot`；快照由 Lead 侧生成（120 PNG，HEAD `1f66af3`，04:15）。
- **900×600 无任何证据帧**；P1-3 的高度结论来自 `p1-approval.png` 像素测量 + 布局推算，需补帧实测（标注「推算」）。
- 暗色仅覆盖 context/goal/interrupted/insights/tool-row-states；approval、团队浮层、冷恢复子会话、轨迹页的暗色表现**待验证**。
- `view.go`（4538 行）只精读页头/消息/过程折叠/轨迹/composer/状态条等关键函数，未逐行；toolcards/uikit 按入口读取。
- Insights 页不在本报告（跨会话页，归 02/03 或独立报告）；壳层导航、设置页、设计 token 系统性问题归 02/03。
- 未在真机验证 OS 通知点击、剪贴板图片、拖放（快照为注入帧）。
- 「轨迹卡时间字段来源」「团队计数的产品语义」「搜索命中回填 ID 的成本」标注**待验证**，需产品/后端确认。
- `docs/UI-AUDIT-2026-11-11.md:572` 记录的「回到最新与长围栏相交」在本批帧**未复现**（`p2-tool-row-complete.png` 中浮层只覆盖右侧空栏），未列为升级项；建议补一张长围栏滚到底的帧再判定。
