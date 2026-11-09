# UI 官方一致性清单（mygo v0.8.3）

这份清单把 `design-guide` 与 mygo v0.8.3 的官方能力映射到本仓库的用法，目的是让
下一次 UI 改动的第一步是「查官方文档/组件」，而不是「先手搓一个」。新增/修改 UI 时
先对照本表，再读官方文档。

官方源：`/root/go/pkg/mod/github.com/egoist/mygo@v0.8.3/`（`docs/ui/*.md`、
`examples/*`、`ui/` 源码）。**不要用 v0.7.x 的行号/语义**；升级时先 `go get` 再复核。

## 1. 官方标准件速查（按场景）

| 场景 | 官方组件/API | 官方文档 | 本项目规则 |
|---|---|---|---|
| 窗口骨架 | `Row + Sidebar + Grow 主列 + Split` | layout.md / sidebar.md / split.md | 壳层用 `ui.Split`（可拖拽、`SplitReset` 复位、宽度存 host settings），不要再写固定宽度算术 |
| 可拖分栏 | `Split/SplitPercent/SplitReset` | split.md | 侧栏/右栏宽度必须可拖且持久化 |
| 抽屉/浮层 | `ui.Overlay` + `Row…Modal()` + `Role(RoleDialog)` + `PressedOutside` + `OverlayShortcut(Esc)` | overlays.md「A drawer on the right」 | 自绘 drawer 必须走这个 recipe（Esc/焦点陷阱/焦点归还） |
| 长列表/日志/会话 | `ui.List` + `ListState`（Key/Selected/FollowEnd/HoldPlace/Label/**Header**） | list.md | 集合行不再用 `Scroll`+全量循环；测试用 `Rows`/`ItemKey`；分组列表用 `Header` 段头+计数（任务板），选中行用 `Selected`/`Submitted` 展开详情，详情渲染在列表**下方**的独立面板（选中行有 accent 背景，详情放行内会不可读） |
| 表格 | `ui.Table` / `TableColumn` / `CellSelect` | table.md | 多列可排序数据用 Table，不用 Row 手排 |
| 树/大纲 | `ui.Outline[T]` / `ui.Tree` / `OutlineState` | outline.md / tree.md | 工作区/文件树用 Outline |
| 菜单 | `PopoverMenuButton` / `PopoverMenuButtonOptions{At,Self,Open}` / `MenuItem.Danger/Radio/ID/Subtitle/Trailing/Content/MaxWidth` / `MenuBase` | menu-button.md / context-menu.md | 行尾⋯菜单右对齐；删除项 `Danger()`；单选 `Radio()`；稳定选择 `ID()`；自绘菜单迁移 `MenuBase` |
| 右键菜单 | `Element.ContextMenu(menu)` / `ContextMenuBase` / `Context.SetMenuPresentation(ui.PopoverMenus)` | context-menu.md | 同一 menu 同时供右键与⋯；窗口在 `Root.view` 统一自绘菜单外观 |
| 命令面板 | `SearchField + List + ActiveDescendant` | accessibility.md / list.md | 列表要有 `Label`，搜索框指向选中行 |
| 建议菜单（slash/@） | `RoleListBox` + `RoleOption.Checked` + `PositionInSet` + 输入框 `ActiveDescendant` | accessibility.md「Suggestions」 | 不再用无 role 的按钮行 |
| 表单 | `Form/Field/Fieldset`；`Description/Error`；`LabelledBy`；`NumberInput`/`DecimalInput` | form.md / number-input.md | 数字用官方输入（记忆策略、运行上限、上下文窗口、生成参数已全部迁移，配“使用默认”开关）；错误用 `Error`；分组用 Fieldset（MCP STDIO 已采用）；**多字段对话框（如「新建队友」）用 `Form/Field` 给出可见标签，不能只靠 placeholder** |
| 对话框 | `Modal`/`AlertDialog`；`DialogFooter` + `Default` 焦点 + `Submitted()` 提交 | dialog.md / alert-dialog.md / text-input.md | Enter 提交必须覆盖每个输入对话框；**危险确认（删除任务/关闭队友）用 `ui.Modal` + 自建 footer：取消在前、无 `Default` 动作，避免 `AlertDialog` 末位按钮作为平台默认被 Enter 误触**；安全文案按钮（如取消）可放首位承接焦点 |
| 请求/提交动作 | `uikit.PageHeaderAction` / `uikit.SectionAction`（基于官方 Row/Button 的共享布局） | layout.md / form.md | 页面级即时动作（新建/刷新/检查更新）在页头同行右侧；小节工具动作（恢复默认/导出报告）在节标题同行右侧；显式保存页只保留宿主 footer 的一个主操作，不在内容底部再放动作行 |
| 查找 | `FindBar`（文内查找）；`SearchField`（过滤列表） | find-bar.md / search-field.md | 查找≠过滤，按场景选 |
| 差异 | `editor.DiffState` + `editor.DiffView`（`ParsePatch`/`SetPatch`，词级/折叠/审查） | diff-viewer.md / code-editor.md | 不手写逐行 diff 文本 |
| 终端/选择 | `ui.Select`（shell）；`ScrollState` 贴底跟随 | select.md / scroll.md | 选择器用官方 Select；日志跟随 end |
| 文本语义 | `Selectable`（错误/ID/命令可复制）；`RoleHeading/Level`；`RoleAlert`；`c.Announce` | text.md / accessibility.md | 错误与诊断文本可复制；页面/分组有标题层级；异步结果播报 |
| 主题/偏好 | `Theme`（从 `ui.LightTheme/DarkTheme` 派生）；`Theme.InactiveAccent`；`Preferences.TextScale/ReduceMotion` | styling.md / windows.md | 品牌主题显式设置 `InactiveAccent`；`design.Of` 接 `TextScale` |
| 图标 | `ui.Icon` + `ui.SVG`/`MustParseSVG`；一套图标网格 | icon.md | 不加 emoji/Unicode 图标；新增图标必须过 `internal/icons` 测试（网格/描边） |
| 加载/进度 | `Spinner` / `Progress` | spinner.md / progress.md | 耗时操作给进度或 spinner，不只文字 |
| 测试 | `ui.NewTester`（Click/Key/Advance/Image/AccessByRole/SetWindowActive/SetPreferences） | testing.md | 每个交互改动补帧测试；重复 Key 会 panic |
| 菜单/弹层顺序 | `OverlayShortcut`（最后构建先吃 Esc） | overlays.md | 不要自己维护 z-order 规则 |

## 1.1 官方组件采用与保留自绘（定期复核）

- 集合一律用 `ui.List`（transcript、轨迹、记忆、自动化、右栏各 pane、侧栏会话/搜索、
  设置归档/技能/记忆/模型目录/MCP/LSP/预设/诊断、右栏产物历史、composer 排队弹层、
  插件 MCP/模块/参数/工具、Home 最近、palette）；单文本面/页面容器用 `ui.Scroll`。
  （2026-11-09 第三轮审计后补齐，证据：`.cache/ui-audit/report.md`、`.cache/migrate-*/report.md`。）
- 控件优先官方：`Sidebar`、`TabsBase`、`Split`、`Toolbar`、`SearchField`、`TokenField`、
  `NumberInput`、`Select`、`Segmented`、`Switch`、`Collapsible`/`CollapsibleBase`、`Spinner`、
  `Breadcrumbs`、`Toggle`/`ToggleGroup`、`Modal`/`AlertDialog`/`DialogFooter`、
  `PopoverMenuButton`/`ContextMenu`、`Icon`、`Markdown`、`editor.View`/`editor.DiffView`、
  `ToastBase`/`ToastViewportBase`。忙碌态用 `Spinner`（有真实进度才用 `Progress`）。
- 保留自绘清单与理由见 [UI-COMPONENT-MIGRATION.md](UI-COMPONENT-MIGRATION.md) §3：
  仅限无官方对应（无边框图标按钮、富行建议菜单、composer 动作行布局、palette 按钮行、
  hover 动作行、回到最新浮层、测试 seam）或产品语义不适用（Rating/Meter 等）。
- 设置导航的「高级」分组跟随当前页：选中的页面属于高级组时展开，切回日常页时折叠
  （避免上次访问留下的长/短两种导航形态；`internal/uiroot/shell_ux_test.go` 钉住）。
- 新增 UI 时先查 §1 表与官方 docs；出现新的自绘控件必须在本文件与迁移文档记录理由。

## 1.2 团队可见性 / 任务板约定（本轮 M1）

- **运行态语义**：`contracts.TeamMember.Status` 只是 rig manager 的可用性（`active` /
  `interrupted`），**不是运行态**；真实运行态在 `RunState`（`running`/`idle`/`done`/
  `failed`/`interrupted`，agent 的 `team_state.go` 包装每个 spawn 的 `rig.Subagent` 跟踪）。
  UI 任何位置不得把 `active` 画成「运行中」；未知 RunState（旧后端）回退为「空闲」而不是「运行中」。
- **通知链**：团队变化（spawn/send/interrupt/close/finish）由 agent 的
  `notifyTeamChange()` 走既有 `ChatService.Subscribe` 通知所有会话；右栏订阅回调在
  `Post` 里同时失效 **log 与 tasks 缓存**（`tasksLoaded=false`）——任务板打开时 agent 写入
  `team_task_*` 不再显示旧数据（T-03 回归由 `tasks-auto-refresh` drive 步骤与帧测试覆盖）。
- **契约增量（nil-safe）**：`TeamMember` 的 RunState/Error/Model/Started/Finished/
  UsageTokens/Iterations、`TeamActivity`（有界：总 ≤200、每成员 ≤30）+ 可选
  `TeamActivityLister`；`BoardTask.BlockedBy/WriteScopes`；可选 `TaskAssigner`（无实现时
  owner 只读，不画死菜单）。
- **稳定命令 ID**：`rightpanel.team.open` / `rightpanel.tasks.open`（palette/会话页头团队
  chip 共用；命令在检查器关闭时先打开检查器再切 pane，一条调用链完成）。命令 Hint 必须
  包含「团队」/「任务」（en: team/tasks）关键词，palette 搜索才能命中。
- **原生选型**：团队 pane = 段头（主代理/队友/同步子代理·只读）+ `ui.List`（无卡片墙）；
  任务板 = `ui.List` 的 `Header` 状态分组+计数、行内 owner 用 `PopoverMenuButton`（→
  `TaskAssigner`）、`Selected`+`Submitted` 展开到列表下方 `design.Panel()`；会员详情 =
  `design.Panel()` + `ui.Spinner`/`Badge`/`Selectable` 文本 + 复制产出用 `c.WriteClipboard`；
  新建队友 = `ui.Form/Field` + `ui.Modal`（无默认动作）；完成/失败通知 = `Notifier` 的
  可选 `ActionNotifier`（「查看」跳 pane 选中该成员，按 Finished 时间戳去重，首次观察不弹历史；
  预览必须清洗：去 code fence 及其内容、去 `#`/`>`/列表符号与行内反引号、空白折叠、≤80 rune 截断）。
