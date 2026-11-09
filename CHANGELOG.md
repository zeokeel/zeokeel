# Changelog

All notable changes to ZeoKeel are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/).

## [0.1.2] - 2026-11-09 · 交互修复轮

本轮针对真实使用反馈修复工作区 / 审批 / 输入区 / 用量显示 / Windows 编码，并加入
DSH 风格的按工具系统提示注入与更宽松的默认权限记忆。

### Added

- **按工作区新建会话（⊕）**：侧栏每个工作区分组行右侧新增「在“X”中新建会话」按钮
  （当前工作区常显，其余悬停/聚焦/键盘聚焦时出现；焦点在行上时 Space 触发）。点击会
  打开该工作区、在其中创建新会话、展开分组并进入会话页——新建工作区后可以立刻开始对话。
  全局「新对话」也会展开当前工作区的分组。
- **DSH 风格的系统提示注入**：每次运行都会重建系统提示，开头是身份 + 运行环境（真实
  OS/架构、应使用的 shell、工作目录与文件策略、当前审批策略），结尾按已注册工具逐条注入
  用法提示（read_file/list_dir/glob/grep/write_file/edit_file、pwsh/bash、terminal、jobs、
  web、subagent 等）——模型从一开始就知道这是 Windows（用 pwsh，不存在 bash），文件读写
  优先用专用文件工具而不是 shell 命令。子 agent / 团队成员共用同一份提示。
- **会话级「本会话内始终允许」**：审批弹窗的中键改为「本会话内始终允许」，选择后该工具在
  本会话后续运行中不再询问（持久化到 settings，删除会话或从会话菜单「清除自动允许的工具（N）」
  可清除）。弹窗在多个请求排队时显示「还有 N 个待审批请求」，工具名显示为本地化名称。
- **默认「工作区」权限放宽**：工作区模式现在自动放行工作区文件工具与只读/信息类工具
  （working_directory、read_image、memory、skill(s)、settings_get、todo*、team_task_list/get、
  terminal_list、job_* 读取、session 查询、lsp_* 等）；shell、网络、MCP、settings 写入与未知
  工具仍会询问。两处权限判断共用 `contracts.WorkspaceAutoAllowedTools` 单一清单。
- **会话底部状态条（DSH 风格）**：输入区下方新增一条安静的单行状态：`N 轮 · M 步 ·
  X tok/s · 上下文 U/窗口 · 缓存命中 P% · 剩余 Q%`（数据不准确/未知时省略对应项，Tooltip
  给出精确值）。上下文用量改用 `MessageUsage.ContextTokens`（最后一次模型调用的 prompt
  token 数，由新增的 rig.EventUsage 采集），不再拿累计计费 token 或压缩预算当上下文窗口。
- **Windows 命令行输出编码修复**：新增 `internal/lib/textenc`，在工具代理层与右侧终端
  统一把 GBK/cp936 / UTF-16LE 输出修复为 UTF-8（合法 UTF-8 原样通过）；中文 Windows 上
  `python …`、`cmd` 等输出不再乱码。
- **后台任务完成通知真正送达模型**：toolkit 的 root-scoped 工具代理实现 rig `NoticeSource`
  并在每个工具轮次后向 agent 排空 `jobs.Manager.PendingNotices()`，agent 侧工具包装器透传该
  接口；后台 job 结束会以 `Background notifications:` 形式进入下一轮上下文（此前文案承诺
  “会收到通知”但从未接线，真实会话里 6 个后台 job 全部滞留且模型一直在等通知）。
- **重复工具调用提醒（DSH loop hygiene 移植）**：agent 每 run 跟踪「工具名 + 规范化参数」
  连续相同调用，在第 3/5/8 次时在结果尾部附加 DSH 原文建议（只提示不阻断、todo 记账调用透传、
  新用户消息清空链）。
- **会话底部状态条（DSH 风格）**：新增 `p2-running-composer.png` / `p2-status-strip.png`
  截图记录「生成期间输入框常驻」与「轮/步/tok/s/上下文/缓存命中/剩余」状态条。

### Changed

- **新建工作区 = 直接选择目录**：点击「添加工作区」直接打开原生目录选择器，不再需要命名
  对话框；名称由后端按目录名推导，选中已注册的目录视为切换工作区；成功/失败用 toast 或
  标题行状态/错误行反馈。
