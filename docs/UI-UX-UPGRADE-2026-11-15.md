# UI/UX 升级空间评估（v0.2.38 · 2026-11-15）

> **问题**：按照当前功能规划（长任务 DSH 对齐、Goal v2、PromptQueue/inbox、Context、Insights、冷恢复、系统通知），UI/UX 还有哪些升级空间？
>
> **口径**：只谈「功能已经存在、体验尚未闭环」的升级，不重做 2026-11-11 已拍板的 **Quiet Cockpit** 风格，不引入依赖/第二套设计系统，不为了「显得完整」加功能。
>
> **证据**：三份只读审计（[会话/Agent 表面](research/ui-ux-upgrade/01-conversation-surface.md)、[壳层/IA/设置](research/ui-ux-upgrade/02-shell-ia-settings.md)、[设计系统/跨屏质量](research/ui-ux-upgrade/03-system-quality.md)）+ 120 张评审帧（`.cache/ux-review/shots/`，HEAD `1f66af3`，2026-11-15 04:15）+ 2 张实现标本（`.cache/impl-shots/`）+ 静态代码/像素采样。
>
> **边界**：本文未改代码、未运行 build/test/designlint（只读评审）。标「推算」「待复现」的结论需实现前补帧/补测试确认。

---

## 0. 结论

**一句话**：功能规划已经把产品从「聊天客户端」推进到「长任务运行台 + 多智能体协作 + 运行可观测」，但壳层、共享层和控制面仍按「聊天 + 几个页面」的旧假设工作——于是长任务**能看见，却停不掉、批不准、找不到、通知漏/吵、窗口一窄就不可用**。

下一轮 ROI 最高的不是新视觉，而是五件事：

1. **决策信息**：审批（尤其 write/edit）必须能看到将改什么，而不是盲批。
2. **控制面**：队友/子代理/后台作业要有与「后台任务可 kill」一致的控制；执行域（job/terminal）要有持久可查的面。
3. **壳层让位与响应式**：通知不再压住检查器/设置；命令面板长列表首屏必须能看到选中项；窄窗（1024/900×600）要能边看文件边对话。
4. **共享层收口**：分段控件、属性表、图标按钮、状态解剖、表格列宽在新表面已经开始分叉，先把 uikit 唯一入口补上。
5. **可信度**：后台完成通知的静默判定、恢复会话的元信息、Insights 键盘选中行的对比度——都是「看起来能用、实际会误导」的缺陷。

建议顺序：**Batch A 正确性/遮挡/可达（1–2 周）→ Batch B 运行台（2–3 周）→ Batch C 共享层收口与验证债**。详见 §4。

> 用户在 2026-11-15 另外点名两处输入框问题：**工具切换器像一排带边框的小方块**、**附件要求手动输入路径**。两项已并入 P2-12，并给出效果图（§6.2）。

---

## 0.1 实施状态（2026-11-15）

本次已把「效果图」中的设计落成真实代码（不是 mock）：

| 设计 | 状态 | 实现入口 |
|---|---|---|
| 输入框去小方块、附件直连 picker | **已实现** | `uikit.ComposerToolbar` / `ComposerAttachButton`；session/home 共用；删除手动路径菜单/对话框（拖入/粘贴保留） |
| 审批「将改动」diff | **已实现** | `uikit.DiffPreview` + `ApprovalDialog`；write/edit/str_replace_editor create/replace，中文「将改动/Changes」；无 Default/Esc=deny 未动 |
| 命令面板首屏选中 + 分组 + 前往/设置 | **已实现** | `uiroot` palette：`ScrollTo`/`ScrollIntoView`、`Header` 分组、`goto.<screen>`、`settings.<page>`、Keywords 搜索 |
| Context 同轴 + 属性表左对齐 | **已实现** | `uikit.ReadingColumn` + `uikit.PropertySheet`；About/Diagnostics/Network/Update 同步迁移 |
| Insights 选中行对比度 | **已实现** | 按 `row.Selected()` 切 `t.AccentText`；亮/暗像素断言 |
| 通知「打开会话」 | **已实现（右上角不变）** | `notifierService` 注入 `openSession` + session 通知自动带 Action；toast 位置/inset 零改动 |
| 团队/子代理停止 + 执行域历史 | **已实现（第二批）** | `TeamService.Interrupt/Close` + `uikit.ConfirmDanger`；`contracts.ExecutionHistoryService` + toolkit durable 折叠；后台任务浮层「历史」分组 |
| 窄窗自动 rail / 900×600 | **已实现（第二批）** | 1024×720 / 900×600 检查器 docked；临时 rail 不写用户折叠偏好；检查器按可用宽度 ≥260 |
| 前台会话通知静默 | **已实现（第二批）** | `contracts.ForegroundSession`（uiroot 提供、host 接线、agent 消费）；非会话屏不再漏报 |
| 会话 FindBar + 轨迹跟随/定位 | **已实现（第三批）** | 官方 `ui.FindBar`；匹配正文/推理并滚动到分组行；Esc 不停止；轨迹 FollowEnd、计数行同轴、「在对话中查看」 |
| 设置条目级搜索 | **已实现（第三批）** | `SettingsPageSpec.Keywords/SearchItems/Reveal`；模块声明关键字；打开页并滚动/短暂高亮目标条目 |
| 工具行预览本地化 | **已实现（第三批）** | `toolcards.Preview` 模板注册表；未知工具回退原文；单行 96 runes |
| 恢复会话元信息 | **已实现（第四批）** | durable `user_message`/`run_end` 诚实回填（压缩/rewind 不错配）；无时钟无 usage 不再孤立 ⋯ |
| 搜索命中定位 | **已实现（第四批）** | `MessageSearchHit.MessageID` + `contracts.SessionFocus`；消息命中滚到分组行（ordinal locator 跳过 tool 行） |
| 非会话屏单选中 + 返回 | **已实现（第四批）** | 洞察/自动化/插件屏不双高亮会话行；会话页头 `‹ 来源屏` 走 nav.Back |
| rail 会话入口 + 标题/托盘运行态 | **已实现（第四批）** | 64 rail 会话 popover；`Root.Activity()` → 窗口标题/托盘「运行中 N/等待审批」 |
| hero 开始输入/示例 + 设置分组/保存 | **已实现（第四批）** | 空会话「开始输入」+ 3 示例仅填草稿；数据与通知组；4 页脚统一「保存」+ clean Disabled |
| P3 子代理回执/Goal 入口/冷恢复 | **已实现（第四批）** | 回执「打开子会话」；⋯ 无目标「设置目标」；冷恢复空草稿 DisabledPrimary；活动行去重 |
| P3 状态三层/a11y/图标/字号 | **已实现（第四批）** | AP-21 残留 StatusBadge；Modal/Sidebar 可访问名；goal IconButton 24 DIP；成句 11px→TypeXXS；Insights 4 档 |
| P2-6 团队工作台 | **不做（产品决策）** | 不新增右栏常驻团队页；Run Band/浮层/子会话已覆盖 |
| P3 TableColumn token / plugins 首页 / 暗色证据债 | **部分** | release 快照覆盖主要亮/暗/窄窗；TableColumn token 与 plugins 新首页留待后续版本 |

