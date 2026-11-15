# 壳层 / 信息架构 / 设置与发现性升级审计（v0.2.37 · 2026-11-15）

> 范围：侧栏一级入口、工作区树与会话列表、折叠 rail、会话页头与页签、右侧检查器、设置（18 页 / 5 组 + 搜索）、命令面板、通知/toast/托盘/窗口标题、Home 与首启、a11y 命名与键盘。
> 证据：代码 `file:line` + `.cache/ux-review/shots/`（HEAD `1f66af3`，2026-11-15 04:15 生成，120 PNG，manifest `changed files: 0`）。**只读审计**：未改代码、未跑 build/test/designlint。
> 与 01 报告分工：01 收口会话/Agent 表面（transcript、审批卡、goal dock、团队浮层内部）；本报告只收口壳层与 IA，个别重叠条目引用 01 的编号而不重复展开。

---

## 1. 结论摘要

1. **能力铺开了，壳层假设还停在旧壳层**：v0.2.29–0.2.37 新增 Insights、Context tab、Goal v2、inbox、系统通知、团队/任务页头浮层，而一级导航仍是「通用循环遍历已注册 screen」（`internal/ui/sidebar/sidebar.go:418-430`），设置搜索仍是手写页面别名表（`internal/uiroot/frame.go:1117-1165`）。
2. **最伤日常路径的不是缺功能，是遮挡**：toast 视口固定在窗口右上（`internal/ui/toasts/toasts.go:66,342-349`），而新布局的右上角就是右侧检查器/设置弹窗——帧里它压住检查器文件页工具条、开始页、插件页过滤控件、设置弹窗右栏（P1-1）。
3. **发现性断在「第二跳」**：洞察/自动化/插件/团队都有可见入口，但命令面板没有一条「前往 …」命令（P2-8），设置搜索只到页面级且关键字表已漂移：搜「压缩」「价格」零命中，搜「字号」落到根本没有该控件的「外观」页（P2-1）。
4. **设置已经从卡墙收敛为平铺行，剩下的是分组与保存语义**：18 页/5 组可用，但通知/记忆/归档默认埋在折叠的「高级」，组名「外观与技能」混搭两类无关设置（P2-6）；4 个页脚保存页用 3 种动词、无改动时 primary 仍常亮（P2-7）。
5. **长时任务客户端缺平台化信号**：后台完成通知按「活动会话」而非「当前屏幕」判静默，在洞察/自动化/设置页会漏报（P1-4）；托盘写死中文、窗口标题无运行态、首启语言固定 zh（P2-5）。这些是可信度问题，不是装饰问题。

---

## 2. 当前 IA / 入口盘点

| 入口 / 页面 | 用户任务 | 现状与问题 | 证据 |
|---|---|---|---|
| 一级导航：自动化 / 洞察 / 插件 | 管理定时任务、看跨会话用量、管工具与 MCP | 三个目的地无分组标题，紧贴「新对话」下方、与工作区/会话列表同一列；「洞察」（视图）与「插件」（运行时+配置）语义不同却平级 | `p2-workspace-tree.png`；`sidebar.go:418-430`；`ScreenNavSpec` `sidebar.go:1709-1732` |
| 工作区树 + 会话列表 | 找上次会话、按工作区分组 | 可用：原生 `ui.Outline` 虚拟化、组计数、0.2.35 起行内显示运行/等待/未读；`未分组` 会无限增长，无时间分组 | `sidebar.go:1095`；`p2-sidebar-states.png`、`p2-sidebar-unread.png` |
| 折叠 rail（64 DIP） | 最大化内容区后仍要导航 | 只有：展开、新对话、三个 screen 图标、设置；**没有任何会话入口**，折叠后无法切换/回到会话，也看不到当前会话 | `p2-sidebar-collapsed.png`；`frame.go:277-302`（明确 `continue` 掉 session/home） |
| 会话页头条带（标题+工作区 chip+团队/子代理 chip+⋯） | 识别当前会话、进团队/任务 | 零值 chip 常显（DS-17 决策）；团队与任务都收进同一个 popover，关闭即消失，长任务没有持久工作台 | `p2-team-tasks.png`；`rightpanel.go:16-21`（8-pane inspector 已退役） |
| 会话页签：对话 / 轨迹 / 记忆 / 上下文 | 看过程、记忆、上下文账本 | 4 个平级页签，Context 仅后端存在时才出现；「记忆」页签与设置「记忆」页是同一数据的两个面（V22 残余） | `p2-context.png`；`session/view.go:649-668` |
| 右侧检查器：开始 / 文件 / 命令 + 每文件文档页签 | 看文件、diff、跑一次性命令 | 默认收起（持久化）；点文件/产物自动打开；三固定页+文件页签；但「开始」页只有两张卡，主体几乎空白 | `p2-guide.png`、`p2-files.png`；`rightpanel/view.go:229-282`；`uiroot.go:241-258` |
| 右侧检查器在窄窗（<1084 DIP） | 同上 | 变成**模态抽屉**：主内容 scrim 阻断，看文件时不能继续输入；1024 帧即如此 | `narrow/narrow-session-inspector.png`；`frame.go:29,707-748,1034-1057`；`design/components.go:58-60` |
| 设置：常用 / 外观与技能 / 集成 / 高级 / 关于（18 页） | 改配置 | 结构可用；通知/记忆/归档/导入/更新/诊断/LSP/数据与隐私在默认折叠的「高级」；组名「外观与技能」把外观与技能硬拼 | `p2-model-settings.png`、`p2-notifications.png`、`narrow/narrow-settings.png`；`frame.go:1084-1093,1166-1215` |
| 设置搜索 | 直接找到某个设置项 | 只匹配页面标题/分组/`settingsPageKeywords` 别名；无条目级索引、无定位高亮；别名与控件位置已漂移 | `frame.go:1099-1165`；见 P2-1 |
| 命令面板（Cmd+K） | 键盘完成一切 | 扁平列表、无分组、无别名/模糊匹配；**没有前往洞察/自动化/插件/设置页/归档的命令**；命令数超过一屏时首屏看不到选中行（P1-2） | `p2-palette.png`、`p2-palette-right-pages.png`；`frame.go:773-940` |
| 通知：应用内 toast + 系统通知 | 后台完成/审批别错过 | 两个总开关，无按事件类型/免打扰；toast 位置与新表面冲突（P1-1）；静默判定看「活动会话」而非「当前屏幕」（P1-4） | `p2-notifications.png`；`settings/notifications.go:29-31,75-135` |
| 托盘菜单 / 窗口标题 | 关窗继续运行、一眼看状态 | 托盘项写死中文（英文界面也不变），窗口标题恒为 `ZeoKeel`，无运行中/等待审批信号 | `host/tray.go:174-235`；`host/host.go:656-657` |
| Home 屏幕 / 空会话 hero | 首启与空态引导 | Home 屏幕仍注册但**没有任何入口**（死表面，代码仍在）；真正的首启在会话 hero：一个只聚焦输入框的「新对话」按钮 + 缺依赖清单，无示例 prompt | `p2-landing.png`；`home/home.go:129-183`；`session/view.go:947-985` |
| 归档 / 记忆 / 技能 | 恢复归档、改记忆 | 归档只有「设置→高级→归档」一步入口（侧栏永不显示归档，`sidebar.go:466-467`）；归档在会话 ⋯ 菜单可产生，但恢复路径不可从会话侧到达 | `p2-archive-settings.png`；`session/view.go:476-490` |