- **去掉工作区「视图」菜单**：产品固定为「按工作区划分 + 会话最近活动排序（组内置顶优先）」，
  不再有排序/平铺开关与 `sidebar.sort`/`sidebar.group` 设置行；工作区分组按各自最新活动排序
  （空工作区按创建时间），新建工作区会出现在顶部；没有工作区后端时才回退到单一平铺列表。
  侧栏工作区树在零会话时也始终渲染分组行与 ⊕（此前空状态会隐藏整棵树）。
- **会话输入框在生成期间保持可见**：发送消息后输入框不再消失；排队发送默认开启
  （`ui.sendWhileRunning` 仅显式 `"false"` 关闭），运行中显示「排队 + 停止」，草稿保留；
  排队数据按会话保存，切走/切回不丢，由模块订阅在回合结束时发送给所属会话；「停止」会暂停
  排队发送而不是立刻把队列发出去。
- **用量数字改为紧凑单位**：`用时 358.8s · 173.6K tokens`（K/M/B，保留一位小数），不再显示
  `173,635` 这类长数字；展开明细同样使用紧凑单位，精确值在 Tooltip。
- **审批弹窗不再有默认操作**：此前「拒绝」是默认动作且打开即聚焦，用户刚按 Enter 发送消息、
  弹窗随即出现时，二次 Enter 会误拒工具调用（真实 Windows 会话导出里出现过一次 pwsh 被误拒）。
  现在不自动聚焦任何按钮、Enter 不做决定；Esc 仍显式拒绝，允许/拒绝都需要点击或 Tab+Enter。
- **工具指引增强**（来自真实会话复盘）：pwsh 行补充 Windows `Start-Process` 启动的是分离子进程
  （服务器型任务用 `run_in_background`、记 PID 并 `Stop-Process`）；jobs 行改为 DSH 原文并
  提醒服务器型 job 不会自己结束、别留多份。
- **全仓手搓 UI 审计后的官方组件化（第三轮）**：独立审计逐条核对 mygo v0.8.3
  文档后，把剩余 21 处真正手搓的 UI 迁移为官方组件（审计/证据：
  `.cache/ui-audit/report.md`、`.cache/migrate-shell/report.md`、
  `.cache/migrate-settings/report.md`）：
  - 设置页 7 个集合（记忆/模型目录/MCP/LSP/提示词预设/代理预设/诊断模块）由
    `for…range`+`Divider` 改为官方 **`ui.List`**（Key/Label/Selected/Rows，行内按钮保留，
    重排后 Key 跟随；行名/键盘/type-ahead 由控件提供）；
  - 右栏**产物历史**与 **composer 排队弹层**改为 `ui.List`（前者 Enter/双击展开，
    后者有界高度、逐条取消保留）；
  - Files **面包屑**改为官方 **`ui.Breadcrumbs`**（navigation landmark、Link 角色、
    CurrentPage、点击/Enter 导航、长路径 ellipsis；深路径保留 first→…→last two）；
  - **会话重命名对话框**补齐 `uikit.DialogFooter` + `AutoFocus` + `EnterTo`（全仓不再有
    手写 footer 的对话框）；
  - 消息评分与 /feedback 改为官方 **`ui.Toggle` / `ui.ToggleGroup`**（pressed 语义；
    `ui.Segmented` 无法表达“未评价”，偏差与备选方案已记录）；
  - 8 处文字忙碌态（更新检查/下载/安装、导入扫描/导入、模型连接检查、记忆提取、
    附件选择/压缩）改为官方 **`ui.Spinner`**（不伪造 Progress 百分比）；
  - 记忆 pane 的 Unicode `✎` 改为新增的 Lucide **`icons.Pencil()`**；设置「未保存」
    裸文本改为官方 `ui.Badge`；
  - 保留自绘项均有 `custom-widgets.md`/headless base 依据并列入审计报告：composer 卡片、
    slash/@ 富行建议菜单、命令面板按钮行、消息 hover 动作行、无边框图标按钮、tabs
    下划线、思考/工具行 trigger、toast parts、附件 chip 等；`ui.Rating`/`ui.Table`/
    `ui.Progress`/`ui.FindBar`/`ui.Accordion` 等经核对语义不适用，明确不“为用而用”。