- **证据**：帧测试 `TestAgentsRunStateBadgesAreTruthful`、`TestAgentsCompletionToastOnceAndViewAction`、
  `TestAgentsEmptyStateExplainsTeamAndOpensSpawnForm`、`TestAgentsCloseConfirmationHasNoDefaultAction`、
  `TestTasksChatNotifyRefreshesOpenPane`、`TestTasksOwnerDisplayAndAssign`、`TestTasksDependencyLine`、
  `TestTasksEnterExpandsDetail`、`TestTasksDeleteConfirmationHasNoDefault`、
  `TestOpenPaneCommandsStableIDs`；ui-drive `team-*`/`tasks-*`/`palette-team` 步骤；快照
  `p2-team-chip/p2-team-detail/p2-team-spawn/p2-tasks-expanded/p2-palette-team`。

## 2. 写 UI 的固定流程

1. **先查表**：本条功能在官方有没有标准件？有就先用官方组件/API，禁止先手搓。
2. **读源码确认签名**：`docs/ui/<file>.md` 与 `ui/*.go` 可能不同步，以 v0.8.3 源码为准。
3. **实现**：在 view 回调内 build；状态归模块实例；控件身份用 `c.Key`；跨线程走
   `UIRuntime.Post`；不要在 build 里做 I/O（缓存 + 显式刷新，见 memory/files 页）。