---

## 3. 升级机会（按影响排序）

### P1

#### P1-1 ［缺陷］toast 叠在新表面的正上方：检查器、设置弹窗、页面过滤控件被压
- **问题**：toast 视口是窗口级右上（`toastTopInset=84`，右对齐），而新布局右侧是检查器或设置弹窗，toast 直接盖住其头部与工具条；抽屉/弹窗打开时 toast 仍在最上层。并发通知多时（批准 + 自动化完成 + 后台 run）遮挡时间随之延长（`+N 条通知排队`）。
- **证据**：`p2-files.png`（文件页「工作区/刷新 + 过滤行」被盖）、`p2-guide.png`（开始页两张卡被盖）、`p2-plugins.png`（「仅显示已停用」被盖）、`narrow/narrow-settings.png`（设置弹窗右栏被盖）、`narrow/narrow-session-inspector.png`（抽屉整幅被两条 toast 覆盖）；`toasts.go:66,342-349`；`frame.go:977-1003`（先画设置 Modal，再画 `overlay.toasts`，故 toast 在最上）。
- **建议**：给 toast 视口加**壳层让位**：dock 态 `paddingRight = inspectorWidth + Space3`；抽屉/设置弹窗打开时把 toast 锚到内容列右缘（或降到弹窗之下，让 `RoleStatus` 仍在但不遮内容）。实现不引依赖：host 在 `overlays()` 计算占用宽度，通过既有 `SlotOverlayToasts` 的构建上下文传一个只读 inset（或 host 侧在 overlay slot 外包一层带 padding 的 Row）。
- **改动面**：`internal/uiroot/frame.go`（overlays/toast inset 计算 + 新增 host→toast 的小契约或既有 runtime 方法）、`internal/ui/toasts/toasts.go`（接收 inset）。**验证**：帧测试断言 toast rect 与 inspector 页头/设置弹窗右栏不相交（1024×720 + 1512×944 两档）；快照 `p2-files` / `narrow/narrow-settings` / `narrow/narrow-session-inspector` 复核。**风险**：toast 仍须指向状态栏之外且不盖 composer；**工作量 M**。

