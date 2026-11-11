# ZeoKeel 设计契约（DESIGN.md）

> **给 AI coding agent 与 reviewer 的可执行设计契约。** 本文件不是设计原则散文，而是「写 UI 前必须查表、写完后必须能被检查」的硬约束集合。
>
> **版本锚定**：`github.com/egoist/mygo v0.8.3`（见 [../go.mod](../go.mod)）。本文的组件目录、API 签名与规则都以该版本源码 + `docs/ui/*.md` 实测为准；升级 mygo 时按 §8.4 重核。
>
> **文件分工**：
> - 本文 = 唯一契约入口（组件目录 / 场景映射 / 反模式 / 最小示例 / token / 入口组件 / 白名单 / 维护）。
> - [DESIGN-LINT.md](DESIGN-LINT.md) = 执行层：每条约束对应的确定性检查（规则 ID、接入点、基线）。
> - [AGENT-SURFACES.md](AGENT-SURFACES.md) = Agent 客户端 9 类 surface 的入口组件完整规格（状态矩阵 / API 草案 / 迁移）。
> - [UI-CONFORMANCE.md](UI-CONFORMANCE.md) = 官方 API 速查与写 UI 固定流程（逐条维护）。
> - [UI-COMPONENT-MIGRATION.md](UI-COMPONENT-MIGRATION.md) = 自绘保留清单与理由、官方化历史。
> - [DESIGN-REVIEW.md](DESIGN-REVIEW.md) = 2026-11-08 / mygo v0.7.4 的历史审计报告，**只作证据，不作现状依据**。
> - 根 [AGENTS.md](../AGENTS.md) = AI 每次任务开头必读的 10 条常驻规则 + 工作流。

---

## 0. 使用方式（Scope & precedence）

### 0.1 适用对象

| 对象 | 用途 |
|---|---|
| AI coding agent | 写任何 UI 代码前先查 §2/§3；按 §4 默认写法实现；交付前跑 §8.1 |
| Reviewer | 用 §3 反模式表 + §8.5 checklist 判断是否拒绝 |
| 设计系统维护者 | 新增组件/场景/自绘时按 §6/§7/§8.3 同步本文与 registry |

### 0.2 优先级与冲突处理

冲突时按下列顺序裁决（**高优先级覆盖低优先级**）：

1. 用户在当前对话中的明确指令；
2. 根 [AGENTS.md](../AGENTS.md)；
3. 本文件（DESIGN.md）；
4. [UI-CONFORMANCE.md](UI-CONFORMANCE.md)（官方 API 事实）；
5. `.agents/skills/` 的两个技能（design-guide / mygo-maintenance，通用方法论）；
6. 现有代码习惯（**现状不等于规范**；以本文为准）。

> 规则：**违反本文件的实现默认拒绝**。确实需要偏离时走 §7.2 的偏离流程，不允许「先写完再补理由」。

### 0.3 阅读路径

- **第一次改 UI**：§0.4 → §2（找场景）→ §3（避开反模式）→ §4（抄默认写法）→ §5（取 token）。
- **改 Agent 客户端表面**：§2 + [AGENT-SURFACES.md](AGENT-SURFACES.md) 对应表面 → 迁移步骤。
- **新增组件/自绘**：§1.4（决策顺序）→ §6（预设/入口）→ §7（白名单流程）→ §8.3（同步义务）。

### 0.4 版本锚定

- 官方事实来源：`/root/go/pkg/mod/github.com/egoist/mygo@v0.8.3/` 的 `docs/ui/*.md`（87 篇 = 25 篇指南 + 62 篇组件文档）与 `ui/` 源码。
- 文档与源码冲突时**以 v0.8.3 源码为准**；`docs/ui` 中的能力缺口见 §7.4。
- 本仓库当前依赖的是 mygo 的通用 `ui` 包。若未来依赖升级、引入更细的组件分类（例如 agent 客户端专用组件族），只需替换 §1.2、附录 A 的 `source` 与组件行，**三层约束结构不变**。

### 0.5 术语

| 术语 | 含义 |
|---|---|
| 官方件 | `github.com/egoist/mygo/ui` 包及其子包（`ui/markdown`、`ui/editor`、`ui/richtext`、`ui/chart`）提供的组件 |
| 最短调用（本项目版 ZeroOptions） | 只传必需参数、不做额外 fluent 配置的构造调用。v0.8.3 的 widget 默认取主题外观，**最短调用即设计默认** |
| token | `internal/design` 的语义颜色/字号/间距/圆角/阴影/布局度量；唯一 hex 来源是 `internal/brand` |
| 预设 | `internal/design/components.go` 的组件样式（PrimaryButton/SecondaryButton/DangerButton/Panel/Composer/Badge/…） |
| 入口组件 | 对官方件的薄组合，锁定某类 Agent surface 的语义/状态/骨架；见 §6.3 与 [AGENT-SURFACES.md](AGENT-SURFACES.md) |
| surface | Agent 客户端的一类界面表面：会话线程、工具调用、推理、审批、计划审阅、提问、composer、运行态、团队/分支 |
| 自绘 | 用 `*Base`/`ButtonBase`/`Box` 等自行绘制的控件外观；必须登记白名单并在代码里写理由 |

---

## 1. 组件清单与选用规则

### 1.1 资产分层（必须按层使用，不许跨层自造）

| 层 | 位置 | 内容 | 硬规则 |
|---|---|---|---|
| 品牌层 | `internal/brand` | 27 个语义色、8 个标志 hex（带 logo 出处）、`Radius=10` / `Spacing=4` / `FontSize=14`、字体栈、`LightTheme/DarkTheme` | **唯一允许出现 hex 的包**；改品牌只改这里 |
| 设计层 | `internal/design` | 22 个语义 token（含 `*Text` 变体）、8 级 type scale、`Space1..8`、`RadiusXS..Max`、2 个阴影 token、布局度量、9 个组件预设、`Style/Apply/Font/Text/TextMuted/Scale/Metrics` | 屏幕内**只能用 token/预设**，不许写裸颜色/裸度量 |
| 共享组合层 | `internal/uikit` | 页头/小节/空态/错误行/属性表/对话框动作条/工具行/工具图标/locale 等薄组合 | 组合 mygo 原生控件；**不手绘控件**；新共享模式加在这里 |
| 官方控件层 | `github.com/egoist/mygo/ui`（+子包） | 62 个组件的交互/焦点/键盘/a11y 语义 | 有官方件就**不许重造**；只设度量、不覆写原生状态色 |
| 入口层 | `internal/uikit`（目标新增） | Agent surface 入口组件（§6.3） | 场景内**只能从入口进入**；入口内部组合官方件 + token |

### 1.2 官方组件目录（按用途；API 以 v0.8.3 源码为准）

> 官方文档路径均在 mygo 模块的 `docs/ui/<name>.md`。表中「本项目规则」是硬约束，不是建议。

#### A. 布局与容器

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Column` / `Row` / `Box` | 纵/横/自绘容器，flexbox 子集 | 页面骨架唯一方式；内容用末尾 `.Children(func())`；间距用 `t.Space(n)` / `design.Space*` |
| `Spacer` / `Divider` | 吃剩余空间 / 分隔线 | 分隔优先 Divider+间距，不许给每个 section 加边框卡片 |
| `Grid` | CSS grid 式行列 | 多列对齐的表单/参数区可用；普通行不用 Grid 硬凑 |
| `Scroll` / `ScrollHorizontal` / `ScrollBoth` + `ScrollState` | 滚动容器 | **只用于单文本面/页面滚动**；集合一律 `ui.List`；偏移持久化用 `ScrollState.TrackScroll` |
| `Split` / `SplitVertical`（+`Percent`、`SplitReset`） | 可拖动分栏 | 壳层唯一分栏方式；宽度存设置；不许手写分隔条 |
| `Toolbar` | 窗口工具栏（按钮/Toggle/菜单，自动溢出菜单） | 工具栏行用 `ui.Toolbar`；**composer 动作行例外**（溢出会吞发送键，已登记自绘） |
| `Form` / `Field` / `Fieldset` | 标签/描述/错误/分组 | 所有表单字段必须用 `Field`（label 与 a11y 绑定）；多字段子组用 `Fieldset` |
| `Accordion` / `Collapsible`（+Base） | 折叠/手风琴 | 推理、工具详情、参数展开用 `Collapsible`；不许用 `if open` 手拼箭头 |
| `Overlay` | 窗口最上层浮层 | 命令面板/抽屉必须走官方 overlay recipe（Esc/焦点陷阱/焦点归还）；不许 ZIndex 模拟 |

#### B. 文本 / Markdown

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Text` / `Textf` | 换行文本/格式化 | 字号必须走 `t.Font(design.Type*)`；**禁止裸 `FontSize`**（会绕过 TextScale） |
| `RichText` / `Span` | 单段多 Span 混排 | 内联高亮/引用用 RichText；普通段落仍用 Text |
| `Link` | 可点击文本 | URL/系统 handler 用 Link；router 路径走 `ui.Router` |
| `Selectable` / `CopyData` | 跨段选择/复制 | 错误、ID、命令、路径必须可选中（`Selectable`） |
| `ui/markdown` `View` / `Stream` / `Revealed` | Markdown / **流式回复** | 会话正文与推理必须用 markdown；流式用 `Stream`，完成后同位置 `View`；**不许自解析/逐字符渲染** |

