# ZeoKeel UI/UX 设计审查报告

> 审查日期：2026-11-08 · HEAD `e977b12` · 依据：`design-guide` + `mygo-maintenance` 两个技能
> 审查性质：只读审查，未修改任何产品代码；本文件为新增报告。
> **修复执行结果见 [docs/DESIGN-FIX-STATUS.md](DESIGN-FIX-STATUS.md)**（Round-1 → Round-2 全部修复、验证证据与遗留 backlog）。
> 证据：最新全量截图（1280×800 浅色 65 张、1024×720 窄窗 67 张、暗色 3 张）见
> `.cache/design-review/shots/`、`.cache/design-review/narrow/`、`.cache/design-review/crops/`；
> 四份分模块详细报告见 `.cache/design-review/reports/{settings,shell,session,conformance}-audit.md`。

---

## 0. 结论摘要

**一句话：骨架是对的，设计语言和交互规范没有收敛。** 原生控件选型、设计 token 的颜色层、
会话/设置/右栏的功能骨架整体可用，也能看出有意识地使用了 `internal/design`、`internal/brand`、
`internal/icons`、`internal/uikit` 和 mygo v0.7.4 的原生控件。问题集中在三处——

1. **设计语言没有单一事实源**：`internal/design` 与 `internal/uikit` 没有被真正用起来，模块各自
   手写页头/空态/卡片/按钮/状态色，形成"两套设计系统 + 至少 5 套按钮外观 + 4 套页头 + 4 套空态
   + 6 套错误行 + 7 套状态色映射"；主操作按钮被设计系统反向拉平（`design.PrimaryButton` 实际是
   浅灰中性按钮），界面里到处找不到"主操作"。
2. **交互不完整**：核心入口（命令面板、斜杠命令、@ 引用）没有键盘选择/Enter/Esc；流式回复
   不跟随滚动；品牌化按钮把 mygo 原生的 hover/pressed 反馈覆盖没了；对话框按钮顺序反桌面惯例、
   危险操作缺确认；窄窗口下发送按钮和会话标题被裁掉。
3. **布局容器失效（唯一 P0）**：设置弹窗固定 760×640 且内容列没有滚动容器，默认 1280×800 下
   长设置页的「应用/保存」主按钮直接画到弹窗外面；同一壳层在 1024×720 下会话发送按钮不可见。

**严重度（去重后）**：P0 = 1，P1 = 14 组，P2 ≈ 20 组，P3 若干。四份分报告共记录 226 条原始
finding（含重复），本报告按根因合并。**已核实无需整改的正面项**：无 emoji/Unicode 图标滥用；
颜色 token 覆盖率高、暗色主题基本一致；UI 生命周期纪律总体良好（无跨帧存 `ui.Element`、
无 `*ui.Context` 逃出 build、10 个 goroutine 全部经 `UIRuntime.Post` 回 UI 线程）；
tooltip 实际有 38 处（不是缺失）；89 处 badge 中 73 处语义合法；`ui.Sidebar`/`TabsBase`/
`Modal`/`AlertDialog`/`PopoverMenuButton`/`Collapsible`/`SearchField`/`markdown` 用对了。

---

## 1. 审查方法与证据

- 装载两个技能并逐条对照：`design-guide`（含 MyGo/ZeoKeel adaptation）、`mygo-maintenance`。
- 以当前 HEAD 运行 `go run ./internal/devtools/ui-snapshot` 生成 1280×800 全量界面截图；另以
  1024×720（产品最小窗口 900×600，见 `internal/host/host.go:60-64`）与暗色主题生成探针截图，
  用于验证响应式和暗色（探针代码为临时修改，已 `git checkout` 还原）。
- 4 个并行审查（settings / shell / session / 横向 conformance）逐文件读码 + 逐图审查 + 裁剪放大取证；
  所有结论带 `文件:行号` 与截图路径，可复验。
- 对照 mygo v0.7.4 源码与 `docs/ui/*.md`（`switch/toggle/list/tree/dialog/alert-dialog/toolbar/
  toast/scroll/form/autocomplete/markdown` 等）判断"是否重复实现了原生控件"。
- 限制：无 900×600 截图；无键盘焦点/流式生成态截图；`go tool mygo vet` 在本环境不可用
  （`go: no such tool "mygo"`，`go vet ./...` 通过）。相关结论已标注"待确认"。

---

## 2. 设计语言（用户重点一）

### 2.1 共享层失效：名义上有一套设计系统，实际是两套

`internal/design` 有完整语义 token（颜色/type scale/间距/圆角/组件样式），但**共享层自己没被用**：

- `internal/uikit/uikit.go` 连 `design` 都不 import：`PageHeader` 裸 `FontSize(22)`、`Section`
  裸 15、`Bullet/KeyValueRows/EmptyState` 裸 13（`uikit.go:17-27,30,41,59,70`），绕过
  `TypeMetrics`/`TextScale`；而真实页面也基本绕过 uikit——`PageHeader` 仅 about/welcome 2 处，
  `EmptyState` **0 处**。
- `internal/design/components.go` 里 `NavRow/Row/Composer/Main/Page/Tab/BrandRow` 等组件样式、
  `style.go` 的原子样式、`motion.go` 整个 274 行动效 token，非测试代码 **0 使用**（死代码）。
