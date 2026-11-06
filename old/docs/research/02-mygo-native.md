# 02 · mygo 原生 UI 原型实测：能力清单、被重造的轮子、代码量与视觉对照

> 角色：原型工程师（Team 成员 native-prototype）· 任务：`task-2` · 时间：2026-11-05 12:32–14:10（Asia/Shanghai）
> 仓库：`/data/mygo`（ZeoKeel，go 1.27.3，`CGO_ENABLED=0`）· 依赖：mygo v0.7.4（`.cache/gomod/github.com/egoist/mygo@v0.7.4`）
> 口径：只写 `.probe/research/` 与本文；不改 `internal/`、`cmd/`；不启 GUI/服务器；原型用 `ui.Render` headless 出图。
> 术语：本文的"原生"= mygo 自绘控件（`ui` 包，自带主题/度量/交互/无障碍），不是系统控件（macOS/Windows 控件）。mygo 的原生 UI 三平台同一套外观与行为。
> 相关：`01-code-audit.md`（代码度量与成本归因）、`03-ux-design.md`（UI/UX 判断）；本文的代码量结论与其交叉验证。

---

## 0. 结论先行（一页）

1. **能做，而且比预期容易。** `294 行`原型（其中视图构建函数 `135 行`、代码行 `240 行`）渲染出完整 Home 屏 + Session 消息流小样（markdown 回复、可折叠工具卡、可拖拽分栏、Tabs 右栏），明/暗双主题，**零像素手调、零自定义几何、stock 主题下零自定义颜色**。见 §3、图 1/图 2。
2. **视觉上"够用，但不像 DSH"。** 原生默认是"另一个产品"：raw 像素差 3.27% vs 现实现 1.18%，但观感差距远大于 2 个百分点；`compare-home-4up.png` 一眼可见。**DSH 观感不是主题 token 给的**：只改约 20 个主题字段（18 个颜色 + 圆角，同一布局）只改动 **0.60%** 像素——颜色几乎不影响"像不像"，几何/控件形态/字体/图标才是。见 §4。
3. **代码量能省，但幅度是 UI 层的 1/3 左右，不是"腰斩"。** 单屏 Home：现实现 `home.go 525 + home_live_menus.go 612 = 1,137 行`（另有 design/icons 共享依赖）→ 原型同屏约 `100 行`视图代码（补全被省略的功能按 2–3× 折算 200–300 行）。五屏外推：UI 生产代码 `17,121 行 → 9,231–11,464 行`，**净省 5,657–7,890 行（33%–46%），中位 ≈6.8k（40%）**；与 01 报告自下而上的 5,150–6,900 行收敛，对外建议引用 **5.2k–7.9k 行（30%–46%）**。见 §5。
4. **现有 UI 确实在大量"重造轮子"（不是没用到 mygo，而是只用到"图元"层）。** `internal/shell` 生产代码里 `ui.Sidebar/Select/Tabs/Collapsible/Table/Tree/List/MenuButton/Toolbar/Badge/Toggle/Switch/PrimaryButton` 等高级控件**调用次数全部为 0**；`1,169` 次 `ui.*` 包级调用中 **72% 是 Text/Row/Column/Box/Spacer 布局图元**，高级控件只占 11.5%；侧栏树/下拉菜单/标签页/工具卡/表格/消息列表全部手搓（`Text 310`、`Row 303`、`Column 154`）。另有 `22/35` 个 design/motion.go 导出在 design 包外**零引用**——1:1 研究层是研究副产品，不是被产品消费的样式。见 §2。
5. **原生方案在 UX 上更好（且有据可查）**：键盘导航、焦点环、无障碍树、暗色、DPI、悬停/按下/禁用态、列表虚拟化、下拉菜单键盘操作、折叠动画、markdown 选中复制（含 Copy as Markdown / HTML），全部随控件免费；上游 mygo `ui` 顶层包自带 `34,993 行`控件测试。代价是**产品辨识度**：要补一套品牌层（logo/wordmark、图标语言、2–3 个自有组合控件、约 30 行主题覆盖），一次性 3–5 人日量级。
6. **推荐"原生控件 + 品牌层"（选项 A）**；如果必须保留 DSH 视觉语言，走选项 B（原生交互 + 保留 DSH 令牌/密度，但取消逐像素门禁）；**只有定位"DSH 官方桌面壳"时**才值得维持选项 C（继续 1:1，继续付 106MB 基线 + 像素预算 + 修-验循环）。见 §6。

---

## 1. mygo 原生能力盘点：免费得到什么

### 1.1 主题与令牌

| 能力 | mygo 位置 | 原型用法 | ZeoKeel 现状 |
| --- | --- | --- | --- |
| 明/暗主题、约 25 个语义字段（背景/表面/边框/文字/主色/危险/警告/成功/选区/焦点/滚动条/圆角/间距/字号/字体） | `ui/theme.go:7`（`Theme` struct）、`:106`（`LightTheme`）、`:132`（`DarkTheme`） | `ui.LightTheme()`/`ui.DarkTheme()` 直接用；`dshTheme()` 只覆盖 18 个颜色 + `Radius`（17 行） | `internal/design/tokens.go:46` 自建 `Tokens`（183 行），`:155` 再映射回 `ui.Theme`；另有 50 处 `ui.Hex/RGBA` 散落 shell |
| 运行时换主题 | `api_gen.go:467`（`Context.SetTheme`）、`:2378`（`Themed` 分区主题） | 一行切换明暗 | 等价能力，但要多维护一套 Dark 令牌与暗色专用覆盖色 |
| 间距/字号/圆角换算 | `theme.go:59`（`Space`）、`:91`（`Rem`） | `t.Space(n)`、`t.Rem(n)`、`t.Radius` 全程 | 自建 `design.Space1..8`、`RadiusXS..Max` 常量并逐处手调 |

### 1.2 控件（原型实测可用）