#### C. 按钮 / 菜单

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Button` / `PrimaryButton` / `ButtonBase` | 普通/主操作/无外观按钮 | 主操作 `ui.PrimaryButton` + `t.PrimaryButton()`（36 高；页面唯一主操作可用 `t.PrimaryButtonLarge()` 40）；次操作 `ui.Button` + `t.SecondaryButton()`；禁用主操作 `t.DisabledPrimary()`（中性面 + tertiary 文字，**不许 50% accent**）；危险用 `t.DangerButton()`；都**不许覆写背景色** |
| `Toggle` / `ToggleGroup` | 保持按下的按钮（非开关） | 只用于 press 语义（评分、格式）；**开关必须 `ui.Switch`** |
| `MenuButton` / `PopoverMenuButton` | 按钮+菜单（系统/窗口内绘制） | 行尾「⋯」与溢出菜单用 `PopoverMenuButton`；`Danger()`/`Radio()`/`ID()` 按语义用 |
| `Menu` / `MenuItem` / `ContextMenu` / `MenuBase` | 右键/下拉菜单 | 菜单项一律 `MenuItem`；同一菜单供右键与「⋯」复用；编辑项用 `m.EditItems()` |

#### D. 选择与输入

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Checkbox` / `Switch` / `Radio` / `RadioGroup` / `Segmented` | 勾选/开关/单选/分段 | 布尔设置用 `Switch`；互斥选项用 `Radio`/`Segmented`；**禁止 `Toggle` 冒充开关** |
| `Select` / `Combobox` / `Autocomplete` | 下拉/可过滤/补全 | 模型、provider、shell 等选择用官方 Select/Combobox；不许自绘下拉 |
| `Slider` / `RangeSlider` / `Stepper` / `Rating` / `ColorWell` | 连续值/区间/步进/评分/颜色 | 数值参数优先 `NumberInput`；滑块仅在需要连续手势时用 |
| `TextInput` / `TextArea` / `TextInputBuffer` / `TextAreaBuffer` | 单/多行输入 | composer 用 `TextArea`；大文本用 Buffer 版；`OnTextKey` 处理 Enter/Esc |
| `SearchField` | 搜索框（放大镜/清空/Esc） | 搜索语义必须 `SearchField`；不许 TextInput 手绘放大镜 |
| `FindBar` | 页内查找（计数/上下/Done） | 页内查找用 FindBar；搜索=过滤用 SearchField，二者不混 |
| `TokenField` | chips 多值输入 | 多值（工具 allowlist/标签）用 TokenField |
| `EditableText` | 原地重命名 | 列表内重命名可用；当前产品仍是 Modal 流程（登记为后续项） |
| `NumberInput` / `DecimalInput` | 数字/精确十进制 | 所有数字设置必须官方输入 + 默认开关；不许自由文本校验 |
| `DateInput` / `TimeInput` / `Calendar` | 日期/时间 | 定时任务等用官方输入；官方无 DatePicker，不许发明 |
| 拖放 API（`Drop` / `DragOver` / `DataDragOver`） | 文件/数据拖放 | 附件拖入 composer、文件移动用官方拖放 |

#### E. 集合

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `List` + `ListState` / `ListRow` | 虚拟化列表（任意长度、行高可变） | **所有集合的唯一选择**（transcript/轨迹/记忆/日志/文件/设置列表/侧栏）；`Key` 稳定、`Label` 供 a11y、流式 `FollowEnd`、折叠 `HoldPlace` |
| `Table` + `TableColumn` | 虚拟化表格（排序/列宽/单元格选择） | 多列可排序数据用 Table；不许 Row 手排 |
| `Outline` + `OutlineState[K]` | 虚拟化树/大纲 | 文件树、工作区树、可展开层级用 Outline；小树可用 `Tree` |
| `Tree` / `TreeBase` | 静态小树 | 只用于小规模固定树；大集合用 Outline |
| `GridView` + `GridState` | 虚拟化网格 | 卡片墙式集合用 GridView + State；不许 Row 循环分列 |

#### F. 浮层 / 对话框 / 通知

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Modal` / `DialogBase` | 遮罩对话框（焦点陷阱/Esc/焦点归还） | 结构化内容与危险确认用 Modal；动作条走 `uikit.DialogFooter`——危险动作**不得为 Default**，安全动作（取消）可 Default 承接 Enter；审批**无 Default** |
| `AlertDialog` | 重要简单确认（末位按钮=默认，Esc=取消，外部点击不关闭） | **只用于非危险、非不可逆**的简单确认；删除/清空/恢复默认一律用 `uikit.ConfirmDanger`（Modal + DialogHeader + DialogFooter：取消为 Default，危险按钮 danger 非 Default） |
| `Popover` / `PopoverBase` | 锚点面板 | 筛选/详情/参数浮层用 Popover；不许 Absolute 手搓 |
| `Tooltip` | 悬停/聚焦提示 | 图标按钮必须 `Label`+`Tooltip`；Description 优先于 tooltip 作为 AT 描述 |
| `c.Toast` / `c.ToastAction` / `c.AddToast` / `ToastViewportBase` | 通知/撤销 | 所有通知走 toast（框架负责计时/暂停/Esc/读屏）；**不许自绘 toast** |

#### G. 导航 / 主题 / 状态

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `Tabs` / `TabsBase` | 标签页 | 视图切换用 Tabs；不许用 tab 模拟分支/线程 |
| `Sidebar` / `SidebarSection` / `SidebarItem` | Finder 式源列表 | 侧栏导航/会话/工作区用 Sidebar；徽标用 Children(Badge) |
| `Breadcrumbs` / `BackButton` / `ForwardButton` / `Router` | 层级导航/历史 | 文件路径/设置子页用 Breadcrumbs/Router |
| `Spinner` / `Progress` / `Meter` | 未知/已知进度/水位 | 未知时长用 Spinner；真实进度用 Progress；不许伪造进度 |
| `Badge` | 状态/类型/计数 pill | 只表达信息，不做普通元数据装饰；禁用全宽胶囊条 |
| `Icon` / `Image` / `Avatar` | 图标/图片/头像 | 图标只从 `internal/icons` + `ui.Icon`；不许 emoji/Unicode/手写 SVG |
| `Themed` / `c.Theme()` / `Theme.Space(n)` / `Theme.Rem(n)` | 局部主题/间距/字号单位 | 颜色与间距只从这里取；不许硬编码 |
| a11y API（`Role` / `Label` / `ActiveDescendant` / `AccessSelected` / `AccessDisabled` / `Announce`） | 无障碍语义 | 自绘元素必须补 role+name+state；异步结果 `c.Announce` |

#### H. 编辑器 / 差异 / 富文本 / 图表（按需）

| 组件/API | 用途 | 本项目规则 |
|---|---|---|
| `ui/editor.View` + `State` / `Buffer` | 代码查看/编辑 | 文件预览/源码用 editor；不许自己格式化代码 |
| `ui/editor.DiffView` + `DiffState` | 并排/统一 diff | 变更/审阅用 DiffView；不许逐行画 diff |
| `ui/richtext.Editor` / `Document` | 富文本编辑 | 只在需要格式/附件时用；只读展示用 markdown |
| `ui/chart` | headless 图表原语 | 需要趋势图时用；颜色/字体走 token |

> **官方没有的 API（禁止发明）**：`ui.DialogFooter`、`Button.Default`/`Element.Default`、`ui.Markdown` 构造器（在 `ui/markdown` 子包）、`ui.DatePicker`（用 DateInput/Calendar）、`ui.CommandPalette`/`SearchDialog`/`Section`/`PageHeader`（应用层组合，本仓库在 `internal/uikit`）、`ui.Dialog` 构造器（用 Modal/DialogBase）。详见 §7.4。

### 1.3 跨组件硬规则

1. **官方件只设度量、不覆写状态色**：对 `ui.Button`/`ui.PrimaryButton` 覆写背景会关掉 mygo 原生 hover/pressed（`styleButton` 只给 base 背景上状态色）。品牌样式只允许改尺寸/圆角/字重（见 `internal/design/components.go` 的注释）。
2. **容器内容一律末尾 `func()`**：`.Children(func())` 或构造器收 `fn func()`；布局容器（Column/Row/Box）不在构造时收内容。
3. **状态放 app state**：`ListState`/`OutlineState[K]`/`GridState`/`ScrollState`/`Router`/`TextBuffer`/`editor.State`/`markdown.Options` 必须由模块实例持有；元素/parts **不能跨 build pass 存储**（dev/Tester 会 panic）。
4. **稳定身份用 `c.Key`**：循环内 stateful 控件、容器、列表行按 `c.Key`/`ListState.Key`/`ItemKey`；`c.Key` 必须在 children 之前。
5. **a11y 默认接上**：无文本控件 `Label`；heading 用 `RoleHeading`+`Level`；错误 `RoleAlert`；异步结果 `c.Announce`；自绘行/面板补 role 与状态。
6. **主题与缩放**：颜色/字号只经 `design.Of(c)` → `t.Font/t.Text/t.TextMuted`；`TextScale` 只作用于文字，不作用于间距/控件度量。
7. **Fluent 默认值不重复设置**：`editor.State`/`markdown.Options`/`richtext.EditorOptions` 零值即默认；不要为了"看起来对"堆配置。
8. **选择与激活分离**：`ui.List` 的 `Selected`/`Changed` 只表达"选择"（点击或方向键）；"打开/进入/提交"必须走 `Submitted()`（Enter/双击）或行内 `RowDoubleClicked()`。方向键/Home/End 不得触发导航或提交（文件树目录、设置归档列表等入口均按此修）。
9. **选择态两级**：持久导航/树/列表的当前项 = `BgSelected`(12%) + 左侧 2px `Accent` 竖条 + `LabelPrimary`（行内图标 `Accent`，徽章用普通 `uikit.Badge`）；只有浮层内的瞬态选择（命令面板、菜单、下拉当前项）保留 Accent 填充 + `OnPrimary`。满蓝导航选中行 = AP-19。
10. **危险确认与 Esc 安全默认**：不可逆动作必须 `uikit.ConfirmDanger`（安全动作 Default）；审批无 Default；「Esc 拒绝」等承诺必须在窗口级成立，不能因焦点在侧栏而失效（AP-24）。
11. **强调色预算**：每屏 ≤2 Accent 填充、≤1 primary、≤2 红色元素；状态徽章只用 `uikit.StatusBadge`（AP-20/AP-21）。
12. **字号与度量纪律**：11px 禁成句；控件高度只用 32/36/40/44（禁 38）；圆角语义见 §5.4；表面 ramp 见 §5.1（AP-22/AP-23）。

### 1.4 选用决策顺序

**先设计、后组件**：需求 → 信息结构 → 交互 → 布局 → 视觉层级 → 组件（design-guide §16/§25）。

**优先级链（从高到低）**：

```
官方 mygo 控件（有交互/焦点/键盘/a11y 语义）
  → 官方 *Base（保留交互，自定义外观）
  → design 预设 / uikit 共享组合（本仓库）
  → Agent 入口组件（§6.3）
  → 自绘（仅在 §7 白名单流程下）