- 结果：4 套页头（uikit 22/700、settings 20/500、automation/plugins 20/600、home 24/600）、
  4 套空态、6 套错误行、7 套"状态→颜色"映射、2 套自绘页签（`session/view.go:381-411` 与
  `rightpanel/view.go:68-101` 近似复制）、3 份 `secondaryButtonStyle`
  （`settings/settings.go:908-915`、`interaction/interaction.go:535-541`、
  `approval/approval.go:267-273`；前一份高度 32，另两份 36，注释还写 "36 DIP height"）。
- `Tokens.TextScale` 从未被赋非 1 值（`design/tokens.go:65-71`、`style.go:42-79`），整套
  文字缩放机制空转；系统文字偏好只影响会话正文（`session/view.go:855-870`），`restore` 与否
  需要产品决策。

**判断**：这不是"风格偏好"，而是"共享层没有被使用"的结构性问题。技能明确要求
"Check whether the project already has an equivalent / Reuse before reinventing"
（design-guide §17、§20）。修复应优先于一切视觉打磨。

### 2.2 主按钮不主：≥5 套按钮外观，没有一套"主操作"

| # | 体系 | 外观 | 使用面 |
|---|---|---|---|
| 1 | `design.PrimaryButton()`（`components.go:128-134`） | `BgStrong` 浅灰 `#F1F3F5` + 1px 边框 + 深色文字 + h38 | 39 处：保存/应用/批准/允许/添加/新建/发送（home）/运行… |
| 2 | 手搓 accent 填充 | `t.Accent` + `OnPrimary`，h38 / h32 | **全 App 只有 2 个动作**：侧栏「新对话」（`sidebar.go:248-264`）、会话「发送/排队发送」（`session/view.go:1478-1490`） |
| 3 | 原生 `ui.Button` 主题默认 | 中性描边 | 104 处：刷新/筛选/取消/编辑/历史/删除/关闭/更多… |
| 4 | `secondaryButtonStyle` ×3 份 | 白底 + 边框 | 设置/对话框次按钮 |
| 5 | `ui.Toggle` 当开关 | 全宽文字按钮，按下=填充蓝 | 15 处布尔设置（见 §5.1） |

关键事实：`ui.PrimaryButton` 本来是 mygo 的强调色主按钮（mygo `docs/ui/button.md`："for the main
action of a window or a dialog"），但本项目用 `design.Apply(..., t.PrimaryButton())` 把它覆盖成
浅灰中性按钮；而真正用强调色的两处是模块自己手搓的。于是：

- 所有对话框（审批「允许」、计划「批准」、ask 选项、/goal 保存、/feedback 提交…）的主按钮与
  次按钮几乎同权（`p1-approval.png`、`p2-plan.png`）；
- 页面级主操作被弱化：automation「新建任务」与「筛选」同权重（`p2-automation.png`）、
  plugins 整页最显眼的页级动作是「刷新」、设置页「保存/应用」是内容底部的普通灰按钮；
- 同一个"发送"在 Home 是中性灰 h38（`home/composer.go:291-293`），在会话页是 accent h32
  （`session/view.go:1478-1490`）——用户在两个界面看到两种"发送"。

**这个问题的优先级应高于任何单页美化**：主次层级是 design-guide §2/§22 的第一原则，
也是用户"按钮位置是否合理"的核心。

### 2.3 排版尺度失控

- 页头 4-5 套字号：24/20/20/20/22，字重 500/600/700 混用；`uikit` 的 22 不在 type scale 内。
- 裸字号 47 处：uikit 22/15/13、mono 手拼 25 处（10/11/12/13，10 不在 scale）、`frame.go` 18、
  `session/view.go:1802` 10。
- 手写 `FontWeight(600)` 84 处；`SectionLabel` 无字重规则。
- 会话内 Markdown 标题用 mygo 默认阶（H1 = 1.6×正文 ≈ 22.4/700），**比页面标题 20/600 还大**
  （`session/view.go:837-853`；`p1-chat.png`「工作目录已确认」）——内容层级压过页面层级。
- 社区建议：在 design 里定义 `PageTitle/SectionTitle/PanelTitle/MonoFont` 语义样式，
  所有页面/会话 markdown 统一取用，删除裸 `FontSize/FontWeight`。

### 2.4 卡片、边框、圆角、阴影

- **卡片墙**：`settings.go:884-887` 的 `panel()` 注释直接写 "used for every settings block"，
  外观页一屏 5 张卡、通知页 2 卡里塞 5 个全宽按钮、记忆页"卡套卡"；右栏 320px 里每个 pane 的
  每行都是圆角 16 的 Panel（overview 卡、每个工具卡、每条日志卡、每个变更卡、每个任务卡）；
  对话页每条记忆、每个附件 chip、每条工具卡、composer 全都是 Panel——同屏 6+ 个同规格
  边框圆角容器。
- **圆角不统一**：同一壳层同时有 4/8/16/20/999 与主题半径；同一个"composer"在首页是 16 无阴影、
  在会话页是 20+阴影；320px 宽右栏里用 16px 圆角明显过大。
- **阴影无 token**：composer 的 `shadow` 是 `ui.RGBA(0,0,0,0.05)` 手写（`session/view.go:1068`），
  `design.Composer()` 里有同样代码但 0 调用；toast 反而没有阴影/浮层感。