- **会话过程展示改用 mygo 官方组件**（推理/思考、工具调用）：
  - 思考块由「`ui.Collapsible` + 另一行手搓的 `思考中…` + 原始 tail 预览 + 纯文本正文」改为
    **单个 `ui.CollapsibleBase`**：流式时 trigger 为官方 `ui.Spinner` + 「思考中…」，结束后为
    思考图标 + 「思考」+ 折叠行数；chevron 由 `parts.Progress()` 驱动；正文用官方
    `ui/markdown.Stream`/`View` 以更小更淡的 Markdown 主题渲染（不再有「思考中… ile.」这类
    原始 tail 文本，也不再重复渲染）。
  - 「生成中」指示器由 `ui.Badge` + 静态 Sparkles 图标改为官方 `ui.Spinner`（带无障碍 label）。
  - 工具调用行由手搓 chevron `ui.Button` + 原始 snake_case 工具名改为 **`ui.CollapsibleBase`
    整行 disclosure**：图标 + 本地化友好工具名 + 状态（运行中显示 `ui.Spinner`）+ 单行预览 +
    `parts.Progress()` chevron；原始工具 id 只出现在 Tooltip/无障碍描述里。H15 默认展开规则、
    每行折叠状态、`HoldPlace` 保持。
  - 决策卡与轨迹 Tab 的 `ui.Collapsible`、正文 `markdown`、会话 `ui.List` 本来就是官方用法，
    保持不变；`ui.Accordion`（设置类多分区）与 `DiffViewer`（读模型没有 diff 数据）经审计后
    **不使用**，原因记录在 `.cache/transcript-ui/report.md`。
- **删除当前工作区后的回退**：删除当前工作区时自动选择剩余工作区中最近创建的一个作为当前
  工作区（原子持久化，失败回滚），避免工具根变空后文件/shell 工具静默失败。

### Removed

- 侧栏工作区视图菜单、名称排序/平铺选项及其 `sidebar.sort` / `sidebar.group` 设置键
  （旧的遗留设置行会被忽略，不再写入）。
- 添加工作区命名对话框与「选择目录…/名称/目录」表单字段。

### Fixed

- 新建工作区后无法直接开始对话（重做 ⊕ 动作 + 空会话树渲染）。
- 中文 Windows 下命令输出乱码（见上）。
- Windows 上模型误用 bash（平台感知提示 + Windows 不注册 bash；非 Windows 仅在 PATH 中确实
  存在 pwsh 时才注册）。
- 会话切换会静默丢弃排队消息；生成结束时若不在会话页排队消息不会发出；停止后队列被立即发送。
- 审批弹窗工具名显示英文原始 id、多请求无计数提示。
- 空白标题会话在英文界面无法用 “Untitled” 搜索到。
- 切换工作区时旧工具构建（及其后台 job / terminal 进程）一直留到进程退出：现在在最后一个
  in-flight 调用结束后释放（不中断正在运行的调用）。
- 「停止」被归类成普通错误（`errorKind=other`，UI 显示原始 `cancelled`）；现在映射为本地化的
  「已取消」。
- 一个长且不含空格的工作区名会把该行的 ⋯/⊕ 挤出 280 DIP 侧栏：名称文本现在可收缩
  （`MinWidth(0)`），行内操作仍在栏内且可点击。
- Esc 关闭斜杠菜单后清空输入再输入同样的 `/`，菜单不会重新打开（提示词挂起状态残留）：
  输入为空时清除该状态，并提供回归测试。
- 极短回合（<250ms，如脚本化/取消回合）不再显示五位数 `tok/s` 这种无意义的平均速度。

### Verification (2026-11-09, v0.1.2)

- `./scripts/verify.sh` (full): **10/10 PASS** — gofmt, build, vet, full test
  suite, `-race` (384s), regression **287 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot **83 steps / 81 PNGs**, desktop (Xvfb, tray) / headless / ACP
  smokes green; **535s** total.
- `go test ./...` 41 packages PASS; `go build ./...` and
  `GOOS=windows go build ./...` PASS; `go vet ./...` clean.
- `internal/devtools/ui-drive`: **84/84 PASS** (0 fail / 0 warn) on the final
  tree, including the new workspace ⊕, transcript reasoning/tool rows and
  approval no-default steps.