```

**决策树（写代码前在注释/PR 里回答）**：

1. 这是集合吗？→ `List`（大/虚拟化）/ `Table`（多列）/ `Outline`（树）/ `GridView`（网格）。
2. 是浮层吗？→ `Modal`（结构化）/ `AlertDialog`（简单确认）/ `Popover`（锚点）/ `Overlay`（命令面板/抽屉）。
3. 是 Agent surface 吗？→ 查 §2 场景表，只能走入口组件。
4. 需要交互状态（焦点/键盘/选择）吗？→ 必须官方件或 `*Base`，不许 `Box+HandleInput`。
5. 只是样式/尺寸？→ token / 预设，不许字面量。
6. 官方确实没有、且必须自绘？→ §7.2 流程 + 登记 + lint 白名单。

---

## 2. Agent 场景 → 组件硬映射

> 原则：组件选择跟随**任务阶段**，不跟随"视觉一致性"。同一任务阶段只允许一个入口；入口内部的官方件固定，业务只通过 slot 提供数据。

### 2.1 场景表（写 UI 前先查）

| 任务阶段 | 唯一入口（目标） | 底层官方件（现状） | 明令禁止 |
|---|---|---|---|
| 会话线程 / 消息 | `uikit.SessionThread`（已有，Phase 3a） | `ui.List` + `ListState{Key, FollowEnd, HoldPlace}` + `markdown.Stream/View` | `Scroll` + 循环构建消息；手写 scroll 贴底；消息卡片墙 |
| 推理 / 思维链 | `uikit.ReasoningTrace`（已有，Phase 3a） | `ui.CollapsibleBase` + `markdown.View/Stream` + `Spinner` | 大 Card 当正文；默认展开；把推理混进正文段落 |
| 工具调用 | `uikit.ToolRow`（已有）+ `uikit.ToolCallCard`（已有，Phase 2c） | `ui.CollapsibleBase` + `Spinner` + `Badge` + 可复制文本段 | 塞进消息气泡；纯 `ui.Text` 渲染结果；另一套工具卡解剖 |
| 审批 / 中断 | `uikit.ApprovalDialog`（已有，Phase 3b；**Inline=V3 内联卡**为产品默认） | 内联卡（输入框上方，警示横幅+警示描边+中性卡体）或兼容 Modal + `uikit.DialogFooter`（**无 Default**，Esc=deny，危险在前）；session 负责 Esc 审批优先 | 普通 Button + 中性文案；默认焦点落在允许/拒绝；`AlertDialog` 末位默认语义；把审批做成居中 Modal（产品默认路径） |
| 计划审阅 | `uikit.PlanReviewDialog`（已有，Phase 3b） | `ui.Modal` + `markdown.View` + `DialogFooter`（无 Default；Esc 不取消；反馈 Enter=继续规划） | Esc 静默取消计划；空反馈 Enter 批准；自拼 footer 顺序 |
| 用户提问（ask） | `uikit.AskDialog`（已有，Phase 3b） | `ui.Modal` + 等权选项按钮 + `TextInput` + `DialogFooter`（取消 Default） | Enter 误选未选中的选项；选项排主次；空回答可提交 |
| Composer | `uikit.ComposerShell`（已有，Phase 3c） | `ui.TextArea` + `PopoverMenuButton` + `ui.PrimaryButton` + `uikit.ErrorLine` | 新造输入框；把主操作藏进溢出菜单；宽度写死 |
| 流式 / 运行态 | `uikit.RunStatus`（已有，Phase 3c） | `Spinner` / `Progress` / 队列 Badge / 停止按钮 / `Toast` | 按 token 重排布局；伪造进度；停止按钮与发送模糊并排 |
| 错误 / 恢复 | `uikit.ErrorLine`（已有）+ `uikit.RunStatus` 的重试（Phase 3c）；大块 ErrorPanel 暂缓（当前无调用点） | `RoleAlert` + danger token + 恢复动作（重试/去设置） | Warning 色；只报错不给下一步；纯 toast 代替可操作错误 |
| 多 Agent / 团队 | 会话 agent surface（`session` 包：header 三入口 + `ui.PopoverBase` 团队浮层 + 主栏子会话视图） | `PopoverBase` + 成员/任务卡 + 面包屑标题 + `ChildAgentService` | 把子代理/团队放进侧边栏或右栏页签；把 `active` 画成 running；卡片墙 |
| 代码 / 差异审阅 | `ui/editor.DiffView`（已有用法） | `editor.DiffState` + `Split` | 逐行文本 diff；手绘 gutter |
| 设置 / 表单 | 现有 `uikit` 组合 + 官方输入 | `Form/Field/Fieldset` + `Switch/NumberInput/Select` + `uikit.PageHeader/Section` | 自绘开关；无标签输入；保存按钮放内容底部 |

### 2.2 场景判定问句（实现前自检）

1. 用户此刻是在**读**（线程/推理/工具结果）、**决定**（审批/计划/提问）、还是**操作**（composer/停止/重试）？
2. 这个内容会**流式增长**吗？→ SessionThread/ReasoningTrace + FollowEnd，几何稳定。
3. 这个动作**不可逆**吗？→ 危险动作非 Default、必须有确认或撤销。
4. 这个信息**影响 Agent 下一步**吗？→ 必须产生结构化事件（见 [AGENT-SURFACES.md](AGENT-SURFACES.md) §2 双向上下文契约），不能只是展示。

### 2.3 非 Agent 场景速查

| 场景 | 组件 |
|---|---|
| 页面头部/小节 | `uikit.PageHeader` / `Section`；页面级动作 `PageHeaderAction`，小节动作 `SectionAction` |
| 设置列表 | `ui.List`（Key/Label）或 `ui.Form/Field`；禁止卡片墙 |
| 命令面板 | `ui.Overlay`/`Modal` + `SearchField` + `List` + `ActiveDescendant` |
| 查找 | 过滤用 `SearchField`；页内查找用 `FindBar` |
| 危险确认 | 危险/不可逆 → `ui.Modal` + `uikit.DialogFooter`（危险动作**不得 Default**；安全取消可 Default 承接 Enter）；非危险简单确认 → `ui.AlertDialog` |
| 通知 | `c.AddToast` / `c.ToastAction` |
| 空态 | `uikit.EmptyState`（纯文本）/ `EmptyStateBlock`（带下一步动作） |
| 错误行 | `uikit.ErrorLine`（RoleAlert，可选中） |

---

## 3. 反模式拒绝清单

> 编号 AP-xx 与 [DESIGN-LINT.md](DESIGN-LINT.md) 的规则 ID 对应；新增反模式必须同时新增检查或说明为何只能人工评审。

### 3.1 拒绝清单

| 编号 | 禁止 | 为什么 | 正确替代 | 检查 |
|---|---|---|---|---|
| AP-01 | 在 UI 层写颜色字面量（hex / `ui.RGBA` / `ui.Hex`） | 暗色/高对比失效；品牌无法统一演进 | `design.Tokens` / `c.Theme()`；阴影用 `t.ShadowOverlay()` | DS-01 |
| AP-02 | 覆写官方控件背景/边框来"品牌化" | 关掉 mygo 原生 hover/pressed/disabled 状态机 | 只设度量（尺寸/圆角/字重），颜色交给主题 | DS-02 |
| AP-03 | 裸数值度量（`FontSize`/`FontWeight`/`Padding`/`Height`/`Size`/`Width` 等字面量） | 绕过 type scale/TextScale/4 DIP 网格；跨屏漂移 | `t.Font(Type*)` / `design.Space*` / `design.Radius*` / `DialogWidth*` / 新 token | DS-03 |
| AP-04 | 手搓官方已有标准交互组件 | 焦点/键盘/a11y/IME/滚动语义会缺失或不一致 | 用官方件；外观不同用对应 `*Base` | DS-06 |
| AP-05 | 通用容器/组件模拟专用组件（Card 拼 CoT、Dialog 拼审批） | 语义、状态机、默认动作全部要自己维护，必然漂移 | §2 场景入口 | DS-04 |
| AP-06 | 纯文本代替结构化 slot（工具结果/选项/推理当正文） | 用户无法折叠/复制/操作；Agent 无法回传结构化事件 | 条目化 + 折叠 + 可复制段 | DS-04 |
| AP-07 | 给默认组件加多余 options/样式覆写（不知道默认已够） | 破坏视觉一致性；引入未测试状态 | 最短调用；确有需求走预设/入口 | DS-05 |
| AP-08 | 状态语义降级（error→warning/普通文本；审批动词中性化；`active`→running） | 危险/失败信息被弱化；用户误判 | `semantic.error`；语义动词；`RunState` 真实值 | DS-09 |
| AP-09 | 用 tab/badge 承担分支/状态机 | 丢失层级、键盘与 a11y 语义 | 分支/线程原语；状态组件 | DS-04/DS-09 |
| AP-10 | 图标乱用（emoji/Unicode/手写 SVG/尺寸裸值） | 视觉不一致；跨平台渲染差异 | `internal/icons` + `ui.Icon`；尺寸用 token/入口 | DS-07 |
| AP-11 | 自绘 base 不处理 Hovered/Pressed/Focused/Selected | 交互"看起来是死的" | 官方件优先；自绘必须补齐全部状态 | DS-06 |
| AP-12 | 对话框反惯例（主操作在左、危险动作默认焦点、Esc 语义与提示不一致） | 误操作风险（真实事故：发消息后 Enter 误触拒绝） | `uikit.DialogFooter` 排序；危险动作不得 Default（安全取消可 Default）；审批无 Default；Esc 明示 | DS-10 |
| AP-13 | 流式内容导致几何跳动（按 token 重排、滚动跳顶、无 FollowEnd） | 阅读中断、误操作 | `ListState.FollowEnd` + `HoldPlace` + 固定占位 | DS-03/帧测试 |
| AP-14 | 虚构官方 API（`ui.DialogFooter`、`Button.Default`、`ui.Markdown`、`ui.DatePicker`、`ui.CommandPalette`） | 编译失败或临时拼一个不合规实现 | §1.2 组合；`uikit.DialogFooter` 是应用层 | DS-08 |
| AP-15 | 新增组件/自绘不登记（DESIGN.md/registry/白名单/测试不同步） | 契约外生长，下一次审计才发现 | §7.2 流程 + §8.3 同步义务 | DS-08 |
| AP-16 | 在 build 回调做 I/O、每帧构建全量集合、跨线程直接改 UI | 卡帧、数据竞争、元素过期 | 缓存 + 显式刷新；`UIRuntime.Post`；`List` 虚拟化 | `go tool mygo vet` + 帧测试 |
| AP-17 | 覆盖原生控件尺寸导致焦点环/命中区异常（图标按钮过小、无 Label） | 键盘/触屏/读屏不可用 | 图标按钮 ≥ 24×24 目标区、必须 `Label`+`Tooltip` | DS-06/DS-07 |
| AP-18 | 用状态色小字当正文（Success/Warning/Danger 直接作正文前景） | 对比度不足 | 用 `*Text` 变体（WCAG-AA） | DS-02 |
| AP-19 | 持久导航/列表当前项用满饱和 Accent 填充 | 与「新对话」等主按钮形成两个重色块，选中语义与设置导航/检查器 tab 不一致 | `BgSelected`(12%) + 2px `Accent` 左条 + `LabelPrimary`；瞬态选择才用 Accent 填充 | 帧测试 + 快照（DS-12） |
| AP-20 | 一屏 >2 个 Accent 填充块或 >1 个 primary 按钮 | 主次不清，用户不知道下一步点哪 | 强调预算：每屏 ≤2 Accent、≤1 primary；分节动作 `SectionAction` | 帧测试/快照（DS-13） |
| AP-21 | 状态徽章用中性底 + 状态色文字拼装 | 红字压蓝灰底语义错位；状态没有统一解剖 | 只用 `uikit.StatusBadge`（语义 tint + `*Text` AA + 必填标签） | grep/评审（DS-14） |
| AP-22 | 11px 用于成句文本/按钮；控件高度 38 或非 4 倍数 | 可读性差、节奏破格 | 微标签才用 11；成句 ≥12，正文 14；高度 32/36/40/44 | 评审 + 快照（DS-15） |
| AP-23 | 表面 ramp 反转（暗色侧栏比页面浅）或抽屉/浮层无 elevation | 层级隐喻失效；抽屉看起来像布局被裁切 | S0–S4 ramp 两主题同向；抽屉/浮层 `ShadowOverlay` | 像素采样/快照（DS-16） |
| AP-24 | 危险确认用 `AlertDialog`（危险动作成为 Default/Enter）；「Esc 拒绝/停止」在焦点离开会话时失效 | 一次回车不可逆删除；承诺的快捷键不生效 | `uikit.ConfirmDanger`（取消 Default）；Esc 安全动作提升到 window scope | 帧测试（DS-11） |
| AP-25 | 页面主操作落点超过两种（页头右/固定页脚右下）；管理列表用 r16 卡墙 | 用户学不到“主操作在哪”；设置页像 dashboard | 两落点规则；管理列表平铺行 + Divider + 行尾 ⋯ | 评审 + 快照 |

### 3.2 现状违规证据（2026-11-09，Phase 1a/1b 后）

> 旧 [DESIGN-REVIEW.md](DESIGN-REVIEW.md) 是 v0.7.4 历史报告，多数 P0/P1 已修复；下表为 Phase 1 实施后的**现状**，剩余存量进 `.design-baseline.json`。

| 类别 | 计数 | 位置示例 | 目标 |
|---|---|---|---|
| 硬编码颜色（`ui.RGBA`） | **0**（Phase 1b 已清零） | 3 处 session 浮层阴影已改 `t.ShadowOverlay()` | 保持零（DS-01 阻断） |
| 裸 `FontSize` | **0**（Phase 1b 已清零） | 7 处 mono 小字已改 `t.MonoText(role)` | 保持零（DS-03 阻断） |
| 裸数值度量调用 | **51**（grep 行口径） | 全模块（UI 层已无宽度/字号字面量） | 逐模块 token 化，baseline 只降不升 |
| 裸 `FontWeight` | designlint warn（baseline 收口） | 多为标题 600 | heading type role 后收敛 |
| 手绘 `ButtonBase` | **10**（Phase 2a 后） | 命令面板行 / slash@ 富行 / 侧栏富行与行内 ⋯ / terminal copy success 态 / `uikit.IconButton` 内核 | 富行与 success 态登记 allowlist；图标按钮已全部收敛 |
| 重复状态→(文案,颜色) 函数 | **0**（Phase 2b 收敛） | uikit.Status* 唯一出口 | 保持零（design-check DS-09 阈值 0） |
| 对话框宽度绕 token | **0**（Phase 1b 已清零） | 8 处已换 `DialogWidth*`/`OverlayWidthS` | 保持零 |
| 工具卡渲染路径 | **2**（Phase 2c） | `uikit.ToolRow`（紧凑行）+ `uikit.ToolCallCard`（完整卡：决策/轨迹/plan/demo） | 保持 2；新增工具只注册渲染器 |
| 裸数值圆角 | 0 | — | 保持零（已 100% token 化） |

> 基线复算命令与完整清单见 [DESIGN-LINT.md](DESIGN-LINT.md) 附录 A。

---

## 4. 最小用法示例（默认即正确）

### 4.1 原则

1. **最短调用即设计默认**：v0.8.3 的 widget 构造后即取主题外观；`*Base` 才是"无外观、只保留交互"。
2. **带 options/state 的组件零值即默认**：`editor.State`、`markdown.Options`、`richtext.EditorOptions` 不要堆配置。
3. **本项目只加"度量封装"**：`design.Apply(el, t.PrimaryButton())` 这类只改尺寸/圆角/字重的预设可以加；**改颜色/背景的覆写不可以**。
4. **先默认，确有需求再升级**：需要非默认外观时，先查 §6 预设与入口组件，最后才走 §7 自绘流程。

### 4.2 官方件默认写法

```go
// 布局 + 文本：间距/字号走 token
ui.Column(c).Gap(design.Space2).Children(func() {
    design.Apply(ui.Text(c, "标题"), t.Font(design.TypeM)).TextColor(t.LabelPrimary)
    design.Apply(ui.Text(c, "说明"), t.TextMuted(design.TypeXS))
})
```

```go
// 按钮三档：主操作 Primary、次操作 Button、危险 Danger；只加度量预设
if design.Apply(ui.PrimaryButton(c, "保存"), t.PrimaryButton()).Clicked() {
    save()
}
if design.Apply(ui.Button(c, "取消"), t.SecondaryButton()).Clicked() {
    close()
}
if design.Apply(ui.Button(c, "删除"), t.DangerButton()).Clicked() {
    confirmDelete()
}
```

```go
// 输入与搜索
ui.TextInput(c, &st.name).Label("名称").Placeholder("例如：release")
ui.SearchField(c, &st.query).Label("搜索会话")
```

```go
// 表单：label/描述/错误都交给 Field（a11y 自动接线）
ui.Form(c, func() {
    ui.Field(c, "模型", func() { ui.TextInput(c, &st.model) }).Description("启动时生效")
    ui.Field(c, "启用自动更新", func() { ui.Switch(c, &st.autoUpdate) })
})
```

```go
// 列表：Key 稳定 + FollowEnd 流式跟随；集合不许 Scroll+循环
st.list.Key = func(i int) any { return messages[i].ID }
st.list.FollowEnd = true
ui.List(c, &st.list, len(messages)).
    Label("对话消息").
    Rows(func(row ui.ListRow) {
        design.Apply(ui.Text(row.Context, messages[row.Index].Text), t.Font(design.TypeBody))
    })