#### P1-2 ［缺陷］命令面板一屏装不下时，首屏看不到任何选中行（SH-02 类回归）
- **问题**：产品当前启用命令约 16 条，面板列表 `MaxHeight(320)` ≈ 9 行；帧显示列表停在**末尾**（首行是「帮助」，`设置/新会话/权限模式/附加文件/压缩上下文` 均不可见），且整个列表区域**没有 accent 选中行像素**（0 个 `0D44F6`），即 Enter 会运行一个看不见的「设置」。现有回归测试只覆盖 ≤5 条命令，未覆盖超一屏。
- **证据**：`p2-palette.png`（列表停在末尾、无选中行；对面板区域取色直方图无 accent）；`frame.go:871,918`（`ui.List` + `MaxHeight(320)`）、`frame.go:855-895`（`Choose(0)` 只写索引、不滚动）；`uiroot/fix_round_test.go:61-180`（测试命令数 5）；`mygo ui/list.go` 首帧以 `s.anchor`/`inset` 布局，新元素复用 `ListState` 锚点。
- **建议**：打开时显式 `paletteList.ScrollTo(0, ui.Start)`（框架已有 `ListState.ScrollTo/ScrollIntoView`），`movePaletteSelection` 同步 `ScrollIntoView`；顺带把「最近使用」提到顶部（可选，仍需显式滚动）。补一条 16 条命令的帧测试：断言选中行在视口内（像素 + `Visible()`）。
- **改动面**：`internal/uiroot/frame.go`（2 处）、`internal/uiroot/fix_round_test.go`（新增长列表用例）。**验证**：新帧测试 + `p2-palette.png` 复核。**风险**：不与 List 的 Key 记忆/`Choose` 语义打架，需在 `Choose(0)` 之后调用；**工作量 S**。
- 说明：本条为单帧观察（快照状态），机制（复用 `ListState` 的 anchor/首帧 0 行布局）需 Lead 用实机/测试复现确认；无论机制如何，「首屏必须能看到选中行」是硬要求。

#### P1-3 ［缺陷/升级边界］窄窗（<1084 DIP）检查器是模态抽屉：看文件时不能继续对话
- **问题**：dock 阈值 = `SidebarWidth(280)+ContentMinWidth(520)+Space6(24)+InspectorMinWidth(260)=1084 DIP`，在 host 常用窗 900–1024 区间就会退化成带 scrim 的模态抽屉。1024 帧里抽屉打开时主内容不可交互——而「边看文件边追问」正是检查器的核心用法。
- **证据**：`narrow/narrow-session-inspector.png`（1024×720 抽屉 + scrim）；`frame.go:29,707-748`（抽屉是 `Modal` scrim + `PressedOutside` 关闭）；`frame.go:1034-1057`；`design/components.go:58-60`；`host/host.go:67-68`（最小窗 900×600）。
- **建议**：(a) 把 `ContentMinWidth` 520→420（阈值降到 984，1024 可 dock；900 折叠 rail 已可 dock：64+520+24+260=868）；(b) 或给抽屉去 scrim、改为「浮层可关闭且不阻断主内容」，并在抽屉头部加「钉住为侧栏」（仅当宽度够）。(a) 改动最小，但需同时检查 900 宽下 composer/状态条的窄态回落（已有 1024 证据，需补 900×600 帧）。
- **改动面**：`internal/design/components.go`（一个常量）、`frame.go`（如需第二形态）、`narrow` 快照步骤。**验证**：帧测试断言 1024×720 打开检查器后 composer 的 `发送` 仍可点击（`tester.Click`）；900×600 与 1024×720 快照。**风险**：内容列变窄会影响 transcript 行宽与工具卡折叠（需一并看暗色/英文帧）；**工作量 S/M**。

#### P1-4 ［缺陷］后台完成通知按「活动会话」判静默，在非会话屏幕会漏报
- **问题**：完成/失败通知的静默条件是 `agent.active == sessionID`（聊天后端的活动会话），与「用户此刻在看哪个屏幕」无关。用户在洞察/自动化/插件/设置页时，运行中的那个会话其实不可见，但通知被当作「你正在看」而不发——长任务最需要通知的时刻恰好静默。
- **证据**：`internal/modules/agent/run_notify.go:24-36`（`if active == sessionID { return }`）；`contracts/notify.go:104-140`；`frame.go:153`（host 侧已用 `nav.Current()` 决定侧栏选中，却没有把「当前是否在看该会话」暴露给通知链路）。
- **建议**：把静默判定换成「前台会话」：新增一个只读小契约（如 `contracts.ForegroundSession`，由 uiroot 提供 `ForegroundSession() (id string, ok bool)`，`nav.Current()==session` 时返回 chat.ActiveSession），agent 侧改为 `if fg, ok := ...; ok && fg == sessionID { return }`；无该服务（headless）时保持现有行为。
- **改动面**：`internal/contracts`（新可选服务）、`internal/uiroot`（提供实现）、`internal/modules/agent/run_notify.go`。**验证**：agent 单测 + uiroot 帧测试（在 insights 屏完成一次后台 run → 有通知；在 session 屏同一会话完成 → 无通知）。**风险**：契约新增要同步 ARCHITECTURE；headless 无影响；**工作量 S/M**。

### P2