- Independent audits and evidence:
  `.cache/ui-audit/report.md` (hand-rolled UI audit),
  `.cache/migrate-shell/report.md` / `.cache/migrate-settings/report.md`
  (official-component migrations), `.cache/session-debug/report.md` (real
  Windows session export replay), `.cache/audit-round2/verification.md`.

---

## [0.1.1] - 2026-11-09

Second public ZeoKeel release: the UI/UX conformance pass against mygo v0.8.3,
the settings consolidation, denser and virtualized transcript/tool cards, dead
code and legacy archive removal, and the zeokeel-rig v0.13.2 SDK upgrade.

### Changed

- **Rig SDK v0.13.2**: `github.com/zeokeel/zeokeel-rig` v0.13.1 → v0.13.2, whose
  Windows command-allowlist comparison now ignores the `.exe` suffix and case. The
  host shell allowlist therefore lists bare `pwsh`/`powershell`; the temporary
  `.exe` entries added as a v0.13.1 workaround are gone. No other behavior change.
- **Settings consolidation**: the separate 模型目录 (Model catalog) page was
  merged into the 模型 (Model) page — one place now owns the provider
  connection, the model list, thinking-family/mode editing, custom modes and
  per-model generation parameters. The page title stays 模型; the standalone
  catalog page and its navigation entry are gone. The merged page keeps the
  connection form's footer Save and immediate model-list edits, with the
  editor's generation/custom-mode groups behind one 生成与自定义模式 disclosure.
- The provider quick-preset "自定义" chip was renamed to 手动配置 / Manual setup
  to distinguish it from the thinking-family "自定义" (custom) option.
- **Transcript order**: assistant text and tool cards now interleave in event
  order (`ToolCall.TextOffset`), so a reply that called tools keeps its
  pre-tool text above the calls and its final text below them instead of
  drawing the whole reply above the tool cards.
- **Quieter tool calls**: transcript tool cards are now compact one-line rows
  (muted icon/name, subtle status, one-line result preview and a small 详情
  disclosure) instead of full-width panels; decision tool cards (plan review,
  exit plan, ask user) stay prominent and the trajectory pane keeps its
  detailed cards.
- **Default permission is now workspace-level**: an unset `agent.permission`
  resolves to workspace everywhere (desktop assembly, approvals gate, session
  menu, behavior page). Workspace mode auto-allows the workspace file tools
  (read/write/edit/str_replace/list/glob/grep) without asking; shell, network,
  MCP and unknown tools still ask. The composer label stays quiet
  (工作区权限/Workspace) while full/never and non-default chat modes keep the
  warning label.
- **Unlimited iterations by default**: the agent assembly no longer sets
  `maxIterations: 20`; 0 means no practical limit (the run passes an effectively
  unreachable cap to Rig, which otherwise defaults to 20). The model page's
  runtime summary shows 不限制/unlimited when unset; a numeric value still caps
  the run.
- **mygo v0.8.3**: upgraded from v0.7.4 and adapted to the stricter Tester
  hit-testing, the richer menu APIs (model menu now uses `Menu.MaxWidth` +
  `MenuItem.Subtitle`/`Trailing` for model notes and context windows), the
  official `NewTesterWith`/`Tester.AccessByRole` test APIs, and GitHub
  alerts/mentions/references in Markdown. Hand-written import/skill input
  accessibility labels now follow the shared `inputLabel()` pattern.
- **Domestic Go module mirror**: the Makefile, `scripts/verify.sh` and CI
  workflows default to `GOPROXY=https://goproxy.cn,direct` with
  `GOSUMDB=sum.golang.org`; offline builds can override both to `off`.

### Removed

- **Legacy archive**: the 389 MB pre-rewrite `old/` module (836 tracked files)
  was removed from the repository, together with its `.gitignore` rules and
  the obsolete rebuild-plan/status docs that pointed at it. Historical design
  research remains in `docs/research/`.
- **Dead code**: unused handlers, helpers, test fakes and legacy constants
  found by `deadcode -test` / `staticcheck U1000` were removed (host tray
  helpers, old agent-preset picker remnants, toolcard color helpers, session
  goal-menu state, the unused diagnostic-export seam, run-command helper and
  stale test harnesses), while keeping the public contracts and behavior
  intact.