验证（v0.2.38 发布前）：`go test ./... -count=1` 全绿；`go vet ./...` / `go tool mygo vet .` 干净；`designlint --strict --baseline` 0 new / 0 blocking；`scripts/design-check.sh` 6/6；`go test -race`（agent/toolkit/sessionstore/session/uiroot/settings/uikit）通过；`ui-drive` 93/93、`ui-snapshot` 122 PNG；真实帧在 `.cache/impl-shots-final/`（`p1-chat.png`、`p1-trajectory.png`、`p2-palette.png`、`p2-context.png`、`p2-approval-diff.png`、`p2-insights.png`、`p2-team-overlay.png`、`narrow/narrow-session-inspector.png`）。

---

## 1. 功能规划 → 体验闭环度

| 已落地能力 | 当前 UI 面 | 闭环度 | 主要缺口 |
|---|---|---|---|
| 会话/流式/工具/推理/审批/计划/提问 | transcript、Run Band、inline 审批、计划/提问 Modal | **基本闭环** | 审批无 diff；计划/提问提示行在 footer 之后（V2 残留） |
| Goal v2 | composer 上方 goal dock + `/goal` 命令 | **半闭环** | 只在已有目标时可见；创建/编辑只在命令面板；状态词与「运行中」撞词 |
| PromptQueue / inbox | composer 排队 chip + popover | **闭环** | Stop 后不自动 drain（已有文案，可接受） |
| 中断恢复 | transcript 顶部 pin 条 | **半闭环** | 「继续」只聚焦输入框，无可见反馈 |
| 冷恢复 / 团队 / 子代理 | 页头 chip + 浮层 + 主栏子会话 | **半闭环** | 成员**不可停止/关闭**；完成回执是死链；无持久工作台 |
| 执行域（job / terminal） | 页头「后台任务」浮层（仅 live，list/kill） | **未闭环** | 没有作业/终端历史面；durable execution 事件无人读；模型 terminal 只活在工具卡里 |
| Context 观测 | 会话第 4 个页签 | **半闭环** | 与对话不同轴；属性表值甩到最右；无「压缩/去设置」动作；压缩原文不可看 |
| Insights | 侧栏一级「洞察」页 | **半闭环** | 键盘选中行不可读；宽窗表格数值贴右/空行多；无下钻/筛选 |
| 通知 / 常驻 | 右上 toast + OS 通知 + 托盘 | **未闭环** | toast 遮挡新表面；无历史/分级/可操作；托盘与窗口标题无状态；静默判定错误 |
| 设置 / 自动化 / 插件 / 记忆 / 技能 | 设置 18 页 5 组 + 3 个一级页 | **基本闭环** | 搜索只到页面级且别名漂移；保存语义三种动词；日常页埋在折叠「高级」 |

> 结论：**「能不能用」已经不是主要问题；「敢不敢信、能不能控、找不找得到」才是。**

---

## 2. 已经做完、不要再重做的部分

以下来自 2026-11-11 设计语言 v2 与后续签名表面，本次审计确认已达标，重做只会带回归：