- **背景**：整体分层（BgBase/BgSidebar/BgRaised/BgHover）是清晰的，没有装饰性渐变，
  这部分是正面的；问题只在"每个块都要一个容器"的卡片化习惯。

判断依据：design-guide §3「Not everything is a card」、§4「borders with a purpose」、
§10「consistent radius」、§11「shadows sparingly」、§24「Everything in a container」。

### 2.5 Badge 与图标

- 全仓 89 处 `ui.Badge`：73 处语义合法（运行状态/连接状态/计数/权限/变更状态），
  16 处是把普通元数据做成 pill（版本号、Go 版本、构建时间、快捷键键位、模型名、工具列表、
  当前工作区名），其中 automation 有 3 处 badge 被放进 Column 撑成**全宽胶囊条**
  （`automation/view.go:183-191`，`p2-automation.png`）。首页 hero 的 `v0.1.0` / `go1.27.3`
  （`home.go:166-171`）是典型"开发元数据当 badge"。
- 图标：`internal/icons` 是 26 个 SVG 的双网格（16×16/1.2 与 24×24/2 混用，注释自承），
  缺 `panel-right/close/check/send/stop/file/image/trash` 等常用图标，导致：
  `frame.go:211` 手写 SVG（违反 §8 不要重造常用图标）；侧栏会话行用文本"⋯"而非
  `icons.More`（`sidebar.go:806`）；「发送」用 `ChevronRight`、图片附件用 `Globe`；
  折叠 rail 里"自动化"和"插件"都落到 `icons.Data()` 同一个立方体图标（`frame.go:136-149`）。
  图标总数里还有 7 个 0 调用（FolderPlus/CirclePlus/PanelLeft/More/Reload/Bell/Download）。

### 2.6 暗色与颜色（正面对照）

暗色主题抽查（`.cache/design-review/narrow/dark-home.png`、`dark-settings.png`、`dark-session.png`）：
表面层级、强调色、正文对比度均成立，未发现硬编码品牌色；全仓唯一裸颜色是 composer 阴影
`ui.RGBA(0,0,0,0.05)`。颜色层基本达标，问题主要是**状态色当小字前景**时缺文本变体
（Success/Warning/Danger 直接用状态色写正文时对比度不足，`session` 内 badge/状态行有实例）。

---

## 3. 交互（用户重点二）

### 3.1 键盘：三个核心入口都没有键盘路径

| 入口 | 现状 | 证据 |
|---|---|---|
| 命令面板（Cmd+K） | 输入框过滤 + 最多 8 条 `ButtonBase` 行，无选中高亮、无 ↑/↓、无 Enter 执行；底部却写「Esc 关闭 · Enter 选择」 | `uiroot/frame.go:275-335`；`p2-palette.png` |
| 斜杠命令菜单 | 自绘行只有 `Clicked()`；每帧 `input.Focus()` 把焦点钉回输入框；Esc 无处理；按 Enter 会把「/goal」当聊天文本发出去 | `session/view.go:1081-1103,1608-1811`；`p2-slash.png` |
| @ 引用菜单 | 同上，无方向键/Enter 选中；工作区错误态无"去设置"入口 | `session/view.go:1646-1822`；`p2-sessionrefs.png` |

这三处 mygo 都有现成控件（`ui.List`、`ui.Autocomplete`、`ui.SearchField`、`ui.Popover`），
属于 design-guide §18「Do not reimplement standard interactive components」的明确违规。
同时 `/help` 页写的快捷键与实际不一致（如「Cmd/Ctrl+Enter 发送」而实际 Enter 发送；
漏 Cmd+N/Cmd+,），快捷键文档与实现没有共享注册表。

### 3.2 焦点、滚动与控件身份

- **流式回复不跟随滚动**：对话/轨迹/记忆三处 `ui.Scroll` 都没有 `ScrollState`/`ScrollIntoView`，
  流式内容增长时视图停在原处；切页签/会话可能复用偏移（待确认）。mygo `docs/ui/scroll.md`
  专门写了"跟随日志需要主动滚到底"。
- **焦点管理用 hack**：斜杠/@ 菜单打开时每帧 `input.Focus()` 对抗结构插入导致的失焦
  （`session/view.go:1097-1103`），而没有用 `c.Key`/`FocusBind` 的稳定身份。
- **`c.Key` 全仓只有 2 处**（都在 `plugins/view.go:191,337`）；会话行、工作区组、消息、
  工具卡、自动化任务卡、设置里的记忆行都是循环内 stateful 控件无 key——排序/过滤/搜索变化时，
  焦点或展开状态可能错位。这是 mygo-maintenance 的硬性要求，属系统性风险。

### 3.3 状态反馈：hover/pressed 被品牌样式静默吃掉

mygo 的 `styleButton` 只在 `b.bg == base` 时切换 hover/pressed
（`/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4/ui/widgets.go:171-180`）。本项目用
`design.PrimaryButton()`、`secondaryButtonStyle()`、`t.Accent` 等覆盖背景后，`b.bg != base`，
于是**这些按钮没有 hover/pressed 反馈**——设置页保存、对话框允许/批准、自动化新建等全部"看起来是死的"。
`ui.Toggle` 开态覆盖背景后同样无 hover/pressed（mygo `ui/toggle.go:24-43`）。