#### P2-1 ［升级+缺陷］设置搜索只到页面级，且关键字表已漂移（新设置搜不到、旧别名指错页）
- **问题**：搜索只过滤左侧页面列表，不在页内定位/高亮；`settingsPageKeywords` 是 host 手写别名表，新设置落地后没人补：搜「压缩」/「价格」（0.2.36 的上下文预算与模型价格）**零命中**；搜「字号」命中「外观」，但外观页没有字号控件（真正的「消息字号」在「行为」页）。
- **证据**：`frame.go:1099-1165`；`settings/settings.go:1375-1387`（上下文预算/自动压缩）、`modelcatalog_editor.go:229-236`（输入/缓存/输出价格）、`behavior.go:298-320`（消息字号）vs `frame.go:1126-1132`（外观别名含「字号/text size」）；别名表无「压缩/价格/成本」。
- **建议**：分两步。① 修漂移：别名表移出 host 到每页 spec（`contracts.SettingsPageSpec.Keywords []string`），由页面模块自己声明（模块最清楚自己的控件）；② 条目级索引：每页关键控件给稳定 `c.Key` 锚点（如 `setting/model/context-budget`），host 索引「页面 + 锚点 + 标签/别名」，命中后 `OpenSettingsPage` + `ScrollIntoView(anchor)` + 短暂 `BgSelected` 高亮；折叠的「高级选项」在命中时自动展开。先做①（S），再做②（M）。
- **改动面**：`internal/contracts`（spec 字段）、`internal/uiroot/frame.go`（索引/跳转/高亮）、18 个页面文件（关键字 + 锚点）。**验证**：扩展 `uiroot.TestSettingsSearchMatchesSettingNames`（压缩→模型页并滚到上下文预算；价格→模型页；字号→行为页）；帧测试断言命中控件 rect 在视口内。**风险**：18 页逐个加锚点，需与 03/其他工作流分文件；**工作量 M/L**。

#### P2-2 ［升级］一级导航语义混列：视图（洞察）与运行时/配置（插件）平级且无分组标题
- **问题**：一级列从上到下是「新对话 → 自动化 → 洞察 → 插件 → 工作区 → 会话 → 设置」，没有分组；「插件」本质是运行时状态 + 工具开关（MCP 配置在设置里，二者已有双向跳转），却占据与「洞察」同等的一级位置；新增第 4 个能力时只能继续向下堆。
- **证据**：`p2-workspace-tree.png`；`sidebar.go:418-430`（通用循环，无分组）；`plugins/plugins.go:161`、`plugins/view.go:118-149`（MCP 状态 + 打开设置 MCP）、`settings/mcp.go:310`（设置→插件）；`sidebar.go:1709-1732`。
- **建议**：给一级导航加**两个静默分组标题**（`工作`：自动化/洞察；`工具与集成`：插件），复用 `SectionLabel`；或把「插件」降为设置页并在设置里保留「打开插件」——需产品选。**不加新入口**，只做分组语义。
- **改动面**：`internal/ui/sidebar/sidebar.go`（分组渲染，复用 `buildWorkspaceSectionHeader` 的样式思路）。**验证**：帧测试/快照 `p2-workspace-tree`；英文/暗色各一帧。**风险**：分组标题占高度（会话列表可见性），900×600 需复核；**工作量 S**。

#### P2-3 ［升级］折叠 rail 没有会话入口
- **问题**：rail 只放 screen 目的地 + 新对话 + 设置，**完全没有会话/工作区入口**；用户折叠侧栏（或窄窗）后无法切回另一个会话，也看不到当前会话状态。代码注释把它当「rail 只导航 screen」的既定选择，但对「会话是一等对象」的桌面客户端是硬缺口。
- **证据**：`p2-sidebar-collapsed.png`；`frame.go:277-302`。
- **建议**：rail 增一个「会话」图标（`icons.Message()`），点击开**原生 Popover**：最近会话 8 条（复用会话行的状态渲染 + 顶部搜索框），底部「展开侧栏」（`SetSidebarCollapsed(false)`）。不新增页面/不新设计语言。
- **改动面**：`frame.go`（rail + popover，可复用 `ui.PopoverMenu`/`ui.List`）、会话行标签函数复用（跨模块需走既有 slot 或 host 侧简化行）。**验证**：帧测试（折叠态点「会话」→ popover 列出会话 → 选择后 `nav.Current()=="session"`）；`p2-sidebar-collapsed` 快照。**风险**：host 不能 import UI 模块实现（AGENTS 模块边界），会话行需在 host 侧做轻量版（标题+状态点）或在 sidebar 模块补一个 rail popover slot；**工作量 M**。

#### P2-4 ［升级］通知只有两个总开关，缺事件类型与免打扰
- **问题**：事件源已从 1 类涨到 5 类（后台 run 完成/失败、审批等待、自动化结算、团队完成），但通知页只有「应用内气泡」「系统通知」两个布尔；无法「关掉成功的 run、只留失败与审批」，也没有「仅窗口失焦时」「免打扰」。长任务多开时打断成本上升。
- **证据**：`p2-notifications.png`；`settings/notifications.go:75-135`；事件源：`agent/run_notify.go`、`contracts/notify.go:122-130`（审批）、automation 的 `NotifyRunFinished`、团队完成通知。
- **建议**：通知页加「事件类型」分组：完成 / 失败 / 审批等待 / 自动化 / 团队（`Switch` 每项，默认完成关？需产品定）；加「仅应用在后台时」+ 简单免打扰（时间段）。全部即时生效（当前页面已是即时保存语义）。存储沿用 `ui.notifications.*` 命名空间。
- **改动面**：`settings/notifications.go`、`contracts`（键常量）、`notifier`/toast 过滤读取、各事件源注释。**验证**：帧测试（关失败类 → 失败通知不弹，审批仍弹）；页面快照（空/非空/不支持系统通知三态）。**风险**：默认值会改变行为，需产品确认；**工作量 M**。