```

```go
// 对话框：结构化内容用 Modal；动作条只走 uikit.DialogFooter
ui.Modal(c, &st.open, func() {
    design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
        return el.PaddingX(design.Space6).PaddingY(design.Space2)
    }).Children(func() {
        design.Apply(ui.Text(c, "重命名"), t.Font(design.TypeL)).FontWeight(600)
        ui.TextInput(c, &st.draft).Label("新名称").AutoFocus()
        uikit.DialogFooter(c, []uikit.DialogAction{
            {Label: "取消", OnClick: func() { st.open = false }},
            {Label: "保存", Primary: true, OnClick: func() { rename(st.draft) }},
        })
    })
})
```

```go
// 非危险简单确认：AlertDialog 的最后一个按钮是默认（Enter）；危险/不可逆确认不要用 AlertDialog
switch ui.AlertDialog(c, &st.switching, "切换工作区？", "当前会话会保留，任务在后台继续运行。", "取消", "切换") {
case 1:
    switchWorkspace()
}
```

```go
// 按钮/菜单：窗口内绘制的统一 ⋯ 菜单
ui.PopoverMenuButton(c, "⋯", func(m *ui.Menu) {
    if m.Item("重命名").Chosen() { st.renaming = true }
    m.Separator()
    if m.Item("删除").Danger().Chosen() { st.confirming = true }
})
```

```go
// 折叠：推理/工具详情只用 Collapsible，不手拼箭头
ui.Collapsible(c.Key("reasoning."+msg.ID), "思考", &st.thinkOpen[msg.ID], func() {
    markdown.View(c, msg.Reasoning, st.reasoningMarkdownOptions(t))
})
```

```go
// 标签页/状态
ui.Tabs(c, &st.tab, "会话", "轨迹", "记忆")
ui.Spinner(c).Label("运行中")
ui.Progress(c, done).Label("上传中")
design.Apply(ui.Badge(c, "运行中"), t.Badge()).TextColor(t.WarningText)
```

```go
// 流式 Markdown：Stream 增量解析；完成后同位置 View
if msg.Streaming {
    markdown.Stream(c, msg.Text, st.markdownOptions(t))
} else {
    markdown.View(c, msg.Text, st.markdownOptions(t))
}
```

```go
// 无障碍：无文字控件必须 Label；自绘角色/状态接上
ui.Button(c, "").Label("关闭检查器").Tooltip("关闭检查器").Children(func() {
    ui.Icon(c, icons.Close()).Size(design.Space3, design.Space3)
})
design.Apply(ui.Text(c, "加载失败"), t.Font(design.TypeXS)).TextColor(t.DangerText).
    Role(ui.RoleAlert).Selectable()