| 域 | 原生控件（`ui/api_gen.go` 行号） | 原型用到 | `internal/shell` 使用次数 |
| --- | --- | --- | ---: |
| 布局 | `Row:1744` `Column:952` `Box:754` `Spacer:1994` `Divider:1090` `Scroll:1757` `Split:2029` `SplitPercent:2045` `Grid:1277` `GridView:1303` | Row/Column/SplitPercent/Spacer | 图元大量用（Row 303/Column 154/Box 69） |
| 按钮/表单 | `Button:788` `PrimaryButton:1589` `TextInput:2295` `TextArea:2245` `Select:1849` `Segmented:1821` `Toggle:2469` `Switch:2111` `Checkbox:834` `Slider:1951` `Stepper:2099` `Form:1243` `Field:1184` `Fieldset:1202` | Button/PrimaryButton/TextArea/Select | **PrimaryButton 0、Select 0、Toggle 0、Switch 0、Checkbox 1**；Button 55、TextInput 14、TextArea 1 |
| 容器/导航 | `Sidebar:1911` `SidebarItem:1924` `SidebarSection:1939` `Tabs:2192` `Toolbar:2554` `Collapsible:891` `Accordion:645` `Breadcrumbs:775` `MenuButton:1392` `PopoverMenuButton:1577` `Popover:1530` `Menu`（`menu.go:68`）`ContextMenu`（`menu.go:45`） | Sidebar/SidebarSection/SidebarItem/Toolbar/Tabs/Collapsible/Popover | **Sidebar 0、Tabs 0（仅 TabsBase 3）、Collapsible 0、Toolbar 0、MenuButton 0、ContextMenu 0** |
| 数据/集合 | `Table:2169` `Tree:2609` `TreeItem:2625` `List:list_builder.go:22`（虚拟列表）`GridView:1303` | —（原型用 Collapsible/Tabs） | **Table 0、Tree 0、List 0**——消息列表/插件表/文件树全手搓 |
| 反馈 | `ToastBase:2417` `ToastViewportBase:2451` `c.Toast`（`toast.go:79`）`TooltipBase:2587` / `Element.Tooltip`（`tooltip.go:96`）`Spinner:2010` `Progress:1604` `Meter:1409` `Badge:742` `Avatar:715` `Modal:1423` `AlertDialog:685` | 无（静态图不显示） | Toast 用 `c.Toast` 109 次（已用原生）；Tooltip 9 次；**Badge 0**（自绘药丸） |
| 内容 | `Icon`/`SVG`（`svg.go:40 ParseSVG`）`Image:1339` `RichText:1731` `Text`/`Textf` `markdown` 子包 | Icon（无图标库）/Image | Icon 77、Image 17、MustParseSVG 46 |

### 1.3 内容与编辑器（本次"省代码"的最大金矿）

| 能力 | mygo 位置 | 原型验证 | ZeoKeel 现状 |
| --- | --- | --- | --- |
| Markdown 渲染（CommonMark+扩展、表格、代码块、任务列表、数学、语法高亮、选中复制 Markdown/HTML） | `ui/markdown/view.go:98`（`View`）、`:115`（`Stream` 流式）、`theme.go:103`（`DefaultTheme`）、`code.go:87`（`DefaultCodeBlock`，自带 Copy 按钮）、`copy.go`、`table.go`、`reveal.go` | Session 小样直接用 `markdown.View` 渲染标题/列表/表格/引用/代码块 | `internal/shell/markdown.go:51` 起自研解析+渲染（628 行），无流式、无复制变体 |
| 代码编辑器（语法高亮、多光标、软换行、折叠、查找替换、补全、LSP、Diff 视图） | `ui/editor/build.go:22`（`View`）、`diffview.go:412`（`DiffView`）、`doc.go`（能力说明） | 未用（原型不需要） | 未使用；工具卡里纯文本 |
| 富文本编辑器（Markdown/HTML/RTF/JSON 编解码） | `ui/richtext/view.go:67`（`Editor`） | 未用 | 未使用 |
| 图表 | `ui/chart/chart.go:67`（`Plot`） | 未用 | 未使用 |
| 系统终端 | `plugins/terminal`（libghostty-vt） | 未用 | 未使用 |

### 1.4 动效、无障碍、i18n、字体/DPI、测试

| 域 | mygo 位置 | 原型/说明 | ZeoKeel 现状 |
| --- | --- | --- | --- |
| 过渡动画 | `ui/transition.go:20`（`ElementTransition`）、`:89`（`Element.Transition`）、`widgets.go:53`（`Animate`）、`:69`（`Loop`）、`motion.go:302`（`AnimateTo`）、`:466`（`AnimateColor`）、`:513`/`:534`/`:721`（橡皮筋/投影/贝塞尔）、`timeline.go:496-516`（Stagger/AtLabel） | Collapsible 展开、Tab 指示、hover 全自带 | `internal/design/motion.go` 313 行自建时长/缓动/预设 → **22/35 导出零引用** |
| 焦点/键盘/无障碍 | `access.go:16`（`Role`）、`:93`（`Element.Role`）、`:128`（`Value`）、`focusgroup.go`、`focus_binding.go`、`c.Shortcut`（`context.go:536`）；无障碍树由控件自动上报 | Sidebar 树形语义、方向键/Home/End/首字母选择、Toolbar 左右移动与溢出菜单、焦点环全免费 | 自建 `Label/Role/Focusable` 覆盖（Role 82 处）+ 1,016 行 a11y/交互/几何/像素专项测试 |
| i18n | `github.com/egoist/mygo/i18n`（CLDR 匹配/复数/日期数字格式）、`ui/locale.go:14`（`Context.Locale`）、控件内建 l10n（菜单/对话框/日期控件） | 未用（原型英文/中文硬编码） | `internal/shell/i18n.go:47` 350 键双语文案表 + `:445 T` + `:499` 语言解析（589 行；文案表仍要） |
| 字体/DPI | `ui/window.go:37`（`RegisterFont`）、`:66`（`FontFamilies`）、`Element.Font`、`Theme.Font`；`ui.Render(view,w,h,scale)` 直接按 DPI 缩放；`ui.NewTester` 可驱动交互测试（`api_gen.go:1437`、`headless.go:222`） | 系统字体 + scale=1 出图；`Tester` 可做交互断言 | 自建 `design/font.go:1-55` 字体栈；像素基线手动维护 |
| 调试/自动化 | `ui/inspector.go`（inspect 面板）、`ui/automation.go`（`MYGO_AUTOMATION` 外部驱动）、`ui.Router`（`router.go:140`） | 未用 | 自建 verifier overlay 探针（837 行） |