- 颜色零字面量、S0–S4 表面 ramp 两主题同向、浮层/抽屉 `ShadowOverlay`、选择态两级、主操作两落点、「38」已消失。
- **Agent Run Band**（零值常显、不显示 0、团队/子代理可发现）、**State-first Tool Row**（固定 gutter 不 reflow）、**Markdown 阅读主题**、Inspector 文档 `MarkdownDoc`。
- 管理列表去卡墙、状态徽章 `StatusBadge`、`ConfirmDanger` 危险默认安全、窄窗抽屉让宽（R3 的 1024 部分）。
- Insights 的克制（无图、无卡墙、0 accent/0 primary）是**对的**——它的问题在表格宽度与选中行，不是「缺图表」。

---

## 3. 升级机会

### 3.1 P1（先做：正确性 / 不可逆决策 / 可达性）

#### P1-1 审批缺 diff：全产品唯一不可逆决策却是「盲批」

- **现状**：write/edit 审批只有摘要 `key: value` + 折叠的「原始参数」，看不到将写入什么；用户只能凭 trust 点「允许」。
- **证据**：`p1-approval.png`（摘要 `subject/description`，无 diff）；`internal/uikit/approval.go:220,527`；可复用能力已在仓库内：`internal/ui/rightpanel/changes.go:179`（`editor.ParsePatch + editor.DiffView`）；`docs/INTERACTION-DEPTH-ROUND.md:172` 仍列为开放项。
- **建议**：在「摘要」与「原始参数」之间加「将改动」折叠块（write/edit 默认展开）：write_file 由 `arguments.content` 生成全新增 diff；edit_file/str_replace_editor 由 old→new 合成 unified patch 后走 `editor.DiffView`；无法合成时回退为有界 mono 内容预览（复用 toolcards 的截断 helper）。块头给路径与 `+N −M`。**footer、无 Default、Esc=deny 一律不动**。
- **改动面**：`internal/uikit/approval.go`、`internal/ui/approval/approval.go`（生成/传递 diff，读文件走缓存 + `UIRuntime.Post`，禁止 build 内 I/O）、`internal/ui/toolcards`（复用 helper）；同步 AGENT-SURFACES S4。
- **验证**：`uikit/approval_test.go` 帧测试（write/edit 出现 diff 行、不可解析回退、空参数不渲染、仍无 Default）；快照 `p1-approval.png` + 暗色；`designlint --strict`。
- **风险**：大文件/二进制需上限，回退必须诚实。**工作量 M**。

#### P1-2 队友/子代理不可控：能看见，停不掉

- **现状**：Run Band 显示「智能体团队 N 运行中」，团队浮层成员卡却只有「打开/继续」；后台 job 能 kill、子会话没有停止按钮，控制语义不一致。
- **证据**：`internal/contracts/team.go:147-150`（`Interrupt/Close` 早已暴露）；`internal/ui/session/agentsurface.go:612-665`（成员卡唯一动作是打开/继续）；`agentsurface.go:1241`（job 可 kill）；`p2-team-overlay.png`；CHANGELOG 0.2.36 known limitation「运行中 overlay 无停止按钮」。
- **建议**：成员行 `RunState=running` 时给「停止」（调 `Interrupt`，状态词走中性 cancelled）；「关闭」收进行尾 ⋯ danger → `uikit.ConfirmDanger`（取消 Default）；子会话 header 同位置给「停止」。入口结构不变，只补动作。
- **改动面**：`agentsurface.go`（成员卡/子会话 header，`uikit.IconButton` 现成）；文档 S9「团队浮层只读」改为「只读数据 + 控制动作」。
- **验证**：帧测试（running 行出现停止、点击调 fake `TeamService.Interrupt`、停止后中性、Close 走 ConfirmDanger 且取消 Default）；快照。
- **风险**：这是既有设计选择，需产品确认；Interrupt 失败用 `ErrorLine`。**工作量 M**。

#### P1-3 执行域（job / terminal）没有持久面

- **现状**：执行域持久化（v0.2.36）已让 job/terminal 跨工作区/权限/沙盒重建存活，并写入 durable 生命周期事件；但 UI 只有页头「后台任务」浮层（live list/kill），**没有作业/终端历史、没有输出、重启后不可回看**；模型 terminal 只存在于工具卡里。设计稿明确「本轮不做重启后 UI 展示历史 job/terminal」。
- **证据**：`internal/contracts/agentsurface.go:123-134`（JobService 只有 Jobs/KillJob）；`internal/contracts/execution.go`（4 类 durable 事件无 UI reader）；`p2-files.png`/`p2-team-tasks.png`（右栏只有开始/文件/命令 + 文件页签）；`docs/plans/execution-domain-persistence.md:49-52`。
- **建议**：不新增一级导航。① 页头「后台任务」浮层加「历史」分组（本会话 durable `execution/*`：开始/结束/中断，明确标注「输出不持久」）；② 检查器「命令」页增加「模型终端」区（若 `terminal_list` 有会话），或把 job 详情做成检查器第 4 个固定页（仅当有执行时出现）；③ 空态/截断/中断态要诚实。
- **改动面**：`internal/ui/session/agentsurface.go`、`internal/ui/rightpanel/*`、`internal/contracts`（只读 execution 汇总/reader）、`internal/modules/toolkit`（暴露只读历史）；注意模块边界，优先 contracts 服务。
- **验证**：帧测试（历史分组、重启后 interrupted、无输出提示）；快照；`-race`。
- **风险**：跨模块读模型有成本；先做「会话内 live + 历史」再考虑全局。**工作量 M**。