### Fixed (UI conformance, mygo v0.8.3)

A full UI/UX conformance pass against `mygo v0.8.3/docs/ui` and the
`design-guide` skill; the inventory and the rules live in
[docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md).

- **Shell**: the sidebar and the inspector are native `ui.Split` panes with
  persisted widths and `SplitReset`; the inspector draws as an accessible
  modal drawer (`RoleDialog`, Escape, focus trap, scrim token); the window menu
  presentation is unified with `Context.SetMenuPresentation(ui.PopoverMenus)`;
  the top band is a `ui.Toolbar` with visible command-palette and inspector
  entries; the palette list has a name and `ActiveDescendant`; screen changes
  and the inspector announce to assistive technology; chrome toggles ignore
  key auto-repeat; the rail is a keyboard `FocusGroup`; a back affordance and
  Alt+Left follow the navigation stack.
- **Brand theme**: `Theme.InactiveAccent` keeps the brand accent in inactive
  windows (matching the hand-drawn accent surfaces, locked by a test);
  `design.Of` wires the desktop text-size preference into `Tokens.TextScale`
  and the root theme scales its `FontSize` by the same factor, so native
  controls and brand-drawn text grow together (both covered by tests).
- **Shared kit**: `uikit.DialogFooter` supports a default (focused, Enter)
  action; `PageHeader`/`Section` titles are headings; `ErrorLine` is an alert
  live region with a real icon and selectable text; `KeyValueRows` values are
  selectable; one shared `uikit.EnterTo` and one shared `uikit.ToolIcon` mapping
  replace the per-module copies.
- **Right inspector**: the Changes pane renders the official
  `editor.DiffView` (word-level inline changes, line numbers, hunks) with a
  bounded text fallback for unparsable patches; Files/Log/Tools/Tasks/Agents
  are native `ui.List`s with keyboard selection and row a11y; the terminal
  shell picker is a `ui.Select` and its output follows the end; the directory
  listing and the session log are cached and refreshed explicitly instead of
  reading disk/SQLite every frame; empty states, file/image icons and
  selectable previews follow the guide.
- **Sidebar**: the workspace tree is a native `ui.Outline` (arrow keys open and
  close), session rows are a `ui.List` with `Key`/`Label`/`Selected`; right
  click and the row's ⋯ menu share one menu built with `ContextMenu` +
  `PopoverMenuButtonOptions`; destructive items use `MenuItem.Danger()`; the
  add-workspace dialog uses the shared footer with Enter submission.
- **Session**: the goal chip uses the official `PopoverMenuButton` menu
  (Danger items, stable IDs) instead of a hand-drawn popover; permission, chat
  mode, thinking, model and preset choices are `MenuItem.Radio` (single-choice
  semantics) with model `ID`/`Subtitle`/`Trailing`; the slash/@ suggestion
  popups are named list boxes with option roles, positions and an
  `ActiveDescendant`; dialogs accept Enter, and dialog widths use the design
  tokens; the English session menu is "Session settings" so it no longer
  collides with the global Settings.
- **Settings**: the memory page caches its read model (no per-frame SQLite),
  and add/edit/tidy/clear/extract invalidate it; dialogs submit on Enter
  (model catalog, presets, memory, policy); the data & privacy page makes
  exporting the primary action and opening the data directory secondary;
  diagnostics use localized unknown values; the stale English subtitle is
  fixed; settings dialog widths use tokens.
- **Other screens**: Home recent sessions are a `ui.List` with localized
  dates; Automation summaries are localized, truncated and hide internal
  session IDs, its enable switch and filters use Switch/Radio; Plugins show a
  localized tool summary with the original text in the tooltip and condense
  MCP errors; Approval uses `uikit.DialogFooter` with Deny as the focused
  default (Enter = deny); toasts use `ShadowOverlay` and grid tokens; icons for
  image/file reads are the correct glyphs, and an icon invariant test locks the
  SVG grid/stroke rules.
- **Official collections**: every remaining collection is a native `ui.List`;
  the model catalog editor's custom thinking-mode rows and the help dialog
  command list were the last two `Scroll`+row loops and now virtualize/scroll
  with the official list (keys follow the mode ID, falling back to the row
  while a new mode has no ID).