另外：`hover` 与 `selected` 共用 `BgHover` token（`design/components.go:94-103`、`tokens.go:76`），
截图实测"当前工作区"和"活动会话"同色，悬停与选中无法区分。技能要求
"States on every interactive surface: hover, focus, selected, active, disabled, loading…"。

### 3.4 对话框与浮层

- **按钮顺序反桌面惯例**：除原生 `ui.AlertDialog`（正确：取消左、默认主操作右）外，6/10 个手写
  Modal 都是「主操作在左、取消/危险在右」：计划审阅 `批准 | 继续规划 | 取消`
  （`interaction.go:340-354`）、审批 `允许 | 总是允许 | 拒绝`（`approval.go:239-248`）、
  ask `选项… | 取消`（`interaction.go:426-444`）、技能新建/编辑、代理预设等
  （`settings/skills_edit.go:102-147`、`presets.go:495-506`）。
- **Enter 默认动作不一致**：approval 默认焦点在「允许」（Enter=允许、Esc=拒绝，但界面无提示）；
  plan 默认焦点在反馈输入框（Enter 无动作）；/feedback 默认焦点在「有帮助」（Enter=好评）。
  没有统一的"Enter 触发主操作 / Esc 取消"约定。
- **确认/撤销覆盖不足**：放弃目标、清除目标（`view.go:270-293`、`command_dialogs.go:93-98`）、
  整理记忆、单条删除记忆直接执行；Esc 在 plan_review 会**直接取消整份计划**且无确认；
  rightpanel 删除任务、设置删除预设/移除 MCP 也没有确认（`tasks.go:128-135`、
  `presets.go:462-470`、`mcp.go:150-156`）。
- **设置弹窗无可见关闭按钮/标题栏**，只能 Esc 或点遮罩（`frame.go:339-345,362-405`）。
- **嵌套 Modal**（设置 → 技能编辑/代理预设）叠加两层同色遮罩，层次偏弱（待确认，需帧测试）。

### 3.5 状态覆盖与"伪控件"

- 权限模式在会话工具条里是**只读 badge**（`view.go:1210-1213`），看起来像可点菜单却不可点；
  首页 composer 的 workspace/model/mode chip 也是只读 badge（`home/composer.go:217-238`），
  与会话页同样的信息是可点菜单——同一信息两种控件。
- 发送按钮的 disabled 是自定义"accent 16% + accent 文字"，与首页原生 disabled 不同；
  运行中"排队发送"仍叫「发送」（`view.go:1496-1526`），与停止按钮并排语义易混。
- loading 覆盖不足：审批/记忆操作/导出等缺少按钮级进行态与禁用；approval 双击可能重复提交。

### 3.6 Toast 与反馈通道

toast 模块整体重实现了 mygo 自带的 toast 系统（`c.AddToast`/`ToastViewportBase` 全仓 0 使用）：
无 hover/focus 暂停、无 Esc、无 action、无屏幕阅读器公告、无淡入淡出；每个 toast 起一个
`time.Sleep` goroutine 且不可取消（`toasts.go:196-217`），在 build 回调里过滤并改写状态
（`toasts.go:232-248`，违反"渲染回调只做绘制/构建"的纪律）；固定 140px 底距在 1024×720 下
直接盖住消息操作行与 composer（`narrow/p2-toast.png`）。同时会话头的「已导出/已停止/已压缩/
目标已完成」badge 常驻无清除路径，与 toast 形成两套反馈通道。

### 3.7 响应式：窄窗与右栏打开时核心操作消失

- 壳层：280px 侧栏 + 320px 右栏固定、内容柱 `MinWidth(0)`、composer 固定 `868`/94%
  （`frame.go:39-46,252`、`session/view.go:23-24`），而产品允许窗口缩到 900×600
  （`host.go:60-64`）。
- 实测 1024×720：会话标题变「工作…」/「…」，**发送按钮被裁到卡片外不可见**，提示只剩「Enter 发…」；
  右栏打开（1280 宽）时同样把标题/工具条挤爆（`narrow/p1-chat.png`、`narrow/p1-trajectory.png`、
  `.cache/design-review/shots/p2-rightpanel.png`）。
- 右栏 8 个页签在 320px 内墨迹约 290px > 可用 288px，无溢出处理（`rightpanel/view.go:68-101`）；
  英文下更差。

---

## 4. 按钮位置与主次权重（用户重点三）

### 4.1 现状：同一类动作在任何两个界面都长得不一样

| 动作 | 位置 | 权重 |
|---|---|---|
| 新建/新对话 | 侧栏顶部（全宽）/ automation 页头右 / 会话空态居中 / 设置标题行右 / 设置面板内 | accent 填充 / 中性 primary / 中性 primary+图标 |
| 保存/应用 | 对话框页脚右 / 设置页内容底部（非粘性，会溢出）/ 面板内行内 | 中性 primary（有时配无关图标：Sliders/Globe/Search 当"保存"图标） |
| 刷新 | 章节标题右（plugins/skills/rightpanel） | ✅ 全项目最一致的一类 |
| 发送 | 会话右下（accent h32）/ 首页右下（中性 h38） | 两种颜色两种高度 |
| 删除/移除 | automation 卡片底部左（红字）/ 预设行（中性）/ MCP 行（中性）/ 右栏任务（原生）/ 技能确认（中性 PrimaryButton） | 4 种样式，3 处无确认 |
| 关闭/取消 | 对话框页脚（左或右不定）/ present 头部右上 / 设置无可见关闭 / toast 右上 × | 4 种 |