#### P1-4 通知：应用内 toast 不可操作（位置保持右上角）

- **用户决定（2026-11-15）**：toast **保持右上角**，不做让位/移动/安全区；「打开会话」动作保留。下面只实现可操作，不改位置。
- **现状**：toast 固定窗口右上（inset 84、最多 2 条、4s）；会话标签的通知在应用内 toast **没有点击动作**（只有 OS 通知点击回会话），用户看到「审批等待」却无法从 toast 直接进入会话。
- **证据**：`internal/uiroot/notifier.go:119-130`（sessionID 只给 OS 通知用）；`internal/ui/toasts/toasts.go:285-310`（只有 `Action` 非空才渲染按钮）；`p2-files.png`（位置：右上，按决定保留）；`internal/host/notification_click.go`（OS 点击打开会话的语义可复用）。
- **建议**：`notifierService` 增加 `openSession func(string)`（Root 注入，内部 `chat.SetActiveSession` + `navigator.Push("session")`，未知/已删会话只置前）；`notify()` 对 session-tagged 通知自动填 `ActionLabel="打开会话"` 与 Action；不传 action 的普通通知保持原样。**不改 toast 位置、inset、宽度、堆叠。**
- **改动面**：`internal/uiroot/notifier.go`、`internal/uiroot/frame.go`（注入回调）、`internal/ui/toasts/*`（通常无需改，确认 action 按钮渲染）。
- **验证**：notifier 单测（session 通知带 action、普通通知不带、点击调 fake openSession）；toast 位置/inset 既有测试不变；点击「打开会话」后 active session 切换且 screen=session。
- **风险**：回调需在 UI 线程（经 `UIRuntime.Post`）；不重复实现 host 的打开逻辑。**工作量 S**。

#### P1-5 命令面板：命令数超过一屏时，首屏可能看不到选中行

- **现状（快照观察）**：面板列表 `MaxHeight(320)` 约 9 行；当前约 16 条命令时列表锚点不在顶部，首屏**没有 accent 选中行像素**，Enter 可能运行一个看不见的「设置」。现有回归测试只覆盖 5 条命令。
- **证据**：`p2-palette.png`（列表从「帮助」开始、无选中行）；`internal/uiroot/frame.go:871,918`；`internal/uiroot/fix_round_test.go:61-180`（测试仅 5 条）；`docs/UI-AUDIT-2026-11-11.md` 的 SH-02 类问题。
- **建议**：打开时显式 `paletteList.ScrollTo(0, ui.Start)`，`movePaletteSelection` 同步 `ScrollIntoView`；补一条 16+ 命令的帧测试，断言选中行在视口内（像素 + `Visible()`）。**机制待复现**，但「首屏必须可见选中行」是硬要求。
- **改动面**：`frame.go`（2 处）+ 测试。**工作量 S**。

#### P1-6 窄窗：检查器变成阻断式抽屉；900×600 底部 dock 无高度预算

- **现状 A**：dock 阈值 = 280 + 520 + 24 + 260 = **1084 DIP**。1024 窗打开检查器即退化为带 scrim 的模态抽屉，看文件时不能继续对话——而「边看边追问」正是检查器的核心用法（`narrow/narrow-session-inspector.png`）。
- **现状 B（推算）**：底部 dock = goal 条 → 审批内联卡 → composer → 状态条逐层累加。1512 帧实测审批卡 ≈320 DIP + composer ≈137 → dock ≈487；窗高 600 时 transcript 可能被挤到 ≈0。全仓没有 900×600 证据帧。
- **证据**：`internal/design/components.go:56-60`；`frame.go:29,707-748,1034-1057`；`p1-approval.png`（像素测量）；`internal/uikit/approval.go:170-178`（无 MaxHeight）；`ui-snapshot` 只有 1512/1024 两档。
- **建议**：
  1. 窄窗打开检查器时**自动把侧栏收成 64 rail**，再按 dock 计算（900 下 64+520+24+260=868 可 dock）；或把 `ContentMinWidth` 520→420，让 1024 可 dock。需先补 900×600 帧。
  2. 审批卡加高度预算（如 `MaxHeightPercent(60)` 或「横幅+footer 固定、摘要有界滚动」），保证 transcript 可见；先补 900×600 + 审批 + goal + 队列帧再定数值。
- **改动面**：`design/components.go`、`frame.go`、`uikit/approval.go`、`ui-snapshot` 新步骤。**工作量 S/M**。

#### P1-7 后台完成通知按「活动会话」判静默，在非会话屏幕漏报

- **现状**：静默条件是 `agent.active == sessionID`（聊天后端的活动会话），与「用户此刻在看哪个屏幕」无关。用户在洞察/自动化/插件/设置页时，运行中的那个会话其实不可见，但通知被当作「你正在看」而不发——长任务最需要通知的时刻恰好静默。
- **证据**：`internal/modules/agent/run_notify.go:24-36`；`contracts/notify.go:104-140`；`frame.go:132-139`（host 已知 `nav.Current()` 但未暴露给前端）。
- **建议**：新增可选只读契约 `contracts.ForegroundSession`（uiroot 提供：`nav.Current()=="session"` 时返回 `chat.ActiveSession`），agent 改为「前台会话才静默」；无该服务（headless）保持现状。
- **改动面**：`contracts`、`uiroot`、`agent/run_notify.go`；同步 ARCHITECTURE。
- **验证**：agent 单测 + uiroot 帧测试（洞察屏完成后台 run → 有通知；会话屏同会话完成 → 无）。**工作量 S/M**。