- **Bounded transcript**: a conversation builds only its newest 200 messages by
  default (a "load earlier messages (N)" row reveals the hidden range in
  steps), so streaming a reply no longer re-lays out thousands of rows in a
  long session; the test covers the tail window and the load-earlier step.
- **Number inputs where the official control fits**: the memory consolidation
  policy (max entries / max retention), the model runtime limits (max
  iterations / context budget, each with a "use the module default" switch that
  clears the settings row), the model context-window fields and the model
  generation parameters (temperature, Top P, max output tokens — each with a
  "use the provider default" switch) are native `ui.NumberInput`s: stepper
  buttons, arrow keys, range clamping and immediate application. The page tests
  were rewritten to the control's range semantics with one shared
  `typeNumberInput` helper, and the old free-text "must be a number / in range"
  error paths are gone.
- **One action-placement rule for settings**: `uikit.PageHeaderAction` and
  `uikit.SectionAction` put a page's immediate-effect actions (new skill,
  refresh, check updates) on the page header row and a section's utility
  actions (restore presets/defaults, export the diagnostic report) on the
  section heading row. The explicit-save pages keep their single primary action
  in the host footer, so the old mix of bottom-of-content and right-aligned
  action rows is gone; frame tests pin the placement and the migrated pages'
  tests pass.