### 4.2 结论（按严重度）

1. **[P0] 主操作可能被画到容器外**：设置弹窗固定 760×640 + 内容列无 `ui.Scroll`
   （`frame.go:378-398`）→ 外观页「应用」、LSP 页「保存」渲染在弹窗外遮罩上（`p2-appearance.png`、
   `p2-lsp-settings.png`）；点击是否会被 Modal 的"点外部关闭"吞掉需 Tester 验证，但已属于破损。
2. **[P1] 主次权重失效**：见 §2.2；每个对话框/页面都有多个"同等重要"的按钮。
3. **[P1] 浮层按钮顺序反惯例**：见 §3.4；"取消/拒绝/危险"占最右主位，"主操作"在最左。
4. **[P1] 危险操作位置与样式不统一**：红字/中性描边/原生/中性 primary 四种；批量危险操作
   （清空会话/数据）与普通操作并排；危险操作可能成为 Enter 默认焦点。
5. **[P2] 行内动作位置错误、权重过高**：每条助手消息在整条消息最后、工具卡之后常驻
   「复制/有帮助/没帮助」3 个等宽按钮 + 已反馈 badge + 用量行（`view.go:539-602`），
   归属歧义（看起来像工具卡的操作），且窄内容下按钮比正文显眼。
6. **[P2] 页面动作区不统一**：设置页保存位置 5 种（页底左下/面板内左下/面板内行内/页头右上/
   全宽流内）；右栏每个 pane 的刷新/预览/保存/危险按钮位置各异（`rightpanel/*`）；
   侧栏底部「设置/显示已归档」是不贴底、会被搜索结果顶出视口的小描边按钮
   （`sidebar.go:886-916`，`p2-search.png`）。
7. **[P2] 控件位置随开合漂移**：右栏唯一开关是 contentHeader 的 24×24 图标（`frame.go:215-241`），
   打开后位置在右栏左边界、与「检查」标题不同基线；右栏自身没有关闭 X。
8. **[P3] 会话行右侧常显 "⋯ ⌄"**（`sidebar.go:806`）：双字形、无 tooltip、占 44px，
   长标题被截断；会话行内动作没有 hover 才显示的分级。

### 4.3 建议规范（一次收敛）

1. **按钮三档 token**（放 `internal/design`）：
   - `Primary` = accent 填充 + OnPrimary 文字 + hover/pressed（收编侧栏「新对话」、会话「发送」两处手搓）；
   - `Secondary` = 中性描边（现 `secondaryButtonStyle`，统一 32 或 36 高）；
   - `Danger` = danger 文字/边框，禁止作为默认/Enter 焦点。
2. **统一 `DialogFooter`**（放 `internal/uikit`）：固定 `[危险/取消（左） … 次操作 … 主操作（最右、accent、默认聚焦、Enter 触发）]`，
   所有手写 Modal 改调；原生 `ui.AlertDialog` 保持。
3. **页面动作区**：每屏只有一个主操作，固定在页头右上（≤2 个页面级动作）；分组级动作固定在
   分组标题右；设置/长表单的主操作放**固定 footer**（`SlotSettingsPageFooter` 现成可用），
   内容区滚动。
4. **危险操作规则**：不可逆 → `ui.AlertDialog` 确认（默认焦点不落在危险按钮）；可逆 → 立即执行 +
   toast 撤销。行内危险操作统一红字/图标 + 行最右。
5. **行内动作收敛**：消息动作改为 hover/聚焦时出现的图标组（Copy/👍/👎/⋯ + Tooltip），
   用量降为 11px 或折叠。
6. **hover/pressed 必须显式实现**：基于 `ui.ButtonBase` 自绘时必须按 `Hovered()/Pressed()` 换色，
   或只覆盖非背景属性；开关改用 `ui.Switch` 后由原生提供状态。

---

## 5. 信息架构与逐界面问题

### 5.1 设置（19 页）

- **[P0] 容器失效** → 见 §4.2-1。
- **[P1] 导航 IA**：`SettingsPageSpec.Section` 字段存在且被用于排序（`runtime.go:316-332`），
  但壳层只渲染一个 `SidebarSection("设置")`，**从不显示分组头**（`frame.go:378-391`）→
  19 项裸平铺，顺序由 Section 原始字符串（ASCII `general` 最前）决定：打开设置默认落在「关于」
  （`about` Order 90 是最低）；「外观/技能」被硬塞进「模型」组；`Order` 有重复
  （behavior/mcp=96、modelcatalog/providers=15），实际由 ID 兜底；中文下最后一项被裁、需要滚动。
- **[P1] 布尔设置用 `ui.Toggle` 冒充开关（10 处 + automation/plugins 共 15 处，`ui.Switch` 0 处）**：
  全宽文字按钮，开=浅蓝填充、关=白底描边，没有滑钮/勾选/开|关文字；通知页四个级别过滤是一叠
  相同蓝条，点击"蓝色"实际是隐藏。mygo `switch.md` 明确"setting 用 Switch"，`Toggle` 是
  toolbar 按下按钮；mygo 还提供 `Form/Field`（0 使用）来做"标签在左、控件在右、说明在下方"。