#### P2-5 ［缺陷合集］平台化信号：托盘写死中文、窗口标题无状态、首启语言固定 zh
- **问题**：① 托盘菜单项是字面中文（`显示 ZeoKeel/新建会话/设置/退出`），切英文后不变；② 窗口标题恒为产品名，最小化后无从判断「是否有 run 在跑/等审批」；③ 首启语言 `settings.Get("ui.language", "zh")`，英文系统用户第一屏是中文（之后再手动切）。
- **证据**：`host/tray.go:174-235`（字面量）；`host/host.go:656-657`（`Title: appName`，全仓无 `SetTitle` 调用）；`host/host.go:434`（默认 zh）；框架已具备能力：`mygo/ui/window.go:1088`（`Window.SetTitle` 可用）。
- **建议**：① 托盘菜单按 `uiRoot.Locale().Language()` 取文案并在 `OnLanguageChange` 重建菜单（host 已有回调）；② 标题加最小状态后缀：`ZeoKeel` / `ZeoKeel · 运行中 N` / `ZeoKeel · 等待审批`（只在状态变化时设，注意托盘/窗口关闭路径）；③ 首启读系统 locale（`LANG`/`LC_ALL`）映射 zh/en，选择结果落库，仅首次。
- **改动面**：`internal/host/tray.go`、`internal/host/host.go`（标题更新 + 语言初始值）、新增 `systemLocale` 小函数。**验证**：host 单测（语言映射、标题文案）；人工 tray 检查（Xvfb 无 tray，需真机/记录限制）。**风险**：托盘重建要避免重复项；标题更新频率（每帧一次会有闪烁，需仅在值变化时设）；**工作量 S/M**。

#### P2-6 ［升级］设置分组把「通知/记忆/归档」埋进默认折叠的「高级」，组名混搭，滚动后组标题被裁切
- **问题**：三个日常会用的页面在 `高级`（默认折叠，搜索/deep link 才展开），用户要「改通知」「恢复归档」「改记忆」需先展开高级；「外观与技能」把两种无关设置拼在一起；导航滚动后第一个组标题被搜索框下半截裁掉（`p2-notifications.png`、`p2-data-privacy.png` 均可见「常用」半行），narrow 720 高时「诊断/更新/记忆/LSP/数据与隐私」落在折叠线下。
- **证据**：`settings/notifications.go:31`、`settings/archive.go:29`、`settings/memory.go:31`（`Section = "高级"`）；`frame.go:1084-1093`（组排名）、`:1166-1190`（组名）、`:1283-1288`（滚动边界 hairline 但组标题仍被裁）；`p2-notifications.png`、`p2-data-privacy.png`、`narrow/narrow-settings.png`。
- **建议**：① 重新分组（页数不变、只改 Section 与顺序）：`常用`（模型/行为/预设）、`外观与快捷键`（外观/快捷键）、`能力与集成`（MCP/搜索/网络/技能/LSP）、`数据与通知`（通知/记忆/归档/数据与隐私/导入/更新）、`关于`；「高级」概念取消或只留诊断。② 组标题 sticky（框架 `ListState.Header` 能力已在用），或滚动时给组标题加 `BgRaised` 实心底。③ deep link 目标页可用（已有 `OpenSettingsPage`），保证改分组不破坏命令/深链。
- **改动面**：`internal/ui/settings/*`（Section 常量与 Order）、`frame.go`（rank/组标题渲染）。**验证**：帧测试（页面顺序/深链仍工作/高级项可见）；快照 `p2-notifications`、`narrow/narrow-settings`。**风险**：改 Section 会影响 `settingsPageKeywords` 与测试断言，需全量同步；**工作量 S/M**。

#### P2-7 ［缺陷］保存语义三种动词 + 页脚 primary 无改动也常亮
- **问题**：四个页脚保存页用「保存提示词与运行参数」「应用」「保存」三种动词；更多页是即时保存（无页脚）。页脚 primary 无条件 enabled——打开模型页什么都不改也亮着一个主按钮，反而让「什么时候该点」变模糊；页头「未保存」徽标是唯一信号。
- **证据**：`p2-model-settings.png`（页脚「保存提示词与运行参数」）、`p2-appearance.png`（页脚「应用」）、`settings/settings.go:671-684`（默认文案）、`:697-705`（模型页脚）、`:746-754`（外观页脚）、`:793-800`/`:901-908`（搜索/网络「保存」）；`uikit.DialogAction` 已有 `Disabled` 字段（`uikit.go:163`）但页脚未使用。
- **建议**：① 统一动词为「保存」（模型页可保留「保存提示词与运行参数」的差异，但其它页统一）；② 页脚 primary 用 `Disabled: !saveDirty()`——注意模型页 `Dirty` 含目录草稿（即时生效项），需新增一个「仅页脚字段是否有改动」的信号，避免误灰；③ 页脚左侧加一行「本页改动需保存；上方标（即时生效）的项目会立即保存」的静默说明（复用 `TypeXXS`）。
- **改动面**：`internal/ui/settings/settings.go`（footer 构造 + 新 dirty 派生）、各页 draft 状态。**验证**：帧测试（未改动时按钮 disabled；改动后 enabled；点后 dirty 清空）；快照 4 个页脚页。**风险**：模型页目录是即时生效，灰按钮逻辑必须只覆盖页脚字段；**工作量 S**。