4. **可访问性**：每个无文本控件 `Label`；页面标题 heading；列表/建议菜单补 role 与
   `ActiveDescendant`；错误行 `RoleAlert`；异步结果 `c.Announce`。
5. **验证**：
   - `go build ./... && go vet ./... && go test ./... -count=1`
   - 新交互：`ui.NewTester` 帧测试（键盘 ↑↓/Enter/Tab/Esc、焦点归还、a11y role）
   - 新界面：`go run ./internal/devtools/ui-snapshot <dir>` 生成 1280/1024/暗色快照并看图
   - 涉及 v0.8 行为（`InactiveAccent`、菜单、Markdown）时，抓 active/inactive 双态
6. **收口**：删除被替换的手写逻辑与死代码；若确实要偏离官方，在代码注释里写清理由
   （例：composer 动作行不用 `ui.Toolbar` 是因为溢出菜单会吞掉发送按钮）。

## 3. 常用验证命令

```bash
export PATH=/usr/local/go/bin:$PATH
# /tmp is read-only in this environment; the temp dir must stay OUTSIDE the git
# worktree, or tests that create throwaway workspaces see them as untracked
# files of this repository (e.g. workspace Changes tests).
export GOCACHE=/data/mygo/.cache/go-build GOTMPDIR=/data/zeokeel-tmp TMPDIR=/data/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off

go build ./...
go vet ./...
go test ./... -count=1
go run ./internal/devtools/ui-snapshot .cache/ui-conformance
```

## 4. 维护

- 新增官方组件/API 时追加一行到 §1，并注明采用状态（已用/待用/不适用）。
- 每轮 UI 审计的 finding 修复后，把「本项目规则」列更新为现状，避免清单与代码漂移。
- 本文件是 `design-guide` 技能的落地补充：设计原则见技能，具体 API 见本表。
- 团队/任务板的运行态、通知链、稳定命令 ID 与危险确认约定见 §1.2；改动这些语义时同步更新该节。