- **[P1] 跨页状态不同步**：模型页把 model/baseURL/prompt 读入模块级草稿，服务商页「使用」
  直接写 store（`settings.go:469-486,856-867`）→ 返回模型页仍是旧草稿，页尾「当前模型」显示草稿
  而非生效值；此时保存会静默覆盖服务商页刚写入的配置。
- **[P2] 表单没有可见标签**：至少 19 个输入只有 placeholder 或完全空白（模型目录/新增 LSP/
  记忆策略/技能新建 3 个连续空输入框等），placeholder 输入后消失；未用 `ui.Form/Field`
  （0 使用），错误只出现在页底/红行，不在字段旁。
- **[P2] 保存语义三套并存**：即时生效 / 底部保存 / 即时+冗余保存（LSP 增删即时写、底部还一个
  「保存」再写一次）；同页混用（模型页预设即时、字段要保存）；无 dirty 标记/未保存拦截/取消。
- **[P2] 版本/构建元数据进页头**：`about.go:163-177` 给每个设置页 header 追加
  「ZeoKeel v0.1.0 构建 unknown」，其中 "构建 unknown" 是空构建信息直接外露；版本信息应只留在
  「关于/诊断」页（那里已有）。
- **[P2] 无可见关闭/取消**；**[P2] 危险操作样式/确认不一致**；**[P3] 输入框无
  Cmd+S/Enter 提交、焦点不 AutoFocus、Tab 路径长（19 项导航 → 内容 → footer）。**

### 5.2 会话主界面

- **[P1] 会话头动作过载**：标题行同时放 标题 + 运行/模式/目标 badge + 目标文本（MaxWidth 360）
  + `暂停目标/完成目标/放弃目标/清除目标` 4 个同权描边按钮 + `更多` + 右栏开关
  （`view.go:110-156,208-295`）→ 1280 下双截断（`p1-trajectory.png`），右栏打开/1024 下标题只剩
  「工作…」；其中含危险动作（放弃/清除）与主操作平排。
- **[P1] 输入区工具条超容量**：模型/权限badge/推理/模式/预设/`/`/附件/提示/发送 9 项平铺，
  提示文本默认宽度即被截断（`p2-chat.png`「Enter 发送 · Shift+Enter 换…」），右栏打开时
  附件/提示溢出卡片、发送键被裁出（`p2-mentions.png`）。
- **[P2] 消息动作行归属歧义**（§4.2-5）；**[P2] 工具卡两套解剖结构**（`toolcards.go:180-245`
  与 `view.go:629-700`）、两套状态标签；**[P2] 轨迹页信息价值低**：标题「轨迹」与页签重复、
  「1 个工具调用」计数 badge、步骤卡与对话页工具卡重复；**[P2] 状态 badge 永久驻留**；
  **[P2] Markdown 标题层级压过页面标题**（§2.3）；**[P2] 主列宽不一致**：正文 840、
  composer 实际约 894（`MaxWidthPercent(94)` 覆盖 `MaxWidth(868)`，mygo `element.go:854-858`），
  左右各偏 26px；**[P2] 反馈通道分裂**（toast + 常驻 badge）。
- **正面**：消息区/输入区/浮层骨架全部是 mygo 原生控件，Markdown 代码块自带 Copy 语言标签，
  Esc/Enter 在原生 Modal/AlertDialog 内行为正确；这些不要重写。

### 5.3 首页与侧栏

- **[P1] 「工作区」标题被压成竖排**：`sidebar.go:501-536` 一行里塞「工作区 + 当前工作区 badge +
  视图菜单 + 添加工作区」，280px 下标签被压成 9×39px 竖排，与工作区 chip 重叠；英文下
  `+ Add workspa…` 直接被侧栏边缘裁断（`p2-home.png`、`p2-english.png`）。同一行在会话页
  （无 workspace badge 时）是正常的，说明是布局脆弱而非字体问题。
- **[P2] 首页 hero 把开发元数据当徽章**：`v0.1.0`、`go1.27.3`（`home.go:166-171`），
  对用户无决策价值；`welcome` 模块同样（`welcome.go:44,64-65`）。
- **[P2] 侧栏底部「设置/显示已归档」**：小描边按钮、左对齐、不贴底、在搜索/会话多时被顶出视口
  （`p2-search.png`）；「显示已归档」实际是视图开关/菜单项（`view_options.go` 已有「视图」菜单）。
- **[P2] 导航两套实现且已漂移**：展开态由 sidebar 模块自绘（`sidebar.go:292-322`），折叠态由宿主
  `frame.go:136-162` 另写；折叠 rail 里自动化/插件同为 `icons.Data()` 立方体、label 取
  `spec.Title` 未本地化；展开态没有折叠入口（只能 Cmd+B）。
- **正面**：原生 `ui.Sidebar` 承担了选择/键盘/滚动；会话行有 hover/selected；命令面板/主题/
  导航注册机制（槽位）本身是好的。

### 5.4 右栏 inspector

- **[P2] 8 个页签在 320px 内溢出**（`rightpanel/view.go:68-101`，无横向滚动/溢出菜单）；
- **[P2] 卡片化过度**：320px 内每行一张带边框卡片（`view.go:118,205,357`、`files.go:95`、
  `tasks.go:112`…），一屏只看到 3-5 行；