#### P2-8 ［升级］命令面板覆盖缺口：没有「前往」命令、无别名/分组
- **问题**：全仓命令约 22 条（session 14 + host 5 + 右栏 2 + home 1），**没有任何一条导航到自动化/洞察/插件**，也没有「打开设置→某页」（只有「软件更新」一条深链）；搜索仅对 `Title+ID+Hint` 做 `strings.Contains`，中文标题的英文别名（theme/shortcut）与「打开归档」都无命中；列表无分组（导航/会话/设置/工具混在一起）。
- **证据**：`uiroot.go:373-414`（host 命令）；`session/session.go:731-934`（无 goto）；`rightpanel.go:457-475`；`frame.go:824-840`（matches 匹配逻辑）；`p2-palette.png`。
- **建议**：(a) 三个 screen 各注册一条「前往 X」命令（Order 放 15–25，与主导航一致；宿主可统一扫 `ScreenLister` 生成，避免每模块重复）；(b) 生成「设置：<页名>」一组命令（18 条，`OpenSettingsPage` 已具备）；(c) 给 `CommandSpec` 加可选 `Keywords []string` + `Category`，面板按 category 分组（或至少按 Order 段映射：0-100 操作、900-999 界面、1000+ 设置），并在标题右侧显示快捷键（已有）。
- **改动面**：`internal/contracts`（CommandSpec 两字段可选）、`internal/uiroot/frame.go`（生成 goto/settings 命令 + 分组渲染）、`session`/`sidebar` 命令注册。**验证**：帧测试（Cmd+K 输「洞察」回车 → `nav.Current()=="insights"`；输「通知」→ 设置通知页并定位；分组标题渲染）；`p2-palette*` 快照 + 英文帧。**风险**：命令从 22 条涨到 60+，必须同步 P1-2 的长列表滚动/分组，否则面板更难用；**工作量 M**。

#### P2-9 ［升级］非会话屏幕上「双选中」，且没有返回路径
- **问题**：在洞察/自动化/插件页时，侧栏同时高亮「洞察」行与「活动会话」行（都是 `BgSelected`+2px accent 左条），当前上下文有两个；从洞察排行点进会话后，没有任何 back/面包屑回洞察（`nav.Back()` 有实现但无 UI，注释明确「Navigation has no back control」）。
- **证据**：`p2-insights.png`（洞察行 + 未命名会话行同时选中）；`sidebar.go:1042,1104`（活动会话行独立于当前 screen 上色）；`navigator.go:48-68`（Back/CanGoBack 无调用方）；`frame.go:562-565`（注释：无 back 控制、无 Alt+←）。
- **建议**：① `nav.Current()!=session` 时把活动会话行降为「弱选中」（只留 2px 左条或改为 hover 色），保证一屏一个当前项；② 给「从某页跳入会话」加轻量返回路径（推荐面包屑，因为桌面用户会按 Alt+←）：在会话页头标题左侧放一个 `‹ 洞察` 次级按钮，仅当导航栈上一项不是 session 时出现，点击走 `nav.Back()`；洞察排行行本身保持普通行内点击，不引入新页签模型。
- **改动面**：`sidebar/sidebar.go`（选中态条件）、`session` 页头 or `uiroot` band（back 按钮 + 导航栈）。**验证**：帧测试（洞察→点会话→出现「‹ 洞察」→ 点击回洞察；会话屏幕内不出现）；快照 `p2-insights` + 新 back 帧。**风险**：返回按钮不能与 Esc/关闭语义冲突；**工作量 S/M**。

#### P2-10 ［升级］空会话 hero 的 CTA 名不副实，且没有起步示例
- **问题**：hero 主按钮写「新对话」，但在已打开的空会话里点击只会 `focusComposer`（标签承诺新建、行为是聚焦，若输入框已聚焦则毫无反馈）；空白区没有示例 prompt/能力提示，新用户面对「开始新对话 + 一条红色缺失依赖」不知道能做什么。
- **证据**：`p2-landing.png`；`session/view.go:947-985`（`hasSession` 分支只设 `focusComposer`）。
- **建议**：① 有会话时按钮改为「开始输入」并直接聚焦（或去掉按钮，只留文案），真正新建留给侧栏「新对话」；② 在缺失依赖清单下加 2–3 个示例 prompt 按钮（`SecondaryButton`，点击填入 composer 不自动发送），文案围绕长时任务能力（如「帮我整理这个工作区的发布清单」）——复用现有组件与 token，不加新依赖。
- **改动面**：`internal/ui/session/view.go`（hero）。**验证**：帧测试（空会话点示例 → composer 文本就位且未发送）；`p2-landing` 快照。**风险**：示例文案需产品确认；**工作量 S**。