```

> 更多官方用法见 [UI-CONFORMANCE.md](UI-CONFORMANCE.md) §1 与 mygo `docs/ui/*.md`。

### 4.3 本项目入口组件用法（目标 API 草案，尚未实现）

> 草案仅定契约；实现时以 [AGENT-SURFACES.md](AGENT-SURFACES.md) 的签名/状态矩阵为准。

```go
// 会话线程：虚拟化 + 流式跟随 + 空态 hero + 回到最新，都由入口负责
uikit.SessionThread(c, uikit.SessionThreadOptions{
    Messages: session.Messages,
    Running:  session.Running,
    Empty:    func(c *ui.Context) { buildHero(c) },
    OnRetry:  func() { retryLast() },
})
```

```go
// 工具行：状态机 / 折叠默认值 / 可复制段 / 团队跳转，都由入口负责
uikit.ToolRow(c, uikit.ToolRowOptions{
    Key: tool.ID, Name: tool.Name, RawName: tool.Name, Status: tool.Status,
    Preview: tool.Summary, Sections: sections, Footer: teamFooter,
})
```

### 4.4 反例 → 正例

| 反例 | 正例 |
|---|---|
| `ui.Text(c, msg.Text).FontSize(15).TextColor(ui.Hex("#333"))` | `design.Apply(ui.Text(c, msg.Text), t.Font(design.TypeBody))` |
| `ui.Button(c, "保存").Background(t.Accent)` | `design.Apply(ui.PrimaryButton(c, "保存"), t.PrimaryButton())` |
| `ui.Row(c).Children(...)` 手绘 checkbox | `ui.Checkbox(c, &st.ok, "同意")` |
| `ui.Toggle(c, &st.enabled, "启用")` 当开关 | `ui.Switch(c, &st.enabled)` / `ui.Field(c, "启用", ...)` |
| `Scroll` + `for range messages` 渲染会话 | `ui.List(c, &st.chatList, len(messages))` + `FollowEnd` |
| 手写 `ui.Row` footer：允许在左、拒绝在右 | `uikit.DialogFooter`：危险最左、主操作最右；无 Default |
| `ui.Text(c, "工具结果："+result)` | `uikit.ToolRow` + 可复制段（mono/Json 段） |
| 每个设置块一个 `t.Panel()` | `uikit.Section`（标题+Divider+body）；卡片只给独立对象 |

---

## 5. Token 语义表

> 取用方式：`t := design.Of(c)`；颜色 `t.X`，字号 `t.Font(design.Type*)`/`t.Text`/`t.TextMuted`，间距 `design.Space*` 或 `t.Theme().Space(n)`，圆角 `design.Radius*`，阴影 `t.ShadowComposer()`/`t.ShadowOverlay()`。

### 5.1 颜色 token（`internal/design/tokens.go`）

| token | 语义 | 典型用途 | 禁止 |
|---|---|---|---|
| `BgBase` | 页面背景 | 主内容区、滚动区底 | 当卡片/浮层底 |
| `BgSidebar` | 侧栏/检查器底 | 侧栏、右栏 | 主内容区 |
| `BgRaised` | 抬升表面 | 卡片、composer、面板 | 页面大背景 |
| `BgStrong` | 强化中性面 | 填充式中性控件 | 主操作（用 Accent） |
| `BgHover` / `BgSelected` | 悬停 / 选中 | 行、菜单项、tab | 两者混用（选中必须可区分） |
| `LabelPrimary/Secondary/Tertiary` | 主/次/弱文字 | 正文/说明/元数据 | 用状态色当正文 |
| `BorderL2/BorderL3` | 常规/强化分隔 | 分隔线、卡片边、聚焦前 | 给每块加边框 |
| `Accent*` / `OnPrimary` | 品牌强调/主操作前景 | 主按钮、选中、链接 | 大面积装饰 |
| `Success/SuccessText` | 成功（fill/AA 文本） | 状态点、Badge、成功文本 | fill 色当小字 |
| `Warning/WarningText` | 警告/进行中（fill/AA 文本） | running 状态、警告文本 | 用 warning 表达失败 |
| `Danger/DangerText` | 危险/失败（fill/AA 文本） | 错误、危险动作、失败状态 | 降级为普通文本 |
| `Scrim()` | 模态遮罩 | Modal 背景 | 自定义半透明色 |

**规则**：状态色本身只做 fill/border/icon；小字必须用 `*Text` 变体（WCAG-AA）。硬编码颜色 = AP-01。

**表面 ramp（S0–S4，两个主题同向）**：导航永远下沉，页面最低、浮层最高；暗色不能反转。

| 语义 | token | 浅色 | 暗色 | 用途 | 边框/阴影 |
|---|---|---|---|---|---|
| S0 page | `BgBase` | `#FFFFFF` | `#111118` | 主内容、transcript、设置 body | 无 |
| S1 sunken | `BgSidebar` | `#F9FAFB` | `#0B0B10` | 侧栏、窗口顶带、设置导航 | 右侧 1px `BorderL2` |
| S2 raised | `BgRaised` | `#FFFFFF` | `#1B1B26` | composer、工具卡、审批卡、对话框 | 1px 边框；composer 额外 `ShadowComposer`；浮层 `ShadowOverlay` |
| S3 raised-soft | `BgBase`+`BorderL2`+`RadiusMD` | 白 + 10% ink 边 | `#111118` + 12% 白边 | 团队/任务/子代理行、产物/文件条目 | 只描边，无阴影 |
| S4 overlay | `BgRaised`+`BorderL2`+`RadiusLG` | 白 | `#1B1B26` | modal/palette/菜单/toast/抽屉 | `ShadowOverlay` + scrim |

**规则**：S2 内最多再嵌一层 S3，禁止卡中卡套弹窗；抽屉/浮层必须有 elevation（无阴影的“假浮层”= AP-24）；暗色导航比页面浅 = AP-24。

### 5.2 状态语义矩阵

| 状态 | 文本色 | 填充/边框/图标 | 语义 |
|---|---|---|---|
| success | `SuccessText` | `Success` | 操作/工具成功 |
| warning / running | `WarningText` | `Warning` | 进行中、需注意（**不表示失败**） |
| danger / error | `DangerText` | `Danger` | 失败、不可逆、审批拒绝 |
| cancelled | `LabelTertiary` | `LabelTertiary` | 用户取消（中性，不画成失败） |
| unknown | `LabelTertiary` | `LabelTertiary` | 未知/旧后端回退 |

**语义不可降级**：error 不得用 warning 或普通文本；审批动词不得中性化（「允许/拒绝/提交」不写成「确定/取消」）；`cancelled` 不得画成红色失败。

### 5.3 字号（type scale，`design.TypeRole`）

| Role | size/line/weight | 用途 |
|---|---|---|
| `TypeXL` | 24/32/400 | 欢迎页/极少用 |
| `TypeL` | 20/28/500 | 页面标题、对话框标题（配合 600 权重） |
| `TypeM` | 16/24/400 | 小节标题 |
| `TypeBody` | 14/22/400 | 正文、消息；Markdown 阅读列行高 1.55 比例（`uikit.ChatMarkdown`，14→21.7） |
| `TypeS` | 14/24/400 | 行内 label |
| `TypeXS` | 13/20/400 | 说明、列表次文本 |
| `TypeXXS` | 12/18/400 | 元数据 |
| `TypeXXXS` | 11/14/400 | **仅**微标签：分组小标签、kbd 提示、终端 gutter；**禁止成句文本** |

**字号使用规则（v2）**：

| Role | 允许 | 禁止 |
|---|---|---|
| `TypeXL 24` | 仅品牌/落地页 hero | chrome、设置、检查器 |
| `TypeL 20/600` | 设置页/对话框 H1 | 列表行、卡头 |
| `TypeM 16/600` | 分节标题、窗口顶带屏幕标题 | 正文 |
| `TypeBody 14` | 会话正文、问题正文、表单成句说明 | 元数据、按钮 |
| `TypeS 14/24` | 列表行标题、菜单项、按钮标签、工具名 | 长段正文 |
| `TypeXS 13` | 次要正文、表单标签、属性值、检查器正文 | 时间/计数（用 XXS） |
| `TypeXXS 12` | 元数据行、hint、状态徽章文字、≤2 行的活动/回执摘要 | 长段正文 |
| `TypeXXXS 11` | 微标签/kbd/gutter | 成句文本、按钮文字、诊断值 |

**规则**：字号只走 `t.Font(role)`，它会同时设置字体栈、行高、字重并随 `TextScale` 缩放；裸 `FontSize` = AP-03。Mono 文本用 `t.MonoText(role)`。**一屏最多 4 种字号**；权重只用 `design.WeightRegular/Medium/Semibold`（400/500/600），禁止裸 `FontWeight(600)`（DS-03）；元数据永不用 accent。

### 5.4 间距 / 圆角 / 阴影 / 布局度量

| 类别 | 取值 | 规则 |
|---|---|---|
| 间距 | `Space0..8` = 0/4/8/12/16/20/24/32 | 只用这些；容器间距用 `Gap(design.SpaceN)`，内外边距同理；显式零用 `Space0`，禁止裸 0（DS-03） |
| 控件高度 | `RowCompactHeight 32` / `ControlHeight 36` / `PageActionHeight 40` / `RowDoubleHeight 44`；紧凑 chip 用 `ControlCompactHeight 28` | 全部 4 的倍数；**禁止 38**；页面唯一 primary 用 40，其余控件 36，列表行 36/44，树/菜单 32 |
| 圆角 | `RadiusXS 4` / `SM 8` / `MD 10` / `LG 16` / `XL 20` / `Max 999` | XS 仅内嵌小方块；SM 行/控制 chip/palette；MD 原生控件/S3/菜单；LG S2 卡/对话框/toast/团队浮层；**XL 仅 composer**；Max 状态药丸。子圆角 ≤ 父圆角；同屏最多 3 档；禁止 chip 用 LG |
| 阴影 | `ShadowComposer`（composer）、`ShadowOverlay`（浮层/抽屉/toast，深色更重） | 卡片默认无阴影；浮层才用 elevation；禁止手写 `ui.RGBA` |
| 布局 | `SidebarWidth 280` / `SidebarRailWidth 64` / `InspectorWidth 320` / `InspectorMinWidth 260` / `ContentMinWidth 520` / `SettingsWidth 820` / `SettingsHeight 760` / `TranscriptWidth 960` / `DialogWidthXS 360` / `DialogWidthS 460` / `DialogWidthCompact 520` / `DialogWidthM 560` / `DialogWidthL 620` / `DialogWidthXL 680` / `DialogBodyMax 480` / `OverlayWidthS 380` | 壳层/对话框/浮层/阅读列尺寸只能引这些 token（Phase 1b 已补齐并替换存量） |
| 图标尺寸 | `IconSizeS/M/L = 16/20/24`（已落地） | 图标按钮统一走 `uikit.IconButton`（Phase 2）；其他图标尺寸优先用 token，禁止 12–40 裸值继续新增 |

### 5.5 使用规则与检查

1. 颜色只从 `internal/brand`（唯一 hex）→ `design.Tokens` → 屏幕。
2. 所有文字走 `design.Of(c)` 的 `Font/Text/TextMuted`；`TextScale` 必须生效（设计层已有测试，屏幕不许绕过）。
3. 间距/圆角/尺寸只用上表 token；新增尺寸先加 token 再使用。
4. 阴影只用两个预设；浮层/菜单/抽屉用 `ShadowOverlay`。
5. **强调色预算**：每屏 ≤2 个 Accent 填充块、≤1 个 primary 按钮、≤2 个红色元素；页面主操作只有两个落点（页头右 / 固定页脚右下），分节动作走 `SectionAction`。
6. **状态徽章**：只用 `uikit.StatusBadge`（success 8%/warning/danger 12% tint + `*Text` AA 文字 + 必填标签）；禁止 `t.Badge()+TextColor(t.DangerText)` 这类中性底 + 状态字拼法（AP-21）。
7. 对应检查：DS-01/DS-02/DS-03/DS-11..DS-16（见 [DESIGN-LINT.md](DESIGN-LINT.md)）。

---

## 6. 预设与入口组件清单

### 6.1 design 预设（`internal/design/components.go`）

| 预设 | 外观 | 用法 | 禁止 |
|---|---|---|---|
| `SectionLabel()` | TypeXXXS + LabelTertiary | 侧栏/设置导航小节标题 | 当正文 |
| `Panel()` | BgRaised + BorderL2 + RadiusLG | 独立对象卡（工具卡、Agent 详情、任务详情、composer） | 给每个 section 套卡片 |
| `ShadowComposer()` / `Composer()` | 卡片 + 软阴影 + RadiusXL | 两个 composer 卡片 | 其他浮层 |
| `PrimaryButton()` | h36/PaddingX/Radius/weight，**套 `ui.PrimaryButton`** | 主操作（保存/发送/批准/连接） | 套 `ui.Button` 或覆写背景 |
| `PrimaryButtonLarge()` | h40，套 `ui.PrimaryButton` | 页面唯一主操作 / 空态 hero | 对话框/列表内 |
| `DisabledPrimary()` | h36 + BgStrong + LabelTertiary，套 `ui.Button` | 禁用态主操作（发送/运行/保存未就绪） | 可用的主操作 |
| `SecondaryButton()` | h36/PaddingX/Radius/weight，套 `ui.Button` | 次操作/取消/关闭/分节动作 | 主操作 |
| `DangerButton()` | danger 文字，套 `ui.Button` | 删除/拒绝/停止 | 默认/Enter 焦点 |
| `SelectedRow()` | BgSelected(12%) + RadiusSM | 持久导航当前项（配合 2px Accent 左条） | 瞬态选择/满蓝填充 |
| `Badge()` | 胶囊 + BgHover + LabelSecondary | 状态/类型/计数（非语义状态） | 普通元数据、全宽胶囊、状态语义 |
| `ShadowOverlay()` | 浮层阴影（深色 alpha 0.32） | 菜单/浮层/palette/toast/抽屉 | 卡片 |

### 6.2 uikit 共享组合（`internal/uikit`）

| 分类 | API | 用途 |
|---|---|---|
| 页面结构 | `PageHeader` / `Section` / `PageHeaderAction` / `SectionAction` | 页头/小节/动作位置（heading role 已内置） |
| 空态/错误 | `EmptyState` / `EmptyStateBlock` / `uikit.EmptyStateHero` / `ErrorLine` | 行内空态 / 带下一步动作 / hero（图标+标题+primary）/ RoleAlert 错误行 |
| 只读展示 | `KeyValueRows` / `Bullet` | 属性表（值可选中）/ 项目符号 |
| 列表行强调 | `OnAccentBadge` | List 选中 accent 行上的可读徽章（AccentText 填充 + accent 文字；status label 语义保留） |
| 对话框 | `DialogFooter` + `DialogAction` / `uikit.DialogHeader` / `uikit.ConfirmDanger` | 唯一动作条（危险→次要→主操作最右）；`DialogHeader` 统一图标/标题/副标题/关闭；`ConfirmDanger` 是不可逆动作的唯一入口（取消 Default、危险非 Default）；`Default` 仅无文本输入时用；审批无 Default |
| 工具行 | `ToolRow` + `ToolRowOptions` / `ToolPanelSectionView` / `ToolPreview` / `ToolDisplayName` / `ToolIcon` | 工具调用统一解剖（图标/名/状态/预览/详情段/团队跳转）；状态 gutter 是**固定宽度左边框**（active 染色、完成态透明），只换色不 reflow |
| 输入辅助 | `EnterTo` | 单行输入 Enter→动作（有文本输入时替代 Default） |
| 文案/locale | `T` / `FormatDateTime` / `FormatClock` / `Language` / `FormatCompactCount` / `FormatShortcut` | 本地化与格式化；`FormatShortcut` 统一键位记法（平台化 `Cmd/Ctrl + N`） |
| 检查器文档 | `uikit.MarkdownDoc(t, size)` | 320px 检查器内的文件/产物预览（13px 正文，H1≈15，不再用默认 22px H1） |
| 阅读区 Markdown | `uikit.ChatMarkdown` | 主阅读列（会话正文）的 Markdown 主题：14px 正文 + 1.55 比例行高；H1 17–18 / H2≤16 / H3≤15，均在屏幕标题之下；显式收紧标题上下距；行内代码用 `BgStrong` 底 + 等宽；引用/分隔线用 `BorderL2`；表格头 `BgStrong`；链接 Accent + 500 字重；暗色同构 |
| 代码围栏 | `uikit.MarkdownCodeBlock` | 会话/推理/检查器 Markdown 的统一代码块：填充 `BgStrong`、无边框、`RadiusSM`；只有语言真实命名时才显示语言标签（不再把 `text` 当成标题）；官方 Copy 按钮保留复制反馈；流式/高亮/横向滚动沿用官方 `CodeBlockView` |
| 附件 | `SplitAttachmentPaths` / `InvalidAttachmentPath` / `ResolveAttachmentPaths` | 手输附件路径 |
| accent 行徽章 | `uikit.OnAccentBadge` | **仅**在 Accent 填充的瞬态行（palette/菜单选中项）使用；持久导航改为 tint + 普通 `uikit.Badge`（AP-19） |

### 6.3 Agent 入口组件（目标；规格见 [AGENT-SURFACES.md](AGENT-SURFACES.md)）

| 入口 | 状态 | 说明 |
|---|---|---|
| `uikit.SessionThread` | **已有**（Phase 3a） | transcript + 流式跟随 + 空态 + 回到最新 + 错误带；消息体 Row slot |
| `uikit.ReasoningTrace` | **已有**（Phase 3a） | 折叠推理、streaming 展开/结束后收起；muted 主题在 uikit |
| `uikit.ToolRow` | **已有** | 工具调用唯一入口；需收编 session 的 `buildToolCard` 卡头 |
| `uikit.ToolCallCard` | **已有**（Phase 2c） | 决策/轨迹/plan/demo 统一完整卡；普通工具调用仍走 `uikit.ToolRow`；无 body 时 Normal 退化为单行 |
| `uikit.ApprovalDialog` | **已有**（Phase 3b；Inline 2026-11-10） | `Inline : bool`（产品默认 true）：输入框上方 V3 内联卡（警示横幅/描边）；`false` 保留 Modal 兼容路径；无 Default footer；Esc 由 session 审批优先路径 deny 最早一条；模块只留队列/自动切回会话 |
| `uikit.PlanReviewDialog` | **已有**（Phase 3b） | Esc 不取消 + 一次性 RoleAlert 提示；空反馈不批准；失败 Esc 可关 |
| `uikit.AskDialog` | **已有**（Phase 3b） | 等权选项 + 「其他…」+ 取消 Default；自由文本空 Disabled |
| 会话 Agent Run Band | **已有（2026-11-11 签名表面）** | `session.buildAgentRunBand`：标题后一行原生入口按钮（不套第二层容器，避免官方按钮描边+容器描边的双重外观；仅以间距分组），收拢运行中/智能体团队/子智能体/后台任务/待审批；团队与子代理为**关键功能，零值也常显**（可发现）但零值不显示数字 0，后台任务/审批只在有内容时出现；入口仍是 `ui.PopoverBase` 浮层；子代理/成员在主栏打开并以面包屑返回；右栏不再有团队/任务 pane |
| `uikit.ComposerShell` | **已有**（Phase 3c） | 两处 composer 共用卡片/输入/宽度/发送位/键语义；toolbar/提交保留为 slot |
| `uikit.RunStatus` | **已有**（Phase 3c） | 生成中/不伪造进度、队列、停止、重试、用量折叠 |
| `uikit.ErrorPanel` | 暂缓（无调用点） | 行内错误用 `uikit.ErrorLine`；会话级错误带在 SessionThread；需要大块错误面板时再建 |
| `uikit.StatusText` / `StatusBadge` | **已有**（Phase 2b） | 13 个重复状态函数已收敛；域词表（Tool/Run/Task）保留，`active` 不再当 running |
| `uikit.IconButton` | **已有**（Phase 2a） | 12 处纯图标 `ButtonBase` 已迁移；统一 hover/pressed/focus/Label、≥24 命中区、IconSize token；`IconButtonOptions.InkColor`（零值=默认前景）供 on-accent 行使用 |
| `design.TypeMono` / `t.MonoText(role)` | **已有**（Phase 1b） | 7 处裸 mono `FontSize` 已迁移，随 TextScale |

> 新建顺序（按收益/风险）：`IconButton` → `StatusText/StatusBadge` → `ToolRow` 收编 → `TypeMono` → `ShadowOverlay` 替换 → 对话框宽度 token → 其余表面入口。

### 6.4 入口组件的锁定/开放边界

- **锁定**：语义（组件代表什么状态）、状态集、默认动作、焦点/Esc 规则、a11y role/播报、外层布局骨架、token 取用。
- **开放**：内容 slot（`func()`）、标题/文案、数据来源；业务模块不得重排动作顺序或覆写状态色。
- **唯一出口**：场景内只允许调用入口函数；入口内部才允许直接编排官方件。

---

## 7. 自绘白名单与偏离流程

### 7.1 现有合法自绘（保留需理由；新代码不得模仿）

| 位置 | 内容 | 理由 |
|---|---|---|
| 命令面板行 / slash@ 富行 / 侧栏富行与行内 ⋯ / terminal copy success 态（8 处富行/success 态站点，按区域登记 registry allowlist） | 富行按钮、on-accent 行内动作、success 确认态 | 图标按钮已在 Phase 2a 收敛到 `uikit.IconButton`；上述站点受官方 List 行语义 / 行状态前景 / 状态语义限制，保留自绘并登记 registry allowlist（DESIGN.md §7.1） |
| composer 动作行 | Plain Row + 3 个 PopoverMenuButton | `ui.Toolbar` 溢出菜单会吞掉发送键（已注释） |
| slash/@ 建议菜单 | 富行建议面板（图标/标题/说明） | 官方 Autocomplete 仅纯文本；已按官方 suggestions a11y 模式实现 |
| 命令面板行按钮 | 单次点击执行 + RoleButton 行 | 官方 List 行语义是 listitem，不提供按钮行 |
| 消息元信息行（时间 · 紧凑用量 · ⋯） | 消息级元信息 + 单一动作菜单 | 官方列表行不提供行内元信息区；只用官方 Text/Button/Menu 组合（不新增自绘控件），时间与用量常显、动作收进一个 ⋯ |
| 会话「回到最新」浮层 | List 之外的 affordance | 行为用官方 `ScrollToEnd`；浮层必要 |
| toast 品牌外观 | `ToastViewportBase` parts 上设色/圆角/阴影 | 框架 toast 的换肤方式；状态/计时/读屏仍归框架 |
| `uikit.ComposerShell` 的 `noEdge=0` | 旧实现度量以包内命名常量承接（非方法实参字面量，designlint 不计），待 token 化 | design 尚无 Space0/部分控件高度 token；后续 token 任务收敛 |
| tabs 下划线、设置页少量 chrome | 产品 chrome | 非标准控件；图标按钮部分已由 IconButton 覆盖（rail hover 已修） |
| 文件/目录选择器 seam | 纯测试注入点 | 无应用状态，非 UI 自绘 |

### 7.2 新增自绘/新组件的偏离流程

1. **先查**：官方 v0.8.3 是否有组件/`*Base`？本项目是否有预设/入口？→ 有则禁止自绘。
2. **写理由**：在代码注释里写清「官方为什么不够」（可访问性/交互语义/布局约束），引用官方文档路径。
3. **登记**：同 PR 更新本文 §7.1、[UI-COMPONENT-MIGRATION.md](UI-COMPONENT-MIGRATION.md) §3 与 [DESIGN-LINT.md](DESIGN-LINT.md) 的 `design-allowlist`（lint 白名单）。
4. **补测试**：帧测试覆盖键盘/焦点/状态/role；新视觉有快照。
5. **评审**：reviewer 按 §8.5 检查；未登记的自绘 = DS-06 违规。

### 7.3 偏离记录模板

```go
// design:allowed AP-04 owner=<team/agent> until=<yyyy-mm-dd>
// Why not the official widget: <官方文档/API 为什么不够>
// What official semantics are preserved: <键盘/焦点/a11y/状态；或说明替代>
```

### 7.4 官方不存在的 API（禁止发明）

| 容易误用 | v0.8.3 事实 | 正确做法 |
|---|---|---|
| `ui.DialogFooter` | 不存在（本仓库 `uikit.DialogFooter` 是应用层封装） | 用 `uikit.DialogFooter`；官方 footer 写法是 `Row + Justify(End)` |
| `Button.Default` / `Element.Default` | 不存在 | Modal 里用 `uikit.DialogFooter` 的 `Default`（仅无输入）或 `EnterTo`/`OnTextKey`/`OverlayShortcut` |
| `ui.Markdown` | 不存在；在 `ui/markdown` 子包 | `markdown.View/Stream` |
| `ui.DatePicker` | 不存在 | `DateInput` / `Calendar` |
| `ui.CommandPalette` / `SearchDialog` | 不存在 | `Overlay/Modal + SearchField + List` |
| `ui.Dialog` | 不存在 | `Modal`（有外观）/ `DialogBase`（无外观） |
| `ui.Section` / `PageHeader` | 不存在 | 本仓库 `uikit.Section/PageHeader` |

---

## 8. 验证与维护

### 8.1 命令（改 UI 后按顺序跑）

```bash
export PATH=/usr/local/go/bin:$PATH
# 本工作区沙箱:模块缓存可写在 .cache/gopath（普通环境可省略 GOPATH/GOCACHE 覆盖）
export GOPATH=/data/mygo/.cache/gopath
export GOCACHE=/data/mygo/.cache/go-build
# 测试临时目录必须在仓库外（同 AGENTS.md）：/tmp/zeokeel-tmp
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp
mkdir -p /tmp/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off

gofmt -l internal cmd                      # 应为空
go build ./...
go vet ./...
go test ./... -count=1                     # 全量（慢）；内环可先跑受影响包
go test ./internal/ui/... ./internal/uiroot ./internal/design ./internal/uikit -count=1
go run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...
bash scripts/design-check.sh
go tool mygo vet .
go run ./internal/devtools/ui-snapshot .cache/ui-conformance   # 生成 PNG 并人工看
```

验证分层：静态规则（lint）→ 帧测试（`ui.NewTester`）→ 快照（ui-snapshot）→ 端到端（ui-drive）。

### 8.2 规则 ID 索引

| 规则 | 名称 | 文档 |
|---|---|---|
| DS-01 | 颜色字面量禁止 | [DESIGN-LINT.md](DESIGN-LINT.md) §3 |
| DS-02 | 主题/状态色覆写、状态语义 | 同上 |
| DS-03 | 裸数值度量 | 同上 |
| DS-04 | Agent surface 绕过入口 | 同上 |
| DS-05 | 默认组件多余样式覆写 | 同上 |
| DS-06 | 未登记自绘/标准交互绕过 | 同上 |
| DS-07 | 图标来源/尺寸 | 同上 |
| DS-08 | 文档/registry 同步 + 虚构 API | 同上 |
| DS-09 | 状态映射重复/语义降级 | 同上 |
| DS-10 | 对话框语义/动作顺序/Esc | 同上 |
| DS-11 | 危险确认安全默认（ConfirmDanger、Enter=取消、非危险才用 AlertDialog） | 帧测试 `uikit.TestConfirmDangerDefaultsToCancel` + 迁移清单 |
| DS-12 | 选择态两级（持久 tint+2px 条 / 瞬态 accent 填充） | 快照 + 帧测试（`uiroot.TestInspectorDrawerReservesContentWidth` 同类） |
| DS-13 | 强调色预算（≤2 accent / ≤1 primary / ≤2 danger） | 快照评审 + 帧测试 |
| DS-14 | 状态徽章只用 `uikit.StatusBadge`（禁中性底+状态字） | grep + 评审 |
| DS-15 | 字号/高度纪律（11px 禁成句；高度 32/36/40/44，禁 38） | 评审 + designlint 后续规则 |
| DS-16 | 表面 ramp / 抽屉 elevation（暗色导航下沉） | 像素采样 + 快照 |

### 8.3 文档同步义务

**任何 UI 改动必须同 PR 更新**：

1. 新增组件/预设/入口 → §1.2/§6 + 附录 A registry + [AGENT-SURFACES.md](AGENT-SURFACES.md)（若是 surface）。
2. 新增 Agent 场景 → §2.1 + registry scenarios + 对应入口迁移行。
3. 新增反模式/规则 → §3 + [DESIGN-LINT.md](DESIGN-LINT.md) 规则 ID + 检查实现（或说明只能人工评审）。
4. 新增自绘 → §7.1 + [UI-COMPONENT-MIGRATION.md](UI-COMPONENT-MIGRATION.md) + lint 白名单。
5. 改动官方用法 → [UI-CONFORMANCE.md](UI-CONFORMANCE.md) §1/§1.1。

**已知文档漂移（勿作依据，修复后删除本节条目）**：

- [DESIGN-REVIEW.md](DESIGN-REVIEW.md) 是 2026-11-08 / mygo v0.7.4 的历史报告，其中 P0/P1 多数已修复（硬编码色、uikit 未接 design、Toggle 当开关、toast 自绘等）。现状以本文件 + 代码 + 测试为准。
- [UI-FIX-STATUS-v0.8.3.md](UI-FIX-STATUS-v0.8.3.md) §1 的「Approval DialogFooter（Deny 默认焦点/Enter=拒绝）」与现状不符：现行审批**没有默认动作**（真实事故修订，见 `internal/ui/approval` 注释与 `TestApprovalFooterHasNoDefaultAction`）；transcript 也早已是 `ui.List` 虚拟化，不是 200 条有界窗口。

### 8.4 mygo 升级流程（v0.8.x → 新版本）

1. 读新版本 mygo 模块内的 `docs/ui/migration.md` 与 release notes；`go get github.com/egoist/mygo@<version>`。
2. `go tool mygo migrate-ui .` 预览 → `-write` 应用（需 Go 1.24+ tool 指令；当前 go.mod 未声明，见 DESIGN-LINT §5）。
3. 逐项复核迁移改动：stateful 泛型 parts、派生值回写（`Changed/Submitted`）、key 迁移到 `Context.Key`。
4. 重跑 §8.1 全链；更新 [UI-CONFORMANCE.md](UI-CONFORMANCE.md) 的版本号与 §1 表。
5. 重核本文 §1.2 的组件目录与 §7.4 的缺口；更新附录 A `version` 字段。
6. 若官方新增了 `DialogFooter`/`Default` 等能力，按「官方优先」替换本仓库的对应封装（保留入口 API 不变）。

### 8.5 PR / 自检 checklist

- [ ] 场景表已查（§2），没有绕过入口组件。
- [ ] 没有新增颜色字面量/裸数值度量/裸字号（DS-01/02/03）。
- [ ] 官方件没有被覆写背景/状态色（AP-02）。
- [ ] 集合用 `List/Table/Outline/GridView`；没有 `Scroll`+循环。
- [ ] 对话框动作顺序/Default/Esc 符合 §3 AP-12/AP-24 与 §6.2；危险确认走 `uikit.ConfirmDanger`，`AlertDialog` 只用于非危险确认。
- [ ] 强调色预算（≤2 accent/≤1 primary/≤2 danger）与选择态两级（持久 tint+2px 条）符合 §5.1/§5.5（DS-12/DS-13）。
- [ ] 状态徽章只用 `uikit.StatusBadge`；没有中性底+状态字（DS-14）。
- [ ] 字号 ≤4 档/屏，11px 只做微标签；控件高度 32/36/40/44（DS-15）。
- [ ] 暗色 surface ramp 方向正确（导航比页面深）；抽屉/浮层有 `ShadowOverlay`（DS-16）。
- [ ] 页面主操作只有两落点，每页唯一 primary；管理列表无 r16 卡墙。
- [ ] 自绘有理由、已登记白名单与迁移清单（§7）。
- [ ] a11y：无文字控件有 `Label`；错误 `RoleAlert`；异步 `c.Announce`。
- [ ] 帧测试覆盖交互；视觉改动有快照并人工看过。
- [ ] 文档同步（§8.3）与 registry（附录 A）已更新。
- [ ] `gofmt`/`build`/`vet`/`test` 通过。

---

## 附录 A：机器可读契约（registry schema + 样例）

> 单一机器真源：`internal/design/registry.json`（Phase 1a 落地；离线环境无法引入 YAML 解析器，schema 字段与本文原 YAML 设计一致，载体改为 JSON，`go:embed` + `encoding/json`）。

```json
{
  "version": 1,
  "source": { "mygo": "v0.8.3", "entries": "internal/uikit", "design_doc": "docs/DESIGN.md", "lint_doc": "docs/DESIGN-LINT.md" },
  "components": [
    { "id": "agent.tool-row", "entry": "uikit.ToolRow", "package": "internal/uikit",
      "states": ["running", "success", "error", "cancelled", "unknown"],
      "allowed": ["uikit.ToolRow", "uikit.ToolPanelSectionView", "uikit.ToolPreview"],
      "forbidden": ["raw_text_body", "per_message_card", "second_tool_row_variant"] },
    { "id": "agent.approval", "entry": "uikit.ApprovalDialog", "package": "internal/uikit",
      "status": "inline card (V3) by default in the session composer dock; modal variant kept for embedders",
      "rules": ["no-default-action", "escape-denies", "danger-first-primary-last", "inline-above-composer", "warning-banner-contrast"] }
  ],
  "scenarios": [
    { "id": "agent.reasoning", "entry": "uikit.ReasoningTrace",
      "forbid": ["Card", "always_expanded", "body_inline"],
      "requires": ["ui.CollapsibleBase", "markdown.View", "markdown.Stream", "Spinner"] }
  ],
  "anti_patterns": [
    { "id": "AP-01", "rule": "DS-01", "message": "禁止颜色字面量；用 design.Tokens / c.Theme()" }
  ],
  "allowlist": [
    { "id": "chrome.icon-buttons", "paths": ["internal/uiroot"], "rules": ["DS-06"],
      "what": "壳层图标按钮（rail/展开折叠/设置/关闭）", "owner": "uiroot",
      "reason": "mygo v0.8.3 无官方 IconButton；待 uikit.IconButton 收敛",
      "until": "2027-06-30" }
  ]
}
```

**同步规则**：`go test ./internal/design/...` 的 conformance 测试校验 registry schema、allowlist 有效性（owner/reason/until）、虚构 API；registry ↔ 本文表格的逐行镜像比对仍待 Phase 3（DS-08）。**新增/迁移入口组件时必须同 PR 更新 registry 与本文 §6.3。**

## 附录 B：官方组件文档索引（mygo v0.8.3）

按 §1.2 类别可直接打开：`accessibility` `accordion` `alert-dialog` `autocomplete` `avatar` `badge` `breadcrumbs` `button` `calendar` `chart` `checkbox` `code-editor` `collapsible` `color-picker` `combobox` `context-menu` `custom-widgets` `date-input` `decimal-input` `dialog` `diff-viewer` `drag-and-drop` `drawing` `editable-text` `find-bar` `form` `grid` `grid-view` `history-buttons` `icon` `image` `input` `inspector` `layout` `link` `list` `markdown` `menu-button` `meter` `number-input` `outline` `overlays` `popover` `progress` `radio` `range-slider` `rating` `rich-text-editor` `right-to-left` `scroll` `search-field` `segmented` `select` `shaders` `sidebar` `slider` `spinner` `split` `stepper` `styling` `switch` `table` `tabs` `testing` `text` `text-input` `time-input` `toast` `toggle` `token-field` `toolbar` `tooltip` `transitions` `tree` `video` `views` `web-view` `windows`。

指南类：`README` `accessibility` `custom-widgets` `layout` `migration` `motion` `navigation` `overlays` `performance` `rendering` `styling` `testing` `transitions` `transforms` `windows`。

## 附录 C：现状基线（2026-11-09，来自代码实测）

| 指标 | 数值 |
|---|---|
| UI 模块 / 壳层代码 | 14 个模块（`internal/ui` 非测试 30,698 行）；`internal/uiroot` 3,430 行 |
| 设计层 | `internal/brand` + `internal/design` + `internal/uikit`（共享组合，见 §6.2）+ `internal/icons`（33 SVG / 33 accessor；2026-11-10 修复轮新增 Home） |
| 官方件用量 | `ui.List` 31、`ui.Scroll` 13、`ui.Modal` 24、`ui.AlertDialog` 15、`uikit.DialogFooter` 33、`PopoverMenuButton` 21、`Collapsible` 18、`SearchField` 10 |
| 按钮 | `ui.PrimaryButton` 22 / `ui.Button` 109；`ui.Toggle` 3 / `ui.Switch` 7 |
| 状态 | `c.Key` 48；重复状态映射函数 0（Phase 2b）；工具卡渲染路径 2（Phase 2c） |
| 违规存量 | 颜色 0、裸 FontSize 字面量 0（Phase 2b 后；唯一例外是聊天字号动态值）、裸数值度量 51（grep）、FontWeight designlint warn、ButtonBase 10、对话框宽度 0；designlint 实测 156 条 violations（全部 baseline/allowlist 收口；baseline 已刷新为 156 条） |
| 测试 | 全仓 2,260 个 `func Test`；`internal/regression` 73 文件 / 288 测试；ui+uiroot 203 文件 |
| 验证资产 | `scripts/verify.sh`（约 300 行，含 designlint/design-check/mygo-vet）、`scripts/design-check.sh`、`ui-snapshot`、`ui-drive`（38 个步骤函数）、快照 PNG |

> 复算命令见 [DESIGN-LINT.md](DESIGN-LINT.md) 附录 A；数字随工作区演进，以命令输出为准。

## 附录 D：决策记录

| # | 决策 | 理由 | 影响 |
|---|---|---|---|
| D1 | Agent 入口组件放 `internal/uikit`，不新建 `internal/agentui` | 模块边界允许 import uikit；避免第二套共享层与 import 白名单变更；先按文件名分组（`session.go`/`toolcall.go`/`approval.go`/`status.go`） | 若入口膨胀到 >10 个再拆子包 |
| D2 | registry 为机器可读单一真源（`internal/design/registry.json`），DESIGN.md 是校验镜像 | 防止「文档一套、lint 一套」漂移；离线环境用 JSON 载体，schema 不变 | 已落地（Phase 1a）；逐行镜像待 DS-08 完整实现 |
| D3 | lint 先做独立命令 `internal/devtools/designlint`（v1 标准库 AST，无 x/tools），不塞进 mygo vet | 设计规则是应用层契约；离线环境无 type-aware 依赖；独立命令便于 `--diff`/`--json`/hook | 已落地（Phase 1a）；联网后再评估 `go/analysis` 升级 |
| D4 | 存量治理用 baseline + 时间盒清零；新代码零容忍 | 106+ 处存量一次清零不现实；baseline 只降不升 | 豁免需 owner + 到期日 |
| D5 | 非危险简单确认用 `ui.AlertDialog`；危险/结构化用 `ui.Modal`+`uikit.DialogFooter`（危险动作不得 Default，安全取消可 Default）；审批用 `ui.Modal`+footer 且无 Default | 保留原生 AlertDialog 的 Enter 语义；审批两边都可敏感，必须显式选择；单一危险确认让安全动作承接 Enter | 两类对话框并存，必须按内容复杂度/敏感度选择 |
| D6 | 新增 token 优先于字面量：`IconSize*`、mono type role、控件高度/对话框宽度逐步收口 | 现状 106 处裸度量是最大存量；token 化后才能 lint | 先加 token 再做 lint 规则 |
| D7 | `uikit.ToolRow` 定为工具调用唯一入口 | 现有 4 条渲染路径导致解剖/状态漂移 | 决策类工具卡归 `ToolCallCard`，由 ToolRow 组合 |
