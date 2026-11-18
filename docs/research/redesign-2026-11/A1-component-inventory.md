# A1 组件盘点：mygo v0.9.2 官方件 vs 自绘（全仓、逐组件）

> 任务：mygo 由 v0.9.0 升到 **v0.9.2** 后，回答两个问题——(1) v0.9.2 到底提供了哪些官方组件/API；
> (2) 本仓哪些 UI 是「官方已有却自绘/手搓」，逐处给位置、现状、官方替代、迁移代价、风险、验收。
>
> 方法：以源码为准（`go list -m -f '{{.Dir}}' github.com/egoist/mygo` → `@v0.9.2`），
> AST 比对 v0.9.0→v0.9.2 的导出面，全仓 grep + 逐文件扫描，所有推荐片段已用探针包编译验证。
> `docs/ui/*.md` 88 篇作目录；**文档与源码冲突一律以源码为准**（本文记录了 5 处文档滞后）。
>
> 只读审计：**未修改任何产品代码**。交付物仅本文件与 `A1-matrix.tsv`。

---

## 0. 结论摘要（按影响排序）

1. **本仓"手搓控件"问题已经基本清零，真正的缺口是"官方能力未采用"。** 全仓 0 处自绘绘制（`ui.Painter`/`Canvas`/`DrawOver`）、0 处裸色（`ui.RGBA`/hex）、0 处 emoji/Unicode/手写 SVG 图标；**10 处 `ui.ButtonBase` 调用点**（另有 2 处注释提及）全部是登记过的富行/徽章/图标按钮站点。审计假设的"大面积的 ButtonBase 手绘、手写 diff、手写高亮、手绘 scrollbar/spinner/tab/toast"在本仓**不存在**（证据见 §1.3）。
2. **P0：全仓零动效。** `Element.Transition`、`AnimateColor`、`AnimatePose`、`ContentTransition`、`Match/Fly`、`ListState.MoveAnimation`、`FollowAnimation` 的使用数**全部为 0**；39 处手动读取 `Hovered/Pressed/Focused` 的地方全是**瞬时跳变**。官方 `docs/ui/transitions.md` 给出的正是"hover 渐变色"的 4 行成品配方。**最新快照（2026-11-18 00:18）：`internal/design/motion.go` 已由并行的设计系统改动落地**——`MotionFast/Base/Enter/List` 四个时长 token + `TransitionColors/TransitionHeight/TransitionOverlay/TransitionSize/ListFollowAnimation/ListMoveAnimation/ReduceMotion` 七个 helper，语义与本文建议**完全一致**；但全仓**调用点仍为 0**。结论从"缺积木"变成"**积木和 token 都摆好了，就是没接线**"——这是本轮最便宜的高价值改动。
3. **P0：流式与列表位移无动画。** transcript 已有官方 `ListState{Key,Label,FollowEnd,HoldPlace}`，但缺 `FollowAnimation`（流式新内容从下方滑入）与 `MoveAnimation`（v0.9.1 新增 `ui/list_move.go`：折叠/重排时行不跳）。这两条是 ChatGPT/Claude 桌面端观感的主要来源。
4. **P1：长路径/长标题/长模型名靠"数据截断"而非"绘制期截断"。** 8 处 UI 标签级 `runes[:n]+"…"`；`TruncateStart`/`TruncateMiddle`（v0.9.1 新增）全仓 **0 使用**。数据截断会让 tooltip/无障碍/复制都拿不到全文，且宽度变化时不自适应。
5. **P1：会话 Chat/轨迹/记忆 标签条是"官方 TabsBase + 手绘 accent 下划线 `ui.Box`"**（`session/view.go:896-916`）。官方 `ui.Tabs`（`ui/tabs.go:15-52`）内置**同款**下划线（inset `Space(1.5)`、高 `Space(0.5)`、accent）并自带 hover 文字提亮与 `FocusVisible` 焦点环——整段可替换，删 20 行手绘。
6. **P1：3 处 `if index > 0 { ui.Divider(c) }` 循环手绘分隔**（`uikit/propertysheet.go:107`、`settings/settings.go:227`、`:601`）→ 官方 `Element.Dividers(1, t.Border)` 一行声明。
7. **P1：10 处 ButtonBase 调用点里状态机被手写了 5 遍**（IconButton ghost、RunStatus 徽章、rail 行、palette 行、slash/@ 行）。其中 `sidebar.go:1609/2205` 两处行尾 `⋯` 应直接收敛到 `uikit.IconButton`（Native 变体）。
8. **P2：43 项官方能力/方法全仓零使用**（§1.2-C 清单）。除动效外，`ScrollbarStyle`（滚动条外观）、`Sticky`（小节吸顶）、`EndReached/NearEnd`（长列表分页）、`PopoverMenuOptions{Panel,Item}`（逐菜单换肤，v0.9.2 新增）四项对重设计有直接价值。
9. **P2：三个"看起来能用、实际不能调"的 API**：`Element.Access` / `Element.AccessChildren`（回调参数是 `mygo/internal/platform` 类型）/ `Context.TextSystem()`（返回 `mygo/internal/text` 类型）。实测编译报 `use of internal package ... not allowed`。**不要写进设计契约**（详见 F-11 与 §3.2）。
10. **P2：文档漂移 3 处**：`UI-COMPONENT-MIGRATION.md` 声称"工具 allowlist 已用官方 `ui.TokenField`"，实测全仓 `ui.TokenField` **0 处**；`uikit.KeyValueRows` 自己标了 `Deprecated`，却仍有 2 个调用方；`DESIGN.md §1.2-F` 写 `AlertDialog` 用于非危险确认，实测 `ui.AlertDialog` **0 处真实调用**（产品统一走 `uikit.ConfirmDanger`）。

---

## 1. 现状（证据）

### 1.1 环境与版本基线

```bash
$ go list -m -f '{{.Dir}} {{.Version}}' github.com/egoist/mygo
/data/mygo/.cache/gopath/pkg/mod/github.com/egoist/mygo@v0.9.2 v0.9.2
$ go build ./...            # REPO BUILD OK（审计时点，未改任何代码）
```

- 官方源码根：`/data/mygo/.cache/gopath/pkg/mod/github.com/egoist/mygo@v0.9.2`
- `ui` 包：`.go` 共 392 个（其中 `_test.go` 233、源码 159，合计 136,230 行）；导出面 = **控件构造器 111 个**（`func X(c *Context, ...) Element`）、`Element` 方法 **336 个**、包级导出函数 **184 个**、导出类型 **153 个**。
- 子包：`ui/markdown`、`ui/editor`、`ui/richtext`、`ui/chart`、`ui/video`、`ui/shader`。
- 官方目录：`docs/ui/*.md` **88 篇**（清单见 §5.2），另有 `docs/ui/README.md`、`docs/ui/migration.md`。
- 本项目 UI 规模（非测试）：`internal/ui` 13 模块（14 目录，`uimodtest` 为测试支撑）**75 文件 / 40,536 行**（含测试共 360 个 .go）；`internal/uiroot` **15 文件 / 4,965 行**；`internal/uikit` **29 文件 / 6,594 行**。**合计 119 个非测试文件 / 52,095 行**，逐文件结论见 §3.3。

### 1.2 官方件采用率（量化）

**A. 构造器（业务面 106 个，已用 52 个 / 未用 54 个）**

| 状态 | 组件 |
|---|---|
| **已用（52）** | Badge Box Breadcrumbs Button ButtonBase Checkbox Collapsible CollapsibleBase Column Divider Field Fieldset FindBar Form Grid GridView Icon Image List Modal NumberInput Outline Overlay Popover PopoverBase PopoverMenuButton PrimaryButton RichText Row Scroll ScrollBoth ScrollHorizontal SearchField SegmentedBase Select Sidebar SidebarItem SidebarSection Spacer Spinner Split Switch Table Tabs TabsBase Text TextArea TextInput Themed ToastBase ToastViewportBase Toolbar |
| **未用（54）** | Accordion Accordion(Base/Item) AlertDialog Autocomplete Avatar BackButton Calendar CheckboxBase CheckboxGroup CheckboxState ColorPicker ColorWell Combobox ContextMenuBase DateInput DecimalInput DialogBase EditableText ForwardButton Link MenuBase MenuButton Meter NativeView PrimarySplitButton Progress Radio RadioGroup RangeSlider Rating Segmented Slider SliderBase SplitPercent SplitVertical SplitVerticalPercent Stepper StepSlider SwitchBase TextAreaBase Textf TextInputBuffer TimeInput Toggle ToggleBase ToggleGroup TokenField TooltipBase Tree TreeBase TreeItem Video WebView |

调用量前列（非测试）：`Text` 514、`Row` 264、`Column` 260、`Button` 110、`Field` 73、`Icon` 69、`TextInput` 59、`Spacer` 52、`Divider` 35、`Scroll` 32、`List` 30、`Modal` 29、`Spinner` 26、`Badge` 22、`Box` 22、`Collapsible` 20、`PrimaryButton` 14、`PopoverMenuButton` 12、`NumberInput` 11、`Select` 9、`Switch` 8、`SearchField` 8、`Table` 5、`Overlay` 3、`TabsBase` 3、`Split` 2、`GridView` 2、`Outline` 2、`Sidebar` 2。

**B. `Element` fluent 方法：336 个中用了 76 个（226 未用）**

高频正确用法（说明"官方位"已吃透）：`Label` 116、`Ellipsis` 102、`Tooltip` 86、`NoWrap` 80、`MaxLines` 207、`Description` 51、`Selectable` 17、`Bind` 5、`Focus` 14、`ScrollIntoView` 8、`Announce` 6、`TrackScroll` 6、`Header` 4、`OverlayShortcut` 3、`ContextMenu` 3、`ActiveDescendant` 2、`KeyShortcuts` 2。

**C. 43 项能力/方法零使用**（`grep` 全仓非测试 = 0；`AnimatePose(Pose)` 记为 2 个 API）

> **重要更新（2026-11-18 00:18 快照）**：`internal/design/motion.go` 已落地 4 个时长 token
> （`MotionFast` 80ms / `MotionBase` 120ms / `MotionEnter` 160ms / `MotionList` 200ms）+ 7 个 helper
> （`TransitionColors`/`TransitionHeight`/`TransitionOverlay`/`TransitionSize`/`ListFollowAnimation`/
> `ListMoveAnimation`/`ReduceMotion`）。**这 7 个 helper 的调用点数仍为 0**——本文所有动效建议都改用它们，
> 实现者不需要自己发明时长。

```
Transition  AnimateColor  Animate  AnimateTo  AnimatePose(Pose)  Spring  Timeline  Keyframes  Loop
ContentTransition  Match  Morph  Dividers  TruncateStart  TruncateMiddle  Sticky  ScrollSnap  SnapAlign
ScrollbarStyle  PopoverMenuOptions  WithDark  Unsupported  AccessChildren  SelectAll  Touches  NewRecorder
TextSystem  FocusGroup  TabStop  InView  Cache  DragDrop(Drag/Drop)  FontFeatures  LetterSpacing
TextShadow  InsetShadow  Invalid  ErrorMessage  DescribedBy  SingleLine  Underline  Group  DimDisabled
```

### 1.3 自绘点全量清单（本仓的"手搓"全集：10 个 `ui.ButtonBase` 调用点 + 4 类结构性手绘 + 8 处数据截断）