#### P2-11 ［升级］团队/任务只有页头浮层，没有持久工作台
- **问题**：0.2.32 把 8-pane 检查器删除后，团队与任务只剩页头 chip 的 popover；关闭即消失，长任务里无法「一直看着任务板/成员活动」。INTERACTION-DEPTH §5 已把「团队工作台 overlay」列为开放项，但之后几轮没做；现在的壳层也没有它的落点（右栏固定页只有开始/文件/命令）。
- **证据**：`p2-team-tasks.png`（popover）；`rightpanel.go:16-21`（8-pane 已退役说明）；`docs/INTERACTION-DEPTH-ROUND.md` §5 第 1 条；`session/agentsurface.go`（chip 浮层）。
- **建议**：两个不新增一级导航的落点：① 右栏加第 4 个固定页「团队」（仅会话有团队/任务后端时出现），复用现有成员行/任务行 build；② 或把页头 chip popover 升级为可缩放的 `Modal` 工作台（980×740 原型已有）。优先 ①，因为它复用「检查器就是会话的一个面」的 IA，且不增加一级入口。与 01 报告 P1-2（成员控制）同批做。
- **改动面**：`internal/ui/rightpanel`（新固定页 + 命令/快捷键）、`internal/ui/session/agentsurface.go`（把行构建抽成可复用，注意模块边界：右栏不能 import session 实现，需在 contracts/uikit 抽共享行或由 session 注册 slot 到右栏页面）。**验证**：帧测试（有团队时右栏出现「团队」页、空态解释、任务分组）；快照。**风险**：跨模块共享行有边界成本，可能需先在 `uikit` 抽 `AgentMemberRow`/`TaskRow`；**工作量 M/L**。

### P3

#### P3-1 ［缺陷］设置表面是无名对话框、设置导航是无名列表；主侧栏却有名字
- **问题**：设置是 `RoleDialog` 但没有可访问名（V26 去掉标题后只剩关闭按钮 + 页内 H1）；设置导航 `ui.Sidebar` 没有 `Label`（主侧栏有「主导航」）。屏幕阅读器读到「dialog」「列表」，不知道是什么。
- **证据**：`frame.go:980`（`ui.Modal` 返回值被忽略，未 `.Label`）、`:1290`（`ui.Sidebar` 无 `.Label`）；对照 `frame.go:159-164`（主侧栏 `Label("侧栏")`）、`:721`（抽屉 `RoleDialog().Label("检查器")`）；框架 `mygo/ui/base.go:705`。
- **建议**：`ui.Modal(...)` 返回值接 `.Role(ui.RoleDialog).Label(r.tr("设置","Settings"))`；设置导航 `ui.Sidebar(...).Label(r.tr("设置导航","Settings navigation"))`。
- **改动面**：`internal/uiroot/frame.go`（2 行 + 文案）。**验证**：a11y 帧测试（`AccessByRole(RoleDialog, "设置")`）；**风险**：低；**工作量 S**。

#### P3-2 ［升级］侧栏行的动作只在 hover/focus 出现，工作区菜单没有重命名
- **问题**：会话行 ⋯、工作区行 ⋯/+ 都是 `Opacity(0)` + hover/focus 才出现（可发现性成本）；工作区菜单只有「打开工作区/删除工作区」，没有重命名（会话可重命名）——按路径添加的工作区名字改了目录就乱了。
- **证据**：`sidebar.go:1393,2034`（`Opacity(0).IgnoreInput(true)`）、`:1399-1422`（workspaceGroupMenu 仅 2 项）；`p2-workspace-tree.png`。
- **建议**：工作区菜单加「重命名」（原生 Modal，同会话重命名；需要 workspace 后端有 rename/display name 能力则先补后端，否则从「显示名称」设置项走）；行内动作保持 hover 模式（桌面惯例，不改为常显）。
- **改动面**：`internal/ui/sidebar/workspace_*.go`、可能 `internal/modules/workspace`。**验证**：帧测试（菜单项、重命名后行标题更新）；快照。**风险**：后端能力缺失时先做前端不可行，需确认；**工作量 S/M**。

#### P3-3 ［升级］插件页 73 个工具开关是一堵长墙：无组计数/批量
- **问题**：73 个工具按组平铺（文件 6、终端与任务 5、…），只有总数与「仅显示已停用」过滤；没有组计数、没有「整组启用/停用」，长列表靠滚动。
- **证据**：`p2-plugins.png`（73 个工具）；`plugins/view.go:118+`；`p2-plugins-search.png`。
- **建议**：组标题后缀计数（`文件 6/73`），组头加 `SectionAction`「全部启用/停用」（走既有批量写路径），保留过滤框。**不改一行为卡**。
- **改动面**：`internal/ui/plugins/view.go`。**验证**：帧测试（组计数正确、批量切换写回）；快照。**风险**：批量停用是破坏性操作，需确认；**工作量 S**。

#### P3-4 ［升级］归档只有「设置→高级→归档」一条恢复路径
- **问题**：会话可从 ⋯ 菜单归档（有撤销 toast），但「找回归档会话」只能进设置→高级→归档；侧栏永不显示归档（产品决策），命令面板也没有「打开归档」。
- **证据**：`session/view.go:476-490`（归档 + 撤销）；`sidebar.go:466-467`（侧栏排除归档）；`settings/archive.go:26-31`；`p2-archive-settings.png`。
- **建议**：命令面板加「打开归档」深链（与 P2-8 的 settings 命令一并生成即可），归档页空态已有教学文案；无需新增侧栏入口。**改动面**：随 P2-8。**验证**：随 P2-8 帧测试；**风险**：低；**工作量 S**。