#### P1-8 Context 页与对话不同轴、属性表值甩到最右

- **现状**：Context 容器多接了 `.Grow(1)`，阅读列被拉宽约 13%、左缘比对话左移约 49–64 DIP；概览 `KeyValueRows` 用 Spacer 把值顶到最右，一对标签/值相距约 1050 DIP；系统提示分段/模型调用表也贴右。切换页签时文本列横跳。
- **证据**：`internal/ui/session/context_pane.go:80-82`；`internal/uikit/uikit.go:104-116`（`KeyValueRows` 两端对齐）；`p2-context.png`/`p2-context-sections.png`（像素测量）；对比 `uikit/session.go:140-142`（960 居中、无 Grow）。
- **建议**：删 `.Grow(1)`，与 Chat/Goal dock 统一 `Width(960).MaxWidthPercent(92)`；概览 KV 块限宽（≤560）左对齐；表格仍占整列；抽 `uikit.ReadingColumn` 或注释钉死「阅读列=960 居中、不许 Grow」。
- **改动面**：`context_pane.go`；可选 `uikit` 帮助函数。
- **验证**：帧测试断言三 tab 左缘一致；快照。**工作量 S**。

#### P1-9 Insights 会话排行：键盘选中行前景不反色，选中即不可读

- **现状**：`ui.List` 选中行用 accent 填充，但行内文字显式设了 resting 色，不随选中反色——审计员像素采样：浅色 ink-on-royal ≈2.9:1、暗色白-on-sky ≈2.1:1。
- **证据**：`internal/ui/insights/view.go:434,465-499`（`TextColor` 固定）；`p2-insights.png`；`mygo ui/table.go` 的 `Selected` 语义；`uikit/onaccent_test.go` 已有同类测试写法。
- **建议**：行内按 `row.Selected()` 切 `t.AccentText`，成本徽章用 `uikit.OnAccentBadge`；补亮/暗两主题像素对比度测试（≥4.5:1）。
- **改动面**：`insights/view.go` + 测试。**工作量 S**。

---

### 3.2 P2（下一轮：长任务运行台 + 发现性 + 阅读定位）