| # | 位置 | 类别 | 官方替代 | 处置 |
|---|---|---|---|---|
| 1 | `uikit/iconbutton.go:133` | 图标按钮基座（rest/hover/pressed/selected/禁用） | 官方无图标按钮 | 保留（登记） |
| 2 | `uikit/runstatus.go:357` | 队列计数徽章（全状态手绘，H28） | `ui.Badge` 无点击/键盘 | 保留（登记 `uikit.run-status-queue-badge`） |
| 3 | `uiroot/frame.go:426` | rail 会话富行（标题+状态点+整行点击） | List 行是 listitem | 保留（登记 `uiroot.rail-session-rows`） |
| 4 | `uiroot/frame.go:1158` | 命令面板行（单击即执行 + RoleButton） | 同上 | 保留（登记 `command-palette.rows`） |
| 5 | `session/view.go:4745` | slash 命令富行 | `Autocomplete` 仅纯文本 | 保留（登记） |
| 6 | `session/view.go:5222` | `@` 提及富行 | 同上 | 保留（登记） |
| 7 | `sidebar/sidebar.go:1442` | 工作区树行（前缀槽+行尾动作） | Outline 行无自定义内容 | 保留（登记） |
| 8 | `sidebar/sidebar.go:1609` | 会话行 `⋯` 触发器 | **`uikit.IconButton`（Native）** | **迁移** |
| 9 | `sidebar/sidebar.go:2205` | 工作区组 `⋯` 触发器 | 同上 | **迁移** |
| 10 | `sidebar/sidebar.go:2380` | 设置行（整行点击+图标+chevron） | 官方 Sidebar/`List` | 可迁移（模型改动大） |
| 11 | `design/conform/conform.go:16`（**注释**，非调用点） | 合规工具把 `ButtonBase`/`DialogBase` 当自绘指标点名 | — | 保留（工具自身，非产品 UI） |
| 12 | `session/view.go:913` | tabs accent 下划线 `ui.Box(0.5)` | **`ui.Tabs` 内置同款** | **迁移** |
| 13 | `uikit/propertysheet.go:107`、`settings/settings.go:227`、`:601` | 循环内 `if index>0 { ui.Divider }` | **`Element.Dividers(1, t.Border)`** | **迁移** |
| 14 | `sidebar/sidebar.go:1555/1615/2212` | hover 才显形的行内动作（`Opacity(0)+IgnoreInput`） | `ElementTransition`（淡入） | **迁移** |
| 15 | `uikit/uikit.go:109` | 已弃用 `KeyValueRows`，2 个调用方（`rightpanel/view.go:399`、`plugins/mcp_config.go:100`） | `uikit.PropertySheet` | **迁移** |
| 16 | 8 处标签级 `runes[:n]+"…"` | 数据层截断（清单见 F-03） | `MaxLines+Ellipsis(+TruncateStart/Middle)` | **迁移** |
| 17 | 5 处 payload 级截断（错误/预览文本） | 有界载荷 | — | 保留（理由见 F-03） |

**审计任务里点名、但本仓不存在的自绘项**（每项均给出 0 命中证据）：

| 被怀疑的自绘 | 实测 |
|---|---|
| 手写 diff | 0；全部走官方 `editor.DiffState` + `editor.DiffView` + `ParsePatch`（`rightpanel/changes.go:174-219`、`uikit/diffpreview.go:313`） |
| 手写代码高亮 | 0；`markdown.CodeBlockView` 保留官方高亮，仅换肤（`uikit/markdown_code.go`）；文件预览用 `editor.View`（`rightpanel/view.go:489`） |
| 自绘 scrollbar | 0；无 `Scrollbar` 相关手绘，滚动条由 `ui.Scroll`/`List` 原生绘制（外观只由 `brand` 的 `Theme.Scrollbar` 定） |
| 自绘 spinner / progress | spinner 全走 `ui.Spinner`（26 处）；`ui.Progress` 0 处且**明确不伪造进度**（`settings/listhelper.go:45`） |
| 自绘 toast | 0；`ui.ToastBase` + `ui.ToastViewportBase` 换肤（`toasts/toasts.go:354,439`） |
| 自绘 tooltip | 0；`.Tooltip(` 86 处 |
| 自绘 badge/chip | 0；`ui.Badge` 22 处 + `uikit.StatusBadge`（`Badge` + 语义 tint） |
| 自绘 menu/dropdown | 0；`PopoverMenuButton` 12、`Element.ContextMenu` 3、窗口级 `c.SetMenuPresentation(ui.PopoverMenus)`（`uiroot/frame.go:53`） |
| 手写输入框 | 0；`TextInput` 59 + `TextArea` 10 + `SearchField` 8 + `NumberInput` 11 |
| `Row`+循环代替 `List` | 仅 12 处 `Row` 紧跟 `range`；人工核对后 9 处是 2–8 项固定集合（对话框动作条、属性表小段、统计行），3 处是**静态参考表**（`settings/settings.go:226` 快捷键参考 ~20 行、`session/commands_help.go:121` 快捷键表、`session/view.go:5044/5069` 提及菜单分组），都在页面滚动内且不需要虚拟化。清单：`automation/view.go:532`、`uikit/propertysheet.go:110`、`uikit/runstatus.go:425`、`uikit/uikit.go:113`、`uikit/uikit.go:199`、`plugins/skills_section.go:423`、`session/commands_help.go:123`、`session/view.go:5044`、`session/view.go:5069`、`settings/import.go:249`、`settings/settings.go:230`、`settings/settings.go:604`。**唯一可讨论项**：`settings/settings.go:226` 的快捷键参考表（约 20 行、会随命令增加）可改 `ui.List`，但收益低（无流式/无重排），列为 P3 |
| emoji/Unicode/内联 SVG 图标 | 0；箭头 `→`/`↑↓` 仅出现在**文案字符串**里（如 `session/view.go:3459`「↑↓ 选择 · Enter 执行」），不是图标 |
| 裸色 `ui.RGBA`/hex（UI 层） | 0（`internal/brand` 之外无命中） |

### 1.4 已达标项（防重复迁移，勿再改）

1. 集合：`ui.List` 30 处（transcript/轨迹/记忆/自动化/归档/右栏各 pane/侧栏会话与搜索/设置各表/palette/命令帮助）、`ui.Table` 5、`ui.Outline` 2、`ui.GridView` 2。
2. 浮层：`Modal` 29、`PopoverBase` 7、`Overlay` 3、`ui.OverlayShortcut` 3（Esc 栈语义交给框架）。
3. 表单：`Field` 73 + `Description` 51；`NumberInput` 11（无自由文本数字）；`Switch` 8；`SegmentedBase` 1（换肤有对比度理由，写在 `uikit/segmented.go:1-18`）。
4. 文本：`Ellipsis` 102 + `MaxLines` 207 + `NoWrap` 80（绘制期截断已是主流写法，只有 8 处漏网）。
5. 无障碍：`Label` 116、`Announce` 6、`Role` 9、`ActiveDescendant` 2、`Controls` 1；错误行走 `RoleAlert`（`uikit/uikit.go:152`）。
6. 主题：`design.Of(c)` + token；`internal/brand` 是唯一 hex 源；`Themed` 2 处局部主题。
7. 危险确认：`uikit.ConfirmDanger`（`Modal` + `DialogFooter`，取消为 Default），`ui.AlertDialog` 0 处。

### 1.5 本次审计的边界

- 未跑全量 `go test ./...`（30+ 分钟且并发成员在改代码，结果会被污染）；只跑 `go build ./...`（OK）与探针 `go build`/`go vet`（OK）。
- 未生成新 PNG 快照（BRIEF 说基线已有 `.cache/ui-conformance/`）；本文视觉结论不依赖新截图，均以源码行 + 官方源码行互证。
- `internal/ui/*/*.go` 共 360 个，其中 `_test.go` **285 个（79%）**；逐文件三态结论只覆盖非测试文件 119 个（`internal/ui` 75 + `uiroot` 15 + `uikit` 29）。
- **并发变更**：审计期间其他成员已落地 `internal/design/motion.go`（动效 token + helper，0 调用点）与
  `internal/{brand,design}` 的配色/token 改动。本文在 2026-11-18 00:18 快照上并入 motion.go；此后若
  `DESIGN.md §7.1`/registry 的自绘条目数或 token 名再变，以代码为准重跑 `.cache/inventory/metrics.sh` 即可。
- 重建验证（快照时点）：`go build ./...` = 0（`BUILD EXIT:0`），含他人改动。

---

## 2. 发现

> 格式：**编号 | 严重度 | 位置 | 问题 | 为什么是问题 | 具体改法**。改法里的片段都已在
> `/data/mygo/.cache/inventory/probe/probe.go` 通过 `go build` + `go vet`（mygo v0.9.2）。

### F-01 | **P0** | 全仓（39 处状态读点） | 零过渡：hover/pressed/焦点全是瞬时切色

- **位置**：`uikit/iconbutton.go:158-176`、`uikit/runstatus.go:363-372`、`uiroot/frame.go:432-437`、`uiroot/frame.go:1173-1176`、`sidebar/sidebar.go:1448-1454`、`toasts/toasts.go:485-506`、`session/goal.go:372`、`session/todo.go:86`、`uikit/toolrow.go:304`、`uikit/reasoning.go:102`、`session/view.go:1553/4483`。
- **问题**：`Element.Transition` 全仓 0 次。所有 `Background(t.BgHover)` 都是在 `if Hovered()` 里直接换值。
- **为什么**：这是"死抠每次悬浮"的第一性体验——企业级工具（Linear/VS Code/Raycast）的 hover 都是 100–150ms 渐变；硬切在 60Hz 下就是一次跳变，用户会说"感觉廉价"，但说不出原因。
- **改法**（用已就绪的 token 层，不要写裸时长——AGENTS §3.3）：
  ```go
  button = button.Transition(design.TransitionColors(c))   // internal/design/motion.go:50
  ```
  该 helper 内含 `Colors: true`、`Duration: MotionFast(80ms)`、`Ease: EaseOut`，并在 `Preferences.ReduceMotion`
  时给出负时长（等于"立即显示"）。裸写法（探针已验证可编译）仅作为对照：
  ```go
  button = button.Transition(ui.ElementTransition{Duration: 120 * time.Millisecond, Colors: true})
  ```
  落点建议：`uikit.IconButton`（一处改，全仓图标按钮受益）、`uikit.RunStatusOptions` 徽章、`uikit.ToolRow` 触发器、`uikit.Reasoning` 触发器、`frame.go` 两处行、`toasts` 两个按钮。
- **风险**：`Transition` 要求元素跨帧**身份稳定**（`c.Key` 或结构位置不变）；本项目这些站点都已用 `c.Key`，风险低。`Preferences.ReduceMotion` 由框架自动降级。
- **验收**：帧测试 `Tester.Advance(60ms)` 两帧截图，hover 中间帧背景色应介于两端（可断言 `Image()` 像素）；`go test ./internal/uikit -run IconButton`。

### F-02 | **P0** | `uikit/session.go:118-123` | 流式追加与行位移都没有动画

- **位置**：`uikit/session.go`（transcript 的 `ListState` 装配）、`session/session.go`。
- **问题**：已有 `FollowEnd`/`HoldPlace`（正确），但没有 `FollowAnimation`（v0.9.2）与 `MoveAnimation`（**v0.9.1 新增**，实现见官方 `ui/list_move.go:1-40`）。
- **为什么**：`FollowEnd` 只保证"贴底"，新内容仍是一次性顶上来；`MoveAnimation` 决定"行被折叠/工具卡展开/重排时是滑过去还是跳过去"。这两条是 ChatGPT/Claude 桌面端"内容有生命感"的全部来源。
- **改法**（用已就绪的 token 层）：
  ```go
  state.FollowAnimation = design.ListFollowAnimation(c)   // internal/design/motion.go:87
  state.MoveAnimation = design.ListMoveAnimation(c)       // internal/design/motion.go:95（ReduceMotion 时为 nil）
  ```
  裸写法（探针已验证）：`&ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.EaseOut}`。
- **风险**：`MoveAnimation` 需要 `Key`（已有，message.ID）；流式高频重建时官方按 `ReduceMotion`/`instant()` 自动降级。**实测风险**：`ui-snapshot` 与帧测试的时间是"停住的"，动画会让既有像素断言不稳定 → 需要在测试里 `SetPreferences(ui.Preferences{ReduceMotion: true})` 或 `Advance`。
- **验收**：新增帧测试「流式追加两帧，第二帧滚动偏移介于两端」；既有 `scroll_follow_test` 全部保持绿。

### F-03 | **P1** | 8 处 | 标签级"数据截断"应为"绘制期截断"

- **位置**（标签级，**应迁移**）：
  `session/view.go:3979`（`truncateRunes` → 模型 chip）、`home/composer.go:355`（`homeChipLabel`）、`settings/model_check.go:139`（`truncateModelName`）、`uikit/runstatus.go:388`（队列摘要）、`uikit/approval.go:849`（审批摘要）、`uikit/toolrow.go:456`（工具摘要）、`plugins/view.go:177`（状态徽章 12 runes!）、`automation/view.go:442`（运行摘要）。