- **Dead injection points removed**: the contract's `SlotTopBarBrand/Center/
  Actions` and `SlotOverlaySettings/Palette` constants, the welcome module's
  top-bar marker registration and the corresponding test assertions are gone.
  The shell has no top bar (screen headers plus the content-top toolbar own the
  global actions); the architecture/injection-point docs now say so instead of
  inviting modules to register slots the host never renders.
- **Native dialogs take their title as an argument**: the package-level dialog
  title variables (workspace folder, session export markdown/JSONL, data export
  and diagnostic report) are gone; every picker/saver seam now receives the
  frame's localized title, so two windows in different languages cannot race a
  shared global. Tests capture the title from the seam argument and one
  regression probe now checks both languages instead of skipping a known defect.
- **Transcript is a native List**: the conversation transcript now uses the
  official `ui.List` (`ListState.Key` by message ID, `Label`, `FollowEnd` for
  streaming, `HoldPlace` when a fold opens, `ScrollToEnd` behind the floating
  back-to-latest action) instead of a `Scroll` plus a bounded message loop, so
  only the visible rows build and long conversations stay fast. The help
  dialog's command list and the trajectory timeline use `ui.List` as well.
- **Brand new-chat button**: the sidebar's New chat action is the official
  `PrimaryButton` (theme accent + native hover/pressed/focus states) instead of
  a hand-drawn `ButtonBase`; its icon+label stay the button content and the
  label remains the accessible name.
- **Remaining collections are Lists**: the sidebar's message-search hits, the
  settings archive rows and skill cards, and the Plugins page's MCP servers,
  built-in modules, curated settings and tool catalogue all use official
  `ui.List` states (stable keys, named rows); the slash/@ suggestion popovers
  stay custom because their rich rows need the documented listbox/option
  accessibility model rather than a plain text autocomplete.
- **Read-only code preview**: Files' source previews use the official
  `editor.View` in read-only mode (syntax highlighting by extension, line
  numbers, soft wrap, virtualization) instead of a plain mono text block; the
  tests read the content from the editor's accessibility text value.
- **Agent preset tools are a TokenField**: the comma-separated tools text
  input is the official `ui.TokenField` (tokens as chips, Enter/comma to add,
  Backspace to remove), and the preset still persists `[]string`.
- **More native Lists**: the Memory tab's entries and the Automation screen's
  scheduled tasks are official `ui.List`s (keyed by ID, named rows, virtualized
  scrolling) with the search/tools/add rows fixed around them, so the last
  hand-drawn scrolling collections are gone.
- **Settings navigation follows the page**: the low-frequency 高级 group now
  unfolds for an advanced page and folds again when a daily page is selected,
  so the settings navigation no longer keeps the previous visit's longer
  layout (the snapshots showed both widths depending on the order pages were
  opened). A frame test covers the group folding after a daily page is chosen.
- **Bounded per-message UI state**: switching conversations drops the fold /
  usage / unfolded-actions / transcript-window entries of the conversation that
  no longer shows (one internal test covers the pruning), and sent attachments'
  decoded thumbnail caches are released, so a long-running app does not grow
  state for every session it ever opened.
- **Grouped subprocess fields**: the MCP add form's STDIO fields are a native
  `ui.Fieldset` with a legend, so assistive technology reads the group with
  every field inside it.

### Verification

- `./scripts/verify.sh` (2026-11-09): **10/10 PASS** — gofmt, build, vet,
  full test suite, `-race`, regression **284 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot (81 steps / 79 PNGs), desktop (Xvfb) / headless / ACP smokes green.
- All six release archives cross-compile with `CGO_ENABLED=0`
  (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64,
  windows/arm64); SDK dependency is `github.com/zeokeel/zeokeel-rig v0.13.2`.

## [0.1.0] - 2026-11-08

The first public ZeoKeel release: a compile-time modular AI agent for the
desktop and the headless/ACP worlds, written in Go and drawn with native mygo
controls (no webview, no browser engine).

### Added

- **Compile-time modular architecture**: one binary, assembly products
  (`desktop` / `headless`), a kernel of contribution points, and `assembly.json`
  entries that enable or trim modules without runtime plugin shipping.
- **Desktop product**: native chat UI with streaming answers, reasoning
  preview, tool cards, session history, workspaces, right-side inspector
  (overview/tools/log/files/changes/terminal/team/tasks), command palette,
  keyboard shortcuts, tray/notification integration and global hotkey.
- **Headless + ACP**: the same binary runs `--headless` (with tools, memory,
  sessions, automations) or as an ACP stdio server for editor integration,
  including image prompt blocks.
- **Agent capabilities**: tools (shell/file/read/edit/todo), approvals and
  permission modes (ask/workspace/full/never), agent/chat modes
  (normal/plan/minimal/creator), subagents and team management, goals with
  continuation, plan review, memos/memory, skills, MCP servers, LSP tools,
  prompt/agent presets, session references and context compaction.
- **Model configuration**: providers and quick presets, per-session model
  switching, per-model thinking configuration for Qwen / DeepSeek / other
  families with custom modes, per-model generation parameters
  (temperature / top_p / max_tokens), runtime limits (max iterations, context
  budget, stream-completeness check) and a model catalog editor.
- **Settings surface**: 19 pages in five groups, with low-frequency advanced
  pages collapsed behind one group; progressive disclosure on the model,
  behavior and model-catalog pages; searchable navigation, fixed footer
  actions and unsaved-changes guards.
- **i18n**: Chinese and English UI, localized errors and date formatting.
- **Packaging**: cross-platform release builds with explicit OS + architecture
  asset names — `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
  (`.tar.gz`) and `windows/amd64`, `windows/arm64` (`.zip` with `zeokeel.exe`);
  version/commit/build-time injected at link time, `CGO_ENABLED=0` pure-Go
  builds stripped with `-s -w`, gzip/zip-compressed archives, `SHA256SUMS.txt`
  and a download table in the release notes; CI on push/PR (including a
  cross-compile check for every release target) and a tag-driven release
  workflow.

### Fixed

- ACP failures are reported as JSON-RPC errors; interrupted turns persist
  partial assistant output and can be regenerated without losing reasoning.
- Oversized tool results spill to disk for both desktop and headless sessions.
- Truncated model streams are reported instead of silently appearing complete
  (zeokeel-rig `RequireCompleteStream`, v0.13.1).
- Attachment picker runs off the UI thread; manual path fallback for
  portal-less environments; per-conversation composer drafts and attachments.
- Composer action row simplified (attach menu, model, session menu, send);
  narrow-window overflow and session-menu accessibility fixes.
- Memory update/edit, model test-connection details, approval copy, and a
  large batch of UI interaction fixes found by independent audits.

### Verification

- `./scripts/verify.sh` (2026-11-08): **10/10 PASS** — gofmt, build, vet,
  full test suite, `-race`, regression **284 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot (83 steps / 81 PNGs), desktop (Xvfb) / headless / ACP smokes.
- Desktop smoke: window + tray + single instance under Xvfb, no panics.
- Model thinking/generation mapping verified by captured request bodies and
  unit tests; SDK dependency is `github.com/zeokeel/zeokeel-rig v0.13.1`.

[0.1.2]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.2
[0.1.1]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.1
[0.1.0]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.0