| # | 升级项 | 现状与证据 | 建议 | 量 |
|---|---|---|---|---|
| P2-1 | 命令面板「前往」与分组 | 约 22 条注册命令（当屏约 16 条启用），无「前往自动化/洞察/插件/设置页」；仅 `Contains` 匹配，无别名/分组。`frame.go:824-840`；`p2-palette.png` | 由 `ScreenLister` 生成 goto 命令；由 `OpenSettingsPage` 生成「设置：页名」；`CommandSpec` 加可选 `Keywords/Category`；补 `P1-5` 长列表滚动后再扩 | M |
| P2-2 | 设置条目级搜索 + 别名漂移 | 搜「压缩」「价格」零命中；搜「字号」落到没有字号控件的「外观」（实际在「行为」）；只到页面级。`frame.go:1099-1165`；`p2-notifications.png` | ① 别名表移入每页 `SettingsPageSpec.Keywords`（模块自己声明）；② 关键控件加稳定锚点，命中后打开页面 + `ScrollIntoView` + 短暂高亮 + 自动展开折叠项 | M/L |
| P2-3 | 恢复会话每轮元信息只剩孤立 `⋯` | restore 的 `messageTime` 恒零（`agent.go:3097` 是显式 stub），UI 仍渲染 `⋯`；长恢复会话像一列圆点。`p2-interrupted.png` | 根治：restore 回填 CreatedAt（durable `user_message/run_end` 已有时间）；兜底：无时间且无用量时不渲染空元信息行，动作 hover/focus 出现并保持键盘可达 | S/M |
| P2-4 | 页内 FindBar + 搜索命中定位 | 会话无 FindBar；侧栏消息命中点击只切会话、不滚到消息（`MessageSearchHit` 无 messageID）。`contracts/search.go:10-21`；`INTERACTION-DEPTH §5.7` | Cmd/Ctrl+F → 官方 `ui.FindBar` 匹配正文/推理 + `ListState.ScrollTo`；后端命中补 `MessageID` 后点击定位 | M |
| P2-5 | 轨迹页「跟随 + 回到最新」名不副实 | 注释承诺 FollowEnd/浮层，实际 `trajList` 没设、无浮层；计数行贴面板左缘（x≈328 vs 卡片 417）；无回合分组/定位。`view.go:1974-2011`；`p1-trajectory.png` | 设 `FollowEnd/HoldPlace` + 复用「回到最新」；计数行移入阅读列；卡片头补回合；点卡片切回对话并滚动 | M |
| P2-6 | 团队/任务只有浮层，没有持久工作台 | popover 关闭即消失；长任务无法持续看任务板/成员活动。INTERACTION-DEPTH §5.1；`rightpanel.go:16-21` | **不做 980×740 大 overlay**；优先在检查器加「团队」固定页（仅当有 team/task 后端），复用现有成员/任务行；需先在 `uikit` 抽 `AgentMemberRow/TaskRow`（模块边界） | M/L |
| P2-7 | 非会话屏「双选中」且无返回 | 洞察屏同时高亮「洞察」行与活动会话行（都 tint+2px 条）；从洞察点进会话后无 back/面包屑（`nav.Back` 无 UI）。`p2-insights.png`；`sidebar.go:1042,1104`；`navigator.go:48-68` | 非 session 屏把活动会话行降为弱选中；会话页头在「上一屏≠session」时显示 `‹ 洞察` 面包屑，走 `nav.Back()` | S/M |
| P2-8 | 折叠 rail 没有会话入口 | rail 只有展开/新对话/三 screen/设置；折叠后无法切回会话。`p2-sidebar-collapsed.png`；`frame.go:277-302` | rail 增「会话」图标 → 原生 Popover：最近 8 条（标题+运行/未读点）+ 搜索 + 底部「展开侧栏」；不新增页面 | M |
| P2-9 | 托盘/窗口标题没有运行态 | 托盘项写死中文；标题恒为 `ZeoKeel`；后台 run/审批无可从任务栏感知的信号。`host/tray.go:174-235`；`host/host.go:656-657` | 托盘按 locale 重建；标题按状态追加 `· 运行中 N` / `· 等待审批`；首启语言按系统 locale 映射（仅首次） | S/M |
| P2-10 | 空会话 hero CTA 名不副实 | 按钮写「新对话」，但已打开空会话时只聚焦输入框；无起步示例。`p2-landing.png`；`session/view.go:947-985` | 有会话时改「开始输入」；缺失依赖下加 2–3 个示例 prompt（填入但不发送，复用 SecondaryButton） | S |
| P2-11 | 设置分组/保存语义 | 通知/记忆/归档埋在默认折叠「高级」；「外观与技能」混搭；4 个页脚保存页 3 种动词、无改动时 primary 仍常亮。`p2-notifications.png`；`settings.go:673-705` | 重分组（数据与通知 / 能力与集成）；统一动词；页脚 primary 用 `Disabled: !saveDirty()`（只覆盖页脚字段，模型页即时项另算） | S/M |
| P2-12 | 输入框去边框 + 附件入口收敛（用户点名） | 三个切换器各自带边框，视觉上像一排小方块；附件菜单还含「手动输入路径…」，把选择文件降级成手输路径。`p1-chat.png`；`internal/ui/session/view.go:2791-2835`（附件菜单）；`internal/uikit/composer.go:380-420`（动作行） | 三个触发器放进官方 `ui.Toolbar`：toolbar 内按钮默认无边框、悬停才出现底色，焦点/键盘/菜单语义不变；发送按钮保持唯一实心主操作。附件按钮直接打开原生文件选择器，删除手动路径菜单/对话框（保留拖入、粘贴）；附件 chip 改浅底无描边（可选，见效果图）。 | S |

---

### 3.3 P3（打磨 / 一致性 / 证据债）