> 一句话：**mygo 把"控件行为 + 主题 + 无障碍 + 动效 + markdown/编辑器"都做完了；ZeoKeel 只用了它的布局图元和少数容器，其余全部按 DSH 样式重画。**

---

## 2. 项目在重造哪些轮子（file:line 对照）

### 2.1 总览：原生高级控件使用审计（`internal/shell` 生产 38 文件）

```bash
# 复现：统计 internal/shell 非测试代码里的原生控件调用
for w in Sidebar Select Tabs Collapsible Table Tree List MenuButton Toolbar Badge Toggle Switch PrimaryButton; do
  echo -n "$w "; grep -rho "ui\.$w(" internal/shell --include='*.go' --exclude='*_test.go' | wc -l
done
```

| 原生控件 | 使用次数 | 替代品（ZeoKeel 自造） |
| --- | ---: | --- |
| `ui.Sidebar` / `SidebarItem` / `SidebarSection` | **0 / 0 / 0** | `workspaces.go:90 workspaceSidebar`、`:225 workspaceNode`、`:353 workspaceSessionRow`（1,232 行）；`:922-943` 钉住逻辑；`:663/:816` 手写拖拽排序 |
| `ui.Select` / `ui.MenuButton` / `ui.Combobox` / `ui.Autocomplete` | **0 / 0 / 0 / 0** | `home_live_menus.go:154 livePermissionChip`、`:191 permissionMenu`、`:237 liveModelMenuBadge`、`:270 modelPickerMenu`、`:329 homeWorkspacePicker`；`composer_menus.go:166 commandPalette`、`:381 mentionMenu`、`:565 attachmentMenu`（共 1,228 行） |
| `ui.Tabs` | **0**（仅 `TabsBase` 3 次） | `start_tab.go`、`session_live.go` 里手写 tab 条 |
| `ui.Collapsible` | **0** | `toolcards.go:80 liveToolCards`、`:96 liveToolCard`、`:110 liveToolRow`、`:169 liveToolDetail`（479 行，含手写状态点/图标/摘要） |
| `ui.Table` / `ui.Tree` / `ui.List` | **0 / 0 / 0** | `session_live.go` 手写消息流；`plugins_ui.go`/`settings_ui.go` 手写列表行；无虚拟列表 |
| `ui.Toolbar` / `ui.Badge` / `ui.Toggle` / `ui.Switch` / `ui.PrimaryButton` | **0 / 0 / 0 / 0 / 0** | 全部用 `Row/Box/Text` + `design.Apply` 手搓（`design/components.go:65 NavRow`、`:85 Row`、`:116 PrimaryButton` 等） |
| `ui.Modal` / `ui.Popover` / `ui.Button` / `ui.TextInput` | 15 / 15 / 55 / 14（已在用） | 弹窗外壳已原生，**内容仍是手写行** |

**图元 vs 控件比例**（`internal/shell` 生产，`ui.*` 包级调用共 1,169 次）：

| 类别 | 次数 | 占比 | 说明 |
| --- | ---: | ---: | --- |
| 布局图元 `Text/Row/Column/Box/Spacer` | 841 | 72% | 控件主体是"手搓" |
| 图形/颜色 `Icon/MustParseSVG/Image/Hex/RGBA/NewBitmap` | 191 | 16% | 含 46 处内联 SVG |
| 其他（按键/双向滚动） | 3 | 0.3% | `ui.Key` 2、`ui.ScrollBoth` 1 |
| 真正的高级控件 | 134 | 11.5% | Button/TextInput/Modal/Popover/Scroll/RichText/TabsBase/Overlay… |

### 2.2 逐项"重造的轮子"