- **问题**：在数据侧用 `runes[:n]+"…"` 砍字符串再交给 `ui.Text`。
- **为什么**：① tooltip/a11y/复制文本都被砍（已有 tooltip 的站点在 tooltip 里放全文，等于双份真相）；② 宽度变化不自适应（窄窗更该砍、宽窗白砍）；③ `plugins/view.go:177` 把 12 runes 用在中文徽章上 ≈ 3 个汉字，语义直接失效；④ 模型 id 的差异在**尾部**（`:free`、`@2026-11`），右截断恰好砍掉唯一区分位。
- **改法**（按内容决定保头/保中/保尾）：
  ```go
  // 路径、模型 id、会话标题：保尾
  design.Apply(ui.Text(c, name), t.MonoText(design.TypeXXS)).
      MaxLines(1).Ellipsis("…").TruncateStart()
  // 文件路径：保头保尾
  ui.Text(c, path).MaxLines(1).TruncateMiddle()
  // 普通单行标签：只截尾
  ui.Text(c, label).MaxLines(1).Ellipsis("…")
  ```
- **保留项（payload 级，不要动）**：`uikit/previewprose.go:55`、`uikit/toolpreview.go:415`、`plugins/connectors_auth.go:143`、`plugins/connectors_edit.go:876`、`rightpanel/artifacts_download.go:42` —— 这些是"进入 UI 之前把有界载荷收窄"（错误栈、散文预览、文件名 slug），语义正确。
- **风险**：`TruncateStart/Middle` 需要 `SingleLine()` 或 `MaxLines(n)` 才生效；官方 `docs/ui/text.md` **未记录**这两个方法（文档滞后），以源码 `ui/element.go:2006-2021` 为准。
- **验收**：`Tester.Find("…")` 找不到（说明未被数据截断）；`AccessByRole(RoleTextField,"文件内容").Value` 或 `Texts()` 断言全文仍在；窄窗快照（1024）下不溢出。

### F-04 | **P1** | `session/view.go:879-916` | 会话标签条：官方 TabsBase + 手绘下划线

- **问题**：`ui.TabsBase` 提供选择/键盘，但下划线是手绘 `ui.Box(c).Height(th.Space(0.5))`，active 时 `Background(t.Accent)`，还靠 `ui.Column(...).Justify(SpaceBetween)` 手动贴底。
- **为什么**：官方 `ui.Tabs`（`ui/tabs.go:15-52`）画的就是**同一套**：`p.Fill(Rect{r.X+in, r.Y+r.H-h, ...}, line, h/2)`，`in=t.Space(1.5)`、`h=t.Space(0.5)`、`line=accent`，并且额外给了 hover 文字提亮（`if !on && tab.Hovered() { tab.TextColor(t.Text) }`）与 `FocusVisible` 焦点环。手绘版**丢了 hover 提亮**，且 20 行样板要自己维护。
- **同一产品内的直接证据**：能力页的页级 tabs 已经用官方 `ui.Tabs`（`plugins/capability_shell.go:91`），会话条却用手绘版——**同一个产品里两套 tab 外观**，是最典型的"官方件在隔壁已被证明可用、这里还在手搓"。
- **改法**：
  ```go
  ui.Tabs(c.Key("session.tabs"), &st.tab, labels...).Label(st.tr("会话视图", "Conversation views"))
  ```
  如需保住 TypeXS 字号与 Space4 间距：在返回的 `List` 上设 `Font(...)`/`Gap(design.Space4)`，不要重画下划线。
- **风险**：官方下划线的 inset（`Space(1.5)`）与当前手绘一致，但当前还做了 `Margin(Space2,0,0,0)` 的文字位移——替换后视觉会有 ~2 DIP 偏移，需要人工看 PNG 确认（一次即可）。
- **验收**：帧测试「→/← 切换 tab，Enter 不触发导航」；快照 `p1-session-*.png` 亮/暗各一张。

### F-05 | **P1** | 3 处 | 循环内手绘分隔线

- **位置**：`uikit/propertysheet.go:105-110`、`settings/settings.go:226-232`、`settings/settings.go:600-605`。
- **问题**：`for index, row := range rows { if index > 0 { ui.Divider(c) } ... }`。
- **官方替代**：`Element.Dividers(width, color)`（`ui/api_gen.go:4738-4744`）：在 row/column/List 的**相邻子项之间**画线，且"线不占间距，中间位置"，所以本来就该有一个 `Gap`。
- **改法**：
  ```go
  ui.Column(c).Dividers(1, t.Border).Gap(design.Space0).Children(func() { /* rows */ })
  ```
- **风险**：`Dividers` 在每两个子项之间都画线，如果循环里还有"条件性子项"（比如可选的错误行），线数量会变——`PropertySheet` 目前每个 row 恒一个 `ui.Row`，安全；`settings.go:601` 需先确认那个循环体内有 `if` 分支没。
- **验收**：像素断言分隔线数量 == rows-1（快照对比即可）；`go test ./internal/uikit ./internal/ui/settings`。

### F-06 | **P1** | 10 处 ButtonBase 调用点 | 状态机被手写 5 遍，其中 2 处可直接收敛

- **位置**：见 §1.3 表 1/2/3/4/5/6/7/8/9/10。
- **问题**：`IconButton`(ghost)、`runStatusQueueBadge`、`railSessionRow`、palette 行、slash/@ 行各自写了一遍 `switch { Pressed → BgSelected; Hovered/Focused → BgHover; default: ... }`。
- **为什么**：五份实现已经出现分叉（IconButton 的 Subtle 变体在 hover 时用 `BgSelected` 而 rest 用 `BgHover`；rail 行 pressed 用 `BgSelected`、hover 用 `BgHover`；palette 行的 chosen 用 accent **再叠一层自己的背景**，注释说是为了修 `SH-02`）。这种"同名状态不同色"是设计一致性的慢性病。
- **改法**（两步，先做低风险的那步）：
  1. `sidebar.go:1609` / `:2205` 两个行尾 `⋯` 直接换 `uikit.IconButton{Icon: icons.More(), Variant: uikit.IconButtonNative, ...}`（Native 走官方 `ui.Button`，自带完整状态机）。
  2. 在 `uikit` 里抽一个 `RowButton` 入口（内部仍 `ui.ButtonBase` + 一份状态机 + 一份 `Transition{Colors:true}`），让 rail 行/palette 行/slash 行/工作区行共用。**这不是新增自绘，是把既有自绘收敛成一处**（登记条目从 8 处减到 1 处）。
- **风险**：palette 行的 chosen 叠色是为修 `SH-02`（列表选中填充在后续 layout pass 没到），收敛时必须保住这条注释与行为，否则会回归到"Enter 跑了看不见的行"。
- **验收**：`uiroot/palette_test.go`、`rail_sessions_test.go`、`session/suggestion` 相关帧测试全绿；再加一条「⋯ 按钮 Tab 可达 + 有 Label + hover 有中间色帧」。

### F-07 | **P1** | 3 处 | hover 才显形的行内动作没有淡入

- **位置**：`sidebar/sidebar.go:1555`（工作区加号）、`:1615`（会话 `⋯`）、`:2212`（组 `⋯`）。
- **问题**：`Opacity(0).IgnoreInput(true)` → hover 时 `Opacity(1)`，一帧跳变。
- **为什么**：瞬显 + 瞬隐在指针快速扫过时会闪；而且 `Opacity(0)` 的元素仍占位（这是对的），但**键盘用户只有 `FocusWithin` 才看到**——`revealed := current || row.Hovered() || row.FocusWithin()`（`:1453`）逻辑正确，问题只在观感。
- **改法**：给动作容器 `.Transition(ui.ElementTransition{Duration: 120*time.Millisecond, Colors: true})`（`Opacity` 走 `AnimateColor` 或在 `Motion` 里做 enter/exit 都行；最小改动是给按钮加 `Transition{Colors:true}` 并在显示时 `Opacity(1)` 保持硬切——**先只做颜色渐变**，先拿到 80% 收益）。
- **风险**：`Opacity(0)` 会让 a11y 仍能读到（框架不会自动 `AccessHidden`），这与当前"键盘可达"意图一致，不要顺手加 `AccessHidden`。
- **验收**：帧测试「hover 后 1 帧即 `Find` 得到按钮 rect（不是 0 宽）」+ 中间帧像素非纯色。

### F-08 | **P1** | `uikit/uikit.go:109` | 已弃用件仍有调用方（契约自相矛盾）

- **位置**：`uikit/uikit.go:105-125`（`KeyValueRows`，注释里写了 `Deprecated: use PropertySheet`），调用方 `rightpanel/view.go:399`、`plugins/mcp_config.go:100`。
- **为什么**：注释自述"右对齐把值甩到页右边缘（实测 ~1050 DIP 无关联）"——即产品自己判定这是错形态，却还在两个页面用着。设计系统里"标了弃用但没人清"是最容易被复制的坏样板。
- **改法**：两处改成 `uikit.PropertySheet(c, uikit.PropertySheetOptions{Rows: ..., Locale: st.locale})`，然后删掉 `KeyValueRows`（`uikit` 内还有测试 `format_test.go` 之外无引用）。
- **风险**：`plugins/mcp_config.go` 的 rows 是动态 key，注意 `Key` 字段避免复制按钮身份冲突。
- **验收**：`grep -rn KeyValueRows internal` 只剩测试；两页快照对齐检查。

### F-09 | **P2** | 3 处 | 文档与源码漂移（以源码为准）

| 漂移 | 证据 | 建议 |
|---|---|---|
| `UI-COMPONENT-MIGRATION.md` P2 行称"代理预设工具 allowlist ✅ 已完成（官方 `ui.TokenField`）" | `grep -rn "ui.TokenField(" internal cmd` = **0**；`TokenField` 全仓仅出现在 mygo 模块缓存 | 若 allowlist 编辑已随"工具目录删除"下线，则删掉该行；若还在，用 `ui.TokenField` 实现 |
| `DESIGN.md §1.2-F`：`AlertDialog` 用于非危险确认 | `grep -rn "ui.AlertDialog("` = **0**（仅注释提及）；产品统一 `uikit.ConfirmDanger` | 改成"一律 `ConfirmDanger`；`AlertDialog` 当前不使用" |
| mygo 自己的 `docs/ui/text.md` 未记录 `TruncateStart/TruncateMiddle`；`docs/ui/*.md` 无 `Recorder`/`TextSystem`/`AccessChildren` | §3.2 统计：这 5 个 API 的 docs 命中数 = 0 | 以源码为准，本文已补事实 |

### F-10 | **P2** | 全仓 | 25 项官方能力闲置，其中 4 项对重设计直接有用

- **`ScrollbarStyle`**（`api_gen.go:7702-7717`）：可对 `Scroll/List/Table` 单独设 `TrackWidth/ThumbWidth/Square/Flush/MinLength/Thumb/Hover/Active`。当前只由 `Theme.Scrollbar` 全局定（`brand/palette.go:174,219`）。→ 建议在 `uikit` 里做一个全仓统一值（8 DIP 方角 + hover 加深），一次调用收口"滚动条比正文还显眼"的问题。
- **`Sticky()`**（`api_gen.go:8076-8088`）：小节标题吸顶。设置页/检查器长面板/右栏预览都能用。
- **`EndReached` + `NearEnd`**（`list.go:228,231`）：长会话/日志/产物历史按需加载；当前一次性全量。
- **`PopoverMenuOptions{Panel,Item}`**（v0.9.2 新增，`popovermenu.go:72-81`）：逐菜单换肤。当前只有窗口级 `SetMenuPresentation(ui.PopoverMenus)` + Theme，**是唯一能给"右栏 ⋯"更紧凑外观的官方途径**。

### F-11 | **P2** | 3 个 API | 对外不可调用，不要写进契约

- `Element.Access(fn)` / `Element.AccessChildren(fn)`：回调参数类型是 `*platform.AccessNode`，`platform = github.com/egoist/mygo/internal/platform`。实测探针报错：
  `use of internal package github.com/egoist/mygo/internal/platform not allowed`。