| # | 项 | 现状与证据 | 建议 | 量 |
|---|---|---|---|---|
| P3-1 | 共享层分叉 | 分段控件 2 份复制（settings/insights）；属性表 2 套（uikit 右对齐 7 处 vs settings 160px 左对齐 2 处）；Context 11 个 `TableColumn` 裸宽度落在 designlint 盲区。`03` 报告矩阵 #11/#12/#15 | 抽 `uikit.SegmentedControl` + `uikit.PropertySheet`；TableColumn 宽度 token 化并把 DS-03 扩到复合字面量 | M |
| P3-2 | 状态展示三层未写进契约 + 3 处 AP-21 残留 | `files.go:261`、`sidebar.go:965`、`search.go:71` 仍是中性底+状态字；ToolRow 行内状态词是刻意但契约没记录 | 在 DESIGN §5.2 写「对象头=StatusBadge / 紧凑行=StatusText / 瞬时成功=带图标」三层，清 3 处残留或显式豁免 | S |
| P3-3 | 字号仍偏微缩 | `TypeBody` 14 处 vs `TypeXXS` 182 / `TypeXS` 145 / `TypeXXXS` 93；Insights 一屏 5 档；11px 仍有成句（`toasts.go:351`、`uiroot/frame.go:850`）。`03` 矩阵 #6 | 成句改 12；新增 `TypeMetric`（仅指标值）写进 §5.3；Context 时间→XXS | S |
| P3-4 | 图标按钮未收口 | 12 处 `ui.Button(c,"")` + 9 处 `ButtonBase`；goal dock 20×20 < 24 目标区（`goal.go:362-366`）。`03` 矩阵 #13 | 给 `uikit.IconButton` 加 `Variant: Native` 后迁移；统一 24/28 两档 | M/L |
| P3-5 | 工具行预览暴露后端英文 | 团队任务预览是 `created shared team task id: … owner: -`；中文界面像日志。`toolcards.go:305`；`p2-tool-row-complete.png` | toolcards 加预览模板注册表（工具名→从 arguments 取字段的本地化格式串），未知保持原文；顺带本地化审批摘要键名 | S |
| P3-6 | 子代理完成回执是死链 | transcript notice 只有图标+文本，不能点开子会话。`view.go:1073-1088`；`p2-cold-resume.png` | 回执行尾加 IconButton「打开子会话」（调 `openChildView`），正文 2 行、tooltip 全文 | S |
| P3-7 | Goal 无零值入口 | 只在已有目标时出现；创建/编辑只在命令面板；与「关键功能入口零值常显」不一致。`goal.go:328-338`；`session.go:806-838` | 会话 ⋯ 菜单加「设置/编辑目标」（无目标时「设置目标」）；不往 Run Band 加常驻 chip | S |
| P3-8 | 冷恢复/中断的文案与状态 | 「继续」只聚焦输入框；冷恢复空草稿发送仍品牌蓝（`agentsurface.go:1590-1592`，像素 `#86A1FA`）；活动行「关闭 关闭「研究员」」重复。`p2-cold-resume-child.png` | 文案改「在输入框继续」并给可见反馈；发送按可用性切 `t.DisabledPrimary()`；Close/Interrupt 同 Finish 去重 | S |
| P3-9 | 设置/页面 a11y 与 i18n | 设置 Modal 无可访问名、设置导航无 Label；Context 表头「Prompt」未翻译；快捷键参考表重复行。`frame.go:983,1290`；`context_pane.go:390`；`p2-shortcuts.png` | Modal/Sidebar 补 `Label`；表头改 `tr`；参考表按「文案+chord」去重 | S |
| P3-10 | 插件 73 开关长墙 / 工作区不可重命名 / Home 死表面 | `p2-plugins.png`；`workspaceGroupMenu` 仅 2 项；`home` screen 注册但无入口 | 组标题计数 + 组级启用/停用（批量是破坏性，需确认）；工作区重命名（可能需后端）；Home 与产品确认后删除或给低权重入口 | S/M |
| P3-11 | 验证债 | 120 帧中暗色 11 张、无「暗色+窄窗」组合、无 900×600、无 TextScale 帧；新表面窄窗测试只断言文本存在。`03` 矩阵 #17/#18/#23 | ui-snapshot 补 insights/context/approval/team/cold-resume 的 1024/900×600/dark/TextScale 帧；窄窗测试加矩形断言 | M |

---

## 4. 建议批次与验收

### Batch A — 正确性 / 遮挡 / 可达（P1，约 1–2 周）

**目标**：让不可逆决策可核对、长任务可停、右上角不再挡内容、命令面板与窄窗可用。

范围：P1-1（审批 diff）、P1-2（队友停止/关闭）、P1-4（toast 可操作，位置保持右上角）、P1-5（palette 滚动）、P1-7（前台会话静默）、P1-8（Context 轴/属性表）、P1-9（Insights 对比度）、P1-6 的**补帧先行**、P2-12（输入框去边框 + 附件直连选择器，用户点名）。

**Exit criteria**：`p1-approval` 含 diff；team running 行可停；16 命令 palette 选中行在视口内；toast 位置/inset 不变且「打开会话」可用；Context 三 tab 左缘一致；Insights 选中行亮/暗对比 ≥4.5:1；洞察屏后台 run 有通知；新增 900×600 帧（审批+goal+队列）后即使只得出「需分阶段」的结论也算完成该子项。

### Batch B — 长任务运行台（P2，约 2–3 周）

**目标**：从「聊天里夹着运行状态」升级为「运行状态有统一去处」，但不新增一级导航。

范围：P1-3（执行域历史面）、P2-1（palette goto/设置页）、P2-2（设置条目搜索）、P2-3（恢复元信息）、P2-4（FindBar）、P2-5（轨迹）、P2-6（团队检查器页，产品拍板后）、P2-7/P2-8（双选中/返回/rail）、P2-9（托盘/标题）。

**Exit criteria**：后台 run/审批/作业在「会话屏以外」也能被发现；通知可点回会话、可回看、可按事件类型关闭；长会话能 Cmd+F 定位；恢复会话不再出现裸 `⋯`；团队/任务可在检查器持续查看（若拍板）。

### Batch C — 共享层收口与证据债（P2/P3，持续）

范围：P3-1（SegmentedControl/PropertySheet/TableColumn token）、P3-2（状态三层契约）、P3-3（字号）、P3-4（IconButton）、P3-11（暗色/窄窗/TextScale/900×600 帧与几何断言），以及 P3-5..P3-10 的小项。

**Exit criteria**：新表面只通过 uikit 入口；`designlint --strict` 0 new/0 blocking；暗色/窄窗/TextScale 有帧且人工看过；无 baseline 新增。

### 需要产品拍板的问题

1. **团队/任务持久工作台**：检查器固定页（推荐，复用现有 IA）还是 980×740 overlay（不改 IA 但更重）？
2. **通知默认值**：完成类默认关、失败/审批默认开？是否要「仅应用在后台时」默认开？
3. **Home 屏幕**：删除（只留会话 hero）还是给低权重入口？当前「注册但不可达」是最差状态。
4. **设置重分组**：是否接受把通知/记忆/归档从「高级」提到日常组（会改变用户路径与测试断言）？
5. **示例 prompt**：空会话 hero 是否放 2–3 个真实示例（面向长任务/文件/自动化）？
6. **审批 diff 的粒度**：只给 write/edit，还是所有文件类工具都给；超大文件/二进制的回退阈值。