| # | 重造的东西 | ZeoKeel 自造（file:line） | mygo 原生等价物（file:line） | 判定 |
| --- | --- | --- | --- | --- |
| 1 | **Markdown 解析 + 渲染** | `internal/shell/markdown.go:51`（`markdownBlocks`）、`:170`（`inlineSpans`）、`:362`（`parseMarkdown`）、`:470`（`mdFence`）、`:514`（`mdHeading`）、`:572`（`mdListMarker`）、`:608`（`mdStartsBlock`），628 行 | `ui/markdown/view.go:98 View`、`:115 Stream`、`theme.go:103 DefaultTheme`、`code.go:87 DefaultCodeBlock`（含 Copy 按钮）、`copy.go`（复制 Markdown/HTML）、`table.go`、`reveal.go` | **可整体删除**（原型 2 行替代） |
| 2 | **设计令牌系统** | `internal/design/tokens.go:46 Tokens`、`:84 Light`、`:110 Dark`、`:155 Theme`（映射回 ui.Theme） | `ui/theme.go:7/106/132`；`api_gen.go:467 SetTheme` | 保留 ~30 行覆盖即可（原型 `dshTheme` 17 行、18 色） |
| 3 | **CSS 式样式原子层** | `internal/design/style.go:8 Style`、`:11 Apply`；`components.go:36 Page`…`:116 PrimaryButton`（141 行） | `ui` 的 `Element.Padding/Height/Width/Radius/Background/Border/Shadow/...` 链式方法（`api_gen.go` 全量） | 可删（原生方法即样式层） |
| 4 | **动效令牌/缓动/预设** | `internal/design/motion.go:25` 与 `:138` 常量块、`:168 MessageTransition`、`:190 CollapseTransition`、`:223 Progress`、`:258 StreamOpacity`、`:289 PulseOpacity`，313 行；实测 **22/35 导出在包外零引用** | `ui/transition.go:20/89`、`api_gen.go:2799 Animate`、`:6118 Transition`、`widgets.go:53/69`、`motion.go:302/466/513/534/721`、`timeline.go:496-516` | 基本可删（研究副产品） |
| 5 | **侧栏/工作区树** | `internal/shell/workspaces.go:90/225/353/412`、`sidebar_hover.go:26/73`（1,350 行合计） | `ui/sidebar.go:41 coreSidebar`（键盘/首字母/树语义）、`:103 SidebarSection`、`:161 SidebarItem`；`tooltip.go:96` | 大量可删（选择/展开/hover/键盘免费） |
| 6 | **Composer 的权限/模型/工作区下拉** | `internal/shell/home_live_menus.go:154/191/237/270/329`（612 行） | `api_gen.go:1849 Select`、`:1392 MenuButton`、`:1530 Popover`、`:972 Combobox` | 可大幅缩减（原型 4 行 `Select`） |
| 7 | **命令面板 / @ 提及 / 附件菜单** | `internal/shell/composer_menus.go:125 menuFrame`、`:166 commandPalette`、`:381 mentionMenu`、`:565 attachmentMenu`（616 行） | `api_gen.go:700 Autocomplete`、`:2524 TokenField`、`:972 Combobox`、`menu.go:68 Menu` | 部分可删（命令语义仍是产品逻辑） |
| 8 | **工具卡（折叠 + 状态 + 详情）** | `internal/shell/toolcards.go:80/96/110/169`（479 行） | `api_gen.go:891 Collapsible` + `markdown/code.go:87` + `Badge` | 部分可删（原型 3 行 Collapsible） |
| 9 | **手绘 hover/焦点/禁用/行样式** | shell 里 `.Hovered()` 36 次、`.OnClick()` 98 次、`.Transition()` 仅 1 次（动效基本没接） | `Element` 内建 hover/focus/disabled 态；`Transition` 自动插值 | 可删（且现实现动效覆盖很低） |
| 10 | **i18n 语言匹配/格式化** | `internal/shell/i18n.go:499 resolveLanguage`、`:522 normalizeLang`、`:445 T` | `mygo/i18n`（CLDR 匹配/复数/日期数字）+ `ui` 控件自带 l10n | 部分可删；**350 键文案表保留**（`i18n.go:47`） |
| 11 | **图标** | `internal/icons/icons.go:1-81` + `internal/icons/svg/*.svg` 20 个（8.2KB）+ 46 处内联 `ui.MustParseSVG`（合计约 20.7KB 路径数据；如 `home.go:79` 鲸鱼 logo、`home.go:81-93` 7 个 UI 图标） | **mygo 没有图标库**：只提供 `ui.MustParseSVG`（`svg.go:40`）与 `currentColor` 染色 | **不可省**（换图标库可，但仍是资产） |
| 12 | **像素基线/差异工具** | `internal/pixeltest/diff.go:19 Compare`（140 行）+ `screens_test.go:18 TestScreensBaseline` + `baseline/`（106MB，80 截图/53 CSS/108 行为）+ `docs/baseline/` 1,222 行 | 原生方案不需要"对齐 Web 截图"；保留自回归即可 | 对齐基建可整体退役（自回归另说） |
| 13 | **a11y 补丁与专项测试** | `internal/shell/a11y*_test.go` 4 个文件 703 行（属 01 报告所列视觉/a11y 专项测试 2,341 行的一部分） | `access.go:16-128` 内建树；控件自动上报 | 大部分可删（原生语义自带） |

### 2.3 一个反直觉证据：1:1 研究层没被产品消费

`internal/design/motion.go` 导出 35 个名字，**22 个在 `design` 包外零引用**（含 `Progress/OpacityAt/ScaleAt/HeightAt/ShimmerPhase/ShimmerSteps/DurationSlow/DurationTab/EaseCSS/EaseStandard` 等）。也就是说这 313 行里的大部分是"为像素对齐做研究、做完没人用"。同类现象：`.Transition(` 全 shell 只有 1 处、`.Animate(` 0 处。

---

## 3. 原型：294 行 + 8 张渲染图

### 3.1 文件与组成

- 代码：`/.probe/research/native-demo/main.go`（294 行 = **240 代码行** + 36 注释 + 18 空行）
- 分析/出图工具：`/.probe/research/native-demo/analyse/main.go`（只读 PNG、写对比图，不计入原型）
- 运行（headless，无需显示器）：

```bash
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
go run ./.probe/research/native-demo            # 写 8 张 PNG
go run ./.probe/research/native-demo/analyse    # 写对比/差异图
```

| 组成 | 行数 | 说明 |
| --- | ---: | --- |
| 文件头（包文档 + import + 类型 + fixture 数据） | 59 | 首个函数之前 |
| 视图构建函数 | **135** | `view:101`(19) `sidebar:145`(24) `home:172`(19) `composer:194`(18) `session:215`(20) `transcript:239`(20) `details:261`(15) |
| 渲染 harness + 主题 | 55 | `main:60`(32) `themeName:93`(6) `dshTheme:125`(17) |
| 函数间空行 + 函数文档注释 | 26 | 10 个函数的 doc comment 与分隔空行 |
| 文件尾常量（`toolOutput/reply/changes/terminal`）与注释 | 19 | 文案 fixture |
| **合计** | **294**（240 代码 + 36 注释 + 18 空行） | 10 个函数共 190 行 |