- **[P2] 操作位置不统一**：刷新在 section 头右、预览/下载在产物卡内、任务操作在卡片底部右、
  终端运行在输入行右、Agents 发送在输入行右（4 种模式）；
- **[P3] 空态用「图标 + 彩色圆底」反模式**（`view.go:230-240`，design-guide §24 明确列为 AI 反模式）；
- **[P3] 与主区同为 BgSidebar 底色**（个人偏好：VS Code 也这样做，可不改）。

### 5.5 Automation / Plugins

- **[P1/P2] automation 任务卡右上角是 `ui.Toggle`，文案 = "任务名 + 启用"**（`automation/view.go:166-179`），
  卡片标题已重复任务名，英文下出现 "每日构建检查 enabled" 中英混排；状态只能靠浅蓝底猜。
- **[P1/P2] automation「新建任务」与「筛选」同权重**（页头右侧），主操作不突出。
- **[P2] automation 元数据 badge 被撑成全宽胶囊条**（`view.go:183-191`），运行历史显示
  raw session id + 英文 "ran in chat session sess-…"；「历史」用按钮+map 手搓 disclosure
  （mygo `ui.Collapsible` 已有 10 处在用）。
- **[P2] plugins 工具目录 70 行全量构建、每行一个"启用"大按钮**（同名 a11y 标签，读屏无法区分）；
  搜索后"70 个工具"计数不随过滤更新（`view.go:270` vs `:284`）；MCP 启用开关藏在展开详情内；
  「插件参数」用整行大按钮手搓展开，术语「已接线/Wired」是内部黑话。
- **[P2] 两页的空态/错误行都是第 4/6 套自定义实现**（见 §2.1）。

---

## 6. mygo-maintenance 合规评估

### 6.1 已核实 CLEAN（不需要改）

- 无 struct 字段存 `ui.Element`/parts；无 `*ui.Context` 存字段或传给 goroutine；
- 10 个 `go func` 均只捕获 state/service/runtime，结果一律 `runtime.Post` 回 UI 线程
  （terminal 的互斥锁例外有注释说明，可接受）；
- `Changed()/Submitted()` 在同帧构造后查询，无"用旧元素"回调；无 `Window.Update` 直调；
- 无一帧内跨 build 复用元素；无绘制回调副作用（UI 模块没有 Draw 回调）。

### 6.2 命中项（需要修）

| 问题 | 位置 | 级别 |
|---|---|---|
| `c.Key` 全仓 2 处；循环内 stateful 控件（会话行、工具卡、记忆行、automation Toggle、Collapsible、palette 行）无稳定身份 | `plugins/view.go:191,337` 之外 | P2 |
| 15 处 `ui.Toggle` 当开关、`ui.Switch` 0、`Form/Field/List/Tree` 0 | 见 §5.1 | P1 |
| toast 模块在 build 回调里改 state、每 toast 一个不可取消 goroutine | `toasts.go:196-217,232-248` | P2 |
| 渲染路径同步 I/O：记忆 List/Search、@ 菜单 Files/Search、缩略图解码、点选读文件（上限 10MB×16） | `session/view.go:876-886,1710-1724`、`attachments.go:73-95,112-128` | P1 |
| 包级可变 test seam（14 个 func 型 var + 标题字符串）与 `toastTTL`；多窗口下会互相覆盖本地化标题 | `session/export.go:16-25`、`attachments.go:20-28`、`sidebar/workspace_add.go:24` 等 | P3 |
| `design.TextScale` 从未接线 → 文档承诺的文字缩放机制空转 | `design/tokens.go:65-71` | P1（设计系统） |
| uikit 不依赖 design、裸字号；真实页面绕过 uikit | `uikit/uikit.go` | P1（设计系统） |

---

## 7. 修复路线（建议按此顺序，先结构后装饰）

### Batch 0（P0，当天可完成）
1. **设置壳**：内容列包 `ui.Scroll`，主操作行移入固定 footer（`SlotSettingsPageFooter` 已存在）：
   `frame.go:383-398`；同时把设置尺寸改为随窗口 `min(760, 窗口宽-80) × min(640, 窗口高-80)`，
   加 960×640/1024×720 快照回归。验收：19 页最后一可交互元素的 bounds 都在弹窗内。

### Batch 1（P1，本轮迭代）
2. **收敛按钮体系**：`design.PrimaryButton` 改 accent（收编 sidebar/session 两处手搓），新增
   `SecondaryButton/Danger`；新增 `uikit.DialogFooter` 并替换 6/10 反惯例页脚；删除 3 份
   `secondaryButtonStyle`。
3. **15 处 `ui.Toggle` → `ui.Form + ui.Field + ui.Switch`**（布尔设置），多选过滤改 `ui.Checkbox`；
   顺手修复 automation "任务名+启用" 文案与 automation/plugins 的 a11y 同名标签。
4. **响应式**：内容柱设最小宽、280/320 可收缩（或右栏在 <1100 宽时抽屉化/默认关闭）；
   composer 工具条改 `ui.Toolbar`（原生"放不下收进溢出菜单"）或分组 + 溢出菜单；
   提示文案移出布局竞争；补 900/1024/1280 三档快照。
5. **键盘**：命令面板、斜杠菜单、@ 菜单改用 `ui.List`/`ui.Autocomplete`/`SearchField`，
   首行选中、↑/↓/Enter/Esc 可用；命令面板去掉 8 条硬上限、去重"设置/打开设置"。