---

## 5. 明确不建议做（anti-scope）

1. **不重做整体风格/信息架构**：Quiet Cockpit 的结构（侧栏 → 阅读列 → docked 检查器 → composer）已拍板且适合开发者；问题在语义、控制与可达性，不在版式。
2. **不加 dashboard / 图表 / 卡片墙 / 更多 KPI**：Insights 的克制是对的；数据页的问题是宽度、选中态与下钻，不是「缺图」。DESIGN §2.3 已禁止自绘图表。
3. **不新增一级导航**（团队/任务/队列/运行中心都不进侧栏一级）：用页头 Run Band + 检查器固定页收口，避免稀释「工作区/会话」主队列。
4. **不为「显得完整」加设置页/首启引导/第二落地页**：Home 应删除或低权重，不应扩张。
5. **不删状态条 6 项、不隐藏团队/子代理常显入口**（AGENTS §3.17 明确禁止）：本轮只改「是否遮挡/是否可操作/是否可读」，不靠减法解决拥挤。
6. **不引第三方 UI/图标/图表库、不建第二套设计系统**：全部分段/属性表/图标按钮/行组件都进 `internal/uikit`。
7. **不向 `.design-baseline.json` 加新条目**来绕过 lint；新违规直接修，存量只在 owner 批准下 shrink。
8. **不做 640×480 适配**：低于产品最小窗 900×600，历史已接受不修。

---

## 6. 验收与证据

- **交互**：每个交互改动补 `ui.NewTester` 帧测试；本轮特别需要「矩形不相交/选中行在视口内/对比度」这类几何与像素断言，而不只是「文本存在」。
- **视觉**：`ui-snapshot` 至少补 **900×600（审批+goal+队列）**、**1024×720 暗色**、**insights/context 窄窗**、**TextScale 1.5**；人工看图，不接受只看 diff。
- **静态**：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；`designlint --strict --baseline` 0 new/0 blocking；`scripts/design-check.sh`；`go tool mygo vet .`。
- **文档**：新增/迁移 uikit 组件同步 DESIGN §6/§7、registry、UI-CONFORMANCE；审批 diff 同步 AGENT-SURFACES S4；状态三层同步 DESIGN §5.2。
- **本次评审证据**：三份子报告 + `.cache/ux-review/shots/`（120 PNG）+ `.cache/impl-shots/`（122 PNG，含实现后真实帧与审批/差异标本）+ 关键帧 `p1-approval.png`、`p1-chat.png`、`p2-palette.png`、`p2-files.png`、`p2-insights.png`、`p2-context.png`、`p2-team-overlay.png`、`narrow/narrow-session-inspector.png`、`narrow/narrow-settings.png`。

---

## 6.2 效果图（prototype，未改产品代码）

以下帧由仓库现有 `mygo ui` + `internal/design` + `internal/uikit` 在 headless tester 中渲染，**是设计验证稿，不是已实现的产品代码**；其中「现在」为当前产品帧或同构组件，「建议」为提案。文件在 `.cache/ui-preview/final/`：

- `01-composer.png`：输入框三个小方块 → Toolbar 无边框；附件按钮直连原生文件选择器（删除手动路径入口），chip 浅底无描边。
- `02-approval.png`：审批卡「摘要」和「原始参数」之间加 `将改动` 的 unified diff；其余安全语义不变。
- `03-palette.png`：命令面板首屏可见选中行 + 操作/前往/设置分组 + 「前往…」「设置：页名」。
- `04-context.png`：Context 与对话同轴（960 居中），属性表 160px 标签列 + 值左对齐。
- `05-notification.png`：toast 保持右上角不变，session 通知增加「打开会话」动作（位置不变）。

落地后的真实产品帧见 `.cache/impl-shots/`（本段为设计验证稿，不是实现替代）。实现方式（P2-12）：不手写去边框按钮，而是把三个切换器放进 **官方 `ui.Toolbar`**（toolbar 内按钮的 `base/border` 为零，hover/pressed 由原生状态机提供），发送/排队/停止仍在 Toolbar 之外，避免溢出菜单吞掉主操作；附件从「菜单里选『选择文件…』或『手动输入路径…』」改为「点击即打开原生 picker」，拖入/粘贴图片保留，非桌面环境不支持 picker 时用错误行/提示说明，不再要求手输路径。

---

## 7. 一句话路线图

**先修「挡住/够不到/看不见选中」，再补「不可逆决策与控制」，最后收口共享层与证据债。** 视觉方向不变；把长任务从「聊天里的附属状态」升级为「有一致入口、可控制、可回看、可通知」的运行台，是这一轮最大的 UX 增量。

> 子报告（实现时按它们展开）：
> - [01 · 会话/Agent 表面](research/ui-ux-upgrade/01-conversation-surface.md)
> - [02 · 壳层/IA/设置](research/ui-ux-upgrade/02-shell-ia-settings.md)
> - [03 · 设计系统/跨屏质量](research/ui-ux-upgrade/03-system-quality.md)