**样式手调量**：全文件 44 处样式调用，**全部**用 `t.Space/t.Rem/t.Radius/t.FontSize/t.Border/t.Background` 等主题令牌；显式颜色 19 处 = `dshTheme` 里 18 处（明暗各 9 组 token 覆盖）+ composer 阴影 1 处；**stock 主题变体只使用 `ui.LightTheme()/DarkTheme()` 的默认值**（文件里那 18 个颜色只在 dsh 变体生效），也没有任何自定义像素值（`Width(t.Space(220))` 这类同样走令牌）。

实现要点（全部原生默认行为）：

- 侧栏：`ui.Sidebar` + `ui.SidebarSection`×4（workspaces，可折叠，自动 disclosure 箭头）+ `ui.SidebarItem`（选中灰底、hover、键盘↑↓/Home/End/首字母）；品牌行 `Text+Bold` + `ui.Badge`；`ui.PrimaryButton("New Session").Fill()`（禁用态、hover、焦点环免费）。
- 主区：`ui.Toolbar`（`Home` + Spacer + `Right panel` 按钮，溢出自动收进菜单）；hero `Text(t.Rem(2)).Bold()` + `Badge`；chips 用两个 `ui.Select`（自带下拉、键盘、选中态）；composer = `Column` 卡片（`Border/Radius/Shadow` 全用主题值）+ `ui.TextArea`（占位符、多行、IME、撤销）+ `Row`（`Button("+")` + `Select` 权限 + Spacer + `Select` 模型 + `PrimaryButton("Send")`，空输入自动 `Disabled`）。
- Session 小样：`ui.Toolbar` 标题栏 + `ui.SplitPercent`（可拖拽分栏）+ `ui.List`（虚拟列表）承载消息流：用户气泡（`Text` + `Padding/Radius/Background`）、`ui.Collapsible` 工具卡（自动展开动画 + 箭头）内嵌 `markdown.View` 代码块（自带语言标签 + Copy 按钮）、`markdown.View` 渲染标题/列表/表格/引用；右栏 `ui.Tabs`（Changes/Terminal）；底部同一 composer + 状态行。

### 3.2 渲染产物（`/.probe/research/out/`）

| 文件 | 内容 |
| --- | --- |
| `native-home-stock-light.png` | **Home 屏 · mygo 默认主题**（主证据） |
| `native-home-stock-dark.png` | Home · 默认深色 |
| `native-home-dsh-light.png` | Home · "少量主题定制"变体（18 色 + Radius 10） |
| `native-home-dsh-dark.png` | Home · 定制深色 |
| `native-session-stock-light.png` | Session 小样：markdown + 工具卡折叠 + Split + Tabs |
| `native-session-stock-dark.png` / `-dsh-light.png` / `-dsh-dark.png` | 同上其余主题 |
| `compare-home-4up.png` | 四宫格：DSH 基线 / 现实现 / 原生 stock / 原生+DSH 令牌 |
| `compare-home-detail.png` | hero+composer 1:1 局部四宫格 |
| `diff-native-dsh-vs-baseline.png` | 原 vs DSH 差异热力图（红=差异） |
| `diff-current-impl-vs-baseline.png` | 现实现 vs DSH 差异热力图 |

![Home：原生默认 vs DSH 基线](../../.probe/research/out/compare-home-4up.png)

图 1 · `compare-home-4up.png`：左上 DSH 基线、右上现实现、左下原生默认（stock）、右下原生+DSH 令牌。**注意左下/右下几乎一样**——见 §4.3 的 0.60% 证据。

![hero+composer 局部对照](../../.probe/research/out/compare-home-detail.png)

图 2 · `compare-home-detail.png`（上排基线/现实现，下排原生 stock/原生+DSH 令牌）：差距集中在 chips 形态（原生 Select 有框 vs DSH 无框下拉）、composer 单卡 vs 卡片内嵌输入框双框、字体/字重、图标。

### 3.3 屏幕上看不到、但免费得到的

- 交互：Tab 焦点顺序、焦点环、hover/按下/禁用态、下拉菜单键盘操作、Sidebar 方向键/Home/End/首字母选择、Toolbar 左右移动 + 溢出菜单、Split 拖拽、Tabs 方向键、折叠动画、Tooltip 延迟显示。
- 无障碍：控件自动上报 Role/Value/Expanded/Checked 树，屏幕阅读器可用；键盘全可达（原型零 a11y 代码）。
- 其他：暗色（一行）、系统字体、DPI 缩放（`Render(...,scale)`）、列表虚拟化（万行不卡）、markdown 选中复制（text/HTML/Markdown）、无 CJK 字体栈手工适配。
- 测试：`ui.NewTester` 可在无窗口下点击/输入/推进时间并断言（mygo 上游 34,993 行控件测试即用此机制）。

---

## 4. 视觉对照：原生默认 vs DSH 基线 vs 现实现

### 4.1 数值（raw 像素差异，容差 12/255，1600×1000）

```bash
go run ./.probe/research/native-demo/analyse   # 复现
```

| 变体 | 全屏 | 侧栏 0–240 | 主区 240–1600 | hero+composer 440–1160 |
| --- | ---: | ---: | ---: | ---: |
| 现实现 `artifacts/screens/home.png` | **1.18%** | 4.37% | 0.61% | 3.10% |
| 原生 stock | **3.27%** | 10.41% | 2.01% | 8.31% |
| 原生 + DSH 令牌（18 色 + Radius） | **3.65%** | 12.86% | 2.03% | 8.30% |

其他实测：
- **stock vs 同一布局+DSH 令牌 = 0.60%**（只改约 20 个主题字段的效果）。
- 原生 stock vs 现实现 = **3.03%**；原生+DSH 令牌 vs 现实现 = 3.41%。
- Session 小样 vs DSH session 基线 = 11.23%/11.93%（**布局故意不同：多了右栏**，只作 sanity check）。
- 现实现的 1.18% 与既有"masked 0.9%"口径不同（本文用未掩码 raw；`screens_test.go:18` 的 masked 预算为 2.0%）。