- `Context.TextSystem() *text.System`：`text` 同为 internal 包；且没有任何**已导出**的 mygo API 接受该值（`Painter.Glyphs` 收的是 `[]Glyph`），所以既不能命名也不能传递。
- **结论**：a11y 只能走 `Role/Label/Description/AccessSelected/AccessHidden/AccessDisabled/Announce`（本仓已这么做，正确）。`AccessChildren`（本可让 diff 逐行被读屏读到）**用不上**，别再讨论。

### F-12 | **P2** | 全仓 | 官方拖放已写进规范，代码 0 实现

- `DESIGN.md §1.2-D` 把「拖放 API（`Drop`/`DragOver`/`DataDragOver`）」列为规范，实测 `internal/**` 0 处使用。
- 缺口具体是：附件拖入 composer（`session/attachments.go` 现在只有"选择文件…/手动输入路径…"两个入口）、文件树拖拽移动。
- 这不是"官方已有却自绘"，而是"官方已有未实现"——列入 B 阶段排期，不要在本轮无请求地加功能（AGENTS §3.17/§7）。

### F-13 | **P2** | 全仓 | 表单字段从不标记 `Invalid`

- `Element.Invalid` / `ErrorMessage` / `DescribedBy` 全仓 0 使用；错误统一由 `uikit.ErrorLine`（`RoleAlert`，88 处）单独画一行。
- **为什么是问题**：读屏会播报错误，但**输入框本身不会被读成 invalid**，`Field` 与错误之间没有 `aria-errormessage` 关系；键盘用户在 Tab 回填时听不到"这个字段错了"。
- **改法**（官方 `api_gen.go:5081-5093` 示例）：
  ```go
  in := ui.TextInput(c.Key("model.base"), &st.base).Label(label).Invalid(errText != "")
  if errText != "" {
      uikit.ErrorLine(c, errText)   // 保留现有视觉
      in.ErrorMessage(uikit.LastErrorLine(c))  // 需 uikit 提供"返回该 error 元素"的变体
  }
  ```
  最小改动：`ErrorLine` 增加一个返回 `ui.Element` 的版本，字段侧补 `.Invalid(...)`。

### F-14 | **P3** | 全仓 | `Recorder` / `Touch` 对本产品不必要

- `ui.NewRecorder`（v0.9.2 新增，`recorder.go:30-157`）产出 `*scene.Scene`，渲染管线要自己接；本仓已有 `internal/devtools/ui-snapshot` 用 `Tester.Image()` + `SetScale` + `Advance` 出 PNG（`ui-snapshot/main.go:1920-1933`），能力覆盖。**结论：不引入**（唯一潜在价值"脱离 Tester 输入模型批量出帧"在本仓没有场景）。
- `Touch/Touches`（`api_gen.go:8385-8392`）：官方明说 macOS 无触屏、trackpad 不报触点；本产品是桌面三平台。**结论：不实现**（Windows 触屏平板不是目标）。

### F-15 | **P3** | 全仓 | fluent API 226 个未用，但绝大多数**不该用**

- 未用清单里真正"可惜"的只有：动效族（10 个）、`TruncateStart/Middle`、`Dividers`、`Sticky`、`ScrollbarStyle`、`FontFeatures("tnum")`（数字列等宽，用量/耗时/token 数会跳动）、`Invalid/ErrorMessage`。
- 其余（`Draw`/`ShaderInput`/`Wave`/`Perspective`/`Morph`/`Match`/`Cache`/`InView`/`FocusGroup`）是给画布型/网页型应用准备的，本产品不该碰——**避免为了"用官方"而制造视觉噪音**。

---
## 3. 建议（可执行）

### 3.1 官方组件全目录（v0.9.2，源码核对）

> 列含义：**组件** | 签名/要点（源码） | 官方文档 | **本项目**（非测试调用数 / 状态）。
> `docs/ui` 路径均相对 mygo 模块根。凡"本项目"为 0 的，即为未采用的官方件（多数是产品不需要，见 §3.2/§4）。

#### A. 布局与容器

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Column` `Row` | `Column(c)` `Row(c)`，flexbox 子集，内容走末尾 `.Children(func())` | layout.md | 260 / 264 已用 |
| `Box` | 无外观叶子容器（点、条、占位） | layout.md | 22 已用（8 DIP 状态点、2 DIP accent 左条、Spacer 型占位） |
| `Spacer` | 吃剩余空间 | layout.md | 52 已用 |
| `Divider` | 一条分隔线 | layout.md | 35 已用；**建议改用 `Element.Dividers`**（F-05） |
| `Grid` | CSS grid 式轨道（`RowTracks/ColumnTracks/GridRows`） | grid.md | 1 已用（右栏） |
| `Scroll` `ScrollHorizontal` `ScrollBoth` | 滚动容器 + `ScrollState`（`TrackScroll`/`ScrollToEnd`/`ScrollIntoView`） | scroll.md | 32 / 2 / 1 已用（单文本面与页面容器） |
| `Split` `SplitVertical`（`SplitPercent`/`SplitReset`） | 可拖分栏 + 双击复位 | split.md | `Split` 2 已用；`SplitPercent` 0（壳层用像素宽 + `SplitReset`） |
| `Toolbar` | 工具栏 + 自动溢出菜单 | toolbar.md | 1 已用（`uikit.ComposerToolbar`） |
| `Form` `Field` `Fieldset` | 标签/描述/错误/分组，a11y 绑定 | form.md | 1 / 73 / 1 已用 |
| `Accordion` `AccordionItem` `AccordionBase` | 手风琴 | accordion.md | **0**（产品用 `Collapsible`，手风琴语义不适用） |
| `Collapsible` `CollapsibleBase` | 折叠 + 状态回写 | collapsible.md | 20 / 6 已用（推理、工具、参数、运行历史） |
| `Overlay` | 窗口最上层浮层（Esc 栈/焦点陷阱/归还） | overlays.md | 3 已用 |
| `Themed` | 局部主题作用域 | styling.md | 2 已用 |
| `NativeView` | 平台原生视图槽 | native.md | **0** |
| `Element.Sticky()` | 滚动时吸顶（v0.9.x） | layout.md / scroll.md | **0** → 建议采用（F-10） |
| `Element.Dividers(w, c)` | 在子项之间画分隔线 | layout.md | **0** → 建议采用（F-05） |

#### B. 文本与文本语义

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Text` `Textf` | 换行文本 / 格式化 | text.md | 514 / **0**（用 `fmt.Sprintf` + `Text`） |
| `RichText` `Span` | 单段多 Span 混排（字重/颜色/下划线/阴影） | text.md | 1 已用（侧栏徽标混排） |
| `Link` | 可点击文本（系统 handler / URL） | link.md | **0**（markdown 内部链接由 `markdown.View` 处理） |
| `Element.Selectable` / `CopyData` | 跨段选择/复制（错误、ID、命令、路径）/ 富剪贴板载荷 | text-input.md | `Selectable` 17 已用；`CopyData` **0**（剪贴板走官方 `c.WriteClipboard` 与 `Services.ReadClipboardData`，10 处） |
| `Element.MaxLines/Ellipsis/NoWrap` | 绘制期截断 | text.md | 207 / 102 / 80 已用（主流写法，8 处漏网见 F-03） |
| `Element.TruncateStart()` `TruncateMiddle()` | **v0.9.1 新增**：省略号放行首 / 行中（保尾 / 保头保尾） | **无文档**（源码 `element.go:2006-2021`） | **0** → 建议采用（F-03） |
| `Element.FontFeatures("tnum")` | 等宽数字（OpenType） | text.md | **0** → 建议用于用量/耗时/token 数列 |
| `Element.TextShadow/TextStroke/LetterSpacing/Pretty/Balance` | 排版细节 | text.md | 0 / 0 / 0 / 0 / 0（暂不需要） |
| `Breadcrumbs` | 层级路径 | breadcrumbs.md | 1 已用（子会话返回） |

#### C. 按钮

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Button` `PrimaryButton` | 带外观按钮（原生状态机） | button.md | 110 / 14 已用 |
| `ButtonBase` | 无外观按钮（全状态自绘） | button.md / custom-widgets.md | 11 已用（§1.3 全部登记） |
| `SplitButton` `PrimarySplitButton` | 主段执行 + 箭头菜单二合一 | split-button.md | **0**（产品无"保存/另存为"式变体主操作） |
| `Toggle` `ToggleBase` `ToggleGroup` | 保持按下（非开关） | toggle.md | **0**（正确定位：开关用 `Switch`） |
| `Stepper` `StepSlider` | 步进 / 分段滑 | stepper.md / range-slider.md | **0** |

#### D. 菜单

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `PopoverMenuButton` + `PopoverMenuButtonOptions{At,Self,Open,Panel,Item}` | 窗口内绘制的锚点菜单（可反向/对齐/受控/换肤） | menu-button.md | 12 已用 |
| `Element.PopoverMenu(build, opts...)` + `PopoverMenuOptions{Panel,Item}` | **v0.9.2 签名扩展**：右键菜单可逐菜单换肤 | context-menu.md | `Element.ContextMenu` 3 已用；`PopoverMenuOptions` **0** |
| `MenuBase` | 无外观菜单按钮 | custom-widgets.md | **0** |
| `MenuButton` | 系统菜单 | menu-button.md | **0**（产品统一窗口内绘制：`c.SetMenuPresentation(ui.PopoverMenus)`） |
| `MenuItem`(`Danger/Radio/ID/Subtitle/Trailing/Shortcut/Content`) / `Menu.EditItems()` | 菜单项语义 | context-menu.md | 已用（随 `PopoverMenuButton`） |
| `ContextMenuBase` | 自定义右键菜单容器 | context-menu.md | **0** |

#### E. 输入

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `TextInput` `TextArea` | 单行 / 多行（原生 IME、撤销、右键菜单） | text-input.md / text-input-client.md | 59 / 10 已用 |
| `TextInputBuffer` `TextAreaBuffer`（+`Base`） | 大文本（`ui.TextBuffer` 有界分块） | text-input.md | **0**（当前无超大文本编辑面） |
| `SearchField` | 搜索（放大镜/清空/Esc） | search-field.md | 8 已用 |
| `FindBar` | 页内查找（计数/上下/Done） | find-bar.md | 1 已用 |
| `TokenField` + `Element.Query(&q)` | chips 多值输入；`Query` 为 v0.9.x 建议词查询 | token-field.md | **0**（文档声称已用 → 漂移，F-09） |
| `Combobox` `ComboboxBase` `Autocomplete` | 可过滤下拉 / 纯文本补全 | combobox.md / autocomplete.md | **0**（富行建议菜单走了自绘 + 官方 a11y 模式） |
| `NumberInput` | 数字输入（上下键、clamp） | number-input.md | 11 已用 |
| `DecimalInput` | 精确十进制（金额/比例） | decimal-input.md | **0** |
| `DateInput` `TimeInput` `Calendar` | 日期/时间（**官方无 DatePicker**） | date-input.md / time-input.md / calendar.md | **0**（自动化目前用文本表达式） |
| `EditableText` | 就地重命名 | editable-text.md | **0**（重命名仍走 Modal，属产品交互选择） |
| `Slider` `SliderBase` `RangeSlider` | 连续值 / 区间 | slider.md / range-slider.md | **0**（数值一律 `NumberInput`，正确） |
| `ColorPicker` `ColorWell` `Rating` | 颜色 / 评分 | color-picker.md / rating.md | **0** / **0** / **0** |

#### F. 选择

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Checkbox` `CheckboxBase` `CheckboxState`(+Base) `CheckboxGroup` | 勾选（含三态） | checkbox.md | 3 已用（其余 0） |
| `Switch` `SwitchBase` | 开关 | switch.md | 8 已用 |
| `Radio` `RadioBase[T]` `RadioGroup` | 互斥单选 | radio.md | **0**（互斥用 `SegmentedBase`/`Select`） |
| `Segmented` `SegmentedBase` | 分段控件 | segmented.md | `SegmentedBase` 1 已用（`uikit.SegmentedControl` 换肤，含对比度理由） |
| `Select` | 下拉选择 | select.md | 9 已用 |

