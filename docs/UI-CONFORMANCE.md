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
| 长列表/日志/会话 | `ui.List` + `ListState`（Key/Selected/FollowEnd/HoldPlace/Label） | list.md | 集合行不再用 `Scroll`+全量循环；测试用 `Rows`/`ItemKey` |
| 表格 | `ui.Table` / `TableColumn` / `CellSelect` | table.md | 多列可排序数据用 Table，不用 Row 手排 |
| 树/大纲 | `ui.Outline[T]` / `ui.Tree` / `OutlineState` | outline.md / tree.md | 工作区/文件树用 Outline |
| 菜单 | `PopoverMenuButton` / `PopoverMenuButtonOptions{At,Self,Open}` / `MenuItem.Danger/Radio/ID/Subtitle/Trailing/Content/MaxWidth` / `MenuBase` | menu-button.md / context-menu.md | 行尾⋯菜单右对齐；删除项 `Danger()`；单选 `Radio()`；稳定选择 `ID()`；自绘菜单迁移 `MenuBase` |
| 右键菜单 | `Element.ContextMenu(menu)` / `ContextMenuBase` / `Context.SetMenuPresentation(ui.PopoverMenus)` | context-menu.md | 同一 menu 同时供右键与⋯；窗口在 `Root.view` 统一自绘菜单外观 |
| 命令面板 | `SearchField + List + ActiveDescendant` | accessibility.md / list.md | 列表要有 `Label`，搜索框指向选中行 |
| 建议菜单（slash/@） | `RoleListBox` + `RoleOption.Checked` + `PositionInSet` + 输入框 `ActiveDescendant` | accessibility.md「Suggestions」 | 不再用无 role 的按钮行 |
| 表单 | `Form/Field/Fieldset`；`Description/Error`；`LabelledBy`；`NumberInput`/`DecimalInput` | form.md / number-input.md | 数字用官方输入（记忆策略、运行上限、上下文窗口、生成参数已全部迁移，配“使用默认”开关）；错误用 `Error`；分组用 Fieldset（MCP STDIO 已采用） |
| 对话框 | `Modal`/`AlertDialog`；`DialogFooter` + `Default` 焦点 + `Submitted()` 提交 | dialog.md / alert-dialog.md / text-input.md | Enter 提交必须覆盖每个输入对话框；危险确认用 AlertDialog/`DangerButton` |
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
  设置归档/技能、插件 MCP/模块/参数/工具、Home 最近、palette）；单文本面/页面容器用
  `ui.Scroll`。
- 控件优先官方：`Sidebar`、`TabsBase`、`Split`、`Toolbar`、`SearchField`、`TokenField`、
  `NumberInput`、`Select`、`Segmented`、`Switch`、`Collapsible`、`Modal`/`AlertDialog`/
  `DialogFooter`、`PopoverMenuButton`/`ContextMenu`、`Icon`、`Markdown`、`editor.View`/
  `editor.DiffView`、`ToastBase`/`ToastViewportBase`。
- 保留自绘清单与理由见 [UI-COMPONENT-MIGRATION.md](UI-COMPONENT-MIGRATION.md) §3：
  仅限无官方对应（无边框图标按钮、富行建议菜单、composer 动作行布局、palette 按钮行、
  hover 动作行、回到最新浮层、测试 seam）或产品语义不适用（Rating/Meter 等）。
- 设置导航的「高级」分组跟随当前页：选中的页面属于高级组时展开，切回日常页时折叠
  （避免上次访问留下的长/短两种导航形态；`internal/uiroot/shell_ux_test.go` 钉住）。
- 新增 UI 时先查 §1 表与官方 docs；出现新的自绘控件必须在本文件与迁移文档记录理由。

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