### 4.2 逐项差距（原型 vs DSH，按重要性）

| 差距 | 性质 | 能否用主题/原生补 | 说明 |
| --- | --- | --- | --- |
| **图标语言**（DSH 图标体系；ZeoKeel 为此自绘 20 个 SVG + 46 处内联） | 品牌资产 | ❌ 原生无图标库 | 原型没有图标，行高/密度因此更松；必须自备一套（Lucide/自绘 20–40 个） |
| **品牌 mark/wordmark** | 品牌资产 | ❌ | DSH 有鲸鱼 + 定制字标，原生原型只有文本 |
| **Composer 单卡构图** | 视觉 | ⚠️ 需 1 个自有组合控件 | 原生 `TextArea` 自带边框，套进卡片形成"双框"；要 DSH 的单卡需自绘输入面（约 30–60 行） |
| **chips 无框下拉**（工作区/模式/权限/模型） | 视觉 | ⚠️ 需主题/组件定制 | 原生 `Select` 是带框下拉；DSH 是无框文字+chevron。可换 `MenuButton` + 自定义样式逼近 |
| **信息密度/行高** | 视觉+产品 | ✅ 可调 | 原生默认更松（`Spacing=4`、行高 1.5）；DSH 是 Web 紧凑密度。改 `Spacing/Rem` 或组件内 padding 可控 |
| **字体/字重** | 渲染地板 | ⚠️ 部分 | 两者都用系统字体；DSH 的 Chromium 抗锯齿与 mygo 灰度 AA 差异是地板（`docs/STATUS.md` 已承认 ~85% 差异来自 AA） |
| **发送按钮/工具栏形状** | 视觉 | ✅ | 原生 `PrimaryButton("Send")` 是文字按钮，DSH 是圆形箭头按钮；用 `Icon` 包按钮即可 |
| **右侧栏按钮/底部状态栏** | 细节 | ✅ | 原型已放 Toolbar 按钮与状态行 |

### 4.3 关键判断：raw 像素这个数字会骗人

- 现实现 1.18%、原生 3.27%——只看数字会以为原生"只差 2 个百分点、可以接受"。但 `compare-home-4up.png` 一眼就能看出是**两个不同产品**：侧栏结构、图标、控件形态、密度全不同。原因是画面大部分是白底（主区 2.01% 的差异里绝大多数像素根本没变）。
- 反方向也成立：**约 20 个主题字段只改动 0.60% 像素**。颜色/圆角是"像不像"里最便宜的部分；真正昂贵的是**布局几何、控件形态、字体与图标**——正是 1:1 项目 16.3k 行 shell 代码花的地方。
- 因此：`masked pixel diff 0.9%` 这类指标只适合衡量"同一设计的微调"，**不能用来衡量"换成原生设计要付多少代价"**；决策必须同时看截图与代码量。

### 4.4 哪些差距是"功能/产品需要"，哪些是"纯视觉对齐需要"

| 类别 | 例子 | 结论 |
| --- | --- | --- |
| **产品必须有**（任何方案都要做） | 图标/隐喻（发送、附件、权限、工作区、状态点）、品牌 mark、输入法与多语言、状态色语义（成功/失败/进行中）、工具卡与消息流的层级 | 原生方案也要写，但可复用系统/第三方图标与原生控件；**不算"省"的部分** |
| **原生本就更好、只是需要品牌化** | 焦点/键盘/无障碍、暗色、DPI、控件一致行为、下拉/弹窗、跨平台一致 | 品牌层（主题 30 行 + 图标 + 2–3 个自有组件）即可，**是推荐路径** |
| **纯像素对齐成本** | 0.5px 外环、7px/31px 卡内边距、909/877/52/42/28 等魔法几何（`home.go:20-35`）、深色专用覆盖色（`home.go:48-53`）、逐屏 masked 预算、像素回归测试 14.5k 行中的 5.4k | **这是要省掉的部分**；换成原生后不再存在 |

---

## 5. 代码量对照与外推

### 5.1 单屏 Home：现实现 vs 原型

| 项 | 现实现 | 原生原型 | 倍率 |
| --- | ---: | ---: | --- |
| Home 屏独占代码 | `home.go 525`（视图构建 322 + 业务逻辑 90 + 顶层声明/SVG 113）+ `home_live_menus.go 612` = **1,137 行** | 原型 Home 路径 ≈ **100 行**（view 19 + sidebar 24 + home 19 + composer 18 + dshTheme 17） | **11×** |
| 加上省略功能的折算（附件 chip、`/` 命令、`@` 提及、工作区/权限/模型 picker、i18n、a11y 标签、真实数据接线；按 2–3× 折算） | — | **200–300 行** | 4–6× |
| 共享依赖 | `design/` 752 + `icons/` 81(+8.2KB SVG) + i18n 589（全屏摊分） | `ui.Theme` 0 行 + 图标资产（仍需） | — |

> 结论：**单屏视图代码省 70%–85%**；但"共享层 + 图标 + 文案"不会消失。

### 5.2 五屏外推（方法、前提、区间）

对 `internal/shell` 生产 16,288 行做函数级拆分（脚本：按 `func` 签名是否含 `*ui.Context` 区分视图构建函数；见附录命令）：