#### P3-5 ［升级］Home 屏幕不可达但整套首启代码仍在（双落地页漂移风险）
- **问题**：`home` screen 仍注册（setup card、最近会话、下一步、home composer），但侧栏/rail/命令都不提供入口，产品实际落地页是会话 hero。两套首启文案/能力已开始分叉（例如最近会话排序与非空态只在 Home 有），后续改动容易只改一处。
- **证据**：`home/home.go:129-183`（注册）；`sidebar.go:404-407`（注释：Home 不是目的地）；`p2-landing.png`（真正落地页）；host 侧无任何 `Push("home")`。
- **建议**：与产品确认：要么给 Home 一个低权重入口（如侧栏品牌行点击/命令「打开首页」），要么删除 screen 只保留 host 级组件；当前「注册但不可达」是最差状态（测试/文档都以为它在用）。**不做**新增一级入口。
- **改动面**：`internal/ui/home`、`uiroot`/组装、快照步骤。**验证**：删/留后帧测试与快照；**风险**：可能被 headless/embedder 使用，需先 grep 契约；**工作量 S**。

#### P3-6 ［缺陷］快捷键页「全部快捷键」有重复行，且与上方可编辑表重叠
- **问题**：「全部快捷键」把 7 条手写行与所有注册命令（带 Shortcut 字段）拼接，`切换右侧栏 Cmd/Ctrl+Alt+I` 因此在同一张表出现两次；「新会话/设置」也不同程度与上方「桌面快捷键」编辑表重复。
- **证据**：`p2-shortcuts.png`（切换右侧栏出现两次）；`settings/settings.go:89-123`（手写行 + 动态命令，无去重）；`:95` 的「切换右侧栏」与 `uiroot.go:379` 的 `Shortcut: "Cmd/Ctrl + Alt + I"` 重复。
- **建议**：参考表按「文案+chord」去重；可编辑的两项在参考表中只保留一处（标注「可自定义」）。**改动面**：`settings/settings.go`（去重）。**验证**：帧测试断言无重复文案；快照；**工作量 S**。

---

## 4. 不建议做（anti-scope）

1. **不把团队/任务/队列/洞察升为第 4、5 个一级导航入口**：团队与任务属于会话上下文（页头 chip + 右栏第 4 页即可），新一级入口会稀释「工作区/会话」这个真实主队列。
2. **不把 Home 重新做成导航目的地或加首启引导页**：首启价值路径已在会话 hero；两个落地页只会漂移（见 P3-5，方向是收敛而不是扩张）。
3. **不给洞察加图表/卡片墙/更多 KPI**：现在 4 个平铺 KPI + `ui.Table` 已符合 DESIGN §3；加图表需引依赖，且违反自绘白名单。
4. **不做设置首页 dashboard、不增加第三级设置层级**：18 页/2 级分组足够，问题在分组语义与搜索，不在页面数量。
5. **不给检查器加第 4、5 个固定页来「显得完整」**：开始页本来只该有两张卡（保留空/极少内容是正确的）；团队页是功能需要，不是装饰。
6. **不做「全局搜索一切」的新搜索层**：先把命令面板覆盖（P2-8）与设置条目索引（P2-1）补齐；再考虑统一搜索，否则只是另一个找不到东西的框。
7. **不删状态条/团队/子代理常显入口、不因「减法」隐藏通知或审批入口**（AGENTS §17 明确禁止）。
8. **不引第三方 UI/图标/图表/命令行组件**：上述全部可用 mygo 原生控件 + 现有 uikit/token 完成。

---

## 5. 局限

1. **只读审计**：未改代码、未跑 `go build/test/vet/designlint`，本文不构成验证通过；所有「建议」需 Lead 侧实现并验证。
2. **快照环境**：120 张 PNG 由 `ui-snapshot` 在 HEAD `1f66af3` 生成（Xvfb、注入帧、mock 模型/会话数据）；OS 系统通知在快照环境不支持（通知页开关为禁用态），托盘在 Xvfb 不可用——托盘/窗口标题结论来自代码而非帧。
3. **P1-2 的机制未实机复现**：帧证据是「列表停末尾 + 无可视选中行」的可观察事实；怀疑与复用 `ListState` 锚点/首帧 0 行布局有关，需 Lead 用 16 条命令的帧测试确认后再定修法（修法本身不依赖机制）。
4. **无 900×600 帧**（与 01 报告同一缺口）：host 最小窗 900×600 的布局结论（设置 nav 溢出、抽屉阈值）部分来自 `settingsPanelSize`/阈值常量推算，补帧后才能定稿。
5. **未覆盖**：HiDPI/真机字体、触控/触控板手势、OS 通知点击回跳、headless/ACP 产品壳、托盘重建的竞态。
6. **跨模块建议有边界成本**：P2-3/P2-11 涉及 host 与会话/右栏模块的行复用，受 AGENTS 模块边界约束，实际落点可能需先在 `uikit` 抽共享行，工作量按上限估。
7. **与 01/03 的边界**：审批卡高度、goal dock 零值入口、队友停止、页内 FindBar 等已在 01；系统质量/性能/安全不在本文范围。
