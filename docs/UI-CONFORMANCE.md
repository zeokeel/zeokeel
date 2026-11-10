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

## 1.2 团队 / 子代理 / 后台任务约定（2026-11-10 重构）

- **团队与子代理不再有右栏 pane**：会话头部三个入口（`智能体团队 / 子智能体 N / N 个后台任务`）打开官方 `PopoverBase` 原生浮层；团队浮层 = 成员卡 + 共享任务卡（只读），子代理浮层打开主栏子会话（面包屑返回），后台任务浮层可停止运行中的任务。
- **跨模块命令 ID**：`uiroot.install-update`（更新安装，host 注册）、`rightpanel.files.open`（Ctrl/Cmd+Alt+P）、`rightpanel.terminal.open`（Ctrl/Cmd+`）；旧的 `rightpanel.team.open`/`rightpanel.tasks.open`/`rightpanel.pane.*` 已删除。
- **数据来源**：`contracts.TeamService`/`TaskBoard`（只读展示），子代理与队友用 `contracts.ChildAgentService` 统一读模型（`ChildAgents`/`SendChildAgent`）；后台任务用 `contracts.JobService`。
- **测试/Snapshot**：`internal/ui/session/agentsurface_test.go`、`internal/modules/agent/childagents_test.go`、`internal/modules/toolkit/jobs_service_test.go`；ui-snapshot 场景 `p2-team-*`/`p2-jobs*`/`p2-child-*`。改动这些语义时同步本文件与 AGENT-SURFACES S9。

## 1.3 列表选择/激活、on-accent 前景、框架 locale（2026-11-10 修复轮）

- **选择 ≠ 激活**：`Selected`/`Changed` 只表示"选择"（点击、Up/Down/Home/End）；打开/进入/提交走
  `Submitted()`（Enter/双击）或行内元素 `RowDoubleClicked()`。方向键不得触发导航（右栏文件树目录、
  设置「归档」列表按此修；测试见 `internal/ui/rightpanel/files_test.go`、
  `internal/ui/settings/archive_test.go`）。
- **accent 选中行前景**：`ui.List` 以 accent 填充选中行。行内文字/图标用 `t.AccentText`；徽章用
  `uikit.OnAccentBadge`（AccentText 填充 + accent 文字，status label 语义保留），不得保留 resting
  前景色（暗色下实测 ≈1.2:1）。像素断言见 `internal/ui/sidebar/badge_contrast_test.go`、
  `internal/uikit/onaccent_test.go` 与 `internal/ui/rightpanel/panels_fix_test.go`。
- **框架 locale 与产品 locale 分离**：mygo 自己的控件/菜单/无障碍文案由 `mygo.App.SetLocale` 决定。
  host blank-import `github.com/egoist/mygo/locales/zh-Hans`，启动时按 `ui.language` 调
  `App.SetLocale`，运行期经 `uiroot.Config.OnLanguageChange` 同步；模块内不要直接调 `App.SetLocale`。
  devtools（snapshot/drive）同样 link 该 catalog 并用 `Tester.SetLocale` 镜像，保证帧与产品一致。
- **全局浮层顺序**：命令面板构建在 overlays（设置面）之后，叠在设置面之上；Esc 由最上层先消费
  （官方 `OverlayShortcut` 语义）。设置面内 Cmd+K 必须可用，不能静默失效。

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