| 组成（实测） | 行数 | 原生重写后保留比例（假设） | 说明 |
| --- | ---: | ---: | --- |
| 视图构建函数（返回/操作 ui.Element） | 7,723 | **35%–50%** | Home 实测 1,137→200~300（保留 18–26%，但原型功能不全，故放宽）；其余屏原生控件替代更多 |
| 非视图函数（数据/控制/搜索/状态/i18n 查询） | 4,218 | 80%–90% | 业务逻辑必须保留并迁移 |
| 顶层声明（类型/常量/颜色/SVG/文案表） | 4,347 | 70%–85% | 颜色/魔法几何消失；类型与文案表保留 |
| `design/`（含 tokens/style/components/motion） | 752 | →≈30 | 原型 `dshTheme` 17 行 + 主题映射 |
| `icons/`（含 20 SVG） | 81 | 100% | 原生无图标库，必须保留 |
| **合计（UI 生产）** | **17,121** | **9,231–11,464** | **省 5,657–7,890 行 = 33%–46%** |
| （可选）`pixeltest` 对齐工具 | +140 | 0 | 退役 |

- 若把 `pixeltest` 也算入：17,261 → 9,231–11,464，省 **5,797–8,030 行（34%–47%）**。
- 与 01 报告（code-metrics）自下而上的 5,150–6,900 行**收敛**；对外建议引用 **5.2k–7.9k 行（UI 生产 30%–46%），中位 ≈6.5k（38%）**。
- 另：01 报告测算测试侧可再减 4,300–5,100 行（像素/几何/对比度/a11y/修-验循环测试），本报告不重复计算。

**前提（必须一起读）：**
1. 不含功能删减：controller 接线、会话搜索、设置持久化、工具执行等业务逻辑全部保留（4,218 逻辑行里 80–90% 保留）。
2. 视图形状按"原生控件 + 品牌层"估算；若要求"DSH 外观 1:1"，保留比例回到 70%–90%，节省项目归零（这正是不推荐 C 的原因）。
3. i18n 350 键文案表（`i18n.go:47` 的一部分）保留；可删的是语言匹配/格式化逻辑（≈100–150 行）。
4. 图标资产（20 个 SVG + 46 处内联）不可省；若换第三方图标库，约等于美术替换，不产生"代码节省"。
5. 数据来自 2026-11-05 快照；`.probe/` 有并发队友在写，行数会继续变化。

### 5.3 增量成本（每新增一屏）

- 原生：约 **150–300 行**（原型 Home 100 行、Session 小样 55 行同量级）+ 数据接线。
- 现实现：`home.go+home_live_menus` 级屏约 **1,100–1,300 行** + 像素回归（`screens_test.go` 的 5 屏静态渲染，CI 6 次像素调用）+ 可能的修-验循环。
- 结论：**真正收益在增量**——以后每屏省 70%+，且不再背像素预算。

---

## 6. 结论与推荐

### 6.1 可行性 / 代价

| 维度 | 判断 | 依据 |
| --- | --- | --- |
| 技术可行性 | ✅ 高：294 行原型已跑通 Home + Session 小样，headless 渲染，明暗双主题 | §3 |
| 交互/无障碍 | ✅ 原生更强：焦点/键盘/a11y/动效/虚拟列表免费 | §3.3 |
| 视觉 | ⚠️ 原生默认是"另一个产品"；DSH 观感需要品牌层（图标/字标/密度/组合控件），不是 token 能买到的 | §4.2/§4.3 |
| 代码量 | ✅ 净省 5.2k–7.9k 行（UI 生产 30%–46%），单屏省 70%–85% | §5 |
| 测试/CI | ✅ 可退役像素门禁与 4.3k–5.1k 行对齐测试（01 报告）；用 `ui.NewTester` 做交互回归 | §1.4/§2.2 |
| 代价 | 一次性迁移 8–14 人日（01 报告）+ 品牌化 3–5 人日；mygo v0.7.4 仍在 0.x（API 可能变，需锁定版本） | §5.3 |
| 不推荐的做法 | ❌ "原生控件 + 少量主题定制就想复刻 DSH"——0.60% 证据表明买不到 | §4.3 |

### 6.2 推荐用法（选项 A：原生控件 + 品牌层）

1. **控件跑原生默认**：Sidebar/Select/Tabs/Collapsible/Table/List/Toolbar/Badge/Modal/Toast/markdown/editor 全部换 `ui` 原生；删掉 `internal/design` 的 style/components/motion 层与 `internal/shell/markdown.go`。
2. **品牌层只有三件事**（预计 300–600 行）：
   - `theme` 覆盖 ≈30 行（品牌色/圆角/密度，参考原型 `dshTheme`）；
   - 一套图标（20–40 个 SVG，复用现有 Lucide 资产）+ wordmark/logo；
   - 2–3 个组合控件：composer 卡、工具卡、状态药丸（用原生控件组合，不重画）。
3. **交互按桌面惯例**：键盘优先、右键菜单、拖拽分栏/reorder、`Esc` 关闭、焦点环可见（原生自带）；保留 `ReduceMotion`。
4. **测试策略**：删像素门禁；改用 `ui.NewTester` 的交互回归 + 少量关键屏快照（自回归，不与 DSH 比）。
5. **迁移顺序（低风险→高收益）**：
   a. Home 屏（原型已证明，1–2 天）；
   b. Session 消息流（markdown.View/Stream + Collapsible 工具卡 + List，2–3 天，同时删 628 行自研 markdown）；
   c. Settings/Plugins/Automation 表单（原生 Form/Field/Table，2–3 天）；
   d. 侧栏与工作区树（ui.Sidebar，2–3 天，删 1.3k 行）；
   e. 最后删 `internal/design` 与 `internal/pixeltest`、退役 `baseline/`（0.5 天）。

### 6.3 选项对照

| 选项 | 做法 | UI 代码 | 视觉 | 桌面体验 | 建议 |
| --- | --- | --- | --- | --- | --- |
| **A（推荐）** | 原生控件 + 品牌层 + 去像素门禁 | 省 5.2k–7.9k 行 | 新的 ZeoKeel 原生风（80 分起步，品牌化后 90） | 最好 | ✅ |
| **B** | 保留 DSH 视觉语言（令牌/密度），交互本地化，去掉逐像素门禁 | 省 2k–3k 行（只删对齐层） | 接近 DSH（90 分） | 好（但仍有 Web 味） | 若品牌必须像 DSH 时选 |
| **C** | 继续 1:1（现状） | 省 0，且持续付费 | 100 分像 | 一般（浏览器交互模型） | 仅"DSH 官方桌面壳"成立 |