6. **会话滚动跟随**：三个 Scroll 加 `ScrollState`，贴底自动跟随 + "回到最新"按钮；切页签恢复位置。
7. **共享层收口**：uikit 改为基于 `*design.Tokens`（`PageHeader/Section/EmptyState/ErrorLine/
   KeyValueRows/TabStrip`），页面全部改调；`TextScale` 接线或删除；删/用 design 死代码；
   统一 mono 样式与页头规格。
8. **浮层规范**：统一 DialogFooter/关闭位置/Esc 语义/危险确认；plan_review 的 Esc 不得直接取消计划；
   审批/计划主操作改 accent 并聚焦。
9. **toast 回到框架**：`c.AddToast`（或 ToastViewportBase 保留品牌外观），删除自建 state/TTL/
   goroutine；补 hover 暂停/Esc/读屏。状态通知走 toast，删掉常驻 badge。
10. **侧栏**：工作区标题行拆分（标签 + 当前工作区独占一行，或把 badge 移入视图菜单）；
    footer 移出可滚动区 dock 到底；折叠 rail 与展开态共用图标映射并本地化；展开态补折叠按钮。
11. **设置 IA/状态**：按 Section 渲染分组头并重排（通用→模型与对话→工具→外观/技能→高级→关于），
    默认页改为业务页；修 `Order` 重复；模型/服务商单一数据源，页尾"当前模型"读生效值。

### Batch 2（P2，排期）
- 卡片墙治理（设置/右栏/记忆/对话）：独立对象保留卡片，其余改行 + Divider/间距；
- 页头/空态/错误行/状态色映射/页签条收敛到 uikit（4/4/6/7/2 套 → 各 1 套）；
- badge 元数据 pill 改 muted 文本（16 处），automation 3 处全宽条修复；
- 图标库补齐常用图标（close/check/send/stop/file/image/trash/panel-right），删手绘 SVG、
  替换 "⋯"/Globe/ChevronRight 误用，清理 7 个死图标，统一 16 网格；
- 表单统一 `ui.Form/Field` + 可见标签 + 字段级错误；
- 列表虚拟化（plugins 70 行、rightpanel 日志、palette）与 `ui.List/Tree` 替换手搓行；
- 行内动作改 hover 图标组；统一危险操作样式/确认/撤销；
- `c.Key` 补齐（循环内 stateful 控件）；hover/selected 拆 token；状态色补文本变体；
- automation/plugins 的空态/计数/历史 Collapsible/术语黑话修复；
- 首页 hero 去版本徽章；设置页头去构建元数据；i18n 日期格式与术语表。

### 验收方式（技能要求的"done"）
- `go build ./...`、`go test ./...`、`go vet ./...` 通过（`make ci`）；
- 新交互有 `ui.NewTester` 帧测试：键盘路径（palette ↑↓Enter、slash Enter/Esc、滚动跟随）、
  设置落盘、对话框 Enter/Esc；
- 新/改界面有 900×600、1024×720、1280×800 三档快照 + 暗色快照，并与本报告截图对比；
- Final Review 清单：去掉边框/背景/图标/阴影/卡片后信息是否仍成立；是否复用现有组件；是否新增
  了未验证的结构或交互。

---

## 8. 证据索引

| 类别 | 路径 |
|---|---|
| 全量浅色截图（1280×800，65 张） | `.cache/design-review/shots/` |
| 窄窗 + 暗色探针截图（1024×720，67 张） | `.cache/design-review/narrow/`（`dark-home/dark-settings/dark-session`） |
| 裁剪放大证据 | `.cache/design-review/crops/{lead,settings,shell,session,conformance}/` |
| 设置专项报告（489 行，56 条） | `.cache/design-review/reports/settings-audit.md` |
| 壳层专项报告（491 行，59 条） | `.cache/design-review/reports/shell-audit.md` |
| 会话专项报告（469 行，50 条） | `.cache/design-review/reports/session-audit.md` |
| 横向一致性报告（430 行，61 条） | `.cache/design-review/reports/conformance-audit.md` |
| 关键代码事实 | `internal/uiroot/frame.go`、`internal/ui/settings/settings.go`、`internal/ui/session/view.go`、`internal/ui/sidebar/sidebar.go`、`internal/ui/rightpanel/view.go`、`internal/ui/toasts/toasts.go`、`internal/design/{tokens,style,components}.go`、`internal/uikit/uikit.go` |
| mygo 对照 | `/root/go/pkg/mod/github.com/egoist/mygo@v0.7.4/docs/ui/*.md`、`ui/widgets.go:152-181`、`ui/toggle.go:24-43` |

## 9. 待确认清单（不影响主结论）

1. 设置弹窗外溢出的「应用」按钮点击是否被 Modal 的 backdrop 关闭吞掉（需 Tester）。
2. 自绘行的 focus-visible 环是否可见（截图无键盘焦点证据）。
3. 切页签/切会话时 Scroll 偏移是否复用（需帧测试）。
4. 嵌套 Modal（设置 → 技能编辑）的 Esc/Tab/焦点归还行为。
5. 900×600 最小窗口与英文长文案组合下的实际表现（无该档截图）。
6. 流式生成态（停止/排队）与错误态没有截图，相关交互结论来自代码阅读。