#### G. 集合

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `List` + `ListState` + `ListRow` | 虚拟化列表：`Key/Label/Header/Selected/Selection/Reorder/DragRows/DragSelect/FollowEnd/FollowAnimation/**MoveAnimation**/HoldPlace/Sort/Columns/RoundRows/**EndReached/NearEnd**` | list.md | 30 已用；`FollowAnimation`/`MoveAnimation`/`EndReached` **0**（F-02/F-10） |
| `Table` + `TableColumn` + `CellSelect` + `ListState.{Cells,RowHeight,SortBy}` | 虚拟化表格（列宽/排序/单元格块选/固定行高） | table.md | 5 已用；`RowHeight` 0 |
| `Outline[K]` / `OutlineTable[K]` + `OutlineState[K]` | 虚拟化树/大纲 | outline.md | 2 已用（工作区树、文件树） |
| `Tree` `TreeBase` `TreeItem` | 小规模静态树 | tree.md | **0**（一律用 Outline，正确） |
| `GridView` + `GridState` | 虚拟化网格（卡片墙，固定卡高） | grid-view.md | 2 已用（能力页技能/连接器卡） |

#### H. 浮层与对话框

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Modal` / `DialogBase` | 遮罩对话框（焦点陷阱/Esc/焦点归还） | dialog.md | 29 / **0** 已用 |
| `AlertDialog` | 简单确认（末位按钮=默认） | alert-dialog.md | **0**（产品统一 `uikit.ConfirmDanger`，见 F-09） |
| `Popover` `PopoverBase` | 锚点面板 | popover.md | 1 / 7 已用 |
| `TooltipBase` / `Element.Tooltip` | 悬停/聚焦提示 | tooltip.md | `TooltipBase` 0；`.Tooltip(` 86 已用 |
| `ToastBase` `ToastViewportBase` + `c.AddToast/ToastAction` | 通知（计时/暂停/Esc/读屏归框架） | toast.md | 1 / 1 / 1 已用 |
| `Element.Transition(ElementTransition)` | **enter/exit + 颜色/阴影/尺寸过渡**（`Motion`/`Match`/`Fly`/`ScaleContent`/`AfterExits`） | transitions.md | **0** → P0（F-01） |

#### I. 导航

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Tabs` `TabsBase` | 标签页（内置 accent 下划线 + hover 提亮 + FocusVisible） | tabs.md | 1 / 3 已用（会话条手绘下划线，F-04） |
| `Sidebar` `SidebarSection` `SidebarItem` | Finder 式源列表 | sidebar.md | 2 / 1 / 2 已用 |
| `BackButton` `ForwardButton` `Router` | 历史导航 | navigation.md | **0**（用 `uikit.Breadcrumbs` + host navigator） |
| `Role*`/`Controls`/`LabelledBy`/`DescribedBy` | landmark 与 tabpanel 关联 | accessibility.md | `Role` 9、`Controls` 1、`LabelledBy`/`DescribedBy` **0** |

#### J. 反馈与状态

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Spinner` | 未知时长忙碌 | spinner.md | 26 已用 |
| `Progress` | 真实进度 | progress.md | **0**（明确不伪造，`settings/listhelper.go:45`） |
| `Meter` + `MeterLevels` | 水位/阈值（上下文占用可视化） | meter.md | **0**（当前只有 `42K/128K · 剩余 67%` 文本） |
| `Badge` | 信息徽章 | badge.md | 22 已用 |
| `Avatar` | 头像/缩写 | avatar.md | **0**（团队浮层用 Badge/文本） |
| `Element.Busy/Settled` | 忙碌态/静止态 | — | **0** |

#### K. 富媒体

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Icon` + `ui.SVG`/`ParseSVG`/`MustParseSVG` | SVG 图标（`WithDark`/`Unsupported` 新增） | icon.md | 69 已用；`WithDark`/`Unsupported` **0** |
| `Image` + `ImageSource`（Bitmap/GIF/WebP/APNG/YUV） | 图片 | image.md | 6 已用 |
| `Video` + `VideoPlayer` | 视频播放 | video.md | **0** |
| `WebView` | 原生 webview 槽 | web-view.md | **0**（产品无 HTML） |
| `Canvas`/`Effect`/`Shader`/`Material`/`NativeGlass` | GPU 绘制/毛玻璃 | drawing.md / shaders.md / native.md | **0**（产品刻意不用画布） |

#### L. 编辑器 / Markdown / 富文本 / 图表

| 组件 | 签名 / 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `markdown.View/Stream/Revealed` + `Options/Theme/CodeBlockView` | Markdown 与流式 | markdown.md | `View` 15 / `Stream` 4 / `Options` 12 / `DefaultTheme` 2 / `CodeBlockView` 1 |
| `editor.View` + `State`/`Buffer`/`LanguageFor`/`LanguageForText` | 代码查看/编辑、语法高亮、虚拟化 | code-editor.md | `View` 1、`State` 6、`Buffer` 3、`LanguageFor` 2 |
| `editor.DiffView` + `DiffState` + `ParsePatch/SetPatch` | 统一/并排 diff、词级、折叠、审查 | diff-viewer.md | `DiffView` 3、`DiffState` 5、`ParsePatch` 2 |
| `richtext.Editor/Document`（Markdown/HTML/RTF 编解码） | 富文本编辑 | rich-text-editor.md | **0** |
| `ui/chart`（`Plot`/`Line`/`Area`/`Band`/`Pie`/`Scale{Linear,Log,SymLog,Time}`/`Stack`/`MinMaxIndex`） | headless 图表原语 | chart.md | **0**（Insights 用 `Table` 而非图表） |

#### M. 交互与动效 API（本仓整族未用）

| API | 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `Element.Transition(ElementTransition)` | 布局/尺寸/颜色/阴影过渡 + `Enter`/`Exit`（`Motion{X,Y,Opacity,Collapse,Scale,Rotate,Blur,Flip}`）+ `Match`/`Fly`（跨父级 magic move）+ `AfterExits` | transitions.md | **0** |
| `Element.Animate/AnimateTo/AnimateWith/AnimateColor/AnimatePose` + `Pose` | 逐帧插值（颜色/浮点/姿态） | motion.md | **0** |
| `Animation{Duration,Ease,Spring,Duration...}` / `ui.Snappy` / `ui.Bouncy` / `ui.EaseOut` | 曲线与弹簧 | motion.md | **0** |
| `Timeline` `Tween` `Keyframes` `Loop` `Motion.Spring()` | 时间线编排 | motion.md | **0** |
| `ContentTransition` / `Element.Morph` | 文本/内容形变 | transitions.md | **0** |
| `ListState.MoveAnimation` / `FollowAnimation` | 列表行滑移 / 贴底滑动 | list.md | **0**（F-02） |
| `Drag` `Drop` `DragOver` `DataDragOver` `Draggable` | 拖放 | drag-and-drop.md | **0**（F-12） |
| `Pinch`/`Touches`/`Touch` | 触控 | input.md | **0**（F-14） |

#### N. 辅助 API

| API | 要点 | 官方文档 | 本项目 |
|---|---|---|---|
| `c.Theme()` / `Theme{Space,Rem,Radius,ScrollbarStyle,…}` / `Preferences{TextScale,ReduceMotion}` | 主题与偏好 | styling.md | 已用（`internal/brand` 派生） |
| `Handle` + `Bind/Focus/CancelFocus/Focused/FocusWithin/Bounds/OnShortcut/**SelectAll**` | 持久身份/焦点/快捷键 | migration.md | `Bind` 5、`Focus` 14；`SelectAll` **0** |
| `FocusBind` / `ui.FocusedValue` | 焦点绑定到应用数据 | migration.md | **0**（用 `Handle.Focus`） |
| `ui.Local` / `ui.FocusedValue` / `c.Resolve` | 局部状态/元素解析 | migration.md | 已用（uikit 各入口） |
| `ListState`/`ScrollState`/`GridState`/`OutlineState`/`Router`（均带 Handle） | 状态对象 | 各自文档 | 已用 |
| `c.Announce` / `RoleAlert` / `RoleStatus` / `AccessHidden`/`AccessSelected` | 读屏 | accessibility.md | `Announce` 6、`Role` 9、`AccessSelected` 1、`AccessHidden` 1 |
| `ui.NewTester` + `TesterOptions{Preferences,Dark}`（`Click/Key/Type/Advance/Image/AccessByRole/SetScale/Touch*/Wheel`） | 帧测试 | testing.md | 已用（`internal/ui/*_test.go`、`internal/devtools`） |
| `ui.NewRecorder(RecorderOptions) *Recorder`（`Frame(at)/Now/Size/Close`） | **v0.9.2 新增**：无窗口帧录制 | **无文档** | **0**（判定：不引入，F-14） |
| `Context.TextSystem()` | **v0.9.2 新增**：窗口文本系统 | **无文档** | **0**（不可用，F-11） |
| `Services`（剪贴板/URL/Invalidate） | 跨线程 UI 服务 | migration.md | 已用 |

### 3.2 v0.9.1 / v0.9.2 新增能力逐项判定

> 生成方式：AST 比对 v0.9.0 与 v0.9.2 的导出面（脚本 `.cache/inventory/api_diff.sh`，输出
> `.cache/inventory/api090.txt` / `api092.txt`）。**v0.9.0→v0.9.2 新增导出面 35 条**（6 个类型、5 个 Touch 常量、24 个函数/方法条目），**删除 0**；其中 `PopoverMenu` 由定参改为变参（向后兼容扩展）。

| 新增 | 出处（源码） | 官方文档 | 外部可用 | 对重设计的价值 | 结论 |
|---|---|---|---|---|---|
| `Element.TruncateStart()` | `element.go:2006-2021`、`api_gen.go:8569` | **无** | ✅ | **高**：长路径 / 模型 id / 会话标题保尾（8 处数据截断的解法） | **采用**（F-03） |
| `Element.Query(*string)` | `api_gen.go:7197-7205` | token-field.md | ✅（仅 TokenField 生效） | 低（无 TokenField 场景） | 暂不用 |
| `Element.AccessChildren(fn)` | `api_gen.go:3443-3451` | **无** | ❌ `internal/platform` | 理论高（diff 行/代码行可读），实际不可用 | **不可用**（F-11） |
| `Element.Touches() []Touch`、`Touch`、`TouchPhase`（5 常量） | `api_gen.go:8385-8392` | input.md/accessibility.md | ✅ | 低（桌面三平台，macOS 无触屏） | 不采用（F-14） |
| `Handle.SelectAll(window...)` | `ref.go:63-67` | input.md/code-editor.md | ✅ | 低（编辑菜单 Cmd+A 已由框架处理） | 暂不用 |
| `ui.NewRecorder`、`Recorder`、`RecorderOptions`（`Frame/Now/Size/Close`） | `recorder.go:30-157` | **无** | ✅（但返回 `*scene.Scene`，需自接渲染） | 低（`ui-snapshot` 的 `Tester.Image` 已覆盖） | 不采用（F-14） |
| `Context.TextSystem()` | `context.go:1019-1023` | **无** | ❌ 返回 `internal/text.System`，无公开 API 接受 | 无 | **不可用** |
| `SVG.Unsupported() []string` | `svg.go:142-152` | image.md | ✅ | 中：第三方 SVG 画不出来时给说明而非空白 | 建议采用（markdown 图片/连接器图标） |
| `SVG.WithDark(bool)` | `svg.go:159-167` | image.md | ✅ | 中：品牌图/第三方图随**应用**主题而非系统 | 建议采用 |
| `PopoverMenuOptions{Panel,Item}`（`PopoverMenu` 变参） | `popovermenu.go:72-81` | context-menu.md | ✅ | 中：逐菜单换肤的唯一官方途径 | 建议按需采用（F-10） |
| `ListState.MoveAnimation *Animation` | `list.go:78`、`list_move.go` | list.md | ✅ | **高**：行折叠/重排/流式追加时滑移而非跳跃 | **采用**（F-02） |
| `ListState.EndReached func(last int)` / `NearEnd int` | `list.go:228,231` | list.md | ✅ | 中：长会话/日志分页加载 | 建议采用（长列表出现卡顿时） |
| `ListState.RowHeight float32`（Table 固定行高） | `list.go:194-200` | table.md | ✅ | 中：密集表格（Insights/诊断）多显示 40% 行 | 建议按需 |
| `Element.Access...` inert 语义（exit 副本不进 a11y 树） | `access.go:653,732`、`access_inert_test.go` | **无** | 自动 | **高（前置依赖）**：一旦用 `Transition` 的 exit，官方保证淡出副本不被读屏重播 | 随 F-01 自动获得 |
| `path_spill.go`（大路径溢出到 CPU 层） | `path_spill.go:1-25` | — | 自动 | 低（本仓无画布/大路径） | 无需动作 |
| `rasterscale.go`（缩放时按实际像素栅格化文本/缓存层） | `rasterscale.go:1-20` | — | 自动 | 中：一旦用 `Transition{ScaleContent}` 或 `TextScale` 放大，文本不糊 | 随 F-01 自动获得 |
| `list_move.go`（MoveAnimation 的实现） | `list_move.go:1-40` | list.md | 自动 | 高 | 见 `MoveAnimation` |
| `SettleTime`/`Progress`（弹簧时长/进度换算） | `motion.go:143-146` | motion.md | ✅ | 低（做编排时用） | 暂不用 |
| `ShaderInstances`（内部） | — | — | ❌ `internal/scene` | 无 | 不适用 |
| `VideoSeeker` | `video.go` | video.md | ✅ | 低（无视频） | 不采用 |
| `Measure/Rasterize`（`svg.Font`，内部类型） | — | — | ❌ | 无 | 不适用 |

### 3.3 逐文件结论（internal/ui 13 模块 + internal/uiroot + internal/uikit，非测试 .go 共 119 个）

> 三态：**迁移**（现在就改）/ **可迁移**（顺手改，低优先）/ **保留**（有理由）。同数据在 `A1-matrix.tsv` C 段（机读）。

| 文件 | 行数 | 结论 | 现状（该文件已用的官方件） | 理由 |
|---|---:|---|---|---|
| `internal/ui/approval/approval.go` | 310 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/automation/automation.go` | 248 | 保留 | List | 组合层：全为官方件，无手绘状态 |
| `internal/ui/automation/view.go` | 1292 | 迁移 | Button Collapsible Divider Field Form List PopoverMenuButton PrimaryButton Row SearchField Spacer Switch Text TextArea TextInput | 运行摘要数据截断 → 官方替代：Ellipsis |
| `internal/ui/home/composer.go` | 851 | 迁移 | Button Icon PopoverMenuButton Row Spinner Text | 上下文 chip 数据截断 → 官方替代：Ellipsis |
| `internal/ui/home/home.go` | 533 | 保留 | Button Icon Image List Row Scroll Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/insights/insights.go` | 318 | 保留 | List Table | 组合层：全为官方件，无手绘状态 |
| `internal/ui/insights/view.go` | 734 | 保留 | Button List Row Scroll SearchField Spacer Spinner Table Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/interaction/interaction.go` | 526 | 保留 | Divider Modal Scroll Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/approval.go` | 860 | 迁移 | Badge Button Collapsible Divider Icon Modal Row Scroll Spacer Text | 审批摘要 runes 截断 → 官方替代：MaxLines(1).Ellipsis |
| `internal/uikit/ask.go` | 220 | 保留 | Button Divider Modal Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/composer.go` | 414 | 保留 | Button Icon List PrimaryButton Row Spacer Text TextArea | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/diffpreview.go` | 353 | 保留 | Row Scroll Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/format.go` | 166 | 保留 | 0 | 纯数据/映射层，不绘制 |
| `internal/uikit/iconbutton.go` | 237 | 可迁移 | Button Icon | 入口组件正确（官方无图标按钮）；补 120ms 颜色过渡即可 → 官方替代：ElementTransition{Colors:true} |
| `internal/uikit/locale.go` | 55 | 保留 | 0 | 纯数据/映射层，不绘制 |
| `internal/uikit/markdown_code.go` | 93 | 保留 | Row Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/markdown.go` | 100 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uikit/markdown_image.go` | 107 | 保留 | Icon Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/memoryedit.go` | 96 | 保留 | Divider Field Modal Text TextArea TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/onaccent.go` | 27 | 保留 | Badge | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/planreview.go` | 205 | 保留 | Divider Modal Scroll Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/previewprose.go` | 56 | 保留 | 0 | 数据侧有界载荷，非 UI 文本 |
| `internal/uikit/propertysheet.go` | 158 | 迁移 | Divider Row Text | 循环内手绘 Divider 分隔 → 官方替代：Element.Dividers(1,t.Border) |
| `internal/uikit/readingcolumn.go` | 38 | 保留 | Row | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/reasoning.go` | 264 | 可迁移 | Collapsible Icon Row Spacer Spinner Text | 折叠触发器 hover 硬切 → 官方替代：Transition(Colors:true) |
| `internal/uikit/runstatus.go` | 448 | 迁移 | Badge Button Collapsible Divider Icon List PopoverBase Row Spacer Spinner Text | 队列摘要数据截断 + 徽章状态硬切 → 官方替代：Ellipsis/TruncateStart + Transition(Colors) |
| `internal/uikit/screennav.go` | 56 | 保留 | 0 | 纯数据/映射层，不绘制 |
| `internal/uikit/segmented.go` | 94 | 保留 | Segmented Text | 官方 SegmentedBase 换肤（合法偏离，已在注释说明对比度理由） |
| `internal/uikit/session.go` | 356 | 迁移 | Badge Button Icon List Row Spinner Text | transcript 已官方化，缺两条动画 → 官方替代：ListState.FollowAnimation/MoveAnimation |
| `internal/uikit/settingsform.go` | 152 | 保留 | Button Divider Form Icon PrimaryButton Row Switch Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/status.go` | 333 | 保留 | Badge Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/toolbar.go` | 119 | 保留 | Button Icon Row Text Toolbar | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/toolcall.go` | 199 | 保留 | Collapsible Row Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/uikit/toolnames.go` | 93 | 保留 | 0 | 纯数据/映射层，不绘制 |
| `internal/uikit/toolpreview.go` | 424 | 保留 | 0 | 纯文案函数（数据侧截断） |
| `internal/uikit/toolrow.go` | 457 | 迁移 | Collapsible Icon Row Scroll Spacer Spinner Text | 高频 hover 无过渡；摘要数据截断 → 官方替代：Transition(Colors)+Ellipsis |
| `internal/uikit/uikit.go` | 414 | 迁移 | AlertDialog Button Divider Field Icon Modal PrimaryButton Row Spacer Text | 弃用件仍有调用方；Section 分隔可一行化 → 官方替代：Dividers / 下线 KeyValueRows |
| `internal/ui/plugins/capability_shell.go` | 133 | 保留 | Divider Scroll Tabs Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/connectors_auth.go` | 285 | 保留 | Button Modal Row Spinner Text | 错误载荷保界（数据侧），非标签截断 |
| `internal/ui/plugins/connectors_edit.go` | 1224 | 保留 | Checkbox Collapsible Divider Field Fieldset Modal Row Scroll SearchField Select Spinner Text TextArea TextInput | 错误载荷保界（数据侧），非标签截断 |
| `internal/ui/plugins/connectors_reload.go` | 68 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/plugins/connectors_section.go` | 890 | 保留 | Badge Button Collapsible Field Grid GridView Icon Row Spacer Spinner Switch Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/mcp_config.go` | 101 | 迁移 | Text | KeyValueRows 弃用件调用方 → 官方替代：PropertySheet |
| `internal/ui/plugins/mcp_server_switches.go` | 222 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/plugins/modules_section.go` | 74 | 保留 | Collapsible List Row Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/plugin_params.go` | 258 | 保留 | Badge Button Collapsible Divider List Modal Row Spacer Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/plugins.go` | 204 | 保留 | List | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/skills_edit.go` | 484 | 保留 | Collapsible Field Modal Scroll Text TextArea TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/skills_section.go` | 661 | 保留 | Badge Button Collapsible Field Grid GridView Icon Modal Row Scroll SearchField Spacer Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/plugins/view.go` | 206 | 迁移 | GridView Text | 状态文案 12 runes 截断 → 官方替代：Ellipsis + Tooltip |
| `internal/ui/rightpanel/artifacts_download.go` | 82 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/rightpanel/changes.go` | 295 | 保留 | Collapsible Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/rightpanel/files.go` | 652 | 保留 | Button Icon List PrimaryButton Row Scroll Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/rightpanel/rightpanel.go` | 656 | 保留 | List Outline Tree | 组合层：全为官方件，无手绘状态 |
| `internal/ui/rightpanel/view.go` | 771 | 迁移 | Button Grid Icon Image Outline PrimaryButton Row Scroll ScrollHorizontal Spacer Spinner Tabs TabsBase Text Tree | KeyValueRows 弃用件调用方 → 官方替代：PropertySheet |
| `internal/uiroot/artifacts.go` | 120 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/filedrop.go` | 36 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/foreground.go` | 67 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/frame.go` | 2139 | 可迁移 | Badge Button Divider Icon Image List Modal Overlay Popover Row Scroll SearchField Sidebar SidebarItem SidebarSection Spacer Split Text TextInput | rail 与 palette 富行合法自绘；缺过渡与列表动画 → 官方替代：Transition(Colors)/MoveAnimation |
| `internal/uiroot/interaction.go` | 145 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/locale.go` | 99 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/navigator.go` | 213 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/notifier.go` | 364 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/plan.go` | 114 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/running.go` | 124 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/runtime.go` | 401 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/shortcuts.go` | 356 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/theme.go` | 72 | 保留 | 0 | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/uiroot/toastanchor.go` | 138 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/uiroot/uiroot.go` | 577 | 保留 | List Modal Sidebar Split Tabs | 壳层状态/服务/runtime，绘制在 frame.go |
| `internal/ui/session/agentsurface.go` | 3129 | 保留 | Badge Breadcrumbs Button Divider Icon List Modal PopoverBase PrimaryButton Row Scroll ScrollBoth Spacer Spinner Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/attachments.go` | 797 | 保留 | Button Icon Image Modal Overlay Row Spinner Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/command_dialogs.go` | 301 | 保留 | Divider Modal Text TextArea TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/commands_help.go` | 137 | 保留 | Divider List Modal Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/commands_locale.go` | 79 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/session/compact_command.go` | 108 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/session/context_pane.go` | 1249 | 保留 | Badge Button List Modal Row Scroll Spinner Table Text | List/Table 已是官方件 |
| `internal/ui/session/export.go` | 236 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/session/goal.go` | 525 | 可迁移 | Badge Button Icon Row Text | hover 硬切 → 官方替代：Transition(Colors:true) |
| `internal/ui/session/interrupted.go` | 83 | 保留 | Button Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/memory_tools.go` | 369 | 保留 | Button Divider Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/session.go` | 1459 | 可迁移 | FindBar List Scroll | transcript 状态宿主 → 官方替代：FollowAnimation |
| `internal/ui/session/session_reminders.go` | 510 | 保留 | Button Field List Modal PrimaryButton Row Scroll Spacer Switch Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/session/todo.go` | 214 | 可迁移 | Collapsible Icon List Row Text | hover 硬切 → 官方替代：Transition(Colors:true) |
| `internal/ui/session/view.go` | 5451 | 迁移 | Badge Button Collapsible Divider FindBar Icon List PopoverBase PopoverMenuButton PrimaryButton Row Scroll SearchField Spacer Spinner Tabs TabsBase Text TextInput Toolbar | 手绘 tabs 下划线可整体换官方 Tabs；模型 chip 数据截断 → 官方替代：ui.Tabs / TruncateStart |
| `internal/ui/session/worksteps.go` | 188 | 保留 | Icon Row Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/about.go` | 109 | 保留 | Collapsible | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/archive.go` | 276 | 保留 | Button List Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/behavior.go` | 481 | 保留 | Collapsible Field PopoverMenuButton Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/capabilities.go` | 318 | 保留 | Field NumberInput Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/data.go` | 399 | 保留 | Badge Button Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/denied_globs.go` | 138 | 保留 | Field TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/diagnostics_modules.go` | 96 | 保留 | Collapsible List Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/hotkey_settings.go` | 427 | 保留 | Button Field Icon Row Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/import.go` | 313 | 保留 | Badge Button Field PrimaryButton Row Spacer Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/listhelper.go` | 86 | 保留 | List PrimaryButton Progress Row Spinner | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/lsp_settings.go` | 417 | 保留 | Button Field List Row Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/memory.go` | 910 | 保留 | Button Field List NumberInput Row SearchField Spacer Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/modelcatalog_editor.go` | 1064 | 保留 | Button Checkbox Collapsible Divider Field List Modal NumberInput Row Scroll Select Text TextArea TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/modelcatalog.go` | 815 | 保留 | Badge Button Checkbox Field List Modal NumberInput Row Select Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/model_check.go` | 256 | 迁移 | Button Row Spacer Text | 模型 id 数据截断（差异在尾部） → 官方替代：TruncateStart + Ellipsis |
| `internal/ui/settings/network.go` | 162 | 保留 | Field Text TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/notifications.go` | 129 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/settings/page_search.go` | 226 | 保留 | Scroll | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/redact.go` | 156 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/settings/reentry.go` | 67 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/settings/sandbox.go` | 100 | 保留 | Row Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/search.go` | 171 | 保留 | Field Row TextInput | 组合层：全为官方件，无手绘状态 |
| `internal/ui/settings/settings.go` | 1682 | 迁移 | Badge Button Collapsible Divider Field Form Icon List PrimaryButton Row Select Switch Text TextArea TextInput Toggle | 2 处循环内手绘 Divider → 官方替代：Element.Dividers |
| `internal/ui/settings/theme_mode.go` | 79 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/settings/tray_settings.go` | 19 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/settings/update.go` | 655 | 保留 | Badge Button Row Scroll Spacer Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/sidebar/archive_notice.go` | 39 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/sidebar/danger_confirm.go` | 77 | 保留 | AlertDialog Modal Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/sidebar/sidebar.go` | 2557 | 迁移 | Badge Button Divider Icon List Modal Outline PopoverMenuButton PrimaryButton RichText Row SearchField Select SidebarItem Spacer Spinner Text TextInput | 3 处 ⋯ 触发器可收敛到 uikit.IconButton；3 处 hover 显形需过渡 → 官方替代：IconButton + Transition |
| `internal/ui/sidebar/workspace_add.go` | 179 | 保留 | 0 | 非绘制层（状态/格式/服务），无 UI 自绘 |
| `internal/ui/toasts/toasts.go` | 652 | 可迁移 | Icon Row Text ToastBase | 官方 ToastBase 换肤合法；按钮状态硬切 → 官方替代：Transition(Colors:true) |
| `internal/ui/toolcards/toolcards.go` | 459 | 保留 | Collapsible Spinner Text | 组合层：全为官方件，无手绘状态 |
| `internal/ui/welcome/welcome.go` | 109 | 保留 | Text | 组合层：全为官方件，无手绘状态 |

**三态汇总**：迁移 **15** 文件、可迁移 **7** 文件、保留 **97** 文件（合计 119）。
迁移清单：`uikit/{runstatus,uikit,propertysheet,session,approval,toolrow}.go`、
`ui/session/view.go`、`ui/sidebar/sidebar.go`、`ui/settings/{settings,model_check}.go`、
`ui/plugins/{view,mcp_config}.go`、`ui/rightpanel/view.go`、`ui/automation/view.go`、`ui/home/composer.go`。

### 3.4 迁移处方（可直接抄；每段都已在 `.cache/inventory/probe` 编译通过）

> 探针：`/data/mygo/.cache/inventory/probe/probe.go`（`go build ./...` + `go vet ./...` 均 0）。
> 所有片段依赖 `time` 与 `github.com/egoist/mygo/ui`。**不要**在 `build` 里做 I/O。

#### R1｜hover 渐变（P0，一处改全仓受益）

- 文件：`internal/uikit/iconbutton.go`（ghost 分支，`:130-180`）
- 插入点：`switch { ... }` 决定背景之后、`button.Children(...)` 之前。

```go
// 颜色过渡：hover/pressed/selected 之间渐变而非跳变。
// 用产品 token（internal/design/motion.go:50），含 ReduceMotion 降级；
// 官方依据 docs/ui/transitions.md「Colors: as a hover fades in and out」。
button = button.Transition(design.TransitionColors(c))
```

- 同款插入点（每个 1 行）：`uikit/runstatus.go:363-372` 之后、`uiroot/frame.go:437` 之后、
  `uiroot/frame.go:1176` 之后、`sidebar/sidebar.go:1454` 之后、`toasts/toasts.go:496` 与 `:506` 之后、
  `uikit/toolrow.go:304` 之后、`uikit/reasoning.go:102` 之后、`session/todo.go:86`、`session/goal.go:372`。
- 注意：这些元素的身份必须跨帧稳定（已全部有 `c.Key` 或位于稳定结构位置）。

#### R2｜列表动效（P0）：流式滑动 + 行位移

- 文件：`internal/uikit/session.go`（transcript 的 `ListState` 装配，`:110-130` 区域）、`session/session.go`（状态宿主）。

```go
// 流式追加：内容从下方滑入（ChatGPT/Messages 观感）
state.FollowAnimation = design.ListFollowAnimation(c)   // motion.go:87
// 行折叠/展开/重排：滑到新位置而不是跳过去（v0.9.1 list_move.go）
state.MoveAnimation = design.ListMoveAnimation(c)       // motion.go:95
```

- 前置：`state.Key` 已设（`message.ID`）；官方在 `ReduceMotion` 或测试的 `instant()` 下自动降级。
- 若担心长会话重建成本，先只在「≤200 行」时设 `MoveAnimation`（`len(runs)` 判断一次即可）。

#### R3｜绘制期截断（P1）：长路径 / 模型 id / 会话标题

```go
// 保尾（路径、模型 id、会话标题：差异在尾部）
design.Apply(ui.Text(c, name), t.MonoText(design.TypeXXS)).
    MaxLines(1).Ellipsis("…").TruncateStart()

// 保头保尾（文件路径）
ui.Text(c, path).MaxLines(1).TruncateMiddle()

// 普通单行标签（只截尾）
ui.Text(c, label).MaxLines(1).Ellipsis("…")
```

- 落点（8 处，逐个替换并保留 tooltip 全文）：`session/view.go:3979`（`truncateRunes` 改签名或直接内联）、
  `home/composer.go:355`、`settings/model_check.go:139`、`uikit/runstatus.go:388`、`uikit/approval.go:849`、
  `uikit/toolrow.go:456`、`plugins/view.go:177`、`automation/view.go:442`。
- 注意：`TruncateStart/Middle` 必须配合 `SingleLine()` 或 `MaxLines(n)`；官方 `text.md` 未记录，源码 `element.go:2006-2021` 为准。
- **`ui.Badge` 不吃截断选项**：源码 `coreBadge`（`ui/sidebar.go:221-226`）= `Box(Radius 999)` + 内部 `Text(...).SingleLine()`；`Ellipsis/TruncateStart` 只作用于**节点自身**的文本（`ui/layout.go` 的 `text.Params{... Ellipsis: e.ellipsis}`），设在 Badge 元素上不会传到内部 Text。
  所以徽章文案只有两条路：**(a)** 缩短数据（保留现状，但 12 runes 太狠，见 `plugins/view.go:177`，建议 → 20–24 runes 或按语义给全称）；**(b)** 需要真截断时自建 pill（如 `uikit.OnAccentBadge`/`StatusBadge` 的做法：容器 + `Text(...).MaxLines(1).Ellipsis()`），并把状态色 tint 放在容器上。

#### R4｜子项分隔线（P1）

```go
// uikit/propertysheet.go:105-110（删掉循环里的 if index > 0 { ui.Divider(c) }）
sheet.Children(func() {
    ui.Column(c).FillWidth().Dividers(1, t.Border).Children(func() { /* rows */ })
})
```

- 同款：`settings/settings.go:227`、`settings/settings.go:601`（后者先确认循环体内有无条件子项）。
- 语义：`Dividers` 在**相邻子项之间**画线，不占间距；所以要保留原有的 `Gap`。

#### R5｜会话标签条换成官方 Tabs（P1）

- 文件：`internal/ui/session/view.go:879-916`（`buildTabs`），删除 `underline := ui.Box(...)` 整段。

```go
tabs := ui.Tabs(c.Key("session.tabs"), &st.tab, labels...).
    Label(st.tr("会话视图", "Conversation views")).
    FillWidth().Height(design.Space8).Gap(design.Space4)
// 字号/字体走 token：文本样式从父级继承（text.md「set on the text or on any element above it」）
tabs = design.Apply(tabs, t.Font(design.TypeXS))
```

- 官方 `ui/tabs.go:15-52` 已提供：accent 下划线（inset `Space(1.5)`、高 `Space(0.5)`）、
  hover 文字提亮、`FocusVisible` 焦点环、`Changed()`、方向键/Home/End。
- 视觉差异：手绘版有 `Margin(design.Space2,0,0,0)` 的文字位移 → 替换后需人工看一张 PNG。

#### R6｜进场/退场过渡（P1）：面板与列表行

```go
// 浮层/对话框/面板的进出场：用产品 token（motion.go:63，进入 MotionEnter、退出 MotionBase）
el = el.Transition(design.TransitionOverlay(c))

// 行级 enter/exit（例如新消息、palette 结果行）：先看有没有 token，没有再用裸 Motion
el = el.Transition(ui.ElementTransition{
    Duration: design.MotionBase,
    Enter:    &ui.Motion{Opacity: 0, Y: 6},
    Exit:     &ui.Motion{Opacity: 0, Collapse: true},
})
```

- 建议首批：transcript 的新消息行、palette 结果行、设置页小节、右栏预览面板。
- 官方 `access_inert.go` 保证退场副本**不进 a11y 树、不触发读屏重播**（0.9.2 修复）——这正是本仓之前不敢用动效的顾虑。
- `Modal`/`Overlay` 自身的出现仍由框架控制；不要给 overlay 根节点加 `Position` 过渡（会与焦点陷阱的几何计算抢一帧）。

#### R7｜滚动条统一外观（P2）

```go
// 一处定义，全仓引用（建议放 internal/design 或 internal/uikit）
var scrollbarStyle = ui.ScrollbarStyle{
    ThumbWidth: 8, Square: true, Flush: true, MinLength: 24,
    // Thumb/Hover/Active 取 brand 的三档灰
}
ui.Scroll(c).ScrollbarStyle(scrollbarStyle)
```

- 落点：`uikit/readingcolumn.go`、`uikit/toolrow.go`、`rightpanel/view.go`、`settings/page_search.go`、transcript 的 `List`。
- 对比：当前只有 `Theme.Scrollbar` 一档颜色（`brand/palette.go:174,219`），无法做 hover 加深与方角。

#### R8｜小节吸顶（P2）

```go
ui.Row(c).Sticky().Background(t.BgBase).Padding(...).Children(func() { /* 小节标题 */ })
```

- 落点：`settings/settings.go` 的分节标题、`rightpanel/view.go` 的检查器小节、`uikit/propertysheet.go` 分组名。
- 必须给 `Background`，否则吸顶时正文会从标题下透出（官方 list 段头也有同样要求）。

#### R9｜第三方 SVG 的降级（P2）

```go
if missing := svg.Unsupported(); len(missing) > 0 {
    uikit.InlineHint(c, t, st.tr("该图有部分内容未绘制：", "Not drawn: ")+strings.Join(missing, ", "))
}
// 品牌图/第三方图跟随应用主题而不是系统
icon := svg.WithDark(t.Theme().Dark)
```

- 落点：`uikit/markdown_image.go`（markdown 图片）、`plugins/connectors_section.go`（连接器图标）、`design/conform`（图标自检）。

#### R10｜表单字段标记 invalid（P2）

```go
// uikit.ErrorLine 增加返回 ui.Element 的变体：func ErrorLineElement(...) ui.Element
in := ui.TextInput(c.Key("model.base"), &st.base).Label(label).Invalid(errText != "")
if errText != "" {
    uikit.ErrorLine(c, errText)          // 视觉不变
    in.ErrorMessage(errElem)             // aria-errormessage 关系
}
```

- 落点：`settings/capabilities.go`、`settings/network.go`、`plugins/connectors_edit.go`、`session/session_reminders.go` 等所有已有 `ErrorLine` 的表单。

#### R11｜逐菜单外观（P2，仅当需要差异化）

```go
el.PopoverMenu(func(m *ui.Menu) { /* items */ },
    ui.PopoverMenuOptions{
        Panel: func(p ui.Element) { p.Width(220).Radius(design.RadiusMD) },
        Item:  func(i ui.Element) { i.Height(design.RowCompactHeight) },
    })
```

- 现状：`c.SetMenuPresentation(ui.PopoverMenus)`（`uiroot/frame.go:53`）是**窗口级**统一外观。逐菜单换肤只在
  "右栏 ⋯ 需要更紧凑/命令面板分组需要更宽"时使用。

#### R12｜数字等宽（P3，一行）

```go
design.Apply(ui.Text(c, uikit.FormatCompactCount(n)), t.Font(design.TypeXXS)).FontFeatures("tnum")
```

- 落点：`uikit/runstatus.go` 用量行、`insights/view.go` 指标、`session/context_pane.go` token 计数——现在数字变化时会左右抖。

### 3.5 场景 → 组件映射增量（相对 DESIGN.md §2 场景表）

| Agent 场景 | 当前实现 | 目标（v0.9.2 官方） | 依据 |
|---|---|---|---|
| 会话视图切换（Chat/轨迹/记忆/上下文） | `TabsBase` + 手绘下划线 Box | `ui.Tabs`（内置下划线/hover/焦点环） | F-04 |
| 流式回复追加 | `FollowEnd` | `FollowEnd` + `FollowAnimation` | F-02 |
| 折叠/展开/重排导致行位移 | 跳变 | `ListState.MoveAnimation` | F-02 |
| 任何 hover/pressed/焦点变色 | 硬切 | `Transition{Colors:true, 120ms}` | F-01 |
| 长路径 / 模型 id / 会话标题溢出 | `runes[:n]+"…"` | `MaxLines(1).Ellipsis().TruncateStart()` | F-03 |
| 属性表（右栏 / MCP 配置） | 弃用 `KeyValueRows` | `uikit.PropertySheet` | F-08 |
| 设置页分节分隔 | 循环内 `ui.Divider` | `Element.Dividers(1, t.Border)` | F-05 |
| 长面板小节标题 | 随内容滚走 | `Element.Sticky()` + 不透明底 | F-10/R8 |
| 滚动条外观 | 仅 `Theme.Scrollbar` | `Element.ScrollbarStyle` | F-10/R7 |
| 长会话/日志分页 | 全量渲染 | `EndReached` + `NearEnd` | F-10 |
| 第三方 SVG 画不出 | 空白 | `SVG.Unsupported()` 提示 | R9 |
| 表单校验失败 | 独立 `RoleAlert` 行 | 同上 + `Invalid()` + `ErrorMessage()` | F-13 |
| 菜单需要局部不同外观 | 只有窗口级 | `PopoverMenuOptions{Panel,Item}` | F-10/R11 |
| 数字列（用量/耗时/token） | 比例字宽跳动 | `FontFeatures("tnum")` | R12 |
| 附件拖入 composer | 只能点选 | `Drop`/`DragOver`（官方件，未实现） | F-12 |

---

## 4. 风险与取舍

1. **动效 vs 124 个既有测试文件。** `ui.Tester` 的时钟默认"停住"，`Transition`/`MoveAnimation` 只在 `Advance`
   后前进；但**像素快照断言**（`Image()` 比对类测试）可能在动画中间帧取到不同颜色。
   对策：测试里统一 `SetPreferences(ui.Preferences{ReduceMotion: true})`（官方语义：无动画），
   动效本身用新增的"两帧中间色"测试证明；不要为了测试去掉动效。
2. **`Transition` 依赖跨帧身份。** 本项目循环内控件均已 `c.Key`；但 `sidebar` 的树行用 `c.Key("workspace/"+key)`
   在**容器**上、内容行用位置身份。改造时必须先给行自身 `c.Key`，否则过渡会"串行"（元素被认为换了一个）。
3. **`MoveAnimation` 的成本在长会话。** 官方对"被影响的行"重排并插值；transcript 单行可能很高（markdown/工具卡）。
   建议：先开 `FollowAnimation`（无行重排成本），`MoveAnimation` 只在折叠密度高的 pane（轨迹/自动化历史）先试，
   用 `go test -bench` 或 `ui-snapshot` 计时确认没有掉帧，再上主 transcript。
4. **`ui.Tabs` 替换会改动 2 DIP 的视觉（文字位移）。** 只做一次，人工看 PNG；不要为了"零视觉变化"保留手绘——
   手绘版丢了 hover 提亮，这本身是缺陷。
5. **`Dividers` 与条件子项。** `settings/settings.go:601` 的循环体内有条件分支，直接换 `Dividers` 会多/少线；
   先确认每个子项恒定，否则保留现状（这条**允许不迁移**）。
6. **不要把 `Recorder`/`Touch`/`AccessChildren`/`TextSystem` 写进设计契约。** 前三者对产品无场景/不可调用，
   写进去会让实现者反复试错（本报告 F-11/F-14 已给出编译级反证）。
7. **文档同步义务。** 每落一条迁移，同步：`docs/UI-CONFORMANCE.md §1`（官方速查行）、
   `docs/DESIGN.md §1.2`（组件目录）/`§7.1`（自绘白名单条目数从 8 收敛到 1）、
   `docs/UI-COMPONENT-MIGRATION.md §3`（删掉 KeyValueRows 与 TokenField 的漂移行）、`AGENTS.md §3`（如果新增设计规则）。
   AGENTS.md §6 要求"官方 API 变化（升级 mygo）→ UI-CONFORMANCE §1 + DESIGN.md 附录 A source.version + §7.4 缺口"同 PR。
8. **动效的边界（design-guide Step 12）。** 只做**状态过渡**（hover/pressed/selected/焦点、进场/退场、行位移），
   不做装饰性动画（标题 hero 动画、卡片漂浮、渐变光效）。若某一屏的结构与层级还没定型，先解决结构，动效放最后。
9. **不要在 B 阶段顺手加功能。** `Meter`（上下文水位条）、`Avatar`（团队头像）、`ui/chart`（Insights 图表）、
   `Progress` 都属"官方有但产品没要"——按 AGENTS §3.17/§7 与 BRIEF §4，除用户明确要求，不新增。

---

## 5. 附：证据清单

### 5.1 命令与结果（可复现）

```bash
export PATH=/usr/local/go/bin:$PATH
export GOPATH=/data/mygo/.cache/gopath GOCACHE=/data/mygo/.cache/go-build
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp GOFLAGS=-mod=mod GOSUMDB=off
mkdir -p /tmp/zeokeel-tmp

# 1. 官方源码定位
go list -m -f '{{.Dir}} {{.Version}}' github.com/egoist/mygo
#   → /data/mygo/.cache/gopath/pkg/mod/github.com/egoist/mygo@v0.9.2 v0.9.2

# 2. 导出面（AST 比对 v0.9.0 / v0.9.2）
bash .cache/inventory/api_diff.sh          # 输出 .cache/inventory/api090.txt / api092.txt
#   v0.9.0: 1796 行导出面 / v0.9.2: 1829 行；comm 显示新增 35 条、删除 0 条

# 3. 逐文件指标（119 个非测试文件 × 15 列）
bash .cache/inventory/metrics.sh > .cache/inventory/metrics.tsv

# 4. 推荐片段编译验证
cd .cache/inventory/probe && go vet ./... && go build ./...   # 均 0

# 5. 全仓构建（未改代码）
go build ./...                                               # REPO BUILD OK
```

计数命令（本文所有数字的来源）：

```bash
grep -rn "ui\.ButtonBase" --include=*.go internal | grep -v _test | wc -l        # 13 行 = 10 个调用点（IconButton 的 if/else 占 2 行）+ 2 处注释
grep -rn "\.Hovered()" --include=*.go internal | grep -v _test | wc -l           # 20
grep -rn "\.Pressed()" --include=*.go internal | grep -v _test | wc -l           #  8
grep -rn "\.Focused()" --include=*.go internal | grep -v _test | wc -l           # 11（合计 39 处状态读点）
grep -rn "ui\.List(c" --include=*.go internal | grep -v _test | wc -l            # 30
grep -rn "\.Ellipsis(" --include=*.go internal | grep -v _test | wc -l           # 102
grep -rn "\.MaxLines(" --include=*.go internal | grep -v _test | wc -l           # 207
grep -rn "\.Transition(\|\.Animate(\|\.AnimateTo(\|\.Spring(\|\.Timeline(" internal --include=*.go | grep -v _test | wc -l   # 0
grep -rn "ui\.TokenField(\|ui\.AlertDialog(\|\.Dividers(\|TruncateStart\|TruncateMiddle" internal --include=*.go | grep -v _test | wc -l  # 0
```

### 5.2 `docs/ui/*.md` 88 篇目录（mygo v0.9.2）

accessibility accordion alert-dialog autocomplete avatar badge breadcrumbs browsers button calendar chart checkbox
code-editor collapsible color-picker combobox context-menu custom-widgets date-input decimal-input dialog diff-viewer
drag-and-drop drawing editable-text find-bar form grid grid-view history-buttons icon image input inspector layout link
list markdown menu-button meter migration motion navigation number-input outline overlays performance popover progress
radio range-slider rating rendering rich-text-editor right-to-left scroll search-field segmented select shaders sidebar
slider spinner split-button split stepper styling switch table tabs testing text-input-client text-input text time-input
toast toggle token-field toolbar tooltip transforms transitions tree video views web-view windows

> **文档滞后清单（以源码为准）**：`TruncateStart` / `TruncateMiddle`（`element.go:2006-2021`）、`Recorder`
> （`recorder.go`）、`Context.TextSystem`（`context.go:1019`）、`Element.AccessChildren`（`api_gen.go:3443`）
> 在 88 篇文档中**均无记载**；`docs/ui/text.md` 只写到 `Ellipsis`。

### 5.3 逐文件指标原始表

`.cache/inventory/metrics.tsv`（119 行 × 15 列：`FILE LINES OFFICIAL BTNBASE BOX HOVER PRESS FOCUS TRUNC DIVLOOP SCROLL LIST TOOLTIP ICON MOTION`）。

### 5.4 机读矩阵

[docs/research/redesign-2026-11/A1-matrix.tsv](A1-matrix.tsv)：A 段自绘点/动作项、B 段官方能力缺口、C 段逐文件三态（119 行）。

### 5.5 本文引用的关键官方源码行

| 事实 | 官方源码位置 |
|---|---|
| `ui.Tabs` 内置 accent 下划线 + hover 提亮 + FocusVisible | `ui/tabs.go:15-52` |
| `Element.TruncateStart` / `TruncateMiddle` 语义 | `ui/element.go:2006-2021` |
| `Element.Dividers(w, c)` 语义 | `ui/api_gen.go:4738-4744` |
| `Element.ScrollbarStyle`（Scroll/List/Table 通用） | `ui/api_gen.go:7702-7717` |
| `Element.Sticky` 语义 | `ui/api_gen.go:8076-8088` |
| `ElementTransition`（Colors/Shadows/Enter/Exit/Match/Fly/ScaleContent） | `ui/transition.go:100-` |
| `Animation` / `SpringOptions` / `Snappy` / `Bouncy` | `ui/motion.go:41-60,143-146` |
| `ListState.{MoveAnimation,EndReached,NearEnd,RowHeight}` | `ui/list.go:78,194-200,228,231` |
| `list_move.go`（MoveAnimation 实现） | `ui/list_move.go:1-40` |
| `PopoverMenuOptions{Panel,Item}`（0.9.2 新增） | `ui/popovermenu.go:72-81` |
| `SVG.Unsupported` / `WithDark` | `ui/svg.go:142-167` |
| `NewRecorder` / `RecorderOptions` | `ui/recorder.go:30-157` |
| `Handle.SelectAll` | `ui/ref.go:63-67` |
| `Context.TextSystem`（internal 类型） | `ui/context.go:1019-1023` |
| `AccessChildren`（internal platform 类型） | `ui/api_gen.go:3443-3451` |
| exit 副本 inert（不重播读屏） | `ui/access.go:653,732` + `ui/access_inert_test.go` |