### 6.4 回答用户原话

> "如果是用 mygo 的原生去实现，不搞这么多视觉效果，是不是可以减少很多代码工作量？"

**是，能省 5.2k–7.9k 行 UI 代码（30%–46%），而且省的是"以后每新增一屏省 70%+"的那种省**；但"减少视觉"不等于"不要视觉"——原生默认已经有一套完整视觉，产品仍要花 300–600 行做品牌层。原型证明：**不手调像素也能做出可用的 Home 屏（294 行），但做不出"DSH 的样子"；DSH 的样子正是现有 16.3k 行 shell 的主要成本。**

---

## 附录 A · 复现命令

```bash
cd /data/mygo
export PATH=/usr/local/go/bin:$PATH GOMODCACHE=/data/mygo/.cache/gomod CGO_ENABLED=0
go run ./.probe/research/native-demo              # 8 张渲染图 → .probe/research/out/
go run ./.probe/research/native-demo/analyse      # 像素对照 + 差异图/四宫格

# 原型行数（294 = 240 code + 36 comment + 18 blank）
wc -l .probe/research/native-demo/main.go

# shell 视图构建函数 vs 其他函数（7,723 / 4,218；其余 4,347 为顶层声明）
python3 - <<'PYS'
import glob
def split(path):
    lines = open(path).read().split("\n"); out = []; i = 0
    while i < len(lines):
        if not lines[i].startswith("func "): i += 1; continue
        j = i
        while "{" not in lines[j]: j += 1
        d, k = 0, j
        while k < len(lines):
            d += lines[k].count("{") - lines[k].count("}")
            if d <= 0: break
            k += 1
        out.append((k + 1 - i, "*ui.Context" in " ".join(lines[i:j + 1]))); i = k + 1
    return out
view = other = 0
for f in glob.glob("internal/shell/*.go"):
    if f.endswith("_test.go"): continue
    for n, isview in split(f):
        if isview: view += n
        else: other += n
print(view, other)  # 7723 4218
PYS

# 原生控件使用审计（§2.1）
for w in Sidebar Select Tabs Collapsible Table Tree List MenuButton Toolbar Badge Toggle Switch PrimaryButton; do
  echo -n "$w="; grep -rho "ui\.$w(" internal/shell --include='*.go' --exclude='*_test.go' | wc -l
done

# ui.* 调用分类（1,169 次；图元 841 / 图形 191 / 高级控件 134 / 其他 3）
grep -rho 'ui\.[A-Z][A-Za-z0-9]*(' internal/shell --include='*.go' --exclude='*_test.go' | sort | uniq -c | sort -rn

# design/motion.go 零引用导出（35 个导出 / 22 个零引用）
python3 - <<'PYS'
import re, glob
src = open("internal/design/motion.go").read()
names = set(re.findall(r'^(?:func|var|const)\s+([A-Z]\w*)', src, re.M))
names |= set(re.findall(r'^\t([A-Z]\w*)\s*=', src, re.M))
used = set()
for f in glob.glob("internal/**/*.go", recursive=True):
    if f.endswith("_test.go") or "/design/" in f: continue
    used |= set(re.findall(r'design\.([A-Z]\w*)', open(f).read()))
print(len(names), "exports,", len(names - used), "unused")
PYS
```

## 附录 B · 指标口径与证据等级

| 结论 | 证据等级 | 口径/限制 |
| --- | --- | --- |
| 原型 294 行、渲染可跑 | **实测** | `wc -l` + `go run`；`.probe` 点目录不进 `go build ./...` |
| 原生控件 0 使用清单、图元比例 | **实测（正则近似）** | 只统计 `internal/shell` 非测试；正则按 `ui.X(` 匹配，跨行链式调用可能低估 ±5% |
| design/motion 22/35 零引用 | **实测** | 仅查 `design.X` 形式引用；同包内引用不计 |
| 像素差异 1.18%/3.27%/0.60% | **实测** | `pixeltest.Compare`，容差 12/255，未掩码；与 `screens_test.go` 的 masked 预算不可直接比 |
| 代码量外推 5.2k–7.9k | **估算（假设已列明）** | 视图保留 35%–50%、逻辑保留 80%–90%、声明保留 70%–85%；与 01 报告交叉验证 |
| 视觉"够用/不够用" | **定性（截图判断）** | `compare-home-4up.png`、`compare-home-detail.png` 可直接复核 |
| 一次性迁移 8–14 人日 | **引 01 报告** | 未在本任务实测 |
| mygo 上游测试 34,993 行 | **实测** | `find .cache/gomod/github.com/egoist/mygo@v0.7.4/ui -maxdepth 1 -name '*_test.go' \| xargs wc -l = 34,993`（`ui` 顶层包；不含 markdown/editor 等子包） |

## 附录 C · 未做/证据不足

1. **未接真实数据**：原型的侧栏/消息是 fixture；真实接线（controller/state/i18n）的行数按 2–3× 折算，未逐一实现。
2. **未做真机验证**：IME（中日韩输入）、高 DPI 多显示器、屏幕阅读器、系统暗色切换只验证了 headless 渲染层面。
3. **未测性能**：`ui.List` 虚拟化的实际帧率、大消息流的布局耗时未测（mygo 上游有基准，但本机未跑）。
4. **mygo v0.7.4 是 0.x**：API 稳定性、后续版本升级成本未评估；建议锁定版本并提供适配层。
5. **品牌化未做**：原型刻意不画 logo/图标（避免"为了像作品而手调"），因此"品牌层 300–600 行"是估算而非实测。
